// Package store is the SQLite persistence layer.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	DB *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users(
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  last_login_at INTEGER
);
CREATE TABLE IF NOT EXISTS sessions(
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS services(
  id INTEGER PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  provider TEXT NOT NULL,
  category TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL,
  auth_type TEXT NOT NULL,
  auth_name TEXT NOT NULL,
  strategy TEXT NOT NULL,
  cooldown_sec INTEGER NOT NULL,
  max_attempts INTEGER NOT NULL,
  icon TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS keys(
  id INTEGER PRIMARY KEY,
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  secret_enc BLOB NOT NULL,
  secret_fp TEXT NOT NULL,
  hint TEXT NOT NULL,
  tier TEXT NOT NULL DEFAULT 'primary',
  hidden INTEGER NOT NULL DEFAULT 0,
  restricted INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active',
  weight INTEGER NOT NULL DEFAULT 1,
  priority INTEGER NOT NULL DEFAULT 100,
  cooldown_until INTEGER,
  consecutive_fails INTEGER NOT NULL DEFAULT 0,
  quota_limit INTEGER,
  quota_remaining INTEGER,
  quota_reset_at INTEGER,
  usage_total INTEGER NOT NULL DEFAULT 0,
  usage_today INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  last_checked_at INTEGER,
  notes TEXT NOT NULL DEFAULT '',
  created_by INTEGER,
  created_at INTEGER NOT NULL,
  UNIQUE(service_id, secret_fp)
);
CREATE INDEX IF NOT EXISTS keys_service ON keys(service_id);
CREATE TABLE IF NOT EXISTS apps(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  token_hash TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  allowed_services TEXT NOT NULL DEFAULT '[]',
  mode TEXT NOT NULL DEFAULT 'both',
  active INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER
);
CREATE TABLE IF NOT EXISTS key_apps(
  key_id INTEGER NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  PRIMARY KEY(key_id, app_id)
);
CREATE TABLE IF NOT EXISTS leases(
  id TEXT PRIMARY KEY,
  key_id INTEGER NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  outcome TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS events(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL,
  service_id INTEGER,
  key_id INTEGER,
  app_id INTEGER,
  status_code INTEGER NOT NULL DEFAULT 0,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS events_service ON events(service_id, ts);
CREATE TABLE IF NOT EXISTS audit(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  user_id INTEGER,
  user_email TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

func Open(dataDir string) (*Store, error) {
	dsn := "file:" + filepath.Join(dataDir, "cogito.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer keeps SQLite simple and avoids SQLITE_BUSY under load.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) GetMeta(ctx context.Context, k string) string {
	var v string
	_ = s.DB.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, k).Scan(&v)
	return v
}

func (s *Store) SetMeta(ctx context.Context, k, v string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// time helpers

func now() int64 { return time.Now().Unix() }

func toTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func toInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func fromTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func fromInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
