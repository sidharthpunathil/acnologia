package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The dashboard features that only api_request reached have tools of their
// own now, with typed inputs and the confirm step on the destructive ones.
func TestControlMCPParityTools(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "paritykey1"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	names := c.names()
	for _, n := range []string{"list_capabilities", "create_capability", "update_capability", "delete_capability", "try_capability",
		"service_rotation", "set_rotation", "shared_keys", "link_service_keys", "link_all_shared_keys", "link_service_family",
		"ai_rules", "update_ai_rules", "update_ai_models", "set_ai_price", "reset_ai_price", "share_analytics", "rename_app_token",
		"set_service_logo", "delete_service_logo", "fetch_service_logo", "set_provider_logo", "delete_provider_logo", "fill_service_logos",
		"set_service_mcp_tool_access", "assistant_send", "assistant_threads", "checklist", "quickstart"} {
		if !has(names, n) {
			t.Errorf("%s is not listed", n)
		}
	}

	// Capabilities: create, read by slug, update, try for real, delete with confirm.
	capBody := map[string]any{"slug": "websearch", "name": "Web search",
		"params": []any{map[string]any{"name": "q", "type": "string", "required": true}},
		"routes": []any{map[string]any{"service": "serpapi", "path": "/search.json", "query": map[string]any{"q": "{{q}}"}}}}
	if _, text, isErr := c.tool("create_capability", capBody); isErr {
		t.Fatalf("create_capability: %s", text)
	}
	sc, text, isErr := c.tool("get_capability", map[string]any{"capability": "websearch"})
	if isErr || sc["slug"] != "websearch" {
		t.Fatalf("get_capability by slug: %s", text)
	}
	if sc, text, isErr = c.tool("update_capability", map[string]any{"capability": "websearch", "description": "Search the web"}); isErr || sc["description"] != "Search the web" {
		t.Fatalf("update_capability: %s", text)
	}
	sc, text, isErr = c.tool("try_capability", map[string]any{"slug": "websearch", "params": map[string]any{"q": "coffee"}})
	if isErr || sc["status"] != float64(200) || sc["service"] != "serpapi" {
		t.Fatalf("try_capability: %s", text)
	}
	if strings.Contains(text, "paritykey1") {
		t.Fatal("try_capability showed the key")
	}
	if sc, _, _ = c.tool("delete_capability", map[string]any{"capability": "websearch"}); sc["confirm_required"] != true || !strings.Contains(sc["would"].(string), "/proxy/websearch") {
		t.Fatalf("delete_capability without confirm: %v", sc)
	}
	if _, text, isErr = c.tool("delete_capability", map[string]any{"capability": "websearch", "confirm": true}); isErr {
		t.Fatalf("delete_capability: %s", text)
	}

	// Rotation for several services at once, by slug.
	if _, text, isErr = c.tool("set_rotation", map[string]any{"services": []any{"serpapi"}, "rotation": "fallback"}); isErr {
		t.Fatalf("set_rotation: %s", text)
	}
	if sc, text, _ = c.tool("service_rotation", map[string]any{"service": "serpapi"}); sc["rotation"] != "fallback" {
		t.Fatalf("service_rotation: %s", text)
	}

	// App token rename.
	appID, _ := e.app(map[string]any{"name": "renamer"})
	sc, _, _ = c.tool("list_app_tokens", map[string]any{"app_id": appID})
	tokID := sc["tokens"].([]any)[0].(map[string]any)["id"]
	if _, text, isErr = c.tool("rename_app_token", map[string]any{"app_id": appID, "token_id": tokID, "name": "ci"}); isErr {
		t.Fatalf("rename_app_token: %s", text)
	}
	if sc, _, _ = c.tool("list_app_tokens", map[string]any{"app_id": appID}); sc["tokens"].([]any)[0].(map[string]any)["name"] != "ci" {
		t.Fatalf("token not renamed: %v", sc)
	}

	// AI rules round trip, and reads that were raw before.
	if _, text, isErr = c.tool("update_ai_rules", map[string]any{"rules": "Be brief."}); isErr {
		t.Fatalf("update_ai_rules: %s", text)
	}
	if _, text, _ = c.tool("ai_rules", nil); !strings.Contains(text, "Be brief.") {
		t.Fatalf("ai_rules: %s", text)
	}
	for _, n := range []string{"shared_keys", "share_analytics"} {
		if _, text, isErr := c.tool(n, nil); isErr {
			t.Fatalf("%s: %s", n, text)
		}
	}
	// A destructive logo removal asks first.
	if sc, _, _ = c.tool("delete_service_logo", map[string]any{"service": "serpapi"}); sc["confirm_required"] != true {
		t.Fatalf("delete_service_logo: %v", sc)
	}
}

