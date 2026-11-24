package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// surfaceProvider registers a provider with an exchange rate endpoint, a key
// info endpoint and a few others, all served by the env's fake upstream.
func surfaceProvider(t *testing.T, slug string) {
	spec := *providers.Get("serpapi")
	spec.Examples = nil
	spec.Guide = &providers.Guide{Summary: "test", Endpoints: []providers.GuideEndpoint{
		{Name: "Exchange rate", Method: "GET", Path: "/fx", Description: "The exchange rate between two currencies.",
			Params: []providers.GuideParam{{Name: "from", In: "query", Required: true}, {Name: "to", In: "query", Required: true}}},
		{Name: "Key info", Method: "GET", Path: "/whoami", Description: "Which plan this key is on."},
		{Name: "Weather", Method: "GET", Path: "/weather", Description: "Today's forecast for a city.",
			Params: []providers.GuideParam{{Name: "city", In: "query"}}},
		{Name: "Exchange history", Method: "GET", Path: "/fx/history", Description: "Past prices of a currency pair."},
	}}
	providers.Set(slug, &spec)
	t.Cleanup(func() { providers.Remove(slug) })
}

func toolNamesOf(out map[string]any) []string {
	names := []string{}
	for _, x := range out["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

// The sentence every generated tool shares ("adds a pooled key and rotates
// on rate limits") is kept out of the search index, so "rate" and "key"
// are words a search can use again.
func TestMCPSearchIgnoresSharedBoilerplate(t *testing.T) {
	e := newEnv(t)
	surfaceProvider(t, "surface-test")
	svc := catalogService(t, e, "fx")
	svc.Provider = "surface-test"
	if err := e.st.UpdateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	_, tok := e.app(map[string]any{"name": "agent", "allowed_services": []string{"fx"}})
	m := &mcpClient{e: e, tok: tok}

	out, isErr := m.tool("search_tools", map[string]any{"query": "rate"})
	if isErr || out["matches"].(float64) != 1 || toolNamesOf(out)[0] != "fx_exchange_rate" {
		t.Fatalf(`"rate" should find only the exchange rate tool: %v`, out)
	}
	out, _ = m.tool("search_tools", map[string]any{"query": "exchange rate"})
	if names := toolNamesOf(out); len(names) < 2 || names[0] != "fx_exchange_rate" {
		t.Fatalf(`"exchange rate" should rank the rate tool over history: %v`, names)
	}
	out, _ = m.tool("search_tools", map[string]any{"query": "key"})
	if names := toolNamesOf(out); out["matches"].(float64) != 1 || names[0] != "fx_key_info" {
		t.Fatalf(`"key" should find only the key info tool: %v`, out)
	}
}

// A generated tool rejects arguments its schema does not have, before
// anything is sent, and lists the ones it takes; extra_query still works.
func TestMCPServiceToolRejectsUnknownArguments(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "strictkey01"})
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}

	before := e.up.hits["strictkey01"]
	out, isErr := m.tool("serpapi_google_search", map[string]any{"q": "x", "nmu": 10})
	text, _ := out["text"].(string)
	if !isErr || !strings.Contains(text, `"nmu"`) || !strings.Contains(text, "extra_query") || !strings.Contains(text, "q") {
		t.Fatalf("unknown argument: %v", out)
	}
	if e.up.hits["strictkey01"] != before {
		t.Fatal("a rejected call must not reach the provider")
	}
	out, isErr = m.tool("serpapi_google_search", map[string]any{"q": "tea", "extra_query": map[string]any{"hl": "en"}})
	if isErr || out["status"].(float64) != 200 || out["body"].(map[string]any)["q"] != "tea" {
		t.Fatalf("extra_query: %v", out)
	}
	// Every generated tool takes an idempotency key.
	props := m.tools()["serpapi_google_search"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if props["idempotency_key"] == nil {
		t.Fatalf("idempotency_key missing from the schema: %v", props)
	}
}

