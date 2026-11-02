package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// raw sends a request without the helper's JSON and CSRF headers.
func (e *env) raw(c *http.Client, method, path, ctype string, body []byte, hdr ...string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestLoginLimiterIgnoresSpoofedIPHeaders(t *testing.T) {
	e := newEnv(t)
	got429 := false
	for i := 0; i < 15; i++ {
		c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp := e.raw(c, "POST", "/api/auth/login", "application/json",
			[]byte(`{"email":"root@x.io","password":"wrongwrong1"}`), "X-Real-IP", "10.0.0."+itoa(i), "X-Forwarded-For", "10.1.0."+itoa(i))
		if resp.StatusCode == 429 {
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("spoofed client IP headers and fresh connections bypassed the login limiter")
	}
}

func TestLoginLimiterDoesNotGrowForUnknownKeys(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < 1000; i++ {
		l.allow("ip-" + itoa(i))
	}
	if len(l.hits) != 0 {
		t.Fatalf("allow() stored %d entries for keys with no failures", len(l.hits))
	}
}

func TestSetupRaceCreatesOneSuperadmin(t *testing.T) {
	e := newEnv(t)
	// newEnv already ran setup; wipe users to reopen the first-run window.
	if _, err := e.st.DB.Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{"email": "u" + itoa(i) + "@x.io", "password": "supersecret1"})
			resp := e.raw(client(), "POST", "/api/auth/setup", "application/json", body)
			if resp.StatusCode == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if n, _ := e.st.CountUsers(context.Background()); ok != 1 || n != 1 {
		t.Fatalf("setup race: %d successes, %d users", ok, n)
	}
}

func TestCSRFBodylessTextPlainRejected(t *testing.T) {
	e := newEnv(t)
	id, _ := e.app(map[string]any{"name": "site"})
	resp := e.raw(e.super, "POST", "/api/admin/apps/"+itoa(int(id))+"/rotate-token", "text/plain", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("bodyless text/plain POST got %d, want 400 csrf", resp.StatusCode)
	}
}

func TestLongPasswordRejectedNotStoredBroken(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("a", 100)
	e.do(e.super, "PATCH", "/api/admin/me", map[string]any{"current_password": "supersecret1", "password": long}, 400, nil)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "l@x.io", "password": long, "role": "member"}, 400, nil)
	// The old password still works.
	e.do(client(), "POST", "/api/auth/login", map[string]any{"email": "root@x.io", "password": "supersecret1"}, 200, nil)
}

func TestPasswordResetEndsTargetSessions(t *testing.T) {
	e := newEnv(t)
	var u store.User
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, &u)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	e.do(e.super, "PATCH", "/api/admin/users/"+itoa(int(u.ID)), map[string]any{"password": "brandnewpass1"}, 200, nil)
	e.do(m, "GET", "/api/admin/me", nil, 401, nil)
}

func TestDisabledSuperadminCanBeDeleted(t *testing.T) {
	e := newEnv(t)
	var u store.User
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "s2@x.io", "password": "superpass12", "role": "superadmin"}, 201, &u)
	e.do(e.super, "PATCH", "/api/admin/users/"+itoa(int(u.ID)), map[string]any{"disabled": true}, 200, nil)
	e.do(e.super, "DELETE", "/api/admin/users/"+itoa(int(u.ID)), nil, 200, nil)
}

func TestProxyRejectsQueryAndFragmentInPath(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"}, map[string]any{"secret": "k2"})
	_, tok := e.app(map[string]any{"name": "site"})
	if resp := e.raw(http.DefaultClient, "GET", "/proxy/serpapi/a/%2e%2e/account.json", "", nil, "Authorization", "Bearer "+tok); resp.StatusCode != 400 {
		t.Fatalf("dot segment: got %d, want 400", resp.StatusCode)
	}
	// An encoded ? or # stays in the path and cannot move or drop the key.
	for _, p := range []string{"/proxy/serpapi/search.json%3Fq=", "/proxy/serpapi/search.json%23"} {
		e.raw(http.DefaultClient, "GET", p, "", nil, "Authorization", "Bearer "+tok)
	}
	e.up.mu.Lock()
	empty := e.up.hits[""]
	e.up.mu.Unlock()
	if empty != 0 {
		t.Fatalf("%d upstream requests arrived without the key", empty)
	}
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	for _, k := range keys.Keys {
		if k.Status != store.StatusActive {
			t.Fatalf("key %d became %s", k.ID, k.Status)
		}
	}
}

func TestProxyRefusesTraceAndSandboxesResponses(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	_, tok := e.app(map[string]any{"name": "site"})
	if resp := e.raw(http.DefaultClient, "TRACE", "/proxy/serpapi/search.json", "", nil, "Authorization", "Bearer "+tok); resp.StatusCode != 405 {
		t.Fatalf("TRACE got %d", resp.StatusCode)
	}
	resp := e.raw(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=x", "", nil, "Authorization", "Bearer "+tok)
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("proxied response not sandboxed: %v", resp.Header)
	}
}

func TestReportCooldownIsCapped(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	_, tok := e.app(map[string]any{"name": "site"})
	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, "Authorization", "Bearer "+tok)
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report",
		map[string]any{"outcome": "rate_limited", "retry_after_sec": 9000000000, "message": "boom " + l.Secret}, 200, nil, "Authorization", "Bearer "+tok)
	k, err := e.st.GetKey(context.Background(), l.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	if k.CooldownUntil == nil || k.CooldownUntil.After(time.Now().Add(25*time.Hour)) {
		t.Fatalf("cooldown not capped: %v", k.CooldownUntil)
	}
	if strings.Contains(k.LastError, l.Secret) {
		t.Fatalf("reported message leaked the secret: %q", k.LastError)
	}
	// A second report on the same lease is refused.
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "ok"}, 409, nil, "Authorization", "Bearer "+tok)
}

