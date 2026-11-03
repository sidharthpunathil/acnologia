package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Key handover. A handover share seals its keys to the receiver, which calls
// the provider itself. The owner keeps an exposure record per key and share
// (share_handovers): when it was first handed over, last renewed, how long
// the receiver may keep it, and when it is known to be gone. The receiver
// stores handed over keys as ordinary hidden keys tied to the peer link
// (keys.peer_link_id) with the end of their lease (keys.peer_valid_until),
// and deletes them, row and ciphertext, when the lease ends.

const schemaHandover = `
CREATE TABLE IF NOT EXISTS share_handovers(
  share_id TEXT NOT NULL,
  key_id INTEGER NOT NULL,
  peer_id TEXT NOT NULL DEFAULT '',
  service TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  handed_at INTEGER NOT NULL,
  renewed_at INTEGER NOT NULL,
  valid_until INTEGER NOT NULL,
  wiped_at INTEGER,
  wipe_reason TEXT NOT NULL DEFAULT '',
  reported_requests INTEGER,
  reported_status TEXT NOT NULL DEFAULT '',
  reported_at INTEGER,
  PRIMARY KEY(share_id, key_id)
);
CREATE INDEX IF NOT EXISTS share_handovers_key ON share_handovers(key_id);
CREATE TABLE IF NOT EXISTS peer_nonces(
  scope TEXT NOT NULL,
  nonce TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY(scope, nonce)
);
`

