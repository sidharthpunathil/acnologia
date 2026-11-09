package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Capabilities: one job, many providers (see store.Capability). An app calls
// /proxy/{capability} with the capability's params, as query parameters or a
// JSON object body. Acnologia orders the routes by the strategy, fills each
// route's request from the params, runs it through the proxy of that
// service (so its keys rotate as usual), and moves on to the next route when
// the provider cannot serve it. Params starting with "_" steer the call:
// _raw=1 returns the provider's body unmapped, _service=<slug> uses only
// that route.

const (
	maxCapParams   = 30
	maxCapRoutes   = 20
	capResultLimit = 16 << 20
)

var (
	capPlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)
	capParamName   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,47}$`)
)

// capGrantKey marks a proxied request made by a capability for one of its
// routes: access to the capability grants its routes' services, as a
// group's does its members.
type capGrantKey struct{}

func capabilityGrants(r *http.Request, slug string) bool {
	v, _ := r.Context().Value(capGrantKey{}).(string)
	return v != "" && v == slug
}

// capFallback reports whether a route's answer should give way to the next
// route: the provider had no key that worked, was out of quota or rate
// limited, or failed on its side. A request the provider understood and
// answered (2xx, or 400, 404, 422 and the like) is the answer.
func capFallback(status int) bool {
	switch status {
	case 401, 402, 403, 408, 425, 429:
		return true
	}
	return status >= 500
}

// capInput reads the capability's params from the query or a JSON body,
// applies defaults and checks the required ones. Values from a query string
// are converted to the param's declared type.
func capInput(r *http.Request, c *store.Capability, maxBody int64) (map[string]any, map[string]string, error) {
	in := map[string]any{}
	ctl := map[string]string{}
	for k, vs := range r.URL.Query() {
		if len(vs) == 0 {
			continue
		}
		if strings.HasPrefix(k, "_") {
			ctl[k] = vs[0]
			continue
		}
		in[k] = vs[0]
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil {
			return nil, nil, errors.New("could not read the request body")
		}
		if int64(len(b)) > maxBody {
			return nil, nil, errors.New("request body is too large")
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			var body map[string]any
			if err := json.Unmarshal(b, &body); err != nil {
				return nil, nil, errors.New("the body must be a JSON object of the capability's params")
			}
			for k, v := range body {
				if strings.HasPrefix(k, "_") {
					ctl[k] = fmt.Sprint(v)
					continue
				}
				in[k] = v
			}
		}
	}
	for _, p := range c.Params {
		v, ok := in[p.Name]
		if !ok || v == nil || v == "" {
			if p.Default != nil {
				in[p.Name] = p.Default
				continue
			}
			if p.Required {
				return nil, nil, fmt.Errorf("%s is required", p.Name)
			}
			delete(in, p.Name)
			continue
		}
		if s, isStr := v.(string); isStr {
			cv, err := coerceParam(p.Type, s)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %v", p.Name, err)
			}
			in[p.Name] = cv
		}
	}
	return in, ctl, nil
}

func coerceParam(typ, s string) (any, error) {
	switch typ {
	case "integer":
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, errors.New("must be a whole number")
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, errors.New("must be a number")
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return nil, errors.New("must be true or false")
		}
		return b, nil
	}
	return s, nil
}

func paramText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	}
	if b, err := json.Marshal(v); err == nil && (len(b) == 0 || (b[0] != '"' && b[0] != '{' && b[0] != '[')) {
		return string(b)
	}
	return fmt.Sprint(v)
}

// fillText replaces {{param}} placeholders in s. ok is false when s names a
// param that has no value, so the caller can leave the field out.
func fillText(s string, in map[string]any, esc func(string) string) (string, bool) {
	ok := true
	out := capPlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		name := capPlaceholder.FindStringSubmatch(m)[1]
		v, has := in[name]
		if !has {
			ok = false
			return ""
		}
		t := paramText(v)
		if esc != nil {
			t = esc(t)
		}
		return t
	})
	return out, ok
}

// fillBody fills a body template. A string that is exactly one placeholder
// takes the param's value with its type (a number stays a number); a field
// whose placeholder has no value is left out.
func fillBody(v any, in map[string]any) (any, bool) {
	switch t := v.(type) {
	case string:
		if m := capPlaceholder.FindStringSubmatch(t); m != nil && strings.TrimSpace(t) == m[0] {
			val, ok := in[m[1]]
			return val, ok
		}
		return fillText(t, in, nil)
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if fv, ok := fillBody(x, in); ok {
				out[k] = fv
			}
		}
		return out, true
	case []any:
		out := []any{}
		for _, x := range t {
			if fv, ok := fillBody(x, in); ok {
				out = append(out, fv)
			}
		}
		return out, true
	}
	return v, true
}

// buildRoute turns the params into one route's method, path, query and body.
func buildRoute(rt store.CapabilityRoute, in map[string]any) (string, string, map[string]string, json.RawMessage, error) {
	method := strings.ToUpper(orDefault(rt.Method, http.MethodGet))
	path, ok := fillText(rt.Path, in, url.PathEscape)
	if !ok {
		return "", "", nil, nil, errors.New("a param in the path has no value")
	}
	q := map[string]string{}
	for k, tmpl := range rt.Query {
		if v, ok := fillText(tmpl, in, nil); ok && v != "" {
			q[k] = v
		}
	}
	var body json.RawMessage
	if rt.Body != nil {
		b, _ := fillBody(rt.Body, in)
		body, _ = json.Marshal(b)
	}
	return method, path, q, body, nil
}

// jsonPath reads a dot path ("results.0.name") from decoded JSON.
func jsonPath(v any, path string) any {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return v
	}
	for _, seg := range strings.Split(path, ".") {
		switch t := v.(type) {
		case map[string]any:
			v = t[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(t) {
				return nil
			}
			v = t[i]
		default:
			return nil
		}
	}
	return v
}

// mapOutput applies a route's output mapping to a JSON answer.
func mapOutput(o *store.CapabilityOutput, body []byte) (map[string]any, bool) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, false
	}
	pick := func(item any) any {
		if len(o.Fields) == 0 {
			return item
		}
		m := map[string]any{}
		for out, p := range o.Fields {
			m[out] = jsonPath(item, p)
		}
		return m
	}
	if strings.TrimSpace(o.Items) == "" {
		return map[string]any{"data": pick(v)}, true
	}
	list, _ := jsonPath(v, o.Items).([]any)
	items := make([]any, 0, len(list))
	for _, it := range list {
		items = append(items, pick(it))
	}
	return map[string]any{"items": items, "count": len(items)}, true
}

type capAttempt struct {
	Service string `json:"service"`
	Status  int    `json:"status"`
	Error   string `json:"error,omitempty"`
}

// serveCapability answers /proxy/{capability}.
func (s *Server) serveCapability(w http.ResponseWriter, r *http.Request, c *store.Capability) {
	a := appFrom(r)
	if !a.CanUse(c.Slug) || a.ShareID != nil {
		writeErr(w, 403, "service_not_allowed", "this app cannot use capability "+c.Slug)
		return
	}
	in, ctl, err := capInput(r, c, s.Cfg.MaxBodyBytes)
	if err != nil {
		writeErr(w, 400, "bad_params", err.Error())
		return
	}
	raw := ctl["_raw"] == "1" || ctl["_raw"] == "true"
	only := strings.TrimSpace(ctl["_service"])

	type planned struct {
		rt           store.CapabilityRoute
		method, path string
		query        map[string]string
		body         json.RawMessage
	}
	plans := map[int]planned{}
	cands := []routeCand{}
	for i, rt := range c.Routes {
		if rt.Disabled || (only != "" && rt.Service != only) {
			continue
		}
		sv, err := s.Store.GetServiceBySlug(r.Context(), rt.Service)
		if err != nil {
			continue
		}
		method, path, q, body, err := buildRoute(rt, in)
		if err != nil {
			continue
		}
		plans[i] = planned{rt, method, path, q, body}
		cands = append(cands, s.candidate(r.Context(), i, sv, rt.Weight, method, path))
	}
	if len(cands) == 0 {
		msg := "capability " + c.Slug + " has no route that can take these params"
		if only != "" {
			msg = "capability " + c.Slug + " has no usable route on service " + only
		}
		writeErr(w, 503, "no_route", msg)
		return
	}

	tried := []capAttempt{}
	var last *cappedRecorder
	var lastSvc string
	for _, cand := range orderRoutes(c.Slug, c.Strategy, cands) {
		p := plans[cand.idx]
		ctx := context.WithValue(r.Context(), capGrantKey{}, cand.sv.Slug)
		rec, err := s.proxyCapture(ctx, r, cand.sv.Slug, p.method, p.path, p.query, p.body, nil, capResultLimit)
		if err != nil {
			tried = append(tried, capAttempt{Service: cand.sv.Slug, Error: err.Error()})
			continue
		}
		at := capAttempt{Service: cand.sv.Slug, Status: rec.Code}
		if rec.Code >= 400 {
			var e struct {
				Error apiError `json:"error"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &e) == nil && e.Error.Code != "" {
				at.Error = e.Error.Code
			}
		}
		tried = append(tried, at)
		last, lastSvc = rec, cand.sv.Slug
		if capFallback(rec.Code) {
			continue
		}
		s.writeCapResult(w, c, cand.sv, p.rt, rec, tried, raw)
		return
	}
	setHeader(w.Header(), "Capability", c.Slug)
	setHeader(w.Header(), "Tried", triedList(tried))
	if last != nil {
		sv, _ := s.Store.GetServiceBySlug(r.Context(), lastSvc)
		if sv != nil {
			s.writeCapResult(w, c, sv, store.CapabilityRoute{}, last, tried, true)
			return
		}
	}
	writeJSON(w, 503, map[string]any{"error": apiError{Code: "no_route_worked", Message: "no route of " + c.Slug + " could serve this request"}, "tried": tried})
}

