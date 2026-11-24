package api

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// Every route the router has is in the OpenAPI spec or excluded on
// purpose, and every hand written description names a real route, so the
// spec cannot drift from the server.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	e := newEnv(t)
	ops, missing := e.api.routeOps()
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("routes missing from the OpenAPI spec (add a control tool, an apiDocs entry or an openAPIExcluded reason):\n%s", strings.Join(missing, "\n"))
	}
	for k := range apiDocs {
		if ops[k] == nil {
			t.Errorf("apiDocs describes %s, which the router does not have", k)
		}
	}
	routes := map[string]bool{}
	for _, rt := range e.api.walkRoutes() {
		routes[rt.Method+" "+rt.Path] = true
	}
	for k := range openAPIExcluded {
		if !routes[k] {
			t.Errorf("openAPIExcluded lists %s, which the router does not have", k)
		}
	}
}

func TestOpenAPIServed(t *testing.T) {
	e := newEnv(t)
	var spec map[string]any
	// Open, like the docs: no session or token.
	e.do(client(), "GET", "/api/openapi.json", nil, 200, &spec)
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("version: %v", spec["openapi"])
	}
	paths := spec["paths"].(map[string]any)
	// An admin route carries its tool, role and typed body.
	post := paths["/api/admin/services"].(map[string]any)["post"].(map[string]any)
	if post["x-mcp-tool"] != "acnologia_create_service" || post["x-role"] != "admin" {
		t.Fatalf("create service op: %v", post)
	}
	schema := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["properties"].(map[string]any)["provider"] == nil || !has(toStrings(schema["required"]), "provider") {
		t.Fatalf("create service body: %v", schema)
	}
	// Path parameters use the router's names.
	get := paths["/api/admin/services/{id}"].(map[string]any)["get"].(map[string]any)
	if p := get["parameters"].([]any)[0].(map[string]any); p["name"] != "id" || p["in"] != "path" {
		t.Fatalf("path params: %v", get["parameters"])
	}
	// Consumer, proxy and OAuth routes are there too.
	for _, p := range []string{"/api/v1/lease", "/proxy/{service}/{path}", "/oauth/token", "/mcp", "/mcp/control", "/api/admin/checklist"} {
		if paths[p] == nil {
			t.Errorf("%s is missing", p)
		}
	}
	// No secrets: the spec is the same for everyone and holds no token.
	r := e.do(client(), "GET", "/api/openapi.json", nil, 200, nil)
	if r.Header.Get("Content-Type") == "" || strings.Contains(r.Header.Get("Set-Cookie"), "session") {
		t.Fatal("spec answer")
	}
}

// list_routes gives each route's body schema from the same descriptions.
func TestListRoutesHasBodies(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	sc, text, isErr := c.tool("list_routes", map[string]any{"contains": "/capabilities"})
	if isErr {
		t.Fatalf("list_routes: %s", text)
	}
	found := false
	for _, x := range sc["routes"].([]any) {
		r := x.(map[string]any)
		if r["method"] == http.MethodPost && r["path"] == "/api/admin/capabilities" {
			body, _ := r["body"].(map[string]any)
			if r["tool"] != "acnologia_create_capability" || body == nil || body["properties"].(map[string]any)["routes"] == nil {
				t.Fatalf("capability route: %v", r)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no POST /api/admin/capabilities: %s", text)
	}
}

func toStrings(v any) []string {
	out := []string{}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = t
	}
	return out
}
