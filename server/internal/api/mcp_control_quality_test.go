package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// listed is tools/list by the name a client sees.
func (c *ctlClient) listed() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, x := range c.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		out[tl["name"].(string)] = tl
	}
	return out
}

// Agents see every control tool as acnologia_<name>, in the list and in the
// texts that name tools; the old names still call.
func TestControlMCPPublicNames(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "pubctl01"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}

	listed := c.listed()
	if len(listed) != len(ctlTools) {
		t.Fatalf("listed %d of %d tools", len(listed), len(ctlTools))
	}
	for name := range listed {
		if !strings.HasPrefix(name, toolPrefix) || len(name) > 64 || ctlToolByName(name) == nil {
			t.Errorf("tool listed as %q", name)
		}
	}
	desc := func(name string) string { return listed[toolPrefix+name]["description"].(string) }
	if !strings.Contains(desc("create_service"), "acnologia_list_providers") || !strings.Contains(desc("call_service"), "acnologia_service_guide") {
		t.Fatalf("descriptions name tools by their public names: %q", desc("create_service"))
	}
	// Words that are only values or prose stay as they are.
	if !strings.Contains(desc("start_provider_build"), "(for create_provider)") || !strings.Contains(desc("call_service"), "(search, LLM") {
		t.Fatalf("a value was renamed: %q", desc("start_provider_build"))
	}
	props := listed["acnologia_create_service"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if d := props["provider"].(map[string]any)["description"]; d != "Provider slug from acnologia_list_providers" {
		t.Fatalf("input descriptions: %v", d)
	}
	kind := listed["acnologia_start_provider_build"]["inputSchema"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
	if b, _ := json.Marshal(kind["enum"]); string(b) != `["create_provider","update_provider"]` {
		t.Fatalf("enum values changed: %s", b)
	}
	// The shared schema maps are left alone.
	if svcIn["description"] != "Service id, slug or name" || !strings.Contains(serviceFields["egress_id"].(map[string]any)["description"].(string), " list_egress") {
		t.Fatal("rewriting changed a shared schema")
	}

	init := c.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)["instructions"].(string)
	for _, want := range []string{"acnologia_search finds", "acnologia_batch runs", "acnologia_call_service calls", "acnologia_list_routes", "next_offset"} {
		if !strings.Contains(init, want) {
			t.Errorf("instructions lack %q: %s", want, init)
		}
	}

	// Both names call the same tool.
	for _, name := range []string{"list_services", "acnologia_list_services"} {
		if sc, text, isErr := c.tool(name, nil); isErr || len(sc["services"].([]any)) != 1 {
			t.Fatalf("%s: %s", name, text)
		}
	}
	if res := c.call("tools/call", map[string]any{"name": "acnologia_nope"}); res["error"] == nil {
		t.Fatal("an unknown tool was called")
	}

	// Errors, confirm steps, prompts and results that name a tool use the public name.
	if _, text, _ := c.tool("acnologia_delete_service", map[string]any{"service": "serpapi"}); !strings.Contains(text, "call acnologia_delete_service again") {
		t.Fatalf("confirm step: %s", text)
	}
	m := &ctlClient{e: e, tok: adminToken(e, "read")}
	if _, text, isErr := m.tool("create_service", map[string]any{"provider": "serpapi"}); !isErr || !strings.Contains(text, "acnologia_create_service needs the admin role") {
		t.Fatalf("role error: %s", text)
	}
	p := c.call("prompts/get", map[string]any{"name": "health_report"})["result"].(map[string]any)
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), "Call acnologia_overview, acnologia_list_keys") {
		t.Fatalf("prompt: %s", b)
	}
	p = c.call("prompts/get", map[string]any{"name": "onboard_provider", "arguments": map[string]string{"provider": "x"}})["result"].(map[string]any)
	b, _ = json.Marshal(p)
	if !strings.Contains(string(b), "acnologia_start_provider_build with kind create_provider") {
		t.Fatalf("prompt: %s", b)
	}
	sc, text, _ := c.tool("batch", map[string]any{"calls": []any{
		map[string]any{"tool": "acnologia_whoami"}, map[string]any{"tool": "whoami"}, map[string]any{"tool": "acnologia_batch"},
	}})
	r := sc["results"].([]any)
	if r[0].(map[string]any)["ok"] != true || r[1].(map[string]any)["ok"] != true || !strings.Contains(text, "cannot contain acnologia_batch") {
		t.Fatalf("batch: %s", text)
	}

	// The dashboard keeps the internal name and shows the public one.
	var info struct {
		Tools []map[string]any `json:"tools"`
	}
	e.do(e.super, "GET", "/api/admin/mcp/control", nil, 200, &info)
	if info.Tools[0]["name"] != ctlTools[0].Name || info.Tools[0]["public_name"] != toolPrefix+ctlTools[0].Name {
		t.Fatalf("dashboard listing: %v", info.Tools[0])
	}
}

