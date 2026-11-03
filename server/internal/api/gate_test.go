package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/redact"
)

// testAccessPassword is an obvious fake, used only by these tests.
const testAccessPassword = "fake-gate-password-for-tests"

// newGateEnv is newEnv (a superadmin, the serpapi service and the router)
// with the access gate switched on afterwards, so setup could still run.
func newGateEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.api.setAccessPassword(testAccessPassword)
	srv := httptest.NewServer(e.api.Router())
	t.Cleanup(srv.Close)
	e.srv = srv
	return e
}

// gateStateBody is the GET /api/gate answer.
type gateStateBody struct {
	Required     bool `json:"required"`
	Locked       bool `json:"locked"`
	PasswordOnly bool `json:"password_only"`
	UnlockTTLSec int  `json:"unlock_ttl_sec"`
}

func gateCookieFrom(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == gateCookie {
			return c
		}
	}
	return nil
}

func TestGateLocksDashboardRoutes(t *testing.T) {
	e := newGateEnv(t)
	// The superadmin's session cookie alone is not enough: the gate comes first.
	for _, p := range []string{
		"/api/admin/me", "/api/admin/services", "/api/auth/state", "/api/docs",
		"/api/logos/providers/serpapi", "/docs/overview.md", "/docs/assets/logo.png",
		"/llms.txt", "/llms-full.txt", "/api/nope",
	} {
		var out struct {
			Error apiError `json:"error"`
		}
		e.do(e.super, "GET", p, nil, 401, &out)
		if out.Error.Code != "locked" {
			t.Fatalf("%s: code = %q", p, out.Error.Code)
		}
	}
	e.do(client(), "POST", "/api/auth/login", map[string]any{"email": "root@x.io", "password": "supersecret1"}, 401, nil)
	e.do(client(), "POST", "/api/auth/setup", map[string]any{"email": "a@x.io", "password": "supersecret1"}, 401, nil)
	// A write to a path that would be the SPA shell is still refused.
	e.do(client(), "POST", "/settings", map[string]any{}, 401, nil)
}

func TestGateNavigationGetsShell(t *testing.T) {
	e := newGateEnv(t)
	for _, p := range []string{"/", "/login", "/keys", "/docs", "/docs/api", "/dev/icons"} {
		resp, err := http.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(b) != "ui" {
			t.Fatalf("%s: %d %q, want the SPA shell", p, resp.StatusCode, b)
		}
	}
}

// The public landing page renders on a locked deploy without the lock
// screen, so its shell and every static file it loads (script, stylesheet,
// font, the mark) must reach a browser with no gate cookie. The data the
// dashboard reads stays locked.
func TestGateServesLandingAndItsAssets(t *testing.T) {
	e := newEnv(t)
	e.api.WebFS = fstest.MapFS{
		"index.html":                    {Data: []byte("ui")},
		"assets/index-abc123.js":        {Data: []byte("js")},
		"assets/index-abc123.css":       {Data: []byte("css")},
		"assets/inter-latin-opsz.woff2": {Data: []byte("font")},
		"mark.png":                      {Data: []byte("png")},
		"favicon.png":                   {Data: []byte("icon")},
		"site.webmanifest":              {Data: []byte("{}")},
	}
	e.api.setAccessPassword(testAccessPassword)
	srv := httptest.NewServer(e.api.Router())
	t.Cleanup(srv.Close)
	e.srv = srv

	for p, want := range map[string]string{
		"/landing":                       "ui",
		"/":                              "ui",
		"/assets/index-abc123.js":        "js",
		"/assets/index-abc123.css":       "css",
		"/assets/inter-latin-opsz.woff2": "font",
		"/mark.png":                      "png",
		"/favicon.png":                   "icon",
		"/site.webmanifest":              "{}",
	} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(b) != want {
			t.Fatalf("%s: %d %q, want 200 %q", p, resp.StatusCode, b, want)
		}
	}
	// The landing page asks whether there is a lock; that stays open, and
	// the sign in state behind it stays locked.
	var st gateStateBody
	e.do(client(), "GET", "/api/gate", nil, 200, &st)
	if !st.Locked {
		t.Fatalf("gate state = %v", st)
	}
	e.do(client(), "GET", "/api/auth/state", nil, 401, nil)
	e.do(client(), "GET", "/docs/overview.md", nil, 401, nil)
}

