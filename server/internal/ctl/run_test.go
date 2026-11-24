package ctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The tests run this test binary as the child: TestRunHelperProcess does
// what its first argument after "--" says when ACN_RUN_HELPER is set.
func TestRunHelperProcess(t *testing.T) {
	if os.Getenv("ACN_RUN_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "env":
		out := map[string]string{}
		for _, k := range args[1:] {
			out[k] = os.Getenv(k)
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
	case "echo":
		b, _ := io.ReadAll(os.Stdin)
		fmt.Fprintf(os.Stdout, "out:%s", b)
		fmt.Fprint(os.Stderr, "err:line")
	case "exit":
		os.Exit(42)
	case "wait":
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		fmt.Fprintln(os.Stdout, "ready")
		select {
		case <-ch:
			os.Exit(7)
		case <-time.After(20 * time.Second):
			os.Exit(3)
		}
	}
	os.Exit(0)
}

func runTestServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acn_app" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"bad token"}}`)
			return
		}
		switch r.URL.Path {
		case "/api/v1/proxy":
			_, _ = io.WriteString(w, `{"proxy_url":"http://proxy.example:8888","ca_fingerprint":"AB:CD"}`)
		case "/api/v1/proxy/ca.pem":
			_, _ = io.WriteString(w, testCA)
		case "/api/v1/services":
			_, _ = io.WriteString(w, `{"services":[{"slug":"oa","provider":"openai"},{"slug":"x","provider":"custom-thing"},{"slug":"t","provider":"tavily"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// syncBuffer is a bytes.Buffer safe to read while the child writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func runCLI(t *testing.T, srvURL, stdin string) (*cli, *syncBuffer, *syncBuffer) {
	t.Helper()
	c, _, _, _ := newTestCLI(t, stdin)
	var out, errb syncBuffer
	c.out, c.err = &out, &errb
	t.Setenv("ACNOLOGIA_URL", srvURL)
	t.Setenv("ACN_RUN_HELPER", "1")
	return c, &out, &errb
}

func helperArgs(mode string, extra ...string) []string {
	return append([]string{"run", "--", os.Args[0], "-test.run=^TestRunHelperProcess$", "--", mode}, extra...)
}

func TestRunSetsProxyEnvForTheChildOnly(t *testing.T) {
	srv := runTestServer(t)
	roots := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(roots, []byte(testRoots), 0o644); err != nil {
		t.Fatal(err)
	}
	withRoots(t, roots)
	c, out, errb := runCLI(t, srv.URL, "")
	t.Setenv("ACN_APP_TOKEN", "acn_app")
	t.Setenv("TAVILY_API_KEY", "real-one-stays")
	t.Setenv("OPENAI_API_KEY", "")
	keys := []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "NO_PROXY", "NODE_USE_ENV_PROXY", "NODE_EXTRA_CA_CERTS",
		"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "OPENAI_API_KEY", "TAVILY_API_KEY"}
	if code := c.exec(helperArgs("env", keys...)); code != 0 {
		t.Fatalf("run: %d %s %s", code, out, errb)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
		t.Fatalf("child output: %q", out)
	}
	if env["HTTPS_PROXY"] != "http://app:acn_app@proxy.example:8888" || env["HTTP_PROXY"] != env["HTTPS_PROXY"] {
		t.Fatalf("proxy: %v", env)
	}
	if env["NO_PROXY"] != "localhost,127.0.0.1,::1" || env["NODE_USE_ENV_PROXY"] != "1" {
		t.Fatalf("no_proxy: %v", env)
	}
	if b, _ := os.ReadFile(env["NODE_EXTRA_CA_CERTS"]); string(b) != testCA {
		t.Fatalf("node CA: %q", b)
	}
	// SSL_CERT_FILE replaces the trust store, so it gets the roots plus the CA.
	b, _ := os.ReadFile(env["SSL_CERT_FILE"])
	if !strings.Contains(string(b), "ROOTS") || !strings.Contains(string(b), "MIIB") || env["REQUESTS_CA_BUNDLE"] != env["SSL_CERT_FILE"] {
		t.Fatalf("bundle %s: %q", env["SSL_CERT_FILE"], b)
	}
	// Placeholders only for known providers, and never over a real key.
	if env["OPENAI_API_KEY"] != placeholderKey || env["TAVILY_API_KEY"] != "real-one-stays" {
		t.Fatalf("placeholders: %v", env)
	}
	// The parent's environment is untouched.
	if os.Getenv("HTTPS_PROXY") != "" && os.Getenv("HTTPS_PROXY") == env["HTTPS_PROXY"] {
		t.Fatal("acn run changed its own environment")
	}
}

func TestRunPassesStdioAndExitCode(t *testing.T) {
	srv := runTestServer(t)
	withRoots(t)
	c, out, errb := runCLI(t, srv.URL, "hello")
	if code := c.exec(append([]string{"run", "--token", "acn_app", "--no-placeholders", "--"}, helperArgs("echo")[2:]...)); code != 0 {
		t.Fatalf("echo: %d %s %s", code, out, errb)
	}
	if out.String() != "out:hello" || !strings.Contains(errb.String(), "err:line") {
		t.Fatalf("stdio: %q %q", out, errb)
	}
	// No system roots: the trust store variables stay unset, with a note.
	if !strings.Contains(errb.String(), "SSL_CERT_FILE") {
		t.Fatalf("no note about the missing bundle: %q", errb)
	}
	c, out, errb = runCLI(t, srv.URL, "")
	t.Setenv("ACN_APP_TOKEN", "acn_app")
	if code := c.exec(helperArgs("exit")); code != 42 {
		t.Fatalf("exit code %d, want 42 (%s %s)", code, out, errb)
	}
	if strings.Contains(errb.String(), "error:") {
		t.Fatalf("the child's exit code is no CLI error: %q", errb)
	}
}

func TestRunNeedsAnAppToken(t *testing.T) {
	srv := runTestServer(t)
	c, _, errb := runCLI(t, srv.URL, "")
	t.Setenv("ACN_APP_TOKEN", "")
	if code := c.exec([]string{"run", "--", "anything"}); code != 1 || !strings.Contains(errb.String(), "ACN_APP_TOKEN") {
		t.Fatalf("no token: %d %q", code, errb)
	}
	c, _, errb = runCLI(t, srv.URL, "")
	if code := c.exec([]string{"run", "--token", "acn_app"}); code != 1 || !strings.Contains(errb.String(), "usage") {
		t.Fatalf("no command: %d %q", code, errb)
	}
	c, _, errb = runCLI(t, srv.URL, "")
	if code := c.exec([]string{"run", "--token", "acn_wrong", "--", "x"}); code != 1 || !strings.Contains(errb.String(), "bad token") {
		t.Fatalf("bad token: %d %q", code, errb)
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"PATH=/bin", "https_proxy=old", "=C:=C:\\x", "Path=keep"}
	got := mergeEnv(base, map[string]string{"HTTPS_PROXY": "new", "PATH": "/x"}, "windows")
	want := "=C:=C:\\x HTTPS_PROXY=new PATH=/x"
	if strings.Join(got, " ") != want {
		t.Fatalf("windows: %v", got)
	}
	got = mergeEnv(base, map[string]string{"HTTPS_PROXY": "new"}, "linux")
	if strings.Join(got, " ") != "PATH=/bin https_proxy=old =C:=C:\\x Path=keep HTTPS_PROXY=new" {
		t.Fatalf("linux: %v", got)
	}
}
