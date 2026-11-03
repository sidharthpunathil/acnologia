package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// scripted is an upstream whose answer per key is set by the test. Keys
// arrive in the X-Key header.
type scripted struct {
	mu     sync.Mutex
	hits   map[string]int
	total  atomic.Int32
	answer func(key string, r *http.Request) (int, map[string]string, string)
}

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k := r.Header.Get("X-Key")
	s.mu.Lock()
	s.hits[k]++
	s.mu.Unlock()
	s.total.Add(1)
	code, hdr, body := 200, map[string]string{}, `{"ok":true,"key":"`+k+`"}`
	if s.answer != nil {
		code, hdr, body = s.answer(k, r)
	}
	for h, v := range hdr {
		w.Header().Set(h, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

func (s *scripted) hitsFor(k string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[k]
}

// newFeatureEnv is newEnv with the response cache on and no default service.
func newFeatureEnv(t *testing.T) (*env, *Server) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{}, st, cat, box)
	s := New(config.Config{Version: "test", MaxBodyBytes: 1 << 20, CacheBytes: 8 << 20}, st, testPool(st, box), cat, ag,
		fstest.MapFS{"index.html": {Data: []byte("ui")}}, []byte("# Acnologia"))
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	e := &env{t: t, srv: srv, super: client(), st: st}
	e.do(e.super, "POST", "/api/auth/setup", map[string]any{"email": "root@x.io", "password": "supersecret1"}, 200, nil)
	return e, s
}

// upstream starts a scripted upstream and a generic service pointing at it.
func (e *env) upstream(slug string, extra map[string]any, keys ...string) (*scripted, store.Service) {
	e.t.Helper()
	up := &scripted{hits: map[string]int{}}
	ts := httptest.NewServer(up)
	e.t.Cleanup(ts.Close)
	body := map[string]any{"provider": "generic", "slug": slug, "name": slug, "base_url": ts.URL, "auth_type": "header", "auth_name": "X-Key"}
	for k, v := range extra {
		body[k] = v
	}
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", body, 201, &sv)
	ks := []map[string]any{}
	for _, k := range keys {
		ks = append(ks, map[string]any{"secret": k})
	}
	if len(ks) > 0 {
		e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"keys": ks}, 201, nil)
	}
	return up, sv
}

func call(t *testing.T, e *env, method, path, tok string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestBreakerOpensAndFailsFast(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("flaky", nil, "a", "b")
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 503, nil, `{"error":"down"}` }
	_, tok := e.app(map[string]any{"name": "x"})
	for i := 0; i < 10; i++ {
		if resp, _ := call(t, e, "GET", "/proxy/flaky/x", tok); resp.StatusCode != 503 {
			t.Fatalf("call %d: want the upstream 503, got %d", i, resp.StatusCode)
		}
	}
	before := up.total.Load()
	resp, body := call(t, e, "GET", "/proxy/flaky/x", tok)
	if resp.StatusCode != 503 || !strings.Contains(body, "service_unavailable") || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("breaker should fail fast: %d %s", resp.StatusCode, body)
	}
	if up.total.Load() != before {
		t.Fatal("an open breaker must not reach the upstream")
	}
	var st map[string]any
	e.do(e.super, "GET", "/api/admin/services/1/breaker", nil, 200, &st)
	if st["status"].(map[string]any)["state"] != "open" {
		t.Fatalf("status: %v", st)
	}
	// Keys must not be blamed for the provider's outage.
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	for _, k := range keys.Keys {
		if k.Status != "active" {
			t.Fatalf("key %s became %s", k.Label, k.Status)
		}
	}
	up.answer = nil
	e.do(e.super, "POST", "/api/admin/services/1/breaker/reset", nil, 200, nil)
	if resp, _ := call(t, e, "GET", "/proxy/flaky/x", tok); resp.StatusCode != 200 {
		t.Fatalf("after reset: %d", resp.StatusCode)
	}
}

