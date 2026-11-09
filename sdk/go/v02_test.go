package acnologia

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v02Server is a stub for the 0.2.0 calls. It records the last request.
type v02Server struct {
	t      *testing.T
	caPEM  string
	last   *http.Request
	body   string
	srvURL string
	tlsURL string
}

func (s *v02Server) handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		b, _ := io.ReadAll(r.Body)
		s.last, s.body = r, string(b)
		if r.Header.Get("Authorization") != "Bearer acn_test" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"bad token"}}`)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = io.WriteString(w, `{"app":{"id":3,"name":"bot"},"limits":{"daily_requests":100,"monthly_requests":null,"daily_budget":null,"monthly_budget":5.5},
			"usage":{"today_requests":7,"today_cost":0.1,"month_requests":70,"month_cost":1,"last_used_at":"2026-11-01T10:00:00Z","limit_hit":null},
			"limit_hit":{"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z"},
			"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":1,"logo_url":null}],
			"share":{"id":"sh1","name":"Team","owner_name":"Ann","recipient":"bob","label":"x","status":"paused","mode":"proxy","ends_at":null}}`)
	})
	mux.HandleFunc("GET /api/v1/services", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = io.WriteString(w, `{"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":true,"base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"},"guide_url":"http://x/api/v1/services/serpapi/guide"}]}`)
	})
	mux.HandleFunc("GET /api/v1/services/{slug}/guide", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = io.WriteString(w, "# "+r.PathValue("slug"))
			return
		}
		_, _ = io.WriteString(w, `{"markdown":"# serpapi","generated":true,"guide":{"endpoints":[1]}}`)
	})
	mux.HandleFunc("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = io.WriteString(w, `{"capabilities":[{"slug":"web-search","name":"Web search","description":"d","strategy":"failover","params":{"q":{"type":"string"}},"routes":[{"service":"serpapi","mapped":true}],"url":"http://x/proxy/web-search"}]}`)
	})
	mux.HandleFunc("GET /api/v1/proxy", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"proxy_url": "http://proxy.test:8888", "proxy_url_with_token": "http://app:%3Capp%20token%3E@proxy.test:8888",
			"ca_url": s.srvURL + "/api/v1/proxy/ca.pem", "ca_fingerprint": "ab:cd", "passthrough": true,
			"hosts":          []map[string]any{{"host": "serpapi.com", "service": "serpapi", "name": "SerpAPI"}},
			"env":            map[string]string{"HTTPS_PROXY": "http://app:%3Capp%20token%3E@proxy.test:8888"},
			"service_header": "X-Acnologia-Service",
		})
	})
	mux.HandleFunc("GET /api/v1/proxy/ca.pem", func(w http.ResponseWriter, r *http.Request) {
		s.last = r
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = io.WriteString(w, s.caPEM)
	})
	mux.HandleFunc("/proxy/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		switch r.URL.Query().Get("fail") {
		case "limit":
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"code":"app_limit_reached","message":"this app reached its daily requests limit"},"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z","retry_after_sec":60}`)
			return
		case "share":
			w.Header().Set("X-Acnologia-Share", "revoked")
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"error":{"code":"share_revoked","message":"the owner revoked this share"}}`)
			return
		}
		w.Header().Set("X-Acnologia-Key-Id", "7")
		w.Header().Set("X-Acnologia-Attempts", "2")
		w.Header().Set("X-Acnologia-Service", "serpapi")
		w.Header().Set("X-Acnologia-Cache", "miss")
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("X-Acnologia-Capability", "web-search")
		w.Header().Set("X-Acnologia-Tried", "brave=429, serpapi=200")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	return mux
}

func setupV02(t *testing.T, opts ...Option) (*Client, *v02Server) {
	t.Helper()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secure")
	}))
	t.Cleanup(tlsSrv.Close)
	s := &v02Server{t: t, caPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsSrv.Certificate().Raw}))}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	s.srvURL, s.tlsURL = srv.URL, tlsSrv.URL
	return New(srv.URL, "acn_test", opts...), s
}

func TestVersion(t *testing.T) {
	if Version != "0.2.0" {
		t.Fatal(Version)
	}
}

func TestMe(t *testing.T) {
	c, s := setupV02(t)
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.last.Method != "GET" || s.last.URL.Path != "/api/v1/me" {
		t.Fatalf("%s %s", s.last.Method, s.last.URL.Path)
	}
	if me.App.ID != 3 || me.App.Name != "bot" || *me.Limits.DailyRequests != 100 || me.Limits.MonthlyRequests != nil || *me.Limits.MonthlyBudget != 5.5 {
		t.Fatalf("%+v", me)
	}
	if me.Usage.TodayRequests != 7 || me.Usage.LastUsedAt == nil || me.LimitHit == nil || me.LimitHit.Limit != "daily_requests" {
		t.Fatalf("%+v", me.Usage)
	}
	if len(me.Services) != 1 || me.Services[0].Available != 1 || me.Share == nil || me.Share.Status != "paused" || me.Share.OwnerName != "Ann" {
		t.Fatalf("%+v", me)
	}
}

func TestServicesGuideURL(t *testing.T) {
	c, _ := setupV02(t)
	svcs, err := c.Services(context.Background())
	if err != nil || len(svcs) != 1 || svcs[0].GuideURL != "http://x/api/v1/services/serpapi/guide" {
		t.Fatalf("%v %+v", err, svcs)
	}
}

func TestGuide(t *testing.T) {
	c, s := setupV02(t)
	g, err := c.Guide(context.Background(), "serpapi")
	if err != nil {
		t.Fatal(err)
	}
	if s.last.URL.Path != "/api/v1/services/serpapi/guide" || !strings.Contains(s.last.Header.Get("Accept"), "application/json") {
		t.Fatalf("%s %s", s.last.URL.Path, s.last.Header.Get("Accept"))
	}
	if g.Markdown != "# serpapi" || !g.Generated || string(g.Guide) != `{"endpoints":[1]}` {
		t.Fatalf("%+v", g)
	}
	md, err := c.GuideMarkdown(context.Background(), "serpapi")
	if err != nil || md != "# serpapi" || s.last.Header.Get("Accept") != "text/markdown" {
		t.Fatalf("%v %q %q", err, md, s.last.Header.Get("Accept"))
	}
}

func TestCapabilities(t *testing.T) {
	c, s := setupV02(t)
	cs, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.last.Method != "GET" || s.last.URL.Path != "/api/v1/capabilities" {
		t.Fatal(s.last.URL.Path)
	}
	if len(cs) != 1 || cs[0].Slug != "web-search" || cs[0].Strategy != "failover" || len(cs[0].Routes) != 1 || !cs[0].Routes[0].Mapped || cs[0].URL != "http://x/proxy/web-search" {
		t.Fatalf("%+v", cs)
	}
	var params map[string]any
	if err := json.Unmarshal(cs[0].Params, &params); err != nil || params["q"] == nil {
		t.Fatalf("%v %s", err, cs[0].Params)
	}
}

func TestCapabilityCall(t *testing.T) {
	c, s := setupV02(t)
	resp, err := c.Capability(context.Background(), "web-search", map[string]any{"q": "coffee"},
		&CapabilityOptions{Service: "serpapi", Raw: true, IdempotencyKey: "k1", NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := s.last
	if r.Method != "POST" || r.URL.Path != "/proxy/web-search" || r.URL.Query().Get("_service") != "serpapi" || r.URL.Query().Get("_raw") != "1" {
		t.Fatalf("%s %s", r.Method, r.URL)
	}
	if s.body != `{"q":"coffee"}` || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Idempotency-Key") != "k1" || r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("%q %v", s.body, r.Header)
	}
	m := ProxyInfo(resp)
	if m.Capability != "web-search" || m.Service != "serpapi" || len(m.Tried) != 2 || m.Tried[0] != (Tried{"brave", 429}) || m.Tried[1] != (Tried{"serpapi", 200}) {
		t.Fatalf("%+v", m)
	}

	// No options: no query string, an empty object for nil params.
	resp, err = c.Capability(context.Background(), "web-search", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s.last.URL.RawQuery != "" || s.body != `{}` || s.last.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("%q %q", s.last.URL.RawQuery, s.body)
	}
}

func TestProxyOptionsAndInfo(t *testing.T) {
	c, s := setupV02(t)
	base := c.Proxy("serpapi")
	p := base.WithIdempotencyKey("abc").WithNoCache()
	resp, err := p.PostJSON(context.Background(), "/search", map[string]string{"q": "x"})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s.last.Header.Get("Idempotency-Key") != "abc" || s.last.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("%v", s.last.Header)
	}
	m := ProxyInfo(resp)
	if m.KeyID != 7 || m.Attempts != 2 || m.Service != "serpapi" || m.Cache != "miss" || !m.Replayed {
		t.Fatalf("%+v", m)
	}
	// The original Proxy is unchanged.
	resp, err = base.Get(context.Background(), "/search", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s.last.Header.Get("Idempotency-Key") != "" || s.last.Header.Get("Cache-Control") != "" {
		t.Fatalf("%v", s.last.Header)
	}
	if got := ProxyInfo(&http.Response{Header: http.Header{}}); got.KeyID != 0 || got.Tried != nil || got.Replayed {
		t.Fatalf("%+v", got)
	}
}

func TestAccessPassword(t *testing.T) {
	c, s := setupV02(t, WithAccessPassword("gate"))
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.last.Header.Get("X-Acnologia-Access") != "gate" {
		t.Fatal("no access header")
	}
	resp, err := c.Proxy("serpapi").Get(context.Background(), "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s.last.Header.Get("X-Acnologia-Access") != "gate" {
		t.Fatal("no access header on proxy")
	}
	c2, s2 := setupV02(t)
	_, _ = c2.Me(context.Background())
	if _, ok := s2.last.Header["X-Acnologia-Access"]; ok {
		t.Fatal("access header sent without a password")
	}
}

func TestAppLimitReached(t *testing.T) {
	c, _ := setupV02(t)
	err := c.do(context.Background(), http.MethodGet, "/proxy/x?fail=limit", nil, nil)
	var al *ErrAppLimitReached
	if !errors.As(err, &al) || al.Limit != "daily_requests" || al.RetryAfter != time.Minute || al.StatusCode != 429 || al.Code != "app_limit_reached" {
		t.Fatalf("%v", err)
	}
	if !al.ResetAt.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(al.ResetAt)
	}
	if !errors.Is(err, AppLimitReached) || errors.Is(err, ShareUnavailable) {
		t.Fatal("sentinels")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "app_limit_reached" {
		t.Fatal("should match *APIError")
	}
}

func TestShareUnavailable(t *testing.T) {
	c, _ := setupV02(t)
	err := c.do(context.Background(), http.MethodGet, "/proxy/x?fail=share", nil, nil)
	var su *ErrShareUnavailable
	if !errors.As(err, &su) || su.Status != "revoked" || su.StatusCode != 403 || su.Code != "share_revoked" {
		t.Fatalf("%v", err)
	}
	if !errors.Is(err, ShareUnavailable) {
		t.Fatal("sentinel")
	}
	// Without the header the status comes from the code.
	if e := parseError(403, []byte(`{"error":{"code":"share_paused","message":"m"}}`), http.Header{}); !errors.As(e, &su) || su.Status != "paused" {
		t.Fatalf("%v", e)
	}
	// Retry-After reaches the generic error too.
	e := parseError(400, []byte(`{"error":{"code":"bad_params","message":"q"},"retry_after_sec":2}`), http.Header{})
	var ae *APIError
	if !errors.As(e, &ae) || ae.Code != "bad_params" || ae.RetryAfter != 2*time.Second {
		t.Fatalf("%v", e)
	}
}

func TestForwardProxy(t *testing.T) {
	c, s := setupV02(t)
	fp, err := c.ForwardProxy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.last.URL.Path != "/api/v1/proxy" || s.last.Header.Get("Authorization") != "Bearer acn_test" {
		t.Fatal(s.last.URL.Path)
	}
	if fp.ProxyURL != "http://proxy.test:8888" || fp.CAFingerprint != "ab:cd" || !fp.Passthrough || len(fp.Hosts) != 1 || fp.Hosts[0].Host != "serpapi.com" || fp.ServiceHeader != "X-Acnologia-Service" || fp.Env["HTTPS_PROXY"] == "" {
		t.Fatalf("%+v", fp)
	}
	for _, f := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(sprintf(f, fp), "acn_test") {
			t.Fatal("token in", f)
		}
	}

	u, err := c.ForwardProxyURL(context.Background())
	if err != nil || u != "http://app:acn_test@proxy.test:8888" {
		t.Fatalf("%v %q", err, u)
	}
	env, err := c.ForwardProxyEnv(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"HTTPS_PROXY": u, "HTTP_PROXY": u, "NODE_USE_ENV_PROXY": "1",
		"NODE_EXTRA_CA_CERTS": "acnologia-ca.pem", "REQUESTS_CA_BUNDLE": "acnologia-ca.pem", "SSL_CERT_FILE": "acnologia-ca.pem"}
	if len(env) != len(want) {
		t.Fatalf("%v", env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Fatalf("%s=%q", k, env[k])
		}
	}
	env, _ = c.ForwardProxyEnv(context.Background(), "/etc/ca.pem")
	if env["SSL_CERT_FILE"] != "/etc/ca.pem" {
		t.Fatal(env)
	}
}

func TestForwardProxyURLEscapesToken(t *testing.T) {
	c := New("http://x", "acn_a/b@c")
	u, err := c.proxyURLWithToken("http://proxy.test:8888")
	if err != nil || u.String() != "http://app:acn_a%2Fb%40c@proxy.test:8888" {
		t.Fatalf("%v %v", err, u)
	}
}

func TestForwardProxyCA(t *testing.T) {
	c, s := setupV02(t, WithAccessPassword("gate"))
	pemText, err := c.ForwardProxyCA(context.Background())
	if err != nil || pemText != s.caPEM {
		t.Fatalf("%v", err)
	}
	if s.last.URL.Path != "/api/v1/proxy/ca.pem" || s.last.Header.Get("Authorization") != "" {
		t.Fatalf("token sent to the public CA route: %v", s.last.Header)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	got, err := c.SaveForwardProxyCA(context.Background(), path)
	if err != nil || got != path {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != s.caPEM {
		t.Fatal("saved CA differs")
	}
}

func TestForwardProxyTransport(t *testing.T) {
	c, s := setupV02(t)
	tr, err := c.ForwardProxyTransport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "https://api.example.com/v1", nil)
	pu, err := tr.Proxy(req)
	if err != nil || pu.String() != "http://app:acn_test@proxy.test:8888" {
		t.Fatalf("%v %v", err, pu)
	}
	// The transport trusts the Acnologia CA: here the stub's CA is the
	// certificate of a TLS test server, reached without the proxy.
	tr.Proxy = nil
	resp, err := (&http.Client{Transport: tr}).Get(s.tlsURL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "secure" {
		t.Fatal(string(b))
	}
	// The plain default transport does not trust it.
	if _, err := http.Get(s.tlsURL); err == nil {
		t.Fatal("default transport trusted the test CA")
	}
}

func sprintf(f string, v any) string { return fmt.Sprintf(f, v) }
