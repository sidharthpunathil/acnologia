package api

import (
	"strings"
	"testing"
	"time"
)

// The access controls have typed control tools: token limits, tag budgets,
// usage by tag, deny rules (delete asks first), a token's own limits and
// pinned tags, and the server spend ceiling.
func TestControlMCPAccessControls(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	appID, _ := e.app(map[string]any{"name": "billing"})
	app := map[string]any{"app_id": appID}
	with := func(extra map[string]any) map[string]any { return props(app, extra) }

	if _, text, isErr := c.tool("set_token_limits", with(map[string]any{"limits": map[string]any{"daily_requests": 5}})); isErr {
		t.Fatalf("set_token_limits: %s", text)
	}
	if _, text, isErr := c.tool("set_tag_budgets", with(map[string]any{"budgets": []any{map[string]any{"key": "customer", "daily_budget": 2}}})); isErr {
		t.Fatalf("set_tag_budgets: %s", text)
	}
	sc, text, isErr := c.tool("add_deny_rule", with(map[string]any{"method": "post", "service": "serpapi", "path_prefix": "/v1/files"}))
	if isErr || sc["method"] != "POST" {
		t.Fatalf("add_deny_rule: %s", text)
	}
	ruleID := sc["id"]
	sc, text, _ = c.tool("app_controls", app)
	if tl, _ := sc["token_limits"].(map[string]any); tl["daily_requests"] != float64(5) || len(sc["tag_budgets"].([]any)) != 1 || len(sc["deny_rules"].([]any)) != 1 {
		t.Fatalf("app_controls: %s", text)
	}
	if sc, text, _ = c.tool("tag_usage", with(map[string]any{"days": 7})); sc["days"] != float64(7) || len(sc["tag_budgets"].([]any)) != 1 {
		t.Fatalf("tag_usage: %s", text)
	}
	if sc, text, _ = c.tool("list_deny_rules", app); len(sc["rules"].([]any)) != 1 {
		t.Fatalf("list_deny_rules: %s", text)
	}
	sc, _, _ = c.tool("delete_deny_rule", with(map[string]any{"rule_id": ruleID}))
	if sc["confirm_required"] != true || !strings.Contains(sc["would"].(string), "POST serpapi /v1/files") {
		t.Fatalf("delete_deny_rule without confirm: %v", sc)
	}
	if _, text, isErr = c.tool("delete_deny_rule", with(map[string]any{"rule_id": ruleID, "confirm": true})); isErr {
		t.Fatalf("delete_deny_rule: %s", text)
	}
	if sc, _, _ = c.tool("list_deny_rules", app); len(sc["rules"].([]any)) != 0 {
		t.Fatalf("rule still there: %v", sc)
	}

	// A token with its own limits and pinned tags, then cleared.
	sc, text, isErr = c.tool("create_app_token", with(map[string]any{"name": "cust-a", "limits": map[string]any{"monthly_budget": 3.5},
		"pinned_tags": map[string]any{"customer": "cust_A"}}))
	if isErr {
		t.Fatalf("create_app_token: %s", text)
	}
	tok := sc["app_token"].(map[string]any)
	if tok["limits"].(map[string]any)["monthly_budget"] != 3.5 || tok["pinned_tags"].(map[string]any)["customer"] != "cust_A" {
		t.Fatalf("token controls: %v", tok)
	}
	if _, text, isErr = c.tool("update_app_token", with(map[string]any{"token_id": tok["id"], "pinned_tags": map[string]any{}})); isErr {
		t.Fatalf("update_app_token: %s", text)
	}
	sc, _, _ = c.tool("list_app_tokens", app)
	for _, x := range sc["tokens"].([]any) {
		if r := x.(map[string]any); r["id"] == tok["id"] && len(asMap(r["pinned_tags"])) != 0 {
			t.Fatalf("pinned tags not cleared: %v", r)
		}
	}

	// The spend ceiling: save one, then go back to the environment's.
	if sc, text, _ = c.tool("set_spend_ceiling", map[string]any{"usd": 12.5}); sc["ceiling_usd"] != 12.5 || sc["source"] != "saved" {
		t.Fatalf("set_spend_ceiling: %s", text)
	}
	if sc, text, _ = c.tool("set_spend_ceiling", map[string]any{"reset": true}); sc["source"] != "env" {
		t.Fatalf("reset ceiling: %s", text)
	}
	if _, text, isErr = c.tool("set_spend_ceiling", nil); !isErr {
		t.Fatalf("set_spend_ceiling with nothing: %s", text)
	}
	if sc, text, _ = c.tool("get_spend_ceiling", nil); sc["source"] != "env" {
		t.Fatalf("get_spend_ceiling: %s", text)
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// Slow checks take async and answer with a job; get_job and list_jobs read
// it. list_events and list_audit take cursors and filters.
func TestControlMCPJobsAndCursors(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "jobkey01"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}

	sc, text, isErr := c.tool("check_all_keys", map[string]any{"async": true})
	if isErr || sc["job_id"] == nil || sc["status_url"] == nil {
		t.Fatalf("check_all_keys async: %s", text)
	}
	jobID := sc["job_id"]
	deadline := time.Now().Add(10 * time.Second)
	for {
		sc, text, _ = c.tool("get_job", map[string]any{"job_id": jobID})
		job := asMap(sc["job"])
		if job["status"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never finished: %s", text)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if sc, text, _ = c.tool("list_jobs", map[string]any{"kind": "keys.check_all", "mine": true}); len(sc["jobs"].([]any)) != 1 {
		t.Fatalf("list_jobs: %s", text)
	}
	if sc, text, _ = c.tool("check_service_keys", map[string]any{"service": "serpapi"}); sc["job_id"] != nil {
		t.Fatalf("without async the check runs at once: %s", text)
	}

	// Events after a cursor come oldest first, one page at a time, and the
	// page's next_cursor picks up where it stopped.
	for i := 0; i < 3; i++ {
		if _, text, isErr := c.tool("call_service", map[string]any{"service": "serpapi", "path": "/search.json"}); isErr {
			t.Fatalf("call_service: %s", text)
		}
	}
	var seen []float64
	cursor := any(float64(0))
	for i := 0; i < 3; i++ {
		sc, text, _ = c.tool("list_events", map[string]any{"kind": "proxy", "after_id": cursor, "limit": 1})
		evs := sc["events"].([]any)
		if len(evs) != 1 {
			t.Fatalf("page %d: %s", i, text)
		}
		id := evs[0].(map[string]any)["id"].(float64)
		if sc["next_cursor"] != id {
			t.Fatalf("page %d next_cursor %v, event %v", i, sc["next_cursor"], id)
		}
		seen = append(seen, id)
		cursor = sc["next_cursor"]
	}
	if !(seen[0] < seen[1] && seen[1] < seen[2]) {
		t.Fatalf("events after a cursor go oldest first: %v", seen)
	}
	if sc, text, _ = c.tool("list_events", map[string]any{"kind": "proxy", "after_id": cursor}); len(sc["events"].([]any)) != 0 || sc["next_cursor"] != cursor {
		t.Fatalf("nothing newer: %s", text)
	}
	if sc, text, _ = c.tool("list_events", map[string]any{"kind": "proxy", "since": "1h"}); len(sc["events"].([]any)) != 3 {
		t.Fatalf("since: %s", text)
	}

	// Audit filters: how it came in and the action.
	e.app(map[string]any{"name": "from the dashboard"})
	if _, text, isErr = c.tool("create_app", map[string]any{"name": "from claude"}); isErr {
		t.Fatalf("create_app: %s", text)
	}
	for via, name := range map[string]string{"control_mcp": "from claude", "dashboard": "from the dashboard"} {
		sc, text, _ = c.tool("list_audit", map[string]any{"via": via, "action": "app.create"})
		rows, _ := sc["audit"].([]any)
		if len(rows) == 0 || rows[0].(map[string]any)["target"] != name || rows[len(rows)-1].(map[string]any)["via"] != via {
			t.Fatalf("%s app.create: %s", via, text)
		}
	}
	if _, text, isErr = c.tool("list_audit", map[string]any{"via": "fax"}); !isErr {
		t.Fatalf("a bad via is refused: %s", text)
	}
}

// Demo data and Try it have control tools too.
func TestControlMCPDemoAndTry(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "trykey01"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	if sc, text, _ := c.tool("demo_status", nil); sc["loaded"] != false {
		t.Fatalf("demo_status: %s", text)
	}
	if _, text, isErr := c.tool("load_demo", nil); !isErr {
		t.Fatalf("load_demo on a server with services needs force: %s", text)
	}
	if _, text, isErr := c.tool("load_demo", map[string]any{"force": true}); isErr {
		t.Fatalf("load_demo: %s", text)
	}
	sc, text, _ := c.tool("demo_status", nil)
	if sc["loaded"] != true {
		t.Fatalf("demo_status after load: %s", text)
	}
	slug := sc["service_slug"].(string)
	if sc, _, _ = c.tool("remove_demo", nil); sc["confirm_required"] != true || !strings.Contains(sc["would"].(string), slug) {
		t.Fatalf("remove_demo without confirm: %v", sc)
	}
	if _, text, isErr := c.tool("remove_demo", map[string]any{"confirm": true}); isErr {
		t.Fatalf("remove_demo: %s", text)
	}
	if _, text, isErr := c.tool("remove_demo", nil); !isErr {
		t.Fatalf("remove_demo with nothing loaded: %s", text)
	}

	sc, text, isErr := c.tool("try_service", map[string]any{"service": "serpapi", "path": "/search.json", "query": map[string]any{"q": "coffee"}})
	if isErr || sc["status"] != float64(200) {
		t.Fatalf("try_service: %s", text)
	}
	if strings.Contains(text, "trykey01") {
		t.Fatal("try_service showed the key")
	}
	// An object body is sent as JSON text.
	if _, text, isErr = c.tool("try_service", map[string]any{"service": "serpapi", "method": "POST", "path": "/search.json", "body": map[string]any{"q": "tea"}}); isErr {
		t.Fatalf("try_service with a body: %s", text)
	}
	sc, text, _ = c.tool("list_events", map[string]any{"kind": "try"})
	if n := len(sc["events"].([]any)); n != 2 {
		t.Fatalf("try events: %d %s", n, text)
	}
}