func TestCacheServesRepeatsWithoutUpstream(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("search", map[string]any{"cache_ttl_sec": 60, "cost_per_request": 0.01}, "key-a")
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"results":[1,2,3]}` }
	appID, tok := e.app(map[string]any{"name": "x"})
	r1, b1 := call(t, e, "GET", "/proxy/search/q?b=2&a=1", tok)
	r2, b2 := call(t, e, "GET", "/proxy/search/q?a=1&b=2", tok)
	if r1.Header.Get("X-Acnologia-Cache") != "miss" || r2.Header.Get("X-Acnologia-Cache") != "hit" || b1 != b2 {
		t.Fatalf("want miss then hit: %q %q", r1.Header.Get("X-Acnologia-Cache"), r2.Header.Get("X-Acnologia-Cache"))
	}
	if up.total.Load() != 1 {
		t.Fatalf("upstream hit %d times", up.total.Load())
	}
	if r2.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("cached replies keep the sandbox headers")
	}
	call(t, e, "GET", "/proxy/search/q?a=1&b=2", tok, "Cache-Control", "no-cache")
	call(t, e, "POST", "/proxy/search/q?a=1&b=2", tok)
	if up.total.Load() != 3 {
		t.Fatalf("no-cache and POST must reach the upstream, total=%d", up.total.Load())
	}
	var usage struct {
		Usage store.AppUsage `json:"usage"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/usage", appID), nil, 200, &usage)
	if usage.Usage.TodayRequests != 3 || usage.Usage.TodayHits != 1 || usage.Usage.TodayCost < 0.0299 || usage.Usage.TodayCost > 0.0301 {
		t.Fatalf("usage: %+v", usage.Usage)
	}
	var out map[string]any
	e.do(e.super, "POST", "/api/admin/services/1/cache/purge", nil, 200, &out)
	if out["purged"].(float64) != 1 {
		t.Fatalf("purge: %v", out)
	}
}

func TestCacheSkipsResponsesThatEchoTheKey(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("echo", map[string]any{"cache_ttl_sec": 60}, "secret-key-123")
	_, tok := e.app(map[string]any{"name": "x"})
	r, _ := call(t, e, "GET", "/proxy/echo/me", tok)
	call(t, e, "GET", "/proxy/echo/me", tok)
	if r.Header.Get("X-Acnologia-Cache") != "bypass" || up.total.Load() != 2 {
		t.Fatalf("a body containing the key must not be cached: %q total=%d", r.Header.Get("X-Acnologia-Cache"), up.total.Load())
	}
}

func TestIdempotencyKeyReplays(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("tts", nil, "a")
	_, tok := e.app(map[string]any{"name": "x"})
	_, other := e.app(map[string]any{"name": "y"})
	call(t, e, "POST", "/proxy/tts/speak", tok, "Idempotency-Key", "abc")
	r, _ := call(t, e, "POST", "/proxy/tts/speak", tok, "Idempotency-Key", "abc")
	if r.Header.Get("Idempotent-Replayed") != "true" || up.total.Load() != 1 {
		t.Fatalf("retry with the same key should replay, total=%d", up.total.Load())
	}
	call(t, e, "POST", "/proxy/tts/speak", other, "Idempotency-Key", "abc")
	if up.total.Load() != 2 {
		t.Fatal("idempotency keys are per app")
	}
}

