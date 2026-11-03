package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// inferenceSlugs are the LLM providers added with labBuiltins and hostBuiltins.
var inferenceSlugs = []string{
	"openai", "anthropic", "xai", "mistral", "deepseek", "cohere", "perplexity",
	"together", "groq", "fireworks", "cerebras", "openrouter",
}

func builtinSpec(t *testing.T, slug string) (Builtin, Spec) {
	t.Helper()
	for _, b := range Builtins() {
		if b.Slug == slug {
			s := b.Spec
			s.Normalize()
			return b, s
		}
	}
	t.Fatalf("builtin %s missing", slug)
	return Builtin{}, Spec{}
}

// Every new provider ships a logo, a free health check, examples for the
// Integrate panel, cited offers and a fixture for each outcome that matters.
func TestInferenceBuiltinsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range Builtins() {
		if seen[b.Slug] {
			t.Errorf("slug %s is used twice", b.Slug)
		}
		seen[b.Slug] = true
	}
	for _, slug := range inferenceSlugs {
		b, s := builtinSpec(t, slug)
		if b.Name == "" || b.Description == "" || b.Website == "" || b.DocsURL == "" || b.Color == "" || b.Category != "LLM" {
			t.Errorf("%s: metadata incomplete", slug)
		}
		logo, mime := SeedLogo(slug)
		if mime != "image/png" {
			t.Errorf("%s: no PNG seed logo", slug)
		} else if cfg, err := png.DecodeConfig(bytes.NewReader(logo)); err != nil || cfg.Width != 180 || cfg.Height != 180 {
			t.Errorf("%s: seed logo should be a 180x180 PNG: %v %+v", slug, err, cfg)
		}
		if s.Health == nil {
			t.Errorf("%s: no health check", slug)
		}
		var safe, post bool
		for _, e := range s.Examples {
			safe = safe || (e.Safe && e.Method == "GET")
			post = post || e.Method == "POST"
		}
		if !safe || !post {
			t.Errorf("%s: needs a safe GET example and a POST example", slug)
		}
		if len(s.Offers) == 0 {
			t.Errorf("%s: no offers", slug)
		}
		for _, o := range s.Offers {
			if o.CheckedAt != llmChecked || !strings.HasPrefix(o.SourceURL, "https://") {
				t.Errorf("%s/%s: offers need source_url and checked_at %s", slug, o.Name, llmChecked)
			}
		}
		want := map[Outcome]bool{OK: false, Invalid: false, RateLimited: false, Error: false}
		exhausted := false
		for _, f := range s.Tests {
			e := f.Expect
			if e == Busy && f.Status == 429 {
				e = RateLimited // DeepSeek's 429 is its per account concurrency cap
			}
			if _, ok := want[e]; ok {
				want[e] = true
			}
			exhausted = exhausted || f.Expect == QuotaExhausted
		}
		for o, ok := range want {
			if !ok {
				t.Errorf("%s: no fixture expects %s", slug, o)
			}
		}
		if !exhausted {
			t.Errorf("%s: no fixture for an exhausted balance or quota", slug)
		}
	}
}

// Rate limit fixtures that carry Retry-After must cool the key for that long.
func TestInferenceRetryAfterIsHonored(t *testing.T) {
	for _, slug := range inferenceSlugs {
		_, s := builtinSpec(t, slug)
		for _, f := range s.Tests {
			if f.Expect != RateLimited {
				continue
			}
			h := http.Header{}
			for k, v := range f.Headers {
				h.Set(k, v)
			}
			if h.Get("Retry-After") == "" {
				continue
			}
			r := s.Classify(f.Status, h, []byte(f.Body))
			if want := ParseRetryAfter(h); r.RetryAfter != want || want == 0 {
				t.Errorf("%s/%s: retry after %v, want %v", slug, f.Name, r.RetryAfter, want)
			}
		}
	}
	// A daily cap carries its refill time as the reset.
	_, groq := builtinSpec(t, "groq")
	h := http.Header{}
	h.Set("Retry-After", "3600")
	r := groq.Classify(429, h, []byte(`{"error":{"message":"Rate limit reached on requests per day (RPD)"}}`))
	if r.Outcome != QuotaExhausted || r.ResetAt == nil || time.Until(*r.ResetAt) < 59*time.Minute {
		t.Fatalf("groq daily cap: %+v", r)
	}
}

