package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// pathLog records the paths an upstream was asked for.
type pathLog struct {
	mu    sync.Mutex
	paths []string
}

func (p *pathLog) add(s string) {
	p.mu.Lock()
	p.paths = append(p.paths, s)
	p.mu.Unlock()
}

func (p *pathLog) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

// selfHosted makes a provider whose catalog entries are paths on the
// service's base URL, as a self-hosted scraper server's are.
func selfHosted(e *env, slug string, hosts map[string]string) {
	e.t.Helper()
	hs := []map[string]any{}
	for name, path := range hosts {
		hs = append(hs, map[string]any{"name": name, "url": path, "title": name})
	}
	spec := map[string]any{"base_url": "https://" + slug + ".example.com", "auth": map[string]any{"type": "header", "name": "X-Key"},
		"use_standard_rules": true, "hosts": hs,
		"tests": []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}}}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": slug, "name": slug, "spec": spec}, 201, nil)
}

func rotationEnv(t *testing.T) (*env, *scripted, *scripted, store.Service, store.Service, *pathLog, *pathLog) {
	e, _ := newFeatureEnv(t)
	selfHosted(e, "selfa", map[string]string{"amazon-scraper": "/api/scrapers/amazon-scraper/call", "only-a": "/only"})
	selfHosted(e, "selfb", map[string]string{"amazon-scraper": "/b/amazon"})
	upA, a := e.upstream("a", map[string]any{"provider": "selfa", "auth_type": "header", "auth_name": "X-Key"}, "ka")
	upB, b := e.upstream("b", map[string]any{"provider": "selfb", "auth_type": "header", "auth_name": "X-Key"}, "kb")
	la, lb := &pathLog{}, &pathLog{}
	upA.answer = func(k string, r *http.Request) (int, map[string]string, string) {
		la.add(r.URL.Path)
		return 200, nil, `{"from":"a"}`
	}
	upB.answer = func(k string, r *http.Request) (int, map[string]string, string) {
		lb.add(r.URL.Path)
		return 200, nil, `{"from":"b"}`
	}
	return e, upA, upB, a, b, la, lb
}

func TestRotationRoundRobinAcrossProviders(t *testing.T) {
	e, _, _, a, b, la, lb := rotationEnv(t)
	_, tok := e.app(map[string]any{"name": "app"})

	// Off: the service answers alone.
	for i := 0; i < 2; i++ {
		if r, _ := call(t, e, "GET", "/proxy/a/~amazon-scraper/products/details?product=X", tok); r.Header.Get("X-Acnologia-Service") != "a" {
			t.Fatalf("rotation off went to %s", r.Header.Get("X-Acnologia-Service"))
		}
	}
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", a.ID), map[string]any{"rotation": "round_robin"}, 200, nil)
	// b has not joined yet: still a alone.
	if r, _ := call(t, e, "GET", "/proxy/a/~amazon-scraper/products/details", tok); r.Header.Get("X-Acnologia-Service") != "a" {
		t.Fatal("a service with rotation off must not take part")
	}
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", b.ID), map[string]any{"rotation": "round_robin"}, 200, nil)
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		r, body := call(t, e, "GET", "/proxy/a/~amazon-scraper/products/details?product=X", tok)
		if r.StatusCode != 200 {
			t.Fatalf("status %d: %s", r.StatusCode, body)
		}
		seen[r.Header.Get("X-Acnologia-Service")]++
	}
	if seen["a"] != 2 || seen["b"] != 2 {
		t.Fatalf("round robin over a and b: %v", seen)
	}
	// Each provider got the path in its own form.
	for _, p := range la.all() {
		if p != "/api/scrapers/amazon-scraper/call/products/details" {
			t.Fatalf("a was asked for %s", p)
		}
	}
	for _, p := range lb.all() {
		if p != "/b/amazon/products/details" {
			t.Fatalf("b was asked for %s", p)
		}
	}
	// An entry only a serves never goes to b.
	before := len(lb.all())
	for i := 0; i < 3; i++ {
		if r, _ := call(t, e, "GET", "/proxy/a/~only-a/x", tok); r.Header.Get("X-Acnologia-Service") != "a" {
			t.Fatal("only-a is not on b")
		}
	}
	if len(lb.all()) != before {
		t.Fatal("b was asked for an entry it does not list")
	}
}

