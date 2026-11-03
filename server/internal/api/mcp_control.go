package api

// The control MCP: every dashboard feature for an MCP client (Claude Code,
// Claude Desktop, claude.ai, Cursor), as the signed in user. POST
// /mcp/control takes an admin API token (acnadm_...) and acts as its owner
// with the lower of the owner's role and the token scope, exactly like
// /api/admin. Each tool is an in process request to an existing
// /api/admin route carrying the same token, so roles, sessionOnly,
// notShareApp, validation and audit stay with the routes. The tool table is
// in mcp_control_tools.go. /mcp is the app side and stays separate.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/docsmd"
	"github.com/sidharthpunathil/acnologia/server/internal/llmstxt"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// ctlResultCap caps one tool result, like the app MCP's result size.
const ctlResultCap = 512 << 10

// controlAppMeta is the meta key holding the id of the app call_service
// runs as.
const controlAppMeta = "control_mcp_app_id"

// controlAppName is that app's name on the Apps page.
const controlAppName = "Control MCP"

const ctlInstructions = "This is Acnologia's control MCP: it runs the server (services, keys, apps, providers, AI builds, sharing, usage) " +
	"as the signed in user who owns the admin API token, with that user's role (or the token's scope when lower). Every change is audited under that user. " +
	"Tools the role cannot use are not listed. Services are named by id, slug or name. " +
	"call_service calls a provider API through the pooled keys as the app \"Control MCP\"; the key is never shown. " +
	"search finds anything by text; batch runs up to 20 tools in one call; list_routes lists every admin route. " +
	"Read tools take fields, filter, limit and offset to cut big results down, and the guide tools take format: markdown. " +
	"Destructive tools (deletes, revokes, rotate, disconnect) do nothing on the first call: they say what would happen, then run when called again with confirm: true. Ask the user before confirming. " +
	"The app side, where an app's token leases keys and proxies calls, is the separate /mcp endpoint."

// ctlOpts are the client's choices, from query parameters or headers.
type ctlOpts struct {
	// groups is nil for the default set.
	groups   map[string]bool
	exclude  map[string]bool
	readOnly bool
	confirm  bool
}

func ctlOptions(r *http.Request) ctlOpts {
	get := func(h, q string) string {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
		return strings.TrimSpace(r.URL.Query().Get(q))
	}
	list := func(v string) map[string]bool {
		out := map[string]bool{}
		for _, x := range strings.Split(v, ",") {
			if x = strings.ToLower(strings.TrimSpace(x)); x != "" {
				out[x] = true
			}
		}
		return out
	}
	o := ctlOpts{exclude: list(get("X-Acnologia-MCP-Exclude", "exclude")), confirm: true}
	if g := get("X-Acnologia-MCP-Groups", "groups"); g != "" {
		o.groups = list(g)
	}
	o.readOnly = truthy(get("X-Acnologia-MCP-Read-Only", "read_only"))
	switch strings.ToLower(get("X-Acnologia-MCP-Confirm", "confirm")) {
	case "off", "0", "false", "no":
		o.confirm = false
	}
	return o
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (o ctlOpts) groupOn(id string) bool {
	if o.groups == nil {
		for _, g := range ctlGroups {
			if g.ID == id {
				return g.Default
			}
		}
		return false
	}
	return o.groups["all"] || o.groups[id]
}

// ctlCtx is one control MCP request: the caller and their choices.
type ctlCtx struct {
	s    *Server
	r    *http.Request
	user *store.User
	opts ctlOpts
}

// controlUser authenticates the admin API token like tokenUser does.
func (s *Server) controlUser(r *http.Request) (*store.User, string) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !store.IsAdminToken(tok) {
		if appToken(r) != "" {
			return nil, "this is the control MCP and it takes an admin API token (Authorization: Bearer acnadm_...); app tokens use /mcp"
		}
		return nil, "send an admin API token: Authorization: Bearer acnadm_..."
	}
	t, owner, err := s.Store.TokenByHash(r.Context(), crypto.SHA256(tok))
	if err != nil {
		return nil, "API token is invalid, expired or revoked"
	}
	role, _ := store.ScopeRole(t.Scope)
	u := *owner
	if !role.AtLeast(u.Role) {
		u.Role = role
	}
	return &u, ""
}

