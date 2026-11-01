package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// EgressProxy is an outbound HTTP, HTTPS or SOCKS5 proxy that upstream
// traffic for a key or a service goes through. The URL is encrypted because
// it usually carries credentials.
type EgressProxy struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	URLHint       string     `json:"url_hint"`
	Region        string     `json:"region"`
	Notes         string     `json:"notes"`
	CreatedAt     time.Time  `json:"created_at"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastIP        string     `json:"last_ip"`
	LastError     string     `json:"last_error"`
	// Usage counts are filled by the API.
	Services int `json:"services"`
	Keys     int `json:"keys"`

	URLEnc []byte `json:"-"`
}

const egressCols = `id, name, url_enc, url_hint, region, notes, created_at, last_checked_at, last_ip, last_error`

func scanEgress(row interface{ Scan(...any) error }) (*EgressProxy, error) {
	var p EgressProxy
	var created int64
	var checked sql.NullInt64
	if err := row.Scan(&p.ID, &p.Name, &p.URLEnc, &p.URLHint, &p.Region, &p.Notes, &created, &checked, &p.LastIP, &p.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.LastCheckedAt = toTime(checked)
	return &p, nil
}

func (s *Store) ListEgress(ctx context.Context) ([]*EgressProxy, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+egressCols+`,
		(SELECT COUNT(*) FROM services WHERE egress_id=egress_proxies.id),
		(SELECT COUNT(*) FROM keys WHERE egress_id=egress_proxies.id)
		FROM egress_proxies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*EgressProxy{}
	for rows.Next() {
		var p EgressProxy
		var created int64
		var checked sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.URLEnc, &p.URLHint, &p.Region, &p.Notes, &created, &checked, &p.LastIP, &p.LastError, &p.Services, &p.Keys); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(created, 0).UTC()
		p.LastCheckedAt = toTime(checked)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) GetEgress(ctx context.Context, id int64) (*EgressProxy, error) {
	return scanEgress(s.DB.QueryRowContext(ctx, `SELECT `+egressCols+` FROM egress_proxies WHERE id=?`, id))
}

func (s *Store) CreateEgress(ctx context.Context, p *EgressProxy) (*EgressProxy, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO egress_proxies(name,url_enc,url_hint,region,notes,created_at) VALUES(?,?,?,?,?,?)`,
		p.Name, p.URLEnc, p.URLHint, p.Region, p.Notes, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetEgress(ctx, id)
}

// UpdateEgress saves name, region and notes, and the URL when URLEnc is set.
func (s *Store) UpdateEgress(ctx context.Context, p *EgressProxy) error {
	if p.URLEnc != nil {
		_, err := s.DB.ExecContext(ctx, `UPDATE egress_proxies SET name=?, region=?, notes=?, url_enc=?, url_hint=? WHERE id=?`,
			p.Name, p.Region, p.Notes, p.URLEnc, p.URLHint, p.ID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE egress_proxies SET name=?, region=?, notes=? WHERE id=?`, p.Name, p.Region, p.Notes, p.ID)
	return err
}

// DeleteEgress removes a proxy and detaches it from services and keys, which
// then connect directly again.
func (s *Store) DeleteEgress(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE services SET egress_id=NULL WHERE egress_id=?`,
		`UPDATE keys SET egress_id=NULL WHERE egress_id=?`,
		`DELETE FROM egress_proxies WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordEgressCheck(ctx context.Context, id int64, ip, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE egress_proxies SET last_checked_at=?, last_ip=?, last_error=? WHERE id=?`, now(), ip, errMsg, id)
	return err
}

// SetServiceEgress assigns a proxy to a service; nil connects directly.
func (s *Store) SetServiceEgress(ctx context.Context, serviceID int64, egressID *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET egress_id=? WHERE id=?`, fromInt(egressID), serviceID)
	return err
}

// SetKeyEgress assigns a proxy to a key; nil falls back to the service's.
func (s *Store) SetKeyEgress(ctx context.Context, keyID int64, egressID *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE keys SET egress_id=? WHERE id=?`, fromInt(egressID), keyID)
	return err
}