func TestGateOpenRoutes(t *testing.T) {
	e := newGateEnv(t)
	e.unlock(e.super)
	_, tok := e.app(map[string]any{"name": "site"})
	e.addKeys(map[string]any{"secret": "k1"})

	e.do(client(), "GET", "/healthz", nil, 200, nil)
	e.do(client(), "GET", "/robots.txt", nil, 200, nil)
	var st gateStateBody
	e.do(client(), "GET", "/api/gate", nil, 200, &st)
	if !st.Required || !st.Locked {
		t.Fatalf("gate state = %v", st)
	}
	// Consumer routes need their app token, not the gate.
	var body map[string]any
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=tea", nil, 200, &body, "Authorization", "Bearer "+tok)
	if body["key"] != "k1" {
		t.Fatalf("proxy answered %v", body)
	}
	e.do(http.DefaultClient, "GET", "/api/v1/services", nil, 200, nil, "Authorization", "Bearer "+tok)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json", nil, 401, nil)
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200, nil, "Authorization", "Bearer "+tok)
	// The password header never reaches the provider (fakeSerp answers 500 if it does).
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json", nil, 200, nil, "Authorization", "Bearer "+tok, accessHeader, testAccessPassword)
}

func TestGateOpenPrefixCannotBeBorrowed(t *testing.T) {
	e := newGateEnv(t)
	for _, p := range []string{"/proxy/../api/admin/me", "/api/v1/../admin/me", "/api/gate/../admin/me", "/proxy%2F..%2Fapi/admin/me"} {
		req, _ := http.NewRequest("GET", e.srv.URL+"/x", nil)
		req.URL.Opaque = p // sent as written, not cleaned by the client
		resp, err := e.super.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && strings.Contains(string(b), "root@x.io") {
			t.Fatalf("%s leaked the account: %s", p, b)
		}
	}
}

