package providers

import (
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		p      Provider
		status int
		body   string
		want   Outcome
	}{
		{"serp ok", serpAPI{}, 200, `{"search_metadata":{}}`, OK},
		{"serp out of searches", serpAPI{}, 429, `{"error":"Your account has run out of searches."}`, QuotaExhausted},
		{"serp hourly", serpAPI{}, 429, `{"error":"Your searches for the past hour have exceeded the limit of your plan."}`, RateLimited},
		{"serp invalid", serpAPI{}, 401, `{"error":"Invalid API key. Your API key should be here: https://serpapi.com/manage-api-key"}`, Invalid},
		{"serp 500", serpAPI{}, 503, `oops`, Error},
		{"eleven quota", elevenLabs{}, 401, `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota."}}`, QuotaExhausted},
		{"eleven concurrency", elevenLabs{}, 429, `{"detail":{"status":"too_many_concurrent_requests","message":"Too many"}}`, RateLimited},
		{"eleven busy", elevenLabs{}, 429, `{"detail":{"status":"system_busy","message":"busy"}}`, Error},
		{"eleven invalid", elevenLabs{}, 401, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`, Invalid},
		{"eleven bad input", elevenLabs{}, 422, `{"detail":[{"msg":"field required"}]}`, OK},
		{"generic 429", generic{}, 429, ``, RateLimited},
		{"generic 402", generic{}, 402, ``, QuotaExhausted},
		{"generic 403", generic{}, 403, ``, Invalid},
	}
	for _, c := range cases {
		got := c.p.Classify(c.status, http.Header{}, []byte(c.body))
		if got.Outcome != c.want {
			t.Errorf("%s: got %s want %s", c.name, got.Outcome, c.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "30")
	if d := ParseRetryAfter(h); d.Seconds() != 30 {
		t.Fatalf("got %v", d)
	}
	r := elevenLabs{}.Classify(429, http.Header{}, []byte(`{"detail":{"status":"too_many_concurrent_requests"}}`))
	if r.RetryAfter.Seconds() != 5 {
		t.Fatalf("default concurrency cooldown = %v", r.RetryAfter)
	}
}
