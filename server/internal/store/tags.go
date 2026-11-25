package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Caller tags: a caller labels a request with X-Acnologia-Meta
// (customer=cust_A,workspace=w1). Usage is recorded per tag value, and an
// app can give every value of a key its own budget ("each customer at most
// $2 a day"), enforced by reservation like the app's own limits. A token
// can be pinned to tag values, so its holder cannot retag to escape one.

// MaxTags is the most pairs a request may carry, and a token may pin.
const MaxTags = 5

const (
	maxTagKey   = 32
	maxTagValue = 64
	maxMetaLen  = 512
	// MaxTagBudgets is the most tag budgets an app may define.
	MaxTagBudgets = 10
)

// TagBudget caps the use of each value of one tag key: with key "customer"
// and a daily budget of 2, every customer gets $2 a day of its own.
type TagBudget struct {
	Key string `json:"key"`
	AppLimits
}

const schemaTags = `
CREATE TABLE IF NOT EXISTS tag_usage(
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  tag_key TEXT NOT NULL,
  tag_value TEXT NOT NULL,
  day TEXT NOT NULL,
  requests INTEGER NOT NULL DEFAULT 0,
  cost REAL NOT NULL DEFAULT 0,
  PRIMARY KEY(app_id, tag_key, tag_value, day)
);
CREATE TABLE IF NOT EXISTS app_token_usage(
  token_id INTEGER NOT NULL,
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  day TEXT NOT NULL,
  requests INTEGER NOT NULL DEFAULT 0,
  cost REAL NOT NULL DEFAULT 0,
  PRIMARY KEY(token_id, day)
);
CREATE INDEX IF NOT EXISTS app_token_usage_app ON app_token_usage(app_id);
CREATE INDEX IF NOT EXISTS app_usage_day ON app_usage(day);
`

// migrateAccessControls adds per-token limits and pinned tags, the app's
// per-token default and tag budgets, tag and token usage, and deny rules.
func migrateAccessControls(db *sql.DB) error {
	if _, err := db.Exec(schemaTags + schemaDeny); err != nil {
		return err
	}
	for _, c := range []struct{ table, col string }{
		{"app_tokens", "limits"}, {"app_tokens", "pinned_tags"},
		{"apps", "token_limits"}, {"apps", "tag_budgets"},
	} {
		if _, err := addColumn(db, c.table, c.col, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	return nil
}

func tagKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

func tagValueChar(c byte) bool {
	return tagKeyChar(c) || c >= 'A' && c <= 'Z' || c == ':' || c == '@' || c == '/' || c == '+'
}

// ValidTagKey reports whether k is a tag key: 1 to 32 of a-z 0-9 _ - .
func ValidTagKey(k string) bool {
	if k == "" || len(k) > maxTagKey {
		return false
	}
	for i := 0; i < len(k); i++ {
		if !tagKeyChar(k[i]) {
			return false
		}
	}
	return true
}

// ValidTagValue reports whether v is a tag value: 1 to 64 of letters,
// digits and _ - . : @ / +.
func ValidTagValue(v string) bool {
	if v == "" || len(v) > maxTagValue {
		return false
	}
	for i := 0; i < len(v); i++ {
		if !tagValueChar(v[i]) {
			return false
		}
	}
	return true
}

// ParseMeta reads an X-Acnologia-Meta value: up to five key=value pairs
// separated by commas. Keys are lowercased; a repeated key is an error.
func ParseMeta(h string) (map[string]string, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, nil
	}
	if len(h) > maxMetaLen {
		return nil, errors.New("X-Acnologia-Meta is too long")
	}
	parts := strings.Split(h, ",")
	if len(parts) > MaxTags {
		return nil, fmt.Errorf("X-Acnologia-Meta takes at most %d pairs", MaxTags)
	}
	out := make(map[string]string, len(parts))
	for _, p := range parts {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if !ok || !ValidTagKey(k) || !ValidTagValue(v) {
			return nil, errors.New("X-Acnologia-Meta pairs look like customer=cust_A: keys of a-z 0-9 _ - . up to 32 characters, values of letters, digits and _ - . : @ / + up to 64")
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("X-Acnologia-Meta repeats %q", k)
		}
		out[k] = v
	}
	return out, nil
}

// ValidateTags checks a set of tags pinned to a token.
func ValidateTags(t map[string]string) error {
	if len(t) > MaxTags {
		return fmt.Errorf("at most %d tags", MaxTags)
	}
	for k, v := range t {
		if !ValidTagKey(k) {
			return fmt.Errorf("tag key %q must be 1 to 32 of a-z 0-9 _ - .", k)
		}
		if !ValidTagValue(v) {
			return fmt.Errorf("tag value %q must be 1 to 64 of letters, digits and _ - . : @ / +", v)
		}
	}
	return nil
}

// FormatTags writes tags as the header does, keys sorted.
func FormatTags(t map[string]string) string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + t[k]
	}
	return strings.Join(parts, ",")
}

