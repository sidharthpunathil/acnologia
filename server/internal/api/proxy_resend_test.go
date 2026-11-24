package api

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// callBody is call with a request body.
func callBody(t *testing.T, e *env, method, path, tok, body string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
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

func groupOf(e *env, slug string, members ...string) {
	e.do(e.super, "POST", "/api/admin/groups", map[string]any{"slug": slug, "name": slug, "members": members}, 201, nil)
}

// A 5xx may mean the provider did the work, so an unsafe request is not
// sent to the next member; a refusal (429) is, and so is a GET or a request
// with an Idempotency-Key.
func TestGroupResendsOnlyWhatMayBeRepeated(t *testing.T) {
	e, _ := newFeatureEnv(t)
	first, _ := e.upstream("mail-a", nil, "a1")
	second, _ := e.upstream("mail-b", nil, "b1")
	first.answer = func(string, *http.Request) (int, map[string]string, string) { return 502, nil, `{"error":"gateway"}` }
	groupOf(e, "mail", "mail-a", "mail-b")
	_, tok := e.app(map[string]any{"name": "x", "allowed_services": []string{"mail"}})

	if r, _ := call(t, e, "POST", "/proxy/mail/send", tok); r.StatusCode != 502 || second.total.Load() != 0 {
		t.Fatalf("a POST that got a 502 must not be sent again: %d, second=%d", r.StatusCode, second.total.Load())
	}
	if r, _ := call(t, e, "GET", "/proxy/mail/status", tok); r.StatusCode != 200 || second.total.Load() != 1 {
		t.Fatalf("a GET moves on: %d, second=%d", r.StatusCode, second.total.Load())
	}
	if r, _ := call(t, e, "POST", "/proxy/mail/send", tok, "Idempotency-Key", "once"); r.StatusCode != 200 || second.total.Load() != 2 {
		t.Fatalf("a POST with an Idempotency-Key moves on: %d, second=%d", r.StatusCode, second.total.Load())
	}
	first.answer = func(string, *http.Request) (int, map[string]string, string) { return 429, nil, `{"error":"slow"}` }
	if r, _ := call(t, e, "POST", "/proxy/mail/send", tok); r.StatusCode != 200 || second.total.Load() != 3 {
		t.Fatalf("a refusal did no work, so a POST moves on: %d, second=%d", r.StatusCode, second.total.Load())
	}
}

// hangup reads the request and drops the connection: the request reached the
// provider, which may have acted on it.
type hangup struct{ n atomic.Int32 }

func (h *hangup) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.n.Add(1)
	_, _ = io.ReadAll(r.Body)
	conn, _, err := http.NewResponseController(w).Hijack()
	if err == nil {
		conn.Close()
	}
}

func TestTransportErrorsResendOnlyBeforeTheWire(t *testing.T) {
	e, _ := newFeatureEnv(t)
	hu := &hangup{}
	ts := httptest.NewServer(hu)
	t.Cleanup(ts.Close)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "lost", "name": "lost", "base_url": ts.URL,
		"auth_type": "header", "auth_name": "X-Key", "max_attempts": 1}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "l1"}}}, 201, nil)
	// A closed port: the dial fails, nothing is sent.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + ln.Addr().String()
	ln.Close()
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "dead", "name": "dead", "base_url": dead,
		"auth_type": "header", "auth_name": "X-Key"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/2/keys", map[string]any{"keys": []map[string]any{{"secret": "d1"}}}, 201, nil)
	good, _ := e.upstream("good", nil, "g1")
	groupOf(e, "lost-first", "lost", "good")
	groupOf(e, "dead-first", "dead", "good")
	_, tok := e.app(map[string]any{"name": "x"})

	if r, body := call(t, e, "POST", "/proxy/lost-first/charge", tok); r.StatusCode != 502 || good.total.Load() != 0 || hu.n.Load() != 1 {
		t.Fatalf("a POST lost in flight must not be sent again: %d %s good=%d", r.StatusCode, body, good.total.Load())
	}
	if r, _ := call(t, e, "POST", "/proxy/dead-first/charge", tok); r.StatusCode != 200 || good.total.Load() != 1 {
		t.Fatalf("a connection that never opened sent nothing, so the POST moves on: %d good=%d", r.StatusCode, good.total.Load())
	}
	if r, _ := call(t, e, "GET", "/proxy/lost-first/status", tok); r.StatusCode != 200 || good.total.Load() != 2 {
		t.Fatalf("a GET lost in flight moves on: %d good=%d", r.StatusCode, good.total.Load())
	}
}