// An unknown tool name gets up to three close names back: a protocol error
// from tools/call, a tool error from call_tool.
func TestMCPUnknownToolSuggestsCloseNames(t *testing.T) {
	e := newEnv(t)
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}

	res := m.call("tools/call", map[string]any{"name": "serpapi_google_serch", "arguments": map[string]any{}})
	rerr, _ := res["error"].(map[string]any)
	if rerr == nil || rerr["code"].(float64) != -32602 || !strings.Contains(rerr["message"].(string), "Did you mean serpapi_google_search") {
		t.Fatalf("tools/call typo: %v", res)
	}
	out, isErr := m.tool("acnologia_call_tool", map[string]any{"name": "acnologia_get_kye"})
	if !isErr || !strings.Contains(out["text"].(string), "acnologia_get_key") {
		t.Fatalf("call_tool typo: %v", out)
	}
	res = m.call("tools/call", map[string]any{"name": "zzzzzzzzzzzz"})
	if msg := res["error"].(map[string]any)["message"].(string); strings.Contains(msg, "Did you mean") {
		t.Fatalf("nothing is close to that name: %s", msg)
	}
}

// A proxied answer says which service served it, after how many attempts,
// how long to wait, and whether the cache answered; a cut body is marked,
// with the whole body's size.
func TestMCPProxyResultFactsAndTruncation(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("big", nil, "b1")
	var posts atomic.Int32
	up.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		switch r.URL.Path {
		case "/big":
			return 200, nil, `{"items":"` + strings.Repeat("é", 3000) + `"}`
		case "/order":
			posts.Add(1)
			return 201, map[string]string{"Retry-After": "7"}, `{"id":42}`
		}
		return 200, nil, `{"ok":true}`
	}
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/mcp", appID), map[string]any{"core": true, "service_tools": true, "max_result_kb": 1}, 200, nil)
	m := &mcpClient{e: e, tok: tok}

	out, isErr := m.tool("proxy_request", map[string]any{"service": "big", "path": "/big"})
	if isErr || out["truncated"] != true || out["served_by"] != "big" || out["attempts"].(float64) != 1 {
		t.Fatalf("cut body: %v", out)
	}
	body, ok := out["body"].(string)
	if !ok || len(body) > 1024 || !strings.HasPrefix(body, `{"items":"é`) || strings.ContainsRune(body, '�') {
		t.Fatalf("a cut body is text on whole characters: %q", body)
	}
	if total := out["bytes_total"].(float64); total != float64(len(`{"items":""}`)+6000) {
		t.Fatalf("bytes_total: %v", total)
	}

	args := map[string]any{"service": "big", "method": "POST", "path": "/order", "body": map[string]any{"n": 1}, "idempotency_key": "order-1"}
	first, isErr := m.tool("proxy_request", args)
	if isErr || first["retry_after_sec"].(float64) != 7 || first["truncated"] != nil {
		t.Fatalf("first order: %v", first)
	}
	again, _ := m.tool("proxy_request", args)
	if again["cache"] != "replay" || posts.Load() != 1 {
		t.Fatalf("a repeated idempotency key must replay: %v (posts %d)", again, posts.Load())
	}
}

