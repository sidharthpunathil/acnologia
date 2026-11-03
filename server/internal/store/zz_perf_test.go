package store

// Store performance harness. The benchmarks run with -bench; the load and
// timing tests run only with ZZLOAD=1, since they seed a large database:
//
//	go test ./internal/store -run XXX -bench 'BenchmarkQuery|BenchmarkDashboard|BenchmarkGroupCommit'
//	ZZLOAD=1 go test ./internal/store -run 'TestZZLoad$|TestZZLoad2|TestZZPrune|TestZZPlans|TestZZVariants' -v
//
// SEED_EVENTS sets the number of seeded events (default 500000) and
// LOAD_SEC the length of each load run (default 4).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
)

const (
	nServices = 20
	nKeysPer  = 10
	nApps     = 20
)

func seedEvents() int {
	if n, err := strconv.Atoi(os.Getenv("SEED_EVENTS")); err == nil && n >= 0 {
		return n
	}
	return 500000
}

func needLoad(tb testing.TB) {
	if os.Getenv("ZZLOAD") == "" {
		tb.Skip("set ZZLOAD=1 to run the store load harness")
	}
}

// seedDB fills a data dir with services, keys, apps, tokens, usage and n
// events spread over the last 30 days.
func seedDB(tb testing.TB, dir string, n int) {
	s, err := Open(dir)
	if err != nil {
		tb.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatal(err)
	}
	nowS := time.Now().Unix()
	must := func(_ sql.Result, err error) {
		if err != nil {
			tb.Fatal(err)
		}
	}
	for i := 1; i <= nServices; i++ {
		must(tx.Exec(`INSERT INTO services(id,slug,name,provider,base_url,auth_type,auth_name,strategy,cooldown_sec,max_attempts,created_at,target_trusted)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`, i, fmt.Sprintf("svc%d", i), fmt.Sprintf("Service %d", i), "custom", "http://127.0.0.1:1", "bearer", "Authorization", "round_robin", 30, 3, nowS))
		for j := 1; j <= nKeysPer; j++ {
			id := (i-1)*nKeysPer + j
			must(tx.Exec(`INSERT INTO keys(id,service_id,label,secret_enc,secret_fp,hint,created_at) VALUES(?,?,?,?,?,?,?)`,
				id, i, fmt.Sprintf("k%d", id), []byte(strings.Repeat("x", 60)), fmt.Sprintf("fp%d", id), "...abcd", nowS))
		}
	}
	for a := 1; a <= nApps; a++ {
		var dr any
		if a%2 == 0 {
			dr = 1000000
		}
		must(tx.Exec(`INSERT INTO apps(id,name,token_hash,token_prefix,allowed_services,created_at,daily_requests) VALUES(?,?,?,?,?,?,?)`,
			a, fmt.Sprintf("app%d", a), fmt.Sprintf("legacy%d", a), "acn_", `["*"]`, nowS, dr))
		must(tx.Exec(`INSERT INTO app_tokens(app_id,name,prefix,hash,created_at) VALUES(?,?,?,?,?)`, a, "t", "acn_", crypto.SHA256(fmt.Sprintf("tok%d", a)), nowS))
		for d := 0; d < 30; d++ {
			must(tx.Exec(`INSERT INTO app_usage(app_id,day,requests,cost) VALUES(?,?,?,?)`, a, dayOf(time.Now().AddDate(0, 0, -d)), 100, 0.5))
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	kinds := []string{"proxy", "proxy", "proxy", "proxy", "lease", "error", "rate_limited", "cache_hit"}
	r := rand.New(rand.NewPCG(1, 2))
	for done := 0; done < n; {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			tb.Fatal(err)
		}
		st, err := tx.Prepare(`INSERT INTO events(ts,kind,service_id,key_id,app_id,token_id,status_code,latency_ms,detail,cost) VALUES(?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			tb.Fatal(err)
		}
		for i := 0; i < 20000 && done < n; i++ {
			sv := r.IntN(nServices) + 1
			k := (sv-1)*nKeysPer + r.IntN(nKeysPer) + 1
			ts := nowS - int64(r.IntN(30*86400))
			must(st.Exec(ts, kinds[r.IntN(len(kinds))], sv, k, r.IntN(nApps)+1, nil, 200, 120, "POST /v1/chat/completions", 0.001))
			done++
		}
		st.Close()
		if err := tx.Commit(); err != nil {
			tb.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		tb.Fatal(err)
	}
}

// schemaPrint names the schema Open creates, so a seeded template made by an
// older schema is not reused.
func schemaPrint(tb testing.TB) string {
	dir := tb.TempDir()
	s, err := Open(dir)
	if err != nil {
		tb.Fatal(err)
	}
	defer s.Close()
	var sqlText string
	if err := s.DB.QueryRow(`SELECT group_concat(COALESCE(sql,''), ';') FROM (SELECT sql FROM sqlite_master ORDER BY name)`).Scan(&sqlText); err != nil {
		tb.Fatal(err)
	}
	h := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(h[:6])
}

var (
	tmplOnce sync.Once
	tmplDir  string
)

// freshDir copies a seeded template data dir so every run starts equal.
func freshDir(tb testing.TB) string {
	tmplOnce.Do(func() {
		n := seedEvents()
		tmplDir = filepath.Join(os.TempDir(), fmt.Sprintf("acn-store-perf-%d-%s", n, schemaPrint(tb)))
		if _, err := os.Stat(filepath.Join(tmplDir, DBFile)); err != nil {
			_ = os.RemoveAll(tmplDir)
			if err := os.MkdirAll(tmplDir, 0o700); err != nil {
				tb.Fatal(err)
			}
			st := time.Now()
			seedDB(tb, tmplDir, n)
			fmt.Printf("seeded %d events in %v at %s\n", n, time.Since(st).Round(time.Millisecond), tmplDir)
		}
	})
	dir := tb.TempDir()
	b, err := os.ReadFile(filepath.Join(tmplDir, DBFile))
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DBFile), b, 0o600); err != nil {
		tb.Fatal(err)
	}
	return dir
}

func openAt(tb testing.TB, dir string) *Store {
	s, err := Open(dir)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	return s
}

// ---------- per call latency ----------

func BenchmarkQuery(b *testing.B) {
	s := openAt(b, freshDir(b))
	ctx := context.Background()
	hash := crypto.SHA256("tok3")
	run := func(name string, f func()) {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				f()
			}
		})
	}
	run("AppByToken", func() { _, _, _ = s.AppByToken(ctx, hash) })
	run("GetServiceBySlug", func() { _, _ = s.GetServiceBySlug(ctx, "svc3") })
	run("GetAppLimits", func() { _, _ = s.GetAppLimits(ctx, 4) })
	run("GetAppUsage", func() { _, _ = s.GetAppUsage(ctx, 4, time.Now()) })
	run("AppKeyScope_cached", func() { _, _ = s.AppKeyScope(ctx, 4, "svc3") })
	run("ShareServiceID", func() { _, _, _ = s.ShareServiceID(ctx, 4, "svc3") })
	run("CandidateKeys", func() { _, _ = s.CandidateKeys(ctx, 3) })
	run("GetKey", func() { _, _ = s.GetKey(ctx, 25) })
	run("W_MarkUsed", func() { _ = s.MarkUsed(ctx, 25) })
	run("W_AddEvent", func() {
		s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: 3, KeyID: 25, AppID: 4, StatusCode: 200, LatencyMS: 100, Detail: "POST /v1/chat/completions"})
	})
	zero := 0
	run("W_ApplyEffectOK", func() { _ = s.ApplyEffect(ctx, 25, KeyEffect{ConsecutiveFails: &zero, MarkOK: true}) })
	run("W_RecordAppUsage", func() { _ = s.RecordAppUsage(ctx, 4, 0.001, false) })
	old := TokenTouchInterval
	TokenTouchInterval = 0
	run("W_TouchAppToken_unthrottled", func() { s.TouchAppToken(ctx, 4, 4) })
	TokenTouchInterval = old
	run("W_4writes", func() {
		_ = s.MarkUsed(ctx, 25)
		s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: 3, KeyID: 25, AppID: 4, StatusCode: 200, LatencyMS: 100, Detail: "POST /v1/chat/completions"})
		_ = s.ApplyEffect(ctx, 25, KeyEffect{ConsecutiveFails: &zero, MarkOK: true})
		_ = s.RecordAppUsage(ctx, 4, 0.001, false)
	})
}

// BenchmarkGroupCommit inserts events from many goroutines at once, so the
// cost per op is that of one event insert inside a shared commit.
func BenchmarkGroupCommit(b *testing.B) {
	s := openAt(b, freshDir(b))
	ctx := context.Background()
	for _, par := range []int{1, 16, 64} {
		b.Run(fmt.Sprintf("AddEvent_par%d", par), func(b *testing.B) {
			b.SetParallelism(max(par/8, 1))
			if par == 1 {
				for i := 0; i < b.N; i++ {
					s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: 3, KeyID: 25, AppID: 4, StatusCode: 200, LatencyMS: 100, Detail: "POST /v1/chat/completions"})
				}
				return
			}
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: 3, KeyID: 25, AppID: 4, StatusCode: 200, LatencyMS: 100, Detail: "POST /v1/chat/completions"})
				}
			})
		})
	}
}

// BenchmarkDashboard times the list page queries on the seeded events.
func BenchmarkDashboard(b *testing.B) {
	s := openAt(b, freshDir(b))
	ctx := context.Background()
	run := func(name string, f func()) {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				f()
			}
		})
	}
	run("ListEvents_all", func() { _, _ = s.ListEvents(ctx, EventFilter{Limit: 100}) })
	run("ListEvents_key", func() { _, _ = s.ListEvents(ctx, EventFilter{KeyID: 25, Limit: 100}) })
	run("ListEvents_kind", func() { _, _ = s.ListEvents(ctx, EventFilter{Kind: "rate_limited", Limit: 100}) })
	run("ListEvents_service", func() { _, _ = s.ListEvents(ctx, EventFilter{ServiceID: 3, Limit: 100}) })
	run("KeyDailyStats_14d", func() { _, _ = s.KeyDailyStats(ctx, 25, 14) })
	run("KeyDailyStats_30d", func() { _, _ = s.KeyDailyStats(ctx, 25, 30) })
	run("CountEvents_24h_nohidden", func() { _, _ = s.CountEvents(ctx, time.Now().Add(-24*time.Hour), false, "error") })
	run("SpendSince_30d_nohidden", func() { _, _ = s.SpendSince(ctx, time.Now().AddDate(0, 0, -30), false) })
	run("FillStats", func() {
		svcs, _ := s.ListServices(ctx)
		_ = s.FillStats(ctx, svcs, false, nil)
	})
	run("UsageReport_30d", func() { _, _ = s.UsageReport(ctx, time.Now(), 30, false) })
	run("ShareServiceCounts_14d", func() { _, _ = s.ShareServiceCounts(ctx, 4, time.Now().AddDate(0, 0, -14)) })
	run("ListApps", func() { _, _ = s.ListApps(ctx) })
	run("ListKeys_service", func() { _, _ = s.ListKeys(ctx, 3, false) })
	run("CreateKeys_maxid", func() { _ = createKeysHighWater(ctx, s) })
}

// createKeysHighWater runs the id high water query CreateKeys runs.
func createKeysHighWater(ctx context.Context, s *Store) int64 {
	var hw sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, keysHighWater).Scan(&hw)
	return hw.Int64
}

// ---------- housekeeping ----------

// TestZZPrune deletes the oldest third of the events (as a first prune after
// a retention cut from 30 to 20 days would) while a probe writes every
// millisecond, and reports the worst wait the probe saw: how long Prune can
// keep a request's write waiting for the one writer connection.
func TestZZPrune(t *testing.T) {
	needLoad(t)
	s := openAt(t, freshDir(t))
	ctx := context.Background()
	var before int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE ts<?`, time.Now().AddDate(0, 0, -20).Unix()).Scan(&before)
	stop := make(chan struct{})
	var worst time.Duration
	var probes int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			st := time.Now()
			_ = s.MarkUsed(ctx, 25)
			worst = max(worst, time.Since(st))
			probes++
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	st := time.Now()
	if err := s.Prune(ctx, 20*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	took := time.Since(st)
	close(stop)
	wg.Wait()
	var after int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE ts<?`, st.AddDate(0, 0, -20).Unix()).Scan(&after)
	if after != 0 {
		t.Fatalf("%d old events left after prune", after)
	}
	fmt.Printf("prune: deleted=%d took=%v probes=%d worst probe write wait=%v\n", before, took.Round(time.Millisecond), probes, worst.Round(time.Microsecond))
}

// TestZZPlans prints the query plans of the request path and list pages.
func TestZZPlans(t *testing.T) {
	needLoad(t)
	s := openAt(t, freshDir(t))
	for _, n := range sortedKeys(planQueries) {
		fmt.Printf("%-22s %s\n", n, explain(t, s.DB, planQueries[n]))
	}
}

var planQueries = map[string]string{
	"AppByToken.token":     `SELECT ` + appTokenCols + ` FROM app_tokens t LEFT JOIN users u ON u.id=t.created_by WHERE t.hash='x'`,
	"GetApp":               `SELECT ` + appCols + ` FROM apps WHERE id=1`,
	"GetServiceBySlug":     `SELECT ` + serviceCols + ` FROM services WHERE slug='svc1'`,
	"GetAppUsage":          `SELECT SUM(requests) FROM app_usage WHERE app_id=1 AND day>='2026-11-01' AND day<='2026-11-04'`,
	"CandidateKeys":        `SELECT ` + keyCols + ` FROM keys WHERE service_id=1 AND status='active'`,
	"Prune.leases":         `DELETE FROM leases WHERE created_at<0`,
	"Prune.events":         `DELETE FROM events WHERE ts<0`,
	"ListEvents.key":       `SELECT e.id FROM events e WHERE e.key_id=1 ORDER BY e.id DESC LIMIT 100`,
	"ListEvents.service":   `SELECT e.id FROM events e WHERE e.service_id=1 ORDER BY e.id DESC LIMIT 100`,
	"KeyDailyStats":        `SELECT date(ts,'unixepoch') d, COUNT(*) FROM events WHERE key_id=1 AND ts>=0 GROUP BY d ORDER BY d`,
	"FillStats.events":     `SELECT e.service_id, COUNT(*) FROM events e WHERE e.ts>0 AND e.kind IN ('lease','proxy') GROUP BY e.service_id`,
	"ShareServiceCounts":   `SELECT COALESCE(sv.slug,''), COUNT(*) FROM events e LEFT JOIN services sv ON sv.id=e.service_id WHERE e.app_id=1 AND e.ts>=0 AND e.kind IN ('proxy','cache_hit') GROUP BY sv.slug`,
	"CreateKeys.maxEvents": `SELECT MAX(key_id) FROM events`,
	"CreateKeys.maxLeases": `SELECT MAX(key_id) FROM leases`,
	"trigger.hiddenDelete": `DELETE FROM events WHERE key_id=1`,
	"SpendSince":           `SELECT COALESCE(SUM(e.cost),0) FROM events e WHERE e.ts>=0` + notHidden,
	"UsageReport.byDay":    `SELECT strftime('%Y-%m-%d', e.ts, 'unixepoch'), ` + providerSums + ` FROM events e WHERE e.ts>=0` + notHidden + ` GROUP BY 1`,
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func explain(tb testing.TB, db *sql.DB, q string, args ...any) string {
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		tb.Fatalf("explain %s: %v", q, err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			tb.Fatal(err)
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, " | ")
}
