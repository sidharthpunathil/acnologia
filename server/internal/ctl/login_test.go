package ctl

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// deviceServer fakes the server side of browser sign in: the first polls
// are pending (one too fast), then the request is approved, or denied.
type deviceServer struct {
	mu      sync.Mutex
	polls   int
	deny    bool
	created map[string]string
	deleted []string
	auth    []string
	access  []string
	// link replaces verification_uri_complete when set.
	link string
}

func (d *deviceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.auth = append(d.auth, r.Header.Get("Authorization"))
	d.access = append(d.access, r.Header.Get("X-Acnologia-Access"))
	switch {
	case r.URL.Path == "/healthz":
		_, _ = io.WriteString(w, `{"ok":true,"version":"v9"}`)
	case r.URL.Path == "/api/gate":
		_, _ = io.WriteString(w, `{"required":true,"locked":true}`)
	case r.URL.Path == "/api/cli/device" && r.Method == "POST":
		_ = json.NewDecoder(r.Body).Decode(&d.created)
		// Like the real server: the link is on its own base URL, unless
		// the test makes it send another.
		link := d.link
		if link == "" {
			link = "http://" + r.Host + "/cli/authorize?code=WDJB-MJHT"
		}
		b, _ := json.Marshal(map[string]any{"device_code": "dev-secret", "user_code": "WDJB-MJHT",
			"verification_uri": "http://" + r.Host + "/cli/authorize", "verification_uri_complete": link,
			"expires_in": 600, "interval": 5})
		_, _ = w.Write(b)
	case r.URL.Path == "/api/cli/device/token":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["device_code"] != "dev-secret" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"expired_token","message":"gone"}}`)
			return
		}
		d.polls++
		switch {
		case d.polls == 1:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"authorization_pending","message":"wait"}}`)
		case d.polls == 2:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"slow_down","message":"slow"},"interval":10}`)
		case d.deny:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"access_denied","message":"no"}}`)
		default:
			_, _ = io.WriteString(w, `{"token":"acnadm_fromdevice","token_id":42,"scope":"write","user":{"email":"me@x.io","role":"admin"},"expires_at":"2027-01-01T00:00:00Z"}`)
		}
	case r.URL.Path == "/api/admin/me":
		if r.Header.Get("Authorization") != "Bearer acnadm_fromdevice" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"API token is invalid"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"email":"me@x.io","name":"Me","role":"admin"}`)
	case r.URL.Path == "/api/admin/overview":
		_, _ = io.WriteString(w, `{"services":2,"keys":{"total":5,"active":4},"requests_24h":7}`)
	case strings.HasPrefix(r.URL.Path, "/api/admin/tokens/") && r.Method == "DELETE":
		d.deleted = append(d.deleted, strings.TrimPrefix(r.URL.Path, "/api/admin/tokens/"))
		_, _ = io.WriteString(w, `{"ok":true}`)
	default:
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"not found"}}`)
	}
}

func newTestCLI(t *testing.T, stdin string) (*cli, *bytes.Buffer, *bytes.Buffer, *[]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("ACNOLOGIA_CTL_CONFIG", filepath.Join(dir, "ctl.json"))
	t.Setenv("ACNOLOGIA_URL", "")
	t.Setenv("ACNOLOGIA_TOKEN", "")
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "")
	t.Setenv("ACNOLOGIA_ACCESS_URL", "")
	var out, errb bytes.Buffer
	opened := &[]string{}
	c := &cli{
		prog: "acn", in: strings.NewReader(stdin), out: &out, err: &errb,
		openBrowser: func(u string) error { *opened = append(*opened, u); return nil },
		pollEvery:   time.Millisecond,
		hostname:    func() (string, error) { return "test-box", nil },
	}
	return c, &out, &errb, opened
}

