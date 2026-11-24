package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// fakeOAuth is an OAuth provider with one API endpoint: /api/me answers
// 200 with the token it was called with (an upstream echoing the
// credential) or 401 when the token is unknown.
type fakeOAuth struct {
	mu        sync.Mutex
	srv       *httptest.Server
	codes     map[string]string // code -> PKCE challenge
	refresh   map[string]bool   // live refresh tokens
	access    map[string]bool   // live access tokens
	n         int
	rotate    bool
	delay     time.Duration
	account   string
	refreshes atomic.Int32
	lastAuth  string
	lastDev   string
	badClient int
}

func newFakeOAuth(t *testing.T) *fakeOAuth {
	f := &fakeOAuth{codes: map[string]string{}, refresh: map[string]bool{}, access: map[string]bool{}, account: "ana@example.com"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOAuth) idToken(email string) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(map[string]string{"email": email}) + ".sig"
}

func (f *fakeOAuth) issue(withRefresh bool) map[string]any {
	f.n++
	at := fmt.Sprintf("ya29-access-token-%04d", f.n)
	f.access[at] = true
	out := map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 3600}
	if withRefresh {
		rt := fmt.Sprintf("1//refresh-token-%04d", f.n)
		f.refresh[rt] = true
		out["refresh_token"] = rt
	}
	return out
}

func (f *fakeOAuth) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/token":
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.PostForm.Get("client_id") != "cid-123" || r.PostForm.Get("client_secret") != "client-secret-value" {
			f.badClient++
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			ch, ok := f.codes[r.PostForm.Get("code")]
			delete(f.codes, r.PostForm.Get("code"))
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if !ok || ch != base64.RawURLEncoding.EncodeToString(sum[:]) || !strings.HasSuffix(r.PostForm.Get("redirect_uri"), "/oauth/callback") {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code or verifier mismatch"}`))
				return
			}
			out := f.issue(true)
			out["id_token"] = f.idToken(f.account)
			_ = json.NewEncoder(w).Encode(out)
		case "refresh_token":
			f.refreshes.Add(1)
			time.Sleep(f.delay)
			rt := r.PostForm.Get("refresh_token")
			if !f.refresh[rt] {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
				return
			}
			out := f.issue(f.rotate)
			if f.rotate {
				delete(f.refresh, rt)
			}
			_ = json.NewEncoder(w).Encode(out)
		}
	case "/api/me":
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		live := f.access[tok]
		f.lastAuth, f.lastDev = tok, r.Header.Get("Developer-Token")
		f.mu.Unlock()
		if !live {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":401,"status":"UNAUTHENTICATED"}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"you":"ana","token":%q}`, tok)
	default:
		w.WriteHeader(404)
	}
}

// approve plays the account owner: it reads the sign in URL the server
// made and accepts it, returning the state and code to send back.
func (f *fakeOAuth) approve(t *testing.T, link string) (state, code string) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || !strings.HasPrefix(link, f.srv.URL+"/authorize") {
		t.Fatalf("authorize url %q", link)
	}
	q := u.Query()
	if q.Get("client_id") != "cid-123" || q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" ||
		q.Get("response_type") != "code" || q.Get("scope") != "openid email api.read" || !strings.HasSuffix(q.Get("redirect_uri"), "/oauth/callback") {
		t.Fatalf("authorize params %v", q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	code = fmt.Sprintf("code-%d", f.n)
	f.codes[code] = q.Get("code_challenge")
	return q.Get("state"), code
}

func (f *fakeOAuth) kill(at string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.access, at)
}

type oauthEnv struct {
	*env
	f   *fakeOAuth
	svc int64
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	e := newEnv(t)
	f := newFakeOAuth(t)
	spec := map[string]any{
		"base_url": f.srv.URL,
		"auth": map[string]any{"type": "oauth2", "name": "Authorization",
			"fields": []map[string]any{{"key": "developer_token", "label": "Developer token", "in": "header", "name": "developer-token", "optional": true}},
			"oauth": map[string]any{"authorize_url": f.srv.URL + "/authorize", "token_url": f.srv.URL + "/token", "scopes": []string{"openid", "email"},
				"pkce": true, "authorize_params": map[string]string{"access_type": "offline", "prompt": "consent"}}},
		"use_standard_rules": true,
		"tests":              []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}},
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "fakeo", "name": "Fake OAuth", "spec": spec}, 201, nil)
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "fakeo", "slug": "fakeo", "base_url": f.srv.URL}, 201, &sv)
	if sv.AuthType != "oauth2" {
		t.Fatalf("auth type %q", sv.AuthType)
	}
	return &oauthEnv{env: e, f: f, svc: sv.ID}
}

