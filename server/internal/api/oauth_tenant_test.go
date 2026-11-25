package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestOAuthTenantAndTokenPath: a provider whose URLs take {tenant} signs in
// through the service's own tenant (the provider's default when it has
// none), and a token_path reads the token from where the provider nests it,
// as Slack does for user tokens.
func TestOAuthTenantAndTokenPath(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var tokenPaths []string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokenPaths = append(tokenPaths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"access_token":"xoxb-bot-token-0001","token_type":"bot","team":{"name":"Acme"},
			"authed_user":{"id":"U1","access_token":"xoxp-user-token-0001","refresh_token":"xoxe-refresh-0001","expires_in":43200,"scope":"search:read,users:read"}}`))
	}))
	t.Cleanup(idp.Close)
	spec := map[string]any{
		"base_url": idp.URL,
		"auth": map[string]any{"type": "oauth2", "name": "Authorization", "oauth": map[string]any{
			"authorize_url": idp.URL + "/{tenant}/authorize", "token_url": idp.URL + "/{tenant}/token", "tenant": "common",
			"token_path": "authed_user.access_token", "scopes": []string{"search:read"}, "scope_separator": ","}},
		"use_standard_rules": true,
		"tests":              []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}},
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "tenanted", "name": "Tenanted", "spec": spec}, 201, nil)
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "tenanted", "slug": "tenanted", "base_url": idp.URL}, 201, &sv)
	path := "/api/admin/services/" + itoa(int(sv.ID)) + "/oauth"

	var v oauthSettingsView
	e.do(e.super, "PUT", path, map[string]any{"client_id": "cid", "client_secret": "sec"}, 200, &v)
	if !v.TakesTenant || v.Tenant != "" || v.DefaultTenant != "common" || v.AuthorizeURL != idp.URL+"/common/authorize" {
		t.Fatalf("default tenant view %+v", v)
	}
	e.do(e.super, "PUT", path, map[string]any{"client_id": "cid", "tenant": "../evil"}, 400, nil)
	e.do(e.super, "PUT", path, map[string]any{"client_id": "cid", "tenant": "contoso.onmicrosoft.com"}, 200, &v)
	if v.Tenant != "contoso.onmicrosoft.com" || !strings.Contains(v.AuthorizeURL, "/contoso.onmicrosoft.com/authorize") {
		t.Fatalf("tenant view %+v", v)
	}
	// Leaving tenant out keeps it.
	e.do(e.super, "PUT", path, map[string]any{"client_id": "cid"}, 200, &v)
	if v.Tenant != "contoso.onmicrosoft.com" {
		t.Fatalf("tenant not kept: %+v", v)
	}

	var start oauthConnectView
	e.do(e.super, "POST", path+"/connect", map[string]any{}, 201, &start)
	u, _ := url.Parse(start.URL)
	if u.Path != "/contoso.onmicrosoft.com/authorize" || u.Query().Get("scope") != "search:read" {
		t.Fatalf("authorize url %s", start.URL)
	}
	resp, err := http.Get(e.srv.URL + "/oauth/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {"code-1"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var done oauthConnectView
	e.do(e.super, "GET", start.StatusURL, nil, 200, &done)
	if done.Status != "done" {
		t.Fatalf("connect %+v", done)
	}
	if len(tokenPaths) != 1 || tokenPaths[0] != "/contoso.onmicrosoft.com/token" {
		t.Fatalf("token endpoint calls %v", tokenPaths)
	}
	c, err := e.st.GetOAuthCredential(t.Context(), done.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	access, _ := e.api.Pool.Box.Decrypt(c.AccessEnc)
	refresh, _ := e.api.Pool.Box.Decrypt(c.RefreshEnc)
	if access != "xoxp-user-token-0001" || refresh != "xoxe-refresh-0001" || c.ExpiresAt.IsZero() || len(c.Scopes) != 2 {
		t.Fatalf("stored %q %q %v %v", access, refresh, c.ExpiresAt, c.Scopes)
	}
}

func TestParseTokenAnswer(t *testing.T) {
	slack := []byte(`{"ok":true,"access_token":"xoxb-1","token_type":"bot","expires_in":43200,"refresh_token":"xoxe-1","authed_user":{"access_token":"xoxp-1"}}`)
	bot := parseTokenAnswer(&providers.OAuthConfig{}, slack)
	if bot.AccessToken != "xoxb-1" || bot.RefreshToken != "xoxe-1" || bot.ExpiresIn != "43200" {
		t.Fatalf("bot token %+v", bot)
	}
	user := parseTokenAnswer(&providers.OAuthConfig{TokenPath: "authed_user.access_token"}, slack)
	if user.AccessToken != "xoxp-1" || user.RefreshToken != "" {
		t.Fatalf("user token %+v", user)
	}
	refused := parseTokenAnswer(&providers.OAuthConfig{TokenPath: "authed_user.access_token"}, []byte(`{"ok":false,"error":"invalid_refresh_token"}`))
	if refused.Error != "invalid_refresh_token" || !(&oauthTokenError{Code: refused.Error}).invalidGrant() {
		t.Fatalf("refused %+v", refused)
	}
	var m map[string]any
	_ = json.Unmarshal(slack, &m)
	if obj, name := (&providers.OAuthConfig{TokenPath: "nope.access_token"}).TokenObject(m); obj != nil || name != "" {
		t.Fatal("a missing object was found")
	}
}
