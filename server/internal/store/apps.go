package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const appCols = `id, name, description, token_prefix, allowed_services, mode, active, created_at, last_used_at, lease_hidden, daily_requests, monthly_requests, daily_budget, monthly_budget, share_id,
(SELECT COUNT(*) FROM app_tokens t WHERE t.app_id=apps.id AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>CAST(strftime('%s','now') AS INTEGER)))`

func scanApp(row interface{ Scan(...any) error }) (*App, error) {
	var a App
	var allowed string
	var active, leaseHidden int
	var created int64
	var last, dr, mr sql.NullInt64
	var db, mb sql.NullFloat64
	var share sql.NullString
	if err := row.Scan(&a.ID, &a.Name, &a.Description, &a.TokenPrefix, &allowed, &a.Mode, &active, &created, &last, &leaseHidden, &dr, &mr, &db, &mb, &share, &a.TokenCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.AllowedServices = []string{}
	_ = json.Unmarshal([]byte(allowed), &a.AllowedServices)
	a.Active = active == 1
	a.LeaseHidden = leaseHidden == 1
	a.CreatedAt = time.Unix(created, 0).UTC()
	a.LastUsedAt = toTime(last)
	if share.Valid && share.String != "" {
		a.ShareID = &share.String
	}
	a.Limits.DailyRequests, a.Limits.MonthlyRequests = toInt(dr), toInt(mr)
	if db.Valid {
		a.Limits.DailyBudget = &db.Float64
	}
	if mb.Valid {
		a.Limits.MonthlyBudget = &mb.Float64
	}
	return &a, nil
}

// CreateApp creates an app with its first token, named "Default".
func (s *Store) CreateApp(ctx context.Context, a *App, tokenHash string) (*App, error) {
	a, _, err := s.CreateAppWithToken(ctx, a, "Default", TokenPrefixHint(a.TokenPrefix), tokenHash, 0)
	return a, err
}

// CreateAppWithToken creates an app and its first token in one transaction.
// apps.token_hash predates app_tokens and is no longer read; it gets a
// placeholder that can never match a real token's hash.
func (s *Store) CreateAppWithToken(ctx context.Context, a *App, tokenName, prefix, tokenHash string, createdBy int64) (*App, *AppToken, error) {
	allowed, _ := json.Marshal(nonNil(a.AllowedServices))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.ExecContext(ctx, `INSERT INTO apps(name,description,token_hash,token_prefix,allowed_services,mode,active,created_at,lease_hidden) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Description, "app_tokens:"+tokenHash, a.TokenPrefix, string(allowed), a.Mode, b2i(a.Active), t, b2i(a.LeaseHidden))
	if err != nil {
		return nil, nil, err
	}
	id, _ := res.LastInsertId()
	res, err = tx.ExecContext(ctx, `INSERT INTO app_tokens(app_id,name,prefix,hash,created_at,created_by) VALUES(?,?,?,?,?,?)`,
		id, tokenName, prefix, tokenHash, t, nz(createdBy))
	if err != nil {
		return nil, nil, err
	}
	tokID, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	created, err := s.GetApp(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	tok, err := s.GetAppToken(ctx, id, tokID)
	return created, tok, err
}

// AppPatch lists the app fields an edit changes; a nil field keeps the
// stored value.
type AppPatch struct {
	Name            *string
	Description     *string
	AllowedServices *[]string
	Mode            *string
	Active          *bool
	LeaseHidden     *bool
}

// PatchApp writes only the fields the patch sets. An edit never rewrites a
// column from a copy read earlier, so it cannot undo a change made in the
// meantime, such as a token rotation clearing lease_hidden.
func (s *Store) PatchApp(ctx context.Context, id int64, p AppPatch) error {
	sets, args := []string{}, []any{}
	add := func(col string, v any) {
		sets = append(sets, col+"=?")
		args = append(args, v)
	}
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.Description != nil {
		add("description", *p.Description)
	}
	if p.AllowedServices != nil {
		allowed, _ := json.Marshal(nonNil(*p.AllowedServices))
		add("allowed_services", string(allowed))
	}
	if p.Mode != nil {
		add("mode", *p.Mode)
	}
	if p.Active != nil {
		add("active", b2i(*p.Active))
	}
	if p.LeaseHidden != nil {
		add("lease_hidden", b2i(*p.LeaseHidden))
	}
	if len(sets) == 0 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE apps SET `+strings.Join(sets, ", ")+` WHERE id=?`, append(args, id)...)
	return err
}

// RotateAppToken adds a new token to the app and revokes all its others,
// in one transaction. keepHidden false also revokes the app's access to
// hidden keys, since the new token holder may not be trusted.
func (s *Store) RotateAppToken(ctx context.Context, id int64, name, prefix, tokenHash string, createdBy int64, keepHidden bool) (*AppToken, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := `UPDATE apps SET token_prefix=? WHERE id=?`
	if !keepHidden {
		q = `UPDATE apps SET token_prefix=?, lease_hidden=0 WHERE id=?`
	}
	res, err := tx.ExecContext(ctx, q, prefix, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	t := now()
	if _, err := tx.ExecContext(ctx, `UPDATE app_tokens SET revoked_at=? WHERE app_id=? AND revoked_at IS NULL`, t, id); err != nil {
		return nil, err
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO app_tokens(app_id,name,prefix,hash,created_at,created_by) VALUES(?,?,?,?,?,?)`,
		id, name, TokenPrefixHint(prefix), tokenHash, t, nz(createdBy))
	if err != nil {
		return nil, err
	}
	tokID, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAppToken(ctx, id, tokID)
}

// ClearLeaseHidden stops the app from leasing hidden keys.
func (s *Store) ClearLeaseHidden(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE apps SET lease_hidden=0 WHERE id=?`, id)
	return err
}

func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM apps WHERE id=?`, id)
	// App ids can be reused; forget any share scopes cached for this one.
	s.invalidateScopes()
	return err
}

func (s *Store) GetApp(ctx context.Context, id int64) (*App, error) {
	return scanApp(s.r.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE id=?`, id))
}

