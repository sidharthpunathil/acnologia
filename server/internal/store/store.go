// Package store is the SQLite persistence layer.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sidharthpunathil/acnologia/server/internal/redact"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	DB *sql.DB
	// red redacts secrets from text the store writes (see redact.go).
	red atomic.Pointer[redact.Redactor]
	// scopes caches share key scopes by app (see keyscope.go).
	scopes scopeCache
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
  created_at INTEGER NOT NULL,
  max_concurrency INTEGER NOT NULL DEFAULT 0,
  queue_timeout_sec INTEGER NOT NULL DEFAULT 0,
  logo BLOB,
  logo_mime TEXT NOT NULL DEFAULT '',
  logo_rev INTEGER NOT NULL DEFAULT 0,
  target_trusted INTEGER NOT NULL DEFAULT 0
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
  max_concurrency INTEGER,
  last_ok_at INTEGER,
  account_name TEXT NOT NULL DEFAULT '',
  account_email TEXT NOT NULL DEFAULT '',
  account_phone TEXT NOT NULL DEFAULT '',
  account_ref TEXT NOT NULL DEFAULT '',
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
  last_used_at INTEGER,
  lease_hidden INTEGER NOT NULL DEFAULT 0
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
CREATE TABLE IF NOT EXISTS providers(
  slug TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  category TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  website TEXT NOT NULL DEFAULT '',
  docs_url TEXT NOT NULL DEFAULT '',
  color TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  builtin INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT 'manual',
  version INTEGER NOT NULL DEFAULT 1,
  spec TEXT NOT NULL,
  logo BLOB,
  logo_mime TEXT NOT NULL DEFAULT '',
  logo_rev INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS provider_versions(
  slug TEXT NOT NULL REFERENCES providers(slug) ON DELETE CASCADE ON UPDATE CASCADE,
  version INTEGER NOT NULL,
  snapshot TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY(slug, version)
);
CREATE TABLE IF NOT EXISTS agent_runs(
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  provider_slug TEXT NOT NULL DEFAULT '',
  input TEXT NOT NULL,
  test_key_enc BLOB,
  draft TEXT NOT NULL DEFAULT '',
  report TEXT NOT NULL DEFAULT '',
  live TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  service_id INTEGER,
  usage TEXT NOT NULL DEFAULT '{}',
  created_by INTEGER,
  created_by_email TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  finished_at INTEGER
);
CREATE TABLE IF NOT EXISTS chat_threads(
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  data BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS chat_threads_user ON chat_threads(user_id, updated_at);
CREATE TABLE IF NOT EXISTS agent_steps(
  run_id INTEGER NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(run_id, seq)
);
`

// DBFile is the database file name inside the data dir. Data dirs created
// before the rename from Cogito hold cogito.db instead; DBFileIn picks it.
const (
	DBFile       = "acnologia.db"
	legacyDBFile = "cogito.db"
)

// DBFileIn returns the database file name to open in dataDir: acnologia.db,
// or the old cogito.db when only that one exists. The old file is opened in
// place, never renamed or moved.
func DBFileIn(dataDir string) string {
	if _, err := os.Stat(filepath.Join(dataDir, DBFile)); err == nil {
		return DBFile
	}
	if _, err := os.Stat(filepath.Join(dataDir, legacyDBFile)); err == nil {
		return legacyDBFile
	}
	return DBFile
}

func Open(dataDir string) (*Store, error) {
	file := DBFileIn(dataDir)
	slog.Info("opening database", "path", filepath.Join(dataDir, file))
	dsn := "file:" + filepath.Join(dataDir, file) +
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
	// The database holds encrypted keys and password hashes: owner only.
	for _, f := range []string{file, file + "-wal", file + "-shm"} {
		_ = os.Chmod(filepath.Join(dataDir, f), 0o600)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: db}, nil
}

// migrate adds columns introduced after the first release to existing databases.
func migrate(db *sql.DB) error {
	added, err := addColumn(db, "services", "max_concurrency", "INTEGER NOT NULL DEFAULT 0")
	if err != nil {
		return err
	}
	if _, err := addColumn(db, "services", "queue_timeout_sec", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if added {
		// ElevenLabs caps concurrent requests per account (2 on the free plan).
		if _, err := db.Exec(`UPDATE services SET max_concurrency=2, queue_timeout_sec=30 WHERE provider='elevenlabs'`); err != nil {
			return err
		}
	}
	if _, err = addColumn(db, "keys", "max_concurrency", "INTEGER"); err != nil {
		return err
	}
	if _, err = addColumn(db, "services", "logo", "BLOB"); err != nil {
		return err
	}
	if _, err = addColumn(db, "services", "logo_mime", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err = addColumn(db, "services", "logo_rev", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err = addColumn(db, "keys", "last_ok_at", "INTEGER"); err != nil {
		return err
	}
	// Services that existed before target trust was tracked were configured by
	// whoever ran the instance; keep them usable for hidden keys.
	added, err = addColumn(db, "services", "target_trusted", "INTEGER NOT NULL DEFAULT 0")
	if err != nil {
		return err
	}
	if added {
		if _, err := db.Exec(`UPDATE services SET target_trusted=1`); err != nil {
			return err
		}
	}
	// Apps are not trusted with hidden keys until a superadmin says so.
	if _, err = addColumn(db, "apps", "lease_hidden", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	for _, col := range []string{"account_name", "account_email", "account_phone", "account_ref"} {
		if _, err = addColumn(db, "keys", col, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if err := migrateV2(db); err != nil {
		return err
	}
	if err := migrateAIUsage(db); err != nil {
		return err
	}
	if err := migrateAppTokens(db); err != nil {
		return err
	}
	if err := migrateSharing(db); err != nil {
		return err
	}
	if err := migratePeerProviders(db); err != nil {
		return err
	}
	if err := migrateRefresh(db); err != nil {
		return err
	}
	return migrateRuns(db)
}

func addColumn(db *sql.DB, table, col, def string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == col {
			return false, nil
		}
	}
	rows.Close()
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + def)
	return err == nil, err
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
