package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// legacyAppToken turns the app's token into one minted before the rename
// (cog_ prefix), the way it sits in an upgraded database.
func (e *env) legacyAppToken(appID int64) string {
	e.t.Helper()
	tok := "cog_" + strings.Repeat("L", 32)
	if _, err := e.st.DB.Exec(`UPDATE apps SET token_hash=?, token_prefix=? WHERE id=?`, crypto.SHA256(tok), tok[:8], appID); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func TestNewAppTokensUseAcnPrefix(t *testing.T) {
	e := newEnv(t)
	_, tok := e.app(map[string]any{"name": "site"})
	if !strings.HasPrefix(tok, "acn_") {
		t.Fatalf("new app token %q should start with acn_", tok)
	}
}

func TestLegacyAppTokenStillAuthenticates(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	id, _ := e.app(map[string]any{"name": "old-site"})
	tok := e.legacyAppToken(id)

	// Bearer, the old header and the old query parameter all work, and none
	// of them reaches the upstream (fakeSerp answers 500 if it sees one).
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "X-Cogito-Token", tok)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a&cogito_token="+url.QueryEscape(tok), nil, 200, nil)
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, nil, "Authorization", "Bearer "+tok)
}

func TestNewTokenNamesAccepted(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	_, tok := e.app(map[string]any{"name": "site"})
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "X-Acnologia-Token", tok)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a&acn_token="+url.QueryEscape(tok), nil, 200, nil)
}

func TestResponsesCarryBothHeaderNames(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	_, tok := e.app(map[string]any{"name": "site"})
	resp := e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	for _, name := range []string{"Key-Id", "Attempts"} {
		v := resp.Header.Get("X-Acnologia-" + name)
		if v == "" || resp.Header.Get("X-Cogito-"+name) != v {
			t.Fatalf("%s: new %q, old %q", name, v, resp.Header.Get("X-Cogito-"+name))
		}
	}
}

func TestLegacyAdminTokenStillAuthenticates(t *testing.T) {
	e := newEnv(t)
	u, err := e.st.GetUserByEmail(context.Background(), "root@x.io")
	if err != nil {
		t.Fatal(err)
	}
	tok := "cogadm_" + strings.Repeat("A", 40)
	if _, err := e.st.CreateToken(context.Background(), &store.AdminToken{Name: "old-ci", TokenPrefix: tok[:12], Scope: store.ScopeRead, UserID: u.ID}, crypto.SHA256(tok)); err != nil {
		t.Fatal(err)
	}
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 200, nil, "Authorization", "Bearer "+tok)
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 401, nil, "Authorization", "Bearer cogadm_unknown")
}

func TestLegacySessionCookieKeepsUserSignedIn(t *testing.T) {
	e := newEnv(t)
	base, _ := url.Parse(e.srv.URL)
	var sess string
	for _, c := range e.super.Jar.Cookies(base) {
		switch c.Name {
		case "acnologia_session":
			sess = c.Value
		case "cogito_session":
			t.Fatal("sign-in must set only the new cookie")
		}
	}
	if sess == "" {
		t.Fatal("no acnologia_session cookie after setup")
	}
	// A browser upgraded from the old build only holds cogito_session.
	e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 200, nil, "Cookie", "cogito_session="+sess)
	e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 200, nil, "Cookie", "acnologia_session="+sess)
	e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 401, nil, "Cookie", "cogito_session=nope")
}
