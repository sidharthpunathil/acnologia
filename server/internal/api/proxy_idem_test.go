package api

import (
	"bytes"
	"context"
	"fmt"
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

// newIdemEnv is newFeatureEnv with a chosen response cache size, and a
// restart that starts a new Server on the same database: nothing in
// memory (cache, claims, slots) carries over.
func newIdemEnv(t *testing.T, cacheBytes int64) (*env, func() *env) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	start := func() *env {
		box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
		cat := &catalog.Catalog{Store: st}
		if err := cat.Boot(context.Background()); err != nil {
			t.Fatal(err)
		}
		ag := agent.New(agent.Config{}, st, cat, box)
		s := New(config.Config{Version: "test", MaxBodyBytes: 1 << 20, CacheBytes: cacheBytes}, st, testPool(st, box), cat, ag,
			fstest.MapFS{"index.html": {Data: []byte("ui")}}, []byte("# Acnologia"))
		srv := httptest.NewServer(s.Router())
		t.Cleanup(srv.Close)
		return &env{t: t, srv: srv, super: client(), st: st, api: s}
	}
	e := start()
	e.do(e.super, "POST", "/api/auth/setup", map[string]any{"email": "root@x.io", "password": "supersecret1"}, 200, nil)
	return e, start
}

// A stored answer is replayed after a restart, and with the response
// cache off.
func TestIdempotencySurvivesRestartWithCacheOff(t *testing.T) {
	e, restart := newIdemEnv(t, 0)
	up, _ := e.upstream("pay", nil, "k1")
	var n atomic.Int32
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		return 201, map[string]string{"X-Charge": "c1"}, fmt.Sprintf(`{"charge":%d}`, n.Add(1))
	}
	_, tok := e.app(map[string]any{"name": "x"})
	r, body := callBody(t, e, "POST", "/proxy/pay/charge", tok, `{"amount":1}`, "Idempotency-Key", "order-1")
	if r.StatusCode != 201 || body != `{"charge":1}` {
		t.Fatalf("first: %d %s", r.StatusCode, body)
	}
	if r, body := callBody(t, e, "POST", "/proxy/pay/charge", tok, `{"amount":1}`, "Idempotency-Key", "order-1"); r.Header.Get("Idempotent-Replayed") != "true" || body != `{"charge":1}` {
		t.Fatalf("replay with the cache off: %v %s", r.Header, body)
	}
	e2 := restart()
	r, body = callBody(t, e2, "POST", "/proxy/pay/charge", tok, `{"amount":1}`, "Idempotency-Key", "order-1")
	if r.StatusCode != 201 || body != `{"charge":1}` || r.Header.Get("Idempotent-Replayed") != "true" || r.Header.Get("X-Charge") != "c1" || up.total.Load() != 1 {
		t.Fatalf("after a restart: %d %s %v (upstream calls %d)", r.StatusCode, body, r.Header, up.total.Load())
	}
	if r, body := callBody(t, e2, "POST", "/proxy/pay/charge", tok, `{"amount":2}`, "Idempotency-Key", "order-1"); r.StatusCode != 422 || !strings.Contains(body, "idempotency_key_reused") {
		t.Fatalf("another body after a restart: %d %s", r.StatusCode, body)
	}
	// The answer is stored encrypted.
	rec, err := e.st.GetIdempotency(context.Background(), idemHash(idempotencyKey(mustReq("POST", "/proxy/pay/charge", "order-1"), mustApp(t, e, tok), "pay", "charge")))
	if err != nil || bytes.Contains(rec.Payload, []byte("charge")) {
		t.Fatalf("stored record: %v", err)
	}
}

func mustReq(method, path, key string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Idempotency-Key", key)
	return r
}