// exclude takes either form of a tool's name, from the query or the header.
func TestControlMCPExcludeEitherName(t *testing.T) {
	e := newEnv(t)
	tok := adminToken(e, "admin")
	for _, c := range []*ctlClient{
		{e: e, tok: tok, path: "/mcp/control?exclude=delete_service,ACNOLOGIA_delete_key"},
		{e: e, tok: tok, hdr: []string{"X-Acnologia-MCP-Exclude", "acnologia_delete_service, delete_key"}},
	} {
		names := c.names()
		if has(names, "delete_service") || has(names, "delete_key") || !has(names, "list_keys") {
			t.Fatalf("exclude: %v", names)
		}
		for _, name := range []string{"delete_service", "acnologia_delete_key"} {
			if _, text, isErr := c.tool(name, map[string]any{"service": "serpapi", "key_id": 1}); !isErr || !strings.Contains(text, "excluded") {
				t.Fatalf("%s still callable: %s", name, text)
			}
		}
	}
}

// Every tool states each hint and has a top level title.
func TestControlMCPAnnotationsComplete(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}
	for name, tl := range c.listed() {
		ann := tl["annotations"].(map[string]any)
		if tl["title"] == "" || ann["title"] != tl["title"] {
			t.Errorf("%s has no title", name)
		}
		for _, h := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := ann[h].(bool); !ok {
				t.Errorf("%s: %s not set", name, h)
			}
		}
		if ann["readOnlyHint"] == true && ann["destructiveHint"] == true {
			t.Errorf("%s is read only and destructive", name)
		}
	}
}

func TestControlMCPListsPage(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "pagectl01", "label": "a"}, map[string]any{"secret": "pagectl02", "label": "b"}, map[string]any{"secret": "pagectl03", "label": "c"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}

	sc, text, isErr := c.tool("list_keys", map[string]any{"limit": 2, "sort": "label"})
	if isErr || len(sc["keys"].([]any)) != 2 || sc["total"] != float64(3) || sc["count"] != float64(2) || sc["has_more"] != true || sc["next_offset"] != float64(2) {
		t.Fatalf("first page: %s", text)
	}
	sc, text, _ = c.tool("list_keys", map[string]any{"limit": 2, "offset": 2, "sort": "label"})
	k := sc["keys"].([]any)
	if len(k) != 1 || k[0].(map[string]any)["label"] != "c" || sc["has_more"] != false || sc["next_offset"] != nil || sc["offset"] != float64(2) {
		t.Fatalf("second page: %s", text)
	}
	// Without limit a page holds the default size; fields keep the paging block.
	sc, text, _ = c.tool("list_keys", map[string]any{"fields": []any{"keys.label"}})
	if sc["count"] != float64(3) || sc["has_more"] != false || sc["summary"] != nil || len(sc["keys"].([]any)[0].(map[string]any)) != 1 {
		t.Fatalf("default page with fields: %s", text)
	}
	// limit is capped at the tool's largest page.
	sc, _, _ = c.tool("list_providers", map[string]any{"limit": 100000})
	if n := sc["count"].(float64); n > float64(ctlPaging["list_providers"].Max) || n < 1 {
		t.Fatalf("providers page: %v", n)
	}

	// list_events pages through the route's newest events.
	for i := 0; i < 3; i++ {
		if _, text, isErr := c.tool("call_service", map[string]any{"service": "serpapi", "path": "/search.json"}); isErr {
			t.Fatalf("call_service: %s", text)
		}
	}
	sc, text, _ = c.tool("list_events", map[string]any{"kind": "proxy", "limit": 1})
	if len(sc["events"].([]any)) != 1 || sc["total"].(float64) < 3 || sc["has_more"] != true {
		t.Fatalf("events page: %s", text)
	}
	sc, _, _ = c.tool("list_events", map[string]any{"kind": "proxy", "offset": sc["next_offset"]})
	if sc["offset"] != float64(1) || sc["has_more"] != false {
		t.Fatalf("events next page: %v", sc)
	}

	// search pages its hits the same way.
	sc, text, _ = c.tool("search", map[string]any{"query": "a", "limit": 1})
	if len(sc["results"].([]any)) != 1 || sc["has_more"] != true || sc["total"].(float64) < 2 {
		t.Fatalf("search page: %s", text)
	}
	// The input schema says how a list tool pages.
	props := c.listed()["acnologia_list_keys"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if !strings.Contains(props["limit"].(map[string]any)["description"].(string), "default 100") || props["offset"] == nil {
		t.Fatalf("paging inputs: %v", props["limit"])
	}
}

