package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// AppToken is one of an app's tokens. An app can hold several, each named
// and revocable on its own; usage, spend and limits stay per app. Only the
// SHA-256 of the whole token is stored.
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
	_, err := addColumn(db, "events", "token_id", "INTEGER")
	return err
}

const appTokenCols = `t.id, t.app_id, t.name, t.prefix, t.created_at, t.created_by, COALESCE(u.email,''), t.last_used_at, t.expires_at, t.revoked_at`

func scanAppToken(row interface{ Scan(...any) error }) (*AppToken, error) {
	var t AppToken
	var created int64
	var by, last, exp, rev sql.NullInt64
	if err := row.Scan(&t.ID, &t.AppID, &t.Name, &t.Prefix, &created, &by, &t.CreatedByEmail, &last, &exp, &rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.CreatedBy = toInt(by)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = toTime(last), toTime(exp), toTime(rev)
	return &t, nil
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
	res, err := s.DB.ExecContext(ctx, `INSERT INTO app_tokens(app_id,name,prefix,hash,created_at,created_by,expires_at) VALUES(?,?,?,?,?,?,?)`,
		appID, name, prefix, hash, now(), nz(createdBy), fromTime(expiresAt))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
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

// AppByToken finds the app a token hash belongs to, and the token itself.
// It returns revoked and expired tokens too; the caller decides.
func (s *Store) AppByToken(ctx context.Context, hash string) (*App, *AppToken, error) {
	t, err := scanAppToken(s.r.QueryRowContext(ctx, `SELECT `+appTokenCols+` FROM app_tokens t LEFT JOIN users u ON u.id=t.created_by
		WHERE t.hash=?`, hash))
	if err != nil {
		return nil, nil, err
	}
	a, err := s.GetApp(ctx, t.AppID)
	if err != nil {
		return nil, nil, err
	}
	a.TokenID = t.ID
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
func (s *Store) TouchAppToken(ctx context.Context, appID, tokenID int64) {
	t := time.Now()
	touched.Lock()
	k := touchKey{s, tokenID}
	if last, ok := touched.at[k]; ok && t.Sub(last) < TokenTouchInterval {
		touched.Unlock()
		return
	}
	touched.at[k] = t
	touched.Unlock()
	_, _ = s.DB.ExecContext(ctx, `UPDATE app_tokens SET last_used_at=? WHERE id=?`, t.Unix(), tokenID)
	_, _ = s.DB.ExecContext(ctx, `UPDATE apps SET last_used_at=? WHERE id=?`, t.Unix(), appID)
}

type appTokenCtxKey struct{}

// WithAppToken marks ctx as a request made with the given app token, so
// events recorded with it note the token as well as the app.
func WithAppToken(ctx context.Context, tokenID int64) context.Context {
	if tokenID == 0 {
		return ctx
	}
	return context.WithValue(ctx, appTokenCtxKey{}, tokenID)
}

func appTokenFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(appTokenCtxKey{}).(int64)
	return id
}