func TestGroupFallsThroughToNextService(t *testing.T) {
	e, _ := newFeatureEnv(t)
	first, _ := e.upstream("serp-a", nil, "a1", "a2")
	first.answer = func(string, *http.Request) (int, map[string]string, string) { return 429, nil, `{"error":"slow down"}` }
	second, _ := e.upstream("serp-b", nil, "b1")
	e.do(e.super, "POST", "/api/admin/groups", map[string]any{"slug": "search", "name": "Search", "members": []string{"serp-a", "serp-b"}}, 201, nil)
	e.do(e.super, "POST", "/api/admin/groups", map[string]any{"slug": "serp-a", "name": "Clash", "members": []string{"serp-a", "serp-b"}}, 409, nil)
	_, tok := e.app(map[string]any{"name": "x", "allowed_services": []string{"search"}})
	r, body := call(t, e, "GET", "/proxy/search/q", tok)
	if r.StatusCode != 200 || r.Header.Get("X-Acnologia-Service") != "serp-b" || !strings.Contains(body, `"b1"`) {
		t.Fatalf("group should fall through to serp-b: %d %s %s", r.StatusCode, r.Header.Get("X-Acnologia-Service"), body)
	}
	if first.total.Load() != 2 || second.total.Load() != 1 {
		t.Fatalf("hits: first=%d second=%d", first.total.Load(), second.total.Load())
	}
	// Now every first key is cooling: the group goes straight to serp-b.
	call(t, e, "GET", "/proxy/search/q", tok)
	if first.total.Load() != 2 {
		t.Fatal("cooling keys should be skipped without a request")
	}
	// The app may use the group, not its members directly.
	if r, _ := call(t, e, "GET", "/proxy/serp-a/q", tok); r.StatusCode != 403 {
		t.Fatalf("direct member access: %d", r.StatusCode)
	}
	// A malformed request fails everywhere, so it is not retried on the next member.
	first.answer = func(string, *http.Request) (int, map[string]string, string) { return 400, nil, `{"error":"bad q"}` }
	e.do(e.super, "POST", "/api/admin/keys/1/reset", nil, 200, nil)
	e.do(e.super, "POST", "/api/admin/keys/2/reset", nil, 200, nil)
	before := second.total.Load()
	if r, _ := call(t, e, "GET", "/proxy/search/q", tok); r.StatusCode != 400 || second.total.Load() != before {
		t.Fatalf("400 should be relayed from serp-a: %d", r.StatusCode)
	}
}

func TestAppLimits(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("api", nil, "a")
	_, tok := e.app(map[string]any{"name": "x", "limits": map[string]any{"daily_requests": 2}})
	for i := 0; i < 2; i++ {
		if r, _ := call(t, e, "GET", "/proxy/api/x", tok); r.StatusCode != 200 {
			t.Fatalf("call %d: %d", i, r.StatusCode)
		}
	}
	r, body := call(t, e, "GET", "/proxy/api/x", tok)
	if r.StatusCode != 429 || !strings.Contains(body, "app_limit_reached") || r.Header.Get("Retry-After") == "" {
		t.Fatalf("third call: %d %s", r.StatusCode, body)
	}
	var lease map[string]any
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "api"}, 429, &lease, "Authorization", "Bearer "+tok)
}

func TestBusyAndBannedOutcomes(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up := &scripted{hits: map[string]int{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Key", r.Header.Get("xi-api-key"))
		up.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	up.answer = func(k string, r *http.Request) (int, map[string]string, string) {
		switch {
		case r.URL.Path == "/v1/user/subscription":
			return 200, nil, `{"character_count":0,"character_limit":100}`
		case k == "busy":
			return 429, nil, `{"detail":{"status":"too_many_concurrent_requests","message":"Too many"}}`
		case k == "gone":
			return 401, nil, `{"detail":{"status":"detected_unusual_activity","message":"Unusual activity"}}`
		}
		return 200, nil, `ok`
	}
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "elevenlabs", "slug": "el", "base_url": ts.URL}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{
		{"secret": "busy", "priority": 1}, {"secret": "gone", "priority": 2}, {"secret": "good", "priority": 3}}}, 201, nil)
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"strategy": "fill_first"}, 200, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	if r, _ := call(t, e, "POST", "/proxy/el/v1/tts", tok); r.StatusCode != 200 {
		t.Fatalf("good key should answer: %d", r.StatusCode)
	}
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	busy, gone := keys.Keys[0], keys.Keys[1]
	if busy.Status != "active" || busy.ConsecutiveFails != 0 || busy.CooldownUntil == nil || time.Until(*busy.CooldownUntil) > 5*time.Second {
		t.Fatalf("busy key: status=%s fails=%d cooldown=%v", busy.Status, busy.ConsecutiveFails, busy.CooldownUntil)
	}
	if gone.Status != "banned" || gone.Validity != "invalid" {
		t.Fatalf("banned key: %s %s", gone.Status, gone.Validity)
	}
	// A health check never revives a banned key; a reset does.
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/keys/%d/check", gone.ID), nil, 409, nil)
	var k store.Key
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/keys/%d/reset", gone.ID), nil, 200, &k)
	if k.Status != "active" {
		t.Fatalf("reset: %s", k.Status)
	}
	var ov map[string]any
	e.do(e.super, "GET", "/api/admin/services/1", nil, 200, &ov)
}

