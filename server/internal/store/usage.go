package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"time"
)

// AppLimits caps an app's upstream requests and spend. Nil means no limit.
// Days and months are UTC.
type AppLimits struct {
	DailyRequests   *int64   `json:"daily_requests"`
	MonthlyRequests *int64   `json:"monthly_requests"`
	DailyBudget     *float64 `json:"daily_budget"`
	MonthlyBudget   *float64 `json:"monthly_budget"`
}

func (l AppLimits) Any() bool {
	return l.DailyRequests != nil || l.MonthlyRequests != nil || l.DailyBudget != nil || l.MonthlyBudget != nil
}

// Validate refuses a negative limit.
func (l AppLimits) Validate() error {
	if (l.DailyRequests != nil && *l.DailyRequests < 0) || (l.MonthlyRequests != nil && *l.MonthlyRequests < 0) ||
		(l.DailyBudget != nil && *l.DailyBudget < 0) || (l.MonthlyBudget != nil && *l.MonthlyBudget < 0) {
		return errors.New("limits must not be negative")
	}
	return nil
}

// Over is l with every limit it leaves unset taken from def.
func (l AppLimits) Over(def AppLimits) AppLimits {
	if l.DailyRequests == nil {
		l.DailyRequests = def.DailyRequests
	}
	if l.MonthlyRequests == nil {
		l.MonthlyRequests = def.MonthlyRequests
	}
	if l.DailyBudget == nil {
		l.DailyBudget = def.DailyBudget
	}
	if l.MonthlyBudget == nil {
		l.MonthlyBudget = def.MonthlyBudget
	}
	return l
}

// AppUsage is an app's use so far today and this month.
type AppUsage struct {
	TodayRequests int64   `json:"today_requests"`
	TodayCost     float64 `json:"today_cost"`
	MonthRequests int64   `json:"month_requests"`
	MonthCost     float64 `json:"month_cost"`
	TodayHits     int64   `json:"today_cache_hits"`
	MonthHits     int64   `json:"month_cache_hits"`
}

// UsageDay is one row of an app's history.
type UsageDay struct {
	Day       string  `json:"day"`
	Requests  int64   `json:"requests"`
	Cost      float64 `json:"cost"`
	CacheHits int64   `json:"cache_hits"`
}

func dayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }

func (s *Store) GetAppLimits(ctx context.Context, appID int64) (AppLimits, error) {
	var l AppLimits
	var dr, mr sql.NullInt64
	var db, mb sql.NullFloat64
	err := s.r.QueryRowContext(ctx, `SELECT daily_requests, monthly_requests, daily_budget, monthly_budget FROM apps WHERE id=?`, appID).Scan(&dr, &mr, &db, &mb)
	if err == sql.ErrNoRows {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.DailyRequests, l.MonthlyRequests = toInt(dr), toInt(mr)
	if db.Valid {
		l.DailyBudget = &db.Float64
	}
	if mb.Valid {
		l.MonthlyBudget = &mb.Float64
	}
	return l, nil
}

func (s *Store) SetAppLimits(ctx context.Context, appID int64, l AppLimits) error {
	f := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE apps SET daily_requests=?, monthly_requests=?, daily_budget=?, monthly_budget=? WHERE id=?`,
		fromInt(l.DailyRequests), fromInt(l.MonthlyRequests), f(l.DailyBudget), f(l.MonthlyBudget), appID)
	return err
}

// RecordAppUsage adds one upstream request and its cost to today's row.
// Cache hits are counted separately and cost nothing. The write is group
// committed and does not stop when ctx is cancelled: the request was served,
// so a client hanging up must not keep it out of the app's limits. It has
// committed when RecordAppUsage returns. A request made with a token (see
// WithAppToken) also counts toward the token, and one with caller tags
// (SetUsageTags) toward each tag value, in the same commit.
func (s *Store) RecordAppUsage(ctx context.Context, appID int64, cost float64, cacheHit bool) error {
	req, hit := 1, 0
	if cacheHit {
		req, hit, cost = 0, 1, 0
	}
	day := dayOf(time.Now())
	stmts := []wstmt{{`INSERT INTO app_usage(app_id, day, requests, cost, cache_hits) VALUES(?,?,?,?,?)
		ON CONFLICT(app_id, day) DO UPDATE SET requests=requests+excluded.requests, cost=cost+excluded.cost, cache_hits=cache_hits+excluded.cache_hits`,
		[]any{appID, day, req, cost, hit}}}
	if !cacheHit {
		stmts = append(stmts, scopeStmts(ctx, appID, day, cost)...)
	}
	return s.gw.write(stmts...)
}

// ServerSpendToday is every app's recorded spend for the UTC day of at.
func (s *Store) ServerSpendToday(ctx context.Context, at time.Time) (float64, error) {
	var v float64
	err := s.r.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost),0) FROM app_usage WHERE day=?`, dayOf(at)).Scan(&v)
	return v, err
}