// unlock passes c through the gate, as the lock screen does.
func (e *env) unlock(c *http.Client) {
	e.t.Helper()
	e.do(c, "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
}

func TestGateUnlockSetsCookie(t *testing.T) {
	e := newGateEnv(t)
	resp := e.do(e.super, "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
	c := gateCookieFrom(resp)
	if c == nil {
		t.Fatal("no gate cookie")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != int(gateTTL.Seconds()) {
		t.Fatalf("cookie attributes: %+v", c)
	}
	if !e.api.gate.valid(c.Value) {
		t.Fatal("issued cookie does not validate")
	}
	if strings.Contains(c.Value, testAccessPassword) {
		t.Fatal("cookie carries the password")
	}
	var st gateStateBody
	e.do(e.super, "GET", "/api/gate", nil, 200, &st)
	if st.Locked {
		t.Fatal("still locked after unlock")
	}
	e.do(e.super, "GET", "/api/admin/me", nil, 200, nil)
	e.do(e.super, "GET", "/docs/overview.md", nil, 200, nil)

	// Lock clears it again.
	resp = e.do(e.super, "POST", "/api/gate/lock", nil, 200, nil)
	if c := gateCookieFrom(resp); c == nil || c.MaxAge >= 0 {
		t.Fatalf("lock did not clear the cookie: %+v", c)
	}
	e.do(e.super, "GET", "/api/admin/me", nil, 401, nil)
}

func TestGateSecureCookieSetting(t *testing.T) {
	e := newGateEnv(t)
	e.api.Cfg.SecureCookies = true
	resp := e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
	if c := gateCookieFrom(resp); c == nil || !c.Secure {
		t.Fatalf("want a Secure cookie, got %+v", c)
	}
}

func TestGateRefusesTamperedAndExpiredCookies(t *testing.T) {
	e := newGateEnv(t)
	g := e.api.gate
	good := g.issue(time.Now())
	issued, mac, _ := strings.Cut(good, ".")
	flip := map[byte]byte{'0': '1'}
	b := []byte(mac)
	if f, ok := flip[b[0]]; ok {
		b[0] = f
	} else {
		b[0] = '0'
	}
	old := g.issue(time.Now().Add(-gateTTL - time.Hour))
	future := g.issue(time.Now().Add(time.Hour))
	other := newAccessGate("another-fake-password", nil).issue(time.Now())
	for name, v := range map[string]string{
		"tampered mac":   issued + "." + string(b),
		"moved time":     "1" + issued + "." + mac,
		"expired":        old,
		"future":         future,
		"other password": other,
		"no dot":         mac,
		"empty":          "",
	} {
		if g.valid(v) {
			t.Fatalf("%s: accepted", name)
		}
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/admin/me", nil)
		req.AddCookie(&http.Cookie{Name: gateCookie, Value: v})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s: status %d", name, resp.StatusCode)
		}
	}
	if !g.valid(good) {
		t.Fatal("a fresh cookie was refused")
	}
}

func TestGateAccessHeader(t *testing.T) {
	e := newGateEnv(t)
	e.do(e.super, "GET", "/api/admin/me", nil, 200, nil, accessHeader, testAccessPassword)
	e.do(e.super, "GET", "/api/admin/me", nil, 401, nil, accessHeader, testAccessPassword+"x")
	// The right header never counts against the limit.
	for i := 0; i < 2*gateFailLimit; i++ {
		e.do(e.super, "GET", "/api/admin/me", nil, 200, nil, accessHeader, testAccessPassword)
	}
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
}

// TestGateAccessHeaderIsLimited: a wrong X-Acnologia-Access value is a
// guess like a lock screen one, so the header is no way around the limit.
func TestGateAccessHeaderIsLimited(t *testing.T) {
	e := newGateEnv(t)
	now := time.Unix(1_800_000_000, 0)
	e.api.gate.now = func() time.Time { return now }
	// One wrong guess on the lock screen and the rest in the header, on a
	// locked route and on the open gate state alike.
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": "wrong-guess"}, 401, nil)
	for i := 1; i < gateFailLimit; i++ {
		if i%2 == 0 {
			e.do(client(), "GET", "/api/gate", nil, 200, nil, accessHeader, "wrong-guess")
		} else {
			e.do(e.super, "GET", "/api/admin/me", nil, 401, nil, accessHeader, "wrong-guess")
		}
	}
	// Limited now: the right header and the right password are refused unseen.
	e.do(e.super, "GET", "/api/admin/me", nil, 401, nil, accessHeader, testAccessPassword)
	var st struct {
		Locked bool `json:"locked"`
	}
	e.do(client(), "GET", "/api/gate", nil, 200, &st, accessHeader, testAccessPassword)
	if !st.Locked {
		t.Fatalf("limited header passed the gate state: %v", st)
	}
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 429, nil)
	// The next minute takes the header again.
	now = now.Add(gateFailWindow)
	e.do(e.super, "GET", "/api/admin/me", nil, 200, nil, accessHeader, testAccessPassword)
}

// TestGateLimitPerClient: one address that spends its daily budget waits
// alone, and many addresses together meet a ceiling of a minute, never a day.
func TestGateLimitPerClient(t *testing.T) {
	g := newAccessGate(testAccessPassword, nil)
	now := time.Unix(1_800_000_000, 0)
	g.now = func() time.Time { return now }
	// The attacker spends a whole day's budget from one address.
	for i := 0; i < gateDailyLimit; i++ {
		if i > 0 && i%gateFailLimit == 0 {
			now = now.Add(gateFailWindow)
		}
		if _, limited, _ := g.tryUnlock("203.0.113.9", "wrong-guess"); limited {
			t.Fatalf("guess %d limited early", i)
		}
	}
	now = now.Add(gateFailWindow)
	if ok, limited, wait := g.tryUnlock("203.0.113.9", testAccessPassword); ok || !limited || wait <= gateFailWindow {
		t.Fatalf("attacker after its daily cap: ok=%v limited=%v wait=%v", ok, limited, wait)
	}
	// The owner, elsewhere, is not locked out.
	if ok, limited, _ := g.tryUnlock("198.51.100.7", testAccessPassword); !ok || limited {
		t.Fatalf("owner refused: ok=%v limited=%v", ok, limited)
	}

	// A rotating guesser meets the global ceiling within the minute...
	now = now.Add(gateFailWindow)
	for i := 0; i < gateGlobalFailLimit; i++ {
		if _, limited, _ := g.tryUnlock(fmt.Sprintf("192.0.2.%d", i), "wrong-guess"); limited {
			t.Fatalf("rotating guess %d limited early", i)
		}
	}
	ok, limited, wait := g.tryUnlock("198.51.100.7", testAccessPassword)
	if ok || !limited || wait > gateFailWindow {
		t.Fatalf("global ceiling: ok=%v limited=%v wait=%v", ok, limited, wait)
	}
	// ...which lasts a minute, not a day.
	now = now.Add(gateFailWindow)
	if ok, limited, _ := g.tryUnlock("198.51.100.7", testAccessPassword); !ok || limited {
		t.Fatalf("owner still refused after a minute: ok=%v limited=%v", ok, limited)
	}
}

// TestGateLimitIsAtomic: parallel wrong guesses from one address cannot all
// slip past the check before any is counted.
func TestGateLimitIsAtomic(t *testing.T) {
	g := newAccessGate(testAccessPassword, nil)
	var wg sync.WaitGroup
	var seen atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, limited, _ := g.tryUnlock("203.0.113.9", "wrong-guess"); !limited {
				seen.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := seen.Load(); n != gateFailLimit {
		t.Fatalf("%d guesses were compared, want %d", n, gateFailLimit)
	}
}

func TestGateRateLimit(t *testing.T) {
	e := newGateEnv(t)
	now := time.Unix(1_800_000_000, 0)
	e.api.gate.now = func() time.Time { return now }
	for i := 0; i < gateFailLimit; i++ {
		var out struct {
			Error apiError `json:"error"`
		}
		e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": "wrong-guess"}, 401, &out)
		if out.Error.Code != "wrong_password" {
			t.Fatalf("code = %q", out.Error.Code)
		}
	}
	var out struct {
		Error      apiError `json:"error"`
		RetryAfter int      `json:"retry_after_sec"`
	}
	// Even the right password is refused unseen while limited.
	resp := e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 429, &out)
	if out.Error.Code != "too_many_attempts" || out.RetryAfter <= 0 || out.RetryAfter > 60 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("429 body = %+v, Retry-After %q", out, resp.Header.Get("Retry-After"))
	}
	if gateCookieFrom(resp) != nil {
		t.Fatal("a limited unlock set the cookie")
	}
	// The next minute takes guesses again.
	now = now.Add(gateFailWindow)
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)

	// The daily cap: 50 wrong guesses spread over minutes still shut it.
	for i := 0; i < gateDailyLimit-gateFailLimit; i++ {
		if i%gateFailLimit == 0 {
			now = now.Add(gateFailWindow)
		}
		e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": "wrong-guess"}, 401, nil)
	}
	now = now.Add(gateFailWindow)
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 429, &out)
	if out.RetryAfter <= 60 {
		t.Fatalf("daily limit retry_after_sec = %d", out.RetryAfter)
	}
}

