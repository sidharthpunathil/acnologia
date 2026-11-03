package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const keyCols = `id, service_id, label, secret_enc, hint, tier, hidden, restricted, status, weight, priority, cooldown_until,
consecutive_fails, quota_limit, quota_remaining, quota_reset_at, usage_total, usage_today, last_used_at, last_error,
last_checked_at, notes, created_at, max_concurrency, last_ok_at, egress_id, account_name, account_email, account_phone, account_ref,
peer_link_id, peer_valid_until, peer_remote_key_id`

func scanKey(row interface{ Scan(...any) error }) (*Key, error) {
	var k Key
	var hidden, restricted int
	var cd, ql, qr, qra, lu, lc, mc, lok, eg, pl, pv, pr sql.NullInt64
	var created int64
	err := row.Scan(&k.ID, &k.ServiceID, &k.Label, &k.SecretEnc, &k.Hint, &k.Tier, &hidden, &restricted, &k.Status,
		&k.Weight, &k.Priority, &cd, &k.ConsecutiveFails, &ql, &qr, &qra, &k.UsageTotal, &k.UsageToday, &lu,
		&k.LastError, &lc, &k.Notes, &created, &mc, &lok, &eg, &k.AccountName, &k.AccountEmail, &k.AccountPhone, &k.AccountRef,
		&pl, &pv, &pr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	k.Hidden, k.Restricted = hidden == 1, restricted == 1
	k.CooldownUntil, k.QuotaLimit, k.QuotaRemaining, k.QuotaResetAt = toTime(cd), toInt(ql), toInt(qr), toTime(qra)
	k.LastUsedAt, k.LastCheckedAt = toTime(lu), toTime(lc)
	k.MaxConcurrency = toInt(mc)
	k.LastOKAt = toTime(lok)
	k.EgressID = toInt(eg)
	k.PeerLinkID, k.PeerValidUntil, k.PeerRemoteKeyID = toInt(pl), toTime(pv), toInt(pr)
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
	MaxConc    *int64
	Account    KeyAccount
	// Handover marks a key another server handed over (see handover.go).
	Handover *KeyHandoverRef
}

// KeyHandoverRef ties a handed over key to the peer link it came through,
// the owner's key id and the end of its lease.
type KeyHandoverRef struct {
	LinkID      int64
	RemoteKeyID int64
	ValidUntil  time.Time
}

var ErrDuplicate = errors.New("duplicate")

func (s *Store) CreateKey(ctx context.Context, nk NewKey) (*Key, error) {
	keys, dups, err := s.CreateKeys(ctx, []NewKey{nk})
	if err != nil {
		return nil, err
	}
	if len(dups) > 0 {
		return nil, ErrDuplicate
	}
	return keys[0], nil
}

// CreateKeys inserts keys in one transaction: either every new key is stored
// or none is. Secrets already in their service are skipped and their indexes
// in nks returned as dups; the created keys come back in input order.
func (s *Store) CreateKeys(ctx context.Context, nks []NewKey) ([]*Key, []int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	// Never reuse a deleted key's id: events, leases and in-memory slots are
	// keyed by it, and a reused id would inherit another key's history.
	var hw sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(COALESCE((SELECT CAST(v AS INTEGER) FROM meta WHERE k='keys_seq'),0), COALESCE((SELECT MAX(id) FROM keys),0),
		COALESCE((SELECT MAX(key_id) FROM events),0), COALESCE((SELECT MAX(key_id) FROM leases),0))`).Scan(&hw); err != nil {
		return nil, nil, err
	}
	next := hw.Int64 + 1
	ids, dups := []int64{}, []int{}
	for i, nk := range nks {
		// A failed INSERT (a duplicate) rolls back only that statement, so the
		// transaction stays usable.
		ho := nk.Handover
		_, err := tx.ExecContext(ctx, `INSERT INTO keys(id,service_id,label,secret_enc,secret_fp,hint,tier,hidden,restricted,weight,priority,notes,quota_limit,created_by,created_at,max_concurrency,
		account_name,account_email,account_phone,account_ref,peer_link_id,peer_remote_key_id,peer_valid_until)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			next, nk.ServiceID, nk.Label, nk.SecretEnc, nk.SecretFP, nk.Hint, nk.Tier, b2i(nk.Hidden), b2i(nk.Restricted),
			nk.Weight, nk.Priority, nk.Notes, fromInt(nk.QuotaLimit), nk.CreatedBy, now(), fromInt(nk.MaxConc),
			nk.Account.Name, nk.Account.Email, nk.Account.Phone, nk.Account.Ref, ho.link(), ho.remote(), ho.until())
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				dups = append(dups, i)
				continue
			}
			return nil, nil, err
		}
		if err := setKeyApps(ctx, tx, next, nk.AppIDs); err != nil {
			return nil, nil, err
		}
		ids = append(ids, next)
		next++
	}
	if len(ids) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES('keys_seq',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, fmt.Sprint(ids[len(ids)-1])); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	if len(ids) > 0 {
		s.keysChanged()
	}
	out := make([]*Key, 0, len(ids))
	for _, id := range ids {
		k, err := s.GetKey(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, k)
	}
	return out, dups, nil
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
	MaxConc    **int64
	Account    AccountPatch
}

