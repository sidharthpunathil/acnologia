package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// fakeSerp behaves like SerpAPI: some keys are out of searches, some hourly capped.
type fakeSerp struct {
	mu   sync.Mutex
	hits map[string]int
	bad  map[string]string // key -> error text
}

func (f *fakeSerp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k := r.URL.Query().Get("api_key")
	f.mu.Lock()
	f.hits[k]++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	q := r.URL.Query()
	auth := r.Header.Get("Authorization")
	if q.Get("acn_token") != "" || q.Get("cogito_token") != "" || r.Header.Get("X-Acnologia-Token") != "" || r.Header.Get("X-Cogito-Token") != "" || r.Header.Get("X-Acnologia-Access") != "" ||
		strings.HasPrefix(auth, "Bearer acn_") || strings.HasPrefix(auth, "Bearer cog_") {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"acnologia token leaked upstream"}`))
		return
	}
	if msg, ok := f.bad[k]; ok && (r.URL.Path != "/account.json" || strings.Contains(msg, "Invalid")) {
		code := 429
		if strings.Contains(msg, "Invalid") {
			code = 401
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	if r.URL.Path == "/account.json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"searches_per_month": 100, "total_searches_left": 42})
		return
	}
	// key echoes the key as given, which the proxy hides from callers that
	// may not lease it; who names it in a form the proxy leaves alone.
	_ = json.NewEncoder(w).Encode(map[string]any{"search_metadata": map[string]string{"status": "Success"}, "key": k, "who": strings.ToUpper(k), "q": r.URL.Query().Get("q")})
}

type env struct {
	t     *testing.T
	srv   *httptest.Server
	up    *fakeSerp
	upURL string
	super *http.Client
	st    *store.Store
	api   *Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	up := &fakeSerp{hits: map[string]int{}, bad: map[string]string{}}
	ups := httptest.NewServer(up)
	t.Cleanup(ups.Close)
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{}, st, cat, box)
	s := New(config.Config{Version: "test", MaxBodyBytes: 1 << 20}, st, testPool(st, box), cat, ag,
		fstest.MapFS{"index.html": {Data: []byte("ui")}}, []byte("# Acnologia {{BASE_URL}}"))
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	e := &env{t: t, srv: srv, up: up, upURL: ups.URL, super: client(), st: st, api: s}
	e.do(e.super, "POST", "/api/auth/setup", map[string]any{"email": "root@x.io", "password": "supersecret1"}, 200, nil)
	var svc store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serpapi", "base_url": ups.URL}, 201, &svc)
	return e
}

// testPool builds a pool wired like the server's.
func testPool(st *store.Store, box *crypto.Box) *pool.Pool {
	p := pool.New(st, box)
	p.KeyScope = pool.StoreKeyScope(st)
	return p
}

func client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func (e *env) do(c *http.Client, method, path string, body any, want int, out any, hdr ...string) *http.Response {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "test")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s: want %d got %d: %s", method, path, want, resp.StatusCode, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("decode %s: %v: %s", path, err, b)
		}
	}
	return resp
}

func (e *env) addKeys(keys ...map[string]any) []store.Key {
	var out struct {
		Keys []store.Key `json:"keys"`
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": keys}, 201, &out)
	return out.Keys
}

func (e *env) app(body map[string]any) (int64, string) {
	var out struct {
		App   store.App `json:"app"`
		Token string    `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/apps", body, 201, &out)
	return out.App.ID, out.Token
}

func TestProxyRotatesOnRateLimit(t *testing.T) {
	e := newEnv(t)
	e.up.bad["k1"] = "Your account has run out of searches."
	e.up.bad["k2"] = "Your searches for the past hour have exceeded the limit."
	e.addKeys(map[string]any{"secret": "k1", "priority": 1}, map[string]any{"secret": "k2", "priority": 2}, map[string]any{"secret": "k3", "priority": 3})
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"strategy": "fill_first"}, 200, nil)
	_, tok := e.app(map[string]any{"name": "site"})

	var body map[string]any
	resp := e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=coffee&acn_token="+tok, nil, 200, &body)
	if body["key"] != "k3" || body["q"] != "coffee" {
		t.Fatalf("expected k3 to answer, got %v", body)
	}
	if resp.Header.Get("X-Acnologia-Attempts") != "3" {
		t.Fatalf("attempts header = %q", resp.Header.Get("X-Acnologia-Attempts"))
	}
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	got := []string{}
	for _, k := range keys.Keys {
		got = append(got, k.EffectiveStatus)
	}
	if strings.Join(got, ",") != "exhausted,cooling,active" {
		t.Fatalf("statuses = %v", got)
	}
	// Next call goes straight to k3 without touching the bad keys.
	before := e.up.hits["k1"]
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=tea", nil, 200, &body, "Authorization", "Bearer "+tok)
	if e.up.hits["k1"] != before {
		t.Fatal("exhausted key was used again")
	}
}

