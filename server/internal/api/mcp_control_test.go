package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// ctlClient talks to /mcp/control with an admin API token.
type ctlClient struct {
	e    *env
	tok  string
	path string
	hdr  []string
	id   int
}

func (c *ctlClient) call(method string, params any) map[string]any {
	c.id++
	p := c.path
	if p == "" {
		p = "/mcp/control"
	}
	var res map[string]any
	hdr := append([]string{"Authorization", "Bearer " + c.tok}, c.hdr...)
	c.e.do(http.DefaultClient, "POST", p, map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params}, 200, &res, hdr...)
	return res
}

// tool returns the structured content (or the text) and whether it is an error.
func (c *ctlClient) tool(name string, args map[string]any) (map[string]any, string, bool) {
	res := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if res["error"] != nil {
		c.e.t.Fatalf("%s: rpc error %v", name, res["error"])
	}
	r := res["result"].(map[string]any)
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	sc, _ := r["structuredContent"].(map[string]any)
	return sc, text, r["isError"] == true
}

func (c *ctlClient) names() []string {
	res := c.call("tools/list", nil)
	out := []string{}
	for _, t := range res["result"].(map[string]any)["tools"].([]any) {
		out = append(out, t.(map[string]any)["name"].(string))
	}
	return out
}

// adminToken makes an admin API token for root with a scope (read, write or admin).
func adminToken(e *env, scope string) string {
	var out struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "claude " + scope, "scope": scope}, 201, &out)
	return out.Token
}

func TestControlMCPInitializeAndRoles(t *testing.T) {
	e := newEnv(t)
	sup := &ctlClient{e: e, tok: adminToken(e, "admin")}
	init := sup.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)
	if init["serverInfo"].(map[string]any)["name"] != "acnologia-control" || !strings.Contains(init["instructions"].(string), "confirm: true") {
		t.Fatalf("initialize: %v", init)
	}
	caps := init["capabilities"].(map[string]any)
	if caps["tools"] == nil || caps["resources"] == nil || caps["prompts"] == nil {
		t.Fatalf("capabilities: %v", caps)
	}

	member := (&ctlClient{e: e, tok: adminToken(e, "read")}).names()
	if !has(member, "list_services") || !has(member, "list_service_examples") || has(member, "create_service") ||
		has(member, "call_service") || has(member, "settings_summary") || has(member, "list_shares") {
		t.Fatalf("member tools: %v", member)
	}
	admin := (&ctlClient{e: e, tok: adminToken(e, "write")}).names()
	if !has(admin, "create_service") || !has(admin, "call_service") || !has(admin, "list_shares") || has(admin, "create_share") || has(admin, "run_refresh") {
		t.Fatalf("admin tools: %v", admin)
	}
	super := sup.names()
	if !has(super, "create_share") || !has(super, "run_refresh") || has(super, "list_users") || has(super, "reveal_key") || has(super, "api_request") {
		t.Fatalf("superadmin default tools: %v", super)
	}
	all := (&ctlClient{e: e, tok: sup.tok, path: "/mcp/control?groups=all"}).names()
	if !has(all, "list_users") || !has(all, "reveal_key") || !has(all, "api_request") || len(all) != len(ctlTools) {
		t.Fatalf("all groups: %d of %d", len(all), len(ctlTools))
	}

	// Annotations follow the kind.
	for _, x := range sup.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		ann := tl["annotations"].(map[string]any)
		switch tl["name"] {
		case "list_services":
			if ann["readOnlyHint"] != true || ann["destructiveHint"] != false {
				t.Fatalf("list_services annotations: %v", ann)
			}
		case "delete_service":
			props := tl["inputSchema"].(map[string]any)["properties"].(map[string]any)
			if ann["destructiveHint"] != true || props["confirm"] == nil {
				t.Fatalf("delete_service: %v", tl)
			}
		case "check_key":
			if ann["openWorldHint"] != true || ann["readOnlyHint"] != false {
				t.Fatalf("check_key annotations: %v", ann)
			}
		}
	}

	// A member calling an admin tool is refused before any route runs.
	m := &ctlClient{e: e, tok: adminToken(e, "read")}
	if _, text, isErr := m.tool("create_service", map[string]any{"provider": "serpapi", "slug": "nope"}); !isErr || !strings.Contains(text, "admin role") {
		t.Fatalf("member create_service: %v %s", isErr, text)
	}
	if _, _, isErr := m.tool("whoami", nil); isErr {
		t.Fatal("whoami failed")
	}
	sc, _, _ := m.tool("whoami", nil)
	if sc["role"] != "member" || sc["email"] != "root@x.io" {
		t.Fatalf("whoami: %v", sc)
	}
}