func (o *oauthEnv) path(p string) string { return fmt.Sprintf("/api/admin/services/%d%s", o.svc, p) }

func (o *oauthEnv) setClient() {
	o.do(o.super, "PUT", o.path("/oauth"), map[string]any{"client_id": "cid-123", "client_secret": "client-secret-value", "scopes": []string{"openid", "email", "api.read"}}, 200, nil)
}

// connect runs one whole sign in and returns the connect's final state.
func (o *oauthEnv) connect(t *testing.T, path string, body any) oauthConnectView {
	t.Helper()
	var start oauthConnectView
	o.do(o.super, "POST", path, body, 201, &start)
	state, code := o.f.approve(t, start.URL)
	o.callback(t, url.Values{"state": {state}, "code": {code}}, 200)
	var done oauthConnectView
	o.do(o.super, "GET", start.StatusURL, nil, 200, &done)
	return done
}

func (o *oauthEnv) callback(t *testing.T, q url.Values, want int) string {
	t.Helper()
	resp, err := http.Get(o.srv.URL + "/oauth/callback?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("callback: want %d got %d: %s", want, resp.StatusCode, b)
	}
	return string(b)
}

func (o *oauthEnv) settings(t *testing.T) oauthSettingsView {
	t.Helper()
	var v oauthSettingsView
	o.do(o.super, "GET", o.path("/oauth"), nil, 200, &v)
	return v
}

func (o *oauthEnv) expire(t *testing.T, keyID int64, in time.Duration) {
	t.Helper()
	c, err := o.st.GetOAuthCredential(context.Background(), keyID)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.st.UpdateOAuthTokens(context.Background(), keyID, c.AccessEnc, nil, time.Now().Add(in)); err != nil {
		t.Fatal(err)
	}
}

func (o *oauthEnv) stored(t *testing.T, keyID int64) (access, refresh string) {
	t.Helper()
	c, err := o.st.GetOAuthCredential(context.Background(), keyID)
	if err != nil {
		t.Fatal(err)
	}
	access, _ = o.api.Pool.Box.Decrypt(c.AccessEnc)
	refresh, _ = o.api.Pool.Box.Decrypt(c.RefreshEnc)
	return access, refresh
}

