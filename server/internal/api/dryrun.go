package api

// Dry runs: ?dry_run=1 on any POST, PUT, PATCH or DELETE under /api/admin
// says what the call would do and changes nothing. The gate (adminDryRun)
// sits in the /api/admin middleware stack. For a dry run it finds the route
// the request would reach, runs that route's own middlewares (the role
// group, sessionOnly, notShareApp: so a dry run is refused exactly where the
// call would be), and then, in place of the route's handler, a describer:
// the real handler never runs, so nothing can be half written. Routes with a
// describer below parse the body, check it as the handler would and resolve
// their targets; every other write gets the generic answer after its body
// parses and its target resolves. The answer is
// {dry_run: true, method, path, would, ...}.

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/keyparse"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// dryRunCtxKey marks a dry run requested in process (the control MCP's
// dry_run argument). Nothing outside this package sets it.
type dryRunCtxKey struct{}

func withDryRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, dryRunCtxKey{}, true)
}

// isDryRun reports whether a request asks for a dry run.
func isDryRun(r *http.Request) bool {
	if v, _ := r.Context().Value(dryRunCtxKey{}).(bool); v {
		return true
	}
	return truthy(r.URL.Query().Get("dry_run"))
}

// dryRunRoutes holds, per router, each route's middlewares that run after
// the gate, keyed "METHOD pattern".
var dryRunRoutes sync.Map // chi.Routes -> map[string][]func(http.Handler) http.Handler

func (s *Server) routeTail(routes chi.Routes) map[string][]func(http.Handler) http.Handler {
	if m, ok := dryRunRoutes.Load(routes); ok {
		return m.(map[string][]func(http.Handler) http.Handler)
	}
	gate := reflect.ValueOf(s.adminDryRun).Pointer()
	out := map[string][]func(http.Handler) http.Handler{}
	_ = chi.Walk(routes, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		for i, mw := range mws {
			if reflect.ValueOf(mw).Pointer() == gate {
				out[method+" "+strings.ReplaceAll(route, "/*/", "/")] = append([]func(http.Handler) http.Handler(nil), mws[i+1:]...)
				return nil
			}
		}
		return nil
	})
	dryRunRoutes.Store(routes, out)
	return out
}

// adminDryRun is the gate. It must be the last middleware of the
// /api/admin stack (server.go).
func (s *Server) adminDryRun(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWriteMethod(r.Method) || !isDryRun(r) {
			next.ServeHTTP(w, r)
			return
		}
		rctx := chi.RouteContext(r.Context())
		if rctx == nil || rctx.Routes == nil {
			writeErr(w, 500, "internal", "dry run: no router")
			return
		}
		tctx := chi.NewRouteContext()
		tctx.Routes = rctx.Routes
		pattern := rctx.Routes.Find(tctx, r.Method, r.URL.Path)
		if pattern == "" {
			// No such route: the router answers 404 or 405 and runs nothing.
			next.ServeHTTP(w, r)
			return
		}
		tail, ok := s.routeTail(rctx.Routes)[r.Method+" "+pattern]
		if !ok {
			// Never fall through to a handler on a dry run.
			writeErr(w, 500, "internal", "dry run: route not indexed")
			return
		}
		r2 := r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, tctx))
		end := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.describeWrite(w, r, pattern) })
		chi.Chain(tail...).Handler(end).ServeHTTP(w, r2)
	})
}

// dryAnswer writes a dry run's answer.
func dryAnswer(w http.ResponseWriter, r *http.Request, would string, extra map[string]any) {
	out := map[string]any{"dry_run": true, "method": r.Method, "path": r.URL.Path, "would": would}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, 200, out)
}

const trashWords = "move %s to the trash for 7 days (restore it with POST /api/admin/trash/{id}/restore)"