func TestControlMCPFilters(t *testing.T) {
	e := newEnv(t)
	tok := adminToken(e, "admin")
	only := (&ctlClient{e: e, tok: tok, path: "/mcp/control?groups=services,keys&exclude=delete_service"}).names()
	if !has(only, "list_services") || !has(only, "add_keys") || has(only, "delete_service") || has(only, "list_apps") || has(only, "overview") {
		t.Fatalf("groups and exclude: %v", only)
	}
	ro := &ctlClient{e: e, tok: tok, path: "/mcp/control?read_only=1"}
	names := ro.names()
	if !has(names, "list_services") || has(names, "create_service") || has(names, "delete_key") || has(names, "call_service") {
		t.Fatalf("read only: %v", names)
	}
	if _, text, isErr := ro.tool("create_service", map[string]any{"provider": "serpapi", "slug": "ro"}); !isErr || !strings.Contains(text, "read only") {
		t.Fatalf("read only call: %s", text)
	}
	var svcs struct{ Services []store.Service }
	e.do(e.super, "GET", "/api/admin/services", nil, 200, &svcs)
	if len(svcs.Services) != 1 {
		t.Fatal("read only mode created a service")
	}
	// The header wins over the query parameter.
	hdr := (&ctlClient{e: e, tok: tok, path: "/mcp/control?groups=keys", hdr: []string{"X-Acnologia-MCP-Groups", "apps"}}).names()
	if !has(hdr, "list_apps") || has(hdr, "list_keys") {
		t.Fatalf("header groups: %v", hdr)
	}
	hro := (&ctlClient{e: e, tok: tok, path: "/mcp/control?read_only=0", hdr: []string{"X-Acnologia-MCP-Read-Only", "1"}}).names()
	if has(hro, "create_service") {
		t.Fatal("header read only ignored")
	}
}

func TestControlMCPConfirmStep(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "ck1"}, map[string]any{"secret": "ck2"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin")}
	sc, text, isErr := c.tool("delete_service", map[string]any{"service": "serpapi"})
	if isErr || sc["confirm_required"] != true || !strings.Contains(text, "serpapi") || !strings.Contains(text, "2 keys") {
		t.Fatalf("preview: %v %s", isErr, text)
	}
	e.do(e.super, "GET", "/api/admin/services/1", nil, 200, nil)
	if _, text, isErr := c.tool("delete_service", map[string]any{"service": "serpapi", "confirm": true}); isErr {
		t.Fatalf("confirmed delete: %s", text)
	}
	e.do(e.super, "GET", "/api/admin/services/1", nil, 404, nil)

	// confirm=off skips the step.
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "again", "base_url": e.upURL}, 201, nil)
	off := &ctlClient{e: e, tok: c.tok, path: "/mcp/control?confirm=off"}
	if sc, text, isErr := off.tool("delete_service", map[string]any{"service": "again"}); isErr || sc["confirm_required"] != nil {
		t.Fatalf("confirm off: %s", text)
	}
	var svcs struct{ Services []store.Service }
	e.do(e.super, "GET", "/api/admin/services", nil, 200, &svcs)
	if len(svcs.Services) != 0 {
		t.Fatalf("services left: %v", svcs.Services)
	}
	// A preview of something missing is an error, not a deletion.
	if _, _, isErr := c.tool("delete_key", map[string]any{"key_id": 999}); !isErr {
		t.Fatal("preview of a missing key should fail")
	}
}