// The server-wide daily spend ceiling a superadmin saved, in USD. It wins
// over ACNOLOGIA_DAILY_SPEND_CEILING_USD; "off" saved turns the ceiling off
// whatever the environment says. Admission reads it on every request, so it
// is cached here and the cache is dropped on every change.
const metaSpendCeiling = "daily_spend_ceiling_usd"

type ceilingEntry struct {
	v     float64
	saved bool
	at    time.Time
}

var ceilingCache sync.Map // *Store -> ceilingEntry

// ceilingTTL bounds how stale the cached ceiling can be if the meta row
// changes behind the store's back.
const ceilingTTL = 5 * time.Second

// SavedSpendCeiling returns the saved ceiling (0 when saved as off) and
// whether one is saved.
func (s *Store) SavedSpendCeiling(ctx context.Context) (float64, bool) {
	if v, ok := ceilingCache.Load(s); ok {
		if e := v.(ceilingEntry); time.Since(e.at) < ceilingTTL {
			return e.v, e.saved
		}
	}
	raw, found, err := s.LookupMeta(ctx, metaSpendCeiling)
	if err != nil {
		return 0, false
	}
	e := ceilingEntry{at: time.Now()}
	if found {
		if raw == "off" {
			e.saved = true
		} else if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 {
			e.v, e.saved = f, true
		}
	}
	ceilingCache.Store(s, e)
	return e.v, e.saved
}

// SetSpendCeiling saves the ceiling; 0 saves "off".
func (s *Store) SetSpendCeiling(ctx context.Context, usd float64) error {
	v := "off"
	if usd > 0 {
		v = strconv.FormatFloat(usd, 'f', -1, 64)
	}
	defer ceilingCache.Delete(s)
	return s.SetMeta(ctx, metaSpendCeiling, v)
}

// ClearSpendCeiling drops the saved ceiling, so the environment's applies.
func (s *Store) ClearSpendCeiling(ctx context.Context) error {
	defer ceilingCache.Delete(s)
	_, err := s.DB.ExecContext(ctx, `DELETE FROM meta WHERE k=?`, metaSpendCeiling)
	return err
}

