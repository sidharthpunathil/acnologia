package store

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// seedUsage adds 3 services with 4 keys each (one hidden per service), 6
// apps and 20k events over 40 days of every kind, some with no key or app.
func seedUsage(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	for sv := 1; sv <= 3; sv++ {
		exec(`INSERT INTO services(id,slug,name,provider,base_url,auth_type,auth_name,strategy,cooldown_sec,max_attempts,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			sv, "svc"+itoa(sv), "Service "+itoa(sv), "custom", "https://x", "bearer", "Authorization", "round_robin", 30, 3, 1)
		for k := 1; k <= 4; k++ {
			id := (sv-1)*4 + k
			exec(`INSERT INTO keys(id,service_id,label,secret_enc,secret_fp,hint,created_at,hidden) VALUES(?,?,?,?,?,?,?,?)`,
				id, sv, "k"+itoa(id), []byte("x"), "fp"+itoa(id), "h", 1, b2i(k == 4))
		}
	}
	for a := 1; a <= 6; a++ {
		exec(`INSERT INTO apps(id,name,token_hash,token_prefix,created_at) VALUES(?,?,?,?,?)`, a, "app"+itoa(a), "h"+itoa(a), "p", 1)
	}
	kinds := []string{"proxy", "proxy", "proxy", "lease", "cache_hit", "error", "rate_limited"}
	r := rand.New(rand.NewPCG(5, 6))
	nowS := time.Now().Unix()
	for i := 0; i < 20000; i++ {
		sv := r.IntN(4) // 0: no service
		var svc, key, app any
		if sv > 0 {
			svc = sv
			if r.IntN(10) > 0 {
				key = (sv-1)*4 + r.IntN(4) + 1
			}
		}
		if a := r.IntN(7); a > 0 {
			app = a
		}
		exec(`INSERT INTO events(ts,kind,service_id,key_id,app_id,status_code,cost) VALUES(?,?,?,?,?,200,?)`,
			nowS-int64(r.IntN(40*86400)), kinds[r.IntN(len(kinds))], svc, key, app, float64(r.IntN(100))/1000)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	optimize(ctx, s.DB)
}

// legacyProviderUsage is the provider side of UsageReport as it was before
// the windows came from the by day pass: one query per window, and the
// service and app groupings left to the planner.
func legacyProviderUsage(t *testing.T, s *Store, at time.Time, days int, includeHidden bool) (map[string]UsageTotals, []ProviderUsageRow, []ProviderUsageRow, []ProviderUsageRow) {
	t.Helper()
	ctx := context.Background()
	hid := ""
	if !includeHidden {
		hid = notHidden
	}
	tot := map[string]UsageTotals{}
	for _, w := range []struct {
		name string
		n    int
	}{{"today", 1}, {"7d", 7}, {"30d", 30}, {"range", days}} {
		var u UsageTotals
		if err := s.DB.QueryRow(`SELECT `+providerSums+` FROM events e WHERE e.ts>=?`+hid, dayStart(at, w.n).Unix()).
			Scan(&u.Requests, &u.ProviderCostUSD, &u.CacheHits); err != nil {
			t.Fatal(err)
		}
		tot[w.name] = u
	}
	from := dayStart(at, days)
	rows, err := s.provRows(ctx, `SELECT strftime('%Y-%m-%d', e.ts, 'unixepoch'), 0, '', `+providerSums+
		` FROM events e WHERE e.ts>=?`+hid+` GROUP BY 1`, from.Unix())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]ProviderUsageRow{}
	for _, r := range rows {
		m[r.Key] = r
	}
	var byDay []ProviderUsageRow
	for d := 0; d < days; d++ {
		day := dayOf(from.AddDate(0, 0, d))
		p := m[day]
		p.Key = day
		byDay = append(byDay, p)
	}
	bySvc, err := s.provRows(ctx, `SELECT COALESCE(s.slug, '#'||e.service_id), e.service_id, COALESCE(s.name,''), `+providerSums+
		` FROM events e LEFT JOIN services s ON s.id=e.service_id WHERE e.ts>=? AND e.service_id IS NOT NULL
		AND e.kind IN ('proxy','lease','cache_hit')`+hid+` GROUP BY e.service_id ORDER BY 5 DESC, 4 DESC, 1`, from.Unix())
	if err != nil {
		t.Fatal(err)
	}
	byApp, err := s.provRows(ctx, `SELECT COALESCE(a.name, '#'||e.app_id), e.app_id, COALESCE(a.name,''), `+providerSums+
		` FROM events e LEFT JOIN apps a ON a.id=e.app_id WHERE e.ts>=? AND e.app_id IS NOT NULL
		AND e.kind IN ('proxy','lease','cache_hit')`+hid+` GROUP BY e.app_id ORDER BY 5 DESC, 4 DESC, 1`, from.Unix())
	if err != nil {
		t.Fatal(err)
	}
	return tot, byDay, bySvc, byApp
}

// UsageReport's provider numbers match the per window queries it replaced,
// for short and long ranges, with and without hidden keys.
func TestUsageReportMatchesPerWindowQueries(t *testing.T) {
	s := openTest(t)
	seedUsage(t, s)
	ctx := context.Background()
	at := time.Now()
	sameRows := func(name string, a, b []ProviderUsageRow) {
		t.Helper()
		if len(a) != len(b) {
			t.Fatalf("%s: %d rows, want %d", name, len(a), len(b))
		}
		for i := range a {
			if a[i].Key != b[i].Key || a[i].ID != b[i].ID || a[i].Requests != b[i].Requests || a[i].CacheHits != b[i].CacheHits || !near(a[i].CostUSD, b[i].CostUSD) {
				t.Fatalf("%s row %d: %+v, want %+v", name, i, a[i], b[i])
			}
		}
	}
	for _, days := range []int{1, 7, 14, 30, 90} {
		for _, inc := range []bool{false, true} {
			rep, err := s.UsageReport(ctx, at, days, inc)
			if err != nil {
				t.Fatal(err)
			}
			tot, byDay, bySvc, byApp := legacyProviderUsage(t, s, at, days, inc)
			for k, want := range tot {
				got := rep.Totals[k]
				if got.Requests != want.Requests || got.CacheHits != want.CacheHits || !near(got.ProviderCostUSD, want.ProviderCostUSD) {
					t.Fatalf("days=%d hidden=%v %s: %+v, want %+v", days, inc, k, got, want)
				}
			}
			sameRows("byDay", rep.Provider.ByDay, byDay)
			sameRows("byService", rep.Provider.ByService, bySvc)
			sameRows("byApp", rep.Provider.ByApp, byApp)
		}
	}
}

// The per service and per app sums read the range through events_ts.
func TestUsageReportGroupingPlan(t *testing.T) {
	s := openTest(t)
	seedUsage(t, s)
	for _, hid := range []string{"", notHidden} {
		if plan := explain(t, s.DB, serviceAppUsageSQL+hid+` GROUP BY 1, 2`, 0); !strings.Contains(plan, "events_ts") {
			t.Errorf("plan %q does not use events_ts", plan)
		}
	}
}
