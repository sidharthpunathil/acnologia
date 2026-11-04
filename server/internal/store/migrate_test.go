package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A migration that fails halfway leaves nothing behind, so the next start
// runs it again in full. Here the share binding backfill fails after its
// column was added (a trigger stands in for a full disk). Before migrations
// ran in one transaction the column stayed, the next start took the column
// as done and skipped the backfill, and the share was refused every service.
func TestMigrateFailureRollsBackEverything(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO services(id,slug,name,provider,base_url,auth_type,auth_name,strategy,cooldown_sec,max_attempts,created_at) VALUES(7,'openai','OpenAI','openai','https://x','bearer','Authorization','round_robin',30,3,1)`,
		`INSERT INTO apps(id,name,token_hash,token_prefix,created_at) VALUES(3,'share app','h','p',1)`,
		`INSERT INTO shares(id,name,status,services,app_id,created_at) VALUES('sh1','to bob','active','["openai"]',3,1)`,
		`ALTER TABLE shares DROP COLUMN service_ids`,
		`CREATE TRIGGER boom BEFORE UPDATE ON shares BEGIN SELECT RAISE(ABORT,'simulated disk full'); END`,
	} {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()

	if _, err := Open(dir); err == nil {
		t.Fatal("the upgrade should fail while the trigger is there")
	}
	db, err := sql.Open("sqlite", writerDSN(filepath.Join(dir, DBFile)))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('shares') WHERE name='service_ids'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the failed upgrade left shares.service_ids behind")
	}
	if _, err := db.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	id, ok, err := s2.ShareServiceID(context.Background(), 3, "openai")
	if err != nil || !ok || id != 7 {
		t.Fatalf("share not bound after the retried upgrade: id=%d ok=%v err=%v", id, ok, err)
	}
}

// An upgrade from before multi token support keeps the app's token working.
func TestMigrateLegacyAppToken(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO apps(id,name,token_hash,token_prefix,created_at) VALUES(1,'legacy','HASH-OF-OLD-TOKEN','acn_ab',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE app_tokens`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, _, err := s2.AppByToken(context.Background(), "HASH-OF-OLD-TOKEN"); err != nil {
		t.Fatal(err)
	}
}
