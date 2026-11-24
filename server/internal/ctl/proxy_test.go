package ctl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const (
	testCA    = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	testRoots = "-----BEGIN CERTIFICATE-----\nROOTS\n-----END CERTIFICATE-----\n"
)

func proxyTestServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/forward-proxy":
			_, _ = io.WriteString(w, `{"proxy_url":"http://proxy.example:8888","ca_url":"x/ca.pem","ca_fingerprint":"AB:CD","passthrough":true,
				"hosts":[{"host":"serpapi.com","service":"serpapi"}]}`)
		case "/api/v1/proxy/ca.pem":
			_, _ = io.WriteString(w, testCA)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withRoots points the system root search at one file for the test.
func withRoots(t *testing.T, paths ...string) {
	old := systemRootPaths
	systemRootPaths = paths
	t.Cleanup(func() { systemRootPaths = old })
	t.Setenv("SSL_CERT_FILE", "")
}

func exportValue(t *testing.T, out, key string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^export ` + key + `="([^"]*)"$`).FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestProxyCommands(t *testing.T) {
	srv := proxyTestServer(t)
	roots := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(roots, []byte(testRoots), 0o644); err != nil {
		t.Fatal(err)
	}
	withRoots(t, roots)
	c, out, errb, _ := newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")

	if code := c.exec([]string{"proxy"}); code != 0 || !strings.Contains(out.String(), "serpapi.com") || !strings.Contains(out.String(), "AB:CD") {
		t.Fatalf("proxy: %s %s", out, errb)
	}
	out.Reset()
	if code := c.exec([]string{"proxy", "env", "--token", "acn_abc"}); code != 0 ||
		!strings.Contains(out.String(), `export HTTPS_PROXY="http://app:acn_abc@proxy.example:8888"`) {
		t.Fatalf("env: %s %s", out, errb)
	}
	env := out.String()
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if exportValue(t, env, k) != "localhost,127.0.0.1,::1" {
			t.Fatalf("%s missing: %s", k, env)
		}
	}
	// NODE_EXTRA_CA_CERTS adds to Node's roots, so it gets the CA alone.
	nodeCA := exportValue(t, env, "NODE_EXTRA_CA_CERTS")
	if b, _ := os.ReadFile(nodeCA); string(b) != testCA {
		t.Fatalf("NODE_EXTRA_CA_CERTS %q should hold the CA alone: %q", nodeCA, b)
	}
	// SSL_CERT_FILE and REQUESTS_CA_BUNDLE replace the trust store, so they
	// get the system roots plus the CA.
	bundle := exportValue(t, env, "SSL_CERT_FILE")
	if bundle == "" || bundle == nodeCA || exportValue(t, env, "REQUESTS_CA_BUNDLE") != bundle {
		t.Fatalf("SSL_CERT_FILE and REQUESTS_CA_BUNDLE should share a bundle: %s", env)
	}
	b, _ := os.ReadFile(bundle)
	if !strings.Contains(string(b), "ROOTS") || !strings.Contains(string(b), "MIIB") {
		t.Fatalf("bundle should hold the system roots and the CA: %q", b)
	}
	if filepath.Dir(bundle) != c.proxyDir() {
		t.Fatalf("bundle %s should live next to the CLI config %s", bundle, c.proxyDir())
	}

	// Running env again with its own output in effect must not nest the
	// bundle inside itself.
	t.Setenv("SSL_CERT_FILE", bundle)
	out.Reset()
	if code := c.exec([]string{"proxy", "env", "--token", "acn_abc"}); code != 0 {
		t.Fatalf("env again: %s", errb)
	}
	if b2, _ := os.ReadFile(bundle); strings.Count(string(b2), "MIIB") != 1 {
		t.Fatalf("the bundle should hold the CA once: %q", b2)
	}

	path := filepath.Join(t.TempDir(), "ca.pem")
	if code := c.exec([]string{"proxy", "ca", "--out", path}); code != 0 {
		t.Fatalf("ca: %s", errb)
	}
	if b, _ := os.ReadFile(path); string(b) != testCA {
		t.Fatal("CA not saved")
	}
	bpath := filepath.Join(t.TempDir(), "bundle.pem")
	if code := c.exec([]string{"proxy", "ca", "--bundle", "--out", bpath}); code != 0 {
		t.Fatalf("ca --bundle: %s", errb)
	}
	if b, _ := os.ReadFile(bpath); !strings.Contains(string(b), "ROOTS") || !strings.Contains(string(b), "MIIB") {
		t.Fatalf("ca --bundle: %q", b)
	}
}

// With no system bundle to combine, env sets NODE_EXTRA_CA_CERTS only and
// says why, rather than pointing SSL_CERT_FILE at the CA alone.
func TestProxyEnvWithoutSystemRoots(t *testing.T) {
	srv := proxyTestServer(t)
	withRoots(t, filepath.Join(t.TempDir(), "missing.pem"))
	c, out, errb, _ := newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")
	if code := c.exec([]string{"proxy", "env"}); code != 0 {
		t.Fatalf("env: %s", errb)
	}
	env := out.String()
	if exportValue(t, env, "NODE_EXTRA_CA_CERTS") == "" || strings.Contains(env, "SSL_CERT_FILE") || strings.Contains(env, "REQUESTS_CA_BUNDLE") {
		t.Fatalf("only NODE_EXTRA_CA_CERTS should be set: %s", env)
	}
	if !strings.Contains(errb.String(), "no system certificate bundle") {
		t.Fatalf("env should say why: %s", errb)
	}
	if code := c.exec([]string{"proxy", "ca", "--bundle", "--out", filepath.Join(t.TempDir(), "b.pem")}); code == 0 {
		t.Fatal("ca --bundle with no system roots should fail")
	}
}

func TestSystemRootsWindows(t *testing.T) {
	if p, _ := systemRoots("windows", func(string) string { return "" }, ""); p != "" {
		t.Fatalf("windows has no root file: %q", p)
	}
	if runtime.GOOS == "windows" {
		return
	}
	f := filepath.Join(t.TempDir(), "mine.pem")
	_ = os.WriteFile(f, []byte(testRoots), 0o644)
	withRoots(t)
	env := func(k string) string {
		if k == "SSL_CERT_FILE" {
			return f
		}
		return ""
	}
	if p, _ := systemRoots("linux", env, ""); p != f {
		t.Fatalf("an existing SSL_CERT_FILE should be used: %q", p)
	}
	if p, _ := systemRoots("linux", env, filepath.Dir(f)); p != "" {
		t.Fatalf("a bundle in the CLI's own folder should be skipped: %q", p)
	}
}
