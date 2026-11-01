package api

import (
	"net/http"
	"strings"
	"testing"
)

type mcpClient struct {
	e   *env
	tok string
	id  int
}

func (m *mcpClient) call(method string, params any) map[string]any {
	m.id++
	var res map[string]any
	m.e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": m.id, "method": method, "params": params}, 200, &res, "Authorization", "Bearer "+m.tok)
	return res
}

func (m *mcpClient) tool(name string, args map[string]any) (map[string]any, bool) {
	res := m.call("tools/call", map[string]any{"name": name, "arguments": args})
	if res["error"] != nil {
		return map[string]any{"rpc_error": res["error"]}, true
	}
	r := res["result"].(map[string]any)
	if r["isError"] == true {
		return map[string]any{"text": r["content"].([]any)[0].(map[string]any)["text"]}, true
	}
	return r["structuredContent"].(map[string]any), false
}

func (m *mcpClient) toolNames() []string {
	res := m.call("tools/list", nil)
	names := []string{}
	for _, t := range res["result"].(map[string]any)["tools"].([]any) {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	return names
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestMCPv2ToolSetsAndServiceTools(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "mcpkey01"}, map[string]any{"secret": "mcpkey02"}, map[string]any{"secret": "hiddenk1", "hidden": true})
	appID, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}

	init := m.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)
	caps := init["capabilities"].(map[string]any)
	if caps["resources"] == nil || caps["prompts"] == nil {
		t.Fatalf("default capabilities: %v", caps)
	}
	names := m.toolNames()
	// The built-in search spec has a "Google search" example, so a tool is generated for it.
	if !has(names, "get_key") || !has(names, "renew_key") || !has(names, "serpapi_google_search") || has(names, "list_keys") || has(names, "add_keys") {
		t.Fatalf("default tools: %v", names)
	}
	out, isErr := m.tool("serpapi_google_search", map[string]any{"q": "espresso", "engine": "google"})
	if isErr || out["status"].(float64) != 200 || out["body"].(map[string]any)["q"] != "espresso" {
		t.Fatalf("service tool: %v", out)
	}
	if _, isErr := m.tool("list_keys", nil); !isErr {
		t.Fatal("insights must be off by default")
	}

	// Turn on insights and manage, hide one tool, add instructions.
	var view map[string]any
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{
		"core": true, "service_tools": true, "insights": true, "manage": true, "resources": true, "prompts": true,
		"disabled_tools": []string{"proxy_request"}, "instructions": "Prefer cheap searches.", "max_result_kb": 64,
	}, 200, &view)
	names = m.toolNames()
	if !has(names, "list_keys") || !has(names, "check_keys") || has(names, "proxy_request") {
		t.Fatalf("after settings: %v", names)
	}
	if !strings.Contains(m.call("initialize", nil)["result"].(map[string]any)["instructions"].(string), "Prefer cheap searches.") {
		t.Fatal("custom instructions missing")
	}
	keys, _ := m.tool("list_keys", nil)
	if keys["count"].(float64) != 2 {
		t.Fatalf("hidden keys must not show to an app without lease_hidden: %v", keys["count"])
	}
	for _, k := range keys["keys"].([]any) {
		if strings.Contains(strings.ToLower(strings.Join(mapKeys(k.(map[string]any)), ",")), "secret") {
			t.Fatal("key view must not carry a secret")
		}
	}
	st, isErr := m.tool("set_key_status", map[string]any{"key_id": 1, "status": "disabled"})
	if isErr || st["status"] != "disabled" {
		t.Fatalf("set_key_status: %v", st)
	}
	if _, isErr := m.tool("set_key_status", map[string]any{"key_id": 3, "status": "disabled"}); !isErr {
		t.Fatal("hidden key must not be manageable")
	}
	added, _ := m.tool("add_keys", map[string]any{"service": "serpapi", "keys": []map[string]any{{"label": "from agent", "secret": "agentkey9"}, {"secret": "mcpkey02"}}})
	if len(added["added"].([]any)) != 1 || added["skipped"].(float64) != 1 {
		t.Fatalf("add_keys: %v", added)
	}

	// Resources and prompts.
	rs := m.call("resources/read", map[string]any{"uri": "cogito://services/serpapi"})["result"].(map[string]any)
	if !strings.Contains(rs["contents"].([]any)[0].(map[string]any)["text"].(string), "proxy_request") {
		t.Fatal("service resource")
	}
	pr := m.call("prompts/get", map[string]any{"name": "use_service", "arguments": map[string]string{"service": "serpapi"}})
	if pr["result"] == nil {
		t.Fatalf("prompt: %v", pr)
	}
	// The audit log names the app for changes made through MCP.
	var audit struct {
		Audit []struct{ Detail string } `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	found := false
	for _, a := range audit.Audit {
		if strings.Contains(a.Detail, "MCP app agent") {
			found = true
		}
	}
	if !found {
		t.Fatal("MCP changes must be audited with the app name")
	}
}

func mapKeys(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestExclusiveLeases(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "exkey001"}, map[string]any{"secret": "exkey002"})
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}
	auth := []string{"Authorization", "Bearer " + tok}

	a, isErr := m.tool("get_key", map[string]any{"service": "serpapi", "exclusive": true, "ttl_sec": 60})
	if isErr || a["exclusive"] != true {
		t.Fatalf("exclusive lease: %v", a)
	}
	held := int(a["key_id"].(float64))
	// Nobody else gets the held key: leases and the proxy both use the other one.
	for i := 0; i < 4; i++ {
		var l leaseDTO
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
		if int(l.KeyID) == held {
			t.Fatal("exclusive key leased to someone else")
		}
		e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "ok"}, 200, nil, auth...)
		resp := e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=x", nil, 200, nil, auth...)
		if resp.Header.Get("X-Cogito-Key-Id") == itoa(held) {
			t.Fatal("exclusive key used by the proxy")
		}
	}
	// A second exclusive request gets the other key; a third finds none.
	b, _ := m.tool("get_key", map[string]any{"service": "serpapi", "exclusive": true})
	if int(b["key_id"].(float64)) == held {
		t.Fatal("two exclusive leases on one key")
	}
	if _, isErr := m.tool("get_key", map[string]any{"service": "serpapi", "exclusive": true}); !isErr {
		t.Fatal("third exclusive lease should find no idle key")
	}
	// Renew, then report: the key is free again.
	r, isErr := m.tool("renew_key", map[string]any{"lease_id": a["lease_id"], "ttl_sec": 600})
	if isErr || r["ok"] != true {
		t.Fatalf("renew: %v", r)
	}
	m.tool("report_key", map[string]any{"lease_id": a["lease_id"], "outcome": "ok"})
	if _, isErr := m.tool("renew_key", map[string]any{"lease_id": a["lease_id"]}); !isErr {
		t.Fatal("renewing a reported lease must fail")
	}
	c, isErr := m.tool("get_key", map[string]any{"service": "serpapi", "exclusive": true})
	if isErr || int(c["key_id"].(float64)) != held {
		t.Fatalf("released key should be available again: %v", c)
	}
}
