package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// oldAppsSchema is the apps table as it was before app_tokens: one token
// per app, its hash in apps.token_hash.
const oldAppsSchema = `
CREATE TABLE apps(
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
INSERT INTO apps(id,name,token_hash,token_prefix,created_at) VALUES(1,'site','hash-of-cog-token','cog_XQnw',1700000000);
INSERT INTO apps(id,name,token_hash,token_prefix,created_at) VALUES(2,'bot','hash-of-acn-token','acn_abcd',1700000100);
`

func TestMigrationMovesOldTokensToAppTokens(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(oldAppsSchema); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, tok, err := s.AppByToken(ctx, "hash-of-cog-token")
	if err != nil {
		t.Fatalf("old token should still find its app: %v", err)
	}
	if a.ID != 1 || tok.Name != "Default" || tok.Prefix != "cog_XQnw..." || !tok.Usable(time.Now()) || a.TokenCount != 1 {
		t.Fatalf("migrated token: app %+v token %+v", a, tok)
	}
	if _, _, err := s.AppByToken(ctx, "hash-of-acn-token"); err != nil {
		t.Fatal(err)
	}
	// The old column stays, unread.
	var h string
	if err := s.DB.QueryRow(`SELECT token_hash FROM apps WHERE id=1`).Scan(&h); err != nil || h != "hash-of-cog-token" {
		t.Fatalf("apps.token_hash should be left in place: %q %v", h, err)
	}
	// A rotated token is not brought back by a later boot.
	if _, err := s.RotateAppToken(ctx, 1, "new", "acn_new1", "hash-new", 0, true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	toks, err := s.ListAppTokens(ctx, 1)
	if err != nil || len(toks) != 2 {
		t.Fatalf("want 2 tokens after reopen, got %d %v", len(toks), err)
	}
	if _, old, _ := s.AppByToken(ctx, "hash-of-cog-token"); old == nil || old.RevokedAt == nil {
		t.Fatal("the rotated Default token should stay revoked")
	}
}

func TestAppTokensLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.CreateApp(ctx, &App{Name: "web", Mode: "both", Active: true, TokenPrefix: "acn_aaaa"}, "h1")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if _, err := s.CreateAppToken(ctx, a.ID, "ci", "acn_bbbb...", "h2", 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAppToken(ctx, a.ID, "old", "acn_cccc...", "h3", 0, &past); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetApp(ctx, a.ID)
	if got.TokenCount != 2 {
		t.Fatalf("expired token should not count: %d", got.TokenCount)
	}
	_, t2, _ := s.AppByToken(ctx, "h2")
	if err := s.RevokeAppToken(ctx, a.ID, t2.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetApp(ctx, a.ID); got.TokenCount != 1 {
		t.Fatalf("revoked token should not count: %d", got.TokenCount)
	}
	if err := s.RevokeAppToken(ctx, a.ID+1, t2.ID); err != ErrNotFound {
		t.Fatalf("revoking through another app should be not found, got %v", err)
	}
	if err := s.DeleteApp(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppByToken(ctx, "h1"); err != ErrNotFound {
		t.Fatalf("tokens should go with the app: %v", err)
	}
}
