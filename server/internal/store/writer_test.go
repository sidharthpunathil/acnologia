package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// holdWriter takes the one writer connection, so group writes queue up
// behind it until release is called.
func holdWriter(t *testing.T, s *Store) (release func()) {
	t.Helper()
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// BEGIN is deferred; a write makes it hold the write lock too.
	if _, err := tx.Exec(`INSERT INTO meta(k,v) VALUES('hold','1') ON CONFLICT(k) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return func() { _ = tx.Rollback() }
}

// waitQueued waits until n jobs wait behind the current leader.
func waitQueued(t *testing.T, g *groupWriter, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		q, lead := len(g.queue), g.leading
		g.mu.Unlock()
		if lead && q >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue reached %d (leading %v), want %d", q, lead, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func testService(t *testing.T, s *Store) *Service {
	t.Helper()
	svc, err := s.CreateService(context.Background(), &Service{Slug: "svc", Name: "Svc", Provider: "custom", BaseURL: "http://127.0.0.1:1",
		AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func testKey(t *testing.T, s *Store, svc *Service, fp string, hidden bool) *Key {
	t.Helper()
	k, err := s.CreateKey(context.Background(), NewKey{ServiceID: svc.ID, Label: fp, SecretEnc: []byte("x"), SecretFP: fp, Hint: "...",
		Tier: "primary", Weight: 1, Priority: 100, Hidden: hidden})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// N concurrent usage records, some with a cancelled context as when the
// client hangs up, all count.
func TestRecordAppUsageConcurrentExact(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.CreateApp(ctx, &App{Name: "a", Mode: "both", Active: true}, "h")
	if err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	const n = 300
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := ctx
			if i%3 == 0 {
				c = gone
			}
			errs <- s.RecordAppUsage(c, a.ID, 0.001, false)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.GetAppUsage(ctx, a.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u.TodayRequests != n {
		t.Fatalf("recorded %d requests, want %d", u.TodayRequests, n)
	}
}

// A job that fails inside a group transaction rolls back only itself.
func TestGroupBatchIsolatesFailures(t *testing.T) {
	s := openTest(t)
	if err := s.SetMeta(context.Background(), "taken", "x"); err != nil {
		t.Fatal(err)
	}
	release := holdWriter(t, s)
	var wg sync.WaitGroup
	results := make([]error, 12)
	job := func(i int) {
		defer wg.Done()
		k := "k" + itoa(i)
		if i == 5 {
			k = "taken" // a plain INSERT of an existing key fails
		}
		results[i] = s.gw.write(wstmt{`INSERT INTO meta(k,v) VALUES(?, 'v')`, []any{k}})
	}
	wg.Add(1)
	go job(0) // the leader, waiting for the writer
	waitQueued(t, s.gw, 0)
	for i := 1; i < len(results); i++ {
		wg.Add(1)
		go job(i)
	}
	waitQueued(t, s.gw, len(results)-1)
	release()
	wg.Wait()
	for i, err := range results {
		var v string
		got := s.DB.QueryRow(`SELECT v FROM meta WHERE k=?`, "k"+itoa(i)).Scan(&v)
		if i == 5 {
			if err == nil {
				t.Fatal("the failing job reported no error")
			}
			continue
		}
		if err != nil || got != nil {
			t.Fatalf("job %d: err %v, row %v", i, err, got)
		}
	}
}

// Close lets every queued write commit before closing, and refuses writes
// after it.
func TestCloseDrainsQueuedWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	release := holdWriter(t, s)
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.AddEvent(ctx, NewEvent{Kind: "proxy", Detail: "x"})
		}()
	}
	waitQueued(t, s.gw, n-1)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned while writes were queued")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := s.gw.write(wstmt{`SELECT 1`, nil}); !errors.Is(err, errStoreClosed) {
		t.Fatalf("write after Close: %v", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&got); err != nil || got != n {
		t.Fatalf("events after reopen: %d %v, want %d", got, err, n)
	}
}

// A rate limit or ban is written before ApplyEffect returns, so the next
// pick, reading through the read pool, already skips the key. The OK effect
// that goes through group commit writes what the direct one did.
func TestEffectsVisibleToNextPick(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	svc := testService(t, s)
	a := testKey(t, s, svc, "a", false)
	b := testKey(t, s, svc, "b", false)

	until := time.Now().Add(time.Minute)
	fails := 1
	if err := s.ApplyEffect(ctx, a.ID, KeyEffect{CooldownUntil: &until, ConsecutiveFails: &fails}); err != nil {
		t.Fatal(err)
	}
	keys, err := s.CandidateKeys(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.ID == a.ID && k.Available(time.Now()) {
			t.Fatal("rate limited key still available to the next pick")
		}
	}
	banned := StatusBanned
	if err := s.ApplyEffect(ctx, b.ID, KeyEffect{Status: &banned}); err != nil {
		t.Fatal(err)
	}
	if hasKey(t, s, svc.ID, b.ID) {
		t.Fatal("banned key still a candidate")
	}

	// The grouped OK effect, with and without counting down the quota.
	ten := int64(10)
	if err := s.UpdateKey(ctx, a.ID, KeyPatch{QuotaLimit: func() **int64 { p := &ten; return &p }()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE keys SET quota_remaining=3, consecutive_fails=4 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := s.ApplyEffect(ctx, a.ID, KeyEffect{ConsecutiveFails: &zero, MarkOK: true, DecrementQuota: true}); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetKey(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k.ConsecutiveFails != 0 || k.QuotaRemaining == nil || *k.QuotaRemaining != 2 || k.LastOKAt == nil {
		t.Fatalf("OK effect: fails %d remaining %v ok_at %v", k.ConsecutiveFails, k.QuotaRemaining, k.LastOKAt)
	}
	if err := s.ApplyEffect(ctx, a.ID, KeyEffect{ConsecutiveFails: &zero, MarkOK: true}); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.GetKey(ctx, a.ID); k.QuotaRemaining == nil || *k.QuotaRemaining != 2 {
		t.Fatalf("OK effect without decrement changed quota: %v", k.QuotaRemaining)
	}
}

// Events of a hidden key, written through group commit, still go with the
// key, whether it is deleted itself or with its service.
func TestHiddenKeyEventsDeletedWithKey(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	svc := testService(t, s)
	hid := testKey(t, s, svc, "hidden", true)
	vis := testKey(t, s, svc, "visible", false)
	for i := 0; i < 5; i++ {
		s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: svc.ID, KeyID: hid.ID})
		s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: svc.ID, KeyID: vis.ID})
	}
	count := func(id int64) int {
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE key_id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(hid.ID) != 5 {
		t.Fatalf("hidden key events written: %d", count(hid.ID))
	}
	if err := s.DeleteKey(ctx, hid.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(hid.ID); n != 0 {
		t.Fatalf("%d events of a deleted hidden key left", n)
	}
	if n := count(vis.ID); n != 5 {
		t.Fatalf("visible key events: %d, want 5", n)
	}
	hid2 := testKey(t, s, svc, "hidden2", true)
	s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: svc.ID, KeyID: hid2.ID})
	if err := s.DeleteService(ctx, svc.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(hid2.ID); n != 0 {
		t.Fatalf("%d events of a hidden key left after its service went", n)
	}
}