func triedList(t []capAttempt) string {
	parts := make([]string, 0, len(t))
	for _, a := range t {
		parts = append(parts, fmt.Sprintf("%s=%d", a.Service, a.Status))
	}
	return strings.Join(parts, ", ")
}

// writeCapResult relays a route's answer, mapped when the route has an
// output mapping and the answer is a JSON success.
func (s *Server) writeCapResult(w http.ResponseWriter, c *store.Capability, sv *store.Service, rt store.CapabilityRoute, rec *cappedRecorder, tried []capAttempt, raw bool) {
	h := w.Header()
	for k, vs := range rec.Header() {
		ck := http.CanonicalHeaderKey(k)
		if ck == "Content-Length" {
			continue
		}
		h[ck] = vs
	}
	setHeader(h, "Capability", c.Slug)
	setHeader(h, "Service", sv.Slug)
	setHeader(h, "Tried", triedList(tried))
	body := rec.Body.Bytes()
	if !raw && rt.Output != nil && rec.Code >= 200 && rec.Code < 300 && strings.Contains(rec.Header().Get("Content-Type"), "json") {
		if out, ok := mapOutput(rt.Output, body); ok {
			out["capability"], out["service"], out["provider"] = c.Slug, sv.Slug, sv.Provider
			writeJSON(w, rec.Code, out)
			return
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(body)
}

// ---- admin ----

type capabilityInput struct {
	Slug        *string                  `json:"slug"`
	Name        *string                  `json:"name"`
	Description *string                  `json:"description"`
	Strategy    *string                  `json:"strategy"`
	Params      *[]store.CapabilityParam `json:"params"`
	Routes      *[]store.CapabilityRoute `json:"routes"`
}

// applyCapability merges in into c and checks the result. It returns a
// message for the user, empty when c is valid.
func (s *Server) applyCapability(ctx context.Context, in capabilityInput, c *store.Capability) string {
	if in.Slug != nil {
		c.Slug = strings.TrimSpace(*in.Slug)
	}
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		c.Description = strings.TrimSpace(*in.Description)
	}
	if in.Strategy != nil {
		c.Strategy = strings.TrimSpace(*in.Strategy)
	}
	if in.Params != nil {
		c.Params = *in.Params
	}
	if in.Routes != nil {
		c.Routes = *in.Routes
	}
	if c.Strategy == "" {
		c.Strategy = StrategyPriority
	}
	switch {
	case !slugRe.MatchString(c.Slug):
		return "slug must be lowercase letters, digits and hyphens"
	case c.Name == "" || len(c.Name) > 100:
		return "name is required (at most 100 characters)"
	case len(c.Description) > 1000:
		return "description must be at most 1,000 characters"
	case !routingStrategies[c.Strategy]:
		return "strategy must be priority, round_robin, random, weighted, cheapest or most_keys"
	case len(c.Params) > maxCapParams:
		return fmt.Sprintf("at most %d params", maxCapParams)
	case len(c.Routes) == 0:
		return "add at least one route"
	case len(c.Routes) > maxCapRoutes:
		return fmt.Sprintf("at most %d routes", maxCapRoutes)
	}
	declared := map[string]bool{}
	for i, p := range c.Params {
		if !capParamName.MatchString(p.Name) {
			return fmt.Sprintf("params[%d]: name must start with a letter and hold letters, digits and _", i)
		}
		if declared[p.Name] {
			return "param " + p.Name + " is listed twice"
		}
		declared[p.Name] = true
		switch p.Type {
		case "", "string", "integer", "number", "boolean":
		default:
			return "param " + p.Name + ": type must be string, integer, number or boolean"
		}
	}
	undeclared := func(text string) string {
		for _, m := range capPlaceholder.FindAllStringSubmatch(text, -1) {
			if !declared[m[1]] {
				return m[1]
			}
		}
		return ""
	}
	for i, rt := range c.Routes {
		label := fmt.Sprintf("routes[%d] (%s)", i, rt.Service)
		if _, err := s.Store.GetServiceBySlug(ctx, rt.Service); err != nil {
			return label + ": no service named " + rt.Service
		}
		switch strings.ToUpper(orDefault(rt.Method, "GET")) {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return label + ": method must be GET, POST, PUT, PATCH or DELETE"
		}
		if !strings.HasPrefix(rt.Path, "/") {
			return label + ": path must start with /"
		}
		if n := undeclared(rt.Path); n != "" {
			return label + ": the path uses {{" + n + "}}, which is not a param"
		}
		for k, v := range rt.Query {
			if n := undeclared(v); n != "" {
				return label + ": query " + k + " uses {{" + n + "}}, which is not a param"
			}
		}
		if rt.Body != nil {
			b, _ := json.Marshal(rt.Body)
			if n := undeclared(string(b)); n != "" {
				return label + ": the body uses {{" + n + "}}, which is not a param"
			}
		}
		if rt.Weight < 0 || rt.Weight > 1000 {
			return label + ": weight must be 0 to 1000"
		}
	}
	return ""
}

func (s *Server) listCapabilities(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.ListCapabilities(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"capabilities": cs})
}