// ListApps lists the apps people manage on the Apps page. Apps backing a
// share are managed on the Sharing page and left out.
func (s *Store) ListApps(ctx context.Context) ([]*App, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+appCols+` FROM apps WHERE share_id IS NULL ORDER BY name`)
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

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Leases

func (s *Store) CreateLease(ctx context.Context, l *Lease) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO leases(id,key_id,app_id,service_id,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		l.ID, l.KeyID, l.AppID, l.ServiceID, l.CreatedAt.Unix(), l.ExpiresAt.Unix())
	return err
}

func (s *Store) GetLease(ctx context.Context, id string) (*Lease, error) {
	var l Lease
	var c, e int64
	err := s.r.QueryRowContext(ctx, `SELECT id,key_id,app_id,service_id,created_at,expires_at,outcome FROM leases WHERE id=?`, id).
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
	rows, err := s.r.QueryContext(ctx, `SELECT id,key_id,app_id,service_id,created_at,expires_at FROM leases WHERE outcome='' AND expires_at>?`, now())
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

// ErrLeaseReported means the lease already has an outcome.
var ErrLeaseReported = errors.New("lease already reported")

// ClaimLeaseOutcome sets the outcome only if none is set yet, so two
// concurrent reports cannot both apply.
func (s *Store) ClaimLeaseOutcome(ctx context.Context, id, outcome string) error {
	res, err := s.w.ExecContext(ctx, `UPDATE leases SET outcome=? WHERE id=? AND outcome=''`, outcome, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseReported
	}
	return nil
}

// Housekeeping

// pruneBatch is how many rows one prune statement deletes. Each batch is its
// own short write, so requests waiting on the writer get it between batches
// instead of after one long delete.
const pruneBatch = 1000

// Prune deletes expired sessions, leases older than a week and events older
// than eventRetention (zero or less keeps events forever), then refreshes
// planner statistics.
func (s *Store) Prune(ctx context.Context, eventRetention time.Duration) error {
	t := time.Now()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<?`, t.Unix()); err != nil {
		return err
	}
	if err := s.pruneBatched(ctx, `DELETE FROM leases WHERE rowid IN (SELECT rowid FROM leases WHERE created_at<? LIMIT ?)`,
		t.Add(-7*24*time.Hour).Unix()); err != nil {
		return err
	}
	if eventRetention > 0 {
		if err := s.pruneBatched(ctx, `DELETE FROM events WHERE id IN (SELECT id FROM events WHERE ts<? LIMIT ?)`,
			t.Add(-eventRetention).Unix()); err != nil {
			return err
		}
	}
	optimize(ctx, s.DB)
	return nil
}

// pruneBatched runs a DELETE of at most pruneBatch rows until one deletes
// fewer.
func (s *Store) pruneBatched(ctx context.Context, q string, before int64) error {
	for {
		res, err := s.w.ExecContext(ctx, q, before, pruneBatch)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n < pruneBatch {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