func TestHealthCheckErrorDoesNotStoreQuerySecret(t *testing.T) {
	e := newEnv(t)
	ks := e.addKeys(map[string]any{"secret": "sk_SUPERSECRET_123"})
	// Point the service at a closed port so the transport error quotes the URL.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"base_url": "http://127.0.0.1:1"}, 200, nil)
	e.do(e.super, "POST", "/api/admin/keys/"+itoa(int(ks[0].ID))+"/check", map[string]any{}, 502, nil)
	k, _ := e.st.GetKey(context.Background(), ks[0].ID)
	if strings.Contains(k.LastError, "SUPERSECRET") {
		t.Fatalf("last_error leaked the key: %q", k.LastError)
	}
	evs, _ := e.st.ListEvents(context.Background(), store.EventFilter{KeyID: ks[0].ID})
	for _, ev := range evs {
		if strings.Contains(ev.Detail, "SUPERSECRET") {
			t.Fatalf("event leaked the key: %q", ev.Detail)
		}
	}
}

func TestHiddenTrafficNotCountedForAdmins(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "hidden01", "hidden": true})
	_, tok := e.app(map[string]any{"name": "site", "lease_hidden": true})
	for i := 0; i < 3; i++ {
		e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	}
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	var ov struct {
		Requests24h int `json:"requests_24h"`
	}
	e.do(admin, "GET", "/api/admin/overview", nil, 200, &ov)
	var svcs struct{ Services []store.Service }
	e.do(admin, "GET", "/api/admin/services", nil, 200, &svcs)
	if ov.Requests24h != 0 || svcs.Services[0].Requests24h != 0 {
		t.Fatalf("admin sees hidden traffic: overview=%d service=%d", ov.Requests24h, svcs.Services[0].Requests24h)
	}
}

func TestDeletedKeyIDIsNotReused(t *testing.T) {
	e := newEnv(t)
	ks := e.addKeys(map[string]any{"secret": "k1"}, map[string]any{"secret": "k2"})
	last := ks[1].ID
	e.do(e.super, "DELETE", "/api/admin/keys/"+itoa(int(last)), nil, 200, nil)
	nk := e.addKeys(map[string]any{"secret": "k3"})
	if nk[0].ID == last {
		t.Fatalf("new key reused deleted id %d", last)
	}
}

func TestEventRetentionZeroKeepsEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.st.AddEvent(ctx, store.NewEvent{Kind: "proxy", Detail: "x"})
	if err := e.st.Prune(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.st.CountEvents(ctx, time.Time{}, true); n == 0 {
		t.Fatal("retention 0 deleted every event")
	}
}

func TestKeyInputValidation(t *testing.T) {
	e := newEnv(t)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "q1", "quota_limit": -5}}}, 400, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "q2", "restricted": true, "app_ids": []int64{999}}}}, 400, nil)
}

func TestUpstreamURL(t *testing.T) {
	q := func(kv ...string) map[string][]string {
		m := map[string][]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = []string{kv[i+1]}
		}
		return m
	}
	cases := []struct{ base, path, want string }{
		{"https://x.io", "a%2Fb/c", "https://x.io/a%2Fb/c?k=1"},
		{"https://x.io/v1", "files/C%23.md", "https://x.io/v1/files/C%23.md?k=1"},
		{"https://x.io/v1", "s%3Fq", "https://x.io/v1/s%3Fq?k=1"},
		{"https://x.io/base?api-version=2", "chat", "https://x.io/base/chat?api-version=2&k=1"},
		{"https://x.io", "", "https://x.io/?k=1"},
	}
	for _, c := range cases {
		got, err := upstreamURL(c.base, c.path, q("k", "1"))
		if err != nil || got != c.want {
			t.Errorf("upstreamURL(%q, %q) = %q, %v; want %q", c.base, c.path, got, err, c.want)
		}
	}
	if _, err := upstreamURL("https://x.io", "a/../b", nil); err == nil {
		t.Error("dot segment accepted")
	}
}

func TestProxyScrubsKeyFromRedirect(t *testing.T) {
	e := newEnv(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next/?"+r.URL.RawQuery, http.StatusMovedPermanently)
	}))
	defer redir.Close()
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"base_url": redir.URL}, 200, nil)
	e.addKeys(map[string]any{"secret": "REDIRSECRET9"})
	_, tok := e.app(map[string]any{"name": "site"})
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp := e.raw(c, "GET", "/proxy/serpapi/x", "", nil, "Authorization", "Bearer "+tok)
	if loc := resp.Header.Get("Location"); strings.Contains(loc, "REDIRSECRET9") || resp.StatusCode != 301 {
		t.Fatalf("redirect leaked the key or was not relayed: %d %q", resp.StatusCode, loc)
	}
}

func TestDashboardCSPAllowsShippedInlineScript(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("missing dashboard CSP: %q", csp)
	}
	h := inlineScriptHashes(fstestIndex(`<html><script>var a=1;</script><script type="module" src="/x.js"></script></html>`))
	if len(h) != 1 || !strings.Contains(dashboardCSP(h), "'sha256-"+h[0]+"'") {
		t.Fatalf("inline script hash not in CSP: %v", h)
	}
}

func fstestIndex(html string) fstest.MapFS {
	return fstest.MapFS{"index.html": {Data: []byte(html)}}
}
