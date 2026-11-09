package ctl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/forward-proxy":
			_, _ = io.WriteString(w, `{"proxy_url":"http://proxy.example:8888","ca_url":"x/ca.pem","ca_fingerprint":"AB:CD","passthrough":true,
				"hosts":[{"host":"serpapi.com","service":"serpapi"}]}`)
		case "/api/v1/proxy/ca.pem":
			_, _ = io.WriteString(w, "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c, out, errb, _ := newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")

	if code := c.exec([]string{"proxy"}); code != 0 || !strings.Contains(out.String(), "serpapi.com") || !strings.Contains(out.String(), "AB:CD") {
		t.Fatalf("proxy: %s %s", out, errb)
	}
	out.Reset()
	if code := c.exec([]string{"proxy", "env", "--token", "acn_abc"}); code != 0 ||
		!strings.Contains(out.String(), `export HTTPS_PROXY="http://app:acn_abc@proxy.example:8888"`) ||
		!strings.Contains(out.String(), `export NODE_EXTRA_CA_CERTS="acnologia-ca.pem"`) {
		t.Fatalf("env: %s %s", out, errb)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if code := c.exec([]string{"proxy", "ca", "--out", path}); code != 0 {
		t.Fatalf("ca: %s", errb)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Fatal("CA not saved")
	}
}
