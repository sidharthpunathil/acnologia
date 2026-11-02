package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesAcnologiaDB(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "acnologia.db")); err != nil {
		t.Fatal("a fresh data dir should get acnologia.db")
	}
	if _, err := os.Stat(filepath.Join(dir, "cogito.db")); err == nil {
		t.Fatal("no cogito.db should be created")
	}
}

func TestOpenKeepsLegacyCogitoDB(t *testing.T) {
	dir := t.TempDir()
	// Create a database under the old name, as a pre-rename install would have.
	old, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.CreateUser(context.Background(), "a@b.io", "A", "hash", RoleSuperadmin); err != nil {
		t.Fatal(err)
	}
	old.Close()
	for _, suf := range []string{"", "-wal", "-shm"} {
		_ = os.Rename(filepath.Join(dir, "acnologia.db"+suf), filepath.Join(dir, "cogito.db"+suf))
	}

	if got := DBFileIn(dir); got != "cogito.db" {
		t.Fatalf("DBFileIn = %q, want cogito.db", got)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.CountUsers(context.Background()); err != nil || n != 1 {
		t.Fatalf("legacy data not visible: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "acnologia.db")); err == nil {
		t.Fatal("the legacy file must be opened in place, not copied or renamed")
	}
	if _, err := os.Stat(filepath.Join(dir, "cogito.db")); err != nil {
		t.Fatal("the legacy file must not be moved")
	}
}

func TestOpenPrefersAcnologiaDBWhenBothExist(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"acnologia.db", "cogito.db"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := DBFileIn(dir); got != "acnologia.db" {
		t.Fatalf("DBFileIn = %q", got)
	}
}

func TestTokenPrefixes(t *testing.T) {
	for tok, app := range map[string]bool{"acn_x": true, "cog_x": true, "acnadm_x": false, "cogadm_x": false, "sk_x": false} {
		if IsAppToken(tok) != app {
			t.Errorf("IsAppToken(%q) = %v", tok, !app)
		}
	}
	for tok, adm := range map[string]bool{"acnadm_x": true, "cogadm_x": true, "acn_x": false, "cog_x": false} {
		if IsAdminToken(tok) != adm {
			t.Errorf("IsAdminToken(%q) = %v", tok, !adm)
		}
	}
}
