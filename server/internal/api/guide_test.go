package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type guideResp struct {
	Markdown  string `json:"markdown"`
	Generated bool   `json:"generated"`
	Guide     struct {
		Service   string `json:"service"`
		ProxyBase string `json:"proxy_base"`
		Summary   string `json:"summary"`
		Endpoints []struct {
			Name string `json:"name"`
		} `json:"endpoints"`
	} `json:"guide"`
}

func TestAdminGuides(t *testing.T) {
	e := newEnv(t)
	var g guideResp
	e.do(e.super, "GET", "/api/admin/providers/serpapi/guide", nil, 200, &g)
	if !strings.Contains(g.Markdown, "# SerpAPI usage guide") || !strings.Contains(g.Guide.ProxyBase, "/proxy/{service}") || !strings.Contains(g.Markdown, "To get a token") {
		t.Fatalf("provider guide: %+v", g)
	}
	e.do(e.super, "GET", "/api/admin/providers/serpapi/guide?service=serpapi", nil, 200, &g)
	if g.Guide.Service != "serpapi" || !strings.HasSuffix(g.Guide.ProxyBase, "/proxy/serpapi") {
		t.Fatalf("provider guide for a service: %+v", g.Guide)
	}
	e.do(e.super, "GET", "/api/admin/providers/elevenlabs/guide?service=serpapi", nil, 400, nil)
	e.do(e.super, "GET", "/api/admin/providers/nope/guide", nil, 404, nil)

	e.do(e.super, "GET", "/api/admin/services/1/guide", nil, 200, &g)
	if g.Guide.Service != "serpapi" || !strings.Contains(g.Markdown, "/proxy/serpapi") {
		t.Fatalf("service guide: %+v", g)
	}
	e.do(e.super, "GET", "/api/admin/services/99/guide", nil, 404, nil)
	// Signed out callers get nothing.
	e.do(http.DefaultClient, "GET", "/api/admin/services/1/guide", nil, 401, nil)
}

// A written guide round trips through create, update, versions and
// rollback, and the guide endpoint renders it.
func TestGuideRoundTrip(t *testing.T) {
	e := newEnv(t)
	spec := func(summary string) map[string]any {
		s := map[string]any{
			"base_url": "https://api.acme.test", "auth": map[string]any{"type": "bearer"}, "use_standard_rules": true,
			"tests": []any{map[string]any{"name": "ok", "status": 200, "body": "{}", "expect": "ok"}},
		}
		if summary != "" {
			s["guide"] = map[string]any{"summary": summary, "endpoints": []any{map[string]any{
				"name": "Search", "method": "POST", "path": "/v1/search", "description": "Find things.",
				"params": []any{map[string]any{"name": "q", "in": "body", "type": "string", "required": true, "description": "Query"}},
				"body":   map[string]any{"q": "coffee"}}}, "notes": []string{"Pages start at 1."}}
		}
		return s
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "acme", "name": "Acme", "category": "Data", "spec": spec("First summary.")}, 201, nil)
	var g guideResp
	e.do(e.super, "GET", "/api/admin/providers/acme/guide", nil, 200, &g)
	if g.Generated || g.Guide.Summary != "First summary." || len(g.Guide.Endpoints) != 1 || !strings.Contains(g.Markdown, "| `q` | body | string | yes | Query |") {
		t.Fatalf("written guide: %+v", g)
	}
	e.do(e.super, "PUT", "/api/admin/providers/acme", map[string]any{"name": "Acme", "spec": spec("Second summary.")}, 200, nil)
	var full struct {
		Spec struct {
			Guide struct {
				Summary string `json:"summary"`
			} `json:"guide"`
		} `json:"spec"`
	}
	e.do(e.super, "GET", "/api/admin/providers/acme", nil, 200, &full)
	if full.Spec.Guide.Summary != "Second summary." {
		t.Fatalf("update lost the guide: %+v", full)
	}
	e.do(e.super, "POST", "/api/admin/providers/acme/rollback", map[string]any{"version": 1}, 200, nil)
	e.do(e.super, "GET", "/api/admin/providers/acme/guide", nil, 200, &g)
	if g.Guide.Summary != "First summary." {
		t.Fatalf("rollback lost the guide: %+v", g.Guide)
	}
	// An invalid guide is refused like any other spec problem.
	bad := spec("x")
	bad["guide"].(map[string]any)["summary"] = ""
	e.do(e.super, "POST", "/api/admin/providers/validate", map[string]any{"slug": "acme", "name": "Acme", "spec": bad}, 200, nil)
	e.do(e.super, "PUT", "/api/admin/providers/acme", map[string]any{"name": "Acme", "spec": bad}, 400, nil)
}