// search_tools says whether each tool's keys are free, and among equally
// good matches puts the one with a free key first.
func TestMCPSearchShowsAvailability(t *testing.T) {
	e := newEnv(t)
	catalogProvider(t, "catalog-test", 0)
	a := catalogService(t, e, "lnka")
	b := catalogService(t, e, "lnkb")
	var keys struct {
		Keys []store.Key `json:"keys"`
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", a.ID), map[string]any{"keys": []map[string]any{{"secret": "availa01"}}}, 201, &keys)
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", b.ID), map[string]any{"keys": []map[string]any{{"secret": "availb01"}}}, 201, nil)
	waitCreationChecks(t, e, a.ID, 1)
	waitCreationChecks(t, e, b.ID, 1)
	_, tok := e.app(map[string]any{"name": "agent", "allowed_services": []string{"lnka", "lnkb"}})
	m := &mcpClient{e: e, tok: tok}

	find := func(out map[string]any, name string) map[string]any {
		for _, x := range out["tools"].([]any) {
			if x.(map[string]any)["name"] == name {
				return x.(map[string]any)
			}
		}
		t.Fatalf("%s not in %v", name, toolNamesOf(out))
		return nil
	}
	out, _ := m.tool("search_tools", map[string]any{"query": "linkedin jobs"})
	if names := toolNamesOf(out); names[0] != "lnka_linkedin_jobs_search_jobs" || names[1] != "lnkb_linkedin_jobs_search_jobs" {
		t.Fatalf("equal matches keep catalog order while both are free: %v", names)
	}
	av := find(out, "lnka_linkedin_jobs_search_jobs")["available"].(map[string]any)
	if av["state"] != "available" || av["free_keys"].(float64) != 1 || av["breaker"] != "closed" {
		t.Fatalf("availability: %v", av)
	}

	// lnka's only key rests for a minute: lnkb now comes first.
	until := time.Now().Add(time.Minute)
	if err := e.st.ApplyEffect(context.Background(), keys.Keys[0].ID, store.KeyEffect{CooldownUntil: &until}); err != nil {
		t.Fatal(err)
	}
	e.api.keyCounts.mu.Lock()
	e.api.keyCounts.data = nil
	e.api.keyCounts.mu.Unlock()
	out, _ = m.tool("search_tools", map[string]any{"query": "linkedin jobs"})
	if names := toolNamesOf(out); names[0] != "lnkb_linkedin_jobs_search_jobs" {
		t.Fatalf("a tool whose keys rest must follow an equal one with a free key: %v", names)
	}
	av = find(out, "lnka_linkedin_jobs_search_jobs")["available"].(map[string]any)
	if av["state"] != "resting" || av["free_keys"].(float64) != 0 || av["resting_until"] == nil || av["retry_after_sec"].(float64) < 1 {
		t.Fatalf("resting availability: %v", av)
	}
}

// A capability shows up next to its members in search results, and one
// whose routes all read is read-only, so a read-only token can call it.
func TestMCPCapabilitiesInSearchAndReadOnly(t *testing.T) {
	e := newEnv(t)
	catalogProvider(t, "catalog-test", 0)
	svc := catalogService(t, e, "lnk")
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", svc.ID), map[string]any{"keys": []map[string]any{{"secret": "capkey01"}}}, 201, nil)
	e.do(e.super, "POST", "/api/admin/capabilities", map[string]any{
		"slug": "job-search", "name": "Job search", "strategy": "priority",
		"params": []map[string]any{{"name": "query", "type": "string", "required": true}},
		"routes": []map[string]any{{"service": "lnk", "path": "/linkedin/jobs", "query": map[string]string{"keywords": "{{query}}"}}},
	}, 201, nil)
	e.do(e.super, "POST", "/api/admin/capabilities", map[string]any{
		"slug": "job-post", "name": "Post a job", "strategy": "priority",
		"params": []map[string]any{{"name": "title", "type": "string", "required": true}},
		"routes": []map[string]any{{"service": "lnk", "method": "POST", "path": "/video", "body": map[string]any{"prompt": "{{title}}"}}},
	}, 201, nil)
	appID, tok := e.app(map[string]any{"name": "agent", "allowed_services": []string{"lnk", "job-search", "job-post"}})
	m := &mcpClient{e: e, tok: tok}

	tools := m.tools()
	if ann := tools["job_search"]["annotations"].(map[string]any); ann["readOnlyHint"] != true {
		t.Fatalf("a GET only capability is read-only: %v", ann)
	}
	if ann := tools["job_post"]["annotations"].(map[string]any); ann["readOnlyHint"] != false {
		t.Fatalf("a capability with a POST route is not read-only: %v", ann)
	}

	out, _ := m.tool("search_tools", map[string]any{"query": "linkedin jobs"})
	var capHit map[string]any
	for _, x := range out["tools"].([]any) {
		if x.(map[string]any)["name"] == "job_search" {
			capHit = x.(map[string]any)
		}
	}
	if capHit == nil || fmt.Sprint(capHit["served_by"]) != "[lnk]" || capHit["available"].(map[string]any)["state"] != "available" {
		t.Fatalf("the capability should surface with its member: %v", out)
	}
	if names := toolNamesOf(out); names[0] != "job_search" || names[1] != "lnk_linkedin_jobs_search_jobs" {
		t.Fatalf("the capability ranks with its best member, ahead on a tie: %v", names)
	}

	// Capabilities check their arguments too.
	res, isErr := m.tool("job_search", map[string]any{"query": "go", "keyword": "x"})
	if !isErr || !strings.Contains(res["text"].(string), `"keyword"`) {
		t.Fatalf("capability unknown argument: %v", res)
	}

	var tokResp map[string]any
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/tokens", appID), map[string]any{"name": "ro", "read_only": true}, 201, &tokResp)
	ro := &mcpClient{e: e, tok: tokResp["token"].(string)}
	roNames := ro.toolNames()
	if !has(roNames, "job_search") || has(roNames, "job_post") {
		t.Fatalf("read-only tools: %v", roNames)
	}
	got, isErr := ro.tool("job_search", map[string]any{"query": "rust"})
	if isErr || got["status"].(float64) != 200 || got["served_by"] != "lnk" {
		t.Fatalf("read-only token calling a read-only capability: %v", got)
	}
}

// An admin decides whether an upstream tool counts as read-only, whatever
// its server says, and the choice survives a re-sync.
func TestUpstreamMCPToolAccessOverride(t *testing.T) {
	e := newEnv(t)
	goodTok := "77b1c0ffee0d15ea5e0000000000000000000000beefcafe"
	upstream := newMockMCPServer(t, goodTok, "limited-x", "bad-x", nil)
	defer upstream.Close()
	var svc map[string]any
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "mcp", "slug": "scrapebox", "name": "Scrapebox", "mcp_url": upstream.URL + "/mcp/"}, 201, &svc)
	svcID := int(svc["id"].(float64))
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", svcID), map[string]any{"keys": []map[string]any{{"label": "primary", "secret": goodTok}}}, 201, nil)
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/mcp/sync", svcID), map[string]any{}, 200, nil)

	appID, _ := e.app(map[string]any{"name": "agent", "mode": "both"})
	var tokResp map[string]any
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/tokens", appID), map[string]any{"name": "ro", "read_only": true}, 201, &tokResp)
	ro := &mcpClient{e: e, tok: tokResp["token"].(string)}
	if names := ro.toolNames(); has(names, "scrapebox_run_scrape") || !has(names, "scrapebox_list_jobs") {
		t.Fatalf("before any override: %v", names)
	}

	path := fmt.Sprintf("/api/admin/services/%d/mcp/tools", svcID)
	var view map[string]any
	e.do(e.super, "PATCH", path, map[string]any{"name": "scrapebox_list_jobs", "access": "write"}, 200, &view)
	e.do(e.super, "PATCH", path, map[string]any{"name": "scrapebox_run_scrape", "access": "read"}, 200, &view)
	e.do(e.super, "PATCH", path, map[string]any{"name": "scrapebox_run_scrape", "access": "maybe"}, 400, nil)
	e.do(e.super, "PATCH", path, map[string]any{"name": "nope", "access": "read"}, 404, nil)

	check := func(view map[string]any) {
		t.Helper()
		byName := map[string]map[string]any{}
		for _, x := range view["tools"].([]any) {
			byName[x.(map[string]any)["name"].(string)] = x.(map[string]any)
		}
		lj, rs := byName["scrapebox_list_jobs"], byName["scrapebox_run_scrape"]
		if lj["access"] != "write" || lj["read_only"] != false || lj["upstream_read_only"] != true {
			t.Fatalf("list_jobs view: %v", lj)
		}
		if rs["access"] != "read" || rs["read_only"] != true || rs["upstream_read_only"] != false {
			t.Fatalf("run_scrape view: %v", rs)
		}
	}
	check(view)
	names := ro.toolNames()
	if !has(names, "scrapebox_run_scrape") || has(names, "scrapebox_list_jobs") {
		t.Fatalf("read-only token after the overrides: %v", names)
	}
	if res := ro.call("tools/call", map[string]any{"name": "scrapebox_list_jobs", "arguments": map[string]any{}}); res["error"] == nil {
		t.Fatalf("a tool the admin marked write must be refused: %v", res)
	}
	got := ro.call("tools/call", map[string]any{"name": "scrapebox_run_scrape", "arguments": map[string]any{"url": "https://example.com"}})["result"].(map[string]any)
	if got["isError"] == true {
		t.Fatalf("a tool the admin marked read must run: %v", got)
	}
	// Upstream tools keep their own schemas: an extra argument goes through.
	if got := ro.call("tools/call", map[string]any{"name": "scrapebox_run_scrape", "arguments": map[string]any{"url": "https://example.com", "extra": 1}})["result"].(map[string]any); got["isError"] == true {
		t.Fatalf("upstream tools are not checked here: %v", got)
	}

	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/mcp/sync", svcID), map[string]any{}, 200, &view)
	check(view)
	e.do(e.super, "PATCH", path, map[string]any{"name": "scrapebox_list_jobs", "access": ""}, 200, &view)
	if names := ro.toolNames(); !has(names, "scrapebox_list_jobs") {
		t.Fatalf("clearing the override follows the server again: %v", names)
	}
}