func TestControlMCPWritePath(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "write")}
	sc, text, isErr := c.tool("create_service", map[string]any{"provider": "serpapi", "slug": "search2", "name": "Search two", "base_url": e.upURL})
	if isErr || sc["slug"] != "search2" {
		t.Fatalf("create_service: %s", text)
	}
	sc, text, isErr = c.tool("add_keys", map[string]any{"service": "Search two", "keys": []any{
		map[string]any{"secret": "wk1", "label": "one"}, map[string]any{"secret": "wk2", "tier": "backup"},
	}})
	if isErr || len(sc["keys"].([]any)) != 2 {
		t.Fatalf("add_keys: %s", text)
	}
	sc, text, isErr = c.tool("list_keys", map[string]any{"service": "search2"})
	if isErr || len(sc["keys"].([]any)) != 2 || strings.Contains(text, "wk1") {
		t.Fatalf("list_keys: %s", text)
	}
	sc, text, isErr = c.tool("update_service", map[string]any{"service": "search2", "strategy": "fill_first"})
	if isErr || sc["strategy"] != "fill_first" {
		t.Fatalf("update_service: %s", text)
	}
	// A route's validation error comes back as isError with its message.
	if _, text, isErr := c.tool("update_service", map[string]any{"service": "search2", "max_attempts": 99}); !isErr || !strings.Contains(text, "max_attempts") || !strings.Contains(text, "HTTP 400") {
		t.Fatalf("validation: %s", text)
	}
	// update_app_mcp merges with the current settings.
	appID, _ := e.app(map[string]any{"name": "agent"})
	sc, text, isErr = c.tool("update_app_mcp", map[string]any{"app_id": appID, "insights": true})
	if isErr {
		t.Fatalf("update_app_mcp: %s", text)
	}
	st := sc["settings"].(map[string]any)
	if st["insights"] != true || st["core"] != true {
		t.Fatalf("merged settings: %v", st)
	}
	// The audit trail names the token's owner.
	var audit struct{ Audit []map[string]any }
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	found := false
	for _, a := range audit.Audit {
		if a["action"] == "service.create" && a["target"] == "search2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no audit entry: %v", audit.Audit)
	}
}

func TestControlMCPAuth(t *testing.T) {
	e := newEnv(t)
	_, appTok := e.app(map[string]any{"name": "site"})
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	var out map[string]any
	resp := e.do(http.DefaultClient, "POST", "/mcp/control", body, 401, &out, "Authorization", "Bearer "+appTok)
	if resp.Header.Get("WWW-Authenticate") == "" || !strings.Contains(out["error"].(map[string]any)["message"].(string), "/mcp") {
		t.Fatalf("app token: %v", out)
	}
	e.do(http.DefaultClient, "POST", "/mcp/control", body, 401, nil, "X-Acnologia-Token", appTok)
	e.do(http.DefaultClient, "POST", "/mcp/control", body, 401, nil)
	e.do(http.DefaultClient, "POST", "/mcp/control", body, 401, nil, "Authorization", "Bearer acnadm_made_up")
	// A dashboard session is not enough: the control MCP takes a token only.
	e.do(e.super, "POST", "/mcp/control", body, 401, nil)
	e.do(http.DefaultClient, "GET", "/mcp/control", nil, 405, nil)
	e.do(http.DefaultClient, "DELETE", "/mcp/control", nil, 204, nil)
	// A notification gets 202.
	tok := adminToken(e, "read")
	e.do(http.DefaultClient, "POST", "/mcp/control", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, 202, nil, "Authorization", "Bearer "+tok)
}

func TestControlMCPRawRequest(t *testing.T) {
	e := newEnv(t)
	tok := adminToken(e, "admin")
	c := &ctlClient{e: e, tok: tok, path: "/mcp/control?groups=raw"}
	sc, text, isErr := c.tool("api_request", map[string]any{"method": "GET", "path": "/api/admin/services"})
	if isErr || len(sc["services"].([]any)) != 1 {
		t.Fatalf("raw GET: %s", text)
	}
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/admin/tokens"}, {"POST", "/api/admin/tokens"}, {"DELETE", "/api/admin/tokens/1"},
		{"PATCH", "/api/admin/me"}, {"GET", "/api/v1/services"}, {"GET", "/api/admin/../v1/me"},
		{"GET", "/api/admin/%2e%2e/v1/me"}, {"GET", "/api/admin/chat/threads"}, {"GET", "/api/admin/services?x=1"},
	} {
		if _, text, isErr := c.tool("api_request", map[string]any{"method": p.method, "path": p.path, "confirm": true}); !isErr {
			t.Fatalf("%s %s was allowed: %s", p.method, p.path, text)
		}
	}
	// A raw DELETE needs confirm like any destructive tool.
	keys := e.addKeys(map[string]any{"secret": "rawkey1"})
	kp := "/api/admin/keys/" + itoa(int(keys[0].ID))
	if sc, _, _ := c.tool("api_request", map[string]any{"method": "DELETE", "path": kp}); sc["confirm_required"] != true {
		t.Fatalf("raw delete without confirm: %v", sc)
	}
	e.do(e.super, "GET", kp+"/insights", nil, 200, nil)

	// The route still refuses an API token on a session only route, even
	// if a call got past the path check.
	r := httptest.NewRequest("POST", "/mcp/control", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	u, _ := e.api.controlUser(r)
	cc := &ctlCtx{s: e.api, r: r, user: u, opts: ctlOpts{confirm: true}}
	code, out, _ := cc.apiJSON("GET", "/api/admin/tokens", nil)
	if code != 403 || out["error"].(map[string]any)["code"] != "session_required" {
		t.Fatalf("session only route through the MCP: %d %v", code, out)
	}
}

