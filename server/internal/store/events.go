package store

import (
	"context"
	"database/sql"
	"time"
	"unicode/utf8"
)

type NewEvent struct {
	Kind       string
	ServiceID  int64
	KeyID      int64
	AppID      int64
	StatusCode int
	LatencyMS  int64
	Detail     string
	// Cost is the USD price of the upstream call, 0 when free or unknown.
	Cost float64
}

func nz(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *Store) AddEvent(ctx context.Context, e NewEvent) {
	e.Detail = truncate(s.Scrub(ctx, e.Detail), 500)
	// The token is noted only on events of the app that made the request.
	var tok int64
	if e.AppID != 0 {
		tok = appTokenFrom(ctx)
	}
	// Group committed, and kept even when the client has hung up.
	_ = s.gw.write(wstmt{`INSERT INTO events(ts,kind,service_id,key_id,app_id,token_id,status_code,latency_ms,detail,cost) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		[]any{now(), e.Kind, nz(e.ServiceID), nz(e.KeyID), nz(e.AppID), nz(tok), e.StatusCode, e.LatencyMS, e.Detail, e.Cost}})
}

type EventFilter struct {
	ServiceID int64
	KeyID     int64
	Kind      string
	Limit     int
	Since     time.Time
}

func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]*Event, error) {
	q := `SELECT e.id, e.ts, e.kind, e.service_id, COALESCE(s.slug,''), e.key_id, COALESCE(k.label,''), COALESCE(k.hidden,0),
		e.app_id, COALESCE(a.name,''), e.status_code, e.latency_ms, e.detail
		FROM events e LEFT JOIN services s ON s.id=e.service_id LEFT JOIN keys k ON k.id=e.key_id LEFT JOIN apps a ON a.id=e.app_id WHERE 1=1`
	args := []any{}
	if f.ServiceID != 0 {
		q += ` AND e.service_id=?`
		args = append(args, f.ServiceID)
	}
	if f.KeyID != 0 {
		q += ` AND e.key_id=?`
		args = append(args, f.KeyID)
	}
	if f.Kind != "" {
		q += ` AND e.kind=?`
		args = append(args, f.Kind)
	}
	if !f.Since.IsZero() {
		q += ` AND e.ts>=?`
		args = append(args, f.Since.Unix())
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q += ` ORDER BY e.id DESC LIMIT ?`
	args = append(args, f.Limit)
	// The filters make this SQL vary, so it is not kept prepared.
	rows, err := s.rd.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		var e Event
		var ts int64
		var sid, kid, aid sql.NullInt64
		var hidden int
		if err := rows.Scan(&e.ID, &ts, &e.Kind, &sid, &e.ServiceSlug, &kid, &e.KeyLabel, &hidden, &aid, &e.AppName,
			&e.StatusCode, &e.LatencyMS, &e.Detail); err != nil {
			return nil, err
		}
		e.TS = time.Unix(ts, 0).UTC()
		e.ServiceID, e.KeyID, e.AppID = toInt(sid), toInt(kid), toInt(aid)
		e.KeyHidden = hidden == 1
		out = append(out, &e)
	}
	return out, rows.Err()
}

// CountEvents counts events since a time. Events of hidden keys are counted
// only when includeHidden is set.
func (s *Store) CountEvents(ctx context.Context, since time.Time, includeHidden bool, kinds ...string) (int, error) {
	q := `SELECT COUNT(*) FROM events e WHERE e.ts>=?`
	if !includeHidden {
		q += ` AND NOT EXISTS (SELECT 1 FROM keys k WHERE k.id=e.key_id AND k.hidden=1)`
	}
	args := []any{since.Unix()}
	if len(kinds) > 0 {
		q += ` AND e.kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	var n int
	err := s.rd.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func (s *Store) AddAudit(ctx context.Context, u *User, action, target, detail string) {
	var uid any
	email := "system"
	if u != nil {
		uid, email = u.ID, u.Email
	}
	_, _ = s.w.ExecContext(ctx, `INSERT INTO audit(ts,user_id,user_email,action,target,detail) VALUES(?,?,?,?,?,?)`,
		now(), uid, email, action, s.Scrub(ctx, target), s.Scrub(ctx, detail))
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]*AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, ts, user_email, action, target, detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		var ts int64
		if err := rows.Scan(&a.ID, &ts, &a.UserEmail, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, err
		}
		a.TS = time.Unix(ts, 0).UTC()
		out = append(out, &a)
	}
	return out, rows.Err()
}

type DayStat struct {
	Day         string
	Requests    int
	Errors      int
	RateLimited int
}

const keyDailyStatsSQL = `SELECT date(ts,'unixepoch') d,
		SUM(CASE WHEN kind='lease' OR (kind='proxy' AND status_code<400) THEN 1 ELSE 0 END),
		SUM(CASE WHEN kind IN ('error','invalid','banned') THEN 1 ELSE 0 END),
		SUM(CASE WHEN kind IN ('rate_limited','busy','quota_exhausted') THEN 1 ELSE 0 END)
		FROM events WHERE key_id=? AND ts>=? GROUP BY d ORDER BY d`

// KeyDailyStats counts a key's events per UTC day.
func (s *Store) KeyDailyStats(ctx context.Context, keyID int64, days int) ([]DayStat, error) {
	rows, err := s.r.QueryContext(ctx, keyDailyStatsSQL, keyID, time.Now().AddDate(0, 0, -days).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayStat{}
	for rows.Next() {
		var d DayStat
		if err := rows.Scan(&d.Day, &d.Requests, &d.Errors, &d.RateLimited); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// truncate cuts s to at most n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