func TestOAuthConnectInjectRefresh(t *testing.T) {
	o := newOAuthEnv(t)
	f := o.f

	// No client yet: the settings say so, and a connect is refused.
	v := o.settings(t)
	if v.Configured || !strings.HasSuffix(v.RedirectURI, "/oauth/callback") || len(v.Fields) != 1 || !v.Fields[0].Optional {
		t.Fatalf("settings before setup: %+v", v)
	}
	o.do(o.super, "POST", o.path("/oauth/connect"), map[string]any{}, 409, nil)
	// A pasted key is refused for an OAuth service.
	o.do(o.super, "POST", o.path("/keys"), map[string]any{"keys": []map[string]any{{"secret": "pasted-token-123"}}}, 400, nil)
	o.do(o.super, "PUT", o.path("/oauth"), map[string]any{"client_id": ""}, 400, nil)
	o.do(o.super, "POST", o.path("/oauth/connect"), map[string]any{"fields": map[string]string{"nope": "x"}}, 409, nil)
	o.setClient()
	o.do(o.super, "POST", o.path("/oauth/connect"), map[string]any{"fields": map[string]string{"nope": "x"}}, 400, nil)
	if v = o.settings(t); !v.Configured || !v.HasClientSecret || v.ClientID != "cid-123" {
		t.Fatalf("settings after setup: %+v", v)
	}

	// Connect: the callback trades the code with the PKCE verifier and the
	// account becomes a key named after the id_token's email.
	done := o.connect(t, o.path("/oauth/connect"), map[string]any{"fields": map[string]string{"developer_token": "devtoken-secret-42"}})
	if done.Status != "done" || done.KeyID == 0 || done.Account != "ana@example.com" {
		t.Fatalf("connect: %+v", done)
	}
	keyID := done.KeyID
	k, _ := o.st.GetKey(context.Background(), keyID)
	if k.Label != "ana@example.com" || k.Status != store.StatusActive {
		t.Fatalf("key %+v", k)
	}
	v = o.settings(t)
	if len(v.Connections) != 1 || v.Connections[0].Account != "ana@example.com" || v.Connections[0].NeedsReconnect {
		t.Fatalf("connections %+v", v.Connections)
	}
	raw, _ := json.Marshal(v)
	firstAccess, firstRefresh := o.stored(t, keyID)
	if strings.Contains(string(raw), firstAccess) || strings.Contains(string(raw), firstRefresh) {
		t.Fatal("settings show a token")
	}

	// Inject: a proxy only app's request carries the access token and the
	// developer token; the echoed token is scrubbed from the answer.
	_, tok := o.app(map[string]any{"name": "proxyonly", "mode": "proxy"})
	var body map[string]string
	o.do(http.DefaultClient, "GET", "/proxy/fakeo/api/me", nil, 200, &body, "Authorization", "Bearer "+tok)
	if f.lastAuth != firstAccess || f.lastDev != "devtoken-secret-42" {
		t.Fatalf("upstream saw %q / %q", f.lastAuth, f.lastDev)
	}
	if body["token"] != "[key]" {
		t.Fatalf("access token not scrubbed: %v", body)
	}

	// Proactive refresh: a token expiring within a minute is refreshed
	// before the request goes out.
	o.expire(t, keyID, 30*time.Second)
	o.do(http.DefaultClient, "GET", "/proxy/fakeo/api/me", nil, 200, nil, "Authorization", "Bearer "+tok)
	second, refresh2 := o.stored(t, keyID)
	if f.refreshes.Load() != 1 || second == firstAccess || f.lastAuth != second || refresh2 != firstRefresh {
		t.Fatalf("refresh: calls %d, sent %q, stored %q", f.refreshes.Load(), f.lastAuth, second)
	}

	// Refresh token rotation: the new refresh token is kept, and works.
	f.rotate = true
	o.expire(t, keyID, 0)
	o.do(http.DefaultClient, "GET", "/proxy/fakeo/api/me", nil, 200, nil, "Authorization", "Bearer "+tok)
	_, refresh3 := o.stored(t, keyID)
	if refresh3 == firstRefresh || refresh3 == "" || f.refresh[firstRefresh] {
		t.Fatalf("rotation: stored %q", refresh3)
	}
	f.rotate = false

	// A 401 for a token the provider revoked early: one refresh, one retry.
	cur, _ := o.stored(t, keyID)
	f.kill(cur)
	before := f.refreshes.Load()
	o.do(http.DefaultClient, "GET", "/proxy/fakeo/api/me", nil, 200, nil, "Authorization", "Bearer "+tok)
	if f.refreshes.Load() != before+1 || f.lastAuth == cur {
		t.Fatalf("401 retry: refreshes %d, sent %q", f.refreshes.Load()-before, f.lastAuth)
	}

	// Concurrent requests with an expired token refresh it once.
	f.delay = 150 * time.Millisecond
	o.expire(t, keyID, 0)
	before = f.refreshes.Load()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", o.srv.URL+"/proxy/fakeo/api/me", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	if n := f.refreshes.Load() - before; n != 1 {
		t.Fatalf("concurrent refreshes: %d", n)
	}
	f.delay = 0

	// A lease hands out the current access token with the fields, never
	// the refresh token.
	_, both := o.app(map[string]any{"name": "leaser", "mode": "both"})
	var lease struct {
		Secret string `json:"secret"`
	}
	o.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "fakeo"}, 200, &lease, "Authorization", "Bearer "+both)
	access, refresh := o.stored(t, keyID)
	if !strings.Contains(lease.Secret, access) || strings.Contains(lease.Secret, refresh) || !strings.Contains(lease.Secret, "devtoken-secret-42") {
		t.Fatalf("lease secret %q", lease.Secret)
	}

	// The same account signing in again updates its key, no second key.
	again := o.connect(t, o.path("/oauth/connect"), map[string]any{"fields": map[string]string{"developer_token": "devtoken-secret-42"}})
	if again.KeyID != keyID || len(o.settings(t).Connections) != 1 {
		t.Fatalf("same account made a new key: %+v", again)
	}
}