func TestDeviceLogin(t *testing.T) {
	d := &deviceServer{}
	srv := httptest.NewServer(d)
	defer srv.Close()
	c, out, errb, opened := newTestCLI(t, srv.URL+"/\n")

	// No --domain: the CLI asks, and takes the URL with a trailing slash.
	if code := c.exec([]string{"login"}); code != 0 {
		t.Fatalf("login: %s %s", out, errb)
	}
	if !strings.Contains(errb.String(), "Acnologia URL (for example https://key.example.com):") {
		t.Fatalf("no prompt: %q", errb)
	}
	if len(*opened) != 1 || (*opened)[0] != srv.URL+"/cli/authorize?code=WDJB-MJHT" {
		t.Fatalf("browser opened %v", *opened)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	for _, want := range []string{"WDJB-MJHT", "site password", "Signed in to " + host + " as me@x.io (write)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if d.created["client"] != "acn" || d.created["hostname"] != "test-box" || d.created["os"] == "" {
		t.Fatalf("device request: %v", d.created)
	}
	if d.polls != 3 {
		t.Fatalf("polls = %d", d.polls)
	}
	// Nothing before the grant carried a token.
	for _, a := range d.auth {
		if a != "" {
			t.Fatalf("sent %q before signing in", a)
		}
	}

	// Saved per host, 0600 in a 0700 directory. Windows has no Unix modes:
	// the file takes the ACL of the user's profile folder instead.
	p := c.configPath()
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("config mode %v", st.Mode().Perm())
		}
		if st, _ := os.Stat(filepath.Dir(p)); st.Mode().Perm() != 0o700 {
			t.Fatalf("config dir mode %v", st.Mode().Perm())
		}
	}
	cf := c.load()
	ctx := cf.Contexts[host]
	if cf.Current != host || ctx.Token != "acnadm_fromdevice" || ctx.User != "me@x.io" || ctx.URL != srv.URL || ctx.TokenID != 42 {
		t.Fatalf("saved: %+v", cf)
	}

	out.Reset()
	if code := c.exec([]string{"whoami"}); code != 0 || !strings.Contains(out.String(), "me@x.io on "+host) {
		t.Fatalf("whoami: %s %s", out, errb)
	}
	out.Reset()
	if code := c.exec([]string{"status"}); code != 0 || !strings.Contains(out.String(), "version v9") || !strings.Contains(out.String(), "Services   2") {
		t.Fatalf("status: %s %s", out, errb)
	}
	out.Reset()
	if code := c.exec([]string{"whoami", "--json"}); code != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("whoami --json: %q", out)
	}

	// logout revokes the token through the delete route, then forgets it.
	out.Reset()
	if code := c.exec([]string{"logout"}); code != 0 || !strings.Contains(out.String(), "revoked") {
		t.Fatalf("logout: %s %s", out, errb)
	}
	if len(d.deleted) != 1 || d.deleted[0] != "42" {
		t.Fatalf("deleted %v", d.deleted)
	}
	if cf := c.load(); len(cf.Contexts) != 0 {
		t.Fatalf("still saved: %+v", cf)
	}
}

func TestDeviceLoginDenied(t *testing.T) {
	d := &deviceServer{deny: true}
	srv := httptest.NewServer(d)
	defer srv.Close()
	c, _, errb, opened := newTestCLI(t, "")
	c.openBrowser = func(string) error { return errors.New("no browser here") }
	if code := c.exec([]string{"login", "--domain", srv.URL, "--no-browser"}); code == 0 || !strings.Contains(errb.String(), "denied") {
		t.Fatalf("denied login: %d %s", code, errb)
	}
	if len(*opened) != 0 {
		t.Fatal("--no-browser opened a browser")
	}
	if cf := c.load(); len(cf.Contexts) != 0 {
		t.Fatalf("saved after a denial: %+v", cf)
	}
}

func TestLoginNeedsAURL(t *testing.T) {
	c, _, errb, _ := newTestCLI(t, "")
	if code := c.exec([]string{"login"}); code == 0 || !strings.Contains(errb.String(), "--domain") {
		t.Fatalf("empty answer: %s", errb)
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"key.sidharth.co":             "https://key.sidharth.co",
		"https://Key.Sidharth.co/":    "https://key.sidharth.co",
		" https://key.sidharth.co// ": "https://key.sidharth.co",
		"https://x.io/acn/":           "https://x.io/acn",
		"http://localhost:8123":       "http://localhost:8123",
		"http://127.0.0.1:9000/":      "http://127.0.0.1:9000",
		"http://[::1]:9000":           "http://[::1]:9000",
		"http://key.sidharth.co":      "",
		"ftp://key.sidharth.co":       "",
		"https://":                    "",
		"https://x.io/?a=1":           "",
		"":                            "",
	} {
		got, err := NormalizeURL(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: want an error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
}

func TestEnvOverride(t *testing.T) {
	d := &deviceServer{}
	srv := httptest.NewServer(d)
	defer srv.Close()
	c, out, errb, _ := newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_URL", srv.URL+"/")
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_fromdevice")
	if code := c.exec([]string{"whoami"}); code != 0 || !strings.Contains(out.String(), "me@x.io") {
		t.Fatalf("env whoami: %s %s", out, errb)
	}
}
