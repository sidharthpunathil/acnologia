package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// getWithBody sends a GET that carries a body, which some upstreams read.
func getWithBody(t *testing.T, e *env, path, tok, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestCacheVariesByForwardedRequest: an answer shaped by a header or a body
// one app sent is not served to an app that did not send them, and a
// partial answer is never stored.
func TestCacheVariesByForwardedRequest(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("search", map[string]any{"cache_ttl_sec": 60}, "key-a")
	up.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("Range") != "" {
			return 206, nil, `{"partial":true}`
		}
		return 200, nil, `{"variant":"` + r.Header.Get("X-Variant") + `","body":"` + string(b) + `"}`
	}
	_, atok := e.app(map[string]any{"name": "a"})
	_, btok := e.app(map[string]any{"name": "b"})

	if r, body := call(t, e, "GET", "/proxy/search/q?x=1", atok, "X-Variant", "evil"); r.Header.Get("X-Acnologia-Cache") != "miss" || !strings.Contains(body, "evil") {
		t.Fatalf("first: %v %s", r.Header, body)
	}
	r, body := call(t, e, "GET", "/proxy/search/q?x=1", btok)
	if r.Header.Get("X-Acnologia-Cache") == "hit" || strings.Contains(body, "evil") {
		t.Fatalf("header shaped answer served to another app: %v %s", r.Header, body)
	}
	// The same headers still share one entry.
	if r, _ := call(t, e, "GET", "/proxy/search/q?x=1", atok, "X-Variant", "evil"); r.Header.Get("X-Acnologia-Cache") != "hit" {
		t.Fatalf("same request missed: %v", r.Header)
	}

	if r, body := getWithBody(t, e, "/proxy/search/q?x=2", atok, "planted"); r.Header.Get("X-Acnologia-Cache") != "miss" || !strings.Contains(body, "planted") {
		t.Fatalf("get with body: %v %s", r.Header, body)
	}
	if r, body := call(t, e, "GET", "/proxy/search/q?x=2", btok); r.Header.Get("X-Acnologia-Cache") == "hit" || strings.Contains(body, "planted") {
		t.Fatalf("body shaped answer served to another app: %v %s", r.Header, body)
	}

	before := up.total.Load()
	call(t, e, "GET", "/proxy/search/q?x=3", atok, "Range", "bytes=0-1")
	if r, _ := call(t, e, "GET", "/proxy/search/q?x=3", atok, "Range", "bytes=0-1"); r.Header.Get("X-Acnologia-Cache") == "hit" || up.total.Load() != before+2 {
		t.Fatalf("206 was cached: %v total %d", r.Header, up.total.Load()-before)
	}
}

// TestCacheSeparatesShares: a share never reads what another app cached,
// and another app never reads what the share cached.
func TestCacheSeparatesShares(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("search", map[string]any{"cache_ttl_sec": 60}, "key-a")
	up.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"results":[]}` }
	_, code := e.newPersonShare(map[string]any{"name": "For Dana", "services": []string{"search"}})
	stok, _ := e.redeemProxy(code)
	_, atok := e.app(map[string]any{"name": "a"})

	call(t, e, "GET", "/proxy/search/q", atok)
	if r, _ := call(t, e, "GET", "/proxy/search/q", stok); r.Header.Get("X-Acnologia-Cache") != "miss" {
		t.Fatalf("share read another app's entry: %v", r.Header)
	}
	if r, _ := call(t, e, "GET", "/proxy/search/q", stok); r.Header.Get("X-Acnologia-Cache") != "hit" {
		t.Fatalf("share's own entry missed: %v", r.Header)
	}
	if up.total.Load() != 2 {
		t.Fatalf("upstream calls %d", up.total.Load())
	}
}

// TestDeletePurgesCachesOfReusedIDs: SQLite reuses the highest id, so a
// deleted service's cached answers and a deleted app's idempotent replays
// must not reach the service or app that gets the id next.
func TestDeletePurgesCachesOfReusedIDs(t *testing.T) {
	e, _ := newFeatureEnv(t)
	upA, a := e.upstream("svc", map[string]any{"cache_ttl_sec": 60}, "key-a")
	upA.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"from":"old"}` }
	_, xtok := e.app(map[string]any{"name": "x"})
	call(t, e, "GET", "/proxy/svc/q", xtok)

	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/services/%d", a.ID), nil, 200, nil)
	upB, b := e.upstream("svc", map[string]any{"cache_ttl_sec": 60}, "key-b")
	upB.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"from":"new"}` }
	if b.ID != a.ID {
		t.Skipf("service id not reused (%d then %d)", a.ID, b.ID)
	}
	_, ytok := e.app(map[string]any{"name": "y", "allowed_services": []string{"svc"}})
	if r, body := call(t, e, "GET", "/proxy/svc/q", ytok); r.Header.Get("X-Acnologia-Cache") == "hit" || !strings.Contains(body, "new") || upB.total.Load() != 1 {
		t.Fatalf("deleted service's answer served: %v %s", r.Header, body)
	}

	// The app with the highest id stores a replay, then goes.
	wID, wtok := e.app(map[string]any{"name": "w"})
	upB.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"from":"w"}` }
	if r, _ := call(t, e, "POST", "/proxy/svc/q", wtok, "Idempotency-Key", "k"); r.StatusCode != 200 {
		t.Fatalf("idempotent post: %d", r.StatusCode)
	}
	upB.answer = func(string, *http.Request) (int, map[string]string, string) { return 200, nil, `{"from":"new"}` }
	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/apps/%d", wID), nil, 200, nil)
	var out struct {
		App   store.App `json:"app"`
		Token string    `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/apps", map[string]any{"name": "z"}, 201, &out)
	if out.App.ID != wID {
		t.Skipf("app id not reused (%d then %d)", wID, out.App.ID)
	}
	if r, body := call(t, e, "POST", "/proxy/svc/q", out.Token, "Idempotency-Key", "k"); r.Header.Get("Idempotent-Replayed") != "" || !strings.Contains(body, "new") {
		t.Fatalf("deleted app's replay served: %v %s", r.Header, body)
	}
}
