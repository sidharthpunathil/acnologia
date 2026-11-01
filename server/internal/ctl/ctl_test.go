package ctl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fake struct {
	added []map[string]any
	auth  []string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if r.Header.Get("Authorization") != "Bearer cogadm_good" {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"API token is invalid"}}`)
		return
	}
	switch {
	case r.URL.Path == "/api/admin/me":
		_, _ = io.WriteString(w, `{"email":"ops@x.io","role":"admin"}`)
	case r.URL.Path == "/api/admin/services":
		_, _ = io.WriteString(w, `{"services":[{"id":7,"slug":"serpapi","provider":"serpapi","key_counts":{"total":3,"available":2},"requests_24h":40}]}`)
	case r.URL.Path == "/api/admin/services/7/keys" && r.Method == "POST":
		var body struct{ Keys []map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.added = body.Keys
		_, _ = io.WriteString(w, `{"keys":[{},{}],"skipped":[{}]}`)
	case r.URL.Path == "/api/admin/services/7/breaker/reset":
		_, _ = io.WriteString(w, `{"status":{"state":"closed"},"threshold":10,"window_sec":60}`)
	default:
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"not found"}}`)
	}
}

func TestLoginAndCommands(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "ctl.json")
	t.Setenv("COGITO_CTL_CONFIG", cfg)
	t.Setenv("COGITO_URL", "")
	t.Setenv("COGITO_TOKEN", "")
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Run(args, strings.NewReader(stdin), &out, &errb)
		return code, out.String(), errb.String()
	}

	if code, _, errs := run("", "status"); code == 0 || !strings.Contains(errs, "not logged in") {
		t.Fatalf("status without login: %d %s", code, errs)
	}
	if code, _, errs := run("", "login", srv.URL, "--token", "cogadm_bad"); code == 0 || !strings.Contains(errs, "API token is invalid") {
		t.Fatalf("bad token should fail: %s", errs)
	}
	code, out, errs := run("", "login", srv.URL, "--token", "cogadm_good", "--name", "prod")
	if code != 0 || !strings.Contains(out, "ops@x.io") {
		t.Fatalf("login: %d %s %s", code, out, errs)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Fatalf("config holds tokens and must be 0600, got %v", st.Mode().Perm())
	}
	if code, out, _ := run("", "services"); code != 0 || !strings.Contains(out, "serpapi") {
		t.Fatalf("services: %s", out)
	}
	code, out, errs = run("# comment\nk-one\nmain, k-two\n\n", "keys", "add", "serpapi")
	if code != 0 || !strings.Contains(out, "Added 2 keys") || !strings.Contains(out, "skipped 1") {
		t.Fatalf("keys add: %d %s %s", code, out, errs)
	}
	if len(f.added) != 2 || f.added[0]["secret"] != "k-one" || f.added[1]["label"] != "main" || f.added[1]["secret"] != "k-two" {
		t.Fatalf("parsed keys: %v", f.added)
	}
	if code, out, _ := run("", "breaker", "serpapi", "--reset"); code != 0 || !strings.Contains(out, "closed") {
		t.Fatalf("breaker reset: %s", out)
	}
	if code, _, errs := run("", "keys", "add", "nope"); code == 0 || !strings.Contains(errs, `no service named "nope"`) {
		t.Fatalf("unknown service: %s", errs)
	}
	if code, out, _ := run("", "contexts"); code != 0 || !strings.Contains(out, "* ") || !strings.Contains(out, "prod") {
		t.Fatalf("contexts: %q", out)
	}
}
