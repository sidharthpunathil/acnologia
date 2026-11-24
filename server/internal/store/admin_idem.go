package store

// Idempotency-Key answers for the admin API (api/admin_idem.go). One row per
// caller scope (user and token) and key: "running" while the first request
// with the key runs, then "done" with the answer, kept AdminIdemTTL. The
// body is stored encrypted by the caller: answers such as a new app token
// are shown once and are secrets. The proxy keeps its own replays
// elsewhere; the two never share a table.

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AdminIdemTTL is how long an admin answer is replayed for its key.
const AdminIdemTTL = 24 * time.Hour

// Admin idempotency row states.
const (
	AdminIdemRunning = "running"
	AdminIdemDone    = "done"
)

// AdminIdem is a stored claim or answer.
type AdminIdem struct {
	Fingerprint string
	State       string
	Status      int
	ContentType string
	BodyEnc     []byte
	CreatedAt   time.Time
}

const schemaAdminIdem = `
CREATE TABLE IF NOT EXISTS admin_idempotency(
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  state TEXT NOT NULL,
  status INTEGER NOT NULL DEFAULT 0,
  content_type TEXT NOT NULL DEFAULT '',
  body_enc BLOB,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY(scope, key)
);
CREATE INDEX IF NOT EXISTS admin_idempotency_expires ON admin_idempotency(expires_at);
`

func migrateAdminIdem(db *sql.DB) error {
	_, err := db.Exec(schemaAdminIdem)
	return err
}

// ClaimAdminIdem looks up scope and key. With no live row, or a running row
// older than stale (its request died), it records a running claim with fp
// and returns claimed. Otherwise it returns the row as it is.
func (s *Store) ClaimAdminIdem(ctx context.Context, scope, key, fp string, stale time.Duration) (*AdminIdem, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	t := now()
	var it AdminIdem
	var created, expires int64
	err = tx.QueryRowContext(ctx, `SELECT fingerprint, state, status, content_type, body_enc, created_at, expires_at FROM admin_idempotency WHERE scope=? AND key=?`,
		scope, key).Scan(&it.Fingerprint, &it.State, &it.Status, &it.ContentType, &it.BodyEnc, &created, &expires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, false, err
	case expires > t && !(it.State == AdminIdemRunning && created < t-int64(stale/time.Second)):
		it.CreatedAt = time.Unix(created, 0).UTC()
		return &it, false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_idempotency(scope,key,fingerprint,state,created_at,expires_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(scope,key) DO UPDATE SET fingerprint=excluded.fingerprint, state=excluded.state, status=0, content_type='', body_enc=NULL,
		created_at=excluded.created_at, expires_at=excluded.expires_at`,
		scope, key, fp, AdminIdemRunning, t, t+int64(AdminIdemTTL/time.Second)); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &AdminIdem{Fingerprint: fp, State: AdminIdemRunning, CreatedAt: time.Unix(t, 0).UTC()}, true, nil
}

// FinishAdminIdem stores the answer of a claimed key.
func (s *Store) FinishAdminIdem(ctx context.Context, scope, key string, status int, contentType string, bodyEnc []byte) error {
	t := now()
	_, err := s.DB.ExecContext(ctx, `UPDATE admin_idempotency SET state=?, status=?, content_type=?, body_enc=?, expires_at=? WHERE scope=? AND key=?`,
		AdminIdemDone, status, contentType, bodyEnc, t+int64(AdminIdemTTL/time.Second), scope, key)
	return err
}

// ReleaseAdminIdem drops a running claim whose answer is not kept, so the
// key can be used again.
func (s *Store) ReleaseAdminIdem(ctx context.Context, scope, key string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM admin_idempotency WHERE scope=? AND key=? AND state=?`, scope, key, AdminIdemRunning)
	return err
}

// Backup writes a consistent copy of the database to dest, which must not
// exist, with VACUUM INTO: one read transaction, so the copy is a single
// point in time and the server keeps serving. The copy leaves out what is
// only good on this running server: dashboard sessions and the admin
// Idempotency-Key answers. Stored keys stay encrypted with the master key,
// which is never in the database.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+uriPath(dest))
	if err != nil {
		return err
	}
	defer db.Close()
	for _, q := range []string{`DELETE FROM sessions`, `DELETE FROM admin_idempotency`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return db.Close()
}

// pruneAdminIdem deletes expired answers.
func (s *Store) pruneAdminIdem(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM admin_idempotency WHERE expires_at<?`, now())
	return err
}