func TestRotationFallbackMovesOnWhenOutOfKeys(t *testing.T) {
	e, upA, _, a, b, _, _ := rotationEnv(t)
	upA.answer = func(string, *http.Request) (int, map[string]string, string) { return 429, nil, `{"error":"slow down"}` }
	_, tok := e.app(map[string]any{"name": "app"})
	e.do(e.super, "POST", "/api/admin/services/rotation", map[string]any{"services": []any{"a", b.ID}, "rotation": "fallback"}, 200, nil)
	r, body := call(t, e, "GET", "/proxy/a/~amazon-scraper/search", tok)
	if r.StatusCode != 200 || r.Header.Get("X-Acnologia-Service") != "b" || !strings.Contains(body, `"from":"b"`) {
		t.Fatalf("a rate limited should fall to b: %d %s %s", r.StatusCode, r.Header.Get("X-Acnologia-Service"), body)
	}
	// An app that may not use b gets a's answer alone.
	_, only := e.app(map[string]any{"name": "only-a", "allowed_services": []string{"a"}})
	if r, _ := call(t, e, "GET", "/proxy/a/~amazon-scraper/search", only); r.Header.Get("X-Acnologia-Service") == "b" {
		t.Fatal("rotation reached a service the app may not use")
	}
	_ = a
}

func TestRotationMatchesAndSettings(t *testing.T) {
	e, _, _, a, b, _, _ := rotationEnv(t)
	var out struct {
		Rotation string          `json:"rotation"`
		Matches  []rotationMatch `json:"matches"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/services/%d/rotation", a.ID), nil, 200, &out)
	if out.Rotation != "" || len(out.Matches) != 1 || out.Matches[0].Slug != "b" || out.Matches[0].Shared != 1 || out.Matches[0].Entries[0] != "amazon-scraper" {
		t.Fatalf("matches: %+v", out)
	}
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", a.ID), map[string]any{"rotation": "sideways"}, 400, nil)
	e.do(e.super, "POST", "/api/admin/services/rotation", map[string]any{"services": []any{"a"}, "rotation": "sideways"}, 400, nil)
	e.do(e.super, "POST", "/api/admin/services/rotation", map[string]any{"services": []any{"nope"}, "rotation": "fallback"}, 404, nil)
	e.do(e.super, "POST", "/api/admin/services/rotation", map[string]any{"services": []any{"a", "b"}, "rotation": "fallback"}, 200, nil)
	var sv store.Service
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/services/%d", b.ID), nil, 200, &sv)
	if sv.Rotation != "fallback" {
		t.Fatalf("rotation not saved: %q", sv.Rotation)
	}
	e.do(e.super, "POST", "/api/admin/services/rotation", map[string]any{"services": []any{"a", "b"}, "rotation": "off"}, 200, nil)
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/services/%d", b.ID), nil, 200, &sv)
	if sv.Rotation != "" {
		t.Fatalf("rotation off not saved: %q", sv.Rotation)
	}
}

// Two services of one provider on a plain base URL serve the same paths.
func TestRotationSameProviderPlainPaths(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("one", map[string]any{"rotation": "round_robin"}, "k1")
	e.upstream("two", map[string]any{"rotation": "round_robin"}, "k2")
	_, tok := e.app(map[string]any{"name": "app"})
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		r, _ := call(t, e, "GET", "/proxy/one/v1/search", tok)
		seen[r.Header.Get("X-Acnologia-Service")]++
	}
	if seen["one"] != 2 || seen["two"] != 2 {
		t.Fatalf("same provider round robin: %v", seen)
	}
}

func TestShapeAnswerPagesAndSelects(t *testing.T) {
	doc := []any{
		map[string]any{"name": "a", "kind": "scraper", "spec": map[string]any{"title": "A", "endpoints": []any{"/x"}}},
		map[string]any{"name": "b", "kind": "other"},
		map[string]any{"name": "c", "kind": "scraper", "spec": map[string]any{"title": "C"}},
	}
	out := map[string]any{}
	shapeAnswer(out, doc, map[string]any{"where": map[string]any{"kind": "scraper"}, "select": []any{"name", "spec.title"}, "limit": float64(1)})
	items := out["items"].([]any)
	if out["total"] != 2 || len(items) != 1 || out["next_offset"] != 1 {
		t.Fatalf("paging: %+v", out)
	}
	row := items[0].(map[string]any)
	if row["name"] != "a" || row["spec.title"] != "A" || len(row) != 2 {
		t.Fatalf("select: %+v", row)
	}
	out = map[string]any{}
	shapeAnswer(out, map[string]any{"data": map[string]any{"list": doc}}, map[string]any{"items": "data.list", "offset": float64(2)})
	if out["total"] != 3 || len(out["items"].([]any)) != 1 || out["next_offset"] != nil {
		t.Fatalf("items path: %+v", out)
	}
}