func TestQuotaFromResponseHeaders(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up := &scripted{hits: map[string]int{}}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	left := atomic.Int32{}
	left.Store(5)
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		n := left.Add(-1)
		return 200, map[string]string{"X-RateLimit-Limit": "1, 100", "X-RateLimit-Remaining": fmt.Sprintf("0, %d", n), "X-RateLimit-Reset": "1, 3600"}, `{}`
	}
	spec := map[string]any{
		"base_url": ts.URL, "auth": map[string]any{"type": "header", "name": "X-Key"}, "use_standard_rules": true,
		"quota": map[string]any{"unit": "queries", "headers": map[string]any{"limit": "X-RateLimit-Limit", "remaining": "X-RateLimit-Remaining", "reset": "X-RateLimit-Reset", "index": 1}},
		"tests": []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}},
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "hdr", "name": "Header API", "spec": spec}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "hdr", "slug": "hdr", "base_url": ts.URL}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "a"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	call(t, e, "GET", "/proxy/hdr/s", tok)
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	k := keys.Keys[0]
	if k.QuotaLimit == nil || *k.QuotaLimit != 100 || k.QuotaRemaining == nil || *k.QuotaRemaining != 4 || k.QuotaResetAt == nil {
		t.Fatalf("quota from headers: limit=%v remaining=%v reset=%v", k.QuotaLimit, k.QuotaRemaining, k.QuotaResetAt)
	}
	left.Store(1)
	call(t, e, "GET", "/proxy/hdr/s", tok) // provider now reports 0 left
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &keys)
	if keys.Keys[0].Status != "exhausted" {
		t.Fatalf("0 left should exhaust the key, got %s", keys.Keys[0].Status)
	}
}

func TestAdminTokens(t *testing.T) {
	e, _ := newFeatureEnv(t)
	var out struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "ci", "scope": "read"}, 201, &out)
	if !strings.HasPrefix(out.Token, "acnadm_") {
		t.Fatalf("token %q", out.Token)
	}
	bearer := "Bearer " + out.Token
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 200, nil, "Authorization", bearer)
	e.do(http.DefaultClient, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "x", "base_url": "https://x.example"}, 403, nil, "Authorization", bearer)
	e.do(http.DefaultClient, "POST", "/api/admin/tokens", map[string]any{"name": "more", "scope": "admin"}, 403, nil, "Authorization", bearer)
	e.do(http.DefaultClient, "PATCH", "/api/admin/me", map[string]any{"name": "pwned"}, 403, nil, "Authorization", bearer)
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 401, nil, "Authorization", "Bearer acnadm_nope")

	var w struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "deploy", "scope": "write"}, 201, &w)
	e.do(http.DefaultClient, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "x", "base_url": "https://x.example"}, 201, nil, "Authorization", "Bearer "+w.Token)
	e.do(http.DefaultClient, "GET", "/api/admin/users", nil, 403, nil, "Authorization", "Bearer "+w.Token)

	var list struct{ Tokens []store.AdminToken }
	e.do(e.super, "GET", "/api/admin/tokens", nil, 200, &list)
	if len(list.Tokens) != 2 || list.Tokens[0].LastUsedAt == nil {
		t.Fatalf("tokens: %+v", list.Tokens)
	}
	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/tokens/%d", list.Tokens[0].ID), nil, 200, nil)
	e.do(http.DefaultClient, "GET", "/api/admin/services", nil, 401, nil, "Authorization", "Bearer "+w.Token)
}