func (s *Server) mcpControl(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u, msg := s.controlUser(r)
	if u == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="acnologia"`)
		writeErr(w, http.StatusUnauthorized, "unauthenticated", msg)
		return
	}
	var req rpcReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": nil, "error": rpcErr{-32700, "parse error"}})
		return
	}
	if len(req.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	c := &ctlCtx{s: s, r: r, user: u, opts: ctlOptions(r)}
	result, rerr := c.dispatch(req)
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rerr != nil {
		resp["error"] = rerr
	} else {
		resp["result"] = result
	}
	writeJSON(w, 200, resp)
}

func (c *ctlCtx) dispatch(req rpcReq) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = mcpProtocol
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"listChanged": false, "subscribe": false},
				"prompts":   map[string]any{"listChanged": false},
			},
			"serverInfo":   map[string]any{"name": "acnologia-control", "title": "Acnologia control", "version": c.s.Cfg.Version},
			"instructions": ctlInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		out := []map[string]any{}
		for i := range ctlTools {
			t := &ctlTools[i]
			if c.unavailable(t) == "" {
				out = append(out, c.toolJSON(t))
			}
		}
		return map[string]any{"tools": out}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		t := ctlToolByName(p.Name)
		if t == nil {
			return nil, &rpcErr{-32602, "unknown tool: " + p.Name}
		}
		in := map[string]any{}
		if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
			if err := json.Unmarshal(p.Arguments, &in); err != nil {
				return ctlFail("arguments must be a JSON object: " + err.Error()), nil
			}
		}
		return c.call(t, in), nil
	case "resources/list":
		return map[string]any{"resources": ctlResources()}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []map[string]any{{
			"uriTemplate": "acnologia://docs/{slug}", "name": "doc", "title": "An Acnologia doc", "mimeType": "text/markdown",
			"description": "A page of the Acnologia docs, as served under /docs/{slug}.md",
		}}}, nil
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		res, err := c.readResource(p.URI)
		if err != nil {
			return nil, &rpcErr{-32002, err.Error()}
		}
		return res, nil
	case "prompts/list":
		return map[string]any{"prompts": ctlPrompts}, nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		res, err := ctlPrompt(p.Name, p.Arguments)
		if err != nil {
			return nil, &rpcErr{-32602, err.Error()}
		}
		return res, nil
	}
	return nil, &rpcErr{-32601, "method not found: " + req.Method}
}

// unavailable says why this connection cannot use t, or "".
func (c *ctlCtx) unavailable(t *ctlTool) string {
	switch {
	case !c.user.Role.AtLeast(t.Role):
		return fmt.Sprintf("%s needs the %s role; this connection has %s", t.Name, t.Role, c.user.Role)
	case !c.opts.groupOn(t.Group):
		return fmt.Sprintf("the %s group is not on for this connection; add it with ?groups=", t.Group)
	case c.opts.exclude[t.Name]:
		return t.Name + " is excluded on this connection"
	case c.opts.readOnly && t.Kind != ctlRead:
		return "this connection is read only; " + t.Name + " changes things"
	}
	return ""
}

func (c *ctlCtx) toolJSON(t *ctlTool) map[string]any {
	p := props(t.Props)
	if t.Kind == ctlRead || t.Name == "api_request" {
		p = props(p, shapeProps)
	}
	if t.Name == "provider_guide" || t.Name == "service_guide" {
		p["format"] = eProp("json (default): the structured guide and its Markdown. markdown: only the Markdown, ready to read", "json", "markdown")
	}
	if t.Kind == ctlDestructive && c.opts.confirm {
		p["confirm"] = bProp("Set true to do it. Without it nothing changes and the result says what would happen")
	}
	req := t.Required
	if req == nil {
		req = []string{}
	}
	ann := map[string]any{
		"title":           t.Title,
		"readOnlyHint":    t.Kind == ctlRead,
		"destructiveHint": t.Kind == ctlDestructive,
		"idempotentHint":  t.Kind == ctlRead || t.Method == http.MethodPut,
		"openWorldHint":   t.OpenWorld,
	}
	return map[string]any{
		"name": t.Name, "title": t.Title, "description": t.Desc,
		"inputSchema": map[string]any{"type": "object", "properties": p, "required": req},
		"annotations": ann,
	}
}

// call runs a tool and returns its MCP result, shaped by the fields,
// filter, limit, offset and format inputs when given.
func (c *ctlCtx) call(t *ctlTool, in map[string]any) map[string]any {
	res := shapeResult(c.callRaw(t, in), in)
	delete(res, ctlRawKey)
	return res
}

// ctlRawKey holds a result's untrimmed text inside the server only.
const ctlRawKey = "_untrimmed"

func (c *ctlCtx) callRaw(t *ctlTool, in map[string]any) map[string]any {
	if why := c.unavailable(t); why != "" {
		return ctlFail(why)
	}
	for _, k := range t.Required {
		if v, ok := in[k]; !ok || v == nil || v == "" {
			return ctlFail(k + " is required")
		}
	}
	confirmStep := t.Kind == ctlDestructive && c.opts.confirm
	if t.Name == "api_request" && !strings.EqualFold(argStr(in, "method"), http.MethodDelete) {
		confirmStep = false
	}
	if confirmStep {
		if ok, _ := in["confirm"].(bool); !ok {
			what, err := t.Describe(c, in)
			if err != nil {
				return ctlFail(err.Error())
			}
			msg := "Nothing changed yet. This would " + what + ". If the user agrees, call " + t.Name + " again with the same arguments and confirm: true."
			return map[string]any{
				"content":           []map[string]any{{"type": "text", "text": msg}},
				"structuredContent": map[string]any{"confirm_required": true, "would": what},
			}
		}
	}
	if t.Run != nil {
		return t.Run(c, in)
	}
	p, err := c.buildPath(t, in)
	if err != nil {
		return ctlFail(err.Error())
	}
	var body any
	switch {
	case t.Prepare != nil:
		if body, err = t.Prepare(c, in, p); err != nil {
			return ctlFail(err.Error())
		}
	case len(t.Body) > 0:
		m := map[string]any{}
		for k, v := range pick(in, t.Body...) {
			if n, ok := t.Rename[k]; ok {
				k = n
			}
			m[k] = v
		}
		body = m
	}
	code, b, _ := c.api(t.Method, p, body)
	return ctlRouteResult(code, b)
}

// buildPath fills the route's placeholders and query string.
func (c *ctlCtx) buildPath(t *ctlTool, in map[string]any) (string, error) {
	var err error
	resolve := func(name, v string) string {
		if name != "service" {
			return v
		}
		sv, e := c.s.findService(c.r.Context(), v)
		if e != nil {
			err = e
			return ""
		}
		return strconv.FormatInt(sv.ID, 10)
	}
	p := pathParamRe.ReplaceAllStringFunc(t.Path, func(m string) string {
		name := m[1 : len(m)-1]
		v := argStr(in, name)
		if v == "" {
			if err == nil {
				err = fmt.Errorf("%s is required", name)
			}
			return m
		}
		return url.PathEscape(resolve(name, v))
	})
	q := url.Values{}
	for _, name := range t.Query {
		v := argStr(in, name)
		if v == "" {
			continue
		}
		key := name
		if n, ok := t.Rename[name]; ok {
			key = n
		}
		q.Set(key, resolve(name, v))
	}
	if err != nil {
		return "", err
	}
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return p, nil
}

// api calls a dashboard route in process as the caller: the same admin
// token and address, so the route sees exactly what a direct call would.
func (c *ctlCtx) api(method, p string, body any) (int, []byte, http.Header) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	// Drop the outer request's chi route context, or the router would treat
	// this call as a sub route of /mcp/control.
	ctx := context.WithValue(c.r.Context(), chi.RouteCtxKey, nil)
	req := httptest.NewRequest(method, "http://acnologia.internal"+p, rdr).WithContext(ctx)
	req.Header.Set("Authorization", c.r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "acnologia-mcp")
	req.Host = c.r.Host
	req.RemoteAddr = c.r.RemoteAddr
	rec := httptest.NewRecorder()
	c.s.handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

// apiJSON is api with the body decoded and the error message read.
func (c *ctlCtx) apiJSON(method, p string, body any) (int, map[string]any, string) {
	code, b, _ := c.api(method, p, body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return code, out, routeErrMsg(code, out)
}

func routeErrMsg(code int, out map[string]any) string {
	if code < 400 {
		return ""
	}
	if e, ok := out["error"].(map[string]any); ok {
		if m, ok := e["message"].(string); ok && m != "" {
			return m
		}
	}
	return fmt.Sprintf("HTTP %d", code)
}

// merge reads the object under field at p and overlays the given inputs,
// for routes that replace the whole object.
func (c *ctlCtx) merge(p, field string, in map[string]any, keys []string) (any, error) {
	code, cur, msg := c.apiJSON(http.MethodGet, p, nil)
	if code >= 400 {
		return nil, errors.New(msg)
	}
	m, _ := cur[field].(map[string]any)
	if m == nil {
		return nil, fmt.Errorf("could not read the current %s", field)
	}
	for k, v := range pick(in, keys...) {
		m[k] = v
	}
	delete(m, "updated_at")
	delete(m, "updated_by")
	return m, nil
}

// ---- results ----

func ctlFail(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

// ctlText builds a result from text, adding structured content when the
// text is a JSON object, and trims it to the cap.
func ctlText(text string, isErr bool) map[string]any {
	res := map[string]any{}
	if len(text) > ctlResultCap {
		// Kept for shaping, which can cut the result under the cap; call
		// removes it before the result leaves.
		res[ctlRawKey] = text
		n := ctlResultCap
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = text[:n] + fmt.Sprintf("\n[trimmed: the result was %d bytes, over the %d KB cap; narrow the request]", len(text), ctlResultCap>>10)
	} else {
		var obj map[string]any
		if json.Unmarshal([]byte(text), &obj) == nil && obj != nil {
			res["structuredContent"] = obj
		}
	}
	res["content"] = []map[string]any{{"type": "text", "text": text}}
	if isErr {
		res["isError"] = true
	}
	return res
}

// ctlRouteResult turns a route's response into a tool result.
func ctlRouteResult(code int, b []byte) map[string]any {
	if code >= 400 {
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		text := fmt.Sprintf("HTTP %d: %s", code, routeErrMsg(code, out))
		if len(out) > 1 {
			text += "\n" + strings.TrimSpace(string(b))
		}
		return ctlFail(text)
	}
	body := strings.TrimSpace(string(b))
	if body == "" {
		body = fmt.Sprintf(`{"ok":true,"status":%d}`, code)
	}
	return ctlText(body, false)
}

func ctlJSON(v any) map[string]any {
	b, _ := json.Marshal(v)
	return ctlText(string(b), false)
}

// ---- tools that are not one route ----

// ctlRawRequest is api_request: any /api/admin route not covered.
func ctlRawRequest(c *ctlCtx, in map[string]any) map[string]any {
	method := strings.ToUpper(argStr(in, "method"))
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return ctlFail("method must be GET, POST, PUT, PATCH or DELETE")
	}
	p, err := ctlRawPath(method, argStr(in, "path"))
	if err != nil {
		return ctlFail(err.Error())
	}
	q := url.Values{}
	if m, ok := in["query"].(map[string]any); ok {
		for k, v := range m {
			q.Set(k, fmt.Sprint(v))
		}
	}
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	var body any
	if b, ok := in["body"]; ok && b != nil && method != http.MethodGet && method != http.MethodDelete {
		body = b
	}
	code, b, _ := c.api(method, p, body)
	return ctlRouteResult(code, b)
}

// ctlRawPath checks a raw path: it must stay under /api/admin/, and the
// routes an admin token must never reach from here are refused up front
// (the routes refuse a token too).
func ctlRawPath(method, p string) (string, error) {
	if strings.ContainsAny(p, "?#\\") || strings.Contains(p, "%") {
		return "", errors.New("put the query in query, and do not escape the path")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return "", errors.New("the path must not hold . or .. segments")
		}
	}
	clean := path.Clean(p)
	if !strings.HasPrefix(clean, "/api/admin/") {
		return "", errors.New("path must start with /api/admin/")
	}
	switch {
	case clean == "/api/admin/tokens" || strings.HasPrefix(clean, "/api/admin/tokens/"):
		return "", errors.New("admin API tokens are managed in the dashboard only")
	case clean == "/api/admin/me" && method != http.MethodGet:
		return "", errors.New("your account is changed in the dashboard only")
	case clean == "/api/admin/chat" || strings.HasPrefix(clean, "/api/admin/chat/"):
		return "", errors.New("the dashboard assistant's threads are not exposed here")
	}
	return clean, nil
}

// controlApp returns the app call_service runs as, making it on first use
// through the apps route, so it is audited and shows on the Apps page where
// an admin can limit or turn it off. Its id lives in meta.
func (c *ctlCtx) controlApp() (*store.App, error) {
	s, ctx := c.s, c.r.Context()
	s.ctlAppMu.Lock()
	defer s.ctlAppMu.Unlock()
	if id, err := strconv.ParseInt(s.Store.GetMeta(ctx, controlAppMeta), 10, 64); err == nil && id > 0 {
		if a, err := s.Store.GetApp(ctx, id); err == nil {
			if ts, err := s.Store.ListAppTokens(ctx, a.ID); err == nil {
				for _, t := range ts {
					if t.RevokedAt == nil {
						a.TokenID = t.ID
						break
					}
				}
			}
			return a, nil
		}
	}
	code, out, msg := c.apiJSON(http.MethodPost, "/api/admin/apps", map[string]any{
		"name": controlAppName, "mode": "proxy",
		"description": "Calls made with call_service through the control MCP (/mcp/control). Limit or turn it off here.",
	})
	if code >= 400 {
		return nil, fmt.Errorf("could not make the %s app: %s", controlAppName, msg)
	}
	app, _ := out["app"].(map[string]any)
	id, _ := argNum(app, "id")
	if err := s.Store.SetMeta(ctx, controlAppMeta, strconv.FormatInt(id, 10)); err != nil {
		return nil, err
	}
	a, err := s.Store.GetApp(ctx, id)
	if err != nil {
		return nil, err
	}
	if t, ok := out["app_token"].(map[string]any); ok {
		a.TokenID, _ = argNum(t, "id")
	}
	return a, nil
}

// ctlBlockedHeaders are request headers call_service never sends: the key
// comes from the pool, and Acnologia's own headers are not the caller's.
func ctlBlockedHeader(k string) bool {
	k = strings.ToLower(k)
	return k == "authorization" || k == "cookie" || k == "host" ||
		strings.HasPrefix(k, "x-acnologia-") || strings.HasPrefix(k, "x-cogito-")
}

// ctlCallService is call_service: one request through the proxy, as the
// Control MCP app, so key rotation, retries, the breaker, the cache, usage
// and the app's limits all apply.
func ctlCallService(c *ctlCtx, in map[string]any) map[string]any {
	if !c.user.Role.AtLeast(store.RoleAdmin) {
		return ctlFail("call_service needs the admin role: it spends provider quota")
	}
	sv, err := c.s.findService(c.r.Context(), argStr(in, "service"))
	if err != nil {
		return ctlFail(err.Error())
	}
	a, err := c.controlApp()
	if err != nil {
		return ctlFail(err.Error())
	}
	if !a.Active {
		return ctlFail("the " + controlAppName + " app is turned off; turn it on in Apps to call services")
	}
	if !a.CanUse(sv.Slug) {
		return ctlFail("the " + controlAppName + " app may not use " + sv.Slug + "; allow it in Apps")
	}
	query := map[string]string{}
	if m, ok := in["query"].(map[string]any); ok {
		for k, v := range m {
			query[k] = fmt.Sprint(v)
		}
	}
	hdr := map[string]string{}
	if m, ok := in["headers"].(map[string]any); ok {
		for k, v := range m {
			if !ctlBlockedHeader(k) {
				hdr[k] = fmt.Sprint(v)
			}
		}
	}
	var body json.RawMessage
	if b, ok := in["body"]; ok && b != nil {
		body, _ = json.Marshal(b)
	}
	ctx := store.WithAppToken(context.WithValue(c.r.Context(), appKey, a), a.TokenID)
	limit := ctlResultCap - 4<<10
	rec, err := c.s.proxyCapture(ctx, c.r, sv.Slug, argStr(in, "method"), argStr(in, "path"), query, body, hdr, limit)
	if err != nil {
		return ctlFail(err.Error())
	}
	h := map[string]string{}
	for k, vs := range rec.Header() {
		ck := http.CanonicalHeaderKey(k)
		if len(vs) == 0 {
			continue
		}
		if ck == "Content-Type" || ck == "Retry-After" || ck == "Location" || strings.HasPrefix(ck, "X-Acnologia-") ||
			strings.HasPrefix(strings.ToLower(ck), "x-ratelimit") || strings.HasPrefix(strings.ToLower(ck), "ratelimit") {
			h[ck] = vs[0]
		}
	}
	out := map[string]any{"status": rec.Code, "service": sv.Slug, "headers": h}
	b := rec.Body.Bytes()
	ct := rec.Header().Get("Content-Type")
	over := len(b) > limit
	switch {
	case strings.Contains(ct, "json") && !over:
		var v any
		if json.Unmarshal(b, &v) == nil {
			out["body"] = v
		} else {
			out["body"] = string(b)
		}
	case utf8.Valid(truncate(b, limit)) && !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "video/"):
		out["body"] = string(truncate(b, limit))
		if over {
			out["note"] = fmt.Sprintf("body trimmed to %d KB", limit>>10)
		}
	default:
		if over {
			out["note"] = "binary response too large for MCP; call the HTTP proxy directly"
		} else {
			out["bytes"] = len(b)
			out["body_base64"] = base64.StdEncoding.EncodeToString(b)
		}
	}
	return ctlJSON(out)
}

// ctlServiceExamples is list_service_examples.
func ctlServiceExamples(c *ctlCtx, in map[string]any) map[string]any {
	sv, err := c.s.findService(c.r.Context(), argStr(in, "service"))
	if err != nil {
		return ctlFail(err.Error())
	}
	spec := docSpec(sv)
	examples := []map[string]any{}
	for _, ex := range spec.Examples {
		args := map[string]any{"service": sv.Slug, "method": orDefault(ex.Method, "GET"), "path": ex.Path}
		if len(ex.Query) > 0 {
			args["query"] = ex.Query
		}
		if ex.Body != nil {
			args["body"] = ex.Body
		}
		examples = append(examples, map[string]any{"name": ex.Name, "call_service": args})
	}
	return ctlJSON(map[string]any{
		"service": sv.Slug, "name": sv.Name, "provider": sv.Provider, "base_url": sv.BaseURL,
		"quota_unit": spec.Quota.Unit, "offers": offerRows(spec.Offers), "examples": examples,
		"note": "Pass an example's call_service object to call_service; change the query or body as needed. Acnologia adds the key.",
	})
}

// ---- resources ----

func ctlResources() []map[string]any {
	out := []map[string]any{
		{"uri": "acnologia://overview", "name": "overview", "title": "Overview", "mimeType": "application/json",
			"description": "Services, keys by state, requests in the last 24 hours and recent events"},
		{"uri": "acnologia://services", "name": "services", "title": "Services", "mimeType": "application/json",
			"description": "Every service with its provider and key counts"},
		{"uri": "acnologia://llms-full.txt", "name": "llms-full.txt", "title": "Full Acnologia reference", "mimeType": "text/plain"},
	}
	for _, d := range docsmd.All() {
		out = append(out, map[string]any{"uri": "acnologia://docs/" + d.Slug, "name": "docs/" + d.Slug, "title": d.Title,
			"mimeType": "text/markdown", "description": d.Description})
	}
	return out
}

func (c *ctlCtx) readResource(uri string) (map[string]any, error) {
	text := func(mime, body string) map[string]any {
		return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mime, "text": body}}}
	}
	route := func(p string) (map[string]any, error) {
		code, b, _ := c.api(http.MethodGet, p, nil)
		if code >= 400 {
			var out map[string]any
			_ = json.Unmarshal(b, &out)
			return nil, errors.New(routeErrMsg(code, out))
		}
		return text("application/json", string(b)), nil
	}
	switch {
	case uri == "acnologia://overview":
		return route("/api/admin/overview")
	case uri == "acnologia://services":
		code, out, msg := c.apiJSON(http.MethodGet, "/api/admin/services", nil)
		if code >= 400 {
			return nil, errors.New(msg)
		}
		list := rows(out["services"], "id", "slug", "name", "provider", "category", "base_url", "strategy", "key_counts", "requests_24h")
		b, _ := json.MarshalIndent(map[string]any{"services": list}, "", "  ")
		return text("application/json", string(b)), nil
	case uri == "acnologia://llms-full.txt":
		return text("text/plain", strings.ReplaceAll(string(llmstxt.Full), "{{BASE_URL}}", c.s.baseURL(c.r))), nil
	case strings.HasPrefix(uri, "acnologia://docs/"):
		if d, ok := docsmd.Get(strings.TrimSuffix(strings.TrimPrefix(uri, "acnologia://docs/"), ".md")); ok {
			return text("text/markdown", string(d.Body)), nil
		}
	}
	return nil, fmt.Errorf("no resource %s", uri)
}

// ---- prompts ----

var ctlPrompts = []map[string]any{
	{"name": "onboard_provider", "title": "Add a provider and its keys",
		"description": "Find or build a provider, create a service for it and add keys.",
		"arguments":   []map[string]any{{"name": "provider", "description": "Provider name, slug or docs URL", "required": true}}},
	{"name": "health_report", "title": "Health report",
		"description": "Summarize failing keys, open breakers and budget use."},
	{"name": "set_up_app", "title": "Set up an app",
		"description": "Create an app for some services, pick its MCP tools and hand back its MCP URL and token.",
		"arguments": []map[string]any{{"name": "name", "description": "App name", "required": true},
			{"name": "services", "description": "Comma separated service slugs", "required": false}}},
	{"name": "share_with_person", "title": "Share with a person",
		"description": "Share services with a person through a claim link that ends after some days.",
		"arguments": []map[string]any{{"name": "services", "description": "Comma separated service slugs", "required": true},
			{"name": "days", "description": "Days until the share ends", "required": false}}},
}

func ctlPrompt(name string, args map[string]string) (map[string]any, error) {
	msg := func(t string) map[string]any {
		return map[string]any{"messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": t}}}}
	}
	switch name {
	case "onboard_provider":
		p := args["provider"]
		if p == "" {
			return nil, errors.New("provider is required")
		}
		return msg(fmt.Sprintf("Add %q to Acnologia. Call list_providers and look for it. If it is there, call create_service with that provider. "+
			"If it is not, call start_provider_build with kind create_provider and what you know (name, docs URLs), then poll get_ai_run until it ends. "+
			"Then ask me for the API keys and add them with add_keys (text accepts a paste). Finish with list_keys for the service and tell me which keys work.", p)), nil
	case "health_report":
		return msg("Make a health report for this Acnologia server. Call overview, list_keys with validity invalid, list_services and service_breaker for any " +
			"service with failures, and usage_report for 30 days. Report: keys that are invalid or out of quota (with the reason), services whose breaker is open, " +
			"app or budget limits being hit, and spend. End with the one or two actions that matter most."), nil
	case "set_up_app":
		n := args["name"]
		if n == "" {
			return nil, errors.New("name is required")
		}
		svcs := args["services"]
		if svcs == "" {
			svcs = "every service"
		}
		return msg(fmt.Sprintf("Create an app named %q for %s with create_app (mode proxy unless I say otherwise). "+
			"Then read get_app_mcp and use update_app_mcp to turn on the core and service_tools sets. "+
			"Give me the app's MCP endpoint URL and its token (shown once), and a one line snippet to add it to an MCP client.", n, svcs)), nil
	case "share_with_person":
		svcs := args["services"]
		if svcs == "" {
			return nil, errors.New("services is required")
		}
		days := args["days"]
		if days == "" {
			days = "7"
		}
		return msg(fmt.Sprintf("Share these services with a person: %s. Ask me for their name. Call create_share with recipient person, mode proxy, "+
			"the services, the label set to their name, and ends_at %s days from now (RFC 3339). Give me the claim link from the result and say when it stops working.", svcs, days)), nil
	}
	return nil, fmt.Errorf("no prompt named %q", name)
}

// ---- the dashboard's view ----

// mcpControlInfo is GET /api/admin/mcp/control: the endpoint, the groups and
// every tool with whether the caller's role can use it.
func (s *Server) mcpControlInfo(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	tools := make([]map[string]any, 0, len(ctlTools))
	for i := range ctlTools {
		t := &ctlTools[i]
		tools = append(tools, map[string]any{"name": t.Name, "title": t.Title, "description": t.Desc, "group": t.Group,
			"kind": t.Kind, "available": u != nil && u.Role.AtLeast(t.Role)})
	}
	writeJSON(w, 200, map[string]any{"url": s.baseURL(r) + "/mcp/control", "groups": ctlGroups, "tools": tools})
}
