package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type appTokenList struct {
	Tokens []store.AppToken `json:"tokens"`
}

type newAppTokenResp struct {
	Token    string         `json:"token"`
	AppToken store.AppToken `json:"app_token"`
}

func (e *env) appTokens(appID int64) []store.AppToken {
	e.t.Helper()
	var out appTokenList
	e.do(e.super, "GET", "/api/admin/apps/"+itoa(int(appID))+"/tokens", nil, 200, &out)
	return out.Tokens
}

func (e *env) newAppToken(appID int64, body map[string]any) newAppTokenResp {
	e.t.Helper()
	var out newAppTokenResp
	e.do(e.super, "POST", "/api/admin/apps/"+itoa(int(appID))+"/tokens", body, 201, &out)
	return out
}

// appCalls makes one proxy call, one lease and one MCP call with tok and
// checks each answers want.
func (e *env) appCalls(tok string, want int) {
	e.t.Helper()
	bearer := []string{"Authorization", "Bearer " + tok}
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, want, nil, bearer...)
	leaseWant := want
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, leaseWant, nil, bearer...)
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, want, nil, bearer...)
}

func TestAppHasSeveralRevocableTokens(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	id, first := e.app(map[string]any{"name": "site"})

	toks := e.appTokens(id)
	if len(toks) != 1 || toks[0].Name != "Default" || toks[0].Prefix != first[:8]+"..." || toks[0].LastUsedAt != nil {
		t.Fatalf("a new app should have one Default token: %+v", toks)
	}

	second := e.newAppToken(id, map[string]any{"name": "ci"})
	if second.Token == "" || second.AppToken.Name != "ci" || second.AppToken.ExpiresAt != nil {
		t.Fatalf("new token: %+v", second)
	}
	e.appCalls(first, 200)
	e.appCalls(second.Token, 200)

	var apps struct {
		Apps []store.App `json:"apps"`
	}
	e.do(e.super, "GET", "/api/admin/apps", nil, 200, &apps)
	if apps.Apps[0].TokenCount != 2 || apps.Apps[0].LastUsedAt == nil {
		t.Fatalf("app list should show 2 tokens and a last use: %+v", apps.Apps[0])
	}

	// Revoking one leaves the other working, on proxy, consumer API and MCP.
	var rev store.AppToken
	e.do(e.super, "DELETE", "/api/admin/apps/"+itoa(int(id))+"/tokens/"+itoa(int(second.AppToken.ID)), nil, 200, &rev)
	if rev.RevokedAt == nil {
		t.Fatal("revoke should set revoked_at")
	}
	e.appCalls(second.Token, 401)
	e.appCalls(first, 200)
	toks = e.appTokens(id)
	if len(toks) != 2 || toks[1].RevokedAt == nil || toks[0].LastUsedAt == nil {
		t.Fatalf("list after revoke: %+v", toks)
	}

	// Rename.
	var ren store.AppToken
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(id))+"/tokens/"+itoa(int(toks[0].ID)), map[string]any{"name": "web"}, 200, &ren)
	if ren.Name != "web" {
		t.Fatalf("rename: %+v", ren)
	}
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(id))+"/tokens/"+itoa(int(toks[0].ID)), map[string]any{"name": " "}, 400, nil)
	// A token id of another app is not found through this one.
	other, _ := e.app(map[string]any{"name": "other"})
	e.do(e.super, "DELETE", "/api/admin/apps/"+itoa(int(other))+"/tokens/"+itoa(int(toks[0].ID)), nil, 404, nil)

	// Events note which token made the call.
	var n int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE app_id=? AND token_id=?`, id, toks[0].ID).Scan(&n); err != nil || n == 0 {
		t.Fatalf("events should record the token: %d %v", n, err)
	}

	// Every action is audited.
	var audit struct {
		Entries []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	seen := map[string]bool{}
	for _, a := range audit.Entries {
		seen[a.Action] = true
	}
	for _, act := range []string{"app.create", "app.token_create", "app.token_revoke", "app.token_rename"} {
		if !seen[act] {
			t.Errorf("no %s audit entry", act)
		}
	}
}

func TestExpiredAppTokenRefused(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	id, _ := e.app(map[string]any{"name": "site"})
	e.do(e.super, "POST", "/api/admin/apps/"+itoa(int(id))+"/tokens", map[string]any{"name": "x", "expires_in_days": 0}, 400, nil)
	tmp := e.newAppToken(id, map[string]any{"name": "temp", "expires_in_days": 7})
	if tmp.AppToken.ExpiresAt == nil || tmp.AppToken.ExpiresAt.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("expiry not set: %+v", tmp.AppToken)
	}
	e.appCalls(tmp.Token, 200)
	if _, err := e.st.DB.Exec(`UPDATE app_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Unix(), tmp.AppToken.ID); err != nil {
		t.Fatal(err)
	}
	e.appCalls(tmp.Token, 401)
}

func TestAppTokenLastUsedIsThrottled(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	id, tok := e.app(map[string]any{"name": "site"})
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	tid := e.appTokens(id)[0].ID
	lastUsed := func() int64 {
		var v int64
		if err := e.st.DB.QueryRow(`SELECT last_used_at FROM app_tokens WHERE id=?`, tid).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if lastUsed() == 0 {
		t.Fatal("first use should be recorded")
	}
	if _, err := e.st.DB.Exec(`UPDATE app_tokens SET last_used_at=1 WHERE id=?`, tid); err != nil {
		t.Fatal(err)
	}
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	if lastUsed() != 1 {
		t.Fatal("a second use within a minute should not write last_used_at")
	}
	old := store.TokenTouchInterval
	store.TokenTouchInterval = 0
	defer func() { store.TokenTouchInterval = old }()
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	if lastUsed() == 1 {
		t.Fatal("once the interval passed, last_used_at should be written")
	}
}

func TestRotateCreatesTokenAndRevokesOthers(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	id, first := e.app(map[string]any{"name": "site"})
	second := e.newAppToken(id, map[string]any{"name": "ci"})
	var rot struct {
		Token string    `json:"token"`
		App   store.App `json:"app"`
	}
	e.do(e.super, "POST", "/api/admin/apps/"+itoa(int(id))+"/rotate-token", map[string]any{}, 200, &rot)
	e.appCalls(rot.Token, 200)
	e.appCalls(first, 401)
	e.appCalls(second.Token, 401)
	if rot.App.TokenCount != 1 || rot.App.TokenPrefix != rot.Token[:8] {
		t.Fatalf("after rotate: %+v", rot.App)
	}
	toks := e.appTokens(id)
	if len(toks) != 3 || toks[0].RevokedAt != nil || toks[1].RevokedAt == nil || toks[2].RevokedAt == nil {
		t.Fatalf("rotate should keep one live token: %+v", toks)
	}
	var audit struct {
		Entries []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	found := false
	for _, a := range audit.Entries {
		found = found || a.Action == "app.rotate_token"
	}
	if !found {
		t.Fatal("no app.rotate_token audit entry")
	}
}
