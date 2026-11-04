package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestFailingHookDoesNotDelayOthers: one webhook answers 500 and sits in its
// retry wait. Another webhook still gets every event right away, and Run
// returns promptly once ctx ends, even mid retry wait.
func TestFailingHookDoesNotDelayOthers(t *testing.T) {
	n, st, box := setup(t)
	// The retry wait lasts until ctx ends, standing in for the real 2s and 10s.
	n.sleep = func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	bad := &sink{status: 500}
	s1 := httptest.NewServer(http.HandlerFunc(bad.handler))
	defer s1.Close()
	var mu sync.Mutex
	var order []string
	got := make(chan struct{}, 10)
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, r.Header.Get("X-Acnologia-Event"))
		mu.Unlock()
		got <- struct{}{}
	}))
	defer s2.Close()
	addHook(t, st, box, s1.URL, "json", nil)
	addHook(t, st, box, s2.URL, "json", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	n.Emit(Event{Type: KeyBanned, KeyID: 1, Message: "key 1 banned"})
	n.Emit(Event{Type: KeyInvalid, KeyID: 2, Message: "key 2 invalid"})
	for i := 0; i < 2; i++ {
		select {
		case <-got:
		case <-time.After(time.Second):
			t.Fatalf("healthy hook got %d of 2 events while the other hook was retrying", i)
		}
	}
	mu.Lock()
	if len(order) != 2 || order[0] != KeyBanned || order[1] != KeyInvalid {
		t.Fatalf("healthy hook saw %v, want events in order", order)
	}
	mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel while a retry wait was running")
	}
}

func TestSleepCtxStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	at := time.Now()
	if err := sleepCtx(ctx, time.Minute); err == nil {
		t.Fatal("sleepCtx returned nil after cancel")
	}
	if time.Since(at) > time.Second {
		t.Fatal("sleepCtx ignored the cancel")
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleepCtx: %v", err)
	}
}

// TestDeletedHookWorkerStops: when a webhook is deleted, its worker stops on
// the next event, even in the middle of a retry wait, and gets no more events.
func TestDeletedHookWorkerStops(t *testing.T) {
	n, st, box := setup(t)
	waiting := make(chan struct{}, 10)
	stopped := make(chan struct{}, 10)
	n.sleep = func(ctx context.Context, _ time.Duration) error {
		waiting <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	}
	bad := &sink{status: 500}
	s1 := httptest.NewServer(http.HandlerFunc(bad.handler))
	defer s1.Close()
	good := make(chan struct{}, 10)
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { good <- struct{}{} }))
	defer s2.Close()
	h1 := addHook(t, st, box, s1.URL, "json", nil)
	addHook(t, st, box, s2.URL, "json", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	n.Emit(Event{Type: KeyBanned, KeyID: 1, Message: "key 1 banned"})
	wait := func(c chan struct{}, what string) {
		t.Helper()
		select {
		case <-c:
		case <-time.After(time.Second):
			t.Fatal(what)
		}
	}
	wait(waiting, "the failing hook never reached its retry wait")
	wait(good, "the healthy hook did not get the first event")

	if err := st.DeleteWebhook(context.Background(), h1.ID); err != nil {
		t.Fatal(err)
	}
	n.Invalidate()
	n.Emit(Event{Type: KeyBanned, KeyID: 2, Message: "key 2 banned"})
	wait(stopped, "the deleted hook's worker kept waiting to retry")
	wait(good, "the healthy hook did not get the second event")
	bad.mu.Lock()
	got := len(bad.bodies)
	bad.mu.Unlock()
	if got != 1 {
		t.Fatalf("deleted hook got %d deliveries, want 1", got)
	}
	cancel()
	wait(done, "Run did not return after cancel")
}