// Route errors keep their code: structuredContent carries the error's
// fields and the status, so an agent can branch on them.
func TestControlMCPErrorCodes(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	sc, text, isErr := c.tool("get_service", map[string]any{"service": "999"})
	if !isErr {
		t.Fatalf("missing service: %s", text)
	}
	_, text, isErr = c.tool("key_details", map[string]any{"key_id": 424242})
	res := c.call("tools/call", map[string]any{"name": "key_details", "arguments": map[string]any{"key_id": 424242}})["result"].(map[string]any)
	sc, _ = res["structuredContent"].(map[string]any)
	if !isErr || sc["code"] != "not_found" || sc["status"] != float64(404) || !strings.HasPrefix(text, "HTTP 404 not_found") {
		t.Fatalf("route error: %s %v", text, sc)
	}
	// A route error with extra fields passes them through.
	e2 := routeErr(400, map[string]any{"error": map[string]any{"code": "invalid", "message": "bad", "hint": "send x", "fix": map[string]any{"x": 1}}, "problems": []any{"p"}})
	if e2["hint"] != "send x" || e2["fix"] == nil || e2["problems"] == nil || e2["status"] != 400 {
		t.Fatalf("routeErr: %v", e2)
	}
	if r := ctlRouteError(503, []byte("upstream down")); r["structuredContent"].(map[string]any)["code"] != "unavailable" {
		t.Fatalf("plain text error: %v", r)
	}
	// Errors found before a route runs have a code too.
	if sc, _, _ = c.tool("get_service", map[string]any{}); sc["code"] != "invalid" {
		t.Fatalf("missing input: %v", sc)
	}
	// A tool with an output schema keeps its error fields in the text only.
	m := &ctlClient{e: e, tok: adminToken(e, "read")}
	res = m.call("tools/call", map[string]any{"name": "list_users", "arguments": map[string]any{}})["result"].(map[string]any)
	if res["isError"] != true || res["structuredContent"] != nil {
		t.Fatalf("output schema tool error: %v", res)
	}
}

// List tools take summary: counts over every match and the first rows,
// and a page over the cap is cut to whole items with a note.
func TestControlMCPSummaryAndCap(t *testing.T) {
	e := newEnv(t)
	keys := []map[string]any{}
	for i := 0; i < 12; i++ {
		keys = append(keys, map[string]any{"secret": "summarykey" + itoa(i), "label": "k" + itoa(i), "notes": strings.Repeat("n", 9000)})
	}
	e.addKeys(keys...)
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	sc, text, isErr := c.tool("list_keys", map[string]any{"summary": true})
	if isErr || sc["summarized"] != true || sc["total"] != float64(12) || len(sc["keys"].([]any)) != summaryRows || sc["has_more"] != true {
		t.Fatalf("summary: %.300s", text)
	}
	counts := sc["counts"].(map[string]any)
	if counts["status"].(map[string]any)["active"] != float64(12) {
		t.Fatalf("summary counts: %v", counts)
	}
	// 12 keys of 9 KB notes do not fit 64 KB: the page is cut, still JSON.
	sc, text, isErr = c.tool("list_keys", nil)
	if isErr || sc == nil || sc["trimmed"] == nil || sc["has_more"] != true || len(text) > ctlResultCap {
		t.Fatalf("cut page: %d bytes, %.200s", len(text), text)
	}
	n := len(sc["keys"].([]any))
	if n < 2 || n >= 12 || sc["next_offset"] != float64(n) || !strings.Contains(sc["trimmed"].(string), "fields") {
		t.Fatalf("cut page: %d items, %v", n, sc["trimmed"])
	}
}

