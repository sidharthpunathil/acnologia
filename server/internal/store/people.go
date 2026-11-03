package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

// Sharing with people who do not run Acnologia. A person share is a share
// whose recipient is someone with a one time claim link instead of another
// server. Redeeming the link either mints a personal app token on the
// share's backing app (proxy mode) or shows the scoped keys once (reveal
// mode). Only the claim code's hash is stored. Reveals are recorded per key
// in share_reveals, the person side twin of share_handovers.

// Share recipients and the person only mode.
const (
	RecipientServer = "server"
	RecipientPerson = "person"
	ShareModeReveal = "reveal"
)

// ClaimPrefix starts every claim code, so the redactor can spot one.
const ClaimPrefix = "acnclaim_"

const schemaPeople = `
CREATE TABLE IF NOT EXISTS share_reveals(
  share_id TEXT NOT NULL,
  key_id INTEGER NOT NULL,
  service TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  revealed_at INTEGER NOT NULL,
  PRIMARY KEY(share_id, key_id)
);
CREATE INDEX IF NOT EXISTS share_reveals_key ON share_reveals(key_id);
`

func migratePeople(db *sql.DB) error {
	for _, c := range []struct{ table, col, def string }{
		{"shares", "recipient", "TEXT NOT NULL DEFAULT 'server'"},
		{"shares", "label", "TEXT NOT NULL DEFAULT ''"},
		{"shares", "email", "TEXT NOT NULL DEFAULT ''"},
		{"shares", "claim_hash", "TEXT NOT NULL DEFAULT ''"},
		{"shares", "claim_expires_at", "INTEGER"},
		{"shares", "claimed_at", "INTEGER"},
	} {
		if _, err := addColumn(db, c.table, c.col, c.def); err != nil {
			return err
		}
	}
	if _, err := db.Exec(schemaPeople); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS shares_claim_hash ON shares(claim_hash) WHERE claim_hash<>''`)
	return err
}

// IsPerson reports whether the share is for a person with a claim link.
func (sh *Share) IsPerson() bool { return sh.Recipient == RecipientPerson }

// IsReveal reports whether the share shows its keys to a person once.
func (sh *Share) IsReveal() bool { return sh.Mode == ShareModeReveal }

var (
	// ErrClaimInvalid means no person share has that claim code.
	ErrClaimInvalid = errors.New("claim link is not valid")
	// ErrClaimUsed means the claim link was already redeemed.
	ErrClaimUsed = errors.New("claim link was already used")
	// ErrClaimExpired means the link ran out, or the share ended before
	// anyone claimed it.
	ErrClaimExpired = errors.New("claim link expired")
)

// ShareByClaim finds the person share a claim code hash belongs to. It
// does not check whether the link is still usable; see CheckClaim.
func (s *Store) ShareByClaim(ctx context.Context, claimHash string) (*Share, error) {
	if claimHash == "" {
		return nil, ErrClaimInvalid
	}
	sh, err := scanShare(s.DB.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares s LEFT JOIN apps a ON a.id=s.app_id
		WHERE s.claim_hash=? AND s.recipient=?`, claimHash, RecipientPerson))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrClaimInvalid
	}
	if err != nil {
		return nil, err
	}
	// The index lookup is exact; compare again in constant time anyway.
	if subtle.ConstantTimeCompare([]byte(sh.claimHash), []byte(claimHash)) != 1 {
		return nil, ErrClaimInvalid
	}
	return sh, nil
}

// CheckClaim says whether a person share's link can be redeemed at t.
func (sh *Share) CheckClaim(t time.Time) error {
	switch {
	case sh.ClaimedAt != nil:
		return ErrClaimUsed
	case sh.Status != SharePending:
		return ErrClaimExpired
	case sh.ClaimExpires != nil && !sh.ClaimExpires.After(t):
		return ErrClaimExpired
	case sh.Expired(t):
		return ErrClaimExpired
	}
	return nil
}

