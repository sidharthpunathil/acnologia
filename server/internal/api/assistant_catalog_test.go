package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func catalogSession(t *testing.T, e *env, s *Server) *chatSession {
	u, _ := url.Parse(e.srv.URL)
	parts := []string{}
	for _, c := range e.super.Jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return &chatSession{s: s, ctx: context.Background(), user: &store.User{ID: 1, Role: store.RoleSuperadmin},
		cookie: strings.Join(parts, "; "), remote: "127.0.0.1:1234"}
}

func TestAssistantCatalogCreateFindVerify(t *testing.T) {
	e, s := newFeatureEnv(t)
	spec := map[string]any{"base_url": "https://main.example.com", "auth": map[string]any{"type": "query", "name": "api_key"}, "use_standard_rules": true,
		"tests": []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}}}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "acme", "name": "Acme", "spec": spec}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "acme", "slug": "acme", "base_url": "https://main.example.com"}, 201, nil)
	cs := catalogSession(t, e, s)
	for _, n := range []string{"get_catalog", "update_catalog", "find_missing_catalog", "verify_catalog"} {
		if chatToolByName(n) == nil {
			t.Fatalf("%s is not registered", n)
		}
	}
	if !chatToolByName("update_catalog").Confirm {
		t.Fatal("update_catalog changes where keys go: it must confirm")
	}

	// Create the catalog: two products, one with endpoints.
	out := updateCatalogTool(cs, map[string]any{"slug": "acme", "upsert": []any{
		map[string]any{"name": "jobs-scraper", "url": "https://jobs-scraper.example.com", "title": "Jobs Scraper", "group": "Jobs",
			"endpoints": []any{map[string]any{"name": "Search jobs", "path": "/search", "description": "Find jobs.",
				"params": []any{map[string]any{"name": "q", "in": "query", "required": true}}}}},
		map[string]any{"name": "gone", "url": "https://gone.example.com", "title": "Gone"},
	}})
	if !out.OK {
		t.Fatalf("create: %s", out.Model)
	}
	out = getCatalogTool(cs, map[string]any{"slug": "acme"})
	if !out.OK || !strings.Contains(out.Model, `"entries":2`) || !strings.Contains(out.Model, `"endpoints":1`) {
		t.Fatalf("get: %s", out.Model)
	}
	// The new endpoint is an MCP tool at once.
	found := false
	for _, tl := range s.serviceTools(context.Background(), &store.App{Mode: "both"}) {
		found = found || (tl.service == "acme" && tl.example.Path == "/~jobs-scraper/search")
	}
	if !found {
		t.Fatal("the catalog endpoint did not become an MCP tool")
	}
	// A host off the provider's site is refused by validation.
	if out := updateCatalogTool(cs, map[string]any{"slug": "acme", "upsert": []any{map[string]any{"name": "evil", "url": "https://evil.org"}}}); out.OK {
		t.Fatal("an off site host was accepted")
	}

	// Find what the site lists that the catalog lacks.
	catalogFetch = func(ctx context.Context, u string) (string, error) {
		if strings.HasSuffix(u, "sitemap.xml") {
			return "", errors.New("404")
		}
		return `<a href="https://jobs-scraper.example.com">Jobs</a> <a href="https://maps-scraper.example.com/docs">Maps Scraper</a>
			<a href="https://www.example.com">Home</a> <a href="https://other.org">x</a>`, nil
	}
	t.Cleanup(func() { catalogFetch = fetchCatalogPage })
	out = findMissingCatalogTool(cs, map[string]any{"slug": "acme"})
	var miss struct {
		Missing []catalogCandidate `json:"missing"`
		Unseen  []string           `json:"not_seen_on_pages"`
	}
	_ = json.Unmarshal([]byte(out.Model), &miss)
	if len(miss.Missing) != 1 || miss.Missing[0].Name != "maps-scraper" || miss.Missing[0].Title != "Maps Scraper" {
		t.Fatalf("missing: %s", out.Model)
	}
	if len(miss.Unseen) != 1 || miss.Unseen[0] != "gone" {
		t.Fatalf("unseen: %s", out.Model)
	}

	// Verify: one host answers, one does not resolve.
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	t.Cleanup(up.Close)
	origClient := catalogClient
	catalogClient = func() *http.Client {
		tr := up.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "gone.") {
				return nil, &net.DNSError{Err: "no such host", Name: addr, IsNotFound: true}
			}
			return (&net.Dialer{}).DialContext(ctx, network, up.Listener.Addr().String())
		}
		return &http.Client{Transport: tr}
	}
	t.Cleanup(func() { catalogClient = origClient })
	out = verifyCatalogTool(cs, map[string]any{"slug": "acme"})
	if !out.OK || !strings.Contains(out.Summary, "1 reachable") || !strings.Contains(out.Summary, "1 unreachable") || !strings.Contains(out.Model, "does not resolve") {
		t.Fatalf("verify: %s / %s", out.Summary, out.Model)
	}
	if out := getCatalogTool(cs, map[string]any{"slug": "acme", "query": "gone"}); !strings.Contains(out.Model, `"status":"unreachable"`) {
		t.Fatalf("verification not kept: %s", out.Model)
	}

	// Remove the dead entry; its stored result goes on the next verify.
	out = updateCatalogTool(cs, map[string]any{"slug": "acme", "remove": []any{"gone"}})
	if !out.OK || !strings.Contains(out.Summary, "1 removed") {
		t.Fatalf("remove: %s", out.Model)
	}
	if out := getCatalogTool(cs, map[string]any{"slug": "acme"}); !strings.Contains(out.Model, `"entries":1`) {
		t.Fatalf("after remove: %s", out.Model)
	}
	// Changing endpoints of one entry replaces only that entry's endpoints.
	out = updateCatalogTool(cs, map[string]any{"slug": "acme", "upsert": []any{map[string]any{"name": "jobs-scraper",
		"endpoints": []any{map[string]any{"name": "Job details", "path": "details"}, map[string]any{"name": "Companies", "path": "/companies"}}}}})
	if !out.OK {
		t.Fatalf("endpoints: %s", out.Model)
	}
	if out := getCatalogTool(cs, map[string]any{"slug": "acme"}); !strings.Contains(out.Model, `"endpoints":2`) || !strings.Contains(out.Model, `"title":"Jobs Scraper"`) {
		t.Fatalf("endpoint replace: %s", out.Model)
	}

	// Live: one real call per entry through the proxy with a pooled key.
	keyHits := map[string]int{}
	liveUp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyHits[r.Host+r.URL.Path+" "+r.URL.Query().Get("api_key")]++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(liveUp.Close)
	tr := liveUp.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, liveUp.Listener.Addr().String())
	}
	s.UpClient = &http.Client{Transport: tr}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "acmekey1"}}}, 201, nil)
	out = verifyCatalogTool(cs, map[string]any{"slug": "acme", "live": true})
	if !out.OK || !strings.Contains(out.Summary, "1 ok") {
		t.Fatalf("live verify: %s / %s", out.Summary, out.Model)
	}
	if keyHits["jobs-scraper.example.com/details acmekey1"] != 1 {
		t.Fatalf("the live call did not reach the host with the key: %v", keyHits)
	}
	if !chatToolByName("verify_catalog").ConfirmIf(map[string]any{"live": true}) {
		t.Fatal("a live verify spends quota: it must confirm")
	}
}