// KeyAccount says who a key belongs to. Every field is optional.
type KeyAccount struct {
	Name, Email, Phone, Ref string
}

// AccountPatch changes the account fields that are set.
type AccountPatch struct {
	Name, Email, Phone, Ref *string
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
	if p.MaxConc != nil {
		steps = append(steps, func() error { return set(`max_concurrency=?`, fromInt(*p.MaxConc)) })
	}
	for col, v := range map[string]*string{"account_name": p.Account.Name, "account_email": p.Account.Email,
		"account_phone": p.Account.Phone, "account_ref": p.Account.Ref} {
		if v != nil {
			col, v := col, *v
			steps = append(steps, func() error { return set(col+`=?`, v) })
		}
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
	if err := tx.Commit(); err != nil {
		return err
	}
	if p.SecretEnc != nil {
		s.keysChanged()
	}
	return nil
}

// DeleteKey removes a key. Events of a hidden key are deleted with it so they
// do not become visible to admins once the hidden flag is gone.
func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE key_id=? AND EXISTS (SELECT 1 FROM keys WHERE id=? AND hidden=1)`, id, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM keys WHERE id=?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.keysChanged()
	return nil
}

// CountKeys returns the number of stored keys.
func (s *Store) CountKeys(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keys`).Scan(&n)
	return n, err
}

// VerifyAnySecret checks that one stored secret decrypts with the current key.
func (s *Store) VerifyAnySecret(ctx context.Context, decrypt func([]byte) (string, error)) error {
	var blob []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT secret_enc FROM keys ORDER BY id LIMIT 1`).Scan(&blob); err != nil {
		return err
	}
	_, err := decrypt(blob)
	return err
}

// AppsExist reports whether every id names an existing app.
func (s *Store) AppsExist(ctx context.Context, ids []int64) (bool, error) {
	seen := map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if len(seen) == 0 {
		return true, nil
	}
	args := make([]any, 0, len(seen))
	for id := range seen {
		args = append(args, id)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE id IN (`+placeholders(len(args))+`)`, args...).Scan(&n); err != nil {
		return false, err
	}
	return n == len(seen), nil
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
	MarkOK           bool
	LastError        *string
	Checked          bool
}

func (s *Store) ApplyEffect(ctx context.Context, id int64, e KeyEffect) error {
	sets := []string{}
	args := []any{}
	add := func(q string, a ...any) { sets = append(sets, q); args = append(args, a...) }
	if e.Status != nil {
		// Disabled and banned are operator states: a request or check that
		// started before the change must not overwrite them, or recovery
		// would later put the key back into rotation.
		add(`status=CASE WHEN status IN ('disabled','banned') THEN status ELSE ? END`, *e.Status)
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
		add(`last_error=?`, s.Scrub(ctx, *e.LastError))
	}
	if e.Checked {
		add(`last_checked_at=?`, now())
	}
	if e.MarkOK {
		add(`last_ok_at=?`, now())
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
