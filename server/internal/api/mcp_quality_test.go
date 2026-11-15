package api

import (
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