type observed struct {
	Provider string `json:"provider"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Key      string `json:"key"`
	Status   int    `json:"status"`
	Body     string `json:"body"`
}

// Responses recorded on 2026-11-03 from each real API, called with no key
// and with an obviously invalid one. Proxied traffic must read them as a bad
// key, never as ok or an outage, and the health check must call the key
// invalid rather than fail.
func TestInferenceObservedNoKeyResponses(t *testing.T) {
	raw, err := os.ReadFile("testdata/observed_no_key.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []observed
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, c := range cases {
		_, s := builtinSpec(t, c.Provider)
		healthPath := s.Health.Path == c.Path && strings.EqualFold(s.Health.Method, c.Method)
		if healthPath {
			covered[c.Provider] = true
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.Path || r.Method != c.Method {
					t.Errorf("%s: health called %s %s", c.Provider, r.Method, r.URL.Path)
				}
				w.WriteHeader(c.Status)
				_, _ = w.Write([]byte(c.Body))
			}))
			res, err := s.Check(context.Background(), srv.Client(), srv.URL, c.Key)
			srv.Close()
			if err != nil || res.Valid {
				t.Errorf("%s %s %s: health check should say invalid, got %+v %v", c.Provider, c.Method, c.Path, res, err)
			}
		}
		// Cohere's check endpoint answers 200, which proxied traffic treats as ok.
		if c.Status >= 400 {
			if r := s.Classify(c.Status, http.Header{}, []byte(c.Body)); r.Outcome != Invalid {
				t.Errorf("%s %s %s (%d): got %s want invalid", c.Provider, c.Method, c.Path, c.Status, r.Outcome)
			}
		}
	}
	for _, slug := range inferenceSlugs {
		if !covered[slug] {
			t.Errorf("%s: no observed response for its health endpoint", slug)
		}
	}
}

func checkWith(t *testing.T, slug string, status int, body string) *CheckResult {
	t.Helper()
	_, s := builtinSpec(t, slug)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	res, err := s.Check(context.Background(), srv.Client(), srv.URL, "k")
	if err != nil {
		t.Fatalf("%s: %v", slug, err)
	}
	return res
}

func TestInferenceHealthChecks(t *testing.T) {
	if r := checkWith(t, "anthropic", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"anthropic-version: header is required"}}`); !r.Valid {
		t.Error("anthropic: a key asked for anthropic-version works")
	}
	if r := checkWith(t, "cohere", 200, `{"valid":true,"organization_id":"o","owner_id":"u"}`); !r.Valid {
		t.Error("cohere: valid key")
	}
	if r := checkWith(t, "cohere", 200, `{"valid":false}`); r.Valid {
		t.Error("cohere: invalid key")
	}
	if r := checkWith(t, "xai", 200, `{"api_key_id":"a","team_blocked":false,"api_key_blocked":false,"api_key_disabled":false}`); !r.Valid {
		t.Error("xai: working key")
	}
	if r := checkWith(t, "xai", 200, `{"api_key_id":"a","team_blocked":false,"api_key_blocked":false,"api_key_disabled": true}`); r.Valid {
		t.Error("xai: disabled key")
	}
	r := checkWith(t, "deepseek", 200, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`)
	if !r.Valid || r.QuotaRemaining == nil || *r.QuotaRemaining != 110 {
		t.Errorf("deepseek balance: %+v", r)
	}
	r = checkWith(t, "openrouter", 200, `{"data":{"label":"k","limit":25,"limit_remaining":7.4,"usage":17.6,"is_free_tier":false}}`)
	if !r.Valid || r.QuotaLimit == nil || *r.QuotaLimit != 25 || r.QuotaRemaining == nil || *r.QuotaRemaining != 7 {
		t.Errorf("openrouter key: %+v", r)
	}
	r = checkWith(t, "openrouter", 200, `{"data":{"label":"k","limit":null,"limit_remaining":null,"usage":3}}`)
	if !r.Valid || r.QuotaRemaining != nil {
		t.Errorf("openrouter unlimited key: %+v", r)
	}
}

// TestInferenceLiveNoKey calls the real APIs with an invalid key. It needs
// the network, so it runs only when asked:
//
//	ACNOLOGIA_LIVE_PROVIDERS=1 go test ./internal/providers -run TestInferenceLiveNoKey -v
func TestInferenceLiveNoKey(t *testing.T) {
	if os.Getenv("ACNOLOGIA_LIVE_PROVIDERS") == "" {
		t.Skip("set ACNOLOGIA_LIVE_PROVIDERS=1 to call the real provider APIs")
	}
	c := &http.Client{Timeout: 20 * time.Second}
	for _, slug := range inferenceSlugs {
		_, s := builtinSpec(t, slug)
		key := "sk-invalid-test"
		if slug == "openrouter" {
			// A malformed key gets a generic 401; a well formed unknown one is looked up.
			key = "sk-or-v1-" + strings.Repeat("0", 64)
		}
		res, err := s.Check(context.Background(), c, "", key)
		switch {
		case err != nil:
			t.Errorf("%-10s health %s %s: %v", slug, s.Health.Method, s.Health.Path, err)
		case res.Valid:
			t.Errorf("%-10s health %s %s: %d said valid", slug, s.Health.Method, s.Health.Path, res.HTTPStatus)
		default:
			t.Logf("%-10s health %s %s: %d invalid, pass", slug, s.Health.Method, s.Health.Path, res.HTTPStatus)
		}
		for _, e := range s.Examples {
			if e.Method != "POST" {
				continue
			}
			body, _ := json.Marshal(e.Body)
			req, _ := http.NewRequest(e.Method, s.BaseURL+e.Path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if slug == "anthropic" {
				req.Header.Set("anthropic-version", "2023-06-01")
			}
			s.Auth.Apply(req, req.URL.Query(), key)
			resp, err := c.Do(req)
			if err != nil {
				t.Errorf("%-10s %s %s: %v", slug, e.Method, e.Path, err)
				break
			}
			b := make([]byte, 64<<10)
			n, _ := resp.Body.Read(b)
			resp.Body.Close()
			r := s.Classify(resp.StatusCode, resp.Header, b[:n])
			if r.Outcome != Invalid {
				t.Errorf("%-10s %s %s: %d %s, want invalid: %s", slug, e.Method, e.Path, resp.StatusCode, r.Outcome, b[:n])
			} else {
				t.Logf("%-10s %s %s: %d invalid, pass", slug, e.Method, e.Path, resp.StatusCode)
			}
			break
		}
	}
}
