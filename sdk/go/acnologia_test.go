package acnologia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
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
	busy    bool
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
		if r.Header.Get("Authorization") != "Bearer acn_test" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthorized","message":"bad token"}}`)
			return false
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "acnologia-go/") {
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
		if f.busy {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}`)
			return
		}
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
		w.Header().Set("X-Acnologia-Key-Id", "7")
		w.Header().Set("X-Acnologia-Attempts", "1")
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
	return New(srv.URL+"/", "acn_test"), f
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

func TestNoKeyAvailableIsNotBusy(t *testing.T) {
	c, _ := setup(t)
	_, err := c.Lease(context.Background(), "serpapi", nil)
	var nk *ErrNoKeyAvailable
	if !errors.As(err, &nk) || nk.Busy || nk.Code != "no_key_available" || errors.Is(err, AllKeysBusy) {
		t.Fatalf("got %+v", err)
	}
}

func TestLeaseSendsWaitSec(t *testing.T) {
	c, f := setup(t, 1, 2)
	if _, err := c.Lease(context.Background(), "elevenlabs", &LeaseOptions{Wait: 1500 * time.Millisecond, TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if f.leases[0]["wait_sec"] != float64(2) || f.leases[0]["ttl_sec"] != float64(60) {
		t.Fatalf("lease body %+v", f.leases[0])
	}
	if _, err := c.Lease(context.Background(), "elevenlabs", &LeaseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.leases[1]["wait_sec"]; ok {
		t.Fatalf("wait_sec should be omitted: %+v", f.leases[1])
	}
	if _, err := c.Lease(context.Background(), "elevenlabs", &LeaseOptions{Wait: 61 * time.Second}); err == nil {
		t.Fatal("expected error for wait above MaxWait")
	}
	if len(f.leases) != 2 {
		t.Fatal("invalid wait should not reach the server")
	}
}

func TestAllKeysBusy(t *testing.T) {
	c, f := setup(t, 1)
	f.busy = true
	_, err := c.Lease(context.Background(), "elevenlabs", &LeaseOptions{Wait: 5 * time.Second})
	var nk *ErrNoKeyAvailable
	if !errors.As(err, &nk) {
		t.Fatalf("got %v", err)
	}
	if !nk.Busy || nk.Code != "all_keys_busy" || nk.RetryAfter != time.Second || nk.Message != "every key is at its limit" {
		t.Fatalf("got %+v", nk)
	}
	if !errors.Is(err, AllKeysBusy) || !errors.Is(err, NoKeyAvailable) {
		t.Fatal("busy error should match AllKeysBusy and NoKeyAvailable")
	}
	if !strings.Contains(err.Error(), "all keys busy") {
		t.Fatalf("message %q", err.Error())
	}
}

func TestWithKeyPassesWait(t *testing.T) {
	c, f := setup(t, 1)
	err := c.WithKey(context.Background(), "elevenlabs", func(l *Lease) error { return &ErrRateLimited{} }, WithWait(15*time.Second))
	if !errors.Is(err, NoKeyAvailable) {
		t.Fatalf("got %v", err)
	}
	if len(f.leases) != 2 || f.leases[0]["wait_sec"] != float64(15) || f.leases[1]["wait_sec"] != float64(15) {
		t.Fatalf("leases %+v", f.leases)
	}
}

func TestWithKeyReportsOnPanic(t *testing.T) {
	c, f := setup(t, 1)
	defer func() {
		if r := recover(); r != "kaboom" {
			t.Fatalf("recovered %v", r)
		}
		if len(f.reports) != 1 || f.reports[0]["outcome"] != "error" {
			t.Fatalf("reports %+v", f.reports)
		}
	}()
	_ = c.WithKey(context.Background(), "serpapi", func(l *Lease) error { panic("kaboom") })
}

func TestWithKeyReportsOnGoexit(t *testing.T) {
	c, f := setup(t, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.WithKey(context.Background(), "serpapi", func(l *Lease) error { runtime.Goexit(); return nil })
	}()
	<-done
	if len(f.reports) != 1 || f.reports[0]["outcome"] != "error" {
		t.Fatalf("reports %+v", f.reports)
	}
}

func TestWithKeyReportsAfterCancel(t *testing.T) {
	c, f := setup(t, 1, 2)
	ctx, cancel := context.WithCancel(context.Background())
	err := c.WithKey(ctx, "serpapi", func(l *Lease) error {
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reports) != 1 || f.reports[0]["outcome"] != "ok" {
		t.Fatalf("reports %+v", f.reports)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	err = c.WithKey(ctx2, "serpapi", func(l *Lease) error {
		cancel2()
		return &ErrRateLimited{}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(f.reports) != 2 || f.reports[1]["outcome"] != "rate_limited" || f.reports[1]["rotate"] != nil {
		t.Fatalf("reports %+v", f.reports)
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
		{Auth{Type: "query", Name: "api_key"}, "", "", "api_key=s"},
		{Auth{Type: "header", Name: "xi-api-key"}, "Xi-Api-Key", "s", ""},
		{Auth{Type: "bearer"}, "Authorization", "Bearer s", ""},
		{Auth{Type: "bearer", Name: "X-Token"}, "X-Token", "Bearer s", ""},
		{Auth{Type: "query", Name: "key", Prefix: "pk_"}, "", "", "key=pk_s"},
		{Auth{Type: "header", Name: "Authorization", Prefix: "Token "}, "Authorization", "Token s", ""},
		{Auth{Type: "header", Prefix: "Token "}, "Authorization", "Token s", ""},
		{Auth{Type: "bearer", Prefix: "Key "}, "Authorization", "Key s", ""},
		{Auth{Type: "bearer", Name: "X-Token", Prefix: "Token "}, "X-Token", "Token s", ""},
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

func TestLeaseParsesAuthPrefix(t *testing.T) {
	var w leaseWire
	if err := json.Unmarshal([]byte(`{"lease_id":"l","secret":"s","auth":{"type":"header","name":"Authorization","prefix":"Token "}}`), &w); err != nil {
		t.Fatal(err)
	}
	l := New("http://x.test", "t").leaseFromWire(&w)
	if l.Auth.Prefix != "Token " {
		t.Fatalf("prefix %q", l.Auth.Prefix)
	}
	req, _ := http.NewRequest("GET", "https://x.test/", nil)
	l.Apply(req)
	if got := req.Header.Get("Authorization"); got != "Token s" {
		t.Fatalf("header %q", got)
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
	if resp.Header.Get("X-Acnologia-Key-Id") != "7" {
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

func TestScrubMessage(t *testing.T) {
	secret := "a+b/c=d"
	msg := `Get "https://x.test/s?api_key=` + url.QueryEscape(secret) + `": timeout; raw ` + secret
	got := scrubMessage(msg, secret)
	if strings.Contains(got, secret) || strings.Contains(got, url.QueryEscape(secret)) {
		t.Fatalf("secret leaked: %q", got)
	}
	if got != `Get "https://x.test/s?api_key=[key]": timeout; raw [key]` {
		t.Fatalf("got %q", got)
	}
	if long := scrubMessage(strings.Repeat("x", 600), secret); len(long) != maxReportMessage {
		t.Fatalf("len %d", len(long))
	}
}

func TestWithKeyReportScrubsSecret(t *testing.T) {
	c, f := setup(t, 1, 2)
	err := c.WithKey(context.Background(), "serpapi", func(l *Lease) error {
		return fmt.Errorf(`Get "https://serpapi.com/search.json?api_key=%s": dial tcp: refused`, l.Secret)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	_ = c.WithKey(context.Background(), "serpapi", func(l *Lease) error {
		return &ErrRateLimited{Message: "429 for key " + l.Secret}
	}, WithMaxAttempts(1))
	if len(f.reports) != 2 {
		t.Fatalf("reports %+v", f.reports)
	}
	for i, want := range []string{
		`Get "https://serpapi.com/search.json?api_key=[key]": dial tcp: refused`,
		"429 for key [key]",
	} {
		if f.reports[i]["message"] != want {
			t.Fatalf("report %d: %+v", i, f.reports[i])
		}
	}
}

func TestProxyDoesNotFollowRedirects(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	hc := &http.Client{}
	for _, c := range []*Client{New(srv.URL, "acn_test"), New(srv.URL, "acn_test", WithHTTPClient(hc))} {
		resp, err := c.Proxy("serpapi").Get(context.Background(), "/search.json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || leaked {
			t.Fatalf("status %d leaked %v", resp.StatusCode, leaked)
		}
	}
	if hc.CheckRedirect != nil {
		t.Fatal("caller's http.Client was mutated")
	}
}

func TestDefaultTimeout(t *testing.T) {
	if c := New("http://x", "acn_test"); c.httpClient == http.DefaultClient || c.httpClient.Timeout != DefaultTimeout {
		t.Fatalf("default client %+v", c.httpClient)
	}
}

func TestStringRedactsSecrets(t *testing.T) {
	c := New("http://x", "acn_supersecret")
	l := &Lease{LeaseID: "l1", Secret: "sk_supersecret", client: c}
	for _, s := range []string{
		fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%v", *c),
		fmt.Sprint(l), fmt.Sprintf("%+v", l), fmt.Sprintf("%#v", l), fmt.Sprintf("%v", *l), fmt.Sprintf("%#v", *l),
	} {
		if strings.Contains(s, "supersecret") {
			t.Fatalf("leaked: %s", s)
		}
	}
}

func TestRenew(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		paths = append(paths, r.URL.EscapedPath())
		bodies = append(bodies, body)
		if strings.Contains(r.URL.Path, "gone") {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":"lease_not_found","message":"lease not found"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"expires_at":"2026-11-01T13:00:00Z"}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "acn_test")
	l := &Lease{LeaseID: "a/b c", client: c}
	exp, err := l.Renew(context.Background(), 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC)
	if !exp.Equal(want) || !l.ExpiresAt.Equal(want) {
		t.Fatalf("expires %v, lease %v", exp, l.ExpiresAt)
	}
	if paths[0] != "/api/v1/leases/a%2Fb%20c/renew" {
		t.Fatalf("path %q", paths[0])
	}
	if bodies[0]["ttl_sec"] != float64(2) {
		t.Fatalf("body %v", bodies[0])
	}
	if _, err := c.Renew(context.Background(), l, 0); err != nil {
		t.Fatal(err)
	}
	if len(bodies[1]) != 0 {
		t.Fatalf("zero ttl should send {}, got %v", bodies[1])
	}
	gone := &Lease{LeaseID: "gone", client: c}
	_, err = gone.Renew(context.Background(), 0)
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 404 || ae.Code != "lease_not_found" {
		t.Fatalf("want 404 lease_not_found, got %v", err)
	}
}

func TestLeaseExclusive(t *testing.T) {
	var leases []map[string]any
	var reports []map[string]any
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/report") {
			reports = append(reports, body)
			_, _ = io.WriteString(w, `{"ok":true,"next":null}`)
			return
		}
		leases = append(leases, body)
		n++
		l := leaseJSON(int64(n))
		l["exclusive"] = body["exclusive"] == true
		_ = json.NewEncoder(w).Encode(l)
	}))
	defer srv.Close()
	c := New(srv.URL, "acn_test")
	plain, err := c.Lease(context.Background(), "serpapi", &LeaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := leases[0]["exclusive"]; ok || plain.Exclusive {
		t.Fatalf("exclusive should be omitted by default: %v", leases[0])
	}
	calls := 0
	err = c.WithKey(context.Background(), "serpapi", func(l *Lease) error {
		calls++
		if !l.Exclusive {
			t.Errorf("lease %d not exclusive", calls)
		}
		if calls == 1 {
			return &ErrRateLimited{StatusCode: 429}
		}
		return nil
	}, WithLeaseOptions(LeaseOptions{Exclusive: true}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(leases) != 3 || leases[1]["exclusive"] != true || leases[2]["exclusive"] != true {
		t.Fatalf("calls %d, leases %v", calls, leases)
	}
}

func TestServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("h") != "" {
			w.Header().Set("Retry-After", "9")
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"code":"service_unavailable","message":"circuit open"}}`)
			return
		}
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"code":"service_unavailable","message":"circuit open"},"retry_after_sec":30}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "acn_test")
	_, err := c.Lease(context.Background(), "serpapi", nil)
	var su *ErrServiceUnavailable
	if !errors.As(err, &su) || su.RetryAfter != 30*time.Second || su.StatusCode != 503 || su.Code != "service_unavailable" {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, ServiceUnavailable) || errors.Is(err, NoKeyAvailable) {
		t.Fatal("should match ServiceUnavailable and not NoKeyAvailable")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "service_unavailable" {
		t.Fatal("should match *APIError")
	}
	err = c.do(context.Background(), http.MethodGet, "/x?h=1", nil, nil)
	if !errors.As(err, &su) || su.RetryAfter != 9*time.Second {
		t.Fatalf("header fallback: %v", err)
	}
}
