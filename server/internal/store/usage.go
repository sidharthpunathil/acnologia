package store

import (
	"context"
	"database/sql"
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
	err := s.DB.QueryRowContext(ctx, `SELECT daily_requests, monthly_requests, daily_budget, monthly_budget FROM apps WHERE id=?`, appID).Scan(&dr, &mr, &db, &mb)
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
// Cache hits are counted separately and cost nothing.
func (s *Store) RecordAppUsage(ctx context.Context, appID int64, cost float64, cacheHit bool) error {
	req, hit := 1, 0
	if cacheHit {
		req, hit, cost = 0, 1, 0
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO app_usage(app_id, day, requests, cost, cache_hits) VALUES(?,?,?,?,?)
		ON CONFLICT(app_id, day) DO UPDATE SET requests=requests+excluded.requests, cost=cost+excluded.cost, cache_hits=cache_hits+excluded.cache_hits`,
		appID, dayOf(time.Now()), req, cost, hit)
	return err
}

func (s *Store) GetAppUsage(ctx context.Context, appID int64, at time.Time) (AppUsage, error) {
	var u AppUsage
	today := dayOf(at)
	month := today[:7] + "-01"
	err := s.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN day=? THEN requests END),0), COALESCE(SUM(CASE WHEN day=? THEN cost END),0),
		COALESCE(SUM(requests),0), COALESCE(SUM(cost),0),
		COALESCE(SUM(CASE WHEN day=? THEN cache_hits END),0), COALESCE(SUM(cache_hits),0)
		FROM app_usage WHERE app_id=? AND day>=? AND day<=?`, today, today, today, appID, month, today).
		Scan(&u.TodayRequests, &u.TodayCost, &u.MonthRequests, &u.MonthCost, &u.TodayHits, &u.MonthHits)
	return u, err
}

func (s *Store) AppUsageHistory(ctx context.Context, appID int64, days int) ([]UsageDay, error) {
	since := dayOf(time.Now().AddDate(0, 0, -days+1))
	rows, err := s.DB.QueryContext(ctx, `SELECT day, requests, cost, cache_hits FROM app_usage WHERE app_id=? AND day>=? ORDER BY day`, appID, since)
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
	Limit   string    `json:"limit"` // daily_requests | monthly_requests | daily_budget | monthly_budget
	ResetAt time.Time `json:"reset_at"`
}

// CheckLimits reports the first limit the app has reached, or nil.
func CheckLimits(l AppLimits, u AppUsage, at time.Time) *LimitHit {
	at = at.UTC()
	y, m, d := at.Date()
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
	switch {
	case l.DailyRequests != nil && u.TodayRequests >= *l.DailyRequests:
		return &LimitHit{"daily_requests", tomorrow}
	case l.DailyBudget != nil && u.TodayCost >= *l.DailyBudget:
		return &LimitHit{"daily_budget", tomorrow}
	case l.MonthlyRequests != nil && u.MonthRequests >= *l.MonthlyRequests:
		return &LimitHit{"monthly_requests", nextMonth}
	case l.MonthlyBudget != nil && u.MonthCost >= *l.MonthlyBudget:
		return &LimitHit{"monthly_budget", nextMonth}
	}
	return nil
}

// SpendSince sums the USD cost of events since a time. Hidden keys' spend is
// left out unless includeHidden is set, so admins cannot infer them.
func (s *Store) SpendSince(ctx context.Context, since time.Time, includeHidden bool) (float64, error) {
	q := `SELECT COALESCE(SUM(e.cost),0) FROM events e WHERE e.ts>=?`
	if !includeHidden {
		q += ` AND NOT EXISTS (SELECT 1 FROM keys k WHERE k.id=e.key_id AND k.hidden=1)`
	}
	var v float64
	err := s.DB.QueryRowContext(ctx, q, since.Unix()).Scan(&v)
	return v, err
}
