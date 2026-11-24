package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// controlURL is the control MCP's resource for e.
func controlURL(e *env) string { return e.srv.URL + controlPath }

// controlCode runs a control MCP authorization as the person signed in on c
// and approves it with scope, returning the code.
func (f *oauthFlow) controlCode(t *testing.T, c *http.Client, scope string) string {
	t.Helper()
	resp := f.authorize(t, url.Values{"resource": {controlURL(f.e)}, "scope": {oauthControlScope}})
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Path != "/oauth/consent" {
		t.Fatalf("authorize: %d %s", resp.StatusCode, loc)
	}
	rid := loc.Query().Get("request")
	var info struct {
		Request    map[string]any   `json:"request"`
		Apps       []map[string]any `json:"apps"`
		Scopes     []string         `json:"scopes"`
		CanApprove bool             `json:"can_approve"`
	}
	f.e.do(c, "GET", "/api/oauth/requests/"+rid, nil, 200, &info)
	if info.Request["kind"] != "control" || info.Request["scope"] != oauthControlScope || len(info.Apps) != 0 || !info.CanApprove {
		t.Fatalf("request info: %+v", info)
	}
	var out map[string]any
	f.e.do(c, "POST", "/api/oauth/requests/"+rid+"/approve", map[string]any{"scope": scope}, 200, &out)
	u, _ := url.Parse(out["redirect"].(string))
	return u.Query().Get("code")
}

// controlConnect runs the whole control flow and returns the token response.
func (f *oauthFlow) controlConnect(t *testing.T, c *http.Client, scope string) map[string]any {
	t.Helper()
	st, tok := f.exchange(t, f.controlCode(t, c, scope), url.Values{"resource": {controlURL(f.e)}})
	if st != 200 || tok["scope"] != oauthControlScope || !strings.HasPrefix(fmt.Sprint(tok["access_token"]), oauthAccessPrefix) {
		t.Fatalf("token: %d %v", st, tok)
	}
	return tok
}

// controlStatus posts tools/list to /mcp/control and returns the status and
// WWW-Authenticate.
func controlStatus(t *testing.T, e *env, tok string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+controlPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("WWW-Authenticate")
}

// signedIn adds a user with a role and returns a client signed in as them.
func signedIn(e *env, email string, role store.Role) (*http.Client, int64) {
	var u store.User
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": email, "password": "userpass12345", "role": role}, 201, &u)
	c := client()
	e.do(c, "POST", "/api/auth/login", map[string]any{"email": email, "password": "userpass12345"}, 200, nil)
	return c, u.ID
}

func TestControlOAuthDiscovery(t *testing.T) {
	e := newEnv(t)
	var prm map[string]any
	e.do(http.DefaultClient, "GET", "/.well-known/oauth-protected-resource/mcp/control", nil, 200, &prm)
	if prm["resource"] != controlURL(e) || fmt.Sprint(prm["scopes_supported"]) != "[mcp:control]" {
		t.Fatalf("control metadata: %v", prm)
	}
	var as map[string]any
	e.do(http.DefaultClient, "GET", "/.well-known/oauth-authorization-server", nil, 200, &as)
	if fmt.Sprint(as["scopes_supported"]) != "[mcp mcp:control]" {
		t.Fatalf("scopes: %v", as["scopes_supported"])
	}
	want := `resource_metadata="` + e.srv.URL + `/.well-known/oauth-protected-resource/mcp/control"`
	if st, h := controlStatus(t, e, ""); st != 401 || !strings.Contains(h, want) || strings.Contains(h, "invalid_token") {
		t.Fatalf("no token: %d %q", st, h)
	}
	if st, h := controlStatus(t, e, "acno_nope"); st != 401 || !strings.Contains(h, want) || !strings.Contains(h, `error="invalid_token"`) {
		t.Fatalf("bad token: %d %q", st, h)
	}
	// Admin API tokens keep working.
	if st, _ := controlStatus(t, e, adminToken(e, "read")); st != 200 {
		t.Fatalf("admin token: %d", st)
	}
	// The scope alone picks the control MCP when no resource is sent, and
	// asking for it on /mcp is refused.
	f := registerClient(t, e)
	resp := f.authorize(t, url.Values{"resource": {""}, "scope": {"mcp mcp:control"}})
	if loc := resp.Header.Get("Location"); resp.StatusCode != 302 || !strings.HasPrefix(loc, "/oauth/consent") {
		t.Fatalf("scope only: %d %q", resp.StatusCode, loc)
	}
	resp = f.authorize(t, url.Values{"scope": {oauthControlScope}})
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Query().Get("error") != "invalid_scope" {
		t.Fatalf("control scope on /mcp: %s", loc)
	}
}

