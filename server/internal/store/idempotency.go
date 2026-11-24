package store

import (
	"context"
	"database/sql"
	"time"
)

// Idempotency-Key records for the proxy (api/proxy_idem.go). A request with
// a key inserts a pending row before it calls the provider; the row's
// primary key is the SHA-256 of the scoped key, so a second request with the
// same key loses the insert and knows another one owns it. The owner then
// stores the final answer (done), or notes that it cannot be replayed (a
// stream, or too large), or deletes its pending row when it ends without an
// answer worth keeping. The answer is stored encrypted by the caller.
//
// Rows outlive restarts and the response cache. A pending row whose owner
// died (the server stopped mid request) is taken over once it is older than
// the stale age the caller passes; expired rows are taken over at once and
// deleted by Prune.

const schemaIdempotency = `
CREATE TABLE IF NOT EXISTS idempotency(
  key_hash TEXT PRIMARY KEY,
  app_id INTEGER NOT NULL,
  token_id INTEGER NOT NULL DEFAULT 0,
  fingerprint TEXT NOT NULL,
  state TEXT NOT NULL,
  owner TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 0,
  payload BLOB,
  key_id INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idempotency_expires ON idempotency(expires_at);
`

func migrateIdempotency(db *sql.DB) error {
	_, err := db.Exec(schemaIdempotency)
	return err
}

// Idempotency record states.
const (
	IdemPending = "pending"
	IdemDone    = "done"
	// IdemUnreplayable is a request that ran but whose answer was not kept
	// (an event stream, or larger than the cap), so it cannot be repeated.
	IdemUnreplayable = "unreplayable"
)

// IdemRecord is one Idempotency-Key and what became of its request.
type IdemRecord struct {
	State       string
	Owner       string
	AppID       int64
	TokenID     int64
	Fingerprint string
	Status      int
	// Payload is the stored answer, encrypted by the caller.
	Payload   []byte
	KeyID     int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

// GetIdempotency returns the record for a key hash, ErrNotFound when there
// is none. An expired record is returned too; callers check ExpiresAt.
func (s *Store) GetIdempotency(ctx context.Context, hash string) (*IdemRecord, error) {
	var r IdemRecord
	var created, expires int64
	err := s.r.QueryRowContext(ctx, `SELECT state,owner,app_id,token_id,fingerprint,status,payload,key_id,created_at,expires_at FROM idempotency WHERE key_hash=?`, hash).
		Scan(&r.State, &r.Owner, &r.AppID, &r.TokenID, &r.Fingerprint, &r.Status, &r.Payload, &r.KeyID, &created, &expires)
	if err != nil {
		return nil, notFound(err)
	}
	r.CreatedAt, r.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
	return &r, nil
}

// ClaimIdempotency makes owner the holder of a key hash with a pending
// record. It reports false when another holder has it: a record that has
// not expired, unless it is pending for the same request body and was
// created before staleBefore, which means its owner is gone.
func (s *Store) ClaimIdempotency(ctx context.Context, hash string, appID, tokenID int64, fingerprint, owner string, now, expires, staleBefore time.Time) (bool, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO idempotency(key_hash,app_id,token_id,fingerprint,state,owner,status,payload,key_id,created_at,expires_at)
		VALUES(?,?,?,?,'pending',?,0,NULL,0,?,?)
		ON CONFLICT(key_hash) DO UPDATE SET app_id=excluded.app_id, token_id=excluded.token_id, fingerprint=excluded.fingerprint,
			state='pending', owner=excluded.owner, status=0, payload=NULL, key_id=0, created_at=excluded.created_at, expires_at=excluded.expires_at
		WHERE idempotency.expires_at<=? OR (idempotency.state='pending' AND idempotency.fingerprint=excluded.fingerprint AND idempotency.created_at<?)`,
		hash, appID, tokenID, fingerprint, owner, now.Unix(), expires.Unix(), now.Unix(), staleBefore.Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FinishIdempotency settles the owner's pending record: done with the
// stored answer, or unreplayable without one. It reports false when the
// owner no longer holds the record.
func (s *Store) FinishIdempotency(ctx context.Context, hash, owner, state string, status int, payload []byte, keyID int64) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE idempotency SET state=?, status=?, payload=?, key_id=? WHERE key_hash=? AND owner=? AND state='pending'`,
		state, status, payload, keyID, hash, owner)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReleaseIdempotency deletes the owner's pending record, so a retry with
// the key runs the request again.
func (s *Store) ReleaseIdempotency(ctx context.Context, hash, owner string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM idempotency WHERE key_hash=? AND owner=? AND state='pending'`, hash, owner)
	return err
}

// pruneExpiring deletes expired idempotency records and key affinities.
func (s *Store) pruneExpiring(ctx context.Context, now time.Time) error {
	if err := s.pruneBatched(ctx, `DELETE FROM idempotency WHERE rowid IN (SELECT rowid FROM idempotency WHERE expires_at<? LIMIT ?)`, now.Unix()); err != nil {
		return err
	}
	return s.pruneBatched(ctx, `DELETE FROM key_affinity WHERE rowid IN (SELECT rowid FROM key_affinity WHERE expires_at<? LIMIT ?)`, now.Unix())
}
