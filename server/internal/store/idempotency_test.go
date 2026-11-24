package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdempotencyClaimFinishRelease(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()
	exp := now.Add(24 * time.Hour)
	claim := func(owner, fp string, at time.Time) bool {
		t.Helper()
		ok, err := s.ClaimIdempotency(ctx, "h1", 1, 2, fp, owner, at, at.Add(24*time.Hour), at.Add(-5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !claim("a", "fp", now) {
		t.Fatal("first claim")
	}
	if claim("b", "fp", now) {
		t.Fatal("a held key cannot be claimed again")
	}
	// Only the owner settles or releases it.
	if ok, _ := s.FinishIdempotency(ctx, "h1", "b", IdemDone, 200, []byte("x"), 7); ok {
		t.Fatal("a stranger settled the record")
	}
	if err := s.ReleaseIdempotency(ctx, "h1", "b"); err != nil {
		t.Fatal(err)
	}
	if r, err := s.GetIdempotency(ctx, "h1"); err != nil || r.State != IdemPending || r.Owner != "a" {
		t.Fatalf("pending record: %+v %v", r, err)
	}
	if ok, err := s.FinishIdempotency(ctx, "h1", "a", IdemDone, 201, []byte("answer"), 7); !ok || err != nil {
		t.Fatalf("finish: %v %v", ok, err)
	}
	r, _ := s.GetIdempotency(ctx, "h1")
	if r.State != IdemDone || r.Status != 201 || string(r.Payload) != "answer" || r.KeyID != 7 || r.AppID != 1 || r.TokenID != 2 {
		t.Fatalf("done record: %+v", r)
	}
	// A done record is never taken over while it lives, even when old.
	if claim("c", "fp", now.Add(time.Hour)) {
		t.Fatal("a done record was taken over")
	}
	// Expired, anyone may claim it again.
	if !claim("c", "fp", exp.Add(time.Second)) {
		t.Fatal("an expired record should be claimable")
	}

	// A pending record released by its owner is gone.
	if !claimOther(t, s, "h2", "d") {
		t.Fatal("claim h2")
	}
	if err := s.ReleaseIdempotency(ctx, "h2", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIdempotency(ctx, "h2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released record still there: %v", err)
	}
}

func claimOther(t *testing.T, s *Store, hash, owner string) bool {
	t.Helper()
	now := time.Now()
	ok, err := s.ClaimIdempotency(context.Background(), hash, 1, 2, "fp", owner, now, now.Add(24*time.Hour), now.Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// A pending record whose owner died is taken over once it is stale, but
// only by a request with the same body.
func TestIdempotencyStalePendingIsReclaimed(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	t0 := time.Now().Add(-10 * time.Minute)
	if ok, _ := s.ClaimIdempotency(ctx, "h", 1, 0, "fp", "dead", t0, t0.Add(24*time.Hour), t0.Add(-5*time.Minute)); !ok {
		t.Fatal("claim")
	}
	now := time.Now()
	if ok, _ := s.ClaimIdempotency(ctx, "h", 1, 0, "other", "x", now, now.Add(24*time.Hour), now.Add(-5*time.Minute)); ok {
		t.Fatal("another body must not take over")
	}
	if ok, _ := s.ClaimIdempotency(ctx, "h", 1, 0, "fp", "y", now, now.Add(24*time.Hour), now.Add(-20*time.Minute)); ok {
		t.Fatal("a record younger than the stale age is still owned")
	}
	if ok, _ := s.ClaimIdempotency(ctx, "h", 1, 0, "fp", "y", now, now.Add(24*time.Hour), now.Add(-5*time.Minute)); !ok {
		t.Fatal("a stale record should be taken over")
	}
	if r, _ := s.GetIdempotency(ctx, "h"); r.Owner != "y" {
		t.Fatalf("owner %q", r.Owner)
	}
}

func TestPruneDropsExpiredIdempotencyAndAffinity(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	past := time.Now().Add(-48 * time.Hour)
	if ok, _ := s.ClaimIdempotency(ctx, "old", 1, 0, "fp", "o", past, past.Add(24*time.Hour), past); !ok {
		t.Fatal("claim")
	}
	_ = claimOther(t, s, "new", "n")
	if err := s.SetKeyAffinity(ctx, 1, "r|gone", 3, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyAffinity(ctx, 1, "r|kept", 4, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.KeyAffinity(ctx, 1, "r|gone", time.Now()); ok {
		t.Fatal("an expired affinity is not returned")
	}
	if err := s.Prune(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIdempotency(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired record kept")
	}
	if _, err := s.GetIdempotency(ctx, "new"); err != nil {
		t.Fatal("live record pruned")
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM key_affinity`).Scan(&n)
	if id, _, ok, _ := s.KeyAffinity(ctx, 1, "r|kept", time.Now()); !ok || id != 4 || n != 1 {
		t.Fatalf("affinity after prune: %d %v rows=%d", id, ok, n)
	}
}