func mustApp(t *testing.T, e *env, tok string) *store.App {
	t.Helper()
	a, _, err := e.st.AppByToken(context.Background(), crypto.SHA256(tok))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Many requests with one key at once: one reaches the provider, the
// others get 409 while it runs or its answer after.
func TestIdempotencyConcurrentClaim(t *testing.T) {
	e, _ := newIdemEnv(t, 8<<20)
	up, _ := e.upstream("slow", nil, "k1", "k2")
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		time.Sleep(100 * time.Millisecond)
		return 200, nil, `{"done":true}`
	}
	_, tok := e.app(map[string]any{"name": "x"})
	var wg sync.WaitGroup
	var ok, busy, replayed atomic.Int32
	for range 12 {
		wg.Go(func() {
			r, _ := call(t, e, "POST", "/proxy/slow/send", tok, "Idempotency-Key", "same")
			switch {
			case r.StatusCode == 409:
				busy.Add(1)
			case r.Header.Get("Idempotent-Replayed") == "true":
				replayed.Add(1)
			case r.StatusCode == 200:
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if up.total.Load() != 1 || ok.Load() != 1 || busy.Load()+replayed.Load() != 11 {
		t.Fatalf("upstream %d, ran %d, 409 %d, replayed %d", up.total.Load(), ok.Load(), busy.Load(), replayed.Load())
	}
}

// Cache traffic cannot evict an idempotent answer: it is not in the cache.
func TestIdempotencyIndependentOfCacheEviction(t *testing.T) {
	e, _ := newIdemEnv(t, 16<<10)
	up, _ := e.upstream("svc", map[string]any{"cache_ttl_sec": 60}, "k1")
	up.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		if r.Method == http.MethodPost {
			return 200, nil, `{"order":"first"}`
		}
		return 200, nil, `{"page":"` + r.URL.Path + strings.Repeat("x", 2000) + `"}`
	}
	_, tok := e.app(map[string]any{"name": "x"})
	call(t, e, "POST", "/proxy/svc/order", tok, "Idempotency-Key", "o1")
	for i := range 50 {
		call(t, e, "GET", fmt.Sprintf("/proxy/svc/page/%d", i), tok)
	}
	r, body := call(t, e, "POST", "/proxy/svc/order", tok, "Idempotency-Key", "o1")
	if r.Header.Get("Idempotent-Replayed") != "true" || body != `{"order":"first"}` {
		t.Fatalf("idempotent answer lost to cache traffic: %v %s", r.Header, body)
	}
}

// An answer over the cap is delivered whole once; a retry is refused, not
// run again.
func TestIdempotencyLargeAnswerIsUnreplayable(t *testing.T) {
	e, _ := newIdemEnv(t, 0)
	up, _ := e.upstream("big", nil, "k1")
	big := strings.Repeat("a", idemMaxBody+10)
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, big }
	_, tok := e.app(map[string]any{"name": "x"})
	if r, body := call(t, e, "POST", "/proxy/big/export", tok, "Idempotency-Key", "e1"); r.StatusCode != 200 || len(body) != len(big) {
		t.Fatalf("first: %d, %d bytes", r.StatusCode, len(body))
	}
	r, body := call(t, e, "POST", "/proxy/big/export", tok, "Idempotency-Key", "e1")
	if r.StatusCode != 422 || !strings.Contains(body, "idempotency_unreplayable") || up.total.Load() != 1 {
		t.Fatalf("retry: %d %s (upstream %d)", r.StatusCode, body, up.total.Load())
	}
	// Up to the cap, the answer is kept.
	fits := strings.Repeat("b", 2<<20)
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, fits }
	call(t, e, "POST", "/proxy/big/export", tok, "Idempotency-Key", "e2")
	if r, body := call(t, e, "POST", "/proxy/big/export", tok, "Idempotency-Key", "e2"); r.Header.Get("Idempotent-Replayed") != "true" || len(body) != len(fits) {
		t.Fatalf("a 2 MB answer replays: %v %d", r.Header, len(body))
	}
}

// A pending record a stopped server left behind blocks the key until it
// is stale, then the request runs; a client that hangs up frees its key.
func TestIdempotencyPendingRecords(t *testing.T) {
	e, _ := newIdemEnv(t, 0)
	up, _ := e.upstream("svc", nil, "k1")
	_, tok := e.app(map[string]any{"name": "x"})
	a := mustApp(t, e, tok)
	ctx := context.Background()
	left := func(key string, age time.Duration) {
		t.Helper()
		hash := idemHash(idempotencyKey(mustReq("POST", "/proxy/svc/run", key), a, "svc", "run"))
		at := time.Now().Add(-age)
		if ok, err := e.st.ClaimIdempotency(ctx, hash, a.ID, a.TokenID, bodyFingerprint(nil), "gone", at, at.Add(idemTTL), at.Add(-idemStaleAfter)); !ok || err != nil {
			t.Fatalf("seed: %v %v", ok, err)
		}
	}
	left("fresh", time.Minute)
	if r, body := call(t, e, "POST", "/proxy/svc/run", tok, "Idempotency-Key", "fresh"); r.StatusCode != 409 || !strings.Contains(body, "idempotency_in_progress") || up.total.Load() != 0 {
		t.Fatalf("a recent pending record: %d %s", r.StatusCode, body)
	}
	left("stale", idemStaleAfter+time.Minute)
	if r, _ := call(t, e, "POST", "/proxy/svc/run", tok, "Idempotency-Key", "stale"); r.StatusCode != 200 || up.total.Load() != 1 {
		t.Fatalf("a stale pending record is taken over: %d", r.StatusCode)
	}

	// The client hangs up while the provider works: the key is released.
	release := make(chan struct{})
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		<-release
		return 200, nil, `{"ok":true}`
	}
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	req, _ := http.NewRequestWithContext(cctx, "POST", e.srv.URL+"/proxy/svc/run", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Idempotency-Key", "hangup")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	cancel()
	// Give the server time to see the hang up before the provider answers.
	time.Sleep(300 * time.Millisecond)
	close(release)
	hash := idemHash(idempotencyKey(mustReq("POST", "/proxy/svc/run", "hangup"), a, "svc", "run"))
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := e.st.GetIdempotency(ctx, hash); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the key of a request whose client hung up was never released")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r, _ := call(t, e, "POST", "/proxy/svc/run", tok, "Idempotency-Key", "hangup"); r.StatusCode != 200 || r.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("retry after a hang up runs: %d", r.StatusCode)
	}
}

// The MCP proxy_request tool's idempotency_key goes through the same
// records, with the response cache off.
func TestMCPIdempotencyKeyWithCacheOff(t *testing.T) {
	e, _ := newIdemEnv(t, 0)
	up, _ := e.upstream("shop", nil, "k1")
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 201, nil, `{"id":42}` }
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/mcp", appID), map[string]any{"core": true}, 200, nil)
	m := &mcpClient{e: e, tok: tok}
	args := map[string]any{"service": "shop", "method": "POST", "path": "/order", "body": map[string]any{"n": 1}, "idempotency_key": "order-1"}
	if first, isErr := m.tool("proxy_request", args); isErr || first["status"].(float64) != 201 {
		t.Fatalf("first: %v", first)
	}
	again, _ := m.tool("proxy_request", args)
	if again["cache"] != "replay" || up.total.Load() != 1 {
		t.Fatalf("a repeated idempotency key must replay: %v (upstream %d)", again, up.total.Load())
	}
}