// ValidateTagBudgets checks an app's tag budgets: known key syntax, one
// budget per key, at least one limit each, none negative.
func ValidateTagBudgets(bs []TagBudget) error {
	if len(bs) > MaxTagBudgets {
		return fmt.Errorf("at most %d tag budgets", MaxTagBudgets)
	}
	seen := map[string]bool{}
	for _, b := range bs {
		if !ValidTagKey(b.Key) {
			return fmt.Errorf("tag key %q must be 1 to 32 of a-z 0-9 _ - .", b.Key)
		}
		if seen[b.Key] {
			return fmt.Errorf("tag key %q has two budgets", b.Key)
		}
		seen[b.Key] = true
		if !b.Any() {
			return fmt.Errorf("the budget for %q sets no limit", b.Key)
		}
		if err := b.AppLimits.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func encodeJSONOrEmpty(v any, empty bool) (string, error) {
	if empty {
		return "", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func decodeTagBudgets(s string) ([]TagBudget, error) {
	if s == "" {
		return nil, nil
	}
	var out []TagBudget
	err := json.Unmarshal([]byte(s), &out)
	return out, err
}

func decodeTags(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	var out map[string]string
	err := json.Unmarshal([]byte(s), &out)
	return out, err
}

func decodeLimits(s string) (AppLimits, error) {
	var l AppLimits
	if s == "" {
		return l, nil
	}
	err := json.Unmarshal([]byte(s), &l)
	return l, err
}

// GetTagBudgets returns an app's tag budgets.
func (s *Store) GetTagBudgets(ctx context.Context, appID int64) ([]TagBudget, error) {
	var raw string
	if err := s.r.QueryRowContext(ctx, `SELECT tag_budgets FROM apps WHERE id=?`, appID).Scan(&raw); err != nil {
		return nil, notFound(err)
	}
	bs, err := decodeTagBudgets(raw)
	if bs == nil {
		bs = []TagBudget{}
	}
	return bs, err
}

// SetTagBudgets replaces an app's tag budgets.
func (s *Store) SetTagBudgets(ctx context.Context, appID int64, bs []TagBudget) error {
	raw, err := encodeJSONOrEmpty(bs, len(bs) == 0)
	if err != nil {
		return err
	}
	return execOne(s.DB.ExecContext(ctx, `UPDATE apps SET tag_budgets=? WHERE id=?`, raw, appID))
}

// GetTokenDefaultLimits returns the limits every token of the app gets
// unless the token sets its own.
func (s *Store) GetTokenDefaultLimits(ctx context.Context, appID int64) (AppLimits, error) {
	var raw string
	if err := s.r.QueryRowContext(ctx, `SELECT token_limits FROM apps WHERE id=?`, appID).Scan(&raw); err != nil {
		return AppLimits{}, notFound(err)
	}
	return decodeLimits(raw)
}

// SetTokenDefaultLimits saves the app's per-token default limits.
func (s *Store) SetTokenDefaultLimits(ctx context.Context, appID int64, l AppLimits) error {
	raw, err := encodeJSONOrEmpty(l, !l.Any())
	if err != nil {
		return err
	}
	return execOne(s.DB.ExecContext(ctx, `UPDATE apps SET token_limits=? WHERE id=?`, raw, appID))
}

func execOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetTagUsage is one tag value's use today and this month.
func (s *Store) GetTagUsage(ctx context.Context, appID int64, key, value string, at time.Time) (AppUsage, error) {
	var u AppUsage
	today := dayOf(at)
	err := s.r.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN day=? THEN requests END),0), COALESCE(SUM(CASE WHEN day=? THEN cost END),0),
		COALESCE(SUM(requests),0), COALESCE(SUM(cost),0)
		FROM tag_usage WHERE app_id=? AND tag_key=? AND tag_value=? AND day>=? AND day<=?`,
		today, today, appID, key, value, today[:7]+"-01", today).
		Scan(&u.TodayRequests, &u.TodayCost, &u.MonthRequests, &u.MonthCost)
	return u, err
}

// TagUsageRow is one tag value's use: today, this month and over the
// window asked for.
type TagUsageRow struct {
	Key           string  `json:"key"`
	Value         string  `json:"value"`
	TodayRequests int64   `json:"today_requests"`
	TodayCost     float64 `json:"today_cost"`
	MonthRequests int64   `json:"month_requests"`
	MonthCost     float64 `json:"month_cost"`
	Requests      int64   `json:"requests"`
	Cost          float64 `json:"cost"`
}

// TagUsage lists an app's use per tag value over the last days days (and
// the current month), the most expensive first, at most limit rows.
func (s *Store) TagUsage(ctx context.Context, appID int64, days, limit int, at time.Time) ([]TagUsageRow, error) {
	today := dayOf(at)
	month := today[:7] + "-01"
	since := dayOf(at.AddDate(0, 0, -days+1))
	from := min(since, month)
	rows, err := s.r.QueryContext(ctx, `SELECT tag_key, tag_value,
		COALESCE(SUM(CASE WHEN day=? THEN requests END),0), COALESCE(SUM(CASE WHEN day=? THEN cost END),0),
		COALESCE(SUM(CASE WHEN day>=? THEN requests END),0), COALESCE(SUM(CASE WHEN day>=? THEN cost END),0),
		COALESCE(SUM(CASE WHEN day>=? THEN requests END),0), COALESCE(SUM(CASE WHEN day>=? THEN cost END),0)
		FROM tag_usage WHERE app_id=? AND day>=? AND day<=?
		GROUP BY tag_key, tag_value ORDER BY 8 DESC, 7 DESC, tag_key, tag_value LIMIT ?`,
		today, today, month, month, since, since, appID, from, today, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagUsageRow{}
	for rows.Next() {
		var t TagUsageRow
		if err := rows.Scan(&t.Key, &t.Value, &t.TodayRequests, &t.TodayCost, &t.MonthRequests, &t.MonthCost, &t.Requests, &t.Cost); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// usageRetentionDays is how many days of caller tag and token usage Prune
// keeps: usage by tag reads at most 366 days back, which also covers the
// current month every monthly budget counts, and one day spare.
const usageRetentionDays = 367

// pruneTagUsage deletes tag and token usage rows older than
// usageRetentionDays.
func (s *Store) pruneTagUsage(ctx context.Context, now time.Time) error {
	cut := dayOf(now.AddDate(0, 0, -usageRetentionDays))
	for _, table := range []string{"tag_usage", "app_token_usage", "oauth_grant_usage"} {
		if err := s.pruneBatched(ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT rowid FROM `+table+` WHERE day<? LIMIT ?)`, cut); err != nil {
			return err
		}
	}
	return nil
}