func TestConsumerGuide(t *testing.T) {
	e := newEnv(t)
	var other store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "elevenlabs", "slug": "voice", "base_url": "https://example.com"}, 201, &other)
	_, tok := e.app(map[string]any{"name": "site", "allowed_services": []string{"serpapi"}})
	auth := []string{"Authorization", "Bearer " + tok}

	var list struct {
		Services []map[string]any `json:"services"`
	}
	e.do(http.DefaultClient, "GET", "/api/v1/services", nil, 200, &list, auth...)
	if len(list.Services) != 1 || !strings.HasSuffix(list.Services[0]["guide_url"].(string), "/api/v1/services/serpapi/guide") {
		t.Fatalf("services: %+v", list.Services)
	}

	var g guideResp
	e.do(http.DefaultClient, "GET", "/api/v1/services/serpapi/guide", nil, 200, &g, auth...)
	if g.Guide.Service != "serpapi" || strings.Contains(g.Markdown, "To get a token") || !strings.Contains(g.Markdown, "Authorization: Bearer acn_...") {
		t.Fatalf("app guide: %+v", g)
	}
	// The app may not read guides for services it cannot use.
	e.do(http.DefaultClient, "GET", "/api/v1/services/voice/guide", nil, 403, nil, auth...)
	e.do(http.DefaultClient, "GET", "/api/v1/services/serpapi/guide", nil, 401, nil)

	for _, hdr := range [][]string{{"Accept", "text/markdown"}, {"X-Format", "query"}} {
		path := "/api/v1/services/serpapi/guide"
		if hdr[0] == "X-Format" {
			path += "?format=md"
		}
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set(hdr[0], hdr[1])
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/markdown") || !strings.HasPrefix(string(b), "# ") {
			t.Fatalf("%v: %d %s %.80s", hdr, resp.StatusCode, resp.Header.Get("Content-Type"), b)
		}
	}
}

// The generate route is mounted for admins and answers 503 without AI.
func TestGenerateGuideRoute(t *testing.T) {
	e := newEnv(t)
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	e.do(e.super, "POST", "/api/admin/providers/serpapi/guide/generate", map[string]any{}, 503, &out)
	if out.Error.Code != "ai_disabled" {
		t.Fatalf("code = %q", out.Error.Code)
	}
	e.do(http.DefaultClient, "POST", "/api/admin/providers/serpapi/guide/generate", map[string]any{}, 401, nil)
}

func TestMCPServiceGuide(t *testing.T) {
	e := newEnv(t)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "elevenlabs", "slug": "voice", "base_url": "https://example.com"}, 201, nil)
	_, tok := e.app(map[string]any{"name": "agent", "allowed_services": []string{"serpapi"}})
	m := &mcpClient{e: e, tok: tok}

	init := m.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)
	if !strings.Contains(init["instructions"].(string), "service_guide") {
		t.Fatalf("instructions: %v", init["instructions"])
	}
	if !has(m.toolNames(), "service_guide") {
		t.Fatal("service_guide is a core tool")
	}
	out, isErr := m.tool("service_guide", map[string]any{"service": "serpapi"})
	if isErr || !strings.Contains(out["markdown"].(string), "/proxy/serpapi") {
		t.Fatalf("service_guide: %v", out)
	}
	if out, isErr := m.tool("service_guide", map[string]any{"service": "voice"}); !isErr {
		t.Fatalf("an app must not read a guide for a service it cannot use: %v", out)
	}

	tpls := m.call("resources/templates/list", nil)["result"].(map[string]any)["resourceTemplates"].([]any)
	found := false
	for _, tp := range tpls {
		if tp.(map[string]any)["uriTemplate"] == "acnologia://services/{slug}/guide" {
			found = true
		}
	}
	if !found {
		t.Fatalf("templates: %v", tpls)
	}
	uris := []string{}
	for _, r := range m.call("resources/list", nil)["result"].(map[string]any)["resources"].([]any) {
		uris = append(uris, r.(map[string]any)["uri"].(string))
	}
	if !has(uris, "acnologia://services/serpapi/guide") || has(uris, "acnologia://services/voice/guide") {
		t.Fatalf("resources: %v", uris)
	}
	res := m.call("resources/read", map[string]any{"uri": "acnologia://services/serpapi/guide"})["result"].(map[string]any)
	c := res["contents"].([]any)[0].(map[string]any)
	if c["mimeType"] != "text/markdown" || !strings.HasPrefix(c["text"].(string), "# SerpAPI usage guide") {
		t.Fatalf("guide resource: %v", c)
	}
	if res := m.call("resources/read", map[string]any{"uri": "acnologia://services/voice/guide"}); res["error"] == nil {
		t.Fatal("reading another service's guide must fail")
	}
}
