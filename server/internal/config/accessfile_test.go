package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateAccessPassword(t *testing.T) {
	dir := t.TempDir()
	p1, created, err := LoadOrCreateAccessPassword(dir)
	if err != nil || !created || len(p1) < MinAccessPasswordLen {
		t.Fatalf("first call: created=%v len=%d err=%v", created, len(p1), err)
	}
	fi, err := os.Stat(filepath.Join(dir, AccessPasswordFile))
	// Windows has no Unix modes; the data dir's ACL protects the file there.
	if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
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

// A password file saved by Notepad ends in CRLF, sometimes with a byte order
// mark; neither may become part of the password.
func TestAccessPasswordFileCRLF(t *testing.T) {
	for name, body := range map[string]string{
		"crlf":     "abcdefghijklmnop1234\r\n",
		"bom crlf": "\ufeffabcdefghijklmnop1234\r\n",
		"cr only":  "abcdefghijklmnop1234\r",
		"no eol":   "abcdefghijklmnop1234",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, AccessPasswordFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p, created, err := LoadOrCreateAccessPassword(dir)
		if err != nil || created || p != "abcdefghijklmnop1234" {
			t.Errorf("%s: got %q created=%v err=%v", name, p, created, err)
		}
	}
}
