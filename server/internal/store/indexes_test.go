package store

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// seedSmall adds 20k events over 30 days for 50 keys and 10 apps, then
// gathers statistics the way Open does.
func seedSmall(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(3, 4))
	nowS := time.Now().Unix()
	for i := 0; i < 20000; i++ {
		if _, err := tx.Exec(`INSERT INTO events(ts,kind,service_id,key_id,app_id,status_code) VALUES(?,?,?,?,?,200)`,
			nowS-int64(r.IntN(30*86400)), "proxy", r.IntN(5)+1, r.IntN(50)+1, r.IntN(10)+1); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	optimize(ctx, s.DB)
}

// The per key and per app event reads use their indexes instead of scanning
// every event, and so do the full scans that used to hold the writer.
func TestEventIndexPlans(t *testing.T) {
	s := openTest(t)
	seedSmall(t, s)
	cases := []struct {
		name, q, want string
		args          []any
	}{
		{"KeyDailyStats", keyDailyStatsSQL, "events_key", []any{7, 0}},
		{"ShareServiceCounts", shareServiceCountsSQL, "events_app", []any{3, time.Now().AddDate(0, 0, -14).Unix()}},
		{"CreateKeys high water", `SELECT MAX(key_id) FROM events`, "events_key", nil},
		{"hidden key trigger", `DELETE FROM events WHERE key_id=?`, "events_key", []any{7}},
		{"lease prune", `SELECT rowid FROM leases WHERE created_at<? LIMIT 2000`, "leases_created", []any{0}},
		{"CreateKeys lease high water", keysHighWater, "leases_key", nil},
		{"key delete cascade", `DELETE FROM keys WHERE id=?`, "leases_key", []any{7}},
		{"app delete cascade", `DELETE FROM apps WHERE id=?`, "leases_app", []any{3}},
	}
	for _, c := range cases {
		plan := explain(t, s.DB, c.q, c.args...)
		if !strings.Contains(plan, c.want) {
			t.Errorf("%s: plan %q does not use %s", c.name, plan, c.want)
		}
	}
}

// Open on a database made before the late indexes adds them, and a second
// Open leaves them alone.
func TestLateIndexesMigrate(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ix := range lateIndexes {
		if _, err := s.DB.Exec(`DROP INDEX ` + ix.name); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	for i := 0; i < 2; i++ {
		s, err = Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, ix := range lateIndexes {
			var n int
			if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, ix.name).Scan(&n); err != nil || n != 1 {
				t.Fatalf("open %d: index %s missing (%v)", i, ix.name, err)
			}
		}
		s.Close()
	}
}
