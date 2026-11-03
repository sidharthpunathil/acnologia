package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestControlMCPShaping(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "shapekey1", "label": "alpha"}, map[string]any{"secret": "shapekey2", "label": "beta"}, map[string]any{"secret": "shapekey3", "label": "alphabet"})
	c := &ctlClient{e: e, tok: adminToken(e, "write")}

	// Every read tool offers the shaping inputs; writes do not.
	for _, x := range c.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		props := tl["inputSchema"].(map[string]any)["properties"].(map[string]any)
		switch tl["name"] {
		case "list_keys", "search", "list_routes":
			if props["fields"] == nil || props["filter"] == nil || props["limit"] == nil {
				t.Fatalf("%s lacks shaping inputs", tl["name"])
			}
		case "create_service":
			if props["fields"] != nil {
				t.Fatal("a write tool offers fields")
			}
		case "service_guide", "provider_guide":
			if props["format"] == nil {
				t.Fatalf("%s lacks format", tl["name"])
			}
		}
	}

	// fields, filter, limit and offset.
	sc, text, isErr := c.tool("list_keys", map[string]any{"fields": []any{"keys.label", "keys.id"}, "filter": map[string]any{"label": "ALPHA"}, "limit": 1})
	if isErr {
		t.Fatalf("list_keys: %s", text)
	}
	keys := sc["keys"].([]any)
	if len(keys) != 1 || len(keys[0].(map[string]any)) != 2 || !strings.HasPrefix(keys[0].(map[string]any)["label"].(string), "alpha") {
		t.Fatalf("shaped keys: %s", text)
	}
	if sc["_matched"].(map[string]any)["keys"] != float64(2) {
		t.Fatalf("matched count: %s", text)
	}
	sc, _, _ = c.tool("list_keys", map[string]any{"filter": map[string]any{"label": "alpha"}, "offset": 1, "fields": "keys.label"})
	if k := sc["keys"].([]any); len(k) != 1 {
		t.Fatalf("offset: %v", sc)
	}
	if strings.Contains(text, "shapekey") {
		t.Fatal("a secret reached the result")
	}

	// Guides as plain Markdown.
	_, md, isErr := c.tool("service_guide", map[string]any{"service": "serpapi", "format": "markdown"})
	if isErr || !strings.HasPrefix(strings.TrimSpace(md), "#") || strings.HasPrefix(strings.TrimSpace(md), "{") {
		t.Fatalf("markdown guide: %.200s", md)
	}
	// Errors pass through untouched.
	if _, text, isErr := c.tool("get_service", map[string]any{"service": "nope", "fields": []any{"x"}}); !isErr || text == "" {
		t.Fatalf("error shaped: %v %s", isErr, text)
	}
}

func TestControlMCPSearch(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "searchsecret1", "label": "Marketing team key"})
	read := &ctlClient{e: e, tok: adminToken(e, "read")}
	sc, text, isErr := read.tool("search", map[string]any{"query": "serp"})
	if isErr {
		t.Fatalf("search: %s", text)
	}
	types := map[string]bool{}
	for _, h := range sc["results"].([]any) {
		types[h.(map[string]any)["type"].(string)] = true
	}
	if !types["service"] || !types["provider"] {
		t.Fatalf("search serp: %s", text)
	}
	// A member cannot read shares or users: they are skipped and named.
	if sk := sc["skipped"].(map[string]any); sk["user"] == nil || sk["share"] == nil {
		t.Fatalf("skipped: %v", sc["skipped"])
	}
	sc, text, _ = read.tool("search", map[string]any{"query": "marketing", "types": []any{"key"}})
	res := sc["results"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["details_tool"] != "key_details" || strings.Contains(text, "searchsecret1") {
		t.Fatalf("key search: %s", text)
	}
	if _, _, isErr := read.tool("search", map[string]any{"query": " "}); !isErr {
		t.Fatal("empty query accepted")
	}
}

