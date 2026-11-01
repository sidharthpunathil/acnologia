package redact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// Fake secrets made up for tests; none of them is a real key.
const (
	poolKey  = "serp_9f8e7d6c5b4a3921"
	shortKey = "q7Xk29pL"
	antKey   = "sk-ant-api03-Zx9Yw8Vu7Ts6Rq5Po4Nm3Lk2Ji1Hg0Fe"
	gKey     = "AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUv"
)

func withKnown(t *testing.T, secrets ...string) *Redactor {
	t.Helper()
	r := New(func(context.Context) ([]string, error) { return secrets, nil })
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRedactorTable(t *testing.T) {
	r := withKnown(t, poolKey, shortKey, "Zm9v/YmFy+cXV4=12")
	ctx := context.Background()
	cases := []struct {
		name, in string
		gone     []string // must not survive
		want     string   // exact output, when set
	}{
		{name: "known secret in a body", in: `{"error":"invalid api key ` + poolKey + `"}`, gone: []string{poolKey},
			want: `{"error":"invalid api key [key ...3921]"}`},
		{name: "known secret url escaped", in: "GET /x?k=Zm9v%2FYmFy%2BcXV4%3D12", want: "GET /x?k=[key ...4=12]"},
		{name: "short pool key", in: "upstream said: bad key " + shortKey + ".", want: "upstream said: bad key [key ...29pL]."},
		{name: "query key", in: "https://generativelanguage.googleapis.com/v1/models?key=Zq81mXk20pLs9TaB&alt=json", gone: []string{"Zq81mXk20pLs9TaB"}},
		{name: "query api_key", in: "Get \"https://serpapi.com/search?q=x&api_key=0123abcd4567efgh\": timeout", gone: []string{"0123abcd4567efgh"}},
		{name: "json field", in: `{"name":"prod","api_key":"Pq7Rs8Tu9Vw0","model":"x"}`, gone: []string{"Pq7Rs8Tu9Vw0"}},
		{name: "json key field", in: `{"key": "a8f3k29dk3l"}`, gone: []string{"a8f3k29dk3l"}},
		{name: "bearer header", in: "Authorization: Bearer abcDEF123456ghi", gone: []string{"abcDEF123456ghi"}},
		{name: "x-api-key header", in: "x-api-key: 7e2b9c41aa0f", gone: []string{"7e2b9c41aa0f"}},
		{name: "anthropic key", in: "my key is " + antKey + " ok", gone: []string{antKey, "Zx9Yw8Vu7Ts6"}},
		{name: "anthropic key glued", in: "key:" + antKey, gone: []string{"Zx9Yw8Vu7Ts6"}},
		{name: "google key glued", in: "thekeyis" + gKey + "thanks", gone: []string{"A1b2C3d4E5f6"}},
		{name: "openai key", in: "OPENAI=sk-proj-4f9KxL2mQ8vR7tY1", gone: []string{"4f9KxL2mQ8vR7tY1"}},
		{name: "github token", in: "token ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8", gone: []string{"A1b2C3d4E5f6G7h8"}},
		{name: "env line", in: "export PASSWORD=hunter2hunter2", gone: []string{"hunter2hunter2"}},
		{name: "high entropy token", in: "use 8Hq2LmZ7vX4kR9tW1nB6cY3dF0gJ5sPa now", gone: []string{"8Hq2LmZ7vX4kR9tW1nB6cY3dF0gJ5sPa"}},
		{name: "jwt", in: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", gone: []string{"dozjgNryP4J3"}},

		// No false positives.
		{name: "normal sentence", in: "Please add these keys to the SerpAPI service and check that they work.", want: "Please add these keys to the SerpAPI service and check that they work."},
		{name: "uuid", in: "run 3f2b8c1e-9d4a-4e7b-8a6c-1f0e2d3c4b5a done", want: "run 3f2b8c1e-9d4a-4e7b-8a6c-1f0e2d3c4b5a done"},
		{name: "sha256", in: "sha256 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", want: "sha256 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		{name: "identifier", in: "BetaWebFetchTool20260209Param and anthropic-sdk-go/v2", want: "BetaWebFetchTool20260209Param and anthropic-sdk-go/v2"},
		{name: "path", in: "/Users/someone/Documents/Projects2/server/internal", want: "/Users/someone/Documents/Projects2/server/internal"},
		{name: "timestamp and slug", in: "2026-11-02T10:00:00Z serpapi-backup-2024 max_tokens: 4096", want: "2026-11-02T10:00:00Z serpapi-backup-2024 max_tokens: 4096"},
		{name: "placeholders", in: `{"secret":"[key k3 ...c21e]","hint":"...c21e"}`, want: `{"secret":"[key k3 ...c21e]","hint":"...c21e"}`},
		{name: "env reference", in: "apiKey: process.env.OPENAI_API_KEY", want: "apiKey: process.env.OPENAI_API_KEY"},
		{name: "sort key", in: `{"key":"created_at","token":"next"}`, want: `{"key":"created_at","token":"next"}`},
		{name: "basic prose", in: "Basic authentication is supported", want: "Basic authentication is supported"},
		{name: "task word", in: "the risk-assessment-framework-version is fine", want: "the risk-assessment-framework-version is fine"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.String(ctx, c.in)
			for _, g := range c.gone {
				if strings.Contains(got, g) {
					t.Fatalf("%q survived: %s", g, got)
				}
			}
			if c.want != "" && got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestRedactorExtrasAndReload(t *testing.T) {
	secrets := []string{"first_secret_value_1"}
	r := New(func(context.Context) ([]string, error) { return secrets, nil })
	ctx := context.Background()
	if got := r.Known(ctx, "a first_secret_value_1 b"); got != "a [key ...ue_1] b" {
		t.Fatalf("lazy load: %q", got)
	}
	secrets = append(secrets, "added_later_value_2")
	if got := r.Known(ctx, "added_later_value_2"); got != "added_later_value_2" {
		t.Fatalf("reloaded without Invalidate: %q", got)
	}
	r.Invalidate()
	if got := r.Known(ctx, "added_later_value_2"); got != "[key ...ue_2]" {
		t.Fatalf("after Invalidate: %q", got)
	}
	ctx = WithSecrets(ctx, "run-test-key-xyz", "tiny")
	if got := r.Known(ctx, "run-test-key-xyz tiny"); got != "[key ...-xyz] tiny" {
		t.Fatalf("extras: %q", got)
	}
	var nilR *Redactor
	if got := nilR.String(ctx, "run-test-key-xyz"); got != "[key ...-xyz]" {
		t.Fatalf("nil redactor: %q", got)
	}
}

func TestRedactorManyKeys(t *testing.T) {
	keys := make([]string, 5000)
	for i := range keys {
		keys[i] = fmt.Sprintf("pool_%06d_secret", i)
	}
	r := withKnown(t, keys...)
	if r.Size() != 5000 {
		t.Fatalf("size %d", r.Size())
	}
	in := "a pool_004999_secret b pool_000001_secret"
	if got := r.String(context.Background(), in); strings.Contains(got, "pool_0") {
		t.Fatalf("got %q", got)
	}
}

func TestMessagesRequest(t *testing.T) {
	r := withKnown(t, poolKey)
	body := `{"model":"claude-opus-5-5","system":[{"type":"text","text":"sys ` + poolKey + `"}],"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"key ` + antKey + `"}]},` +
		`{"role":"assistant","content":[{"type":"thinking","thinking":"8Hq2LmZ7vX4kR9tW1nB6cY3dF0gJ5sPa","signature":"Sig8Hq2LmZ7vX4kR9tW1nB6cY3dF0gJ5sPa"},` +
		`{"type":"tool_use","id":"toolu_01A09q90qw90lq917835lq9X","name":"list_keys","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01A09q90qw90lq917835lq9X","content":"upstream: 401 bad key ` + poolKey + ` api_key=Pq7Rs8Tu9Vw0"}]}],` +
		`"tools":[{"name":"x","input_schema":{"type":"object"}}]}`
	out := string(r.MessagesRequest(context.Background(), []byte(body)))
	for _, g := range []string{poolKey, antKey, "Pq7Rs8Tu9Vw0"} {
		if strings.Contains(out, g) {
			t.Fatalf("%q survived: %s", g, out)
		}
	}
	// Ids, thinking and signatures are untouched.
	for _, keep := range []string{"toolu_01A09q90qw90lq917835lq9X", "Sig8Hq2LmZ7vX4kR9tW1nB6cY3dF0gJ5sPa", `"thinking":"8Hq2LmZ7vX4kR9tW1nB6cY3dF0gJ5sPa"`} {
		if !strings.Contains(out, keep) {
			t.Fatalf("%q was changed: %s", keep, out)
		}
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("invalid JSON: %s", out)
	}
	clean := `{"messages":[{"role":"user","content":"hello"}]}`
	if got := string(r.MessagesRequest(context.Background(), []byte(clean))); got != clean {
		t.Fatalf("clean body rewritten: %s", got)
	}
}

func TestLogHandler(t *testing.T) {
	var b strings.Builder
	h := NewHandler(slog.NewTextHandler(&b, nil))
	h.Use(withKnown(t, poolKey))
	log := slog.New(h)
	log.Warn("failed for "+poolKey, "url", "https://x.io/v1?key=Zq81mXk20pLs9TaB", "err", fmt.Errorf("bad %s", poolKey))
	log.With("k", poolKey).Info("x")
	if out := b.String(); strings.Contains(out, poolKey) || strings.Contains(out, "Zq81mXk20pLs9TaB") {
		t.Fatalf("log leaked: %s", out)
	}
}
