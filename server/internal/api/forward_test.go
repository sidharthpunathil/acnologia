package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// proxyClient sends every request through the Acnologia forward proxy.
func (e *env) proxyClient(token string, roots *x509.CertPool) *http.Client {
	pu, _ := url.Parse(e.srv.URL)
	if token != "" {
		pu.User = url.UserPassword("app", token)
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy:           http.ProxyURL(pu),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
}

func (e *env) proxyCA() *x509.CertPool {
	e.t.Helper()
	resp, err := http.Get(e.srv.URL + "/api/v1/proxy/ca.pem")
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	pem, _ := io.ReadAll(resp.Body)
	pool := x509.NewCertPool()
	if resp.StatusCode != 200 || !pool.AppendCertsFromPEM(pem) {
		e.t.Fatalf("no CA: %d %s", resp.StatusCode, pem)
	}
	return pool
}

// A plain HTTP request to a service's own URL, through the proxy, gets a
// pool key in place of whatever key the app sent.
func TestForwardProxyPlainHTTP(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "poolkey1"})
	_, tok := e.app(map[string]any{"name": "fwd", "mode": "proxy"})

	resp, err := e.proxyClient(tok, nil).Get(e.upURL + "/search.json?q=coffee&api_key=dummy")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"q":"coffee"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if e.up.hits["poolkey1"] == 0 || e.up.hits["dummy"] != 0 {
		t.Fatalf("the pool key was not used: %v", e.up.hits)
	}

	// No token: 407 with a challenge, so clients send the URL's credentials.
	resp, err = e.proxyClient("", nil).Get(e.upURL + "/search.json?q=x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 407 || resp.Header.Get("Proxy-Authenticate") == "" {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	// A host that is no service is refused: never an open proxy.
	resp, err = e.proxyClient(tok, nil).Get("http://example.invalid/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("other host: %d", resp.StatusCode)
	}
}

// HTTPS through CONNECT: the proxy opens the tunnel with a certificate from
// its own CA and puts the pool key on the request.
func TestForwardProxyHTTPS(t *testing.T) {
	e := newEnv(t)
	tlsUp := httptest.NewTLSServer(e.up)
	t.Cleanup(tlsUp.Close)
	e.api.UpClient = tlsUp.Client()
	var svc store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serp-tls", "base_url": tlsUp.URL}, 201, &svc)
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(svc.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "tlskey1"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "fwd", "mode": "proxy"})
	roots := e.proxyCA()

	resp, err := e.proxyClient(tok, roots).Get(tlsUp.URL + "/search.json?q=tea")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"q":"tea"`) || resp.Header.Get("X-Acnologia-Service") != "serp-tls" {
		t.Fatalf("%d %v %s", resp.StatusCode, resp.Header, body)
	}
	if e.up.hits["tlskey1"] == 0 {
		t.Fatalf("the pool key was not used: %v", e.up.hits)
	}
	// Passthrough is off here, so CONNECT to another host is refused.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	_, err = e.proxyClient(tok, roots).Get("https://" + ln.Addr().String() + "/")
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("CONNECT to another host: %v", err)
	}

	// The setup lists the hosts the app's token reaches.
	var setup struct {
		ProxyURL string        `json:"proxy_url"`
		Hosts    []forwardHost `json:"hosts"`
		CA       string        `json:"ca_fingerprint"`
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/proxy", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(r2.Body).Decode(&setup)
	r2.Body.Close()
	found := false
	for _, h := range setup.Hosts {
		found = found || h.Service == "serp-tls"
	}
	if !found || setup.CA == "" || setup.ProxyURL == "" {
		t.Fatalf("setup: %+v", setup)
	}
}

func TestForwardRouteLongestPathAndHosts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := &store.App{}
	host, port := splitHostPort(strings.TrimPrefix(e.upURL, "http://"), "http")
	slug, rest, err := e.api.forwardRoute(ctx, a, host, port, "/search.json", "")
	if err != nil || slug != "serpapi" || rest != "/search.json" {
		t.Fatalf("%s %s %v", slug, rest, err)
	}
	// X-Acnologia-Service names a group or another account.
	if slug, _, _ := e.api.forwardRoute(ctx, a, host, port, "/search.json", "my-group"); slug != "my-group" {
		t.Fatal(slug)
	}
	if _, _, err := e.api.forwardRoute(ctx, a, "other.example", "443", "/", ""); err != errNotService {
		t.Fatal(err)
	}
	if r, ok := pathUnder("/v1/chat", "/v1"); !ok || r != "/chat" {
		t.Fatal(r)
	}
	if _, ok := pathUnder("/v10/chat", "/v1"); ok {
		t.Fatal("matched across a segment")
	}
}

// The dedicated listener speaks plain HTTP and TLS on one port, so both
// http:// and https:// proxy URLs work.
func TestForwardProxyListenerSniffsTLS(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "poolkey2"})
	_, tok := e.app(map[string]any{"name": "fwd", "mode": "proxy"})
	roots := e.proxyCA()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = e.api.ServeForwardProxy(ctx, ln) }()
	for _, scheme := range []string{"http", "https"} {
		pu := &url.URL{Scheme: scheme, Host: ln.Addr().String(), User: url.UserPassword("app", tok)}
		c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: roots}}}
		resp, err := c.Get(e.upURL + "/search.json?q=" + scheme)
		if err != nil {
			t.Fatalf("%s: %v", scheme, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"q":"`+scheme+`"`) {
			t.Fatalf("%s: %d %s", scheme, resp.StatusCode, body)
		}
	}
}

// An http:// URL for a service on https maps to the same host on 443.
func TestForwardPlainHTTPReachesHTTPSService(t *testing.T) {
	e := newEnv(t)
	tlsUp := httptest.NewTLSServer(e.up)
	t.Cleanup(tlsUp.Close)
	e.api.UpClient = tlsUp.Client()
	// Point a service at https://svc.test (port 443) and route an
	// http://svc.test request: same host, default ports differ.
	var svc store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "svc-https", "base_url": "https://svc.test"}, 201, &svc)
	e.api.fwdSvcs = nil
	ctx := context.Background()
	if _, _, err := e.api.forwardRoute(ctx, &store.App{}, "svc.test", "80", "/x", ""); err != errNotService {
		t.Fatal("port 80 should not match a 443 base on its own")
	}
	req := httptest.NewRequest("GET", "http://svc.test/search.json", nil)
	rec := httptest.NewRecorder()
	_, tok := e.app(map[string]any{"name": "fwd", "mode": "proxy"})
	a, _, _ := e.st.AppByToken(ctx, crypto.SHA256(tok))
	e.api.forwardDispatch(rec, req, a, tok, "http", "svc.test")
	if rec.Code == 403 && strings.Contains(rec.Body.String(), "not_a_service") {
		t.Fatalf("http:// did not reach the https service: %s", rec.Body)
	}
}