func (s *Server) getCapability(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	c, err := s.Store.GetCapability(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) createCapability(w http.ResponseWriter, r *http.Request) {
	var in capabilityInput
	if !decode(w, r, &in) {
		return
	}
	c := &store.Capability{}
	if msg := s.applyCapability(r.Context(), in, c); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if taken, err := s.Store.SlugTaken(r.Context(), c.Slug); err != nil || taken {
		writeErr(w, 409, "duplicate", "a service, group or capability already uses the slug "+c.Slug)
		return
	}
	made, err := s.Store.CreateCapability(r.Context(), c)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "capability.create", made.Slug, routeSummary(made))
	writeJSON(w, 201, made)
}

func (s *Server) updateCapability(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	c, err := s.Store.GetCapability(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var in capabilityInput
	if !decode(w, r, &in) {
		return
	}
	in.Slug = nil // the slug is the public URL
	if msg := s.applyCapability(r.Context(), in, c); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if err := s.Store.UpdateCapability(r.Context(), c); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "capability.update", c.Slug, routeSummary(c))
	c, _ = s.Store.GetCapability(r.Context(), id)
	writeJSON(w, 200, c)
}

func (s *Server) deleteCapability(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	c, err := s.Store.GetCapability(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.Store.DeleteCapability(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "capability.delete", c.Slug, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func routeSummary(c *store.Capability) string {
	parts := []string{}
	for _, rt := range c.Routes {
		parts = append(parts, rt.Service)
	}
	return c.Strategy + ": " + strings.Join(parts, ", ")
}

// capabilityDoc is what an app sees of a capability: how to call it.
func capabilityDoc(c *store.Capability, publicURL string) map[string]any {
	routes := []map[string]any{}
	for _, rt := range c.Routes {
		if rt.Disabled {
			continue
		}
		routes = append(routes, map[string]any{"service": rt.Service, "mapped": rt.Output != nil})
	}
	return map[string]any{
		"slug": c.Slug, "name": c.Name, "description": c.Description, "strategy": c.Strategy,
		"params": c.Params, "routes": routes,
		"url": strings.TrimRight(publicURL, "/") + "/proxy/" + c.Slug,
	}
}