// SetShareClaim replaces an unclaimed person share's claim link.
func (s *Store) SetShareClaim(ctx context.Context, id, claimHash string, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE shares SET claim_hash=?, claim_expires_at=? WHERE id=? AND status=? AND recipient=? AND claimed_at IS NULL`,
		claimHash, expires.Unix(), id, SharePending, RecipientPerson)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClaimUsed
	}
	return nil
}

// RevealRecord is one key shown to a person.
type RevealRecord struct {
	KeyID    int64
	Service  string
	Provider string
	Label    string
}

// ClaimRedeem is what redeeming a claim link writes: for proxy, the
// person's token (prefix and hash); for reveal, the keys shown.
type ClaimRedeem struct {
	TokenPrefix, TokenHash string
	Reveals                []RevealRecord
}

// RedeemClaim burns a claim link and activates the share, in one
// transaction, so a link is redeemed once. A proxy share gets the person's
// app token on its backing app; a reveal share records each key shown.
func (s *Store) RedeemClaim(ctx context.Context, claimHash string, in ClaimRedeem) (*Share, error) {
	if claimHash == "" {
		return nil, ErrClaimInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id, status, mode, label, hash string
	var exp, ends, claimed, app sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id, status, mode, label, claim_hash, claim_expires_at, expires_at, claimed_at, app_id FROM shares
		WHERE claim_hash=? AND recipient=?`, claimHash, RecipientPerson).Scan(&id, &status, &mode, &label, &hash, &exp, &ends, &claimed, &app)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClaimInvalid
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(claimHash)) != 1 || !app.Valid {
		return nil, ErrClaimInvalid
	}
	t := now()
	switch {
	case claimed.Valid:
		return nil, ErrClaimUsed
	case status != SharePending:
		return nil, ErrClaimExpired
	case exp.Valid && exp.Int64 <= t, ends.Valid && ends.Int64 <= t:
		return nil, ErrClaimExpired
	}
	res, err := tx.ExecContext(ctx, `UPDATE shares SET status=?, claimed_at=?, redeemed_at=? WHERE id=? AND status=? AND claimed_at IS NULL`,
		ShareActive, t, t, id, SharePending)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrClaimUsed
	}
	if mode == ShareModeReveal {
		// The keys leave this server here; no token is ever made, so the
		// backing app stays off.
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET active=0 WHERE id=?`, app.Int64); err != nil {
			return nil, err
		}
		for _, r := range in.Reveals {
			if _, err := tx.ExecContext(ctx, `INSERT INTO share_reveals(share_id,key_id,service,provider,label,revealed_at) VALUES(?,?,?,?,?,?)
				ON CONFLICT(share_id,key_id) DO NOTHING`, id, r.KeyID, r.Service, r.Provider, r.Label, t); err != nil {
				return nil, err
			}
		}
	} else {
		if in.TokenHash == "" {
			return nil, errors.New("a proxy claim needs a token")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET token_prefix=?, active=1 WHERE id=?`, in.TokenPrefix, app.Int64); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_tokens(app_id,name,prefix,hash,created_at) VALUES(?,?,?,?,?)`,
			app.Int64, "Person "+label, TokenPrefixHint(in.TokenPrefix), in.TokenHash, t); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateScopes()
	return s.GetShare(ctx, id)
}

// Reveal is the owner's record of one key shown to a person.
type Reveal struct {
	ShareID    string    `json:"share_id"`
	KeyID      int64     `json:"key_id"`
	Service    string    `json:"service"`
	Provider   string    `json:"provider"`
	Label      string    `json:"label"`
	RevealedAt time.Time `json:"revealed_at"`
}

// ListReveals returns the keys a share revealed, by key id.
func (s *Store) ListReveals(ctx context.Context, shareID string) ([]*Reveal, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT share_id, key_id, service, provider, label, revealed_at FROM share_reveals WHERE share_id=? ORDER BY key_id`, shareID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Reveal{}
	for rows.Next() {
		var r Reveal
		var at int64
		if err := rows.Scan(&r.ShareID, &r.KeyID, &r.Service, &r.Provider, &r.Label, &at); err != nil {
			return nil, err
		}
		r.RevealedAt = time.Unix(at, 0).UTC()
		out = append(out, &r)
	}
	return out, rows.Err()
}

// KeyReveal is how the owner's key list shows a key revealed to a person.
type KeyReveal struct {
	ShareID    string    `json:"share_id"`
	ShareName  string    `json:"share_name"`
	Label      string    `json:"label"`
	RevealedAt time.Time `json:"revealed_at"`
}

// KeyReveals returns the reveals of the given keys, by key id, with the
// share's name and the person's label.
func (s *Store) KeyReveals(ctx context.Context, keyIDs []int64) (map[int64][]KeyReveal, error) {
	out := map[int64][]KeyReveal{}
	if len(keyIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keyIDs))
	for _, id := range keyIDs {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.key_id, r.share_id, COALESCE(sh.name,''), COALESCE(sh.label,''), r.revealed_at
		FROM share_reveals r LEFT JOIN shares sh ON sh.id=r.share_id WHERE r.key_id IN (`+placeholders(len(args))+`) ORDER BY r.revealed_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kr KeyReveal
		var kid, at int64
		if err := rows.Scan(&kid, &kr.ShareID, &kr.ShareName, &kr.Label, &at); err != nil {
			return nil, err
		}
		kr.RevealedAt = time.Unix(at, 0).UTC()
		out[kid] = append(out[kid], kr)
	}
	return out, rows.Err()
}
