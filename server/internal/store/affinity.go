package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Key affinity: which key a request must use. Some providers keep what a
// request creates (a prediction, a run, a batch, an uploaded file) on the
// account that created it, and answer 404 to any other account. The proxy
// records the key that created such a resource under (service, "r|" + id),
// and the key a caller's X-Acnologia-Key-Affinity value is bound to under
// (service, "h|" + app + "|" + value). Later requests for the same resource
// or value use that key only (api/proxy_affinity.go).

const schemaAffinity = `
CREATE TABLE IF NOT EXISTS key_affinity(
  service_id INTEGER NOT NULL,
  ref TEXT NOT NULL,
  key_id INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY(service_id, ref)
);
CREATE INDEX IF NOT EXISTS key_affinity_expires ON key_affinity(expires_at);
`

func migrateAffinity(db *sql.DB) error {
	_, err := db.Exec(schemaAffinity)
	return err
}

// KeyAffinity returns the key bound to ref on the service and when the
// binding ends. ok is false when there is none or it has expired.
func (s *Store) KeyAffinity(ctx context.Context, serviceID int64, ref string, now time.Time) (keyID int64, expires time.Time, ok bool, err error) {
	var exp int64
	err = s.r.QueryRowContext(ctx, `SELECT key_id, expires_at FROM key_affinity WHERE service_id=? AND ref=?`, serviceID, ref).Scan(&keyID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	expires = time.Unix(exp, 0)
	if !expires.After(now) {
		return 0, time.Time{}, false, nil
	}
	return keyID, expires, true, nil
}

// SetKeyAffinity binds ref on the service to a key until expires.
func (s *Store) SetKeyAffinity(ctx context.Context, serviceID int64, ref string, keyID int64, expires time.Time) error {
	return s.gw.write(wstmt{`INSERT INTO key_affinity(service_id, ref, key_id, expires_at) VALUES(?,?,?,?)
		ON CONFLICT(service_id, ref) DO UPDATE SET key_id=excluded.key_id, expires_at=excluded.expires_at`,
		[]any{serviceID, ref, keyID, expires.Unix()}})
}
