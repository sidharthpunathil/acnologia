package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const serviceCols = `id, slug, name, provider, category, description, base_url, auth_type, auth_name, strategy, cooldown_sec, max_attempts, icon, created_at, max_concurrency, queue_timeout_sec, logo IS NOT NULL, logo_rev, target_trusted, egress_id, cache_ttl_sec, cost_per_request,
peer_link_id, peer_remote_slug, peer_provider, COALESCE((SELECT pl.peer_name FROM peer_links pl WHERE pl.id=services.peer_link_id),''),
COALESCE((SELECT pl.trusted FROM peer_links pl WHERE pl.id=services.peer_link_id),0)`

func scanService(row interface{ Scan(...any) error }) (*Service, error) {
	var s Service
	var created int64
	var egress, ttl, link sql.NullInt64
	var cost sql.NullFloat64
	err := row.Scan(&s.ID, &s.Slug, &s.Name, &s.Provider, &s.Category, &s.Description, &s.BaseURL,
		&s.AuthType, &s.AuthName, &s.Strategy, &s.CooldownSec, &s.MaxAttempts, &s.Icon, &created, &s.MaxConcurrency, &s.QueueTimeoutSec, &s.HasLogo, &s.LogoRev, &s.TargetTrusted,
		&egress, &ttl, &cost, &link, &s.PeerRemoteSlug, &s.PeerProvider, &s.PeerName, &s.PeerTrusted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt = time.Unix(created, 0).UTC()
	s.EgressID, s.CacheTTLSec, s.PeerLinkID = toInt(egress), toInt(ttl), toInt(link)
	if cost.Valid {
		s.CostPerRequest = &cost.Float64
	}
	return &s, nil
}

func (s *Store) CreateService(ctx context.Context, sv *Service) (*Service, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO services(slug,name,provider,category,description,base_url,auth_type,auth_name,strategy,cooldown_sec,max_attempts,icon,created_at,max_concurrency,queue_timeout_sec,target_trusted,egress_id,cache_ttl_sec,cost_per_request)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sv.Slug, sv.Name, sv.Provider, sv.Category, sv.Description, sv.BaseURL, sv.AuthType, sv.AuthName,
		sv.Strategy, sv.CooldownSec, sv.MaxAttempts, sv.Icon, now(), sv.MaxConcurrency, sv.QueueTimeoutSec, b2i(sv.TargetTrusted),
		fromInt(sv.EgressID), fromInt(sv.CacheTTLSec), fromFloat(sv.CostPerRequest))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetService(ctx, id)
}

func (s *Store) UpdateService(ctx context.Context, sv *Service) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET slug=?,name=?,provider=?,category=?,description=?,base_url=?,auth_type=?,auth_name=?,strategy=?,cooldown_sec=?,max_attempts=?,icon=?,max_concurrency=?,queue_timeout_sec=?,target_trusted=?,egress_id=?,cache_ttl_sec=?,cost_per_request=? WHERE id=?`,
		sv.Slug, sv.Name, sv.Provider, sv.Category, sv.Description, sv.BaseURL, sv.AuthType, sv.AuthName,
		sv.Strategy, sv.CooldownSec, sv.MaxAttempts, sv.Icon, sv.MaxConcurrency, sv.QueueTimeoutSec, b2i(sv.TargetTrusted),
		fromInt(sv.EgressID), fromInt(sv.CacheTTLSec), fromFloat(sv.CostPerRequest), sv.ID)
	return err
}

// DeleteService deletes a service; its keys go by cascade, and a trigger
// takes the events of its hidden keys with them.
func (s *Store) DeleteService(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM services WHERE id=?`, id)
	if err == nil {
		s.keysChanged()
	}
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
func (s *Store) FillStats(ctx context.Context, svcs []*Service, includeHidden bool, inflight map[int64]int) error {
	byID := map[int64]*Service{}
	for _, sv := range svcs {
		byID[sv.ID] = sv
		sv.KeyCounts = KeyCounts{}
	}
	q := `SELECT id, service_id, status, tier, cooldown_until, quota_remaining, max_concurrency FROM keys`
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
		var cd, qr, mc sql.NullInt64
		if err := rows.Scan(&k.ID, &sid, &k.Status, &k.Tier, &cd, &qr, &mc); err != nil {
			return err
		}
		k.MaxConcurrency = toInt(mc)
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
		n := inflight[k.ID]
		sv.InFlight += n
		switch {
		case k.Available(t):
			c.Available++
			if lim := k.ConcurrencyLimit(sv); lim > 0 && n >= lim {
				c.Busy++
			}
		case k.Status == StatusActive && k.CooldownUntil != nil && k.CooldownUntil.After(t):
			c.Cooling++
		case k.Status == StatusExhausted || (k.Status == StatusActive && k.QuotaRemaining != nil):
			c.Exhausted++
		case k.Status == StatusInvalid:
			c.Invalid++
		case k.Status == StatusBanned:
			c.Banned++
		case k.Status == StatusDisabled:
			c.Disabled++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rq := `SELECT e.service_id, COUNT(*) FROM events e WHERE e.ts>? AND e.kind IN ('lease','proxy')`
	if !includeHidden {
		rq += ` AND NOT EXISTS (SELECT 1 FROM keys k WHERE k.id=e.key_id AND k.hidden=1)`
	}
	rows2, err := s.DB.QueryContext(ctx, rq+` GROUP BY e.service_id`, time.Now().Add(-24*time.Hour).Unix())
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

func (s *Store) SetServiceLogo(ctx context.Context, id int64, data []byte, mime string) error {
	var d any
	if data != nil {
		d = data
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET logo=?, logo_mime=?, logo_rev=logo_rev+1 WHERE id=?`, d, mime, id)
	return err
}

func (s *Store) ServiceLogo(ctx context.Context, id int64) ([]byte, string, error) {
	var data []byte
	var mime string
	err := s.DB.QueryRowContext(ctx, `SELECT logo, logo_mime FROM services WHERE id=? AND logo IS NOT NULL`, id).Scan(&data, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return data, mime, err
}

// SetServiceTrusted records that a superadmin vouched for the service's
// upstream target, so hidden keys may be sent to it.
func (s *Store) SetServiceTrusted(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET target_trusted=1 WHERE id=?`, id)
	return err
}
