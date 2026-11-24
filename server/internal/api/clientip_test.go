package api

import (
	"net/http"
	"sync"
	"testing"
)

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