func TestWebhooksAPI(t *testing.T) {
	e, _ := newFeatureEnv(t)
	var created struct {
		Webhook store.Webhook `json:"webhook"`
		Secret  string        `json:"signing_secret"`
	}
	e.do(e.super, "POST", "/api/admin/webhooks", map[string]any{"name": "ops", "url": "https://hooks.example.com/T000/B000/xyz", "events": []string{"key.invalid", "pool.empty"}}, 201, &created)
	if !strings.HasPrefix(created.Secret, "whsec_") || created.Webhook.URLHint != "https://hooks.example.com/…" {
		t.Fatalf("created: %+v", created)
	}
	e.do(e.super, "POST", "/api/admin/webhooks", map[string]any{"name": "bad", "url": "https://x.example", "events": []string{"nope"}}, 400, nil)
	e.do(e.super, "POST", "/api/admin/webhooks", map[string]any{"name": "bad", "url": "ftp://x.example"}, 400, nil)
	// Loopback targets are refused by default.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer local.Close()
	var h struct {
		Webhook store.Webhook `json:"webhook"`
	}
	e.do(e.super, "POST", "/api/admin/webhooks", map[string]any{"name": "local", "url": local.URL, "format": "slack"}, 201, &h)
	var res map[string]any
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/webhooks/%d/test", h.Webhook.ID), nil, 200, &res)
	if res["ok"] != false || !strings.Contains(fmt.Sprint(res["error"]), "non-public") {
		t.Fatalf("loopback webhook should be refused: %v", res)
	}
	var list struct {
		Webhooks   []store.Webhook  `json:"webhooks"`
		EventTypes []map[string]any `json:"event_types"`
	}
	e.do(e.super, "GET", "/api/admin/webhooks", nil, 200, &list)
	if len(list.Webhooks) != 2 || len(list.EventTypes) < 8 || list.Webhooks[1].LastError == "" {
		t.Fatalf("list: %+v", list)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "B000") {
		t.Fatal("webhook URLs must never be returned")
	}
}

// forwardProxy is a minimal HTTP forward proxy that counts requests.
type forwardProxy struct{ n atomic.Int32 }

func (p *forwardProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.n.Add(1)
	r.RequestURI = ""
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		w.WriteHeader(502)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func TestEgressProxyCarriesTraffic(t *testing.T) {
	e, _ := newFeatureEnv(t)
	fp := &forwardProxy{}
	ps := httptest.NewServer(fp)
	defer ps.Close()
	up, _ := e.upstream("api", nil, "a")
	var p store.EgressProxy
	e.do(e.super, "POST", "/api/admin/egress", map[string]any{"name": "eu-1", "url": strings.Replace(ps.URL, "http://", "http://user:pw@", 1), "region": "eu"}, 201, &p)
	if !strings.Contains(p.URLHint, "user:***@") {
		t.Fatalf("hint leaks credentials: %q", p.URLHint)
	}
	_, tok := e.app(map[string]any{"name": "x"})
	call(t, e, "GET", "/proxy/api/x", tok)
	if fp.n.Load() != 0 {
		t.Fatal("no proxy assigned yet")
	}
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"egress_id": p.ID}, 200, nil)
	if r, _ := call(t, e, "GET", "/proxy/api/x", tok); r.StatusCode != 200 || fp.n.Load() != 1 {
		t.Fatalf("traffic should go through the proxy: %d n=%d", r.StatusCode, fp.n.Load())
	}
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"egress_id": 9999}, 400, nil)
	var list struct{ Proxies []store.EgressProxy }
	e.do(e.super, "GET", "/api/admin/egress", nil, 200, &list)
	if len(list.Proxies) != 1 || list.Proxies[0].Services != 1 {
		t.Fatalf("list: %+v", list.Proxies)
	}
	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/egress/%d", p.ID), nil, 200, nil)
	call(t, e, "GET", "/proxy/api/x", tok)
	if fp.n.Load() != 1 || up.total.Load() != 3 {
		t.Fatalf("deleting the proxy should go direct again: proxy=%d up=%d", fp.n.Load(), up.total.Load())
	}
}
