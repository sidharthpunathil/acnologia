package ctl

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"
)

// A server can name any link for the browser sign in; only an http(s) one on
// the host the person typed is opened. Anything else is not handed to the OS
// launcher, and the page on the typed server is printed instead.
func TestLoginOpensOnlyServerLinks(t *testing.T) {
	for _, link := range []string{
		"file:///etc/passwd",
		`\\evil.example\share\x.exe`,
		`C:\Windows\System32\calc.exe`,
		"smb://evil.example/share/x",
		"search-ms:query=x",
		"javascript:alert(1)",
		"-a Calculator",
		"/System/Applications/Utilities/Terminal.app",
		"https://evil.example/cli/authorize?code=WDJB-MJHT",
		"http://user@127.0.0.1/cli/authorize",
		"\x1b]52;c;AAAA\x07",
	} {
		d := &deviceServer{link: link}
		srv := httptest.NewServer(d)
		c, out, errb, opened := newTestCLI(t, "")
		if code := c.exec([]string{"login", srv.URL}); code != 0 {
			t.Fatalf("%q: login failed: %s %s", link, out, errb)
		}
		srv.Close()
		if len(*opened) != 0 {
			t.Fatalf("%q: opened %v", link, *opened)
		}
		want := "  " + srv.URL + "/cli/authorize?code=WDJB-MJHT\n"
		if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "was not opened") {
			t.Fatalf("%q: output lacks the local link:\n%s", link, out)
		}
		if strings.Contains(out.String(), link) {
			t.Fatalf("%q: printed the server's link:\n%s", link, out)
		}
	}

	// Its own host on another port is fine (a dashboard served apart).
	d := &deviceServer{link: "http://127.0.0.1:1/cli/authorize?code=WDJB-MJHT"}
	srv := httptest.NewServer(d)
	defer srv.Close()
	c, out, errb, opened := newTestCLI(t, "")
	if code := c.exec([]string{"login", srv.URL}); code != 0 {
		t.Fatalf("login: %s %s", out, errb)
	}
	if len(*opened) != 1 || (*opened)[0] != d.link {
		t.Fatalf("opened %v", *opened)
	}
}

func TestVerifyLink(t *testing.T) {
	st := func(l string) deviceStart { return deviceStart{UserCode: "AB-CD", VerificationURIComplete: l} }
	for _, tc := range []struct {
		server, link string
		ok           bool
	}{
		{"https://key.example.com", "https://key.example.com/cli/authorize?code=AB-CD", true},
		{"https://key.example.com", "https://KEY.example.com:8443/cli/authorize", true},
		{"https://key.example.com", "http://key.example.com/cli/authorize", false},
		{"http://localhost:8080", "http://localhost:5173/cli/authorize", true},
		{"https://key.example.com", "https://key.example.com.evil.example/cli", false},
		{"https://key.example.com", "https://a:b@key.example.com/cli", false},
		{"https://key.example.com", "file://key.example.com/x", false},
		{"https://key.example.com", "https:key.example.com", false},
	} {
		link, ok := verifyLink(tc.server, st(tc.link))
		if ok != tc.ok {
			t.Errorf("%s %s: ok %v", tc.server, tc.link, ok)
		}
		if !ok && link != tc.server+"/cli/authorize?code=AB-CD" {
			t.Errorf("%s %s: fallback %q", tc.server, tc.link, link)
		}
	}
	link, ok := verifyLink("https://k.io", deviceStart{UserCode: "X Y", VerificationURI: "https://k.io/cli/authorize"})
	if !ok || link != "https://k.io/cli/authorize?code=X+Y" {
		t.Errorf("verification_uri: %q %v", link, ok)
	}
}

// recorder answers with h and notes the site password header of each
// request by path.
type recorder struct {
	mu     sync.Mutex
	access map[string]string
	h      http.Handler
}

func (rc *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc.mu.Lock()
	if rc.access == nil {
		rc.access = map[string]string{}
	}
	rc.access[r.URL.Path] = r.Header.Get("X-Acnologia-Access")
	h := rc.h
	rc.mu.Unlock()
	h.ServeHTTP(w, r)
}

// sent is the paths that carried the site password.
func (rc *recorder) sent() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var got []string
	for p, v := range rc.access {
		if v != "" {
			got = append(got, p)
		}
	}
	return got
}

// noAccess fails when any request to d carried the site password.
func noAccess(t *testing.T, what string, d *deviceServer) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, v := range d.access {
		if v != "" {
			t.Fatalf("%s: the site password was sent", what)
		}
	}
}

