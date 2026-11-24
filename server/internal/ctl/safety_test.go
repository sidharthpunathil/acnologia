package ctl

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSafetyCommandsAndFlags(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" key="+r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer acnadm_good" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"no"}}`)
			return
		}
		switch {
		case r.URL.Path == "/api/admin/me":
			_, _ = io.WriteString(w, `{"email":"ops@x.io","role":"superadmin"}`)
		case r.URL.Path == "/api/admin/trash" && r.Method == "GET":
			_, _ = io.WriteString(w, `{"items":[{"id":"trs_1","kind":"service","name":"serpapi","keys":2,"deleted_by":"ops@x.io","expires_at":"2030-01-01T00:00:00Z"}]}`)
		case r.URL.Path == "/api/admin/trash/trs_1/restore":
			_, _ = io.WriteString(w, `{"ok":true,"restored":{"id":"trs_1","kind":"service","name":"serpapi"}}`)
		case r.URL.Path == "/api/admin/version":
			_, _ = io.WriteString(w, `{"current":"v1.0.0","latest":"v1.1.0","schema_version":4,"update_available":true,"upgrade_command":"docker compose pull && docker compose up -d"}`)
		case r.URL.Path == "/api/admin/backup":
			w.Header().Set("Content-Type", "application/vnd.sqlite3")
			w.Header().Set("Content-Disposition", `attachment; filename="acnologia-backup-x.db"`)
			_, _ = io.WriteString(w, "SQLite format 3\x00fake")
		case r.URL.Path == "/api/admin/apps" && r.Method == "POST":
			_, _ = io.WriteString(w, `{"dry_run":true,"would":"create app x"}`)
		default:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"not found"}}`)
		}
	}))
	defer srv.Close()
	t.Setenv("ACNOLOGIA_CTL_CONFIG", filepath.Join(t.TempDir(), "ctl.json"))
	t.Setenv("ACNOLOGIA_URL", "")
	t.Setenv("ACNOLOGIA_TOKEN", "")
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Run(args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}
	if code, _, errs := run("login", srv.URL, "--token", "acnadm_good"); code != 0 {
		t.Fatal(errs)
	}
	if code, out, errs := run("trash"); code != 0 || !strings.Contains(out, "serpapi (2 keys)") {
		t.Fatalf("trash: %d %s %s", code, out, errs)
	}
	if code, out, _ := run("trash", "restore", "trs_1"); code != 0 || !strings.Contains(out, "Restored service serpapi") {
		t.Fatalf("restore: %s", out)
	}
	if code, out, _ := run("version", "--server"); code != 0 || !strings.Contains(out, "v1.1.0") || !strings.Contains(out, "docker compose pull") || !strings.Contains(out, "Schema   4") {
		t.Fatalf("version: %s", out)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "b.db")
	if code, out, errs := run("backup", "-o", dest); code != 0 || !strings.Contains(out, "Saved") {
		t.Fatalf("backup: %s %s", out, errs)
	}
	if b, _ := os.ReadFile(dest); !strings.HasPrefix(string(b), "SQLite format 3") {
		t.Fatal("backup file")
	}
	// The global flags reach admin writes only.
	seen = nil
	if code, out, errs := run("--dry-run", "api", "POST", "/api/admin/apps", "--data", `{"name":"x"}`, "--idempotency-key", "k1"); code != 0 || strings.TrimSpace(out) != "Dry run: would create app x" {
		t.Fatalf("%q %s", out, errs)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "dry_run=1") || !strings.HasSuffix(seen[0], "key=k1") {
		t.Fatalf("sent %v", seen)
	}
	seen = nil
	_, _, _ = run("--dry-run", "trash")
	if strings.Contains(seen[0], "dry_run") {
		t.Fatalf("a read got dry_run: %v", seen)
	}
	if code, out, _ := run("help"); code != 0 || !strings.Contains(out, "--idempotency-key") {
		t.Fatal("help lacks the safety commands")
	}
}
