package store

import (
	"context"
	"database/sql"
	"time"
)

// Token usage: input and output tokens an LLM provider reported in its
// answers, per app, service and UTC day. The proxy reads them from the
// response body (see api/tokenmeter.go); they outlive event retention.

const schemaTokenUsage = `
CREATE TABLE IF NOT EXISTS token_usage(
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL,
  day TEXT NOT NULL,
  input INTEGER NOT NULL DEFAULT 0,
  output INTEGER NOT NULL DEFAULT 0,
  calls INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(app_id, service_id, day)
);
`

func migrateTokenUsage(db *sql.DB) error {
	_, err := db.Exec(schemaTokenUsage)
	return err
}

// RecordTokens adds one metered answer's tokens. Group committed like
// app_usage, and kept when the client hangs up.
func (s *Store) RecordTokens(_ context.Context, appID, serviceID, input, output int64) error {
	if appID == 0 || serviceID == 0 || (input <= 0 && output <= 0) {
		return nil
	}
	return s.gw.write(wstmt{`INSERT INTO token_usage(app_id, service_id, day, input, output, calls) VALUES(?,?,?,?,?,1)
		ON CONFLICT(app_id, service_id, day) DO UPDATE SET input=input+excluded.input, output=output+excluded.output, calls=calls+1`,
		[]any{appID, serviceID, dayOf(time.Now()), max(input, 0), max(output, 0)}})
}

// TokenDay is one app's tokens on one service and day.
type TokenDay struct {
	ServiceID int64  `json:"service_id"`
	Day       string `json:"day"`
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
	Calls     int64  `json:"calls"`
}

// AppTokenDays lists an app's token usage since a day (YYYY-MM-DD).
func (s *Store) AppTokenDays(ctx context.Context, appID int64, sinceDay string) ([]TokenDay, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT service_id, day, input, output, calls FROM token_usage WHERE app_id=? AND day>=? ORDER BY day`, appID, sinceDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TokenDay{}
	for rows.Next() {
		var d TokenDay
		if err := rows.Scan(&d.ServiceID, &d.Day, &d.Input, &d.Output, &d.Calls); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActivityRow is one app's proxied traffic on one service, UTC day and
// token, from the event log.
type ActivityRow struct {
	ServiceID   int64
	ServiceSlug string
	Day         string
	TokenID     int64
	Requests    int64
	CacheHits   int64
	Errors      int64
	Cost        float64
	LatencySum  int64
	LatencyN    int64
	First, Last time.Time
}

const appActivitySQL = `SELECT COALESCE(e.service_id,0), COALESCE(sv.slug,''), strftime('%Y-%m-%d', e.ts, 'unixepoch'), COALESCE(e.token_id,0),
	SUM(e.kind IN ('proxy','cache_hit','lease')), SUM(e.kind='cache_hit'),
	SUM((e.kind='proxy' AND e.status_code>=400) OR e.kind='error'),
	COALESCE(SUM(e.cost),0), COALESCE(SUM(CASE WHEN e.kind='proxy' THEN e.latency_ms END),0), SUM(e.kind='proxy'),
	MIN(e.ts), MAX(e.ts)
	FROM events e LEFT JOIN services sv ON sv.id=e.service_id
	WHERE e.app_id=? AND e.ts>=? AND e.kind IN ('proxy','cache_hit','lease','error')
	GROUP BY 1, 3, 4`

// AppActivity groups an app's request events since a time by service, day
// and token. It reads only what event retention kept.
func (s *Store) AppActivity(ctx context.Context, appID int64, since time.Time) ([]ActivityRow, error) {
	rows, err := s.r.QueryContext(ctx, appActivitySQL, appID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityRow{}
	for rows.Next() {
		var a ActivityRow
		var first, last int64
		if err := rows.Scan(&a.ServiceID, &a.ServiceSlug, &a.Day, &a.TokenID, &a.Requests, &a.CacheHits, &a.Errors, &a.Cost,
			&a.LatencySum, &a.LatencyN, &first, &last); err != nil {
			return nil, err
		}
		a.First, a.Last = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}