func TestControlMCPBatch(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "write")}
	sc, text, isErr := c.tool("batch", map[string]any{"calls": []any{
		map[string]any{"tool": "whoami"},
		map[string]any{"tool": "list_services", "arguments": map[string]any{"fields": []any{"services.slug"}}},
		map[string]any{"tool": "nope"},
		map[string]any{"tool": "delete_service", "arguments": map[string]any{"service": "serpapi"}},
		map[string]any{"tool": "batch", "arguments": map[string]any{"calls": []any{}}},
	}})
	if isErr {
		t.Fatalf("batch: %s", text)
	}
	r := sc["results"].([]any)
	if len(r) != 5 || sc["failed"] != float64(2) {
		t.Fatalf("batch results: %s", text)
	}
	item := func(i int) map[string]any { return r[i].(map[string]any) }
	if item(0)["ok"] != true || item(0)["result"].(map[string]any)["email"] != "root@x.io" {
		t.Fatalf("whoami in batch: %v", item(0))
	}
	svcs := item(1)["result"].(map[string]any)["services"].([]any)
	if len(svcs[0].(map[string]any)) != 1 {
		t.Fatalf("shaping in batch: %v", item(1))
	}
	if item(2)["ok"] != false || item(4)["ok"] != false {
		t.Fatal("unknown tool or nested batch passed")
	}
	// A destructive tool still stops at its confirm step inside a batch.
	if item(3)["structured"].(map[string]any)["confirm_required"] != true {
		t.Fatalf("delete in batch: %v", item(3))
	}
	if _, err := e.st.GetServiceBySlug(t.Context(), "serpapi"); err != nil {
		t.Fatal("batch deleted a service without confirm")
	}
	// stop_on_error stops at the first failure.
	sc, _, _ = c.tool("batch", map[string]any{"stop_on_error": true, "calls": []any{
		map[string]any{"tool": "nope"}, map[string]any{"tool": "whoami"},
	}})
	if sc["ran"] != float64(1) {
		t.Fatalf("stop_on_error: %v", sc)
	}
	// A read only connection cannot write through batch.
	ro := &ctlClient{e: e, tok: c.tok, path: "/mcp/control?read_only=1"}
	if has(ro.names(), "batch") {
		t.Fatal("batch offered on a read only connection")
	}
	many := []any{}
	for i := 0; i < batchMax+1; i++ {
		many = append(many, map[string]any{"tool": "whoami"})
	}
	if _, _, isErr := c.tool("batch", map[string]any{"calls": many}); !isErr {
		t.Fatal("oversized batch accepted")
	}
}

func TestControlMCPRoutesAndHealth(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	sc, text, isErr := c.tool("list_routes", map[string]any{"contains": "health"})
	if isErr {
		t.Fatalf("list_routes: %s", text)
	}
	found := map[string]string{}
	for _, x := range sc["routes"].([]any) {
		r := x.(map[string]any)
		tool, _ := r["tool"].(string)
		found[r["method"].(string)+" "+r["path"].(string)] = tool
	}
	if found["GET /api/admin/health/settings"] != "health_settings" || found["PUT /api/admin/health/settings"] != "update_health_settings" {
		t.Fatalf("routes: %s", text)
	}
	sc, _, _ = c.tool("list_routes", nil)
	if sc["count"].(float64) < 50 {
		t.Fatalf("too few routes: %v", sc["count"])
	}

	if _, text, isErr := c.tool("update_health_settings", map[string]any{"interval": "1h"}); isErr {
		t.Fatalf("update_health_settings: %s", text)
	}
	sc, _, _ = c.tool("health_settings", nil)
	if sc["interval"] != "1h" || sc["source"] != "saved" {
		t.Fatalf("health_settings: %v", sc)
	}
	if _, text, isErr := c.tool("update_health_settings", map[string]any{"interval": ""}); isErr {
		t.Fatalf("back to env: %s", text)
	}
	if sc, _, _ = c.tool("health_settings", nil); sc["source"] != "env" {
		t.Fatalf("after clearing: %v", sc)
	}

	// Link shares through the control MCP.
	e.addKeys(map[string]any{"secret": "mcplinkkey"})
	sc, text, isErr = c.tool("create_share", map[string]any{"name": "Team", "services": []any{"serpapi"}, "reusable": true, "max_claims": 5, "claim_valid": "30d"})
	if isErr {
		t.Fatalf("create link share: %s", text)
	}
	var sh struct {
		Reusable  bool `json:"reusable"`
		MaxClaims int  `json:"max_claims"`
	}
	b, _ := json.Marshal(sc["share"])
	_ = json.Unmarshal(b, &sh)
	if !sh.Reusable || sh.MaxClaims != 5 || !strings.Contains(argStr(sc, "claim_url"), "/claim/") {
		t.Fatalf("link share: %s", text)
	}
}