// The site password belongs to one server: it goes only to
// ACNOLOGIA_ACCESS_URL (or ACNOLOGIA_URL), never to a server being signed in
// to, another saved server, or the target of a redirect.
func TestAccessPasswordStaysWithItsServer(t *testing.T) {
	// Signing in to a new server with a password exported for another.
	d := &deviceServer{}
	b := httptest.NewServer(d)
	defer b.Close()
	c, out, errb, _ := newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "a-site-password")
	if code := c.exec([]string{"login", b.URL}); code != 0 {
		t.Fatalf("login: %s %s", out, errb)
	}
	noAccess(t, "login to a new server", d)

	// ACNOLOGIA_URL names server A; the saved current server is B.
	a := &recorder{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"email":"a@x.io","role":"admin"}`)
	})}
	as := httptest.NewServer(a)
	defer as.Close()
	t.Setenv("ACNOLOGIA_URL", as.URL)
	d.access = nil
	if code := c.exec([]string{"whoami"}); code != 0 {
		t.Fatalf("whoami: %s %s", out, errb)
	}
	noAccess(t, "another saved server", d)

	// ACNOLOGIA_ACCESS_URL binds it to B instead.
	t.Setenv("ACNOLOGIA_ACCESS_URL", strings.ToUpper(b.URL)+"/")
	d.access = nil
	if code := c.exec([]string{"whoami"}); code != 0 || len(d.access) == 0 || d.access[0] != "a-site-password" {
		t.Fatalf("bound server got %q: %s", d.access, errb)
	}
	t.Setenv("ACNOLOGIA_ACCESS_URL", "")

	// A redirects to B: A gets the password, B does not.
	a.mu.Lock()
	a.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+r.URL.Path, http.StatusFound)
	})
	a.mu.Unlock()
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_fromdevice")
	d.access = nil
	if code := c.exec([]string{"whoami"}); code != 0 {
		t.Fatalf("whoami through a redirect: %s %s", out, errb)
	}
	if len(a.sent()) == 0 {
		t.Fatal("the configured server did not get its password")
	}
	if len(d.access) == 0 {
		t.Fatal("the redirect was not followed")
	}
	noAccess(t, "redirect target", d)
}

func TestMCPAccessPasswordStaysWithItsServer(t *testing.T) {
	rc := &recorder{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	})}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	run := mcpEnv(t, srv.URL)
	t.Setenv("ACNOLOGIA_ACCESS_URL", "")
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "a-site-password")
	t.Setenv("ACNOLOGIA_URL", "https://other.example.com")
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	if err, _, _ := run(ping, "mcp"); err != nil {
		t.Fatal(err)
	}
	if got := rc.sent(); len(got) != 0 {
		t.Fatalf("password sent to an unbound server: %v", got)
	}
	t.Setenv("ACNOLOGIA_ACCESS_URL", srv.URL)
	if err, _, _ := run(ping, "mcp"); err != nil {
		t.Fatal(err)
	}
	if got := rc.sent(); len(got) != 1 {
		t.Fatalf("bound server got no password: %v", got)
	}
}

// terminalSafe fails when s holds anything a terminal would act on.
func terminalSafe(t *testing.T, what, s string) {
	t.Helper()
	for i, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if (r == utf8.RuneError && !validAt(s, i)) || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			t.Fatalf("%s: %U at %d in %q", what, r, i, s)
		}
	}
}

// Text a lease report, an upstream error or a peer's key label can carry
// (an OSC 52 clipboard write, a C1 CSI line erase, a bidi override, DEL, an
// invalid byte) is shown escaped, in tables, --json and error messages.
func TestServerTextIsSafeForTheTerminal(t *testing.T) {
	q, _ := json.Marshal("boom\x1b]52;c;ZWNobyBoaQ==\x07\u009b2K\u202eevil\x7f\tend")
	h := strings.Trim(string(q), `"`) + "\x9b"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/services":
			_, _ = io.WriteString(w, `{"services":[{"id":7,"slug":"s","provider":"`+h+`"}]}`)
		case "/api/admin/services/7/keys":
			_, _ = io.WriteString(w, `{"keys":[{"id":1,"label":"`+h+`","hint":"`+h+`","effective_status":"invalid","last_error":"`+h+`"}]}`)
		case "/api/admin/events":
			_, _ = io.WriteString(w, `{"events":[{"kind":"`+h+`","service_slug":"s","key_label":"`+h+`","app_name":"`+h+`","detail":"`+h+h+h+`"}]}`)
		case "/api/admin/services/7/breaker":
			_, _ = io.WriteString(w, `{"status":{"state":"open","reason":"`+h+`"},"threshold":1}`)
		default:
			w.WriteHeader(500)
			_, _ = io.WriteString(w, `{"error":{"code":"x","message":"`+h+`"}}`)
		}
	}))
	defer srv.Close()
	for _, args := range [][]string{
		{"services"}, {"keys", "s"}, {"events"}, {"breaker", "s"}, {"whoami"},
		{"keys", "s", "--json"}, {"events", "--json"},
	} {
		c, out, errb, _ := newTestCLI(t, "")
		t.Setenv("ACNOLOGIA_URL", srv.URL)
		t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")
		c.exec(args)
		terminalSafe(t, strings.Join(args, " ")+" stdout", out.String())
		terminalSafe(t, strings.Join(args, " ")+" stderr", errb.String())
		if !strings.Contains(out.String()+errb.String(), "boom") {
			t.Fatalf("%v: lost the text: %q %q", args, out, errb)
		}
		if strings.Contains(strings.Join(args, " "), "--json") && !json.Valid(out.Bytes()) {
			t.Fatalf("%v: not JSON: %q", args, out)
		}
	}
}

func TestMCPNotesAreSafeForTheTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"boom\u001b]52;c;AAAA\u0007\u009b2K"}}`)
	}))
	defer srv.Close()
	run := mcpEnv(t, srv.URL)
	err, _, errs := run(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n", "mcp")
	if err != nil || !strings.Contains(errs, "boom") {
		t.Fatalf("err %v stderr %q", err, errs)
	}
	terminalSafe(t, "mcp stderr", errs)
}

func TestClean(t *testing.T) {
	if got := clean("plain café ✓"); got != "plain café ✓" {
		t.Fatalf("changed plain text: %q", got)
	}
	if got := clean("a\x1b[2Jb\u009bc\u202ed\xffe"); got != `a\x1b[2Jb\x9bc\u202ed\xffe` {
		t.Fatalf("clean = %q", got)
	}
	if got := clip(strings.Repeat("é", 80), 60); utf8.RuneCountInString(got) != 60 || !utf8.ValidString(got) {
		t.Fatalf("clip = %q", got)
	}
}
