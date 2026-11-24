package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestForwardedClientIP(t *testing.T) {
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			out.Add(kv[i], kv[i+1])
		}
		return out
	}
	cases := []struct {
		name   string
		hdr    http.Header
		hops   int
		header string
		want   string
	}{
		{"one hop takes the rightmost entry", h("X-Forwarded-For", "6.6.6.6, 203.0.113.7"), 1, "", "203.0.113.7"},
		{"a spoofed leftmost entry is ignored", h("X-Forwarded-For", "1.1.1.1, 2.2.2.2, 203.0.113.7"), 1, "", "203.0.113.7"},
		{"two hops skip the inner proxy", h("X-Forwarded-For", "6.6.6.6, 203.0.113.7, 10.0.0.2"), 2, "", "203.0.113.7"},
		{"several header lines read as one list", h("X-Forwarded-For", "6.6.6.6", "X-Forwarded-For", "203.0.113.7"), 1, "", "203.0.113.7"},
		{"a chain shorter than the hops is not trusted", h("X-Forwarded-For", "203.0.113.7"), 2, "", ""},
		{"zero hops reads as one", h("X-Forwarded-For", "6.6.6.6, 203.0.113.7"), 0, "", "203.0.113.7"},
		{"garbage is not an address", h("X-Forwarded-For", "6.6.6.6, not-an-ip"), 1, "", ""},
		{"an empty rightmost entry is not an address", h("X-Forwarded-For", "6.6.6.6, "), 1, "", ""},
		{"a port is dropped", h("X-Forwarded-For", "203.0.113.7:5555"), 1, "", "203.0.113.7"},
		{"IPv6 in brackets with a port", h("X-Forwarded-For", "[2001:db8::1]:443"), 1, "", "2001:db8::1"},
		{"IPv4 mapped IPv6 is unmapped", h("X-Forwarded-For", "::ffff:203.0.113.7"), 1, "", "203.0.113.7"},
		{"True-Client-IP and X-Real-IP are ignored", h("True-Client-IP", "1.1.1.1", "X-Real-IP", "2.2.2.2", "X-Forwarded-For", "203.0.113.7"), 1, "", "203.0.113.7"},
		{"they do not stand in for a missing X-Forwarded-For", h("True-Client-IP", "1.1.1.1", "X-Real-IP", "2.2.2.2"), 1, "", ""},
		{"a named header is read alone", h("Fly-Client-Ip", "203.0.113.9", "X-Forwarded-For", "6.6.6.6"), 1, "Fly-Client-Ip", "203.0.113.9"},
		{"a named header that is missing is not replaced by X-Forwarded-For", h("X-Forwarded-For", "6.6.6.6"), 1, "Fly-Client-Ip", ""},
		{"a named header must hold an address", h("Fly-Client-Ip", "evil"), 1, "Fly-Client-Ip", ""},
	}
	for _, c := range cases {
		if got := forwardedClientIP(c.hdr, c.hops, c.header); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRealIPMiddleware(t *testing.T) {
	var seen string
	h := realIP(1, "fly-client-ip")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr }))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("Fly-Client-IP", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "203.0.113.9" {
		t.Fatalf("the named header (any case) should set the client IP: %q", seen)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("Fly-Client-IP", "nope")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "10.0.0.1:1234" {
		t.Fatalf("an invalid header should leave the socket address: %q", seen)
	}
}

// With proxy headers trusted, a client that sends a fresh True-Client-IP,
// X-Real-IP and leftmost X-Forwarded-For on every attempt still lands in
// one limiter bucket: the proxy appended the same address each time.
func TestLoginLimiterIgnoresSpoofedHeadersWhenTrusted(t *testing.T) {
	e := newEnv(t)
	e.api.Cfg.TrustProxyHeaders = true
	e.api.Cfg.TrustedProxyHops = 1
	// The router reads the config once; build it again with the new one.
	e.api.routerOnce = sync.Once{}
	got429 := false
	for i := 0; i < 15; i++ {
		c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp := e.raw(c, "POST", "/api/auth/login", "application/json",
			[]byte(`{"email":"root@x.io","password":"wrongwrong1"}`),
			"True-Client-IP", "10.2.0."+itoa(i), "X-Real-IP", "10.0.0."+itoa(i),
			"X-Forwarded-For", "10.1.0."+itoa(i)+", 198.51.100.4")
		if resp.StatusCode == 429 {
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("spoofed client IP headers bypassed the login limiter with proxy headers trusted")
	}
}
