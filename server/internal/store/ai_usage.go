package store

import (
	"context"
	"database/sql"
	"fmt"
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

// notHidden leaves out events of hidden keys, as SpendSince and CountEvents do.
const notHidden = ` AND NOT EXISTS (SELECT 1 FROM keys k WHERE k.id=e.key_id AND k.hidden=1)`

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

	windows := []struct {
		name string
		n    int
	}{{"today", 1}, {"7d", 7}, {"30d", 30}, {"range", days}}
	for _, w := range windows {
		since := dayStart(at, w.n).Unix()
		var t UsageTotals
		if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(cost_usd),0) FROM ai_usage WHERE ts>=?`, since).
			Scan(&t.AICalls, &t.AICostUSD); err != nil {
			return nil, err
		}
		if err := s.r.QueryRowContext(ctx, `SELECT `+providerSums+` FROM events e WHERE e.ts>=?`+hid, since).
			Scan(&t.Requests, &t.ProviderCostUSD, &t.CacheHits); err != nil {
			return nil, err
		}
		rep.Totals[w.name] = t
	}

	var err error
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

	provDays := map[string]ProviderUsageRow{}
	rows, err := s.provRows(ctx, `SELECT strftime('%Y-%m-%d', e.ts, 'unixepoch'), 0, '', `+providerSums+
		` FROM events e WHERE e.ts>=?`+hid+` GROUP BY 1`, since)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		provDays[r.Key] = r
	}
	// Every day of the range is present so charts need no gap filling.
	for d := 0; d < days; d++ {
		day := dayOf(from.AddDate(0, 0, d))
		a := aiDays[day]
		a.Key = day
		rep.AI.ByDay = append(rep.AI.ByDay, a)
		p := provDays[day]
		p.Key = day
		rep.Provider.ByDay = append(rep.Provider.ByDay, p)
	}

	// Only services and apps that saw traffic in the range are listed.
	if rep.Provider.ByService, err = s.provRows(ctx, `SELECT COALESCE(s.slug, '#'||e.service_id), e.service_id, COALESCE(s.name,''), `+providerSums+
		` FROM events e LEFT JOIN services s ON s.id=e.service_id WHERE e.ts>=? AND e.service_id IS NOT NULL
		AND e.kind IN ('proxy','lease','cache_hit')`+hid+` GROUP BY e.service_id ORDER BY 5 DESC, 4 DESC, 1`, since); err != nil {
		return nil, err
	}
	if rep.Provider.ByApp, err = s.provRows(ctx, `SELECT COALESCE(a.name, '#'||e.app_id), e.app_id, COALESCE(a.name,''), `+providerSums+
		` FROM events e LEFT JOIN apps a ON a.id=e.app_id WHERE e.ts>=? AND e.app_id IS NOT NULL
		AND e.kind IN ('proxy','lease','cache_hit')`+hid+` GROUP BY e.app_id ORDER BY 5 DESC, 4 DESC, 1`, since); err != nil {
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
