package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Events are kept in the events table (for the event list and streams),
// wake waiting streams, and Done closes when Run returns. key.cooling and
// job.done reach only webhooks that list them.
func TestRecordWakeDoneAndOptIn(t *testing.T) {
	n, st, box := setup(t)
	all := &sink{}
	allSrv := httptest.NewServer(http.HandlerFunc(all.handler))
	defer allSrv.Close()
	listed := &sink{}
	listedSrv := httptest.NewServer(http.HandlerFunc(listed.handler))
	defer listedSrv.Close()
	addHook(t, st, box, allSrv.URL, "json", nil)
	addHook(t, st, box, listedSrv.URL, "json", []string{KeyCooling, PoolEmpty})

	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() { n.Run(ctx); close(ran) }()

	wake := n.Wake()
	n.Emit(Event{Type: KeyCooling, ServiceID: 3, KeyID: 4, Message: "key resting", Data: map[string]any{"until": "2030-01-01T00:00:00Z"}})
	select {
	case <-wake:
	case <-time.After(2 * time.Second):
		t.Fatal("recording an event did not wake streams")
	}
	n.Emit(Event{Type: PoolEmpty, ServiceID: 3, Message: "no key"})
	n.Emit(Event{Type: JobDone, Message: "job 1 done", Data: map[string]any{"job_id": 1}})
	n.Emit(Event{Type: JobDone, Message: "job 2 done", Data: map[string]any{"job_id": 2}})

	deadline := time.Now().Add(3 * time.Second)
	for {
		all.mu.Lock()
		a := len(all.bodies)
		all.mu.Unlock()
		listed.mu.Lock()
		l := len(listed.bodies)
		listed.mu.Unlock()
		if (a >= 1 && l >= 2) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if len(all.bodies) != 1 || len(listed.bodies) != 2 {
		t.Fatalf("an every-event hook got %d (want pool.empty only), the listing hook %d (want 2)", len(all.bodies), len(listed.bodies))
	}

	evs, err := st.ListEvents(context.Background(), store.EventFilter{AfterID: -1})
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	// Both job.done events are kept: jobs are never dropped as repeats.
	if len(evs) != 4 || evs[0].Kind != KeyCooling || string(evs[0].Data) != `{"until":"2030-01-01T00:00:00Z"}` || evs[0].Detail != "key resting" {
		t.Fatalf("recorded %v, first %+v %s", kinds, evs[0], evs[0].Data)
	}

	cancel()
	<-ran
	select {
	case <-n.Done():
	default:
		t.Fatal("Done is open after Run returned")
	}
}
