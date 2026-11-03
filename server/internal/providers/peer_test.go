package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func peerSpec() *Spec {
	s := peerBuiltin().Spec
	s.Normalize()
	return &s
}

func TestPeerClassification(t *testing.T) {
	s := peerSpec()
	cases := []struct {
		status int
		body   string
		want   Outcome
	}{
		{403, `{"error":{"code":"share_revoked","message":"x"}}`, Invalid},
		{403, `{"error":{"code":"share_paused","message":"x"}}`, Invalid},
		{401, `{"error":{"code":"unauthenticated","message":"x"}}`, Invalid},
		{429, `{"error":{"code":"app_limit_reached","message":"x"},"retry_after_sec":120}`, QuotaExhausted},
		{503, `{"error":{"code":"no_key_available","message":"x"},"retry_after_sec":30}`, RateLimited},
		{503, `{"error":{"code":"all_keys_busy","message":"x"},"retry_after_sec":2}`, RateLimited},
		{503, `{"error":{"code":"service_unavailable","message":"x"},"retry_after_sec":9}`, RateLimited},
		// Relayed from the upstream provider: the owner already rotated.
		{429, `{"error":"rate limited upstream"}`, OK},
		{401, `{"error":"Invalid API key"}`, Invalid}, // 401 is always the token
		{502, `{"error":{"code":"upstream_unreachable"}}`, OK},
		{500, `boom`, OK},
		{200, `{}`, OK},
	}
	for _, c := range cases {
		if got := s.Classify(c.status, http.Header{}, []byte(c.body)); got.Outcome != c.want {
			t.Errorf("%d %s: got %s want %s", c.status, c.body, got.Outcome, c.want)
		}
	}
	// The share limit is exhausted until its reset.
	r := s.Classify(429, http.Header{}, []byte(`{"error":{"code":"app_limit_reached"},"retry_after_sec":120}`))
	if r.ResetAt == nil || time.Until(*r.ResetAt) < 100*time.Second {
		t.Fatalf("reset %v", r.ResetAt)
	}
	r = s.Classify(503, http.Header{}, []byte(`{"error":{"code":"no_key_available"},"retry_after_sec":30}`))
	if r.RetryAfter != 30*time.Second {
		t.Fatalf("retry %v", r.RetryAfter)
	}
}

func TestPeerHealthCheckUsesOrigin(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"active"}`))
	}))
	defer srv.Close()
	res, err := peerSpec().Check(context.Background(), srv.Client(), srv.URL+"/proxy/serpapi", "acn_token")
	if err != nil || !res.Valid {
		t.Fatalf("%+v %v", res, err)
	}
	if path != "/api/peer/v1/manifest" || auth != "Bearer acn_token" {
		t.Fatalf("path %q auth %q", path, auth)
	}
}
