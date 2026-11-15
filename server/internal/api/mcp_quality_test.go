package api

import (
	"fmt"
	"strings"
	"testing"
)

// The MCP servers answer with a protocol version they speak, never one a
// client made up.
func TestMCPNegotiatesProtocolVersion(t *testing.T) {
	e := newEnv(t)
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}
	for asked, want := range map[string]string{
		"2025-11-25": "2025-11-25",
		"2025-06-18": "2025-06-18",
		"2024-11-05": "2024-11-05",
		"2099-01-01": mcpProtocol,
		"":           mcpProtocol,
	} {
		got := m.call("initialize", map[string]any{"protocolVersion": asked})["result"].(map[string]any)["protocolVersion"]
		if got != want {
			t.Errorf("asked %q: got %v, want %s", asked, got, want)
		}
	}
}

// Every built in tool states each hint and has a top level title, so a
// client never takes report_key or get_key for a destructive call.
func TestMCPToolAnnotationsAreComplete(t *testing.T) {
	for _, set := range [][]mcpTool{coreTools, insightTools, manageTools, discoveryTools} {
		for _, tl := range set {
			if tl.Title == "" {
				t.Errorf("%s has no title", tl.Name)
			}
			for _, h := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
				if _, ok := tl.Annotations[h].(bool); !ok {
					t.Errorf("%s: %s not set", tl.Name, h)
				}
			}
		}
	}
	for _, name := range []string{"get_key", "renew_key", "report_key", "add_keys", "check_keys"} {
		var tl *mcpTool
		for _, set := range [][]mcpTool{coreTools, manageTools} {
			if f := findTool(set, name); f != nil {
				tl = f
			}
		}
		if tl == nil || tl.Annotations["destructiveHint"] != false {
			t.Errorf("%s must not be marked destructive", name)
		}
	}
}

func TestMCPListsPageWithHasMore(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "pagekey01"}, map[string]any{"secret": "pagekey02"}, map[string]any{"secret": "pagekey03"})
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{
		"core": true, "service_tools": true, "insights": true}, 200, nil)
	m := &mcpClient{e: e, tok: tok}

	first, isErr := m.tool("list_keys", map[string]any{"limit": 2})
	if isErr || len(first["keys"].([]any)) != 2 || first["has_more"] != true || first["next_offset"].(float64) != 2 || first["total"].(float64) != 3 {
		t.Fatalf("first page: %v", first)
	}
	second, _ := m.tool("list_keys", map[string]any{"limit": 2, "offset": 2})
	if len(second["keys"].([]any)) != 1 || second["has_more"] != false || second["next_offset"] != nil {
		t.Fatalf("second page: %v", second)
	}

	svcs, isErr := m.tool("list_services", nil)
	if isErr || svcs["has_more"] != false || svcs["total"].(float64) < 1 {
		t.Fatalf("list_services: %v", svcs)
	}
}

func TestMCPErrorsSayWhatToDoNext(t *testing.T) {
	e := newEnv(t)
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}
	out, isErr := m.tool("proxy_request", map[string]any{"service": "nope", "path": "/x"})
	if !isErr || !strings.Contains(out["text"].(string), "list_services") {
		t.Fatalf("unknown service: %v", out)
	}
	out, _ = m.tool("no_such_tool", nil)
	if !strings.Contains(out["rpc_error"].(map[string]any)["message"].(string), "search_tools") {
		t.Fatalf("unknown tool: %v", out)
	}
}

// Agents see the built in tools as acnologia_<name>, with an output schema;
// the old names still call, and a hidden tool stays hidden in either form.
func TestMCPPublicNamesAndOutputSchemas(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "pubkey01"})
	appID, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}

	listed := map[string]map[string]any{}
	for _, tl := range m.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		listed[tl.(map[string]any)["name"].(string)] = tl.(map[string]any)
	}
	gk := listed["acnologia_get_key"]
	if gk == nil || listed["get_key"] != nil {
		t.Fatalf("get_key must be listed as acnologia_get_key: %v", keysOfMap(listed))
	}
	if !strings.Contains(gk["description"].(string), "acnologia_report_key") {
		t.Fatalf("descriptions name tools by their public names: %v", gk["description"])
	}
	if gk["outputSchema"].(map[string]any)["type"] != "object" || listed["serpapi_google_search"]["outputSchema"] == nil {
		t.Fatal("built in and service tools carry an output schema")
	}
	init := m.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)
	if !strings.Contains(init["instructions"].(string), "acnologia_search_tools") {
		t.Fatalf("instructions: %v", init["instructions"])
	}

	for _, name := range []string{"acnologia_list_services", "list_services"} {
		if out, isErr := m.tool(name, nil); isErr || out["services"] == nil {
			t.Fatalf("%s: %v", name, out)
		}
	}
	out, _ := m.tool("acnologia_get_key", map[string]any{"service": "serpapi"})
	if out["lease_id"] == nil {
		t.Fatalf("get_key by its public name: %v", out)
	}

	// Hidden under either name, it is hidden under both.
	for _, saved := range []string{"pool_status", "acnologia_pool_status"} {
		e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{
			"core": true, "service_tools": true, "disabled_tools": []string{saved}}, 200, nil)
		for _, name := range []string{"pool_status", "acnologia_pool_status"} {
			if _, isErr := m.tool(name, map[string]any{"service": "serpapi"}); !isErr {
				t.Fatalf("saved %q: %s still callable", saved, name)
			}
		}
	}
}

