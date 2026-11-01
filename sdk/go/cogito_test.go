package cogito

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeServer struct {
	t       *testing.T
	mu      sync.Mutex
	keys    []int64
	next    int
	reports []map[string]any
	leases  []map[string]any
}

func leaseJSON(id int64) map[string]any {
	return map[string]any{
		"lease_id": fmt.Sprintf("l%d", id), "key_id": id, "service": "serpapi",
		"secret": fmt.Sprintf("sk%d", id), "auth": map[string]any{"type": "query", "name": "api_key"},
		"base_url": "https://serpapi.com", "expires_at": "2026-11-01T12:00:00Z",
	}
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	check := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer cog_test" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthorized","message":"bad token"}}`)
			return false
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "cogito-go/") {
			f.t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/services", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		_, _ = io.WriteString(w, `{"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":true,"base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"}}]}`)
	})
	mux.HandleFunc("POST /api/v1/lease", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.leases = append(f.leases, body)
		if f.next >= len(f.keys) {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"code":"no_key_available","message":"all keys cooling"},"retry_after_sec":42}`)
			return
		}
		id := f.keys[f.next]
		f.next++
		_ = json.NewEncoder(w).Encode(leaseJSON(id))
	})
	mux.HandleFunc("POST /api/v1/leases/{id}/report", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["_lease_id"] = r.PathValue("id")
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reports = append(f.reports, body)
		var next any
		if body["rotate"] == true && f.next < len(f.keys) {
			next = leaseJSON(f.keys[f.next])
			f.next++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "next": next})
	})
	mux.HandleFunc("/proxy/", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Cogito-Key-Id", "7")
		w.Header().Set("X-Cogito-Attempts", "1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method": r.Method, "path": r.URL.EscapedPath(), "query": r.URL.RawQuery,
			"body": string(b), "ct": r.Header.Get("Content-Type"), "custom": r.Header.Get("X-Custom"),
		})
	})
	return mux
}

func setup(t *testing.T, keys ...int64) (*Client, *fakeServer) {
	t.Helper()
	f := &fakeServer{t: t, keys: keys}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "cog_test"), f
}

func TestServices(t *testing.T) {
	c, _ := setup(t)
	svcs, err := c.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 || svcs[0].Slug != "serpapi" || !svcs[0].Available || svcs[0].Auth.Name != "api_key" {
		t.Fatalf("unexpected %+v", svcs)
	}
}

