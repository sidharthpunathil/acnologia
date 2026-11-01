package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const keyCols = `id, service_id, label, secret_enc, hint, tier, hidden, restricted, status, weight, priority, cooldown_until,
consecutive_fails, quota_limit, quota_remaining, quota_reset_at, usage_total, usage_today, last_used_at, last_error,
last_checked_at, notes, created_at`

func scanKey(row interface{ Scan(...any) error }) (*Key, error) {
	var k Key
	var hidden, restricted int
	var cd, ql, qr, qra, lu, lc sql.NullInt64
	var created int64
	err := row.Scan(&k.ID, &k.ServiceID, &k.Label, &k.SecretEnc, &k.Hint, &k.Tier, &hidden, &restricted, &k.Status,
		&k.Weight, &k.Priority, &cd, &k.ConsecutiveFails, &ql, &qr, &qra, &k.UsageTotal, &k.UsageToday, &lu,
		&k.LastError, &lc, &k.Notes, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	k.Hidden, k.Restricted = hidden == 1, restricted == 1
	k.CooldownUntil, k.QuotaLimit, k.QuotaRemaining, k.QuotaResetAt = toTime(cd), toInt(ql), toInt(qr), toTime(qra)
	k.LastUsedAt, k.LastCheckedAt = toTime(lu), toTime(lc)
	k.CreatedAt = time.Unix(created, 0).UTC()
	k.AppIDs = []int64{}
	k.computeEffective(time.Now())
	return &k, nil
}

func (s *Store) queryKeys(ctx context.Context, where string, args ...any) ([]*Key, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+keyCols+` FROM keys `+where, args...)
	if err != nil {
		return nil, err
	}
	out := []*Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachApps(ctx, out)
}

func (s *Store) attachApps(ctx context.Context, keys []*Key) error {
	if len(keys) == 0 {
		return nil
	}
	byID := map[int64]*Key{}
	ids := make([]any, 0, len(keys))
	for _, k := range keys {
		byID[k.ID] = k
		ids = append(ids, k.ID)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT key_id, app_id FROM key_apps WHERE key_id IN (`+placeholders(len(ids))+`)`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kid, aid int64
		if err := rows.Scan(&kid, &aid); err != nil {
			return err
		}
		byID[kid].AppIDs = append(byID[kid].AppIDs, aid)
	}
	return rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func (s *Store) ListKeys(ctx context.Context, serviceID int64, includeHidden bool) ([]*Key, error) {
	where := `WHERE service_id=?`
	if !includeHidden {
		where += ` AND hidden=0`
	}
	return s.queryKeys(ctx, where+` ORDER BY tier DESC, priority, id`, serviceID)
}

func (s *Store) ListAllKeys(ctx context.Context, where string, args ...any) ([]*Key, error) {
	return s.queryKeys(ctx, where, args...)
}

func (s *Store) GetKey(ctx context.Context, id int64) (*Key, error) {
	keys, err := s.queryKeys(ctx, `WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrNotFound
	}
	return keys[0], nil
}

// CandidateKeys returns keys of a service that are active and not exhausted.
// Cooldown and app restriction filtering happens in the pool.
func (s *Store) CandidateKeys(ctx context.Context, serviceID int64) ([]*Key, error) {
	return s.queryKeys(ctx, `WHERE service_id=? AND status='active'`, serviceID)
}

type NewKey struct {
	ServiceID  int64
	Label      string
	SecretEnc  []byte
	SecretFP   string
	Hint       string
	Tier       string
	Hidden     bool
	Restricted bool
	Weight     int
	Priority   int
	Notes      string
	QuotaLimit *int64
	AppIDs     []int64
	CreatedBy  int64
}

var ErrDuplicate = errors.New("duplicate")

func (s *Store) CreateKey(ctx context.Context, nk NewKey) (*Key, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO keys(service_id,label,secret_enc,secret_fp,hint,tier,hidden,restricted,weight,priority,notes,quota_limit,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nk.ServiceID, nk.Label, nk.SecretEnc, nk.SecretFP, nk.Hint, nk.Tier, b2i(nk.Hidden), b2i(nk.Restricted),
		nk.Weight, nk.Priority, nk.Notes, fromInt(nk.QuotaLimit), nk.CreatedBy, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := setKeyApps(ctx, tx, id, nk.AppIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKey(ctx, id)
}

func setKeyApps(ctx context.Context, tx *sql.Tx, keyID int64, appIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM key_apps WHERE key_id=?`, keyID); err != nil {
		return err
	}
	for _, a := range appIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO key_apps(key_id,app_id) VALUES(?,?)`, keyID, a); err != nil {
			return err
		}
	}
	return nil
}

type KeyPatch struct {
	Label      *string
	SecretEnc  []byte
	SecretFP   string
	Hint       string
	Tier       *string
	Hidden     *bool
	Restricted *bool
	AppIDs     *[]int64
	Status     *string
	Weight     *int
	Priority   *int
	Notes      *string
	QuotaLimit **int64
}

