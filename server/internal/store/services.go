package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const serviceCols = `id, slug, name, provider, category, description, base_url, auth_type, auth_name, strategy, cooldown_sec, max_attempts, icon, created_at`

func scanService(row interface{ Scan(...any) error }) (*Service, error) {
	var s Service
	var created int64
	err := row.Scan(&s.ID, &s.Slug, &s.Name, &s.Provider, &s.Category, &s.Description, &s.BaseURL,
		&s.AuthType, &s.AuthName, &s.Strategy, &s.CooldownSec, &s.MaxAttempts, &s.Icon, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt = time.Unix(created, 0).UTC()
	return &s, nil
}

func (s *Store) CreateService(ctx context.Context, sv *Service) (*Service, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO services(slug,name,provider,category,description,base_url,auth_type,auth_name,strategy,cooldown_sec,max_attempts,icon,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sv.Slug, sv.Name, sv.Provider, sv.Category, sv.Description, sv.BaseURL, sv.AuthType, sv.AuthName,
		sv.Strategy, sv.CooldownSec, sv.MaxAttempts, sv.Icon, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetService(ctx, id)
}

func (s *Store) UpdateService(ctx context.Context, sv *Service) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET slug=?,name=?,provider=?,category=?,description=?,base_url=?,auth_type=?,auth_name=?,strategy=?,cooldown_sec=?,max_attempts=?,icon=? WHERE id=?`,
		sv.Slug, sv.Name, sv.Provider, sv.Category, sv.Description, sv.BaseURL, sv.AuthType, sv.AuthName,
		sv.Strategy, sv.CooldownSec, sv.MaxAttempts, sv.Icon, sv.ID)
	return err
}

func (s *Store) DeleteService(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM services WHERE id=?`, id)
	return err
}

func (s *Store) GetService(ctx context.Context, id int64) (*Service, error) {
	return scanService(s.DB.QueryRowContext(ctx, `SELECT `+serviceCols+` FROM services WHERE id=?`, id))
}

func (s *Store) GetServiceBySlug(ctx context.Context, slug string) (*Service, error) {
	return scanService(s.DB.QueryRowContext(ctx, `SELECT `+serviceCols+` FROM services WHERE slug=?`, slug))
}

func (s *Store) ListServices(ctx context.Context) ([]*Service, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+serviceCols+` FROM services ORDER BY category, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Service{}
	for rows.Next() {
		sv, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// FillStats computes key counts and 24h request totals. Hidden keys are
// counted only when includeHidden is set.
func (s *Store) FillStats(ctx context.Context, svcs []*Service, includeHidden bool) error {
	byID := map[int64]*Service{}
	for _, sv := range svcs {
		byID[sv.ID] = sv
		sv.KeyCounts = KeyCounts{}
	}
	q := `SELECT service_id, status, tier, cooldown_until, quota_remaining FROM keys`
	if !includeHidden {
		q += ` WHERE hidden=0`
	}
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	t := time.Now()
	for rows.Next() {
		var sid int64
		var k Key
		var cd, qr sql.NullInt64
		if err := rows.Scan(&sid, &k.Status, &k.Tier, &cd, &qr); err != nil {
			return err
		}
		sv := byID[sid]
		if sv == nil {
			continue
		}
		k.CooldownUntil, k.QuotaRemaining = toTime(cd), toInt(qr)
		c := &sv.KeyCounts
		c.Total++
		if k.Tier == "backup" {
			c.Backup++
		}
		switch {
		case k.Available(t):
			c.Available++
		case k.Status == StatusActive && k.CooldownUntil != nil && k.CooldownUntil.After(t):
			c.Cooling++
		case k.Status == StatusExhausted || (k.Status == StatusActive && k.QuotaRemaining != nil):
			c.Exhausted++
		case k.Status == StatusInvalid:
			c.Invalid++
		case k.Status == StatusDisabled:
			c.Disabled++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows2, err := s.DB.QueryContext(ctx, `SELECT service_id, COUNT(*) FROM events WHERE ts>? AND kind IN ('lease','proxy') GROUP BY service_id`,
		time.Now().Add(-24*time.Hour).Unix())
	if err != nil {
		return err
	}
	defer rows2.Close()
	for rows2.Next() {
		var sid sql.NullInt64
		var n int
		if err := rows2.Scan(&sid, &n); err != nil {
			return err
		}
		if sv := byID[sid.Int64]; sv != nil {
			sv.Requests24h = n
		}
	}
	return rows2.Err()
}
