package store

import "database/sql"

// schemaV2 holds the tables added for webhooks, egress proxies, service
// groups, admin tokens and app usage. Columns on existing tables are added in
// migrateV2 so live databases upgrade in place.
const schemaV2 = `
CREATE TABLE IF NOT EXISTS webhooks(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  url_enc BLOB NOT NULL,
  url_hint TEXT NOT NULL DEFAULT '',
  format TEXT NOT NULL DEFAULT 'json',
  events TEXT NOT NULL DEFAULT '[]',
  secret_enc BLOB,
  active INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  last_sent_at INTEGER,
  last_status INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS egress_proxies(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  url_enc BLOB NOT NULL,
  url_hint TEXT NOT NULL DEFAULT '',
  region TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_checked_at INTEGER,
  last_ip TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS service_groups(
  id INTEGER PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  members TEXT NOT NULL DEFAULT '[]',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_tokens(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  scope TEXT NOT NULL,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER,
  last_used_at INTEGER
);
CREATE TABLE IF NOT EXISTS app_usage(
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  day TEXT NOT NULL,
  requests INTEGER NOT NULL DEFAULT 0,
  cost REAL NOT NULL DEFAULT 0,
  cache_hits INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(app_id, day)
);
`

func migrateV2(db *sql.DB) error {
	if _, err := db.Exec(schemaV2); err != nil {
		return err
	}
	cols := []struct{ table, col, def string }{
		{"keys", "egress_id", "INTEGER"},
		{"services", "egress_id", "INTEGER"},
		{"services", "cache_ttl_sec", "INTEGER"},
		{"services", "cost_per_request", "REAL"},
		{"events", "cost", "REAL NOT NULL DEFAULT 0"},
		{"apps", "daily_requests", "INTEGER"},
		{"apps", "monthly_requests", "INTEGER"},
		{"apps", "daily_budget", "REAL"},
		{"apps", "monthly_budget", "REAL"},
	}
	for _, c := range cols {
		if _, err := addColumn(db, c.table, c.col, c.def); err != nil {
			return err
		}
	}
	return nil
}

func fromFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