func TestNoKeyAvailable(t *testing.T) {
	e := newEnv(t)
	e.up.bad["k1"] = "Your searches for the past hour have exceeded the limit."
	e.addKeys(map[string]any{"secret": "k1"})
	_, tok := e.app(map[string]any{"name": "site"})
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json", nil, 429, nil, "Authorization", "Bearer "+tok)
	var out struct {
		RetryAfter int `json:"retry_after_sec"`
	}
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 503, &out, "Authorization", "Bearer "+tok)
	if out.RetryAfter <= 0 {
		t.Fatalf("expected retry_after_sec > 0, got %d", out.RetryAfter)
	}
}

func TestLeaseReportRotate(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "aaaa1111"}, map[string]any{"secret": "bbbb2222"})
	_, tok := e.app(map[string]any{"name": "site", "mode": "lease"})
	auth := []string{"Authorization", "Bearer " + tok}
	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
	if l.Auth.Type != "query" || l.Auth.Name != "api_key" {
		t.Fatalf("auth = %+v", l.Auth)
	}
	var rep struct {
		Next *leaseDTO `json:"next"`
	}
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "rate_limited", "retry_after_sec": 60, "rotate": true}, 200, &rep, auth...)
	if rep.Next == nil || rep.Next.Secret == l.Secret {
		t.Fatalf("expected a different key, got %+v", rep.Next)
	}
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "ok"}, 409, nil, auth...)
	// Lease-only app cannot proxy.
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json", nil, 403, nil, auth...)
}

func TestBackupOnlyWhenPrimaryGone(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "primary1"}, map[string]any{"secret": "backup01", "tier": "backup"})
	_, tok := e.app(map[string]any{"name": "site"})
	auth := []string{"Authorization", "Bearer " + tok}
	for i := 0; i < 3; i++ {
		var l leaseDTO
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
		if l.Secret != "primary1" {
			t.Fatalf("backup used while primary available")
		}
		if i == 2 {
			var rep struct{ Next *leaseDTO }
			e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "quota_exhausted", "rotate": true}, 200, &rep, auth...)
			if rep.Next == nil || rep.Next.Secret != "backup01" {
				t.Fatalf("expected backup after primary exhausted, got %+v", rep.Next)
			}
		}
	}
}

func TestRestrictedKeys(t *testing.T) {
	e := newEnv(t)
	vip, vipTok := e.app(map[string]any{"name": "vip"})
	_, otherTok := e.app(map[string]any{"name": "other"})
	e.addKeys(map[string]any{"secret": "vipkey01", "restricted": true, "app_ids": []int64{vip}})
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 503, nil, "Authorization", "Bearer "+otherTok)
	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, "Authorization", "Bearer "+vipTok)
	if l.Secret != "vipkey01" {
		t.Fatal("vip app did not get its key")
	}
}

