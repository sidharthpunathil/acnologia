package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Sharing between servers. The owner keeps a share per peer it gives access
// to; the share is backed by an app row (apps.share_id) so tokens, limits and
// usage reuse the app machinery. The receiver keeps a peer link per share it
// redeemed, and creates local services that proxy through the owner.

// Share statuses.
const (
	SharePending = "pending"
	ShareActive  = "active"
	SharePaused  = "paused"
	ShareRevoked = "revoked"
	ShareExpired = "expired"
)

// ShareNoServices is the allowed_services entry of a share app that shares
// no services. An app with an empty list may use every service, so a share
// never has one; this entry is not a valid service or group slug and so
// matches nothing.
const ShareNoServices = "!share-no-services"

// Share is one bundle this server gives another server.
type Share struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Note          string     `json:"note"`
	Status        string     `json:"status"`
	Services      []string   `json:"services"`
	Providers     []string   `json:"providers"`
	IncludeHidden bool       `json:"include_hidden"`
	AppID         *int64     `json:"app_id"`
	PeerID        string     `json:"peer_id"`
	PeerName      string     `json:"peer_name"`
	PeerURL       string     `json:"peer_url"`
	PeerPublicKey string     `json:"peer_public_key"`
	InviteExpires *time.Time `json:"invite_expires_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedBy     *int64     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	RedeemedAt    *time.Time `json:"redeemed_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	// LeftAt is set when the peer disconnected on its side.
	LeftAt *time.Time `json:"left_at"`
	// Limits are the backing app's limits.
	Limits AppLimits `json:"limits"`
	// KeyScopes limits which keys of each shared service the share may use,
	// by service slug. A missing entry means every key.
	KeyScopes map[string]KeyScope `json:"key_scopes"`
	// Mode is ShareModeProxy or ShareModeHandover, fixed once redeemed.
	Mode string `json:"mode"`
	// LeaseSec is how long a handed over key stays valid on the receiver
	// without renewal (handover only).
	LeaseSec int `json:"lease_sec"`
	// EndAction is what happens when ExpiresAt (the timer, ends_at in the
	// API) passes: EndExpire or EndRevoke.
	EndAction string `json:"end_action"`
	// PeerBoxPublicKey is the receiver's X25519 key, recorded at redeem;
	// handed over keys are sealed to it.
	PeerBoxPublicKey string `json:"peer_box_public_key"`
	// PrivateNetwork lets the revoke push reach a receiver on a private
	// address (a superadmin's call, like a link's trusted flag).
	PrivateNetwork bool `json:"private_network"`
	// LastRevokePush is the outcome of the last revoke push, or nil.
	LastRevokePush *RevokePush `json:"-"`
	// Recipient is RecipientServer (another Acnologia) or RecipientPerson
	// (someone with a claim link). Fixed at creation.
	Recipient string `json:"recipient"`
	// Label names a person recipient; Email is an optional label too and
	// is never used to send anything.
	Label string `json:"label"`
	Email string `json:"email"`
	// ClaimExpires is when an unclaimed person share's claim link stops
	// working; ClaimedAt is when the person redeemed it.
	ClaimExpires *time.Time `json:"claim_expires_at"`
	ClaimedAt    *time.Time `json:"claimed_at"`
	// Reusable makes the claim link work for anyone who has it, each
	// claim a token of its own, up to MaxClaims (0: no cap). ClaimCount is
	// how many times it was redeemed. Fixed at creation.
	Reusable   bool `json:"reusable"`
	MaxClaims  int  `json:"max_claims"`
	ClaimCount int  `json:"claim_count"`

	inviteHash string
	claimHash  string
}

// RevokePush is the outcome of telling a receiver to delete handed over keys.
type RevokePush struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Detail string    `json:"detail"`
}

// Share modes and timer end actions.
const (
	ShareModeProxy    = "proxy"
	ShareModeHandover = "handover"
	EndExpire         = "expire"
	EndRevoke         = "revoke"
	// DefaultLeaseSec is the default handover lease, 24 hours.
	DefaultLeaseSec = 24 * 60 * 60
)