// migrateOAuthGrantUsage adds per OAuth grant usage, so each grant (an MCP
// client signed in with OAuth) is held to the app's per-token default like
// an app token is.
func migrateOAuthGrantUsage(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS oauth_grant_usage(
  grant_id TEXT NOT NULL,
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  day TEXT NOT NULL,
  requests INTEGER NOT NULL DEFAULT 0,
  cost REAL NOT NULL DEFAULT 0,
  PRIMARY KEY(grant_id, day)
);
CREATE INDEX IF NOT EXISTS oauth_grant_usage_app ON oauth_grant_usage(app_id);`)
	return err
}

// GetGrantUsage is one OAuth grant's use today and this month.
func (s *Store) GetGrantUsage(ctx context.Context, grantID string, at time.Time) (AppUsage, error) {
	var u AppUsage
	today := dayOf(at)
	err := s.r.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN day=? THEN requests END),0), COALESCE(SUM(CASE WHEN day=? THEN cost END),0),
		COALESCE(SUM(requests),0), COALESCE(SUM(cost),0)
		FROM oauth_grant_usage WHERE grant_id=? AND day>=? AND day<=?`,
		today, today, grantID, today[:7]+"-01", today).
		Scan(&u.TodayRequests, &u.TodayCost, &u.MonthRequests, &u.MonthCost)
	return u, err
}