func TestHiddenKeysAndRoles(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "visible1"}, map[string]any{"secret": "hidden01", "hidden": true})
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	admin, member := client(), client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	e.do(member, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)

	count := func(c *http.Client) int {
		var out struct{ Services []store.Service }
		e.do(c, "GET", "/api/admin/services", nil, 200, &out)
		return out.Services[0].KeyCounts.Total
	}
	if count(e.super) != 2 || count(admin) != 1 || count(member) != 1 {
		t.Fatalf("counts super=%d admin=%d member=%d", count(e.super), count(admin), count(member))
	}
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	var hiddenID int64
	for _, k := range keys.Keys {
		if k.Hidden {
			hiddenID = k.ID
		}
	}
	id := itoa(int(hiddenID))
	e.do(admin, "PATCH", "/api/admin/keys/"+id, map[string]any{"label": "x"}, 404, nil)
	e.do(admin, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "zzz", "hidden": true}}}, 403, nil)
	e.do(admin, "POST", "/api/admin/keys/1/reveal", map[string]any{}, 403, nil)
	e.do(admin, "DELETE", "/api/admin/services/1", nil, 403, nil)
	e.do(member, "POST", "/api/admin/apps", map[string]any{"name": "nope"}, 403, nil)
	e.do(member, "GET", "/api/admin/users", nil, 403, nil)
	var rev struct{ Secret string }
	e.do(e.super, "POST", "/api/admin/keys/"+id+"/reveal", map[string]any{}, 200, &rev)
	if rev.Secret != "hidden01" {
		t.Fatal("reveal returned wrong secret")
	}
	// A lease hands out the plaintext, so hidden keys go only to apps a
	// superadmin cleared. Without that, the hidden key is invisible to leases.
	e.do(e.super, "PATCH", "/api/admin/keys/1", map[string]any{"status": "disabled"}, 200, nil)
	plainID, plainTok := e.app(map[string]any{"name": "plain"})
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 503, nil, "Authorization", "Bearer "+plainTok)
	// Admins cannot grant it, on create or update.
	e.do(admin, "POST", "/api/admin/apps", map[string]any{"name": "sneaky", "lease_hidden": true}, 403, nil)
	e.do(admin, "PATCH", "/api/admin/apps/"+itoa(int(plainID)), map[string]any{"lease_hidden": true}, 403, nil)
	// A superadmin can.
	siteID, tok := e.app(map[string]any{"name": "site", "lease_hidden": true})
	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, "Authorization", "Bearer "+tok)
	if l.Secret != "hidden01" {
		t.Fatal("hidden key not used")
	}
	// An admin rotating that app's token gets the token, so the grant is revoked.
	var rot struct{ Token string }
	e.do(admin, "POST", "/api/admin/apps/"+itoa(int(siteID))+"/rotate-token", map[string]any{}, 200, &rot)
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 503, nil, "Authorization", "Bearer "+rot.Token)

	// Upstreams can echo the key, so proxying a hidden key needs the same
	// grant. An admin's proxy app never reaches it.
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 503, nil, "Authorization", "Bearer "+plainTok)
	_, trusted := e.app(map[string]any{"name": "trusted", "lease_hidden": true})
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+trusted)
	// Not once an admin points the service somewhere else.
	e.do(admin, "PATCH", "/api/admin/services/1", map[string]any{"base_url": e.upURL + "/v2"}, 200, nil)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 503, nil, "Authorization", "Bearer "+trusted)
	// A superadmin changing an unrelated field does not re-trust the target.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"cooldown_sec": 30}, 200, nil)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 503, nil, "Authorization", "Bearer "+trusted)
	// Submitting the target confirms it.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"base_url": e.upURL}, 200, nil)
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+trusted)
}

func TestHealthCheckAndDuplicates(t *testing.T) {
	e := newEnv(t)
	e.up.bad["badkey99"] = "Invalid API key. Your API key should be here"
	ks := e.addKeys(map[string]any{"secret": "goodkey1"}, map[string]any{"secret": "badkey99"})
	var k store.Key
	e.do(e.super, "POST", "/api/admin/keys/"+itoa(int(ks[0].ID))+"/check", map[string]any{}, 200, &k)
	if k.QuotaRemaining == nil || *k.QuotaRemaining != 42 || *k.QuotaLimit != 100 {
		t.Fatalf("quota not read: %+v", k)
	}
	e.do(e.super, "POST", "/api/admin/keys/"+itoa(int(ks[1].ID))+"/check", map[string]any{}, 200, &k)
	if k.Status != "invalid" {
		t.Fatalf("bad key status %s", k.Status)
	}
	var out struct {
		Keys    []store.Key
		Skipped []string
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "goodkey1"}}}, 201, &out)
	if len(out.Skipped) != 1 {
		t.Fatal("duplicate not skipped")
	}
}

