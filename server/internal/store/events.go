package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
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
	// Data is a notification's structured fields, stored as JSON.
	Data map[string]any
}

func nz(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// maxEventData bounds a notification's stored fields.
const maxEventData = 2000

func (s *Store) AddEvent(ctx context.Context, e NewEvent) {
	e.Detail = truncate(s.Scrub(ctx, e.Detail), 500)
	// The token is noted only on events of the app that made the request.
	var tok int64
	if e.AppID != 0 {
		tok = appTokenFrom(ctx)
	}
	data := ""
	if len(e.Data) > 0 {
		if b, err := json.Marshal(e.Data); err == nil && len(b) <= maxEventData {
			data = s.Scrub(ctx, string(b))
		}
	}
	// Group committed, and kept even when the client has hung up.
	_ = s.gw.write(wstmt{`INSERT INTO events(ts,kind,service_id,key_id,app_id,token_id,status_code,latency_ms,detail,cost,data) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		[]any{now(), e.Kind, nz(e.ServiceID), nz(e.KeyID), nz(e.AppID), nz(tok), e.StatusCode, e.LatencyMS, e.Detail, e.Cost, data}})
}

type EventFilter struct {
	ServiceID int64
	KeyID     int64
	AppID     int64
	TokenID   int64
	Kind      string
	Limit     int
	Since     time.Time
	// AfterID returns the events after this id, oldest first: the way to
	// read events incrementally; a negative id reads from the oldest. Without
	// it the newest come first.
	AfterID int64
	// BeforeID returns the events before this id, newest first: the way to
	// page back through older events.
	BeforeID int64
	// ExcludeHidden leaves out the events of hidden keys in the query
	// itself, so a page is full even for someone who may not see them.
	ExcludeHidden bool
}

const eventCols = `e.id, e.ts, e.kind, e.service_id, COALESCE(s.slug,''), e.key_id, COALESCE(k.label,''), COALESCE(k.hidden,0),
		e.app_id, COALESCE(a.name,''), e.status_code, e.latency_ms, e.detail, e.token_id, e.data`

const eventFrom = ` FROM events e LEFT JOIN services s ON s.id=e.service_id LEFT JOIN keys k ON k.id=e.key_id LEFT JOIN apps a ON a.id=e.app_id`

func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]*Event, error) {
	q := `SELECT ` + eventCols + eventFrom + ` WHERE 1=1`
	args := []any{}
	if f.ServiceID != 0 {
		q += ` AND e.service_id=?`
		args = append(args, f.ServiceID)
	}
	if f.KeyID != 0 {
		q += ` AND e.key_id=?`
		args = append(args, f.KeyID)
	}
	if f.AppID != 0 {
		q += ` AND e.app_id=?`
		args = append(args, f.AppID)
	}
	if f.TokenID != 0 {
		q += ` AND e.token_id=?`
		args = append(args, f.TokenID)
	}
	if f.Kind != "" {
		q += ` AND e.kind=?`
		args = append(args, f.Kind)
	}
	if !f.Since.IsZero() {
		q += ` AND e.ts>=?`
		args = append(args, f.Since.Unix())
	}
	if f.AfterID != 0 {
		q += ` AND e.id>?`
		args = append(args, f.AfterID)
	}
	if f.BeforeID > 0 {
		q += ` AND e.id<?`
		args = append(args, f.BeforeID)
	}
	if f.ExcludeHidden {
		q += notHidden
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	switch {
	case f.AfterID != 0:
		// Reading forward: id order, so the last row is the next cursor.
		q += ` ORDER BY e.id LIMIT ?`
	case f.ServiceID != 0 || f.KeyID != 0:
		// events_service and events_key end in (ts, rowid), so walked
		// backwards they give the newest first without sorting every event
		// of the service or key. Events are appended with the current time,
		// so this is the id order unless the clock stepped back.
		q += ` ORDER BY e.ts DESC, e.id DESC LIMIT ?`
	default:
		q += ` ORDER BY e.id DESC LIMIT ?`
	}
	args = append(args, f.Limit)
	// The filters make this SQL vary, so it is not kept prepared.
	return s.queryEvents(ctx, q, args...)
}

func (s *Store) queryEvents(ctx context.Context, q string, args ...any) ([]*Event, error) {
	rows, err := s.rd.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		var e Event
		var ts int64
		var sid, kid, aid, tid sql.NullInt64
		var hidden int
		var data string
		if err := rows.Scan(&e.ID, &ts, &e.Kind, &sid, &e.ServiceSlug, &kid, &e.KeyLabel, &hidden, &aid, &e.AppName,
			&e.StatusCode, &e.LatencyMS, &e.Detail, &tid, &data); err != nil {
			return nil, err
		}
		e.TS = time.Unix(ts, 0).UTC()
		e.ServiceID, e.KeyID, e.AppID, e.TokenID = toInt(sid), toInt(kid), toInt(aid), toInt(tid)
		e.KeyHidden = hidden == 1
		if data != "" && json.Valid([]byte(data)) {
			e.Data = json.RawMessage(data)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// MaxEventID is the newest event's id, 0 when there is none. A stream that
// starts without a cursor starts after it.
func (s *Store) MaxEventID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT MAX(id) FROM events`).Scan(&id)
	return id.Int64, err
}

// AppEventScope is what an app's event stream may read: its own events,
// apart from notifications not meant for it, and a few notifications about
// the services it can use.
type AppEventScope struct {
	AppID int64
	// OwnNotices are the notification kinds (kinds with a dot) of the app's
	// own events it may read. Its plain events (proxy, lease and the rest)
	// it always reads.
	OwnNotices []string
	// ServiceKinds are the kinds about a service the app reads when the
	// event has no app.
	ServiceKinds []string
	// ServiceIDs are the services it can use; nil means every service.
	ServiceIDs []int64
}

// ListAppEvents returns an app's events after a cursor, oldest first, never
// those of hidden keys.
func (s *Store) ListAppEvents(ctx context.Context, sc AppEventScope, afterID int64, limit int) ([]*Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + eventCols + eventFrom + ` WHERE e.id>? AND ((e.app_id=? AND (instr(e.kind,'.')=0`
	args := []any{afterID, sc.AppID}
	if len(sc.OwnNotices) > 0 {
		q += ` OR e.kind IN (` + placeholders(len(sc.OwnNotices)) + `)`
		for _, k := range sc.OwnNotices {
			args = append(args, k)
		}
	}
	q += `))`
	if len(sc.ServiceKinds) > 0 && (sc.ServiceIDs == nil || len(sc.ServiceIDs) > 0) {
		q += ` OR (e.app_id IS NULL AND e.service_id IS NOT NULL AND e.kind IN (` + placeholders(len(sc.ServiceKinds)) + `)`
		for _, k := range sc.ServiceKinds {
			args = append(args, k)
		}
		if sc.ServiceIDs != nil {
			q += ` AND e.service_id IN (` + placeholders(len(sc.ServiceIDs)) + `)`
			for _, id := range sc.ServiceIDs {
				args = append(args, id)
			}
		}
		q += `)`
	}
	q += `)` + notHidden + ` ORDER BY e.id LIMIT ?`
	args = append(args, limit)
	return s.queryEvents(ctx, q, args...)
}

// CountEvents counts events since a time. Events of hidden keys are counted
// only when includeHidden is set.
func (s *Store) CountEvents(ctx context.Context, since time.Time, includeHidden bool, kinds ...string) (int, error) {
	q := `SELECT COUNT(*) FROM events e WHERE e.ts>=?`
	if !includeHidden {
		q += notHidden
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

// How a request reached the server, recorded on audit rows as via.
const (
	ViaDashboard  = "dashboard"   // a signed in browser session
	ViaAPI        = "api"         // an admin API token, or an app token outside MCP
	ViaCLI        = "cli"         // the acn command line
	ViaControlMCP = "control_mcp" // the control MCP (/mcp/control)
	ViaAppMCP     = "app_mcp"     // an app's MCP (/mcp) with an app token
	ViaAssistant  = "assistant"   // a tool call of the dashboard's Assistant
	ViaOAuth      = "oauth"       // an app's MCP with an OAuth access token
)

// ValidVia reports whether v is one of the via values.
func ValidVia(v string) bool {
	switch v {
	case ViaDashboard, ViaAPI, ViaCLI, ViaControlMCP, ViaAppMCP, ViaAssistant, ViaOAuth:
		return true
	}
	return false
}

// Attribution says who acted beyond the signed in person: how the request
// came in, the token it used, the app and the client program. The auth
// middleware puts it on the request context and AddAudit reads it from
// there, so every audit row carries it without each call site passing it.
type Attribution struct {
	Via string
	// TokenID is the admin API token for api, cli and control_mcp, and the
	// app token for app calls and app_mcp. 0 for a session or OAuth.
	TokenID int64
	AppID   int64
	// Client is the MCP client's name and version from initialize, or a
	// trimmed User-Agent.
	Client string
}

type attributionKey struct{}

// WithAttribution returns ctx carrying a.
func WithAttribution(ctx context.Context, a Attribution) context.Context {
	return context.WithValue(ctx, attributionKey{}, a)
}

// AttributionFrom returns the attribution on ctx, zero when there is none
// (the server's own background work).
func AttributionFrom(ctx context.Context) Attribution {
	a, _ := ctx.Value(attributionKey{}).(Attribution)
	return a
}

// ClientName trims a client name (a User-Agent or MCP clientInfo) to
// something short and printable.
func ClientName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	return truncate(s, 120)
}

// AddAudit writes an audit row and ignores a failure. Use AddAuditErr where
// the action must not go ahead without its row.
func (s *Store) AddAudit(ctx context.Context, u *User, action, target, detail string) {
	_ = s.AddAuditErr(ctx, u, action, target, detail)
}

// AddAuditErr writes an audit row and reports whether it was stored. A
// secret shown to a person is returned only after this succeeds.
func (s *Store) AddAuditErr(ctx context.Context, u *User, action, target, detail string) error {
	var uid any
	email := "system"
	if u != nil {
		uid, email = u.ID, u.Email
	}
	at := AttributionFrom(ctx)
	_, err := s.w.ExecContext(ctx, `INSERT INTO audit(ts,user_id,user_email,action,target,detail,token_id,app_id,via,client) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		now(), uid, email, action, s.Scrub(ctx, target), s.Scrub(ctx, detail), nz(at.TokenID), nz(at.AppID), at.Via, s.Scrub(ctx, ClientName(at.Client)))
	return err
}

// AuditFilter narrows the audit log. The zero value is the newest rows.
type AuditFilter struct {
	Limit    int
	AfterID  int64 // rows after this id, oldest first (negative: from the oldest)
	BeforeID int64 // rows before this id, newest first
	Since    time.Time
	Via      string
	TokenID  int64
	AppID    int64
	Client   string // a substring of the client, any case
	Action   string
	// Target matches the target exactly, or a target under it
	// ("<target>/..."), the way a service's keys are named.
	Target string
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]*AuditEntry, error) {
	return s.ListAuditFiltered(ctx, AuditFilter{Limit: limit})
}

// ListAuditFiltered lists audit rows, up to 1000 per call.
func (s *Store) ListAuditFiltered(ctx context.Context, f AuditFilter) ([]*AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	q := `SELECT id, ts, user_email, action, target, detail, token_id, app_id, via, client FROM audit WHERE 1=1`
	args := []any{}
	add := func(cond string, v any) {
		q += ` AND ` + cond
		args = append(args, v)
	}
	if f.AfterID != 0 {
		add(`id>?`, f.AfterID)
	}
	if f.BeforeID > 0 {
		add(`id<?`, f.BeforeID)
	}
	if !f.Since.IsZero() {
		add(`ts>=?`, f.Since.Unix())
	}
	if f.Via != "" {
		add(`via=?`, f.Via)
	}
	if f.TokenID != 0 {
		add(`token_id=?`, f.TokenID)
	}
	if f.AppID != 0 {
		add(`app_id=?`, f.AppID)
	}
	if f.Client != "" {
		add(`instr(lower(client), lower(?))>0`, f.Client)
	}
	if f.Action != "" {
		add(`action=?`, f.Action)
	}
	if f.Target != "" {
		q += ` AND (target=? OR substr(target,1,?)=?)`
		args = append(args, f.Target, len(f.Target)+1, f.Target+"/")
	}
	if f.AfterID != 0 {
		q += ` ORDER BY id LIMIT ?`
	} else {
		q += ` ORDER BY id DESC LIMIT ?`
	}
	args = append(args, f.Limit)
	rows, err := s.rd.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		var ts int64
		var tid, aid sql.NullInt64
		if err := rows.Scan(&a.ID, &ts, &a.UserEmail, &a.Action, &a.Target, &a.Detail, &tid, &aid, &a.Via, &a.Client); err != nil {
			return nil, err
		}
		a.TS = time.Unix(ts, 0).UTC()
		a.TokenID, a.AppID = toInt(tid), toInt(aid)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// migrateObservability adds who-did-what to the audit log, structured data
// to events, the notification dedupe table and the jobs table.
func migrateObservability(db *sql.DB) error {
	for _, c := range []struct{ col, def string }{
		{"token_id", "INTEGER"}, {"app_id", "INTEGER"},
		{"via", "TEXT NOT NULL DEFAULT ''"}, {"client", "TEXT NOT NULL DEFAULT ''"},
	} {
		if _, err := addColumn(db, "audit", c.col, c.def); err != nil {
			return err
		}
	}
	if _, err := addColumn(db, "events", "data", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS notify_once(k TEXT PRIMARY KEY, ts INTEGER NOT NULL)`); err != nil {
		return err
	}
	return migrateJobs(db)
}

// MarkOnce records k and reports whether it is new. Notifications that must
// go out once (a share's 24 hour warning, an app's 80% warning for a period)
// use it, so a restart does not send them again.
func (s *Store) MarkOnce(ctx context.Context, k string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO notify_once(k, ts) VALUES(?,?)`, k, now())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CoolingKey is a key resting after a rate limit.
type CoolingKey struct {
	ID          int64
	ServiceID   int64
	ServiceSlug string
	Label       string
	Until       time.Time
}

// CoolingKeys lists the active keys resting at t. Hidden keys are left out:
// their notifications would tell people who may not see them that they exist.
func (s *Store) CoolingKeys(ctx context.Context, t time.Time) ([]CoolingKey, error) {
	rows, err := s.rd.QueryContext(ctx, `SELECT k.id, k.service_id, sv.slug, k.label, k.cooldown_until FROM keys k
		JOIN services sv ON sv.id=k.service_id WHERE k.cooldown_until>? AND k.status=? AND k.hidden=0`, t.Unix(), StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CoolingKey{}
	for rows.Next() {
		var c CoolingKey
		var until int64
		if err := rows.Scan(&c.ID, &c.ServiceID, &c.ServiceSlug, &c.Label, &until); err != nil {
			return nil, err
		}
		c.Until = time.Unix(until, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// PruneMarks forgets marks older than d.
func (s *Store) PruneMarks(ctx context.Context, d time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM notify_once WHERE ts<?`, time.Now().Add(-d).Unix())
	return err
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
