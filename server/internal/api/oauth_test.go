package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testRedirect = "http://127.0.0.1:4567/callback"

// oauthFlow drives one client through the authorization server.
type oauthFlow struct {
	e        *env
	clientID string
	verifier string
	redirect string
}

func noFollow() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func registerClient(t *testing.T, e *env, redirects ...string) *oauthFlow {
	t.Helper()
	if len(redirects) == 0 {
		redirects = []string{testRedirect}
	}
	var out map[string]any
	e.do(http.DefaultClient, "POST", "/oauth/register", map[string]any{"client_name": "Test Client", "redirect_uris": redirects}, 201, &out)
	if out["token_endpoint_auth_method"] != "none" || !strings.HasPrefix(out["client_id"].(string), oauthClientPrefix) {
		t.Fatalf("register: %v", out)
	}
	return &oauthFlow{e: e, clientID: out["client_id"].(string), verifier: strings.Repeat("v", 50), redirect: redirects[0]}
}

func (f *oauthFlow) challenge() string {
	sum := sha256.Sum256([]byte(f.verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize starts an authorization request and returns the response.
func (f *oauthFlow) authorize(t *testing.T, extra url.Values) *http.Response {
	t.Helper()
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {f.clientID},
		"redirect_uri":          {f.redirect},
		"code_challenge":        {f.challenge()},
		"code_challenge_method": {"S256"},
		"state":                 {"st4te"},
		"resource":              {f.e.srv.URL + "/mcp"},
		"scope":                 {"mcp"},
	}
	for k, v := range extra {
		q[k] = v
	}
	for k, v := range q {
		if len(v) == 1 && v[0] == "" {
			delete(q, k)
		}
	}
	resp, err := noFollow().Get(f.e.srv.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// requestID runs authorize and returns the consent request id.
func (f *oauthFlow) requestID(t *testing.T) string {
	t.Helper()
	resp := f.authorize(t, nil)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.HasPrefix(loc, "/oauth/consent?request=") {
		t.Fatalf("authorize: %d %q", resp.StatusCode, loc)
	}
	u, _ := url.Parse(loc)
	return u.Query().Get("request")
}

// approve signs in as the superadmin and approves, returning the code.
func (f *oauthFlow) approve(t *testing.T, body map[string]any) string {
	t.Helper()
	rid := f.requestID(t)
	var info map[string]any
	f.e.do(f.e.super, "GET", "/api/oauth/requests/"+rid, nil, 200, &info)
	if info["can_approve"] != true || info["request"].(map[string]any)["client_name"] != "Test Client" {
		t.Fatalf("request info: %v", info)
	}
	var out map[string]any
	f.e.do(f.e.super, "POST", "/api/oauth/requests/"+rid+"/approve", body, 200, &out)
	u, err := url.Parse(out["redirect"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") != "st4te" || q.Get("iss") != f.e.srv.URL || !strings.HasPrefix(u.String(), f.redirect) {
		t.Fatalf("approve redirect: %s", u)
	}
	return q.Get("code")
}

func postForm(t *testing.T, e *env, path string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(e.srv.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func (f *oauthFlow) exchange(t *testing.T, code string, override url.Values) (int, map[string]any) {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {f.clientID},
		"redirect_uri":  {f.redirect},
		"code_verifier": {f.verifier},
		"resource":      {f.e.srv.URL + "/mcp"},
	}
	for k, v := range override {
		form[k] = v
	}
	return postForm(t, f.e, "/oauth/token", form)
}

func (f *oauthFlow) refresh(t *testing.T, rt string) (int, map[string]any) {
	t.Helper()
	return postForm(t, f.e, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {f.clientID}})
}

// connect runs the whole flow and returns the token response.
func (f *oauthFlow) connect(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	code := f.approve(t, body)
	st, tok := f.exchange(t, code, nil)
	if st != 200 || !strings.HasPrefix(fmt.Sprint(tok["access_token"]), oauthAccessPrefix) || tok["token_type"] != "Bearer" {
		t.Fatalf("token: %d %v", st, tok)
	}
	return tok
}

// mcpStatus posts tools/list with a bearer token and returns the status and
// WWW-Authenticate.
func mcpStatus(t *testing.T, e *env, tok string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
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

func TestOAuthDiscovery(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		var prm map[string]any
		e.do(http.DefaultClient, "GET", p, nil, 200, &prm)
		if prm["resource"] != e.srv.URL+"/mcp" || prm["authorization_servers"].([]any)[0] != e.srv.URL {
			t.Fatalf("%s: %v", p, prm)
		}
	}
	var as map[string]any
	e.do(http.DefaultClient, "GET", "/.well-known/oauth-authorization-server", nil, 200, &as)
	if as["issuer"] != e.srv.URL || as["token_endpoint"] != e.srv.URL+"/oauth/token" ||
		fmt.Sprint(as["code_challenge_methods_supported"]) != "[S256]" || as["client_id_metadata_document_supported"] != true {
		t.Fatalf("as metadata: %v", as)
	}
	// A 401 from /mcp points at the metadata, and says invalid_token once
	// a token was sent.
	st, h := mcpStatus(t, e, "")
	if st != 401 || !strings.Contains(h, `resource_metadata="`+e.srv.URL+`/.well-known/oauth-protected-resource/mcp"`) || strings.Contains(h, "invalid_token") {
		t.Fatalf("no token: %d %q", st, h)
	}
	st, h = mcpStatus(t, e, "acno_nope")
	if st != 401 || !strings.Contains(h, `error="invalid_token"`) {
		t.Fatalf("bad token: %d %q", st, h)
	}
	// Plain app tokens still work.
	_, appTok := e.app(map[string]any{"name": "plain"})
	if st, _ := mcpStatus(t, e, appTok); st != 200 {
		t.Fatalf("app token: %d", st)
	}
}

func TestOAuthFullFlow(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "claude"})
	f := registerClient(t, e)
	tok := f.connect(t, map[string]any{"app_id": appID})
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	names := (&mcpClient{e: e, tok: access}).toolNames()
	if !has(names, "get_key") || !has(names, "list_services") {
		t.Fatalf("tools: %v", names)
	}
	// The access token works only on the resource it was issued for.
	resp := e.do(http.DefaultClient, "GET", "/proxy/serpapi/search?q=x", nil, 401, nil, "Authorization", "Bearer "+access)
	resp.Body.Close()

	var conns struct {
		Connections []map[string]any `json:"connections"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/oauth/apps/%d/connections", appID), nil, 200, &conns)
	if len(conns.Connections) != 1 || conns.Connections[0]["client_name"] != "Test Client" || conns.Connections[0]["user_email"] != "root@x.io" {
		t.Fatalf("connections: %v", conns)
	}

	// Refresh rotates: a new pair, and the old access token stops.
	st, tok2 := f.refresh(t, refresh)
	if st != 200 || tok2["refresh_token"] == refresh {
		t.Fatalf("refresh: %d %v", st, tok2)
	}
	access2 := tok2["access_token"].(string)
	if st, _ := mcpStatus(t, e, access); st != 401 {
		t.Fatalf("old access token after refresh: %d", st)
	}
	if st, _ := mcpStatus(t, e, access2); st != 200 {
		t.Fatalf("new access token: %d", st)
	}
	// The spent refresh token coming back revokes the whole family.
	if st, out := f.refresh(t, refresh); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("reuse: %d %v", st, out)
	}
	if st, _ := mcpStatus(t, e, access2); st != 401 {
		t.Fatalf("access token after reuse: %d", st)
	}
	if st, _ := f.refresh(t, tok2["refresh_token"].(string)); st != 400 {
		t.Fatalf("newest refresh after reuse: %d", st)
	}
	var audit struct {
		Audit []map[string]any `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	actions := ""
	for _, a := range audit.Audit {
		actions += fmt.Sprint(a["action"]) + " "
	}
	if !strings.Contains(actions, "oauth.grant") || !strings.Contains(actions, "oauth.refresh_reuse") {
		t.Fatalf("audit: %s", actions)
	}

	// A fresh connection stops when the app is disabled, and again when it
	// is deleted.
	tok3 := f.connect(t, map[string]any{"app_id": appID})
	access3 := tok3["access_token"].(string)
	if st, _ := mcpStatus(t, e, access3); st != 200 {
		t.Fatalf("third: %d", st)
	}
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/apps/%d", appID), map[string]any{"active": false}, 200, nil)
	if st, _ := mcpStatus(t, e, access3); st != 403 {
		t.Fatalf("disabled app: %d", st)
	}
	if st, _ := f.refresh(t, tok3["refresh_token"].(string)); st != 400 {
		t.Fatalf("refresh for a disabled app: %d", st)
	}
	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/apps/%d", appID), nil, 200, nil)
	if st, _ := mcpStatus(t, e, access3); st != 401 {
		t.Fatalf("deleted app: %d", st)
	}
}

func TestOAuthRevoke(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "claude"})
	f := registerClient(t, e)
	tok := f.connect(t, map[string]any{"app_id": appID})
	if st, _ := postForm(t, e, "/oauth/revoke", url.Values{"token": {tok["refresh_token"].(string)}, "client_id": {f.clientID}}); st != 200 {
		t.Fatalf("revoke: %d", st)
	}
	if st, _ := mcpStatus(t, e, tok["access_token"].(string)); st != 401 {
		t.Fatalf("after revoke: %d", st)
	}
	// From the dashboard.
	tok = f.connect(t, map[string]any{"app_id": appID})
	var conns struct {
		Connections []map[string]any `json:"connections"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/oauth/apps/%d/connections", appID), nil, 200, &conns)
	live := conns.Connections[0]
	if live["revoked_at"] != nil {
		t.Fatalf("first connection should be live: %v", conns)
	}
	e.do(e.super, "DELETE", fmt.Sprintf("/api/oauth/apps/%d/connections/%s", appID, live["id"]), nil, 200, nil)
	if st, _ := mcpStatus(t, e, tok["access_token"].(string)); st != 401 {
		t.Fatalf("after dashboard revoke: %d", st)
	}
}

func TestOAuthNewAppAndReadOnly(t *testing.T) {
	e := newEnv(t)
	f := registerClient(t, e)
	tok := f.connect(t, map[string]any{"new_app": "ChatGPT", "read_only": true})
	names := (&mcpClient{e: e, tok: tok["access_token"].(string)}).toolNames()
	if !has(names, "list_services") {
		t.Fatalf("read only tools: %v", names)
	}
	for _, w := range []string{"get_key", "renew_key", "report_key", "proxy_request"} {
		if has(names, w) {
			t.Fatalf("read only grant lists %s: %v", w, names)
		}
	}
	var apps struct {
		Apps []map[string]any `json:"apps"`
	}
	e.do(e.super, "GET", "/api/admin/apps", nil, 200, &apps)
	found := false
	for _, a := range apps.Apps {
		found = found || a["name"] == "ChatGPT"
	}
	if !found {
		t.Fatalf("new app missing: %v", apps)
	}
}

func TestOAuthRefusals(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "claude"})
	f := registerClient(t, e)

	// Registration refuses non loopback http and fragments.
	for _, bad := range []string{"http://evil.example/cb", "https://ok.example/cb#frag", "javascript:alert(1)"} {
		e.do(http.DefaultClient, "POST", "/oauth/register", map[string]any{"client_name": "x", "redirect_uris": []string{bad}}, 400, nil)
	}

	// An unregistered redirect never gets a redirect.
	resp := f.authorize(t, url.Values{"redirect_uri": {"https://evil.example/cb"}})
	if resp.StatusCode != 400 || resp.Header.Get("Location") != "" {
		t.Fatalf("redirect mismatch: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// A loopback redirect may change its port.
	resp = f.authorize(t, url.Values{"redirect_uri": {"http://127.0.0.1:9999/callback"}})
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), "/oauth/consent") {
		t.Fatalf("loopback port: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Errors once the redirect is trusted go back to the client.
	for name, extra := range map[string]url.Values{
		"invalid_target":  {"resource": {"https://elsewhere.example/mcp"}},
		"invalid_request": {"code_challenge_method": {"plain"}},
	} {
		resp := f.authorize(t, extra)
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != 302 || loc.Query().Get("error") != name || loc.Query().Get("state") != "st4te" {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, loc)
		}
	}
	resp = f.authorize(t, url.Values{"code_challenge": {""}})
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("missing challenge: %s", loc)
	}

	// PKCE failure.
	code := f.approve(t, map[string]any{"app_id": appID})
	if st, out := f.exchange(t, code, url.Values{"code_verifier": {strings.Repeat("w", 50)}}); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("pkce: %d %v", st, out)
	}
	// Redirect mismatch at the token endpoint.
	if st, out := f.exchange(t, code, url.Values{"redirect_uri": {"http://127.0.0.1:1/other"}}); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("token redirect: %d %v", st, out)
	}
	// Resource mismatch at the token endpoint.
	if st, out := f.exchange(t, code, url.Values{"resource": {"https://elsewhere.example/mcp"}}); st != 400 || out["error"] != "invalid_target" {
		t.Fatalf("token resource: %d %v", st, out)
	}
	// Another client cannot use it.
	g := registerClient(t, e)
	if st, _ := f.exchange(t, code, url.Values{"client_id": {g.clientID}}); st != 400 {
		t.Fatalf("other client: %d", st)
	}
	// The right request still works, once.
	st, tok := f.exchange(t, code, nil)
	if st != 200 {
		t.Fatalf("exchange: %d %v", st, tok)
	}
	if st, _ := f.exchange(t, code, nil); st != 400 {
		t.Fatalf("second use: %d", st)
	}
	// Using a code twice revokes what the first use got.
	if st, _ := mcpStatus(t, e, tok["access_token"].(string)); st != 401 {
		t.Fatalf("tokens after code reuse: %d", st)
	}

	// Expired code.
	code = f.approve(t, map[string]any{"app_id": appID})
	if _, err := e.st.DB.Exec(`UPDATE oauth_codes SET expires_at=?`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if st, out := f.exchange(t, code, nil); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("expired: %d %v", st, out)
	}

	// Deny goes back with access_denied.
	rid := f.requestID(t)
	var out map[string]any
	e.do(e.super, "POST", "/api/oauth/requests/"+rid+"/deny", nil, 200, &out)
	if u, _ := url.Parse(out["redirect"].(string)); u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "st4te" {
		t.Fatalf("deny: %v", out)
	}
	// A request is used once.
	e.do(e.super, "POST", "/api/oauth/requests/"+rid+"/approve", map[string]any{"app_id": appID}, 404, nil)
	// The consent API needs the CSRF header like the rest of the dashboard.
	rid = f.requestID(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/oauth/requests/"+rid+"/approve", strings.NewReader("app_id=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2, err := e.super.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode < 400 {
		t.Fatalf("form post to approve: %d", r2.StatusCode)
	}
}

func TestOAuthClientMetadataDocument(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "claude"})
	var docURL string
	doc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"client_id": docURL, "client_name": "Test Client", "redirect_uris": []string{testRedirect},
			"token_endpoint_auth_method": "none"})
	}))
	t.Cleanup(doc.Close)
	docURL = doc.URL + "/client.json"
	e.api.oauthSt().fetch = doc.Client()
	f := &oauthFlow{e: e, clientID: docURL, verifier: strings.Repeat("m", 60), redirect: testRedirect}
	tok := f.connect(t, map[string]any{"app_id": appID})
	if st, _ := mcpStatus(t, e, tok["access_token"].(string)); st != 200 {
		t.Fatalf("cimd access: %d", st)
	}
	// A document whose client_id differs from its URL is refused.
	g := &oauthFlow{e: e, clientID: doc.URL + "/other.json", verifier: f.verifier, redirect: testRedirect}
	if resp := g.authorize(t, nil); resp.StatusCode != 400 {
		t.Fatalf("mismatched document: %d", resp.StatusCode)
	}
}