func TestBadToken(t *testing.T) {
	c, _ := setup(t)
	c.token = "nope"
	_, err := c.Services(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 401 || ae.Code != "unauthorized" {
		t.Fatalf("got %v", err)
	}
}

func TestLeaseAndReport(t *testing.T) {
	c, f := setup(t, 1, 2)
	l, err := c.Lease(context.Background(), "serpapi", &LeaseOptions{ExcludeKeyIDs: []int64{9}, TTL: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if l.LeaseID != "l1" || l.KeyID != 1 || l.Secret != "sk1" || l.ExpiresAt.IsZero() {
		t.Fatalf("lease %+v", l)
	}
	if f.leases[0]["ttl_sec"].(float64) != 90 || len(f.leases[0]["exclude_key_ids"].([]any)) != 1 {
		t.Fatalf("lease body %+v", f.leases[0])
	}
	next, err := l.Report(context.Background(), Report{Outcome: OutcomeRateLimited, StatusCode: 429, RetryAfter: 1500 * time.Millisecond, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.KeyID != 2 {
		t.Fatalf("next %+v", next)
	}
	r := f.reports[0]
	if r["outcome"] != "rate_limited" || r["retry_after_sec"].(float64) != 2 || r["status_code"].(float64) != 429 || r["_lease_id"] != "l1" {
		t.Fatalf("report %+v", r)
	}
	// next lease is bound to the client and can report too
	n2, err := next.Report(context.Background(), Report{Outcome: OutcomeOK})
	if err != nil || n2 != nil {
		t.Fatalf("n2=%v err=%v", n2, err)
	}
	if _, ok := f.reports[1]["rotate"]; ok {
		t.Fatalf("rotate should be omitted: %+v", f.reports[1])
	}
}

func TestNoKeyAvailable(t *testing.T) {
	c, _ := setup(t)
	_, err := c.Lease(context.Background(), "serpapi", nil)
	var nk *ErrNoKeyAvailable
	if !errors.As(err, &nk) || nk.RetryAfter != 42*time.Second {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, NoKeyAvailable) {
		t.Fatal("errors.Is sentinel failed")
	}
}

func TestSentinels(t *testing.T) {
	base := errors.New("upstream 429")
	err := fmt.Errorf("search: %w", &ErrRateLimited{RetryAfter: time.Second, Err: base})
	if !errors.Is(err, RateLimited) || !errors.Is(err, base) || errors.Is(err, QuotaExhausted) {
		t.Fatal("rate limited sentinel")
	}
	if !errors.Is(&ErrQuotaExhausted{}, QuotaExhausted) || !errors.Is(&ErrInvalidKey{}, InvalidKey) {
		t.Fatal("sentinels")
	}
}

func TestApply(t *testing.T) {
	cases := []struct {
		auth         Auth
		header, want string
		query        string
	}{
		{Auth{"query", "api_key"}, "", "", "api_key=s"},
		{Auth{"header", "xi-api-key"}, "Xi-Api-Key", "s", ""},
		{Auth{"bearer", ""}, "Authorization", "Bearer s", ""},
		{Auth{"bearer", "X-Token"}, "X-Token", "Bearer s", ""},
	}
	for _, tc := range cases {
		l := &Lease{Secret: "s", Auth: tc.auth, BaseURL: "https://x.test/"}
		req, _ := http.NewRequest("GET", l.URL("/v1/a", nil), nil)
		l.Apply(req)
		if tc.header != "" && req.Header.Get(tc.header) != tc.want {
			t.Errorf("%v: header %q", tc.auth, req.Header.Get(tc.header))
		}
		if req.URL.RawQuery != tc.query {
			t.Errorf("%v: query %q", tc.auth, req.URL.RawQuery)
		}
		if req.URL.String() != "https://x.test/v1/a"+map[bool]string{true: "?" + tc.query, false: ""}[tc.query != ""] {
			t.Errorf("url %s", req.URL)
		}
	}
}

func TestWithKeySuccess(t *testing.T) {
	c, f := setup(t, 1)
	calls := 0
	err := c.WithKey(context.Background(), "serpapi", func(l *Lease) error { calls++; return nil })
	if err != nil || calls != 1 || len(f.reports) != 1 || f.reports[0]["outcome"] != "ok" {
		t.Fatalf("err=%v calls=%d reports=%+v", err, calls, f.reports)
	}
}

func TestWithKeyRotates(t *testing.T) {
	c, f := setup(t, 1, 2, 3)
	var seen []int64
	err := c.WithKey(context.Background(), "serpapi", func(l *Lease) error {
		seen = append(seen, l.KeyID)
		switch l.KeyID {
		case 1:
			return fmt.Errorf("wrapped: %w", &ErrRateLimited{RetryAfter: 30 * time.Second, StatusCode: 429})
		case 2:
			return &ErrQuotaExhausted{StatusCode: 429}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(seen) != "[1 2 3]" {
		t.Fatalf("seen %v", seen)
	}
	want := []string{"rate_limited", "quota_exhausted", "ok"}
	for i, w := range want {
		if f.reports[i]["outcome"] != w {
			t.Fatalf("report %d: %+v", i, f.reports[i])
		}
	}
	if f.reports[0]["retry_after_sec"].(float64) != 30 || f.reports[0]["rotate"] != true {
		t.Fatalf("report 0: %+v", f.reports[0])
	}
}

func TestWithKeyFallsBackToLease(t *testing.T) {
	// Report returns next=null, so WithKey leases again excluding tried keys.
	c, f := setup(t, 1)
	err := c.WithKey(context.Background(), "serpapi", func(l *Lease) error { return &ErrInvalidKey{} })
	var nk *ErrNoKeyAvailable
	if !errors.As(err, &nk) {
		t.Fatalf("got %v", err)
	}
	if len(f.leases) != 2 || fmt.Sprint(f.leases[1]["exclude_key_ids"]) != "[1]" {
		t.Fatalf("leases %+v", f.leases)
	}
	if f.reports[0]["outcome"] != "invalid" {
		t.Fatalf("reports %+v", f.reports)
	}
}

func TestWithKeyMaxAttempts(t *testing.T) {
	c, f := setup(t, 1, 2, 3, 4)
	calls := 0
	err := c.WithKey(context.Background(), "serpapi", func(l *Lease) error {
		calls++
		return &ErrRateLimited{}
	}, WithMaxAttempts(2))
	if !errors.Is(err, RateLimited) || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if len(f.reports) != 2 || f.reports[1]["rotate"] != nil {
		t.Fatalf("final report should not rotate: %+v", f.reports)
	}
}

func TestWithKeyOtherError(t *testing.T) {
	c, f := setup(t, 1, 2)
	boom := errors.New("boom")
	calls := 0
	err := c.WithKey(context.Background(), "serpapi", func(l *Lease) error { calls++; return boom })
	if err != boom || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if f.reports[0]["outcome"] != "error" || f.reports[0]["message"] != "boom" || f.reports[0]["rotate"] != nil {
		t.Fatalf("report %+v", f.reports[0])
	}
}

func decode(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProxyGet(t *testing.T) {
	c, _ := setup(t)
	resp, err := c.Proxy("serpapi").Get(context.Background(), "/search.json", map[string][]string{"q": {"coffee"}, "engine": {"google"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("X-Cogito-Key-Id") != "7" {
		t.Fatal("missing key id header")
	}
	m := decode(t, resp)
	if m["path"] != "/proxy/serpapi/search.json" || m["query"] != "engine=google&q=coffee" || m["method"] != "GET" {
		t.Fatalf("%+v", m)
	}
}

func TestProxyPostJSONAndDo(t *testing.T) {
	c, _ := setup(t)
	p := c.Proxy("elevenlabs")
	resp, err := p.PostJSON(context.Background(), "v1/text-to-speech/abc", map[string]string{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, resp)
	if m["path"] != "/proxy/elevenlabs/v1/text-to-speech/abc" || m["ct"] != "application/json" || m["body"] != `{"text":"hi"}` {
		t.Fatalf("%+v", m)
	}

	req, _ := http.NewRequest("PUT", "/v1/things?x=1", strings.NewReader("raw"))
	req.Header.Set("X-Custom", "yes")
	resp, err = p.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	m = decode(t, resp)
	if m["path"] != "/proxy/elevenlabs/v1/things" || m["query"] != "x=1" || m["body"] != "raw" || m["custom"] != "yes" || m["method"] != "PUT" {
		t.Fatalf("%+v", m)
	}

	// Full proxy path is not doubled.
	req, _ = http.NewRequest("GET", "http://ignored.example/proxy/elevenlabs/v1/voices", nil)
	m = decode(t, mustDo(t, p, req))
	if m["path"] != "/proxy/elevenlabs/v1/voices" {
		t.Fatalf("%+v", m)
	}

	resp, err = p.Request(context.Background(), "DELETE", "v1/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m = decode(t, resp); m["method"] != "DELETE" {
		t.Fatalf("%+v", m)
	}
}

func mustDo(t *testing.T, p *Proxy, req *http.Request) *http.Response {
	t.Helper()
	resp, err := p.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