// Every output schema matches the real result of its tool: clients reject
// a result that does not.
func TestControlMCPResultsMatchOutputSchemas(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "schemactl01", "label": "one"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}
	appID, _ := e.app(map[string]any{"name": "schema app"})
	for _, x := range []struct {
		tool string
		args map[string]any
	}{
		{"call_service", map[string]any{"service": "serpapi", "path": "/search.json"}},
		{"create_webhook", map[string]any{"name": "hook", "url": "https://example.com/hook"}},
		{"create_egress", map[string]any{"name": "exit", "url": "http://proxy.example.com:8080"}},
		{"create_service", map[string]any{"provider": "serpapi", "slug": "search2", "base_url": e.upURL}},
		{"create_group", map[string]any{"slug": "searchers", "name": "Searchers", "members": []any{"serpapi", "search2"}}},
		{"create_share", map[string]any{"name": "Team", "services": []any{"serpapi"}}},
	} {
		if _, text, isErr := c.tool(x.tool, x.args); isErr {
			t.Fatalf("%s: %s", x.tool, text)
		}
	}
	calls := map[string]map[string]any{
		"whoami":          nil,
		"list_services":   nil,
		"list_keys":       nil,
		"list_events":     nil,
		"list_apps":       nil,
		"list_app_tokens": {"app_id": appID},
		"list_providers":  nil,
		"list_webhooks":   nil,
		"list_egress":     nil,
		"list_groups":     nil,
		"list_shares":     nil,
		"list_users":      nil,
		"list_audit":      nil,
		"search":          {"query": "s"},
	}
	listed := c.listed()
	for name, args := range calls {
		schema := ctlOutputs[name]
		if schema == nil {
			t.Errorf("%s has no output schema", name)
			continue
		}
		if listed[toolPrefix+name]["outputSchema"] == nil {
			t.Errorf("%s does not list its output schema", name)
		}
		sc, text, isErr := c.tool(name, args)
		if isErr || sc == nil {
			t.Errorf("%s: %s", name, text)
			continue
		}
		if bad := conforms(schema, sc, name); len(bad) > 0 {
			t.Errorf("%s does not match its output schema: %v", name, bad)
		}
		// Each list holds an item, so the item schema was checked too.
		if p := ctlPaging[name]; p.List != "" {
			if l, _ := sc[p.List].([]any); len(l) == 0 {
				t.Errorf("%s returned no items to check", name)
			}
		}
		// So does a page cut down by fields.
		if p := ctlPaging[name]; p.List != "" {
			sc, _, _ = c.tool(name, map[string]any{"query": argStr(args, "query"), "app_id": args["app_id"], "fields": []any{p.List + ".id"}, "limit": 1})
			if bad := conforms(schema, sc, name+" with fields"); len(bad) > 0 {
				t.Errorf("%s with fields: %v", name, bad)
			}
		}
	}
	if len(calls) != len(ctlOutputs) {
		t.Errorf("checked %d tools, %d have output schemas", len(calls), len(ctlOutputs))
	}
}