func TestControlOAuthFullFlow(t *testing.T) {
	e := newEnv(t)
	f := registerClient(t, e)
	tok := f.controlConnect(t, e.super, "write")
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	c := &ctlClient{e: e, tok: access}
	c.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "claude-ai", "version": "1"}})
	names := c.names()
	// write acts as an admin: service tools yes, superadmin tools no.
	if !has(names, "list_services") || !has(names, "create_service") || has(names, "list_users") {
		t.Fatalf("tools: %v", names)
	}
	if _, text, isErr := c.tool("create_service", map[string]any{"provider": "serpapi", "slug": "viaoauth", "base_url": e.upURL}); isErr {
		t.Fatalf("create_service: %s", text)
	}
	if _, err := e.st.GetServiceBySlug(t.Context(), "viaoauth"); err != nil {
		t.Fatalf("service not created: %v", err)
	}

	// The change is audited as root, via oauth, with the grant in the client.
	var conns struct {
		Connections []store.OAuthGrant `json:"connections"`
	}
	e.do(e.super, "GET", "/api/oauth/control/connections", nil, 200, &conns)
	if len(conns.Connections) != 1 || conns.Connections[0].Access != "write" || conns.Connections[0].AppID != 0 || conns.Connections[0].UserEmail != "root@x.io" {
		t.Fatalf("connections: %+v", conns)
	}
	grant := conns.Connections[0].ID
	var audit struct {
		Audit []map[string]any `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit?via=oauth&action=service.create", nil, 200, &audit)
	if len(audit.Audit) != 1 || audit.Audit[0]["user_email"] != "root@x.io" ||
		!strings.HasPrefix(fmt.Sprint(audit.Audit[0]["client"]), oauthGrantPrefix+grant+" claude-ai") {
		t.Fatalf("audit: %v", audit.Audit)
	}

	// The token is for /mcp/control only: not the admin API, the app MCP or
	// the proxy.
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 401, nil, "Authorization", "Bearer "+access)
	if st, _ := mcpStatus(t, e, access); st != 401 {
		t.Fatalf("control token on /mcp: %d", st)
	}
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search?q=x", nil, 401, nil, "Authorization", "Bearer "+access)
	// Inside a control MCP call it stands in for an admin API token, so
	// sessionOnly routes refuse it like one.
	req := httptest.NewRequest("PATCH", "/api/admin/me", strings.NewReader(`{"name":"x"}`))
	req = req.WithContext(context.WithValue(req.Context(), controlOAuthKey{}, true))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.api.handler().ServeHTTP(rec, req)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "session_required") {
		t.Fatalf("sessionOnly in process: %d %s", rec.Code, rec.Body)
	}

	// Refresh rotates; a spent refresh token coming back revokes the family.
	st, tok2 := f.refresh(t, refresh)
	if st != 200 || tok2["scope"] != oauthControlScope {
		t.Fatalf("refresh: %d %v", st, tok2)
	}
	access2 := tok2["access_token"].(string)
	if st, _ := controlStatus(t, e, access); st != 401 {
		t.Fatalf("old access token after refresh: %d", st)
	}
	if st, _ := controlStatus(t, e, access2); st != 200 {
		t.Fatalf("new access token: %d", st)
	}
	if st, out := f.refresh(t, refresh); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("reuse: %d %v", st, out)
	}
	if st, _ := controlStatus(t, e, access2); st != 401 {
		t.Fatalf("access token after reuse: %d", st)
	}
	if st, _ := f.refresh(t, tok2["refresh_token"].(string)); st != 400 {
		t.Fatalf("newest refresh after reuse: %d", st)
	}
}

func TestControlOAuthCrossResource(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "claude"})
	f := registerClient(t, e)
	appTok := f.connect(t, map[string]any{"app_id": appID})["access_token"].(string)
	ctlTok := f.controlConnect(t, e.super, "admin")["access_token"].(string)

	if st, h := controlStatus(t, e, appTok); st != 401 || !strings.Contains(h, `error="invalid_token"`) {
		t.Fatalf("/mcp token on /mcp/control: %d %q", st, h)
	}
	if st, h := mcpStatus(t, e, ctlTok); st != 401 || !strings.Contains(h, `error="invalid_token"`) {
		t.Fatalf("/mcp/control token on /mcp: %d %q", st, h)
	}
	if st, _ := mcpStatus(t, e, appTok); st != 200 {
		t.Fatalf("app token on /mcp: %d", st)
	}
	if st, _ := controlStatus(t, e, ctlTok); st != 200 {
		t.Fatalf("control token on /mcp/control: %d", st)
	}
	// A code issued for one resource cannot be swapped for the other.
	code := f.controlCode(t, e.super, "read")
	if st, out := f.exchange(t, code, nil); st != 400 || out["error"] != "invalid_target" {
		t.Fatalf("control code for /mcp: %d %v", st, out)
	}
}

func TestControlOAuthScopeCap(t *testing.T) {
	e := newEnv(t)
	member, _ := signedIn(e, "m@x.io", store.RoleMember)
	admin, _ := signedIn(e, "a@x.io", store.RoleAdmin)
	f := registerClient(t, e)

	// A member may give read only, like the admin API tokens they can make.
	rid := f.requestIDFor(t, controlURL(e))
	var info struct {
		Scopes     []string `json:"scopes"`
		CanApprove bool     `json:"can_approve"`
	}
	e.do(member, "GET", "/api/oauth/requests/"+rid, nil, 200, &info)
	if !info.CanApprove || fmt.Sprint(info.Scopes) != "[read]" {
		t.Fatalf("member: %+v", info)
	}
	e.do(member, "POST", "/api/oauth/requests/"+rid+"/approve", map[string]any{"scope": "write"}, 403, nil)
	e.do(admin, "GET", "/api/oauth/requests/"+rid, nil, 200, &info)
	if fmt.Sprint(info.Scopes) != "[read write]" {
		t.Fatalf("admin scopes: %v", info.Scopes)
	}
	e.do(admin, "POST", "/api/oauth/requests/"+rid+"/approve", map[string]any{"scope": "admin"}, 403, nil)
	e.do(admin, "POST", "/api/oauth/requests/"+rid+"/approve", map[string]any{"scope": "root"}, 400, nil)

	// With no scope the connection is read: it lists read tools and cannot
	// run a write tool, even one the person's own role allows.
	tok := f.controlConnect(t, admin, "")
	c := &ctlClient{e: e, tok: tok["access_token"].(string)}
	if names := c.names(); !has(names, "list_services") || has(names, "create_service") {
		t.Fatalf("read tools: %v", names)
	}
	if _, text, isErr := c.tool("create_service", map[string]any{"provider": "serpapi", "slug": "nope", "base_url": e.upURL}); !isErr || !strings.Contains(text, "role") {
		t.Fatalf("write tool with read scope: %s", text)
	}
	raw := &ctlClient{e: e, tok: c.tok, path: controlPath + "?groups=all"}
	if _, text, isErr := raw.tool("api_request", map[string]any{"method": "POST", "path": "/api/admin/services",
		"body": map[string]any{"provider": "serpapi", "slug": "nope", "base_url": e.upURL}}); !isErr || !strings.Contains(text, "forbidden") {
		t.Fatalf("write route with read scope: %s", text)
	}
	if _, err := e.st.GetServiceBySlug(t.Context(), "nope"); err == nil {
		t.Fatal("a read connection created a service")
	}

	// Each person sees only their own connections; a superadmin sees all.
	var conns struct {
		Connections []store.OAuthGrant `json:"connections"`
	}
	e.do(member, "GET", "/api/oauth/control/connections", nil, 200, &conns)
	if len(conns.Connections) != 0 {
		t.Fatalf("member sees: %+v", conns)
	}
	e.do(admin, "GET", "/api/oauth/control/connections", nil, 200, &conns)
	if len(conns.Connections) != 1 || conns.Connections[0].Access != "read" {
		t.Fatalf("admin sees: %+v", conns)
	}
	id := conns.Connections[0].ID
	e.do(e.super, "GET", "/api/oauth/control/connections", nil, 200, &conns)
	if len(conns.Connections) != 1 {
		t.Fatalf("superadmin sees: %+v", conns)
	}
	e.do(member, "DELETE", "/api/oauth/control/connections/"+id, nil, 404, nil)
	e.do(admin, "DELETE", "/api/oauth/control/connections/"+id, nil, 200, nil)
	if st, _ := controlStatus(t, e, tok["access_token"].(string)); st != 401 {
		t.Fatalf("after revoke: %d", st)
	}
}

func TestControlOAuthDemotion(t *testing.T) {
	e := newEnv(t)
	admin, uid := signedIn(e, "a@x.io", store.RoleAdmin)
	f := registerClient(t, e)
	tok := f.controlConnect(t, admin, "write")
	access := tok["access_token"].(string)
	if st, _ := controlStatus(t, e, access); st != 200 {
		t.Fatalf("before: %d", st)
	}
	// Demoted below the scope: the next use fails and revokes the
	// connection, so refreshing fails too.
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/users/%d", uid), map[string]any{"role": "member"}, 200, nil)
	if st, _ := controlStatus(t, e, access); st != 401 {
		t.Fatalf("after demotion: %d", st)
	}
	if st, out := f.refresh(t, tok["refresh_token"].(string)); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("refresh after demotion: %d %v", st, out)
	}
	g, err := e.st.ListControlGrants(t.Context(), oauthControlScope, uid)
	if err != nil || len(g) != 1 || g[0].RevokedAt == nil {
		t.Fatalf("grant after demotion: %+v %v", g, err)
	}

	// A disabled person's connection stops the same way, refresh first.
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/users/%d", uid), map[string]any{"role": "admin"}, 200, nil)
	tok = f.controlConnect(t, admin, "read")
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/users/%d", uid), map[string]any{"disabled": true}, 200, nil)
	if st, _ := f.refresh(t, tok["refresh_token"].(string)); st != 400 {
		t.Fatalf("refresh when disabled: %d", st)
	}
	if st, _ := controlStatus(t, e, tok["access_token"].(string)); st != 401 {
		t.Fatalf("access when disabled: %d", st)
	}
}

func TestControlOAuthBehindGate(t *testing.T) {
	e := newGateEnv(t)
	// Locked, the answer still says where to sign in.
	st, h := controlStatus(t, e, "")
	if st != 401 || !strings.Contains(h, "/.well-known/oauth-protected-resource/mcp/control") {
		t.Fatalf("locked: %d %q", st, h)
	}
	var prm map[string]any
	e.do(http.DefaultClient, "GET", "/.well-known/oauth-protected-resource/mcp/control", nil, 200, &prm)

	// The consent page sits behind the gate: approve with the password.
	f := registerClient(t, e)
	rid := f.requestIDFor(t, controlURL(e))
	var out map[string]any
	e.do(e.super, "POST", "/api/oauth/requests/"+rid+"/approve", map[string]any{"scope": "write"}, 200, &out, accessHeader, testAccessPassword)
	u, _ := url.Parse(out["redirect"].(string))
	st, tok := f.exchange(t, u.Query().Get("code"), url.Values{"resource": {controlURL(e)}})
	if st != 200 {
		t.Fatalf("token: %d %v", st, tok)
	}
	// The token alone opens /mcp/control and the routes its tools call.
	c := &ctlClient{e: e, tok: tok["access_token"].(string)}
	if _, text, isErr := c.tool("create_service", map[string]any{"provider": "serpapi", "slug": "gated", "base_url": e.upURL}); isErr {
		t.Fatalf("write behind the gate: %s", text)
	}
	// But not the admin API directly.
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 401, nil, "Authorization", "Bearer "+tok["access_token"].(string))
}

// requestIDFor runs authorize for a resource and returns the consent id.
func (f *oauthFlow) requestIDFor(t *testing.T, resource string) string {
	t.Helper()
	resp := f.authorize(t, url.Values{"resource": {resource}, "scope": {""}})
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Path != "/oauth/consent" {
		t.Fatalf("authorize: %d %s", resp.StatusCode, loc)
	}
	return loc.Query().Get("request")
}

func TestControlMCPListsAndRevokesControlConnections(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}
	sc, text, isErr := c.tool("list_control_connections", map[string]any{})
	if isErr {
		t.Fatalf("list_control_connections: %s", text)
	}
	if _, ok := sc["connections"]; !ok {
		t.Fatalf("list_control_connections shape: %s", text)
	}
	appID, _ := e.app(map[string]any{"name": "mcp-app"})
	if _, text, isErr := c.tool("list_app_oauth_connections", map[string]any{"app_id": appID}); isErr {
		t.Fatalf("list_app_oauth_connections: %s", text)
	}
	sc, _, _ = c.tool("revoke_control_connection", map[string]any{"grant_id": "nope"})
	if sc["confirm_required"] != true {
		t.Fatalf("revoke_control_connection without confirm: %v", sc)
	}
}

// Every destructive control tool needs Describe: the confirm step calls it,
// and a nil one panics the request.
func TestEveryDestructiveControlToolDescribes(t *testing.T) {
	for _, tool := range ctlTools {
		if tool.Kind == ctlDestructive && tool.Describe == nil && tool.Name != "api_request" {
			t.Errorf("%s is destructive but has no Describe", tool.Name)
		}
	}
}
