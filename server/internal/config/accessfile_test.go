package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateAccessPassword(t *testing.T) {
	dir := t.TempDir()
	p1, created, err := LoadOrCreateAccessPassword(dir)
	if err != nil || !created || len(p1) < MinAccessPasswordLen {
		t.Fatalf("first call: created=%v len=%d err=%v", created, len(p1), err)
	}
	fi, err := os.Stat(filepath.Join(dir, AccessPasswordFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi, err)
	}
	p2, created, err := LoadOrCreateAccessPassword(dir)
	if err != nil || created || p2 != p1 {
		t.Fatalf("second call should reuse the file: created=%v same=%v err=%v", created, p2 == p1, err)
	}
	os.WriteFile(filepath.Join(dir, AccessPasswordFile), []byte("short\n"), 0o600)
	if _, _, err := LoadOrCreateAccessPassword(dir); err == nil {
		t.Fatal("a short stored password must be refused")
	}
}
