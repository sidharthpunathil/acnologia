package store

import (
	"context"
	"testing"
	"time"
)

// Prune deletes in batches until nothing old is left, across several
// batches, and keeps everything newer.
func TestPruneBatches(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	svc, err := s.CreateService(ctx, &Service{Slug: "svc", Name: "Svc", Provider: "custom", BaseURL: "http://127.0.0.1:1",
		AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.CreateKey(ctx, NewKey{ServiceID: svc.ID, Label: "k", SecretEnc: []byte("x"), SecretFP: "fp", Hint: "...", Tier: "primary", Weight: 1, Priority: 100})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateApp(ctx, &App{Name: "a", Mode: "both", Active: true}, "h")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -40).Unix()
	fresh := time.Now().Add(-time.Hour).Unix()
	oldN, freshN := 2*pruneBatch+7, 25
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < oldN+freshN; i++ {
		ts := old
		if i >= oldN {
			ts = fresh
		}
		if _, err := tx.Exec(`INSERT INTO events(ts,kind) VALUES(?, 'proxy')`, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO leases(id,key_id,app_id,service_id,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
			"l"+itoa(i), k.ID, a.ID, svc.ID, ts, ts+60); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"events", "leases"} {
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != freshN {
			t.Fatalf("%s: %d rows left, want %d", table, n, freshN)
		}
	}
	// A retention of zero keeps events forever; old leases still go.
	if _, err := s.DB.Exec(`INSERT INTO events(ts,kind) VALUES(?, 'proxy')`, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	if n != freshN+1 {
		t.Fatalf("retention 0 deleted events: %d left", n)
	}
}
