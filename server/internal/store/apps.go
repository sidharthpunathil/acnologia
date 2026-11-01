package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const appCols = `id, name, description, token_prefix, allowed_services, mode, active, created_at, last_used_at`

func scanApp(row interface{ Scan(...any) error }) (*App, error) {
	var a App
	var allowed string
	var active int
	var created int64
	var last sql.NullInt64
	if err := row.Scan(&a.ID, &a.Name, &a.Description, &a.TokenPrefix, &allowed, &a.Mode, &active, &created, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.AllowedServices = []string{}
	_ = json.Unmarshal([]byte(allowed), &a.AllowedServices)
	a.Active = active == 1
	a.CreatedAt = time.Unix(created, 0).UTC()
	a.LastUsedAt = toTime(last)
	return &a, nil
}

func (s *Store) CreateApp(ctx context.Context, a *App, tokenHash string) (*App, error) {
	allowed, _ := json.Marshal(nonNil(a.AllowedServices))
	res, err := s.DB.ExecContext(ctx, `INSERT INTO apps(name,description,token_hash,token_prefix,allowed_services,mode,active,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		a.Name, a.Description, tokenHash, a.TokenPrefix, string(allowed), a.Mode, b2i(a.Active), now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetApp(ctx, id)
}

func (s *Store) UpdateApp(ctx context.Context, a *App) error {
	allowed, _ := json.Marshal(nonNil(a.AllowedServices))
	_, err := s.DB.ExecContext(ctx, `UPDATE apps SET name=?, description=?, allowed_services=?, mode=?, active=? WHERE id=?`,
		a.Name, a.Description, string(allowed), a.Mode, b2i(a.Active), a.ID)
	return err
}

func (s *Store) SetAppToken(ctx context.Context, id int64, tokenHash, prefix string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE apps SET token_hash=?, token_prefix=? WHERE id=?`, tokenHash, prefix, id)
	return err
}

func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM apps WHERE id=?`, id)
	return err
}

func (s *Store) GetApp(ctx context.Context, id int64) (*App, error) {
	return scanApp(s.DB.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE id=?`, id))
}

func (s *Store) AppByTokenHash(ctx context.Context, hash string) (*App, error) {
	return scanApp(s.DB.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE token_hash=?`, hash))
}

func (s *Store) ListApps(ctx context.Context) ([]*App, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+appCols+` FROM apps ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) TouchApp(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE apps SET last_used_at=? WHERE id=?`, now(), id)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Leases

func (s *Store) CreateLease(ctx context.Context, l *Lease) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO leases(id,key_id,app_id,service_id,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		l.ID, l.KeyID, l.AppID, l.ServiceID, l.CreatedAt.Unix(), l.ExpiresAt.Unix())
	return err
}

func (s *Store) GetLease(ctx context.Context, id string) (*Lease, error) {
	var l Lease
	var c, e int64
	err := s.DB.QueryRowContext(ctx, `SELECT id,key_id,app_id,service_id,created_at,expires_at,outcome FROM leases WHERE id=?`, id).
		Scan(&l.ID, &l.KeyID, &l.AppID, &l.ServiceID, &c, &e, &l.Outcome)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	l.CreatedAt, l.ExpiresAt = time.Unix(c, 0).UTC(), time.Unix(e, 0).UTC()
	return &l, nil
}

// OpenLeases returns unexpired leases that have not been reported yet.
func (s *Store) OpenLeases(ctx context.Context) ([]*Lease, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,key_id,app_id,service_id,created_at,expires_at FROM leases WHERE outcome='' AND expires_at>?`, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Lease{}
	for rows.Next() {
		var l Lease
		var c, e int64
		if err := rows.Scan(&l.ID, &l.KeyID, &l.AppID, &l.ServiceID, &c, &e); err != nil {
			return nil, err
		}
		l.CreatedAt, l.ExpiresAt = time.Unix(c, 0).UTC(), time.Unix(e, 0).UTC()
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (s *Store) SetLeaseOutcome(ctx context.Context, id, outcome string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE leases SET outcome=? WHERE id=?`, outcome, id)
	return err
}

// Housekeeping

func (s *Store) Prune(ctx context.Context, eventRetention time.Duration) error {
	t := time.Now()
	stmts := []struct {
		q string
		a any
	}{
		{`DELETE FROM sessions WHERE expires_at<?`, t.Unix()},
		{`DELETE FROM leases WHERE created_at<?`, t.Add(-7 * 24 * time.Hour).Unix()},
		{`DELETE FROM events WHERE ts<?`, t.Add(-eventRetention).Unix()},
	}
	for _, st := range stmts {
		if _, err := s.DB.ExecContext(ctx, st.q, st.a); err != nil {
			return err
		}
	}
	return nil
}
