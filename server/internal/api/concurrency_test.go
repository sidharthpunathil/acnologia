package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cogito-keys/cogito/internal/store"
)

// slowTTS behaves like ElevenLabs: it blocks until released and fails with
// too_many_concurrent_requests if one key is used more than `limit` times at once.
type slowTTS struct {
	mu       sync.Mutex
	inflight map[string]int
	peak     map[string]int
	limit    int
	gate     chan struct{}
	over     atomic.Int32
}

func (s *slowTTS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k := r.Header.Get("xi-api-key")
	if r.URL.Path == "/v1/user/subscription" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"character_count":0,"character_limit":10000}`))
		return
	}
	s.mu.Lock()
	s.inflight[k]++
	if s.inflight[k] > s.peak[k] {
		s.peak[k] = s.inflight[k]
	}
	n := s.inflight[k]
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.inflight[k]--; s.mu.Unlock() }()
	if n > s.limit {
		s.over.Add(1)
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"detail":{"status":"too_many_concurrent_requests","message":"Too many concurrent requests"}}`))
		return
	}
	<-s.gate
	w.Header().Set("Content-Type", "audio/mpeg")
	_, _ = w.Write([]byte("ID3audio"))
}

func newTTSEnv(t *testing.T, limit, queueSec int) (*env, *slowTTS, string) {
	e := newEnv(t)
	tts := &slowTTS{inflight: map[string]int{}, peak: map[string]int{}, limit: limit, gate: make(chan struct{})}
	up := httptest.NewServer(tts)
	t.Cleanup(up.Close)
	t.Cleanup(func() { defer func() { _ = recover() }(); close(tts.gate) })
	var svc store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "elevenlabs", "slug": "eleven", "base_url": up.URL,
		"max_concurrency": limit, "queue_timeout_sec": queueSec}, 201, &svc)
	if svc.MaxConcurrency != limit {
		t.Fatalf("max_concurrency not saved: %d", svc.MaxConcurrency)
	}
	var out struct{ Keys []store.Key }
	e.do(e.super, "POST", "/api/admin/services/2/keys", map[string]any{"keys": []map[string]any{{"secret": "el_one"}, {"secret": "el_two"}}}, 201, &out)
	_, tok := e.app(map[string]any{"name": "tts"})
	return e, tts, tok
}

func ttsCall(e *env, tok string) (int, string) {
	req, _ := http.NewRequest("POST", e.srv.URL+"/proxy/eleven/v1/text-to-speech/v", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("X-Cogito-Key-Id")
}

func waitInFlight(t *testing.T, e *env, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var sv store.Service
		e.do(e.super, "GET", "/api/admin/services/2", nil, 200, &sv)
		if sv.InFlight == n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("in_flight never reached %d", n)
}

func TestProxyNeverExceedsKeyConcurrency(t *testing.T) {
	e, tts, tok := newTTSEnv(t, 1, 0)
	codes := make(chan int, 3)
	for i := 0; i < 2; i++ {
		go func() { c, _ := ttsCall(e, tok); codes <- c }()
	}
	waitInFlight(t, e, 2) // both keys busy, one request each
	c, _ := ttsCall(e, tok)
	if c != 503 {
		t.Fatalf("third request with no queue: want 503 all_keys_busy, got %d", c)
	}
	close(tts.gate)
	for i := 0; i < 2; i++ {
		if c := <-codes; c != 200 {
			t.Fatalf("got %d", c)
		}
	}
	if tts.over.Load() != 0 || tts.peak["el_one"] > 1 || tts.peak["el_two"] > 1 {
		t.Fatalf("upstream saw too many concurrent requests: %v over=%d", tts.peak, tts.over.Load())
	}
	waitInFlight(t, e, 0)
}

func TestProxyQueuesForFreeKey(t *testing.T) {
	e, tts, tok := newTTSEnv(t, 1, 5)
	codes := make(chan int, 5)
	for i := 0; i < 5; i++ {
		go func() { c, _ := ttsCall(e, tok); codes <- c }()
	}
	waitInFlight(t, e, 2)
	// Let requests through one at a time; queued ones pick up freed keys.
	go func() {
		for i := 0; i < 5; i++ {
			tts.gate <- struct{}{}
		}
	}()
	for i := 0; i < 5; i++ {
		if c := <-codes; c != 200 {
			t.Fatalf("queued request got %d", c)
		}
	}
	if tts.over.Load() != 0 {
		t.Fatalf("upstream concurrency exceeded %d times", tts.over.Load())
	}
}

func TestLeaseHoldsSlotUntilReported(t *testing.T) {
	e, _, tok := newTTSEnv(t, 1, 0)
	auth := []string{"Authorization", "Bearer " + tok}
	var a, b leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 200, &a, auth...)
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 200, &b, auth...)
	if a.KeyID == b.KeyID {
		t.Fatal("two leases share a key with concurrency 1")
	}
	var busy struct {
		Error struct{ Code string } `json:"error"`
	}
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 503, &busy, auth...)
	if busy.Error.Code != "all_keys_busy" {
		t.Fatalf("code = %q", busy.Error.Code)
	}
	// A waiting lease gets the key as soon as another is reported.
	got := make(chan leaseDTO, 1)
	go func() {
		var c leaseDTO
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven", "wait_sec": 5}, 200, &c, auth...)
		got <- c
	}()
	time.Sleep(150 * time.Millisecond)
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+a.LeaseID+"/report", map[string]any{"outcome": "ok"}, 200, nil, auth...)
	select {
	case c := <-got:
		if c.KeyID != a.KeyID {
			t.Fatalf("waiter got key %d, want freed key %d", c.KeyID, a.KeyID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiting lease never got a key")
	}
}

func TestExpiredLeaseFreesSlot(t *testing.T) {
	e, _, tok := newTTSEnv(t, 1, 0)
	auth := []string{"Authorization", "Bearer " + tok}
	for i := 0; i < 2; i++ {
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven", "ttl_sec": 1}, 200, nil, auth...)
	}
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 503, nil, auth...)
	time.Sleep(1100 * time.Millisecond)
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 200, nil, auth...)
}

func TestPerKeyOverrideAndRestore(t *testing.T) {
	e, _, tok := newTTSEnv(t, 1, 0)
	auth := []string{"Authorization", "Bearer " + tok}
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/2/keys", nil, 200, &keys)
	id := itoa(int(keys.Keys[0].ID))
	var k store.Key
	e.do(e.super, "PATCH", "/api/admin/keys/"+id, map[string]any{"max_concurrency": 3, "status": "active"}, 200, &k)
	e.do(e.super, "PATCH", "/api/admin/keys/"+itoa(int(keys.Keys[1].ID)), map[string]any{"status": "disabled"}, 200, nil)
	if k.MaxConcurrency == nil || *k.MaxConcurrency != 3 {
		t.Fatalf("override not saved: %+v", k.MaxConcurrency)
	}
	for i := 0; i < 3; i++ {
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 200, nil, auth...)
	}
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "eleven"}, 503, nil, auth...)
	e.do(e.super, "GET", "/api/admin/services/2/keys", nil, 200, &keys)
	for _, kk := range keys.Keys {
		if kk.ID == k.ID && kk.InFlight != 3 {
			t.Fatalf("in_flight = %d", kk.InFlight)
		}
	}
	e.do(e.super, "PATCH", "/api/admin/keys/"+id, map[string]any{"max_concurrency": -1}, 200, &k)
	if k.MaxConcurrency != nil {
		t.Fatal("override not cleared")
	}
}
