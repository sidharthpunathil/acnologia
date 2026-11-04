package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// schemaAIUsage is the ledger of Claude API calls: one row per call made with
// the operator's ANTHROPIC_API_KEY, so the Usage page can show what the AI
// features cost.
const schemaAIUsage = `
CREATE TABLE IF NOT EXISTS ai_usage(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  feature TEXT NOT NULL,
  user_id INTEGER,
  user_email TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  input_tokens INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  web_searches INTEGER NOT NULL DEFAULT 0,
  cost_usd REAL NOT NULL DEFAULT 0,
  ref TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS ai_usage_ts ON ai_usage(ts);
`

func migrateAIUsage(db *sql.DB) error {
	_, err := db.Exec(schemaAIUsage)
	return err
}

// AI features that call Claude.
const (
	AIFeatureBuilder   = "builder"
	AIFeatureAssistant = "assistant"
	AIFeatureFill      = "fill"
)

// AICall is one Claude API call in the ledger.
type AICall struct {
	TS               time.Time `json:"ts"`
	Feature          string    `json:"feature"`
	UserID           int64     `json:"user_id"`
	UserEmail        string    `json:"user_email"`
	Model            string    `json:"model"`
	InputTokens      int64     `json:"input_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	WebSearches      int64     `json:"web_searches"`
	CostUSD          float64   `json:"cost_usd"`
	// Ref points at what the call was for: "run:12" or "thread:4".
	Ref string `json:"ref"`
}

// RecordAICall appends one call to the ledger. A zero TS means now.
func (s *Store) RecordAICall(ctx context.Context, c AICall) error {
	ts := c.TS.Unix()
	if c.TS.IsZero() {
		ts = now()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO ai_usage(ts, feature, user_id, user_email, model, input_tokens, cache_write_tokens,
		cache_read_tokens, output_tokens, web_searches, cost_usd, ref) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		ts, c.Feature, nz(c.UserID), c.UserEmail, c.Model, c.InputTokens, c.CacheWriteTokens, c.CacheReadTokens,
		c.OutputTokens, c.WebSearches, c.CostUSD, c.Ref)
	return err
}

// ListAICalls returns ledger rows since a time, newest first.
func (s *Store) ListAICalls(ctx context.Context, since time.Time) ([]AICall, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT ts, feature, COALESCE(user_id,0), user_email, model, input_tokens, cache_write_tokens,
		cache_read_tokens, output_tokens, web_searches, cost_usd, ref FROM ai_usage WHERE ts>=? ORDER BY id DESC`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AICall{}
	for rows.Next() {
		var c AICall
		var ts int64
		if err := rows.Scan(&ts, &c.Feature, &c.UserID, &c.UserEmail, &c.Model, &c.InputTokens, &c.CacheWriteTokens,
			&c.CacheReadTokens, &c.OutputTokens, &c.WebSearches, &c.CostUSD, &c.Ref); err != nil {
			return nil, err
		}
		c.TS = time.Unix(ts, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- aggregates for the Usage page ----

// AIUsageRow sums ledger rows for one feature, model, user or day.
type AIUsageRow struct {
	Key              string  `json:"key"`
	UserID           int64   `json:"user_id,omitempty"`
	Calls            int64   `json:"calls"`
	InputTokens      int64   `json:"input_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	WebSearches      int64   `json:"web_searches"`
	CostUSD          float64 `json:"cost_usd"`
}

// ProviderUsageRow sums provider spend through the pool for one service, app or day.
type ProviderUsageRow struct {
	Key       string  `json:"key"`
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name,omitempty"`
	Requests  int64   `json:"requests"`
	CostUSD   float64 `json:"cost_usd"`
	CacheHits int64   `json:"cache_hits"`
}

// UsageTotals is one window of the headline numbers.
type UsageTotals struct {
	AICostUSD       float64 `json:"ai_cost_usd"`
	AICalls         int64   `json:"ai_calls"`
	ProviderCostUSD float64 `json:"provider_cost_usd"`
	Requests        int64   `json:"requests"`
	CacheHits       int64   `json:"cache_hits"`
}

type AIUsageReport struct {
	ByFeature []AIUsageRow `json:"by_feature"`
	ByModel   []AIUsageRow `json:"by_model"`
	ByUser    []AIUsageRow `json:"by_user"`
	ByDay     []AIUsageRow `json:"by_day"`
}

type ProviderUsageReport struct {
	ByDay     []ProviderUsageRow `json:"by_day"`
	ByService []ProviderUsageRow `json:"by_service"`
	ByApp     []ProviderUsageRow `json:"by_app"`
}

type UsageReport struct {
	Days  int    `json:"days"`
	Since string `json:"since"` // first UTC day of the range
	Until string `json:"until"` // today, UTC
	// Totals has fixed windows (today, 7d, 30d) plus "range" for the requested days.
	Totals   map[string]UsageTotals `json:"totals"`
	AI       AIUsageReport          `json:"ai"`
	Provider ProviderUsageReport    `json:"provider"`
}

// dayStart is UTC midnight n-1 days before at, so n=1 is today.
func dayStart(at time.Time, n int) time.Time {
	y, m, d := at.UTC().Date()
	return time.Date(y, m, d-(n-1), 0, 0, 0, 0, time.UTC)
}

// notHidden leaves out events of hidden keys, for every count and sum shown
// to someone who may not see them. An event with no key, or whose key is
// gone, stays in. keys.id is never NULL, so NOT IN cannot turn NULL. The
// hidden ids are read once per query instead of once per event.
const notHidden = ` AND (e.key_id IS NULL OR e.key_id NOT IN (SELECT id FROM keys WHERE hidden=1))`

// providerSums are the per row expressions over events: upstream requests
// (proxied calls and leases, as on the overview), spend, and cache hits.
const providerSums = `COALESCE(SUM(CASE WHEN e.kind IN ('proxy','lease') THEN 1 ELSE 0 END),0),
	COALESCE(SUM(e.cost),0), COALESCE(SUM(CASE WHEN e.kind='cache_hit' THEN 1 ELSE 0 END),0)`

const aiSums = `COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(cache_read_tokens),0),
	COALESCE(SUM(output_tokens),0), COALESCE(SUM(web_searches),0), COALESCE(SUM(cost_usd),0)`

// UsageReport aggregates Claude and provider spend over the last days UTC
// days ending at at. Provider spend of hidden keys is left out unless
// includeHidden is set. Provider numbers come from events, so they only go
// back as far as the event retention.
func (s *Store) UsageReport(ctx context.Context, at time.Time, days int, includeHidden bool) (*UsageReport, error) {
	if days < 1 {
		days = 1
	}
	from := dayStart(at, days)
	rep := &UsageReport{Days: days, Since: dayOf(from), Until: dayOf(at), Totals: map[string]UsageTotals{}}
	hid := ""
	if !includeHidden {
		hid = notHidden
	}

	// One pass over the events, by UTC day, gives the chart and the provider
	// side of every window: each window starts at a UTC midnight. It covers at
	// least 30 days so the 30d window is complete for shorter ranges. The day
	// is grouped as ts/86400 (a Unix day is a UTC day), which is cheaper than
	// formatting a date for every event, and formatted once per day here.
	provDays, err := s.provRows(ctx, `SELECT e.ts/86400, 0, '', `+providerSums+
		` FROM events e WHERE e.ts>=?`+hid+` GROUP BY 1`, dayStart(at, max(days, 30)).Unix())
	if err != nil {
		return nil, err
	}
	for i := range provDays {
		d, _ := strconv.ParseInt(provDays[i].Key, 10, 64)
		provDays[i].Key = dayOf(time.Unix(d*86400, 0))
	}
	windows := []struct {
		name string
		n    int
	}{{"today", 1}, {"7d", 7}, {"30d", 30}, {"range", days}}
	for _, w := range windows {
		start := dayStart(at, w.n)
		var t UsageTotals
		if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(cost_usd),0) FROM ai_usage WHERE ts>=?`, start.Unix()).
			Scan(&t.AICalls, &t.AICostUSD); err != nil {
			return nil, err
		}
		first := dayOf(start)
		for _, r := range provDays {
			if r.Key >= first {
				t.Requests += r.Requests
				t.ProviderCostUSD += r.CostUSD
				t.CacheHits += r.CacheHits
			}
		}
		rep.Totals[w.name] = t
	}

	since := from.Unix()
	aiQ := func(key, extra string) ([]AIUsageRow, error) {
		return s.aiRows(ctx, `SELECT `+key+`, `+aiSums+` FROM ai_usage WHERE ts>=? GROUP BY 1`+extra, since)
	}
	if rep.AI.ByFeature, err = aiQ(`feature, 0`, ` ORDER BY 9 DESC, 1`); err != nil {
		return nil, err
	}
	if rep.AI.ByModel, err = aiQ(`model, 0`, ` ORDER BY 9 DESC, 1`); err != nil {
		return nil, err
	}
	// Grouped by email so a deleted user's history stays readable.
	if rep.AI.ByUser, err = s.aiRows(ctx, `SELECT user_email, COALESCE(MAX(user_id),0), `+aiSums+
		` FROM ai_usage WHERE ts>=? GROUP BY user_email ORDER BY 9 DESC, 1`, since); err != nil {
		return nil, err
	}
	byDay, err := aiQ(`strftime('%Y-%m-%d', ts, 'unixepoch'), 0`, ``)
	if err != nil {
		return nil, err
	}
	aiDays := map[string]AIUsageRow{}
	for _, r := range byDay {
		aiDays[r.Key] = r
	}

	provByDay := map[string]ProviderUsageRow{}
	for _, r := range provDays {
		provByDay[r.Key] = r
	}
	// Every day of the range is present so charts need no gap filling.
	for d := 0; d < days; d++ {
		day := dayOf(from.AddDate(0, 0, d))
		a := aiDays[day]
		a.Key = day
		rep.AI.ByDay = append(rep.AI.ByDay, a)
		p := provByDay[day]
		p.Key = day
		rep.Provider.ByDay = append(rep.Provider.ByDay, p)
	}

	// Only services and apps that saw traffic in the range are listed.
	if rep.Provider.ByService, rep.Provider.ByApp, err = s.serviceAppUsage(ctx, since, hid); err != nil {
		return nil, err
	}
	return rep, nil
}

func (s *Store) aiRows(ctx context.Context, q string, args ...any) ([]AIUsageRow, error) {
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ai usage: %w", err)
	}
	defer rows.Close()
	out := []AIUsageRow{}
	for rows.Next() {
		var r AIUsageRow
		if err := rows.Scan(&r.Key, &r.UserID, &r.Calls, &r.InputTokens, &r.CacheWriteTokens, &r.CacheReadTokens,
			&r.OutputTokens, &r.WebSearches, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) provRows(ctx context.Context, q string, args ...any) ([]ProviderUsageRow, error) {
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("provider usage: %w", err)
	}
	defer rows.Close()
	out := []ProviderUsageRow{}
	for rows.Next() {
		var r ProviderUsageRow
		var id sql.NullInt64
		if err := rows.Scan(&r.Key, &id, &r.Name, &r.Requests, &r.CostUSD, &r.CacheHits); err != nil {
			return nil, err
		}
		r.ID = id.Int64
		if r.Name == "" && r.ID != 0 {
			r.Name = "Deleted (" + strconv.FormatInt(r.ID, 10) + ")"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const serviceAppUsageSQL = `SELECT e.service_id, e.app_id, ` + providerSums + ` FROM events e INDEXED BY events_ts
	WHERE e.ts>=? AND (e.service_id IS NOT NULL OR e.app_id IS NOT NULL) AND e.kind IN ('proxy','lease','cache_hit')`

// serviceAppUsage sums provider usage per service and per app since a time,
// in one pass over the range read through events_ts (left to itself the
// planner skip-scans events_service or events_app and fetches rows out of
// order, which is slower than reading the range). Rows are ordered by cost,
// then requests, then key, as the per service and per app queries were.
func (s *Store) serviceAppUsage(ctx context.Context, since int64, hid string) ([]ProviderUsageRow, []ProviderUsageRow, error) {
	rows, err := s.r.QueryContext(ctx, serviceAppUsageSQL+hid+` GROUP BY 1, 2`, since)
	if err != nil {
		return nil, nil, err
	}
	svc, app := map[int64]*ProviderUsageRow{}, map[int64]*ProviderUsageRow{}
	add := func(m map[int64]*ProviderUsageRow, id int64, r ProviderUsageRow) {
		p := m[id]
		if p == nil {
			p = &ProviderUsageRow{ID: id}
			m[id] = p
		}
		p.Requests += r.Requests
		p.CostUSD += r.CostUSD
		p.CacheHits += r.CacheHits
	}
	for rows.Next() {
		var sid, aid sql.NullInt64
		var r ProviderUsageRow
		if err := rows.Scan(&sid, &aid, &r.Requests, &r.CostUSD, &r.CacheHits); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if sid.Valid {
			add(svc, sid.Int64, r)
		}
		if aid.Valid {
			add(app, aid.Int64, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	finish := func(m map[int64]*ProviderUsageRow, q string, slugKey bool) ([]ProviderUsageRow, error) {
		names := map[int64][2]string{}
		rs, err := s.r.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		for rs.Next() {
			var id int64
			var a, b string
			if err := rs.Scan(&id, &a, &b); err != nil {
				rs.Close()
				return nil, err
			}
			names[id] = [2]string{a, b}
		}
		rs.Close()
		out := []ProviderUsageRow{}
		for id, p := range m {
			n, ok := names[id]
			p.Key = "#" + strconv.FormatInt(id, 10)
			if ok {
				p.Key, p.Name = n[0], n[1]
				if !slugKey {
					p.Key = n[1]
				}
			}
			if p.Name == "" && p.ID != 0 {
				p.Name = "Deleted (" + strconv.FormatInt(p.ID, 10) + ")"
			}
			out = append(out, *p)
		}
		sort.Slice(out, func(i, j int) bool {
			a, b := out[i], out[j]
			// Partial sums added in another order differ in the last bits;
			// costs that close are a tie, as they are in SQL's own sum.
			if d := a.CostUSD - b.CostUSD; d > 1e-9 || d < -1e-9 {
				return a.CostUSD > b.CostUSD
			}
			if a.Requests != b.Requests {
				return a.Requests > b.Requests
			}
			return a.Key < b.Key
		})
		return out, nil
	}
	bySvc, err := finish(svc, `SELECT id, slug, name FROM services`, true)
	if err != nil {
		return nil, nil, err
	}
	byApp, err := finish(app, `SELECT id, name, name FROM apps`, false)
	return bySvc, byApp, err
}
