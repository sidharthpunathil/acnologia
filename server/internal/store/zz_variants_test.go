package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// bestOf runs f three times and returns the fastest run.
func bestOf(f func()) time.Duration {
	best := time.Hour
	for i := 0; i < 3; i++ {
		st := time.Now()
		f()
		best = min(best, time.Since(st))
	}
	return best
}

// TestZZVariants times the dashboard reads and the full scans that hold the
// writer, on the seeded events.
func TestZZVariants(t *testing.T) {
	needLoad(t)
	fmt.Println("== running server: statistics sampled by PRAGMA optimize")
	timeDashboard(t, openAt(t, freshDir(t)))

	// An install from before the late indexes: Open builds them and runs a
	// full ANALYZE once, before serving.
	dir := freshDir(t)
	old := openAt(t, dir)
	for _, ix := range lateIndexes {
		if _, err := old.DB.Exec(`DROP INDEX IF EXISTS ` + ix.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.DB.Exec(`DROP TABLE IF EXISTS sqlite_stat1`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	st := time.Now()
	s := openAt(t, dir)
	fmt.Printf("== upgraded install: Open built the indexes and analyzed in %v\n", time.Since(st).Round(time.Millisecond))
	timeDashboard(t, s)
}

func timeDashboard(t *testing.T, s *Store) {
	ctx := context.Background()
	d24 := time.Now().Add(-24 * time.Hour)
	d30 := time.Now().AddDate(0, 0, -30)
	d14 := time.Now().AddDate(0, 0, -14)
	cases := []struct {
		name string
		f    func()
	}{
		{"FillStats", func() {
			svcs, _ := s.ListServices(ctx)
			_ = s.FillStats(ctx, svcs, false, nil)
		}},
		{"CountEvents24h", func() { _, _ = s.CountEvents(ctx, d24, false, "lease", "proxy") }},
		{"SpendSince24h", func() { _, _ = s.SpendSince(ctx, d24, false) }},
		{"SpendSince30d", func() { _, _ = s.SpendSince(ctx, d30, false) }},
		{"UsageReport30d", func() { _, _ = s.UsageReport(ctx, time.Now(), 30, false) }},
		{"KeyDailyStats14d", func() { _, _ = s.KeyDailyStats(ctx, 25, 14) }},
		{"ShareServiceCounts14d", func() { _, _ = s.ShareServiceCounts(ctx, 4, d14) }},
		{"ListEvents.service", func() { _, _ = s.ListEvents(ctx, EventFilter{ServiceID: 3, Limit: 100}) }},
		{"ListEvents.key", func() { _, _ = s.ListEvents(ctx, EventFilter{KeyID: 25, Limit: 50}) }},
		{"ListEvents.kindRare", func() { _, _ = s.ListEvents(ctx, EventFilter{Kind: "banned", Limit: 100}) }},
		{"CreateKeys.maxKeyID", func() { _ = createKeysHighWater(ctx, s) }},
		{"Trigger.deleteKeyEvents", func() {
			var n int
			_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE key_id=?`, 25).Scan(&n)
		}},
	}
	for _, c := range cases {
		fmt.Printf("   %-26s %10v\n", c.name, bestOf(c.f).Round(10*time.Microsecond))
	}
	var sz int64
	_ = s.DB.QueryRow(`SELECT page_count*page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&sz)
	fmt.Printf("   db size %d MB\n", sz>>20)
}