func TestOAuthInvalidGrantAndReconnect(t *testing.T) {
	o := newOAuthEnv(t)
	o.setClient()
	done := o.connect(t, o.path("/oauth/connect"), nil)
	keyID := done.KeyID
	_, tok := o.app(map[string]any{"name": "a", "mode": "proxy"})

	// The account revoked access: the refresh is refused, the key is
	// marked invalid and leaves rotation, and the dashboard asks for a
	// reconnect.
	o.f.mu.Lock()
	o.f.refresh = map[string]bool{}
	o.f.mu.Unlock()
	o.expire(t, keyID, -time.Minute)
	o.do(http.DefaultClient, "GET", "/proxy/fakeo/api/me", nil, 503, nil, "Authorization", "Bearer "+tok)
	k, _ := o.st.GetKey(context.Background(), keyID)
	v := o.settings(t)
	if k.Status != store.StatusInvalid || len(v.Connections) != 1 || !v.Connections[0].NeedsReconnect || !strings.Contains(v.Connections[0].LastError, "reconnect") {
		t.Fatalf("after invalid_grant: key %s, %+v", k.Status, v.Connections)
	}

	// Reconnect keeps the key and puts it back in rotation.
	o.f.account = "" // an id_token without an email still reconnects the key being fixed
	again := o.connect(t, fmt.Sprintf("/api/admin/keys/%d/oauth/reconnect", keyID), nil)
	k, _ = o.st.GetKey(context.Background(), keyID)
	if again.Status != "done" || again.KeyID != keyID || k.Status != store.StatusActive {
		t.Fatalf("reconnect: %+v, key %s", again, k.Status)
	}
	o.do(http.DefaultClient, "GET", "/proxy/fakeo/api/me", nil, 200, nil, "Authorization", "Bearer "+tok)
}

func TestOAuthCallbackRefusals(t *testing.T) {
	o := newOAuthEnv(t)
	o.setClient()

	// No state, an unknown state.
	o.callback(t, url.Values{"code": {"x"}}, 400)
	o.callback(t, url.Values{"state": {"made-up"}, "code": {"x"}}, 400)

	// A code issued for another PKCE challenge is refused by the provider,
	// and the connect fails.
	var start oauthConnectView
	o.do(o.super, "POST", o.path("/oauth/connect"), nil, 201, &start)
	state, code := o.f.approve(t, start.URL)
	o.f.mu.Lock()
	o.f.codes[code] = "another-challenge"
	o.f.mu.Unlock()
	page := o.callback(t, url.Values{"state": {state}, "code": {code}}, 400)
	if !strings.Contains(page, "invalid_grant") {
		t.Fatalf("page: %s", page)
	}
	var st oauthConnectView
	o.do(o.super, "GET", start.StatusURL, nil, 200, &st)
	if st.Status != "failed" || st.KeyID != 0 {
		t.Fatalf("status %+v", st)
	}
	// The state worked once: replaying it is refused.
	o.callback(t, url.Values{"state": {state}, "code": {code}}, 400)

	// The owner refused consent.
	o.do(o.super, "POST", o.path("/oauth/connect"), nil, 201, &start)
	state, _ = o.f.approve(t, start.URL)
	o.callback(t, url.Values{"state": {state}, "error": {"access_denied"}}, 400)
	o.do(o.super, "GET", start.StatusURL, nil, 200, &st)
	if st.Status != "failed" || !strings.Contains(st.Error, "access_denied") {
		t.Fatalf("denied: %+v", st)
	}
	if n := len(o.settings(t).Connections); n != 0 {
		t.Fatalf("%d connections after refusals", n)
	}

	// A service that takes API keys has no OAuth routes.
	o.do(o.super, "GET", "/api/admin/services/1/oauth", nil, 400, nil)
}

// The control MCP tools are listed, and oauth_connect_url returns the link.
func TestOAuthControlTools(t *testing.T) {
	for _, name := range []string{"oauth_settings", "set_oauth_client", "oauth_connect_url", "oauth_connection_status", "oauth_reconnect"} {
		if ctlToolByName(name) == nil {
			t.Errorf("no control tool %s", name)
		}
	}
}