// IsHandover reports whether the share hands keys over.
func (sh *Share) IsHandover() bool { return sh.Mode == ShareModeHandover }

// HasInvite reports whether an unredeemed invite is outstanding.
func (sh *Share) HasInvite() bool { return sh.inviteHash != "" }

// Expired reports whether the share's own expiry has passed at t.
func (sh *Share) Expired(t time.Time) bool { return sh.ExpiresAt != nil && !sh.ExpiresAt.After(t) }

// PeerLink is a share another server gave this one.
type PeerLink struct {
	ID            int64           `json:"id"`
	PeerID        string          `json:"peer_id"`
	PeerName      string          `json:"peer_name"`
	PeerURL       string          `json:"peer_url"`
	PeerPublicKey string          `json:"peer_public_key"`
	RemoteShareID string          `json:"remote_share_id"`
	Status        string          `json:"status"`
	Manifest      json.RawMessage `json:"manifest"`
	Prefix        string          `json:"prefix"`
	Trusted       bool            `json:"trusted"`
	LastSyncAt    *time.Time      `json:"last_sync_at"`
	LastError     string          `json:"last_error"`
	CreatedBy     *int64          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	// Services maps a remote service slug to the local service made for it.
	Services map[string]int64 `json:"services"`
	// Providers lists provider slugs imported from this peer.
	Providers []string `json:"providers"`
	// Mode is the share's mode: proxy or handover.
	Mode string `json:"mode"`

	TokenEnc []byte `json:"-"`
}

const schemaSharing = `
CREATE TABLE IF NOT EXISTS shares(
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  services TEXT NOT NULL DEFAULT '[]',
  providers TEXT NOT NULL DEFAULT '[]',
  include_hidden INTEGER NOT NULL DEFAULT 0,
  app_id INTEGER REFERENCES apps(id) ON DELETE SET NULL,
  peer_id TEXT NOT NULL DEFAULT '',
  peer_name TEXT NOT NULL DEFAULT '',
  peer_url TEXT NOT NULL DEFAULT '',
  peer_public_key TEXT NOT NULL DEFAULT '',
  invite_hash TEXT NOT NULL DEFAULT '',
  invite_expires_at INTEGER,
  expires_at INTEGER,
  created_by INTEGER,
  created_at INTEGER NOT NULL,
  redeemed_at INTEGER,
  revoked_at INTEGER,
  left_at INTEGER
);
CREATE TABLE IF NOT EXISTS peer_links(
  id INTEGER PRIMARY KEY,
  peer_id TEXT NOT NULL,
  peer_name TEXT NOT NULL DEFAULT '',
  peer_url TEXT NOT NULL,
  peer_public_key TEXT NOT NULL,
  remote_share_id TEXT NOT NULL,
  token_enc BLOB,
  status TEXT NOT NULL,
  manifest TEXT NOT NULL DEFAULT '{}',
  prefix TEXT NOT NULL,
  trusted INTEGER NOT NULL DEFAULT 0,
  last_sync_at INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  created_by INTEGER,
  created_at INTEGER NOT NULL,
  services TEXT NOT NULL DEFAULT '{}',
  providers TEXT NOT NULL DEFAULT '[]',
  UNIQUE(peer_id, remote_share_id)
);
`

func migrateSharing(db *sql.DB) error {
	if _, err := db.Exec(schemaSharing); err != nil {
		return err
	}
	for _, c := range []struct{ table, col, def string }{
		{"apps", "share_id", "TEXT"},
		{"services", "peer_link_id", "INTEGER"},
		{"services", "peer_remote_slug", "TEXT NOT NULL DEFAULT ''"},
	} {
		if _, err := addColumn(db, c.table, c.col, c.def); err != nil {
			return err
		}
	}
	if err := migrateKeyScopes(db); err != nil {
		return err
	}
	if err := migrateShareServiceIDs(db); err != nil {
		return err
	}
	if err := migrateHandover(db); err != nil {
		return err
	}
	return migratePeople(db)
}

