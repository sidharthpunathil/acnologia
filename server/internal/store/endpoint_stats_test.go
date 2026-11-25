package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func addProxyEvent(t *testing.T, s *Store, ts time.Time, kind string, sid int64, status int, lat int64, detail string) {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO events(ts,kind,service_id,status_code,latency_ms,detail) VALUES(?,?,?,?,?,?)`,
		ts.Unix(), kind, sid, status, lat, detail); err != nil {
		t.Fatal(err)
	}
}

// TestFoldEndpointStats: proxy answers fold per template; a caller's 4xx
// counts for neither side, 405 and 429 and 5xx count against, cache hits
// and Acnologia's own refusals are not counted, and folding again adds
// only new events.
func TestFoldEndpointStats(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sv, err := s.CreateService(ctx, &Service{Slug: "svc", Name: "Svc", Provider: "serpapi", BaseURL: "https://x.io", AuthType: "query", AuthName: "k", Strategy: "round_robin"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 8; i++ {
		addProxyEvent(t, s, now, "proxy", sv.ID, 200, 100+int64(i)*10, "GET /v1/users/"+string(rune('0'+i)))
	}
	addProxyEvent(t, s, now, "proxy", sv.ID, 404, 50, "GET /v1/users/9")                              // caller's fault
	addProxyEvent(t, s, now, "proxy", sv.ID, 429, 5, "GET /v1/users/9 (queued 200ms for a free key)") // against
	addProxyEvent(t, s, now, "proxy", sv.ID, 503, 900, "GET /v1/users/9")                             // against
	addProxyEvent(t, s, now, "proxy", sv.ID, 405, 9, "GET /v1/users/9")                               // against
	addProxyEvent(t, s, now, "cache_hit", sv.ID, 200, 0, "GET /v1/users/1")                           // not the provider
	addProxyEvent(t, s, now, "error", sv.ID, 503, 0, "proxy: no key available")                       // Acnologia's refusal
	addProxyEvent(t, s, now.Add(-30*24*time.Hour), "proxy", sv.ID, 200, 1, "GET /old")                // before the window: the first fold starts at it

	tmpl := func(id int64, method, path string) string {
		if strings.HasPrefix(path, "/v1/users/") {
			return "/v1/users/{id}"
		}
		return path
	}
	if _, err := s.FoldEndpointStats(ctx, tmpl, 3, 0); err != nil {
		t.Fatal(err)
	}
	ev := s.Evidence(ctx)
	st := ev.Endpoint(sv.ID, "GET", "/v1/users/{id}")
	if st == nil {
		t.Fatal("no stats for the template")
	}
	if st.Calls != 12 || st.OK != 8 || st.Fail != 3 {
		t.Fatalf("calls %d ok %d fail %d, want 12 8 3", st.Calls, st.OK, st.Fail)
	}
	if st.Enough() {
		t.Fatal("11 counted calls should be under the minimum sample")
	}
	if r := st.SuccessRate(); r < 0.72 || r > 0.73 {
		t.Fatalf("success rate %v, want 8/11", r)
	}
	if st.P50MS < 100 || st.P50MS > 200 || st.P95MS < st.P50MS {
		t.Fatalf("p50 %d p95 %d", st.P50MS, st.P95MS)
	}
	if st.LastOK == nil || st.LastFail == nil {
		t.Fatal("last ok and last failure should be set")
	}
	if ev.Endpoint(sv.ID, "GET", "/old") != nil {
		t.Fatal("an event older than the window was folded")
	}

	// Fold again: nothing new, nothing doubles.
	if n, err := s.FoldEndpointStats(ctx, tmpl, 3, 0); err != nil || n != 0 {
		t.Fatalf("second fold read %d events, err %v", n, err)
	}
	for i := 0; i < 10; i++ {
		addProxyEvent(t, s, now, "proxy", sv.ID, 201, 300, "POST /v1/users/1")
		addProxyEvent(t, s, now, "proxy", sv.ID, 200, 120, "GET /v1/users/1")
	}
	if _, err := s.FoldEndpointStats(ctx, tmpl, 1000, 1); err != nil {
		t.Fatal(err)
	}
	ev = s.Evidence(ctx)
	st = ev.Endpoint(sv.ID, "GET", "/v1/users/{id}")
	if st.OK != 18 || !st.Enough() {
		t.Fatalf("after the second batch ok %d enough %v", st.OK, st.Enough())
	}
	if post := ev.Endpoint(sv.ID, "POST", "/v1/users/{id}"); post == nil || post.OK != 10 {
		t.Fatalf("POST stats %+v", post)
	}
	if all := ev.Service(sv.ID); all == nil || all.OK != 28 || all.Fail != 3 {
		t.Fatalf("service stats %+v", all)
	}

	// Deleting the service drops its rows on the next fold.
	if err := s.DeleteService(ctx, sv.ID); err != nil {
		t.Fatal(err)
	}
	addProxyEvent(t, s, now, "proxy", 999, 200, 1, "GET /x")
	if _, err := s.FoldEndpointStats(ctx, func(int64, string, string) string { return "" }, 10, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM endpoint_stats`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left after the service went", n)
	}
}