func TestControlMCPResourcesAndPrompts(t *testing.T) {
	e := newEnv(t)
	c := &ctlClient{e: e, tok: adminToken(e, "read")}
	list := c.call("resources/list", nil)["result"].(map[string]any)["resources"].([]any)
	uris := []string{}
	for _, r := range list {
		uris = append(uris, r.(map[string]any)["uri"].(string))
	}
	if !has(uris, "acnologia://overview") || !has(uris, "acnologia://services") || !has(uris, "acnologia://docs/api") || !has(uris, "acnologia://llms-full.txt") {
		t.Fatalf("resources: %v", uris)
	}
	read := func(uri string) string {
		res := c.call("resources/read", map[string]any{"uri": uri})
		if res["error"] != nil {
			t.Fatalf("%s: %v", uri, res["error"])
		}
		return res["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	}
	if !strings.Contains(read("acnologia://services"), `"serpapi"`) || !strings.Contains(read("acnologia://overview"), "services") {
		t.Fatal("services or overview resource")
	}
	if !strings.Contains(read("acnologia://docs/api"), "/mcp/control") {
		t.Fatal("api doc does not mention the control MCP")
	}
	if read("acnologia://llms-full.txt") == "" {
		t.Fatal("llms-full")
	}
	if res := c.call("resources/read", map[string]any{"uri": "acnologia://docs/nope"}); res["error"] == nil {
		t.Fatal("missing doc should fail")
	}
	tpl := c.call("resources/templates/list", nil)["result"].(map[string]any)["resourceTemplates"].([]any)
	if tpl[0].(map[string]any)["uriTemplate"] != "acnologia://docs/{slug}" {
		t.Fatalf("templates: %v", tpl)
	}
	prompts := c.call("prompts/list", nil)["result"].(map[string]any)["prompts"].([]any)
	if len(prompts) != 4 {
		t.Fatalf("prompts: %v", prompts)
	}
	p := c.call("prompts/get", map[string]any{"name": "share_with_person", "arguments": map[string]string{"services": "serpapi", "days": "3"}})["result"].(map[string]any)
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), "create_share") || !strings.Contains(string(b), "3 days") {
		t.Fatalf("prompt: %s", b)
	}
}

func TestControlMCPListingEndpoint(t *testing.T) {
	e := newEnv(t)
	var out struct {
		URL    string           `json:"url"`
		Groups []ctlGroup       `json:"groups"`
		Tools  []map[string]any `json:"tools"`
	}
	e.do(http.DefaultClient, "GET", "/api/admin/mcp/control", nil, 200, &out, "Authorization", "Bearer "+adminToken(e, "read"))
	if !strings.HasSuffix(out.URL, "/mcp/control") || len(out.Groups) != len(ctlGroups) || len(out.Tools) != len(ctlTools) {
		t.Fatalf("listing: %s %d %d", out.URL, len(out.Groups), len(out.Tools))
	}
	for _, tl := range out.Tools {
		switch tl["name"] {
		case "list_services":
			if tl["available"] != true || tl["kind"] != "read" || tl["group"] != "services" {
				t.Fatalf("list_services: %v", tl)
			}
		case "create_service", "call_service":
			if tl["available"] != false {
				t.Fatalf("%v for a member", tl)
			}
		}
	}
	defaults := map[string]bool{}
	for _, g := range out.Groups {
		defaults[g.ID] = g.Default
	}
	if !defaults["services"] || !defaults["call"] || defaults["admin"] || defaults["secrets"] || defaults["raw"] {
		t.Fatalf("group defaults: %v", defaults)
	}
	// A dashboard session sees it too.
	e.do(e.super, "GET", "/api/admin/mcp/control", nil, 200, nil)
}

func TestControlMCPBehindGate(t *testing.T) {
	e := newGateEnv(t)
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	var out map[string]any
	e.do(http.DefaultClient, "POST", "/mcp/control", body, 401, &out)
	if out["error"].(map[string]any)["code"] != "locked" {
		t.Fatalf("locked: %v", out)
	}
	// The superadmin's session is past setup but this client holds no gate
	// cookie; the token alone opens the gate, like /api/admin.
	tok := adminTokenGated(e, "write")
	c := &ctlClient{e: e, tok: tok}
	sc, text, isErr := c.tool("list_services", nil)
	if isErr || len(sc["services"].([]any)) != 1 {
		t.Fatalf("list_services behind the gate: %s", text)
	}
	if _, text, isErr := c.tool("create_service", map[string]any{"provider": "serpapi", "slug": "gated", "base_url": e.upURL}); isErr {
		t.Fatalf("write behind the gate: %s", text)
	}
}