var (
	// ErrInviteInvalid means no pending share matches the invite.
	ErrInviteInvalid = errors.New("invite is not valid")
	// ErrInviteUsed means the share was already redeemed.
	ErrInviteUsed = errors.New("invite was already used")
	// ErrInviteExpired means the invite's validity ran out.
	ErrInviteExpired = errors.New("invite expired")
)

const shareCols = `s.id, s.name, s.note, s.status, s.services, s.providers, s.include_hidden, s.app_id, s.peer_id, s.peer_name, s.peer_url,
s.peer_public_key, s.invite_hash, s.invite_expires_at, s.expires_at, s.created_by, s.created_at, s.redeemed_at, s.revoked_at, s.left_at,
s.key_scopes, a.daily_requests, a.monthly_requests, a.daily_budget, a.monthly_budget,
s.mode, s.lease_sec, s.end_action, s.peer_box_public_key, s.private_network, s.revoke_push_at, s.revoke_push_ok, s.revoke_push_detail,
s.recipient, s.label, s.email, s.claim_hash, s.claim_expires_at, s.claimed_at, s.reusable, s.max_claims, s.claim_count`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	var sh Share
	var svcs, provs, scopes, pushDetail string
	var hidden, private, reusable int
	var created int64
	var app, invExp, exp, by, red, rev, left, dr, mr, pushAt, pushOK, claimExp, claimed sql.NullInt64
	var db, mb sql.NullFloat64
	err := row.Scan(&sh.ID, &sh.Name, &sh.Note, &sh.Status, &svcs, &provs, &hidden, &app, &sh.PeerID, &sh.PeerName, &sh.PeerURL,
		&sh.PeerPublicKey, &sh.inviteHash, &invExp, &exp, &by, &created, &red, &rev, &left, &scopes, &dr, &mr, &db, &mb,
		&sh.Mode, &sh.LeaseSec, &sh.EndAction, &sh.PeerBoxPublicKey, &private, &pushAt, &pushOK, &pushDetail,
		&sh.Recipient, &sh.Label, &sh.Email, &sh.claimHash, &claimExp, &claimed, &reusable, &sh.MaxClaims, &sh.ClaimCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sh.Services, sh.Providers = []string{}, []string{}
	_ = json.Unmarshal([]byte(svcs), &sh.Services)
	_ = json.Unmarshal([]byte(provs), &sh.Providers)
	sh.IncludeHidden = hidden == 1
	sh.PrivateNetwork = private == 1
	sh.Reusable = reusable == 1
	if pushAt.Valid {
		sh.LastRevokePush = &RevokePush{At: time.Unix(pushAt.Int64, 0).UTC(), OK: pushOK.Int64 == 1, Detail: pushDetail}
	}
	sh.KeyScopes = decodeScopes(scopes)
	sh.AppID, sh.CreatedBy = toInt(app), toInt(by)
	sh.InviteExpires, sh.ExpiresAt = toTime(invExp), toTime(exp)
	sh.RedeemedAt, sh.RevokedAt, sh.LeftAt = toTime(red), toTime(rev), toTime(left)
	sh.ClaimExpires, sh.ClaimedAt = toTime(claimExp), toTime(claimed)
	sh.CreatedAt = time.Unix(created, 0).UTC()
	sh.Limits.DailyRequests, sh.Limits.MonthlyRequests = toInt(dr), toInt(mr)
	if db.Valid {
		sh.Limits.DailyBudget = &db.Float64
	}
	if mb.Valid {
		sh.Limits.MonthlyBudget = &mb.Float64
	}
	return &sh, nil
}

