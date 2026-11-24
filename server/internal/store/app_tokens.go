package store

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// AppToken is one of an app's tokens. An app can hold several, each named
// and revocable on its own. Usage and spend count toward the app and the
// token; a token may carry limits of its own on top of the app's, and be
// pinned to caller tags. Only the SHA-256 of the whole token is stored.
type AppToken struct {
	ID             int64      `json:"id"`
	AppID          int64      `json:"app_id"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	CreatedAt      time.Time  `json:"created_at"`
	CreatedBy      *int64     `json:"created_by"`
	CreatedByEmail string     `json:"created_by_email"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	ReadOnly       bool       `json:"read_only"`
	// Limits are the token's own; an unset field falls back to the app's
	// per-token default. The app's limits apply as well.
	Limits AppLimits `json:"limits"`
	// PinnedTags go on every call made with the token; a call that sends
	// another value for one of these keys is refused.
	PinnedTags map[string]string `json:"pinned_tags"`
}

// Usable reports whether the token is neither revoked nor expired at t.
func (t *AppToken) Usable(at time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || t.ExpiresAt.After(at))
}

const schemaAppTokens = `
CREATE TABLE IF NOT EXISTS app_tokens(
  id INTEGER PRIMARY KEY,
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  created_by INTEGER,
  last_used_at INTEGER,
  expires_at INTEGER,
  revoked_at INTEGER
);
CREATE INDEX IF NOT EXISTS app_tokens_app ON app_tokens(app_id);
`

// migrateAppTokens creates app_tokens and, the first time, copies every
// app's single token (apps.token_hash) into it as "Default", so tokens issued
// before multi-token support keep working. apps.token_hash stays in place but
// is no longer read. Events also learn which token made the call.
func migrateAppTokens(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_tokens'`).Scan(&n); err != nil {
		return err
	}
	if _, err := db.Exec(schemaAppTokens); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(`INSERT OR IGNORE INTO app_tokens(app_id,name,prefix,hash,created_at,last_used_at)
			SELECT id, 'Default', token_prefix || '...', token_hash, created_at, last_used_at FROM apps`); err != nil {
			return err
		}
	}
	if _, err := addColumn(db, "app_tokens", "read_only", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := addColumn(db, "events", "token_id", "INTEGER"); err != nil {
		return err
	}
	return migrateAppTokenIDs(db)
}

const appTokenCols = `t.id, t.app_id, t.name, t.prefix, t.created_at, t.created_by, COALESCE(u.email,''), t.last_used_at, t.expires_at, t.revoked_at, t.read_only, t.limits, t.pinned_tags`

// tokenScan holds the Scan destinations of one app token row
// (appTokenCols) and turns them into an AppToken. scanAppToken and
// AppByToken share it.
type tokenScan struct {
	t                  AppToken
	created            int64
	by, last, exp, rev sql.NullInt64
	readOnly           int
	limits, pinned     string
}

func (s *tokenScan) dests() []any {
	t := &s.t
	return []any{&t.ID, &t.AppID, &t.Name, &t.Prefix, &s.created, &s.by, &t.CreatedByEmail, &s.last, &s.exp, &s.rev, &s.readOnly, &s.limits, &s.pinned}
}

func (s *tokenScan) finish() *AppToken {
	t := &s.t
	t.CreatedAt = time.Unix(s.created, 0).UTC()
	t.CreatedBy = toInt(s.by)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = toTime(s.last), toTime(s.exp), toTime(s.rev)
	t.ReadOnly = s.readOnly != 0
	// Bad JSON cannot be written through the API; read as no limits here,
	// admission reads the columns again and fails closed on it.
	t.Limits, _ = decodeLimits(s.limits)
	t.PinnedTags, _ = decodeTags(s.pinned)
	if t.PinnedTags == nil {
		t.PinnedTags = map[string]string{}
	}
	return t
}

func scanAppToken(row interface{ Scan(...any) error }) (*AppToken, error) {
	var ts tokenScan
	if err := row.Scan(ts.dests()...); err != nil {
		return nil, notFound(err)
	}
	return ts.finish(), nil
}

// TokenPrefixHint is what the UI shows for a token: its first 8 characters.
func TokenPrefixHint(tok string) string {
	if len(tok) > 8 {
		tok = tok[:8]
	}
	return tok + "..."
}

// CreateAppToken adds a token to an app. createdBy 0 means the system.
func (s *Store) CreateAppToken(ctx context.Context, appID int64, name, prefix, hash string, createdBy int64, expiresAt *time.Time) (*AppToken, error) {
	return s.CreateAppTokenWithScope(ctx, appID, name, prefix, hash, createdBy, expiresAt, false)
}

// CreateAppTokenWithScope adds a token to an app with optional read-only enforcement.
func (s *Store) CreateAppTokenWithScope(ctx context.Context, appID int64, name, prefix, hash string, createdBy int64, expiresAt *time.Time, readOnly bool) (*AppToken, error) {
	return s.CreateAppTokenWith(ctx, appID, name, prefix, hash, createdBy, expiresAt, AppTokenOptions{ReadOnly: readOnly})
}

// AppTokenOptions are a new token's scope and controls.
type AppTokenOptions struct {
	ReadOnly   bool
	Limits     AppLimits
	PinnedTags map[string]string
}