func TestCapabilityFallbackFollowsTheResendRule(t *testing.T) {
	e, _ := newFeatureEnv(t)
	a, _ := e.upstream("sms-a", nil, "a1")
	b, _ := e.upstream("sms-b", nil, "b1")
	a.answer = func(string, *http.Request) (int, map[string]string, string) { return 503, nil, `{"error":"down"}` }
	e.do(e.super, "POST", "/api/admin/capabilities", map[string]any{
		"slug": "send-sms", "name": "Send SMS", "strategy": "priority",
		"params": []map[string]any{{"name": "to", "type": "string", "required": true}},
		"routes": []map[string]any{
			{"service": "sms-a", "method": "POST", "path": "/send", "body": map[string]any{"to": "{{to}}"}},
			{"service": "sms-b", "method": "POST", "path": "/messages", "body": map[string]any{"number": "{{to}}"}},
		},
	}, 201, nil)
	_, tok := e.app(map[string]any{"name": "app", "allowed_services": []string{"send-sms"}})
	if r, _ := call(t, e, "GET", "/proxy/send-sms?to=1", tok); r.StatusCode != 503 || b.total.Load() != 0 {
		t.Fatalf("a POST route that failed with a 5xx must not fall back: %d b=%d", r.StatusCode, b.total.Load())
	}
	r, _ := call(t, e, "GET", "/proxy/send-sms?to=1", tok, "Idempotency-Key", "sms-1")
	if r.StatusCode != 200 || b.total.Load() != 1 || r.Header.Get("X-Acnologia-Service") != "sms-b" {
		t.Fatalf("with an Idempotency-Key it falls back: %d b=%d", r.StatusCode, b.total.Load())
	}
	a.answer = func(string, *http.Request) (int, map[string]string, string) { return 429, nil, `{"error":"slow"}` }
	if r, _ := call(t, e, "GET", "/proxy/send-sms?to=2", tok); r.StatusCode != 200 || b.total.Load() != 2 {
		t.Fatalf("a refusal falls back for any method: %d b=%d", r.StatusCode, b.total.Load())
	}
}

func TestCheapestPutsUnpricedLast(t *testing.T) {
	cands := []routeCand{{idx: 0, unpriced: true}, {idx: 1, cost: 0.02}, {idx: 2, unpriced: true}, {idx: 3, cost: 0.005}, {idx: 4, cost: 0}}
	o := orderRoutes("t", StrategyCheapest, cands)
	got := []int{}
	for _, c := range o {
		got = append(got, c.idx)
	}
	if fmt.Sprint(got) != "[4 3 1 0 2]" {
		t.Fatalf("known prices first (a known 0 is free), unknown after them in order: %v", got)
	}

	e, s := newFeatureEnv(t)
	e.upstream("unpriced", nil, "u1")
	e.upstream("priced", map[string]any{"cost_per_request": 0.01}, "p1")
	ctx := context.Background()
	u, _ := s.Store.GetServiceBySlug(ctx, "unpriced")
	p, _ := s.Store.GetServiceBySlug(ctx, "priced")
	if out := s.orderGroup(ctx, "g", StrategyCheapest, []*store.Service{u, p}, "GET", "x"); out[0].Slug != "priced" {
		t.Fatalf("a service with no price must not pass as free: %s first", out[0].Slug)
	}
}

