package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/store"
)

func setup(t *testing.T) (*Notifier, *store.Store, *crypto.Box) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := crypto.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	n := New(st, box, http.DefaultClient)
	n.sleep = func(time.Duration) {}
	return n, st, box
}

type sink struct {
	mu     sync.Mutex
	bodies [][]byte
	heads  []http.Header
	status int
}

func (s *sink) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, b)
	s.heads = append(s.heads, r.Header.Clone())
	code := s.status
	s.mu.Unlock()
	if code == 0 {
		code = 200
	}
	w.WriteHeader(code)
}

func addHook(t *testing.T, st *store.Store, box *crypto.Box, url, format string, events []string) *store.Webhook {
	t.Helper()
	ue, _ := box.Encrypt(url)
	se, _ := box.Encrypt("whsec_test")
	h, err := st.CreateWebhook(context.Background(), &store.Webhook{Name: "t", URLEnc: ue, Format: format, Events: events, SecretEnc: se, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestSignedJSONDelivery(t *testing.T) {
	n, st, box := setup(t)
	s := &sink{}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	defer srv.Close()
	h := addHook(t, st, box, srv.URL, "json", nil)
	e := Event{Type: KeyInvalid, Message: "key acct-1 was rejected", ServiceID: 1, KeyID: 2}
	if _, err := n.Deliver(context.Background(), h, e); err != nil {
		t.Fatal(err)
	}
	head := s.heads[0]
	want := "sha256=" + Sign("whsec_test", head.Get("X-Cogito-Timestamp"), s.bodies[0])
	if head.Get("X-Cogito-Signature") != want {
		t.Fatalf("bad signature %q, want %q", head.Get("X-Cogito-Signature"), want)
	}
	var got Event
	_ = json.Unmarshal(s.bodies[0], &got)
	if got.Type != KeyInvalid || got.KeyID != 2 {
		t.Fatalf("payload wrong: %s", s.bodies[0])
	}
}

func TestChatFormats(t *testing.T) {
	e := Event{Type: PoolEmpty, Message: "SerpAPI has no usable key"}
	for format, field := range map[string]string{"slack": "text", "discord": "content", "telegram": "text"} {
		b, err := Payload(format, e)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m[field] != "Acnologia: SerpAPI has no usable key" {
			t.Fatalf("%s payload wrong: %s", format, b)
		}
	}
	if _, err := Payload("carrier-pigeon", e); err == nil {
		t.Fatal("unknown formats must fail")
	}
}

func TestQuietWindowAndRecoveryClears(t *testing.T) {
	n, _, _ := setup(t)
	now := time.Unix(1000, 0)
	n.now = func() time.Time { return now }
	e := Event{Type: KeyExhausted, KeyID: 5, Message: "x"}
	n.Emit(e)
	n.Emit(e)
	if len(n.queue) != 1 {
		t.Fatalf("repeat inside the quiet window should be dropped, queue=%d", len(n.queue))
	}
	n.Emit(Event{Type: KeyRecovered, KeyID: 5, Message: "back"})
	n.Emit(e)
	if len(n.queue) != 3 {
		t.Fatalf("a recovery should clear the quiet window, queue=%d", len(n.queue))
	}
	now = now.Add(11 * time.Minute)
	n.Emit(Event{Type: KeyRecovered, KeyID: 5, Message: "back"})
	if len(n.queue) != 4 {
		t.Fatal("events after the quiet window are sent")
	}
}

func TestRunFiltersSubscriptionsAndRecords(t *testing.T) {
	n, st, box := setup(t)
	all, onlyDown := &sink{}, &sink{}
	s1 := httptest.NewServer(http.HandlerFunc(all.handler))
	defer s1.Close()
	s2 := httptest.NewServer(http.HandlerFunc(onlyDown.handler))
	defer s2.Close()
	addHook(t, st, box, s1.URL, "slack", nil)
	h2 := addHook(t, st, box, s2.URL, "json", []string{ServiceDown})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	n.Emit(Event{Type: PoolEmpty, ServiceID: 1, Message: "empty"})
	n.Emit(Event{Type: ServiceDown, ServiceID: 1, Message: "down"})
	deadline := time.Now().Add(3 * time.Second)
	for {
		all.mu.Lock()
		a := len(all.bodies)
		all.mu.Unlock()
		onlyDown.mu.Lock()
		d := len(onlyDown.bodies)
		onlyDown.mu.Unlock()
		if a == 2 && d == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries: all=%d onlyDown=%d", a, d)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	got, _ := st.GetWebhook(context.Background(), h2.ID)
	if got.LastStatus != 200 || got.LastSentAt == nil {
		t.Fatalf("delivery not recorded: %+v", got)
	}
}

func TestRetriesServerErrorsButNotRejections(t *testing.T) {
	n, st, box := setup(t)
	s := &sink{status: 500}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	defer srv.Close()
	h := addHook(t, st, box, srv.URL, "json", nil)
	n.deliverWithRetry(context.Background(), h, Event{Type: Test, Message: "x"})
	if len(s.bodies) != 3 {
		t.Fatalf("5xx should be retried 3 times, got %d", len(s.bodies))
	}
	s.bodies, s.status = nil, 404
	n.deliverWithRetry(context.Background(), h, Event{Type: Test, Message: "x"})
	if len(s.bodies) != 1 {
		t.Fatalf("404 should not be retried, got %d", len(s.bodies))
	}
	got, _ := st.GetWebhook(context.Background(), h.ID)
	if got.LastStatus != 404 || got.LastError == "" {
		t.Fatalf("failure not recorded: %+v", got)
	}
}
