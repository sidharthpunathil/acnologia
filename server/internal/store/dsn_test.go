package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestURIPath(t *testing.T) {
	cases := map[string]string{
		`C:\Users\me\AppData\Local\Acnologia\acnologia.db`: `C:\Users\me\AppData\Local\Acnologia\acnologia.db`,
		"/data/acnologia.db":                               "/data/acnologia.db",
		`D:\keys #2\100%\a?.db`:                            `D:\keys %232\100%25\a%3f.db`,
	}
	for in, want := range cases {
		if got := uriPath(in); got != want {
			t.Errorf("uriPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A data dir whose name holds URI punctuation still opens, and the database
// lands inside it rather than at a truncated path.
func TestOpenDirWithURIPunctuation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys #2 100%")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateUser(context.Background(), "a@b.io", "A", "hash", RoleSuperadmin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DBFile)); err != nil {
		t.Fatalf("database not inside the data dir: %v", err)
	}
}