func TestIdempotencyRules(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("pay", nil, "k1")
	var status atomic.Int32
	status.Store(429)
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		return int(status.Load()), nil, fmt.Sprintf(`{"status":%d}`, status.Load())
	}
	appID, tok := e.app(map[string]any{"name": "x"})

	// A 429 (and any 5xx, 408 or 425) is not stored: the retry reaches the provider.
	callBody(t, e, "POST", "/proxy/pay/charge", tok, `{"amount":1}`, "Idempotency-Key", "c1")
	e.do(e.super, "POST", "/api/admin/keys/1/reset", nil, 200, nil) // the 429 cooled the key
	status.Store(200)
	r, body := callBody(t, e, "POST", "/proxy/pay/charge", tok, `{"amount":1}`, "Idempotency-Key", "c1")
	if r.StatusCode != 200 || r.Header.Get("Idempotent-Replayed") != "" || up.total.Load() != 2 {
		t.Fatalf("a 429 must not be replayed: %d %s total=%d", r.StatusCode, body, up.total.Load())
	}
	// Now a 200 is stored and replayed, but only for the same body.
	r, _ = callBody(t, e, "POST", "/proxy/pay/charge", tok, `{"amount":1}`, "Idempotency-Key", "c1")
	if r.Header.Get("Idempotent-Replayed") != "true" || up.total.Load() != 2 {
		t.Fatalf("same key, same body replays: total=%d", up.total.Load())
	}
	r, body = callBody(t, e, "POST", "/proxy/pay/charge", tok, `{"amount":2}`, "Idempotency-Key", "c1")
	if r.StatusCode != 422 || !strings.Contains(body, "idempotency_key_reused") || up.total.Load() != 2 {
		t.Fatalf("same key, another body: %d %s", r.StatusCode, body)
	}
	// Another token of the same app has keys of its own.
	other := e.newAppToken(appID, map[string]any{"name": "second"}).Token
	if r, _ := callBody(t, e, "POST", "/proxy/pay/charge", other, `{"amount":1}`, "Idempotency-Key", "c1"); r.Header.Get("Idempotent-Replayed") != "" || up.total.Load() != 3 {
		t.Fatalf("idempotency keys are per token: total=%d", up.total.Load())
	}
}

func TestIdempotencyInFlightGets409(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("slow", nil, "k1", "k2")
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		started <- struct{}{}
		<-block
		return 200, nil, `{"ok":true}`
	}
	_, tok := e.app(map[string]any{"name": "x"})
	done := make(chan int, 1)
	go func() {
		r, _ := call(t, e, "POST", "/proxy/slow/send", tok, "Idempotency-Key", "once")
		done <- r.StatusCode
	}()
	<-started
	r, body := call(t, e, "POST", "/proxy/slow/send", tok, "Idempotency-Key", "once")
	close(block)
	if r.StatusCode != 409 || !strings.Contains(body, "idempotency_in_progress") || r.Header.Get("Retry-After") != "1" {
		t.Fatalf("a second request while the first runs: %d %s", r.StatusCode, body)
	}
	if c := <-done; c != 200 || up.total.Load() != 1 {
		t.Fatalf("first: %d, upstream calls %d", c, up.total.Load())
	}
	if r, _ := call(t, e, "POST", "/proxy/slow/send", tok, "Idempotency-Key", "once"); r.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("after the first finished, the key replays")
	}
}