// assistant_send drives the dashboard Assistant over an admin token: the
// Assistant's own tool calls carry that token, so they work.
func TestControlMCPAssistant(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{toolUse("tu_1", "list_services", `{}`), text("You have serpapi.")}}
	e.api.chatModel = f.converse
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	sc, text, isErr := c.tool("assistant_send", map[string]any{"text": "which services do I have?", "wait_sec": 10})
	if isErr || sc["done"] != true {
		t.Fatalf("assistant_send: %s", text)
	}
	var tool, answer map[string]any
	for _, x := range sc["items"].([]any) {
		it := x.(map[string]any)
		switch {
		case it["tool"] == "list_services":
			tool = it
		case it["kind"] == "assistant":
			answer = it
		}
	}
	if tool == nil || tool["ok"] != true || answer == nil || !strings.Contains(answer["text"].(string), "serpapi") {
		t.Fatalf("the Assistant's tool call failed over a token: %s", text)
	}
	id := sc["thread_id"]
	sc, text, _ = c.tool("assistant_threads", nil)
	if sc["total"] != float64(1) {
		t.Fatalf("assistant_threads: %s", text)
	}
	if sc, _, _ = c.tool("delete_assistant_thread", map[string]any{"thread_id": id}); sc["confirm_required"] != true {
		t.Fatalf("delete thread confirm: %v", sc)
	}
	_ = time.Second
}

// The checklist follows the dashboard's Getting started rules, and
// quickstart does the first two steps in one confirmed call.
func TestChecklistAndQuickstart(t *testing.T) {
	e := newEnv(t)
	tok := adminToken(e, "admin")
	var cl struct {
		Steps []struct {
			ID   string `json:"id"`
			Done bool   `json:"done"`
			Next struct{ Tool, Command, URL string }
		} `json:"steps"`
		Done int `json:"done"`
	}
	e.do(e.super, "GET", "/api/admin/checklist", nil, 200, &cl)
	if len(cl.Steps) != 4 || cl.Done != 0 || cl.Steps[0].ID != "service" || cl.Steps[0].Next.Tool != "acnologia_quickstart" {
		t.Fatalf("fresh checklist: %+v", cl)
	}
	c := &ctlClient{e: e, tok: tok}
	args := map[string]any{"provider": "serpapi", "slug": "serp2", "keys": []any{"quickkey001", map[string]any{"secret": "quickkey002", "label": "b"}}}
	sc, _, _ := c.tool("quickstart", args)
	if sc["confirm_required"] != true || !strings.Contains(sc["would"].(string), "create service serp2") {
		t.Fatalf("quickstart confirm: %v", sc)
	}
	args["confirm"] = true
	sc, text, isErr := c.tool("quickstart", args)
	if isErr {
		t.Fatalf("quickstart: %s", text)
	}
	keys := sc["keys"].(map[string]any)
	tokv, _ := sc["token"].(string)
	mcp := sc["mcp"].(map[string]any)
	if keys["added"] != float64(2) || !strings.HasPrefix(tokv, "acn_") || !strings.Contains(mcp["claude_code"].(string), tokv) || !strings.HasSuffix(mcp["url"].(string), "/mcp") {
		t.Fatalf("quickstart result: %s", text)
	}
	// The token works on the app side, for that service.
	var me map[string]any
	e.do(client(), "GET", "/api/v1/me", nil, 200, &me, "Authorization", "Bearer "+tokv)
	// An unknown provider is refused before anything is made.
	if sc, _, isErr = c.tool("quickstart", map[string]any{"provider": "nope-nope", "confirm": true}); !isErr || sc["code"] != "invalid" {
		t.Fatalf("unknown provider: %v", sc)
	}

	// The checklist now has a service with keys and an app; the admin
	// token was used, so an agent is connected.
	e.do(client(), "GET", "/api/admin/checklist", nil, 200, &cl, "Authorization", "Bearer "+tok)
	done := map[string]bool{}
	for _, st := range cl.Steps {
		done[st.ID] = st.Done
	}
	if !done["service"] || !done["app_token"] || !done["agent"] {
		t.Fatalf("checklist after quickstart: %+v", cl)
	}
	sc, _, _ = c.tool("checklist", nil)
	b, _ := json.Marshal(sc)
	if sc["total"] != float64(4) || !strings.Contains(string(b), "first_call") {
		t.Fatalf("checklist tool: %s", b)
	}
}