// shareAllowed is the backing app's allowed_services for a share: the shared
// services, or ShareNoServices when there are none. Never empty.
func shareAllowed(services []string) string {
	list := nonNil(services)
	if len(list) == 0 {
		list = []string{ShareNoServices}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// CreateShare stores a pending share with its invite hash and creates the
// backing app (proxy only, no tokens yet) in one transaction. For a share
// with a person, inviteHash is the claim code's hash and sh.ClaimExpires
// its end; no server invite exists.
func (s *Store) CreateShare(ctx context.Context, sh *Share, inviteHash string, createdBy int64) (*Share, error) {
	svcs, _ := json.Marshal(nonNil(sh.Services))
	provs, _ := json.Marshal(nonNil(sh.Providers))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t := now()
	// apps.token_hash is unique and unused; give it a value no token hashes to.
	res, err := tx.ExecContext(ctx, `INSERT INTO apps(name,description,token_hash,token_prefix,allowed_services,mode,active,created_at,lease_hidden,share_id)
		VALUES(?,?,?,?,?,'proxy',1,?,?,?)`,
		"Share: "+sh.Name, "Backs the share "+sh.ID+". Managed on the Sharing page.", "share:"+sh.ID, "", shareAllowed(sh.Services), t, b2i(sh.IncludeHidden), sh.ID)
	if err != nil {
		return nil, err
	}
	appID, _ := res.LastInsertId()
	f := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	l := sh.Limits
	if _, err := tx.ExecContext(ctx, `UPDATE apps SET daily_requests=?, monthly_requests=?, daily_budget=?, monthly_budget=? WHERE id=?`,
		fromInt(l.DailyRequests), fromInt(l.MonthlyRequests), f(l.DailyBudget), f(l.MonthlyBudget), appID); err != nil {
		return nil, err
	}
	sh.normalize()
	invHash, claimHash := inviteHash, ""
	invExp, claimExp := sh.InviteExpires, (*time.Time)(nil)
	if sh.IsPerson() {
		invHash, claimHash = "", inviteHash
		invExp, claimExp = nil, sh.ClaimExpires
	}
	ids, err := bindServiceIDs(ctx, tx, sh.Services, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO shares(id,name,note,status,services,providers,include_hidden,app_id,invite_hash,invite_expires_at,expires_at,created_by,created_at,key_scopes,
		mode,lease_sec,end_action,private_network,recipient,label,email,claim_hash,claim_expires_at,service_ids,reusable,max_claims)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sh.ID, sh.Name, sh.Note, SharePending, string(svcs), string(provs), b2i(sh.IncludeHidden), appID, invHash,
		fromTime(invExp), fromTime(sh.ExpiresAt), nz(createdBy), t, encodeScopes(sh.KeyScopes),
		sh.Mode, sh.LeaseSec, sh.EndAction, b2i(sh.PrivateNetwork), sh.Recipient, sh.Label, sh.Email, claimHash, fromTime(claimExp), ids, b2i(sh.Reusable), sh.MaxClaims); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateScopes()
	return s.GetShare(ctx, sh.ID)
}

// GetShare returns one share.
func (s *Store) GetShare(ctx context.Context, id string) (*Share, error) {
	return scanShare(s.DB.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares s LEFT JOIN apps a ON a.id=s.app_id WHERE s.id=?`, id))
}

// ListShares returns every share, newest first.
func (s *Store) ListShares(ctx context.Context) ([]*Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+shareCols+` FROM shares s LEFT JOIN apps a ON a.id=s.app_id ORDER BY s.created_at DESC, s.rowid DESC`)
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

// UpdateShare saves a share's editable fields and mirrors them onto the
// backing app: allowed services, hidden key access, name and limits. An
// expired share whose expiry moved into the future becomes active again.
func (s *Store) UpdateShare(ctx context.Context, sh *Share) error {
	svcs, _ := json.Marshal(nonNil(sh.Services))
	provs, _ := json.Marshal(nonNil(sh.Providers))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status := sh.Status
	if status == ShareExpired && !sh.Expired(time.Now()) {
		status = ShareActive
	}
	sh.normalize()
	var oldIDs string
	if err := tx.QueryRowContext(ctx, `SELECT service_ids FROM shares WHERE id=?`, sh.ID).Scan(&oldIDs); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ids, err := bindServiceIDs(ctx, tx, sh.Services, decodeServiceIDs(oldIDs))
	if err != nil {
		return err
	}
	// The mode is fixed once redeemed: only a pending share's mode changes.
	if _, err := tx.ExecContext(ctx, `UPDATE shares SET name=?, note=?, services=?, providers=?, include_hidden=?, expires_at=?, status=?, key_scopes=?,
		mode=CASE WHEN status='pending' THEN ? ELSE mode END, lease_sec=?, end_action=?, private_network=?, label=?, email=?, service_ids=?, max_claims=? WHERE id=?`,
		sh.Name, sh.Note, string(svcs), string(provs), b2i(sh.IncludeHidden), fromTime(sh.ExpiresAt), status, encodeScopes(sh.KeyScopes),
		sh.Mode, sh.LeaseSec, sh.EndAction, b2i(sh.PrivateNetwork), sh.Label, sh.Email, ids, sh.MaxClaims, sh.ID); err != nil {
		return err
	}
	if sh.AppID != nil {
		f := func(p *float64) any {
			if p == nil {
				return nil
			}
			return *p
		}
		l := sh.Limits
		active := status == ShareActive || status == SharePending
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET name=?, allowed_services=?, lease_hidden=?, active=?,
			daily_requests=?, monthly_requests=?, daily_budget=?, monthly_budget=? WHERE id=?`,
			"Share: "+sh.Name, shareAllowed(sh.Services), b2i(sh.IncludeHidden), b2i(active),
			fromInt(l.DailyRequests), fromInt(l.MonthlyRequests), f(l.DailyBudget), f(l.MonthlyBudget), *sh.AppID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateScopes()
	sh.Status = status
	return nil
}

// SetShareInvite replaces a pending share's invite.
func (s *Store) SetShareInvite(ctx context.Context, id, inviteHash string, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE shares SET invite_hash=?, invite_expires_at=? WHERE id=? AND status=?`, inviteHash, expires.Unix(), id, SharePending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInviteUsed
	}
	return nil
}

// ShareInviteMatches compares an invite hash with the share's, in constant time.
func (s *Store) ShareInviteMatches(ctx context.Context, id, inviteHash string) bool {
	var h string
	if err := s.DB.QueryRowContext(ctx, `SELECT invite_hash FROM shares WHERE id=?`, id).Scan(&h); err != nil || h == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h), []byte(inviteHash)) == 1
}

// PeerBinding is the peer a share is redeemed by.
type PeerBinding struct {
	ID, Name, URL, PublicKey string
	// BoxPublicKey is the peer's X25519 key, for handover shares.
	BoxPublicKey string
}

// RedeemShare binds a pending share to a peer, burns the invite and stores
// the share token's hash, all in one transaction, so an invite can be
// redeemed once.
func (s *Store) RedeemShare(ctx context.Context, id, inviteHash string, p PeerBinding, tokenPrefix, tokenHash string) (*Share, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status, hash string
	var exp sql.NullInt64
	var app sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT status, invite_hash, invite_expires_at, app_id FROM shares WHERE id=?`, id).Scan(&status, &hash, &exp, &app)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != SharePending || hash == "" {
		return nil, ErrInviteUsed
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(inviteHash)) != 1 || !app.Valid {
		return nil, ErrInviteInvalid
	}
	t := now()
	if exp.Valid && exp.Int64 <= t {
		return nil, ErrInviteExpired
	}
	res, err := tx.ExecContext(ctx, `UPDATE shares SET status=?, peer_id=?, peer_name=?, peer_url=?, peer_public_key=?, peer_box_public_key=?, invite_hash='', redeemed_at=?
		WHERE id=? AND status=? AND invite_hash=?`, ShareActive, p.ID, p.Name, p.URL, p.PublicKey, p.BoxPublicKey, t, id, SharePending, inviteHash)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrInviteUsed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE apps SET token_prefix=?, active=1 WHERE id=?`, tokenPrefix, app.Int64); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_tokens(app_id,name,prefix,hash,created_at) VALUES(?,?,?,?,?)`,
		app.Int64, "Peer "+p.ID, TokenPrefixHint(tokenPrefix), tokenHash, t); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetShare(ctx, id)
}

// SetShareStatus moves a share to a new status and switches its backing app
// in the same transaction: active turns the app on; paused and expired turn
// it off; revoked turns it off and revokes every token, for good. A revoked
// share never changes status again.
func (s *Store) SetShareStatus(ctx context.Context, id, status string) (*Share, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var cur string
	var app sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT status, app_id FROM shares WHERE id=?`, id).Scan(&cur, &app)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if cur == ShareRevoked {
		return nil, ErrShareRevoked
	}
	t := now()
	switch status {
	case ShareRevoked:
		_, err = tx.ExecContext(ctx, `UPDATE shares SET status=?, revoked_at=?, invite_hash='' WHERE id=?`, status, t, id)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE shares SET status=? WHERE id=?`, status, id)
	}
	if err != nil {
		return nil, err
	}
	if app.Valid {
		active := status == ShareActive
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET active=? WHERE id=?`, b2i(active), app.Int64); err != nil {
			return nil, err
		}
		if status == ShareRevoked {
			if _, err := tx.ExecContext(ctx, `UPDATE app_tokens SET revoked_at=? WHERE app_id=? AND revoked_at IS NULL`, t, app.Int64); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateScopes()
	return s.GetShare(ctx, id)
}

// ErrShareRevoked means the share was revoked and can no longer change.
var ErrShareRevoked = errors.New("share is revoked")

// MarkShareLeft records that the peer disconnected, and revokes the share.
func (s *Store) MarkShareLeft(ctx context.Context, id string) (*Share, error) {
	sh, err := s.SetShareStatus(ctx, id, ShareRevoked)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE shares SET left_at=? WHERE id=?`, now(), id); err != nil {
		return nil, err
	}
	return s.GetShare(ctx, sh.ID)
}