// conforms checks v against the parts of JSON Schema the output schemas use.
func conforms(schema map[string]any, v any, at string) []string {
	var bad []string
	switch schema["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return []string{at + ": not an object"}
		}
		for _, r := range schema["required"].([]string) {
			if _, ok := m[r]; !ok {
				bad = append(bad, at+"."+r+": missing")
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for k, sub := range props {
			if x, ok := m[k]; ok {
				bad = append(bad, conforms(sub.(map[string]any), x, at+"."+k)...)
			}
		}
	case "array":
		l, ok := v.([]any)
		if !ok {
			return []string{at + ": not an array"}
		}
		for i, x := range l {
			bad = append(bad, conforms(schema["items"].(map[string]any), x, fmt.Sprintf("%s[%d]", at, i))...)
		}
	case "string":
		if _, ok := v.(string); !ok {
			bad = append(bad, at+": not a string")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			bad = append(bad, at+": not a boolean")
		}
	case "integer":
		if f, ok := v.(float64); !ok || f != float64(int64(f)) {
			bad = append(bad, at+": not an integer")
		}
	}
	return bad
}

// Every built in tool's real result matches the output schema it declares:
// clients reject a result that does not.
func TestMCPResultsMatchOutputSchemas(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "schemakey01"}, map[string]any{"secret": "schemakey02"})
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{
		"core": true, "service_tools": true, "insights": true, "manage": true}, 200, nil)
	m := &mcpClient{e: e, tok: tok}

	lease, _ := m.tool("get_key", map[string]any{"service": "serpapi"})
	kid := float64(keys[0].ID)
	calls := []struct {
		name string
		args map[string]any
	}{
		{"list_services", nil},
		{"pool_status", map[string]any{"service": "serpapi"}},
		{"get_key", map[string]any{"service": "serpapi", "exclusive": true}},
		{"renew_key", map[string]any{"lease_id": lease["lease_id"], "ttl_sec": 60}},
		{"report_key", map[string]any{"lease_id": lease["lease_id"], "outcome": "rate_limited", "rotate": true}},
		{"service_guide", map[string]any{"service": "serpapi"}},
		{"proxy_request", map[string]any{"service": "serpapi", "path": "/search.json", "query": map[string]any{"q": "x"}}},
		{"search_tools", map[string]any{"query": "google search"}},
		{"call_tool", map[string]any{"name": "serpapi_google_search", "arguments": map[string]any{"q": "x"}}},
		{"list_keys", map[string]any{"limit": 1}},
		{"key_details", map[string]any{"key_id": kid}},
		{"service_stats", map[string]any{"service": "serpapi"}},
		{"check_keys", map[string]any{"service": "serpapi"}},
		{"set_key_status", map[string]any{"key_id": kid, "status": "active"}},
		{"add_keys", map[string]any{"service": "serpapi", "keys": []any{map[string]any{"secret": "schemakey03"}}}},
	}
	for _, c := range calls {
		out, isErr := m.tool(c.name, c.args)
		if isErr {
			t.Errorf("%s failed: %v", c.name, out)
			continue
		}
		if bad := conforms(toolOutputs[c.name], out, c.name); len(bad) > 0 {
			t.Errorf("%s does not match its output schema: %v", c.name, bad)
		}
	}
	if len(calls) != len(toolOutputs) {
		t.Errorf("checked %d tools, %d have output schemas", len(calls), len(toolOutputs))
	}
	out, _ := m.tool("serpapi_google_search", map[string]any{"q": "x"})
	if bad := conforms(proxyOutput, out, "serpapi_google_search"); len(bad) > 0 {
		t.Errorf("service tool result: %v", bad)
	}
}

// The dashboard shows each tool by the name agents see, and keeps the
// internal name for disabled_tools.
func TestMCPViewShowsPublicNames(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "agent"})
	var view struct {
		Tools []struct {
			Name       string `json:"name"`
			PublicName string `json:"public_name"`
		} `json:"tools"`
	}
	e.do(e.super, "GET", "/api/admin/apps/"+itoa(int(appID))+"/mcp", nil, 200, &view)
	public := map[string]string{}
	for _, tl := range view.Tools {
		public[tl.Name] = tl.PublicName
	}
	if public["get_key"] != "acnologia_get_key" {
		t.Fatalf("get_key public name: %q", public["get_key"])
	}
	if public["serpapi_google_search"] != "serpapi_google_search" {
		t.Fatalf("service tool public name: %q (tools %v)", public["serpapi_google_search"], public)
	}
}
