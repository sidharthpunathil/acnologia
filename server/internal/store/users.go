package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const userCols = `id, email, name, password_hash, role, disabled, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created int64
	var last sql.NullInt64
	var disabled int
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &disabled, &created, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Disabled = disabled == 1
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.LastLoginAt = toTime(last)
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CountSuperadmins(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='superadmin' AND disabled=0`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, name, hash string, role Role) (*User, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users(email,name,password_hash,role,created_at) VALUES(?,?,?,?,?)`,
		email, name, hash, role, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(ctx, id)
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email=?`, email))
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type UserPatch struct {
	Name         *string
	Role         *Role
	Disabled     *bool
	PasswordHash *string
}

func (s *Store) UpdateUser(ctx context.Context, id int64, p UserPatch) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.Name != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET name=? WHERE id=?`, *p.Name, id); err != nil {
			return err
		}
	}
	if p.Role != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, *p.Role, id); err != nil {
			return err
		}
	}
	if p.Disabled != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled=? WHERE id=?`, b2i(*p.Disabled), id); err != nil {
			return err
		}
		if *p.Disabled {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
				return err
			}
		}
	}
	if p.PasswordHash != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, *p.PasswordHash, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

func (s *Store) TouchLogin(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, now(), id)
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, ttl time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)`,
		tokenHash, userID, now(), time.Now().Add(ttl).Unix())
	return err
}

func (s *Store) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+prefixed("u.", userCols)+` FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>? AND u.disabled=0`, tokenHash, now()))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, exceptHash)
	return err
}

func prefixed(p, cols string) string {
	out := ""
	field := ""
	flush := func() {
		if field != "" {
			if out != "" {
				out += ", "
			}
			out += p + field
			field = ""
		}
	}
	for _, c := range cols {
		switch c {
		case ',':
			flush()
		case ' ':
		default:
			field += string(c)
		}
	}
	flush()
	return out
}
