package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

// oldNotHidden is the hidden key filter notHidden replaced.
const oldNotHidden = ` AND NOT EXISTS (SELECT 1 FROM keys k WHERE k.id=e.key_id AND k.hidden=1)`

// rowsOf returns every row of q as strings, in order.
func rowsOf(t *testing.T, s *Store, q string, args ...any) []string {
	t.Helper()
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []string{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(vals...))
	}
	return out
}

// The NOT IN filter keeps exactly the events the NOT EXISTS filter kept:
// events of visible keys, events with no key, and events of keys that no
// longer exist; it drops events of hidden keys. Checked on every query that
// uses it, and through the store methods with includeHidden true and false.
func TestNotHiddenMatchesOldFilter(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	svc := testService(t, s)
	var vis, hid []*Key
	for i := 0; i < 4; i++ {
		vis = append(vis, testKey(t, s, svc, "v"+itoa(i), false))
		hid = append(hid, testKey(t, s, svc, "h"+itoa(i), true))
	}
	r := rand.New(rand.NewPCG(5, 6))
	kinds := []string{"proxy", "lease", "cache_hit", "error", "rate_limited"}
	nowS := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3000; i++ {
		var key any
		switch r.IntN(4) {
		case 0:
			key = vis[r.IntN(len(vis))].ID
		case 1:
			key = hid[r.IntN(len(hid))].ID
		case 2:
			key = nil
		case 3:
			key = 9000 + r.IntN(5) // a key that is gone
		}
		var app any
		if r.IntN(3) > 0 {
			app = r.IntN(4) + 1
		}
		if _, err := tx.Exec(`INSERT INTO events(ts,kind,service_id,key_id,app_id,status_code,cost) VALUES(?,?,?,?,?,200,?)`,
			nowS-int64(r.IntN(10*86400)), kinds[r.IntN(len(kinds))], svc.ID, key, app, float64(r.IntN(100))/1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	since := nowS - 5*86400
	queries := []string{
		`SELECT COALESCE(SUM(e.cost),0) FROM events e WHERE e.ts>=?%s`,
		`SELECT COUNT(*) FROM events e WHERE e.ts>=?%s AND e.kind IN ('error','rate_limited')`,
		`SELECT e.service_id, COUNT(*) FROM events e WHERE e.ts>? AND e.kind IN ('lease','proxy')%s GROUP BY e.service_id`,
		`SELECT ` + providerSums + ` FROM events e WHERE e.ts>=?%s`,
		`SELECT strftime('%%Y-%%m-%%d', e.ts, 'unixepoch'), 0, '', ` + providerSums + ` FROM events e WHERE e.ts>=?%s GROUP BY 1`,
		`SELECT e.app_id, ` + providerSums + ` FROM events e WHERE e.ts>=? AND e.app_id IS NOT NULL AND e.kind IN ('proxy','lease','cache_hit')%s GROUP BY e.app_id ORDER BY 1`,
		`SELECT e.id FROM events e WHERE e.ts>=?%s ORDER BY e.id`,
	}
	for _, q := range queries {
		old := rowsOf(t, s, fmt.Sprintf(q, oldNotHidden), since)
		cur := rowsOf(t, s, fmt.Sprintf(q, notHidden), since)
		if !reflect.DeepEqual(old, cur) {
			t.Fatalf("filters differ on %s:\nold %v\nnew %v", q, old, cur)
		}
		all := rowsOf(t, s, fmt.Sprintf(q, ""), since)
		if reflect.DeepEqual(all, cur) {
			t.Fatalf("filter dropped nothing on %s", q)
		}
	}
	var hiddenEvents int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events e WHERE e.key_id IN (SELECT id FROM keys WHERE hidden=1)`).Scan(&hiddenEvents)
	if hiddenEvents == 0 {
		t.Fatal("no hidden key events seeded")
	}

	// Store methods: includeHidden false matches the old filter, true counts
	// everything.
	at := time.Unix(since, 0)
	for _, inc := range []bool{false, true} {
		filter := oldNotHidden
		if inc {
			filter = ""
		}
		var want float64
		if err := s.DB.QueryRow(`SELECT COALESCE(SUM(e.cost),0) FROM events e WHERE e.ts>=?`+filter, since).Scan(&want); err != nil {
			t.Fatal(err)
		}
		got, err := s.SpendSince(ctx, at, inc)
		if err != nil || got != want {
			t.Fatalf("SpendSince(includeHidden=%v) = %v %v, want %v", inc, got, err, want)
		}
		var wantN int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events e WHERE e.ts>=?`+filter+` AND e.kind IN ('error')`, since).Scan(&wantN); err != nil {
			t.Fatal(err)
		}
		n, err := s.CountEvents(ctx, at, inc, "error")
		if err != nil || n != wantN {
			t.Fatalf("CountEvents(includeHidden=%v) = %d %v, want %d", inc, n, err, wantN)
		}
		svcs, _ := s.ListServices(ctx)
		if err := s.FillStats(ctx, svcs, inc, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events e WHERE e.ts>? AND e.kind IN ('lease','proxy')`+filter, time.Now().Add(-24*time.Hour).Unix()).Scan(&wantN); err != nil {
			t.Fatal(err)
		}
		if svcs[0].Requests24h != wantN {
			t.Fatalf("FillStats(includeHidden=%v) = %d, want %d", inc, svcs[0].Requests24h, wantN)
		}
		rep, err := s.UsageReport(ctx, time.Now(), 5, inc)
		if err != nil {
			t.Fatal(err)
		}
		var wantReq int64
		if err := s.DB.QueryRow(`SELECT COALESCE(SUM(CASE WHEN e.kind IN ('proxy','lease') THEN 1 ELSE 0 END),0) FROM events e WHERE e.ts>=?`+filter,
			dayStart(time.Now(), 5).Unix()).Scan(&wantReq); err != nil {
			t.Fatal(err)
		}
		if rep.Totals["range"].Requests != wantReq {
			t.Fatalf("UsageReport(includeHidden=%v) requests %d, want %d", inc, rep.Totals["range"].Requests, wantReq)
		}
	}
}