func (s *Store) UpdateKey(ctx context.Context, id int64, p KeyPatch) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	set := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, `UPDATE keys SET `+q+` WHERE id=?`, append(args, id)...)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return ErrDuplicate
		}
		return err
	}
	steps := []func() error{}
	if p.Label != nil {
		steps = append(steps, func() error { return set(`label=?`, *p.Label) })
	}
	if p.SecretEnc != nil {
		steps = append(steps, func() error {
			return set(`secret_enc=?, secret_fp=?, hint=?, status='active', consecutive_fails=0, cooldown_until=NULL, last_error=''`, p.SecretEnc, p.SecretFP, p.Hint)
		})
	}
	if p.Tier != nil {
		steps = append(steps, func() error { return set(`tier=?`, *p.Tier) })
	}
	if p.Hidden != nil {
		steps = append(steps, func() error { return set(`hidden=?`, b2i(*p.Hidden)) })
	}
	if p.Restricted != nil {
		steps = append(steps, func() error { return set(`restricted=?`, b2i(*p.Restricted)) })
	}
	if p.Status != nil {
		steps = append(steps, func() error {
			if *p.Status == StatusActive {
				return set(`status='active', cooldown_until=NULL, consecutive_fails=0`)
			}
			return set(`status=?`, *p.Status)
		})
	}
	if p.Weight != nil {
		steps = append(steps, func() error { return set(`weight=?`, *p.Weight) })
	}
	if p.Priority != nil {
		steps = append(steps, func() error { return set(`priority=?`, *p.Priority) })
	}
	if p.Notes != nil {
		steps = append(steps, func() error { return set(`notes=?`, *p.Notes) })
	}
	if p.QuotaLimit != nil {
		steps = append(steps, func() error { return set(`quota_limit=?`, fromInt(*p.QuotaLimit)) })
	}
	for _, st := range steps {
		if err := st(); err != nil {
			return err
		}
	}
	if p.AppIDs != nil {
		if err := setKeyApps(ctx, tx, id, *p.AppIDs); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM keys WHERE id=?`, id)
	return err
}

// MarkUsed records a hand-out of the key.
func (s *Store) MarkUsed(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE keys SET usage_total=usage_total+1, usage_today=usage_today+1, last_used_at=? WHERE id=?`, now(), id)
	return err
}

// KeyEffect is the state change produced by an outcome or health check.
type KeyEffect struct {
	Status           *string
	CooldownUntil    *time.Time
	ClearCooldown    bool
	ConsecutiveFails *int
	QuotaRemaining   **int64
	QuotaLimit       **int64
	QuotaResetAt     **time.Time
	DecrementQuota   bool
	LastError        *string
	Checked          bool
}

func (s *Store) ApplyEffect(ctx context.Context, id int64, e KeyEffect) error {
	sets := []string{}
	args := []any{}
	add := func(q string, a ...any) { sets = append(sets, q); args = append(args, a...) }
	if e.Status != nil {
		add(`status=?`, *e.Status)
	}
	if e.ClearCooldown {
		add(`cooldown_until=NULL`)
	} else if e.CooldownUntil != nil {
		add(`cooldown_until=?`, e.CooldownUntil.Unix())
	}
	if e.ConsecutiveFails != nil {
		add(`consecutive_fails=?`, *e.ConsecutiveFails)
	}
	if e.QuotaRemaining != nil {
		add(`quota_remaining=?`, fromInt(*e.QuotaRemaining))
	} else if e.DecrementQuota {
		add(`quota_remaining=CASE WHEN quota_remaining IS NULL THEN NULL WHEN quota_remaining>0 THEN quota_remaining-1 ELSE 0 END`)
	}
	if e.QuotaLimit != nil {
		add(`quota_limit=?`, fromInt(*e.QuotaLimit))
	}
	if e.QuotaResetAt != nil {
		add(`quota_reset_at=?`, fromTime(*e.QuotaResetAt))
	}
	if e.LastError != nil {
		add(`last_error=?`, *e.LastError)
	}
	if e.Checked {
		add(`last_checked_at=?`, now())
	}
	if len(sets) == 0 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE keys SET `+strings.Join(sets, ", ")+` WHERE id=?`, append(args, id)...)
	return err
}

func (s *Store) ResetDailyUsage(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE keys SET usage_today=0`)
	return err
}

// ReactivateExhausted turns exhausted keys back on when their reset time passed.
func (s *Store) ReactivateExhausted(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE keys SET status='active', consecutive_fails=0, cooldown_until=NULL,
		quota_remaining=quota_limit, quota_reset_at=NULL, last_error=''
		WHERE status='exhausted' AND quota_reset_at IS NOT NULL AND quota_reset_at<=?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
