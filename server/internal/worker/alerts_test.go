package worker

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// alertEnv is a worker whose notifier records events, with repeats allowed
// so the worker's own dedupe is what the tests see.
func alertEnv(t *testing.T) (*Worker, *store.Store) {
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
	p := pool.New(st, box)
	p.Notify = notify.New(st, box, http.DefaultClient)
	p.Notify.Quiet = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Notify.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &Worker{Store: st, Pool: p}, st
}

// recorded waits for the notifier to record n events of a kind and returns
// them, failing when more arrive.
func recorded(t *testing.T, st *store.Store, kind string, n int) []*store.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		evs, err := st.ListEvents(context.Background(), store.EventFilter{Kind: kind, AfterID: -1})
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) >= n || time.Now().After(deadline) {
			// Give a stray duplicate a moment to show up.
			time.Sleep(100 * time.Millisecond)
			evs, _ = st.ListEvents(context.Background(), store.EventFilter{Kind: kind, AfterID: -1})
			if len(evs) != n {
				t.Fatalf("%s: want %d events, got %d", kind, n, len(evs))
			}
			return evs
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func addKey(t *testing.T, st *store.Store, hidden bool) (*store.Service, *store.Key) {
	t.Helper()
	ctx := context.Background()
	sv, err := st.CreateService(ctx, &store.Service{Slug: "svc", Name: "Svc", Provider: "generic", BaseURL: "https://x.example",
		AuthType: "header", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3})
	if err != nil {
		sv, err = st.GetServiceBySlug(ctx, "svc")
		if err != nil {
			t.Fatal(err)
		}
	}
	fp := "fp-visible"
	if hidden {
		fp = "fp-hidden"
	}
	keys, _, err := st.CreateKeys(ctx, []store.NewKey{{ServiceID: sv.ID, Label: "main " + fp, SecretEnc: []byte("x"), SecretFP: fp, Hint: "sk-...1234", Hidden: hidden}})
	if err != nil {
		t.Fatal(err)
	}
	return sv, keys[0]
}

func TestKeyCoolingOncePerRest(t *testing.T) {
	w, st := alertEnv(t)
	ctx := context.Background()
	_, k := addKey(t, st, false)
	_, hk := addKey(t, st, true)
	now := time.Now()
	rest := func(id int64, until time.Time) {
		if _, err := st.DB.Exec(`UPDATE keys SET cooldown_until=? WHERE id=?`, until.Unix(), id); err != nil {
			t.Fatal(err)
		}
	}
	rest(k.ID, now.Add(time.Minute))
	rest(hk.ID, now.Add(time.Minute))
	w.CheckCooling(ctx, now)
	w.CheckCooling(ctx, now.Add(time.Second))
	// The rest gets longer: still the same rest.
	rest(k.ID, now.Add(2*time.Minute))
	w.CheckCooling(ctx, now.Add(2*time.Second))
	evs := recorded(t, st, notify.KeyCooling, 1)
	if evs[0].KeyID == nil || *evs[0].KeyID != k.ID || len(evs[0].Data) == 0 {
		t.Fatalf("cooling event: %+v", evs[0])
	}
	// The rest ends; a new one is a new event.
	w.CheckCooling(ctx, now.Add(3*time.Minute))
	rest(k.ID, now.Add(5*time.Minute))
	w.CheckCooling(ctx, now.Add(3*time.Minute+time.Second))
	recorded(t, st, notify.KeyCooling, 2)
}

func TestShareExpiringWarnsOncePerThreshold(t *testing.T) {
	w, st := alertEnv(t)
	ctx := context.Background()
	sh, err := st.CreateShare(ctx, &store.Share{ID: "shr_partner", Name: "partner"}, "hash", 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ends := now.Add(20 * time.Hour)
	if _, err := st.DB.Exec(`UPDATE shares SET status='active', expires_at=? WHERE id=?`, ends.Unix(), sh.ID); err != nil {
		t.Fatal(err)
	}
	w.CheckShareExpiry(ctx, now)
	w.CheckShareExpiry(ctx, now.Add(time.Minute))
	evs := recorded(t, st, notify.ShareExpiring, 1)
	if want := `"within":"24h"`; !contains(string(evs[0].Data), want) {
		t.Fatalf("first warning: %s", evs[0].Data)
	}
	// Within the last hour: the 1 hour warning, once.
	w.CheckShareExpiry(ctx, ends.Add(-30*time.Minute))
	w.CheckShareExpiry(ctx, ends.Add(-20*time.Minute))
	evs = recorded(t, st, notify.ShareExpiring, 2)
	if want := `"within":"1h"`; !contains(string(evs[1].Data), want) {
		t.Fatalf("second warning: %s", evs[1].Data)
	}
	// A share made with under an hour left gets only the 1 hour warning,
	// and the 24 hour one never follows.
	sh2, err := st.CreateShare(ctx, &store.Share{ID: "shr_short", Name: "short"}, "hash2", 0)
	if err != nil {
		t.Fatal(err)
	}
	ends2 := now.Add(30 * time.Minute)
	_, _ = st.DB.Exec(`UPDATE shares SET status='active', expires_at=? WHERE id=?`, ends2.Unix(), sh2.ID)
	w.CheckShareExpiry(ctx, now)
	w.CheckShareExpiry(ctx, now.Add(time.Minute))
	recorded(t, st, notify.ShareExpiring, 3)
}

func TestAppLimitNearOncePerPeriod(t *testing.T) {
	w, st := alertEnv(t)
	ctx := context.Background()
	a, err := st.CreateApp(ctx, &store.App{Name: "bot", Mode: "both", Active: true, TokenPrefix: "acn_test"}, "apphash")
	if err != nil {
		t.Fatal(err)
	}
	ten := int64(10)
	if err := st.SetAppLimits(ctx, a.ID, store.AppLimits{DailyRequests: &ten}); err != nil {
		t.Fatal(err)
	}
	for range 7 {
		_ = st.RecordAppUsage(ctx, a.ID, 0, false)
	}
	now := time.Now()
	w.CheckAppLimits(ctx, now)
	recorded(t, st, notify.AppLimitNear, 0)
	_ = st.RecordAppUsage(ctx, a.ID, 0, false)
	w.CheckAppLimits(ctx, now)
	w.CheckAppLimits(ctx, now)
	evs := recorded(t, st, notify.AppLimitNear, 1)
	if evs[0].AppID == nil || *evs[0].AppID != a.ID || !contains(string(evs[0].Data), `"limit":"daily_requests"`) {
		t.Fatalf("limit near event: %+v %s", evs[0], evs[0].Data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