// GetTokenUsage is one app token's use today and this month.
func (s *Store) GetTokenUsage(ctx context.Context, tokenID int64, at time.Time) (AppUsage, error) {
	var u AppUsage
	today := dayOf(at)
	err := s.r.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN day=? THEN requests END),0), COALESCE(SUM(CASE WHEN day=? THEN cost END),0),
		COALESCE(SUM(requests),0), COALESCE(SUM(cost),0)
		FROM app_token_usage WHERE token_id=? AND day>=? AND day<=?`,
		today, today, tokenID, today[:7]+"-01", today).
		Scan(&u.TodayRequests, &u.TodayCost, &u.MonthRequests, &u.MonthCost)
	return u, err
}

// AdmissionLimits is everything admission checks a request against, read
// in one statement: the app's limits, the token's (its own fields over the
// app's per-token default), the token's pinned tags and the app's tag
// budgets. TokenDefault is the per-token default alone, which an OAuth
// grant gets as its own limits.
type AdmissionLimits struct {
	App          AppLimits
	Token        AppLimits
	TokenDefault AppLimits
	Pinned       map[string]string
	TagBudgets   []TagBudget
}

// GetAdmissionLimits reads the limits a request of app appID made with
// token tokenID (0 for none) is admitted under.
func (s *Store) GetAdmissionLimits(ctx context.Context, appID, tokenID int64) (AdmissionLimits, error) {
	var out AdmissionLimits
	var dr, mr sql.NullInt64
	var db, mb sql.NullFloat64
	var tokDefault, budgets, tokLimits, pinned string
	err := s.r.QueryRowContext(ctx, `SELECT a.daily_requests, a.monthly_requests, a.daily_budget, a.monthly_budget,
		a.token_limits, a.tag_budgets, COALESCE(t.limits,''), COALESCE(t.pinned_tags,'')
		FROM apps a LEFT JOIN app_tokens t ON t.id=? AND t.app_id=a.id WHERE a.id=?`, tokenID, appID).
		Scan(&dr, &mr, &db, &mb, &tokDefault, &budgets, &tokLimits, &pinned)
	if err == sql.ErrNoRows {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.App.DailyRequests, out.App.MonthlyRequests = toInt(dr), toInt(mr)
	if db.Valid {
		out.App.DailyBudget = &db.Float64
	}
	if mb.Valid {
		out.App.MonthlyBudget = &mb.Float64
	}
	if out.TagBudgets, err = decodeTagBudgets(budgets); err != nil {
		return out, err
	}
	if out.TokenDefault, err = decodeLimits(tokDefault); err != nil {
		return out, err
	}
	if tokenID == 0 {
		return out, nil
	}
	own, err := decodeLimits(tokLimits)
	if err != nil {
		return out, err
	}
	out.Token = own.Over(out.TokenDefault)
	out.Pinned, err = decodeTags(pinned)
	return out, err
}

// usageScope rides on a request's context from authentication to the
// recording of its usage: the token it was made with, the OAuth grant when
// it came with an OAuth access token, and the caller tags admission settled
// on.
type usageScope struct {
	tokenID int64
	mu      sync.Mutex
	grantID string
	tags    map[string]string
}

func scopeFrom(ctx context.Context) *usageScope {
	sc, _ := ctx.Value(appTokenCtxKey{}).(*usageScope)
	return sc
}

// SetUsageTags sets the caller tags of the request ctx belongs to. Its
// usage is then recorded under each of them too.
func SetUsageTags(ctx context.Context, tags map[string]string) {
	if sc := scopeFrom(ctx); sc != nil {
		sc.mu.Lock()
		sc.tags = tags
		sc.mu.Unlock()
	}
}

// SetUsageGrant marks the request ctx belongs to as made with an OAuth
// access token of grant grantID. Its usage then counts toward the grant.
func SetUsageGrant(ctx context.Context, grantID string) {
	if sc := scopeFrom(ctx); sc != nil {
		sc.mu.Lock()
		sc.grantID = grantID
		sc.mu.Unlock()
	}
}

// UsageTags returns the caller tags set on ctx's request, or nil.
func UsageTags(ctx context.Context) map[string]string {
	sc := scopeFrom(ctx)
	if sc == nil {
		return nil
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.tags
}

// scopeStmts are the token and tag usage rows of one served request.
func scopeStmts(ctx context.Context, appID int64, day string, cost float64) []wstmt {
	sc := scopeFrom(ctx)
	if sc == nil {
		return nil
	}
	var out []wstmt
	if sc.tokenID != 0 {
		out = append(out, wstmt{`INSERT INTO app_token_usage(token_id, app_id, day, requests, cost) VALUES(?,?,?,1,?)
			ON CONFLICT(token_id, day) DO UPDATE SET requests=requests+1, cost=cost+excluded.cost`,
			[]any{sc.tokenID, appID, day, cost}})
	}
	sc.mu.Lock()
	tags, grantID := sc.tags, sc.grantID
	sc.mu.Unlock()
	if grantID != "" {
		out = append(out, wstmt{`INSERT INTO oauth_grant_usage(grant_id, app_id, day, requests, cost) VALUES(?,?,?,1,?)
			ON CONFLICT(grant_id, day) DO UPDATE SET requests=requests+1, cost=cost+excluded.cost`,
			[]any{grantID, appID, day, cost}})
	}
	for k, v := range tags {
		out = append(out, wstmt{`INSERT INTO tag_usage(app_id, tag_key, tag_value, day, requests, cost) VALUES(?,?,?,?,1,?)
			ON CONFLICT(app_id, tag_key, tag_value, day) DO UPDATE SET requests=requests+1, cost=cost+excluded.cost`,
			[]any{appID, k, v, day, cost}})
	}
	return out
}