func TestMCP(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "mcpkey01"})
	_, tok := e.app(map[string]any{"name": "agent"})
	auth := []string{"Authorization", "Bearer " + tok}
	var res map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200, &res, auth...)
	if len(res["result"].(map[string]any)["tools"].([]any)) < 6 {
		t.Fatal("expected the core tools plus generated service tools")
	}
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "proxy_request", "arguments": map[string]any{"service": "serpapi", "path": "/search.json", "query": map[string]string{"q": "mcp"}}}}, 200, &res, auth...)
	sc := res["result"].(map[string]any)["structuredContent"].(map[string]any)
	if sc["status"].(float64) != 200 || sc["body"].(map[string]any)["q"] != "mcp" {
		t.Fatalf("proxy via mcp: %v", sc)
	}
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "initialize"}, 401, nil)
}

func TestCSRFGuard(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/admin/apps", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := e.super.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 415 {
		t.Fatalf("want 415 got %d", resp.StatusCode)
	}
}

func TestNoIndex(t *testing.T) {
	e := newEnv(t)
	resp := e.do(http.DefaultClient, "GET", "/robots.txt", nil, 200, nil)
	if !strings.Contains(resp.Header.Get("X-Robots-Tag"), "noindex") {
		t.Fatal("missing X-Robots-Tag")
	}
	resp = e.do(http.DefaultClient, "GET", "/", nil, 200, nil)
	if !strings.Contains(resp.Header.Get("X-Robots-Tag"), "nofollow") || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("UI must not be indexable or framable")
	}
}

func TestKeyInsights(t *testing.T) {
	e := newEnv(t)
	e.up.bad["badkey99"] = "Invalid API key. Your API key should be here"
	e.addKeys(map[string]any{"secret": "goodkey1", "label": "good"}, map[string]any{"secret": "badkey99", "label": "bad"}, map[string]any{"secret": "freshkey", "label": "fresh", "status": "active"})
	var sum struct {
		Checked, Valid, Invalid int
		Message                 string
	}
	e.do(e.super, "POST", "/api/admin/services/1/check", map[string]any{}, 200, &sum)
	if sum.Checked != 3 || sum.Valid != 2 || sum.Invalid != 1 {
		t.Fatalf("check summary: %+v", sum)
	}
	var all struct {
		Keys []struct {
			Label, Validity, QuotaUnit, ServiceSlug string
			ValidityReason                          string `json:"validity_reason"`
			HasHealthCheck                          bool   `json:"has_health_check"`
		} `json:"keys"`
		Summary map[string]int `json:"summary"`
	}
	e.do(e.super, "GET", "/api/admin/keys?sort=validity", nil, 200, &all)
	if all.Summary["invalid"] != 1 || all.Summary["valid"] != 2 || all.Keys[0].Label != "bad" || all.Keys[0].Validity != "invalid" {
		t.Fatalf("all keys: %+v", all)
	}
	var ins struct {
		Key struct {
			Validity  string `json:"validity"`
			QuotaUnit string `json:"quota_unit"`
		} `json:"key"`
		Daily          []map[string]any `json:"daily"`
		HasHealthCheck bool             `json:"has_health_check"`
	}
	e.do(e.super, "GET", "/api/admin/keys/1/insights", nil, 200, &ins)
	if ins.Key.Validity != "valid" || ins.Key.QuotaUnit != "searches" || len(ins.Daily) != 14 || !ins.HasHealthCheck {
		t.Fatalf("insights: %+v", ins)
	}
	// Custom API providers have no health check: checking says so instead of faking "valid".
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "custom", "base_url": "https://example.com"}, 201, &sv)
	var out struct {
		Keys []store.Key `json:"keys"`
	}
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "zzzz1111"}}}, 201, &out)
	e.do(e.super, "POST", "/api/admin/keys/"+itoa(int(out.Keys[0].ID))+"/check", map[string]any{}, 422, nil)
	if out.Keys[0].Validity != "unknown" {
		t.Fatalf("unchecked key validity = %s", out.Keys[0].Validity)
	}
}

func TestRoundRobinSpreadsBursts(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "rr_one_11"}, map[string]any{"secret": "rr_two_22"}, map[string]any{"secret": "rr_three_3"})
	_, tok := e.app(map[string]any{"name": "site"})
	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		resp := e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=x", nil, 200, nil, "Authorization", "Bearer "+tok)
		seen[resp.Header.Get("X-Acnologia-Key-Id")]++
	}
	if len(seen) != 3 || seen["1"] != 3 || seen["2"] != 3 || seen["3"] != 3 {
		t.Fatalf("round robin within one second should spread evenly, got %v", seen)
	}
}