func migrateHandover(db *sql.DB) error {
	if _, err := db.Exec(schemaHandover); err != nil {
		return err
	}
	for _, c := range []struct{ table, col, def string }{
		{"shares", "mode", "TEXT NOT NULL DEFAULT 'proxy'"},
		{"shares", "lease_sec", "INTEGER NOT NULL DEFAULT 86400"},
		{"shares", "end_action", "TEXT NOT NULL DEFAULT 'expire'"},
		{"shares", "peer_box_public_key", "TEXT NOT NULL DEFAULT ''"},
		{"shares", "private_network", "INTEGER NOT NULL DEFAULT 0"},
		{"shares", "revoke_push_at", "INTEGER"},
		{"shares", "revoke_push_ok", "INTEGER"},
		{"shares", "revoke_push_detail", "TEXT NOT NULL DEFAULT ''"},
		{"keys", "peer_link_id", "INTEGER"},
		{"keys", "peer_valid_until", "INTEGER"},
		{"keys", "peer_remote_key_id", "INTEGER"},
		{"peer_links", "mode", "TEXT NOT NULL DEFAULT 'proxy'"},
	} {
		if _, err := addColumn(db, c.table, c.col, c.def); err != nil {
			return err
		}
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS keys_peer_link ON keys(peer_link_id) WHERE peer_link_id IS NOT NULL`)
	return err
}

// normalize fills the mode, lease and end action defaults.
func (sh *Share) normalize() {
	if sh.Mode != ShareModeHandover {
		sh.Mode = ShareModeProxy
	}
	if sh.LeaseSec <= 0 {
		sh.LeaseSec = DefaultLeaseSec
	}
	if sh.EndAction != EndRevoke {
		sh.EndAction = EndExpire
	}
}

func linkMode(m string) string {
	if m == ShareModeHandover {
		return m
	}
	return ShareModeProxy
}

// ---- owner side: exposure records ----

// Handover is the owner's record of one key handed over by one share.
type Handover struct {
	ShareID    string     `json:"share_id"`
	KeyID      int64      `json:"key_id"`
	PeerID     string     `json:"peer_id"`
	Service    string     `json:"service"`
	Provider   string     `json:"provider"`
	Label      string     `json:"label"`
	HandedAt   time.Time  `json:"handed_at"`
	RenewedAt  time.Time  `json:"renewed_at"`
	ValidUntil time.Time  `json:"valid_until"`
	WipedAt    *time.Time `json:"wiped_at"`
	WipeReason string     `json:"wipe_reason"`
	// Reported* is what the receiver says about the key (cooperative).
	ReportedRequests *int64     `json:"reported_requests"`
	ReportedStatus   string     `json:"reported_status"`
	ReportedAt       *time.Time `json:"reported_at"`
}

// Live reports whether the receiver may still hold the key.
func (h *Handover) Live() bool { return h.WipedAt == nil }

// KeyHandover is how the owner's key list shows a handover of one key.
type KeyHandover struct {
	ShareID        string     `json:"share_id"`
	ShareName      string     `json:"share_name"`
	PeerID         string     `json:"peer_id"`
	PeerName       string     `json:"peer_name"`
	Live           bool       `json:"live"`
	HandedAt       time.Time  `json:"handed_at"`
	ValidUntil     time.Time  `json:"valid_until"`
	WipedAt        *time.Time `json:"wiped_at"`
	ReportedStatus string     `json:"reported_status"`
}

// HandoverRecord is one key delivered to a receiver.
type HandoverRecord struct {
	KeyID      int64
	Service    string
	Provider   string
	Label      string
	ValidUntil time.Time
}

const handoverCols = `share_id, key_id, peer_id, service, provider, label, handed_at, renewed_at, valid_until, wiped_at, wipe_reason,
reported_requests, reported_status, reported_at`

func scanHandover(row interface{ Scan(...any) error }) (*Handover, error) {
	var h Handover
	var handed, renewed, valid int64
	var wiped, rr, ra sql.NullInt64
	if err := row.Scan(&h.ShareID, &h.KeyID, &h.PeerID, &h.Service, &h.Provider, &h.Label, &handed, &renewed, &valid, &wiped, &h.WipeReason,
		&rr, &h.ReportedStatus, &ra); err != nil {
		return nil, err
	}
	h.HandedAt, h.RenewedAt, h.ValidUntil = time.Unix(handed, 0).UTC(), time.Unix(renewed, 0).UTC(), time.Unix(valid, 0).UTC()
	h.WipedAt, h.ReportedRequests, h.ReportedAt = toTime(wiped), toInt(rr), toTime(ra)
	return &h, nil
}

// RecordHandover notes that a key was delivered or renewed. first is true
// when the receiver did not hold it before: a new record, or one whose key
// was wiped since, so callers audit deliveries and not every renewal.
func (s *Store) RecordHandover(ctx context.Context, shareID, peerID string, r HandoverRecord) (first bool, err error) {
	t := now()
	var wiped sql.NullInt64
	err = s.DB.QueryRowContext(ctx, `SELECT wiped_at FROM share_handovers WHERE share_id=? AND key_id=?`, shareID, r.KeyID).Scan(&wiped)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.DB.ExecContext(ctx, `INSERT INTO share_handovers(share_id,key_id,peer_id,service,provider,label,handed_at,renewed_at,valid_until)
			VALUES(?,?,?,?,?,?,?,?,?)`, shareID, r.KeyID, peerID, r.Service, r.Provider, r.Label, t, t, r.ValidUntil.Unix())
		return err == nil, err
	case err != nil:
		return false, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE share_handovers SET peer_id=?, service=?, provider=?, label=?, renewed_at=?, valid_until=?,
		handed_at=CASE WHEN wiped_at IS NULL THEN handed_at ELSE ? END, wiped_at=NULL, wipe_reason='' WHERE share_id=? AND key_id=?`,
		peerID, r.Service, r.Provider, r.Label, t, r.ValidUntil.Unix(), t, shareID, r.KeyID)
	return wiped.Valid, err
}

// ListHandovers returns a share's exposure records, by key id.
func (s *Store) ListHandovers(ctx context.Context, shareID string) ([]*Handover, error) {
	return s.queryHandovers(ctx, `WHERE share_id=? ORDER BY key_id`, shareID)
}