// adminTokenGated makes a token on a gated server, sending the password.
func adminTokenGated(e *env, scope string) string {
	var out struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "claude", "scope": scope}, 201, &out, accessHeader, testAccessPassword)
	return out.Token
}

func TestControlMCPCallService(t *testing.T) {
	e := newEnv(t)
	e.up.bad["k1"] = "Your searches for the past hour have exceeded the limit."
	e.addKeys(map[string]any{"secret": "k1", "priority": 1}, map[string]any{"secret": "k2", "priority": 2})
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"strategy": "fill_first"}, 200, nil)
	c := &ctlClient{e: e, tok: adminToken(e, "write")}

	// The key is injected, and the rate limited key is rotated out.
	sc, text, isErr := c.tool("call_service", map[string]any{"service": "serpapi", "path": "/search.json", "query": map[string]any{"q": "coffee"},
		"headers": map[string]any{"Authorization": "Bearer acn_leak", "X-Acnologia-Token": "acn_leak"}})
	if isErr || sc["status"].(float64) != 200 {
		t.Fatalf("call_service: %s", text)
	}
	body := sc["body"].(map[string]any)
	if body["key"] != "k2" || body["q"] != "coffee" {
		t.Fatalf("body: %v", body)
	}
	h := sc["headers"].(map[string]any)
	if h["X-Acnologia-Attempts"] != "2" || !strings.Contains(h["Content-Type"].(string), "json") {
		t.Fatalf("headers: %v", h)
	}

	// The app is made once, and its usage is counted.
	if _, text, isErr := c.tool("call_service", map[string]any{"service": "1", "path": "search.json"}); isErr {
		t.Fatalf("second call: %s", text)
	}
	var apps struct{ Apps []store.App }
	e.do(e.super, "GET", "/api/admin/apps", nil, 200, &apps)
	n := 0
	var appID int64
	for _, a := range apps.Apps {
		if a.Name == controlAppName {
			n++
			appID = a.ID
			if a.Mode != "proxy" {
				t.Fatalf("mode %s", a.Mode)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d Control MCP apps", n)
	}
	var usage struct {
		Usage struct {
			TodayRequests int64 `json:"today_requests"`
		} `json:"usage"`
	}
	e.do(e.super, "GET", "/api/admin/apps/"+itoa(int(appID))+"/usage", nil, 200, &usage)
	if usage.Usage.TodayRequests < 2 {
		t.Fatalf("usage: %+v", usage)
	}

	// Turning the app off stops call_service.
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"active": false}, 200, nil)
	if _, text, isErr := c.tool("call_service", map[string]any{"service": "serpapi", "path": "/search.json"}); !isErr || !strings.Contains(text, "turned off") {
		t.Fatalf("off app: %s", text)
	}

	// A member can read examples but not call.
	m := &ctlClient{e: e, tok: adminToken(e, "read")}
	if _, text, isErr := m.tool("call_service", map[string]any{"service": "serpapi", "path": "/search.json"}); !isErr || !strings.Contains(text, "admin") {
		t.Fatalf("member call: %s", text)
	}
	sc, text, isErr = m.tool("list_service_examples", map[string]any{"service": "serpapi"})
	if isErr {
		t.Fatalf("examples: %s", text)
	}
	ex := sc["examples"].([]any)
	if len(ex) == 0 || ex[0].(map[string]any)["call_service"].(map[string]any)["service"] != "serpapi" {
		t.Fatalf("examples: %v", ex)
	}
}

// TestControlMCPLimit checks a huge route result is trimmed to the cap.
func TestControlMCPResultCap(t *testing.T) {
	big := bytes.Repeat([]byte("é"), ctlResultCap)
	res := ctlText(`"`+string(big)+`"`, false)
	text := res["content"].([]map[string]any)[0]["text"].(string)
	if len(text) > ctlResultCap+200 || !strings.Contains(text, "trimmed") || res["structuredContent"] != nil {
		t.Fatalf("cap: %d", len(text))
	}
	if !strings.HasPrefix(text, `"é`) {
		t.Fatal("trim broke a rune")
	}
}
