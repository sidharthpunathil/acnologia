package providers

import (
	"net/http"
	"testing"
)

func TestBuiltinSpecsAreValidAndPassTheirTests(t *testing.T) {
	for _, b := range Builtins() {
		s := b.Spec
		s.Normalize()
		if errs := s.Validate(); len(errs) > 0 {
			t.Errorf("%s: %v", b.Slug, errs)
		}
		rep := s.RunTests()
		for _, r := range rep.Results {
			if !r.OK {
				t.Errorf("%s/%s: got %s want %s (%s)", b.Slug, r.Name, r.Got, r.Expect, r.Detail)
			}
		}
	}
}

func TestRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "30")
	if d := ParseRetryAfter(h); d.Seconds() != 30 {
		t.Fatalf("got %v", d)
	}
	var eleven Spec
	for _, b := range Builtins() {
		if b.Slug == "elevenlabs" {
			eleven = b.Spec
		}
	}
	r := eleven.Classify(429, http.Header{}, []byte(`{"detail":{"status":"too_many_concurrent_requests"}}`))
	if r.RetryAfter.Seconds() != 5 {
		t.Fatalf("concurrency cooldown = %v", r.RetryAfter)
	}
	g := Get("unknown-provider").Classify(429, h, nil)
	if g.Outcome != RateLimited || g.RetryAfter.Seconds() != 30 {
		t.Fatalf("generic fallback: %+v", g)
	}
}

func TestEngineFeatures(t *testing.T) {
	s := Spec{
		BaseURL: "https://x.io", Auth: Auth{Type: "bearer"},
		Rules: []Rule{
			{Name: "json code", Match: Match{Status: []int{400}, JSONPath: "error.code", JSONEquals: "RATE_LIMIT"}, Outcome: RateLimited,
				RetryAfter: &RetryAfter{Source: "body_json", Value: "$.error.retry_in"}},
			{Name: "regex", Match: Match{BodyRegex: `credits?\s+(are\s+)?exhausted`}, Outcome: QuotaExhausted},
			{Name: "header", Match: Match{Status: []int{200}, HeaderPresent: "X-Quota-Exceeded"}, Outcome: QuotaExhausted},
		},
		UseStandardRules: true,
	}
	s.Normalize()
	if errs := s.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	r := s.Classify(400, http.Header{}, []byte(`{"error":{"code":"rate_limit","retry_in":12}}`))
	if r.Outcome != RateLimited || r.RetryAfter.Seconds() != 12 {
		t.Fatalf("json rule: %+v", r)
	}
	if s.Classify(400, http.Header{}, []byte(`{"error":{"code":"other"}}`)).Outcome != OK {
		t.Fatal("400 without match should be ok")
	}
	if s.Classify(403, http.Header{}, []byte(`Your credits are exhausted`)).Outcome != QuotaExhausted {
		t.Fatal("regex rule should win over standard 403")
	}
	h := http.Header{}
	h.Set("X-Quota-Exceeded", "1")
	if s.Classify(200, h, nil).Outcome != QuotaExhausted {
		t.Fatal("explicit 200 rule")
	}
	if s.Classify(200, http.Header{}, nil).Outcome != OK {
		t.Fatal("plain 200")
	}
	bad := Spec{BaseURL: "ftp://x", Auth: Auth{Type: "query"}, Rules: []Rule{{Outcome: "nope"}}}
	if len(bad.Validate()) < 3 {
		t.Fatalf("expected validation errors, got %v", bad.Validate())
	}
	v, ok := JSONPath(map[string]any{"a": []any{map[string]any{"b": 3.0}}}, "$.a.0.b")
	if !ok || v.(float64) != 3 {
		t.Fatal("json path with index")
	}
}