// TestFoldCountsTransportFailures: a call the provider never answered (a
// connection failure) counts against the provider on its endpoint; a call
// that failed on this side (the caller left, a blocked address), an old
// error event without the call in it, and other error events do not.
func TestFoldCountsTransportFailures(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sv, err := s.CreateService(ctx, &Service{Slug: "svc", Name: "Svc", Provider: "serpapi", BaseURL: "https://x.io", AuthType: "query", AuthName: "k", Strategy: "round_robin"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	addProxyEvent(t, s, now, "proxy", sv.ID, 200, 100, "GET /v1/search")
	addProxyEvent(t, s, now, "error", sv.ID, 0, 30, "upstream: GET /v1/search: dial tcp 10.0.0.1:443: connect: connection refused")
	addProxyEvent(t, s, now, "error", sv.ID, 0, 9000, "upstream: GET /v1/search: context deadline exceeded (Client.Timeout exceeded)")
	addProxyEvent(t, s, now, "error", sv.ID, 0, 5, "upstream: GET /v1/search: context canceled")
	addProxyEvent(t, s, now, "error", sv.ID, 0, 5, "upstream: GET /v1/search: dial: refusing to connect to a link-local or metadata address")
	addProxyEvent(t, s, now, "error", sv.ID, 0, 5, "upstream: dial tcp 10.0.0.1:443: connect: connection refused") // before the path was logged
	addProxyEvent(t, s, now, "error", sv.ID, 0, 0, "egress: proxy down")
	addProxyEvent(t, s, now, "error", sv.ID, 200, 5, "MCP search: response over the app's max result size")
	addProxyEvent(t, s, now, "error", sv.ID, 0, 40, "MCP search (queued 200ms): could not connect: EOF")
	if _, err := s.FoldEndpointStats(ctx, func(_ int64, _, p string) string { return p }, 100, 0); err != nil {
		t.Fatal(err)
	}
	ev := s.Evidence(ctx)
	st := ev.Endpoint(sv.ID, "GET", "/v1/search")
	if st == nil || st.Calls != 3 || st.OK != 1 || st.Fail != 2 || st.LastFail == nil {
		t.Fatalf("GET /v1/search stats %+v, want 3 calls, 1 ok, 2 failed", st)
	}
	if m := ev.Endpoint(sv.ID, "MCP", "search"); m == nil || m.Calls != 1 || m.Fail != 1 {
		t.Fatalf("MCP stats %+v, want one failed call", m)
	}
	for _, d := range []string{"upstream: GET /v1/x", "upstream: DIAL tcp: x", "proxy: no key", "MCP : x"} {
		if _, _, ok := ParseTransportFailure(d); ok {
			t.Errorf("%q parsed as a transport failure", d)
		}
	}
}

func TestProxyVerdict(t *testing.T) {
	cases := map[int][2]bool{200: {true, false}, 302: {true, false}, 400: {false, false}, 401: {false, false}, 404: {false, false},
		405: {false, true}, 422: {false, false}, 429: {false, true}, 500: {false, true}, 503: {false, true}, 0: {false, false}}
	for code, want := range cases {
		ok, fail := ProxyVerdict(code)
		if ok != want[0] || fail != want[1] {
			t.Errorf("%d: ok %v fail %v, want %v", code, ok, fail, want)
		}
	}
	if m, p, ok := ParseProxyDetail("MCP search_web (queued 1s)"); !ok || m != "MCP" || p != "search_web" {
		t.Fatalf("MCP detail parsed as %q %q %v", m, p, ok)
	}
}

func TestHistPercentile(t *testing.T) {
	h := make(hist, len(latencyBounds)+1)
	for i := 0; i < 90; i++ {
		h[latencyBucket(100)]++
	}
	for i := 0; i < 10; i++ {
		h[latencyBucket(5000)]++
	}
	if p := h.percentile(0.5); p < 75 || p > 100 {
		t.Fatalf("p50 %d", p)
	}
	if p := h.percentile(0.95); p < 4000 || p > 6000 {
		t.Fatalf("p95 %d", p)
	}
	if parseHist(h.String()).percentile(0.5) != h.percentile(0.5) {
		t.Fatal("histogram does not round trip")
	}
}

// TestExampleResponseScrubbed: a stored sample never holds the key in any
// spelling, is cut to SampleMax, and keeps the JSON shape.
func TestExampleResponseScrubbed(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	key := "sk-live/abcdefghijklmnop1234"
	long := strings.Repeat("x", 300)
	items := `{"id":1,"note":"` + long + `"},{"id":2},{"id":3},{"id":4}`
	body := `{"echo":"` + strings.ReplaceAll(key, "/", `\/`) + `","url":"https://x.io/?k=sk-live%2Fabcdefghijklmnop1234","raw":"` + key + `","items":[` + items + `]}`
	if err := s.SaveExampleResponse(ctx, ExampleResponse{Provider: "p", Example: "List", Path: "/v1/list", Status: 200, ContentType: "application/json"}, []byte(body), key); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExampleResponses(ctx, "p")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err %v", got, err)
	}
	b := got[0].Body
	for _, form := range []string{key, "abcdefghijklmnop1234", "sk-live%2F"} {
		if strings.Contains(b, form) {
			t.Fatalf("sample keeps %q:\n%s", form, b)
		}
	}
	if !strings.Contains(b, `"items"`) || !strings.Contains(b, "2 more") || strings.Contains(b, long) {
		t.Fatalf("sample shape is off:\n%s", b)
	}
	if got[0].VerifiedAt.IsZero() || got[0].Method != "GET" {
		t.Fatalf("verified_at %v method %q", got[0].VerifiedAt, got[0].Method)
	}
	huge := `{"a":[` + strings.Repeat(`{"k":"`+strings.Repeat("y", 100)+`"},`, 1) + `{}],` + `"b":"` + strings.Repeat("z", 100) + `"`
	for i := 0; i < 200; i++ {
		huge += `,"f` + strings.Repeat("q", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":"` + strings.Repeat("w", 100) + `"`
	}
	huge += "}"
	if err := s.SaveExampleResponse(ctx, ExampleResponse{Provider: "p", Example: "Big"}, []byte(huge)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ExampleResponses(ctx, "p")
	for _, r := range got {
		if len(r.Body) > SampleMax {
			t.Fatalf("%s sample is %d bytes", r.Example, len(r.Body))
		}
	}
}