func (s *Store) GetAppUsage(ctx context.Context, appID int64, at time.Time) (AppUsage, error) {
	var u AppUsage
	today := dayOf(at)
	month := today[:7] + "-01"
	err := s.r.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN day=? THEN requests END),0), COALESCE(SUM(CASE WHEN day=? THEN cost END),0),
		COALESCE(SUM(requests),0), COALESCE(SUM(cost),0),
		COALESCE(SUM(CASE WHEN day=? THEN cache_hits END),0), COALESCE(SUM(cache_hits),0)
		FROM app_usage WHERE app_id=? AND day>=? AND day<=?`, today, today, today, appID, month, today).
		Scan(&u.TodayRequests, &u.TodayCost, &u.MonthRequests, &u.MonthCost, &u.TodayHits, &u.MonthHits)
	return u, err
}

func (s *Store) AppUsageHistory(ctx context.Context, appID int64, days int) ([]UsageDay, error) {
	since := dayOf(time.Now().AddDate(0, 0, -days+1))
	rows, err := s.r.QueryContext(ctx, `SELECT day, requests, cost, cache_hits FROM app_usage WHERE app_id=? AND day>=? ORDER BY day`, appID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageDay{}
	for rows.Next() {
		var d UsageDay
		if err := rows.Scan(&d.Day, &d.Requests, &d.Cost, &d.CacheHits); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LimitHit says which limit an app reached and when it lifts.
type LimitHit struct {
	// Limit is daily_requests, monthly_requests, daily_budget or
	// monthly_budget for the app's own limits; the same with a token_ or
	// tag_ prefix for a token's or a tag value's; server_spend_ceiling; or
	// tag_pinned when the request's tags contradict its token's.
	Limit   string    `json:"limit"`
	ResetAt time.Time `json:"reset_at"`
	// Tag is the tag value (customer=cust_A) a tag_ limit is about, or the
	// key a tag_pinned refusal is about.
	Tag string `json:"tag,omitempty"`
}

// CheckLimits reports the first limit the app has reached, or nil. It is
// what the dashboard shows; admitting a request uses AdmitLimits.
func CheckLimits(l AppLimits, u AppUsage, at time.Time) *LimitHit {
	return checkLimits(l, u, at, 0, 0)
}

// AdmitLimits reports the first limit one more request would break, or
// nil. The request counts as one request and est dollars on top of what is
// used, so the request that would cross a budget is refused rather than let
// through to overshoot it by its own cost. A budget already reached refuses
// every request, even one whose price is not known up front (est 0).
func AdmitLimits(l AppLimits, u AppUsage, at time.Time, est float64) *LimitHit {
	return checkLimits(l, u, at, 1, max(est, 0))
}

// budgetSlack absorbs float rounding, so a request that lands exactly on a
// budget is not refused because 0.1+0.2 > 0.3.
const budgetSlack = 1e-9

func checkLimits(l AppLimits, u AppUsage, at time.Time, reqs int64, est float64) *LimitHit {
	at = at.UTC()
	y, m, d := at.Date()
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
	overReqs := func(used, limit int64) bool { return used >= limit || used+reqs > limit }
	overBudget := func(used, limit float64) bool { return used >= limit || used+est > limit+budgetSlack }
	switch {
	case l.DailyRequests != nil && overReqs(u.TodayRequests, *l.DailyRequests):
		return &LimitHit{Limit: "daily_requests", ResetAt: tomorrow}
	case l.DailyBudget != nil && overBudget(u.TodayCost, *l.DailyBudget):
		return &LimitHit{Limit: "daily_budget", ResetAt: tomorrow}
	case l.MonthlyRequests != nil && overReqs(u.MonthRequests, *l.MonthlyRequests):
		return &LimitHit{Limit: "monthly_requests", ResetAt: nextMonth}
	case l.MonthlyBudget != nil && overBudget(u.MonthCost, *l.MonthlyBudget):
		return &LimitHit{Limit: "monthly_budget", ResetAt: nextMonth}
	}
	return nil
}

// SpendSince sums the USD cost of events since a time. Hidden keys' spend is
// left out unless includeHidden is set, so admins cannot infer them.
func (s *Store) SpendSince(ctx context.Context, since time.Time, includeHidden bool) (float64, error) {
	q := `SELECT COALESCE(SUM(e.cost),0) FROM events e WHERE e.ts>=?`
	if !includeHidden {
		q += notHidden
	}
	var v float64
	err := s.r.QueryRowContext(ctx, q, since.Unix()).Scan(&v)
	return v, err
}
