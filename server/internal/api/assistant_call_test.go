package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// The Assistant reads what a self-hosted server serves from the server's
// own list, writes the catalog from it with path entries and free offers,
// and calls reach the entries under the service's base URL.
func TestAssistantCatalogFromSelfHostedServer(t *testing.T) {
	e, s := newFeatureEnv(t)
	spec := map[string]any{"base_url": "https://box.example.com", "auth": map[string]any{"type": "header", "name": "X-Key"}, "use_standard_rules": true,
		"tests": []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}}}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "box", "name": "Box", "spec": spec}, 201, nil)
	up, _ := e.upstream("box", map[string]any{"provider": "box", "auth_type": "header", "auth_name": "X-Key"}, "k1")
	var seen []string
	up.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		seen = append(seen, r.URL.Path)
		if r.URL.Path == "/api/sources" {
			return 200, nil, `[{"name":"amazon-scraper","kind":"scraper","spec":{"title":"Amazon Scraper","endpoints":[{"method":"GET","path":"/products/details"}]}},
				{"name":".github","kind":"other","spec":{"endpoints":[]}}]`
		}
		return 200, nil, `{"asin":"B0"}`
	}
	cs := catalogSession(t, e, s)

	t1 := chatToolByName("call_service")
	if t1 == nil || t1.ConfirmIf(map[string]any{"method": "GET"}) || !t1.ConfirmIf(map[string]any{"method": "POST"}) {
		t.Fatal("call_service: GET runs at once, other methods confirm")
	}
	out := callServiceTool(cs, map[string]any{"service": "box", "path": "/api/sources", "where": map[string]any{"kind": "scraper"},
		"select": []any{"name", "spec.title", "spec.endpoints"}})
	var got struct {
		Status int              `json:"status"`
		Total  int              `json:"total"`
		Items  []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out.Model), &got); err != nil || !out.OK || got.Status != 200 || got.Total != 1 || got.Items[0]["spec.title"] != "Amazon Scraper" {
		t.Fatalf("call_service: %s", out.Model)
	}

	out = updateCatalogTool(cs, map[string]any{"slug": "box", "upsert": []any{map[string]any{
		"name": "amazon-scraper", "url": "/api/scrapers/amazon-scraper/call", "title": "Amazon Scraper", "group": "E-commerce",
		"endpoints": []any{map[string]any{"path": "/products/details", "description": "Product details."}},
		"offer":     map[string]any{"free": true, "notes": "Self-hosted: no quota or rate limit."},
	}}})
	if !out.OK {
		t.Fatalf("update_catalog: %s", out.Model)
	}
	sp := providers.Get("box")
	if len(sp.Hosts) != 1 || sp.Guide == nil || len(sp.Guide.Endpoints) != 1 || sp.Guide.Endpoints[0].Name != "Products details" {
		t.Fatalf("catalog not written: %+v", sp.Guide)
	}
	off := false
	for _, o := range sp.Offers {
		off = off || (o.Free && o.Match != nil && o.Match.Path == "/~amazon-scraper/" && strings.Contains(o.Notes, "Self-hosted"))
	}
	if !off {
		t.Fatalf("offer not written: %+v", sp.Offers)
	}
	_, tok := e.app(map[string]any{"name": "app"})
	if r, body := call(t, e, "GET", "/proxy/box/~amazon-scraper/products/details?product=B0", tok); r.StatusCode != 200 || !strings.Contains(body, "B0") {
		t.Fatalf("proxy: %d %s", r.StatusCode, body)
	}
	if last := seen[len(seen)-1]; last != "/api/scrapers/amazon-scraper/call/products/details" {
		t.Fatalf("upstream path %s", last)
	}
}