// describeWrite is the dry run's stand in for a route's handler.
func (s *Server) describeWrite(w http.ResponseWriter, r *http.Request, pattern string) {
	ctx := r.Context()
	switch r.Method + " " + pattern {
	case "POST /api/admin/services":
		var in serviceInput
		if !decode(w, r, &in) {
			return
		}
		prov := "generic"
		if in.Provider != nil {
			prov = *in.Provider
		}
		p, err := s.Store.GetProvider(ctx, prov)
		if err != nil {
			writeErr(w, 400, "unknown_provider", "no provider named "+prov)
			return
		}
		sv, err := catalog.ServiceDefaults(p)
		if err != nil {
			writeErr(w, 400, "bad_provider", "provider spec is unreadable: "+err.Error())
			return
		}
		if in.Slug == nil {
			sv.Slug = catalog.UniqueServiceSlug(ctx, s.Store, sv.Slug)
		}
		if msg := in.apply(sv); msg != "" {
			writeErr(w, 400, "invalid", msg)
			return
		}
		if !s.checkServiceRefs(w, r, sv, "") {
			return
		}
		if msg := vouchErr(r, in); msg != "" {
			writeErr(w, 403, "forbidden", msg)
			return
		}
		dryAnswer(w, r, fmt.Sprintf("create service %s (%s) from provider %s, calling %s", sv.Slug, sv.Name, sv.Provider, sv.BaseURL),
			map[string]any{"service": map[string]any{"slug": sv.Slug, "name": sv.Name, "provider": sv.Provider, "base_url": sv.BaseURL}})

	case "DELETE /api/admin/services/{id}":
		sv, ok := s.loadService(w, r)
		if !ok {
			return
		}
		all, _ := s.Store.ListKeys(ctx, sv.ID, true)
		if !isSuper(r) {
			for _, k := range all {
				if k.Hidden {
					writeErr(w, 403, "forbidden", "this service holds keys only a superadmin can remove")
					return
				}
			}
		}
		what := fmt.Sprintf("service %s (%s) and its %d keys", sv.Slug, sv.Name, len(all))
		if sv.PeerLinkID != nil {
			dryAnswer(w, r, "delete "+what+" for good: it comes from another server, so it skips the trash", nil)
			return
		}
		dryAnswer(w, r, fmt.Sprintf(trashWords, what), map[string]any{"keys": len(all)})

	case "POST /api/admin/services/{id}/keys":
		s.describeAddKeys(w, r)

	case "DELETE /api/admin/keys/{id}":
		k, sv, ok := s.loadKey(w, r)
		if !ok {
			return
		}
		what := fmt.Sprintf("key %d (%s, %s) of service %s", k.ID, k.Label, k.Hint, sv.Slug)
		if k.IsHandedOver() || sv.PeerLinkID != nil {
			dryAnswer(w, r, "delete "+what+" for good: it comes from another server, so it skips the trash", nil)
			return
		}
		dryAnswer(w, r, fmt.Sprintf(trashWords, what), nil)

	case "POST /api/admin/apps":
		var in appInput
		if !decode(w, r, &in) {
			return
		}
		a := &store.App{Mode: "both", Active: true}
		if code, msg := in.apply(r, a); code != 0 {
			writeErr(w, code, "invalid", msg)
			return
		}
		dryAnswer(w, r, fmt.Sprintf("create app %s (mode %s) with a first token named Default, shown once", a.Name, a.Mode), nil)

	case "DELETE /api/admin/apps/{id}":
		a, ok := s.tokenApp(w, r)
		if !ok {
			return
		}
		dryAnswer(w, r, fmt.Sprintf(trashWords, fmt.Sprintf("app %s (id %d)", a.Name, a.ID))+
			fmt.Sprintf("; its %d tokens stop working until it is restored", a.TokenCount), nil)

	case "POST /api/admin/tokens":
		var in struct {
			Name          string `json:"name"`
			Scope         string `json:"scope"`
			ExpiresInDays int    `json:"expires_in_days"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		role, ok := store.ScopeRole(in.Scope)
		switch {
		case in.Name == "" || len(in.Name) > 100:
			writeErr(w, 400, "invalid", "name is required (at most 100 characters)")
		case !ok:
			writeErr(w, 400, "invalid", "scope must be read, write or admin")
		case !userFrom(r).Role.AtLeast(role):
			writeErr(w, 403, "forbidden", "a token cannot have more access than you")
		case in.ExpiresInDays < 0 || in.ExpiresInDays > 3650:
			writeErr(w, 400, "invalid", "expires_in_days must be 0 (never) to 3650")
		default:
			dryAnswer(w, r, fmt.Sprintf("create admin API token %s with scope %s (acts as %s), shown once", in.Name, in.Scope, role), nil)
		}

	case "DELETE /api/admin/tokens/{id}":
		id, ok := idParam(w, r)
		if !ok {
			return
		}
		t, err := s.Store.GetToken(ctx, id)
		if err == nil && t.UserID != userFrom(r).ID && !isSuper(r) {
			err = store.ErrNotFound
		}
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		dryAnswer(w, r, fmt.Sprintf("revoke admin API token %s (%s); it stops working at once", t.Name, t.TokenPrefix), nil)

	case "POST /api/admin/apps/{id}/tokens":
		a, ok := s.tokenApp(w, r)
		if !ok {
			return
		}
		var in struct {
			Name          string            `json:"name"`
			ExpiresInDays *int              `json:"expires_in_days"`
			ReadOnly      bool              `json:"read_only"`
			Limits        store.AppLimits   `json:"limits"`
			PinnedTags    map[string]string `json:"pinned_tags"`
		}
		if !decode(w, r, &in) {
			return
		}
		name, msg := appTokenName(in.Name)
		if msg == "" {
			msg = tokenControlsError(in.Limits, in.PinnedTags)
		}
		if msg == "" && in.ExpiresInDays != nil && (*in.ExpiresInDays < 1 || *in.ExpiresInDays > 3650) {
			msg = "expires_in_days must be 1 to 3650"
		}
		if msg != "" {
			writeErr(w, 400, "invalid", msg)
			return
		}
		would := fmt.Sprintf("add token %s to app %s, shown once; its other tokens keep working", name, a.Name)
		if a.LeaseHidden && !isSuper(r) {
			would += "; the app loses access to hidden keys, since a superadmin did not make the token"
		}
		dryAnswer(w, r, would, nil)

	case "DELETE /api/admin/apps/{id}/tokens/{tokenId}":
		a, ok := s.tokenApp(w, r)
		if !ok {
			return
		}
		tid, ok := tokenIDParam(w, r)
		if !ok {
			return
		}
		t, err := s.Store.GetAppToken(ctx, a.ID, tid)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		dryAnswer(w, r, fmt.Sprintf("revoke token %s (%s) of app %s; it stops working at once and the app's other tokens keep working", t.Name, t.Prefix, a.Name), nil)

	case "POST /api/admin/shares":
		var in shareInput
		if !decode(w, r, &in) {
			return
		}
		sh := &store.Share{}
		if msg := s.applyShareInput(r, in, sh); msg != "" {
			writeErr(w, 400, shareInputCode(msg), msg)
			return
		}
		who := "another server, through an invite"
		if sh.IsPerson() {
			who = "a person, through a claim link"
		}
		dryAnswer(w, r, fmt.Sprintf("create share %s of services [%s] and providers [%s] for %s",
			sh.Name, strings.Join(sh.Services, ", "), strings.Join(sh.Providers, ", "), who), nil)

	case "POST /api/admin/shares/{id}/revoke":
		sh, ok := s.loadShare(w, r)
		if !ok {
			return
		}
		if sh.Status == store.ShareRevoked {
			writeErr(w, 409, "share_revoked", "this share is already revoked")
			return
		}
		would := fmt.Sprintf("revoke share %s; whoever holds it loses access at once, for good", sh.Name)
		if sh.IsHandover() {
			would += ", and the receiving server is told to delete the keys it was handed"
		}
		dryAnswer(w, r, would, nil)

	case "POST /api/admin/trash/{id}/restore":
		it, ok := s.loadTrash(w, r)
		if !ok {
			return
		}
		dryAnswer(w, r, "restore "+it.Kind+" "+trashTarget(it)+" as it was", map[string]any{"trash": it})

	case "DELETE /api/admin/trash/{id}":
		it, ok := s.loadTrash(w, r)
		if !ok {
			return
		}
		dryAnswer(w, r, "delete "+it.Kind+" "+trashTarget(it)+" for good, now; it can no longer be restored", map[string]any{"trash": it})

	default:
		s.describeGeneric(w, r, pattern)
	}
}

// describeAddKeys checks keys as createKeys does and counts what would be
// added and what skipped as already in the service. Secrets stay in memory.
func (s *Server) describeAddKeys(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var body struct {
		Keys []keyInput `json:"keys"`
		Text string     `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	auth := serviceAuth(sv)
	parsedText := false
	if strings.TrimSpace(body.Text) != "" && auth.MultiPart() {
		body.Keys = append(body.Keys, multiPartLines(auth, body.Text)...)
		body.Text = ""
	}
	if strings.TrimSpace(body.Text) != "" {
		parsedText = true
		res := keyparse.Parse(body.Text)
		for _, k := range res.Keys {
			sec := k.Secret
			body.Keys = append(body.Keys, keyInput{Secret: &sec})
		}
		if res.Overflow > 0 {
			body.Keys = append(body.Keys, make([]keyInput, res.Overflow)...)
		}
	}
	switch {
	case len(body.Keys) == 0 && parsedText:
		writeErr(w, 400, "invalid", "no keys found in the text")
		return
	case len(body.Keys) == 0:
		writeErr(w, 400, "invalid", "send between 1 and 5000 keys")
		return
	case len(body.Keys) > maxKeysPerRequest:
		writeErr(w, 400, "invalid", fmt.Sprintf("send at most %d keys per request (got %d)", maxKeysPerRequest, len(body.Keys)))
		return
	}
	fps := []string{}
	seen := map[string]bool{}
	repeats := 0
	for i, in := range body.Keys {
		if in.Secret == nil && len(in.Parts) == 0 {
			writeErr(w, 400, "invalid", fmt.Sprintf("key %d has no secret", i+1))
			return
		}
		secret, err := auth.NormalizeSecret(deref(in.Secret), in.Parts)
		if err != nil {
			writeErr(w, 400, "invalid", fmt.Sprintf("key %d: %s", i+1, err.Error()))
			return
		}
		in.Secret, in.Parts = &secret, nil
		if code, msg := s.validateKey(in, r); code != 0 {
			writeErr(w, code, "invalid", msg)
			return
		}
		fp := s.Pool.Box.Fingerprint(strings.TrimSpace(secret))
		if seen[fp] {
			repeats++
			continue
		}
		seen[fp] = true
		fps = append(fps, fp)
	}
	have, err := s.Store.KeyFingerprintsIn(r.Context(), sv.ID, fps)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	add := len(fps) - len(have)
	skip := len(have) + repeats
	dryAnswer(w, r, fmt.Sprintf("add %d keys to service %s and skip %d already there; new keys are health checked in the background", add, sv.Slug, skip),
		map[string]any{"add": add, "skip": skip})
}

// genericVerbs say what a method does in a sentence.
var genericVerbs = map[string]string{
	http.MethodPost: "send", http.MethodPut: "replace", http.MethodPatch: "change", http.MethodDelete: "delete",
}

// describeGeneric covers every write without a describer of its own: the
// body must parse and the target must exist.
func (s *Server) describeGeneric(w http.ResponseWriter, r *http.Request, pattern string) {
	if r.ContentLength != 0 && r.Body != nil && r.Body != http.NoBody {
		var body any
		if !decode(w, r, &body) {
			return
		}
	}
	target, ok := s.resolveTarget(w, r, pattern)
	if !ok {
		return
	}
	would := genericVerbs[r.Method] + " " + r.Method + " " + r.URL.Path
	extra := map[string]any{"checked": "permissions, body and target; the route's own checks of the body's values run only on the real call"}
	if target != "" {
		would += " (" + target + ")"
		extra["target"] = target
	}
	dryAnswer(w, r, would, extra)
}

// resolveTarget finds the item a route's {id} names, for the routes whose
// id is a service, key, app or share. Others report "".
func (s *Server) resolveTarget(w http.ResponseWriter, r *http.Request, pattern string) (string, bool) {
	ctx := r.Context()
	rest, ok := strings.CutPrefix(pattern, "/api/admin/")
	if !ok || !strings.Contains(rest, "{id}") {
		return "", true
	}
	kind, _, _ := strings.Cut(rest, "/")
	switch kind {
	case "services":
		sv, ok := s.loadService(w, r)
		if !ok {
			return "", false
		}
		return "service " + sv.Slug, true
	case "keys":
		k, sv, ok := s.loadKey(w, r)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("key %d (%s) of service %s", k.ID, k.Label, sv.Slug), true
	case "apps":
		a, ok := s.tokenApp(w, r)
		if !ok {
			return "", false
		}
		return "app " + a.Name, true
	case "shares":
		sh, ok := s.loadShare(w, r)
		if !ok {
			return "", false
		}
		return "share " + sh.Name, true
	case "users":
		id, ok := idParam(w, r)
		if !ok {
			return "", false
		}
		u, err := s.Store.GetUser(ctx, id)
		if err != nil {
			writeStoreErr(w, err)
			return "", false
		}
		return "user " + u.Email, true
	}
	return "", true
}