// CreateAppTokenWith adds a token with its limits and pinned tags in one
// write, so it never works for a moment without them.
func (s *Store) CreateAppTokenWith(ctx context.Context, appID int64, name, prefix, hash string, createdBy int64, expiresAt *time.Time, o AppTokenOptions) (*AppToken, error) {
	limits, err := encodeJSONOrEmpty(o.Limits, !o.Limits.Any())
	if err != nil {
		return nil, err
	}
	pinned, err := encodeJSONOrEmpty(o.PinnedTags, len(o.PinnedTags) == 0)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id, err := nextAppTokenID(ctx, tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_tokens(id,app_id,name,prefix,hash,created_at,created_by,expires_at,read_only,limits,pinned_tags) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, appID, name, prefix, hash, now(), nz(createdBy), fromTime(expiresAt), b2i(o.ReadOnly), limits, pinned); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAppToken(ctx, appID, id)
}

// GetAppToken returns one token of an app.
func (s *Store) GetAppToken(ctx context.Context, appID, id int64) (*AppToken, error) {
	return scanAppToken(s.r.QueryRowContext(ctx, `SELECT `+appTokenCols+` FROM app_tokens t LEFT JOIN users u ON u.id=t.created_by
		WHERE t.app_id=? AND t.id=?`, appID, id))
}

// ListAppTokens lists an app's tokens, revoked ones included, newest first.
func (s *Store) ListAppTokens(ctx context.Context, appID int64) ([]*AppToken, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+appTokenCols+` FROM app_tokens t LEFT JOIN users u ON u.id=t.created_by
		WHERE t.app_id=? ORDER BY t.revoked_at IS NOT NULL, t.id DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AppToken{}
	for rows.Next() {
		t, err := scanAppToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RenameAppToken changes a token's name.
func (s *Store) RenameAppToken(ctx context.Context, appID, id int64, name string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE app_tokens SET name=? WHERE app_id=? AND id=?`, name, appID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAppTokenLimits replaces a token's own limits.
func (s *Store) SetAppTokenLimits(ctx context.Context, appID, id int64, l AppLimits) error {
	raw, err := encodeJSONOrEmpty(l, !l.Any())
	if err != nil {
		return err
	}
	return execOne(s.DB.ExecContext(ctx, `UPDATE app_tokens SET limits=? WHERE app_id=? AND id=?`, raw, appID, id))
}

// SetAppTokenPinnedTags replaces the tags pinned to a token.
func (s *Store) SetAppTokenPinnedTags(ctx context.Context, appID, id int64, tags map[string]string) error {
	raw, err := encodeJSONOrEmpty(tags, len(tags) == 0)
	if err != nil {
		return err
	}
	return execOne(s.DB.ExecContext(ctx, `UPDATE app_tokens SET pinned_tags=? WHERE app_id=? AND id=?`, raw, appID, id))
}

// RevokeAppToken marks a token revoked. Revoking twice keeps the first time.
func (s *Store) RevokeAppToken(ctx context.Context, appID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE app_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE app_id=? AND id=?`, now(), appID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeOtherAppTokens revokes every token of the app except keepID and
// returns how many it revoked.
func (s *Store) RevokeOtherAppTokens(ctx context.Context, appID, keepID int64) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE app_tokens SET revoked_at=? WHERE app_id=? AND id<>? AND revoked_at IS NULL`, now(), appID, keepID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// appByTokenQ reads a token and its app in one statement, so both come
// from one snapshot and auth costs one read instead of two.
var appByTokenQ = `SELECT ` + appTokenCols + `, ` + appColsAs("a") + `
FROM app_tokens t LEFT JOIN users u ON u.id=t.created_by JOIN apps a ON a.id=t.app_id WHERE t.hash=?`

// AppByToken finds the app a token hash belongs to, and the token itself.
// It returns revoked and expired tokens too, and inactive apps; the caller
// decides. Nothing is cached: every request reads the token and app state.
func (s *Store) AppByToken(ctx context.Context, hash string) (*App, *AppToken, error) {
	var ts tokenScan
	var as appScan
	if err := s.r.QueryRowContext(ctx, appByTokenQ, hash).Scan(append(ts.dests(), as.dests()...)...); err != nil {
		return nil, nil, notFound(err)
	}
	t, a := ts.finish(), as.finish()
	a.TokenID = t.ID
	a.TokenReadOnly = t.ReadOnly
	return a, t, nil
}

// TokenTouchInterval is how often a token's last_used_at (and its app's) is
// written at most. Requests in between do not write.
var TokenTouchInterval = time.Minute

type touchKey struct {
	s  *Store
	id int64
}

var touched = struct {
	sync.Mutex
	at map[touchKey]time.Time
}{at: map[touchKey]time.Time{}}

// TouchAppToken records that a token (and its app) was just used, at most
// once per TokenTouchInterval per token.
func (s *Store) TouchAppToken(_ context.Context, appID, tokenID int64) {
	t := time.Now()
	touched.Lock()
	k := touchKey{s, tokenID}
	if last, ok := touched.at[k]; ok && t.Sub(last) < TokenTouchInterval {
		touched.Unlock()
		return
	}
	touched.at[k] = t
	touched.Unlock()
	_ = s.gw.write(
		wstmt{`UPDATE app_tokens SET last_used_at=? WHERE id=?`, []any{t.Unix(), tokenID}},
		wstmt{`UPDATE apps SET last_used_at=? WHERE id=?`, []any{t.Unix(), appID}})
}

type appTokenCtxKey struct{}

// WithAppToken marks ctx as a request made with the given app token (0 for
// none, as with an OAuth access token), so events recorded with it note the
// token as well as the app, and its usage counts toward the token and the
// caller tags admission sets on it (SetUsageTags).
func WithAppToken(ctx context.Context, tokenID int64) context.Context {
	return context.WithValue(ctx, appTokenCtxKey{}, &usageScope{tokenID: tokenID})
}

func appTokenFrom(ctx context.Context) int64 {
	if sc := scopeFrom(ctx); sc != nil {
		return sc.tokenID
	}
	return 0
}