// ShareServiceCounts counts proxied requests per service made with a share's
// app since the given time, by service slug.
func (s *Store) ShareServiceCounts(ctx context.Context, appID int64, since time.Time) (map[string]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(sv.slug,''), COUNT(*) FROM events e LEFT JOIN services sv ON sv.id=e.service_id
		WHERE e.app_id=? AND e.ts>=? AND e.kind IN ('proxy','cache_hit') GROUP BY sv.slug`, appID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var slug string
		var n int64
		if err := rows.Scan(&slug, &n); err != nil {
			return nil, err
		}
		if slug != "" {
			out[slug] = n
		}
	}
	return out, rows.Err()
}

// ---- peer links (receiver side) ----

const peerLinkCols = `id, peer_id, peer_name, peer_url, peer_public_key, remote_share_id, token_enc, status, manifest, prefix, trusted,
last_sync_at, last_error, created_by, created_at, services, providers, mode`

func scanPeerLink(row interface{ Scan(...any) error }) (*PeerLink, error) {
	var l PeerLink
	var manifest, svcs, provs string
	var trusted int
	var created int64
	var sync, by sql.NullInt64
	err := row.Scan(&l.ID, &l.PeerID, &l.PeerName, &l.PeerURL, &l.PeerPublicKey, &l.RemoteShareID, &l.TokenEnc, &l.Status, &manifest, &l.Prefix, &trusted,
		&sync, &l.LastError, &by, &created, &svcs, &provs, &l.Mode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	l.Manifest = json.RawMessage(manifest)
	if len(l.Manifest) == 0 {
		l.Manifest = json.RawMessage(`{}`)
	}
	l.Trusted = trusted == 1
	l.LastSyncAt, l.CreatedBy = toTime(sync), toInt(by)
	l.CreatedAt = time.Unix(created, 0).UTC()
	l.Services, l.Providers = map[string]int64{}, []string{}
	_ = json.Unmarshal([]byte(svcs), &l.Services)
	_ = json.Unmarshal([]byte(provs), &l.Providers)
	return &l, nil
}

// CreatePeerLink stores a redeemed share's link.
func (s *Store) CreatePeerLink(ctx context.Context, l *PeerLink, createdBy int64) (*PeerLink, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO peer_links(peer_id,peer_name,peer_url,peer_public_key,remote_share_id,token_enc,status,manifest,prefix,trusted,created_by,created_at,mode)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.PeerID, l.PeerName, l.PeerURL, l.PeerPublicKey, l.RemoteShareID, l.TokenEnc, l.Status, "{}", l.Prefix, b2i(l.Trusted), nz(createdBy), now(), linkMode(l.Mode))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	s.keysChanged()
	return s.GetPeerLink(ctx, id)
}

// GetPeerLink returns one peer link.
func (s *Store) GetPeerLink(ctx context.Context, id int64) (*PeerLink, error) {
	return scanPeerLink(s.DB.QueryRowContext(ctx, `SELECT `+peerLinkCols+` FROM peer_links WHERE id=?`, id))
}

// ListPeerLinks returns every peer link, newest first.
func (s *Store) ListPeerLinks(ctx context.Context) ([]*PeerLink, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+peerLinkCols+` FROM peer_links ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PeerLink{}
	for rows.Next() {
		l, err := scanPeerLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SavePeerLinkState stores what a sync learned: status, manifest, the local
// services and imported providers, and the time and error of the sync.
func (s *Store) SavePeerLinkState(ctx context.Context, l *PeerLink) error {
	svcs, _ := json.Marshal(l.Services)
	provs, _ := json.Marshal(nonNil(l.Providers))
	manifest := string(l.Manifest)
	if manifest == "" {
		manifest = "{}"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE peer_links SET status=?, manifest=?, peer_name=?, last_sync_at=?, last_error=?, services=?, providers=? WHERE id=?`,
		l.Status, manifest, l.PeerName, fromTime(l.LastSyncAt), s.Scrub(ctx, l.LastError), string(svcs), string(provs), l.ID)
	return err
}

// ClearPeerLinkToken deletes the stored share token and sets the status.
func (s *Store) ClearPeerLinkToken(ctx context.Context, id int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE peer_links SET token_enc=NULL, status=? WHERE id=?`, status, id)
	return err
}

// DeletePeerLink removes a link row. Its local services stay, unlinked.
func (s *Store) DeletePeerLink(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE services SET peer_link_id=NULL WHERE peer_link_id=?`, id); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM peer_links WHERE id=?`, id)
	return err
}

// SetServicePeer marks a service as made for a peer link's remote service.
func (s *Store) SetServicePeer(ctx context.Context, serviceID, linkID int64, remoteSlug string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET peer_link_id=?, peer_remote_slug=? WHERE id=?`, linkID, remoteSlug, serviceID)
	return err
}

// SetServiceKeysStatus sets the status of every key of a service, clearing
// cooldowns and errors when it turns them back on.
func (s *Store) SetServiceKeysStatus(ctx context.Context, serviceID int64, status, lastError string) error {
	q := `UPDATE keys SET status=?, last_error=? WHERE service_id=?`
	if status == StatusActive {
		q = `UPDATE keys SET status=?, last_error=?, cooldown_until=NULL, consecutive_fails=0 WHERE service_id=?`
	}
	_, err := s.DB.ExecContext(ctx, q, status, s.Scrub(ctx, lastError), serviceID)
	return err
}

// DeleteServiceKeys deletes every key of a service.
func (s *Store) DeleteServiceKeys(ctx context.Context, serviceID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM keys WHERE service_id=?`, serviceID)
	if err == nil {
		s.keysChanged()
	}
	return err
}