func TestGateOffChangesNothing(t *testing.T) {
	e := newEnv(t)
	var st gateStateBody
	e.do(client(), "GET", "/api/gate", nil, 200, &st)
	if st.Required || st.Locked {
		t.Fatalf("gate state = %v", st)
	}
	e.do(e.super, "GET", "/api/admin/me", nil, 200, nil)
	e.do(client(), "GET", "/llms.txt", nil, 200, nil)
}

func TestRobotsAndNoIndexEverywhere(t *testing.T) {
	e := newGateEnv(t)
	resp, err := http.Get(e.srv.URL + "/robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "User-agent: *\nDisallow: /\n" {
		t.Fatalf("robots.txt = %q", b)
	}
	e.unlock(e.super)
	_, tok := e.app(map[string]any{"name": "site"})
	checks := []struct {
		method, path string
		hdr          []string
	}{
		{"GET", "/", nil}, {"GET", "/healthz", nil}, {"GET", "/robots.txt", nil},
		{"GET", "/api/admin/me", nil}, {"GET", "/api/gate", nil}, {"GET", "/assets/none.js", nil},
		{"GET", "/proxy/serpapi/search.json", []string{"Authorization", "Bearer " + tok}},
		{"POST", "/api/gate/unlock", nil},
	}
	for _, c := range checks {
		req, _ := http.NewRequest(c.method, e.srv.URL+c.path, strings.NewReader(`{"password":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(c.hdr); i += 2 {
			req.Header.Set(c.hdr[i], c.hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		got := resp.Header.Get("X-Robots-Tag")
		for _, want := range []string{"noindex", "nofollow", "noarchive", "nosnippet"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s %s (%d): X-Robots-Tag %q lacks %s", c.method, c.path, resp.StatusCode, got, want)
			}
		}
	}
}

func TestGateSecretsRedacted(t *testing.T) {
	e := newGateEnv(t)
	cookie := e.api.gate.issue(time.Now())
	ctx := context.Background()
	r := e.st.Redactor()
	line := "pw=" + testAccessPassword + " Cookie: acnologia_gate=" + cookie + " X-Acnologia-Access: " + testAccessPassword
	out := r.String(ctx, line)
	if strings.Contains(out, testAccessPassword) || strings.Contains(out, cookie) {
		t.Fatalf("not redacted: %s", out)
	}
	// And through the log handler, as main wires it.
	var buf bytes.Buffer
	h := redact.NewHandler(slog.NewTextHandler(&buf, nil))
	h.Use(r)
	slog.New(h).Info("oops", "body", testAccessPassword, "cookie", gateCookie+"="+cookie)
	if strings.Contains(buf.String(), testAccessPassword) || strings.Contains(buf.String(), cookie) {
		t.Fatalf("log leaked: %s", buf.String())
	}
}

func TestGateUnlockNeverLogsPassword(t *testing.T) {
	e := newGateEnv(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": "wrong-guess-123"}, 401, nil)
	resp := e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
	c := gateCookieFrom(resp)
	log := buf.String()
	if strings.Contains(log, testAccessPassword) || strings.Contains(log, "wrong-guess-123") || (c != nil && strings.Contains(log, c.Value)) {
		t.Fatalf("log carries a secret: %s", log)
	}
}

// ACNOLOGIA_ACCESS_TTL sets how long an unlock lasts: the cookie's lifetime,
// the server's own cutoff, and what /api/gate tells the lock screen.
func TestGateCustomTTL(t *testing.T) {
	e := newEnv(t)
	e.api.Cfg.AccessTTL = 7 * 24 * time.Hour
	e.api.setAccessPassword(testAccessPassword)
	srv := httptest.NewServer(e.api.Router())
	t.Cleanup(srv.Close)
	e.srv = srv

	var st gateStateBody
	e.do(client(), "GET", "/api/gate", nil, 200, &st)
	if st.UnlockTTLSec != 7*24*3600 {
		t.Fatalf("unlock_ttl_sec: %d", st.UnlockTTLSec)
	}
	resp := e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
	if c := gateCookieFrom(resp); c == nil || c.MaxAge != 7*24*3600 {
		t.Fatalf("cookie lifetime: %+v", c)
	}
	g := e.api.gate
	if !g.valid(g.issue(time.Now().Add(-6 * 24 * time.Hour))) {
		t.Fatal("a 6 day old unlock should still pass a 7 day ttl")
	}
	if g.valid(g.issue(time.Now().Add(-8 * 24 * time.Hour))) {
		t.Fatal("an 8 day old unlock passed a 7 day ttl")
	}
}
