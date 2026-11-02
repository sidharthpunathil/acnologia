package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Token scopes map to dashboard roles: read acts as a member, write as an
// admin, admin as a superadmin. A token never exceeds its owner's role.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

func ScopeRole(scope string) (Role, bool) {
	switch scope {
	case ScopeRead:
		return RoleMember, true
	case ScopeWrite:
		return RoleAdmin, true
	case ScopeAdmin:
		return RoleSuperadmin, true
	}
	return "", false
}

// AdminToken lets scripts, CI and the acnologia ctl CLI call the admin API
// without a browser session.
type AdminToken struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scope       string     `json:"scope"`
	UserID      int64      `json:"user_id"`
	UserEmail   string     `json:"user_email"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

const tokenCols = `t.id, t.name, t.token_prefix, t.scope, t.user_id, COALESCE(u.email,''), t.created_at, t.expires_at, t.last_used_at`

func scanToken(row interface{ Scan(...any) error }) (*AdminToken, error) {
	var t AdminToken
	var created int64
	var exp, last sql.NullInt64
	if err := row.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.Scope, &t.UserID, &t.UserEmail, &created, &exp, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.ExpiresAt = toTime(exp)
	t.LastUsedAt = toTime(last)
	return &t, nil
}

// ListTokens returns every token, or only one user's when userID > 0.
func (s *Store) ListTokens(ctx context.Context, userID int64) ([]*AdminToken, error) {
	q := `SELECT ` + tokenCols + ` FROM admin_tokens t LEFT JOIN users u ON u.id=t.user_id`
	args := []any{}
	if userID > 0 {
		q += ` WHERE t.user_id=?`
		args = append(args, userID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY t.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AdminToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetToken(ctx context.Context, id int64) (*AdminToken, error) {
	return scanToken(s.DB.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM admin_tokens t LEFT JOIN users u ON u.id=t.user_id WHERE t.id=?`, id))
}

func (s *Store) CreateToken(ctx context.Context, t *AdminToken, hash string) (*AdminToken, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO admin_tokens(name,token_hash,token_prefix,scope,user_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
		t.Name, hash, t.TokenPrefix, t.Scope, t.UserID, now(), fromTime(t.ExpiresAt))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetToken(ctx, id)
}

func (s *Store) DeleteToken(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM admin_tokens WHERE id=?`, id)
	return err
}

// TokenByHash finds a live token and its owner. Expired tokens and tokens of
// disabled users are not found.
func (s *Store) TokenByHash(ctx context.Context, hash string) (*AdminToken, *User, error) {
	t, err := scanToken(s.DB.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM admin_tokens t LEFT JOIN users u ON u.id=t.user_id WHERE t.token_hash=?`, hash))
	if err != nil {
		return nil, nil, err
	}
	if t.ExpiresAt != nil && !t.ExpiresAt.After(time.Now()) {
		return nil, nil, ErrNotFound
	}
	u, err := s.GetUser(ctx, t.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u.Disabled {
		return nil, nil, ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE admin_tokens SET last_used_at=? WHERE id=?`, now(), t.ID)
	return t, u, nil
}

// Token prefixes. New tokens use the acn prefixes; tokens minted before the
// rename from Cogito carry cog_ and cogadm_ and keep working, because lookups
// go by the hash of the whole token and the prefix only routes the request.
const (
	AppTokenPrefix         = "acn_"
	AdminTokenPrefix       = "acnadm_"
	legacyAppTokenPrefix   = "cog_"
	legacyAdminTokenPrefix = "cogadm_"
)

// IsAppToken reports whether tok looks like an app token, new or legacy.
func IsAppToken(tok string) bool {
	return strings.HasPrefix(tok, AppTokenPrefix) || strings.HasPrefix(tok, legacyAppTokenPrefix)
}

// IsAdminToken reports whether tok looks like an admin API token, new or legacy.
func IsAdminToken(tok string) bool {
	return strings.HasPrefix(tok, AdminTokenPrefix) || strings.HasPrefix(tok, legacyAdminTokenPrefix)
}