func (s *Store) queryHandovers(ctx context.Context, where string, args ...any) ([]*Handover, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+handoverCols+` FROM share_handovers `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Handover{}
	for rows.Next() {
		h, err := scanHandover(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// MarkHandoversWiped records that the receiver no longer holds keys of a
// share: the given ones, or every live one when keyIDs is nil. It returns
// the records it changed.
func (s *Store) MarkHandoversWiped(ctx context.Context, shareID string, keyIDs []int64, reason string, at time.Time) ([]*Handover, error) {
	live, err := s.queryHandovers(ctx, `WHERE share_id=? AND wiped_at IS NULL ORDER BY key_id`, shareID)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range keyIDs {
		want[id] = true
	}
	out := []*Handover{}
	for _, h := range live {
		if keyIDs != nil && !want[h.KeyID] {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE share_handovers SET wiped_at=?, wipe_reason=? WHERE share_id=? AND key_id=? AND wiped_at IS NULL`,
			at.Unix(), reason, shareID, h.KeyID); err != nil {
			return out, err
		}
		t := at.UTC()
		h.WipedAt, h.WipeReason = &t, reason
		out = append(out, h)
	}
	return out, nil
}

// LapsedHandovers returns live records whose lease ended before t: a
// cooperating receiver has deleted those keys on its own.
func (s *Store) LapsedHandovers(ctx context.Context, t time.Time) ([]*Handover, error) {
	return s.queryHandovers(ctx, `WHERE wiped_at IS NULL AND valid_until<=? ORDER BY share_id, key_id`, t.Unix())
}

// ReportedKey is one key a receiver says it holds.
type ReportedKey struct {
	KeyID    int64
	Requests int64
	Status   string
}

// ApplyHandoverReport stores a receiver's report: request counts and status
// of the keys it holds. Live records of keys it no longer holds are marked
// wiped. It returns those.
func (s *Store) ApplyHandoverReport(ctx context.Context, shareID string, held []ReportedKey) ([]*Handover, error) {
	t := now()
	ids := []int64{}
	for _, k := range held {
		if _, err := s.DB.ExecContext(ctx, `UPDATE share_handovers SET reported_requests=?, reported_status=?, reported_at=? WHERE share_id=? AND key_id=?`,
			max(k.Requests, 0), k.Status, t, shareID, k.KeyID); err != nil {
			return nil, err
		}
		ids = append(ids, k.KeyID)
	}
	live, err := s.queryHandovers(ctx, `WHERE share_id=? AND wiped_at IS NULL`, shareID)
	if err != nil {
		return nil, err
	}
	have := map[int64]bool{}
	for _, id := range ids {
		have[id] = true
	}
	gone := []int64{}
	for _, h := range live {
		if !have[h.KeyID] {
			gone = append(gone, h.KeyID)
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	return s.MarkHandoversWiped(ctx, shareID, gone, "reported deleted by the other server", time.Unix(t, 0))
}

// KeyHandovers returns the handovers of the given keys, by key id, with
// their share's name and peer.
func (s *Store) KeyHandovers(ctx context.Context, keyIDs []int64) (map[int64][]KeyHandover, error) {
	out := map[int64][]KeyHandover{}
	if len(keyIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keyIDs))
	for _, id := range keyIDs {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT h.key_id, h.share_id, COALESCE(sh.name,''), h.peer_id, COALESCE(sh.peer_name,''), h.handed_at, h.valid_until, h.wiped_at, h.reported_status
		FROM share_handovers h LEFT JOIN shares sh ON sh.id=h.share_id WHERE h.key_id IN (`+placeholders(len(args))+`) ORDER BY h.handed_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kh KeyHandover
		var kid, handed, valid int64
		var wiped sql.NullInt64
		if err := rows.Scan(&kid, &kh.ShareID, &kh.ShareName, &kh.PeerID, &kh.PeerName, &handed, &valid, &wiped, &kh.ReportedStatus); err != nil {
			return nil, err
		}
		kh.HandedAt, kh.ValidUntil, kh.WipedAt = time.Unix(handed, 0).UTC(), time.Unix(valid, 0).UTC(), toTime(wiped)
		kh.Live = kh.WipedAt == nil
		out[kid] = append(out[kid], kh)
	}
	return out, rows.Err()
}

// SetRevokePush records the outcome of a revoke push on a share.
func (s *Store) SetRevokePush(ctx context.Context, shareID string, ok bool, detail string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE shares SET revoke_push_at=?, revoke_push_ok=?, revoke_push_detail=? WHERE id=?`,
		now(), b2i(ok), s.Scrub(ctx, detail), shareID)
	return err
}

// EndedShares returns active or paused shares whose timer passed at t.
func (s *Store) EndedShares(ctx context.Context, t time.Time) ([]*Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+shareCols+` FROM shares s LEFT JOIN apps a ON a.id=s.app_id
		WHERE s.status IN ('active','paused') AND s.expires_at IS NOT NULL AND s.expires_at<=? ORDER BY s.id`, t.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// ---- receiver side: handed over keys ----

// HandoverKeys returns the keys handed over through a link.
func (s *Store) HandoverKeys(ctx context.Context, linkID int64) ([]*Key, error) {
	return s.queryKeys(ctx, `WHERE peer_link_id=? ORDER BY id`, linkID)
}

// ExpiredHandoverKeys returns handed over keys whose lease ended at t.
func (s *Store) ExpiredHandoverKeys(ctx context.Context, t time.Time) ([]*Key, error) {
	return s.queryKeys(ctx, `WHERE peer_link_id IS NOT NULL AND (peer_valid_until IS NULL OR peer_valid_until<=?) ORDER BY id`, t.Unix())
}

// RenewHandoverKey moves a handed over key's lease end, and replaces its
// secret when the owner sent a different one (secretEnc not nil).
func (s *Store) RenewHandoverKey(ctx context.Context, id int64, validUntil time.Time, secretEnc []byte, fp, hint string) error {
	if secretEnc != nil {
		_, err := s.DB.ExecContext(ctx, `UPDATE keys SET peer_valid_until=?, secret_enc=?, secret_fp=?, hint=?, status='active', consecutive_fails=0,
			cooldown_until=NULL, last_error='' WHERE id=? AND peer_link_id IS NOT NULL`, validUntil.Unix(), secretEnc, fp, hint, id)
		if err == nil {
			s.keysChanged()
		}
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE keys SET peer_valid_until=? WHERE id=? AND peer_link_id IS NOT NULL`, validUntil.Unix(), id)
	return err
}

// WipeKeys deletes keys for good: the ciphertext is overwritten with zeros
// first, then the row, its app list, leases and events go. It is for handed
// over keys, which must not linger once their lease or share ends.
func (s *Store) WipeKeys(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE keys SET secret_enc=zeroblob(length(secret_enc)), hint='', secret_fp='wiped:'||id WHERE id=?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE key_id=?`, id); err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM keys WHERE id=?`, id)
		if err != nil {
			return 0, err
		}
		if c, _ := res.RowsAffected(); c > 0 {
			n++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.keysChanged()
	return n, nil
}

// UseNonce records a nonce for scope until expires. It returns false when
// the nonce was already used (a replay). Expired nonces are forgotten.
func (s *Store) UseNonce(ctx context.Context, scope, nonce string, expires time.Time) (bool, error) {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM peer_nonces WHERE expires_at<?`, now()); err != nil {
		return false, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO peer_nonces(scope,nonce,expires_at) VALUES(?,?,?) ON CONFLICT(scope,nonce) DO NOTHING`,
		scope, nonce, expires.Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (r *KeyHandoverRef) link() any {
	if r == nil {
		return nil
	}
	return r.LinkID
}

func (r *KeyHandoverRef) remote() any {
	if r == nil {
		return nil
	}
	return r.RemoteKeyID
}

func (r *KeyHandoverRef) until() any {
	if r == nil {
		return nil
	}
	return r.ValidUntil.Unix()
}

// PeerLinkByShare finds the link for an owner's share id.
func (s *Store) PeerLinkByShare(ctx context.Context, peerID, remoteShareID string) (*PeerLink, error) {
	return scanPeerLink(s.DB.QueryRowContext(ctx, `SELECT `+peerLinkCols+` FROM peer_links WHERE peer_id=? AND remote_share_id=?`, peerID, remoteShareID))
}