func TestIdempotentReplayBeforeLimits(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("api", map[string]any{"cost_per_request": 0.01}, "k1")
	appID, tok := e.app(map[string]any{"name": "x", "limits": map[string]any{"daily_requests": 1}})
	call(t, e, "POST", "/proxy/api/send", tok, "Idempotency-Key", "k")
	r, body := call(t, e, "POST", "/proxy/api/send", tok, "Idempotency-Key", "k")
	if r.StatusCode != 200 || r.Header.Get("Idempotent-Replayed") != "true" || up.total.Load() != 1 {
		t.Fatalf("an app at its limit still gets its paid answer back: %d %s", r.StatusCode, body)
	}
	var usage struct {
		Usage store.AppUsage `json:"usage"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/usage", appID), nil, 200, &usage)
	if usage.Usage.TodayRequests != 1 || math.Abs(usage.Usage.TodayCost-0.01) > 1e-9 {
		t.Fatalf("a replay records no usage: %+v", usage.Usage)
	}
}

// Retries with one Idempotency-Key reach the same key (vendor account).
func TestIdempotencyKeyPinsTheKey(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("vend", nil, "k1", "k2", "k3")
	// A 500 is not stored, so every retry goes upstream.
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 500, nil, `{"error":"x"}` }
	_, tok := e.app(map[string]any{"name": "x"})
	for i := range 4 {
		up.mu.Lock()
		up.hits = map[string]int{}
		up.mu.Unlock()
		for range 3 {
			call(t, e, "POST", "/proxy/vend/send", tok, "Idempotency-Key", fmt.Sprint("pin-", i))
		}
		up.mu.Lock()
		n := len(up.hits)
		up.mu.Unlock()
		if n != 1 {
			t.Fatalf("pin-%d: retries spread over %d keys: %v", i, n, up.hits)
		}
	}
}

func TestTokenSpendCountsTowardBudgets(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up := &scripted{hits: map[string]int{}}
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		return 200, nil, `{"model":"big-2026-01-01","usage":{"input_tokens":100,"output_tokens":50}}`
	}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	spec := map[string]any{
		"base_url": ts.URL, "auth": map[string]any{"type": "header", "name": "X-Key"}, "use_standard_rules": true,
		"offers": []map[string]any{
			{"name": "Big", "match": map[string]any{"method": "POST", "path": "/v1/chat"}, "model": "big",
				"price": map[string]any{"amount": 0.001, "output_amount": 0.002, "unit": "token"}},
		},
		"tests": []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}},
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "llmx", "name": "LLMX", "category": "LLM", "spec": spec}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "llmx", "slug": "llmx", "base_url": ts.URL, "category": "LLM"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "k"}}}, 201, nil)
	appID, tok := e.app(map[string]any{"name": "x", "limits": map[string]any{"daily_budget": 0.3}})
	for i := range 2 {
		if r, body := callBody(t, e, "POST", "/proxy/llmx/v1/chat", tok, `{"model":"big"}`); r.StatusCode != 200 {
			t.Fatalf("call %d: %d %s", i, r.StatusCode, body)
		}
	}
	var usage struct {
		Usage store.AppUsage `json:"usage"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/usage", appID), nil, 200, &usage)
	// 100 input tokens at 0.001 and 50 output at 0.002 is 0.2 a call.
	if math.Abs(usage.Usage.TodayCost-0.4) > 1e-9 {
		t.Fatalf("token spend should be recorded as cost: %+v", usage.Usage)
	}
	if r, body := callBody(t, e, "POST", "/proxy/llmx/v1/chat", tok, `{"model":"big"}`); r.StatusCode != 429 || !strings.Contains(body, "daily_budget") {
		t.Fatalf("the budget is spent, so the next call is refused: %d %s", r.StatusCode, body)
	}
}

func TestAdmissionCountsTheRequestAndFailsClosed(t *testing.T) {
	e, s := newFeatureEnv(t)
	up, _ := e.upstream("api", map[string]any{"cost_per_request": 0.02}, "k1")
	appID, tok := e.app(map[string]any{"name": "x", "limits": map[string]any{"daily_budget": 0.05}})
	codes := []int{}
	for range 3 {
		r, _ := call(t, e, "GET", "/proxy/api/x", tok)
		codes = append(codes, r.StatusCode)
	}
	if fmt.Sprint(codes) != "[200 200 429]" || up.total.Load() != 2 {
		t.Fatalf("the third call would spend 0.06 of 0.05: %v", codes)
	}

	a, err := s.Store.GetApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the store cannot be read with this context
	if hit, _ := s.admitApp(ctx, a, 0); hit == nil || hit.Limit != "limits_unavailable" {
		t.Fatalf("unreadable limits must fail closed: %+v", hit)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/proxy/api/x", nil).WithContext(ctx)
	if _, limited := s.appLimited(w, req, a, 0); !limited || w.Code != 503 || w.Header().Get("Retry-After") == "" ||
		!strings.Contains(w.Body.String(), "limits_unavailable") {
		t.Fatalf("appLimited: %d %s", w.Code, w.Body.String())
	}
}

// A key whose own egress proxy cannot connect steps aside; the next key
// serves and the service's breaker hears nothing.
func TestKeyEgressFailureMovesToNextKey(t *testing.T) {
	e, s := newFeatureEnv(t)
	up, _ := e.upstream("api", map[string]any{"strategy": "fill_first"}, "via-proxy", "direct")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	deadProxy := "http://" + ln.Addr().String()
	ln.Close()
	var p store.EgressProxy
	e.do(e.super, "POST", "/api/admin/egress", map[string]any{"name": "dead", "url": deadProxy}, 201, &p)
	e.do(e.super, "PATCH", "/api/admin/keys/1", map[string]any{"egress_id": p.ID, "priority": 1}, 200, nil)
	e.do(e.super, "PATCH", "/api/admin/keys/2", map[string]any{"priority": 2}, 200, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	for i := range 3 {
		if r, body := call(t, e, "POST", "/proxy/api/send", tok); r.StatusCode != 200 {
			t.Fatalf("call %d: %d %s", i, r.StatusCode, body)
		}
	}
	if up.hitsFor("direct") != 3 || up.hitsFor("via-proxy") != 0 {
		t.Fatalf("hits: %v", up.hits)
	}
	var st map[string]any
	e.do(e.super, "GET", "/api/admin/services/1/breaker", nil, 200, &st)
	if bs := st["status"].(map[string]any); bs["state"] != "closed" || bs["failures"].(float64) != 0 {
		t.Fatalf("the service must not be blamed for a key's proxy: %v", bs)
	}
	k, err := s.Store.GetKey(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if k.CooldownUntil == nil || !strings.Contains(k.LastError, "egress proxy failed") {
		t.Fatalf("the key should be parked briefly: %+v", k)
	}
}

func TestBufferedAnswerHasAnIdleTimeout(t *testing.T) {
	old := proxyIdleTimeout
	proxyIdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { proxyIdleTimeout = old })
	e, _ := newFeatureEnv(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"partial":`)
		http.NewResponseController(w).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "stall", "name": "stall", "base_url": ts.URL,
		"auth_type": "header", "auth_name": "X-Key"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "k"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	start := time.Now()
	r, body := call(t, e, "POST", "/proxy/stall/x", tok, "Idempotency-Key", "i1")
	if r.StatusCode != 504 || !strings.Contains(body, "upstream_idle") || time.Since(start) > 5*time.Second {
		t.Fatalf("a stalled buffered answer must be cut off: %d %s after %v", r.StatusCode, body, time.Since(start))
	}
}

func TestEventStreamIsNeverBuffered(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("sse", nil, "k")
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		return 200, map[string]string{"Content-Type": "text/event-stream"}, "data: hello\n\n"
	}
	_, tok := e.app(map[string]any{"name": "x"})
	for range 2 {
		r, body := call(t, e, "POST", "/proxy/sse/stream", tok, "Idempotency-Key", "s1")
		if r.StatusCode != 200 || body != "data: hello\n\n" || r.Header.Get("Idempotent-Replayed") != "" {
			t.Fatalf("stream: %d %q replayed=%q", r.StatusCode, body, r.Header.Get("Idempotent-Replayed"))
		}
	}
	if up.total.Load() != 2 {
		t.Fatalf("an event stream is relayed, not stored for replay: %d calls", up.total.Load())
	}
}
