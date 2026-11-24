package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newMockMCPServer(t *testing.T, goodTok, limitedTok, badTok string, useSSE *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Require exact /mcp/ trailing slash (like FastAPI/Starlette MCP servers).
		if r.URL.Path == "/mcp" {
			http.Redirect(w, r, "/mcp/", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path != "/mcp/" {
			http.NotFound(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		switch auth {
		case "Bearer " + limitedTok:
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		case "Bearer " + badTok:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid token"}`))
			return
		case "Bearer " + goodTok:
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing token"}`))
			return
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		reply := func(result any) {
			env := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
			b, _ := json.Marshal(env)
			if useSSE != nil && useSSE.Load() {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write(b)
		}

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-sb-1")
			reply(map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "scrapebox", "title": "Scrapebox", "version": "1.2.3"},
				"instructions":    "Use scrapebox tools to inspect and run scrapers.",
			})
		case "tools/list":
			reply(map[string]any{
				"tools": []map[string]any{
					{
						"name":        "scrapebox_list_jobs",
						"title":       "List jobs",
						"description": "List recent scrape jobs.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"limit": map[string]any{"type": "integer"}},
							"required":   []string{},
						},
						"outputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"jobs": map[string]any{"type": "array"}},
						},
						"annotations": map[string]any{"readOnlyHint": true},
					},
					{
						"name":        "search_pages",
						"title":       "Search pages",
						"description": "Search indexed pages.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"q": map[string]any{"type": "string"}},
							"required":   []string{"q"},
						},
						"annotations": map[string]any{"readOnlyHint": true},
					},
					{
						"name":        "scrapebox_run_scrape",
						"title":       "Run scrape",
						"description": "Start a new scrape job.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"url": map[string]any{"type": "string"}},
							"required":   []string{"url"},
						},
						"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false},
					},
				},
			})
		case "tools/call":
			if r.Header.Get("Mcp-Session-Id") != "sess-sb-1" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("session expired"))
				return
			}
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			switch p.Name {
			case "scrapebox_list_jobs":
				// Echo token in text to verify Acnologia scrubs secrets from upstream MCP results.
				reply(map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "jobs ok; debug token=" + goodTok},
					},
					"structuredContent": map[string]any{
						"jobs":  []string{"job-1", "job-2"},
						"echo":  goodTok,
						"limit": p.Arguments["limit"],
					},
				})
			case "search_pages":
				switch p.Arguments["q"] {
				case "fail":
					reply(map[string]any{
						"content": []map[string]any{{"type": "text", "text": "index offline"}},
						"isError": true,
					})
					return
				case "huge":
					reply(map[string]any{
						"content": []map[string]any{{"type": "text", "text": strings.Repeat("x", 300<<10)}},
					})
					return
				}
				reply(map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf("found for %v", p.Arguments["q"])},
					},
				})
			case "scrapebox_run_scrape":
				reply(map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf("started %v", p.Arguments["url"])},
					},
					"structuredContent": map[string]any{"started": true, "url": p.Arguments["url"]},
				})
			default:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32602, "message": "unknown tool"},
				})
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}))
}

func TestUpstreamMCPServiceEndToEndAndReadOnly(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "serp-key-001"})

	goodTok := "33a490bf849686347820f56c9dd6b7eebd813c310deaa86b"
	limitedTok := "limited-upstream-mcp-token-0001"
	badTok := "invalid-upstream-mcp-token-0002"
	var useSSE atomic.Bool
	upstream := newMockMCPServer(t, goodTok, limitedTok, badTok, &useSSE)
	defer upstream.Close()

	// 1. Create a standalone MCP service using built-in "mcp" provider (preserving trailing slash).
	var svc map[string]any
	e.do(e.super, "POST", "/api/admin/services", map[string]any{
		"provider": "mcp",
		"slug":     "scrapebox",
		"name":     "Scrapebox",
		"mcp_url":  upstream.URL + "/mcp/",
	}, 201, &svc)
	svcID := int(svc["id"].(float64))
	if svc["mcp_url"] != upstream.URL+"/mcp/" {
		t.Fatalf("mcp_url should preserve trailing slash, got %v", svc["mcp_url"])
	}

	// Add pooled tokens (rate-limited first so rotation is exercised, then good, then invalid).
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", svcID), map[string]any{
		"keys": []map[string]any{
			{"label": "limited", "secret": limitedTok},
			{"label": "primary", "secret": goodTok},
			{"label": "broken", "secret": badTok},
		},
	}, 201, nil)

	// Run health check: should validate via MCP initialize handshake (429 still proves key is valid, just cooling).
	var chk map[string]any
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/check", svcID), map[string]any{}, 200, &chk)
	if chk["checked"].(float64) != 3 || chk["valid"].(float64) != 2 || chk["invalid"].(float64) != 1 {
		t.Fatalf("unexpected MCP health check summary: %v", chk)
	}

	// Sync upstream MCP tools.
	var synced map[string]any
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/mcp/sync", svcID), map[string]any{}, 200, &synced)
	if synced["tool_count"].(float64) != 3 || synced["server_name"] != "Scrapebox" {
		t.Fatalf("unexpected sync view: %v", synced)
	}

	// Reset limited key back to active so we verify runtime 429 -> rotation on tools/call.
	var keysResp map[string]any
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/services/%d/keys", svcID), nil, 200, &keysResp)
	for _, raw := range keysResp["keys"].([]any) {
		k := raw.(map[string]any)
		if k["label"] == "limited" {
			e.do(e.super, "POST", fmt.Sprintf("/api/admin/keys/%d/reset", int(k["id"].(float64))), map[string]any{}, 200, nil)
		}
	}

	// 2. Full-access app sees upstream tools and executes them with rotation + secret scrubbing.
	appID, fullTok := e.app(map[string]any{"name": "claude-full", "mode": "both"})
	full := &mcpClient{e: e, tok: fullTok}

	listRes := full.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	toolsByName := map[string]map[string]any{}
	for _, raw := range listRes {
		tm := raw.(map[string]any)
		toolsByName[tm["name"].(string)] = tm
	}
	if toolsByName["scrapebox_list_jobs"] == nil {
		t.Fatalf("expected scrapebox_list_jobs (no double prefix), got tools: %v", full.toolNames())
	}
	if toolsByName["scrapebox_search_pages"] == nil {
		t.Fatalf("expected scrapebox_search_pages, got tools: %v", full.toolNames())
	}
	if toolsByName["scrapebox_run_scrape"] == nil {
		t.Fatalf("expected scrapebox_run_scrape, got tools: %v", full.toolNames())
	}
	// Upstream outputSchema must be preserved (not replaced by proxyOutput).
	outSchema, _ := toolsByName["scrapebox_list_jobs"]["outputSchema"].(map[string]any)
	props, _ := outSchema["properties"].(map[string]any)
	if props["jobs"] == nil || props["status"] != nil {
		t.Fatalf("upstream outputSchema was clobbered: %v", outSchema)
	}

	// Direct tools/call on upstream MCP tool returns upstream content + structuredContent directly,
	// rotates past 429, and scrubs the upstream bearer token.
	callResp := full.call("tools/call", map[string]any{
		"name":      "scrapebox_list_jobs",
		"arguments": map[string]any{"limit": 5},
	})
	if callResp["error"] != nil {
		t.Fatalf("unexpected rpc error: %v", callResp["error"])
	}
	res := callResp["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("unexpected tool error: %v", res)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(text, goodTok) {
		t.Fatalf("upstream secret leaked in content text: %s", text)
	}
	sc := res["structuredContent"].(map[string]any)
	if strings.Contains(fmt.Sprint(sc["echo"]), goodTok) {
		t.Fatalf("upstream secret leaked in structuredContent: %v", sc)
	}
	if sc["limit"].(float64) != 5 {
		t.Fatalf("expected limit=5 in structuredContent, got %v", sc)
	}

	// Verify SSE upstream responses work seamlessly too.
	useSSE.Store(true)
	sseResp := full.call("tools/call", map[string]any{
		"name":      "scrapebox_run_scrape",
		"arguments": map[string]any{"url": "https://example.com"},
	})
	sseRes := sseResp["result"].(map[string]any)
	if sseRes["isError"] == true || sseRes["structuredContent"].(map[string]any)["started"] != true {
		t.Fatalf("expected SSE upstream call to succeed, got %v", sseResp)
	}
	useSSE.Store(false)

	// Verify search_tools + call_tool work for upstream MCP tools.
	found, isErr := full.tool("acnologia_search_tools", map[string]any{"query": "scrapebox jobs"})
	if isErr || found["matches"].(float64) < 1 {
		t.Fatalf("expected search_tools to find scrapebox tools, got %v", found)
	}
	// call_tool returns the upstream result as is: the same shape as calling
	// the upstream tool by name, not wrapped a second time.
	direct := full.call("tools/call", map[string]any{"name": "scrapebox_search_pages", "arguments": map[string]any{"q": "golang"}})["result"].(map[string]any)
	viaCall := full.call("tools/call", map[string]any{"name": "acnologia_call_tool",
		"arguments": map[string]any{"name": "scrapebox_search_pages", "arguments": map[string]any{"q": "golang"}}})["result"].(map[string]any)
	if fmt.Sprint(direct) != fmt.Sprint(viaCall) {
		t.Fatalf("call_tool changed the upstream result:\n direct %v\n via    %v", direct, viaCall)
	}
	content := viaCall["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "found for golang" || viaCall["structuredContent"] != nil || viaCall["isError"] != nil {
		t.Fatalf("call_tool on upstream tool: %v", viaCall)
	}
	// An upstream failure stays a failure through call_tool.
	failed := full.call("tools/call", map[string]any{"name": "acnologia_call_tool",
		"arguments": map[string]any{"name": "scrapebox_search_pages", "arguments": map[string]any{"q": "fail"}}})["result"].(map[string]any)
	if failed["isError"] != true || failed["content"].([]any)[0].(map[string]any)["text"] != "index offline" || failed["structuredContent"] != nil {
		t.Fatalf("call_tool must keep the upstream isError: %v", failed)
	}

	// An answer over the app's max result size is an error, logged as one
	// and not billed to the app.
	huge := full.call("tools/call", map[string]any{"name": "scrapebox_search_pages", "arguments": map[string]any{"q": "huge"}})["result"].(map[string]any)
	if huge["isError"] != true || !strings.Contains(fmt.Sprint(huge["content"]), "max result size") {
		t.Fatalf("oversized upstream answer: %v", huge)
	}
	var evs struct {
		Events []struct {
			Kind   string  `json:"kind"`
			Cost   float64 `json:"cost"`
			Detail string  `json:"detail"`
		} `json:"events"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/events?service_id=%d&limit=50", svcID), nil, 200, &evs)
	logged := false
	for _, ev := range evs.Events {
		if strings.Contains(ev.Detail, "search_pages") && strings.Contains(ev.Detail, "max result size") {
			logged = true
			if ev.Kind != "error" || ev.Cost != 0 {
				t.Fatalf("an oversized answer must be an unbilled error event: %+v", ev)
			}
		}
	}
	if !logged {
		t.Fatalf("oversized answer not logged: %+v", evs.Events)
	}

	// Verify service_guide includes synced upstream MCP tools.
	guide, isErr := full.tool("acnologia_service_guide", map[string]any{"service": "scrapebox"})
	if isErr || !strings.Contains(guide["markdown"].(string), "scrapebox_list_jobs") {
		t.Fatalf("service_guide should mention upstream MCP tools: %v", guide)
	}

	// 3. Per-token read-only enforcement across /api/v1/me, /api/v1/lease, /proxy, and /mcp.
	var tokResp map[string]any
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/apps/%d/tokens", appID), map[string]any{
		"name":      "teammate-readonly",
		"read_only": true,
	}, 201, &tokResp)
	roTok := tokResp["token"].(string)
	if tokResp["app_token"].(map[string]any)["read_only"] != true {
		t.Fatalf("expected read_only=true on app_token: %v", tokResp["app_token"])
	}

	// /api/v1/me reports read_only: true.
	var me map[string]any
	e.do(http.DefaultClient, "GET", "/api/v1/me", nil, 200, &me, "Authorization", "Bearer "+roTok)
	if me["read_only"] != true {
		t.Fatalf("expected read_only=true in /api/v1/me: %v", me)
	}

	// /api/v1/lease is blocked for read-only tokens.
	var leaseErr map[string]any
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 403, &leaseErr, "Authorization", "Bearer "+roTok)
	if leaseErr["error"].(map[string]any)["code"] != "read_only_token" {
		t.Fatalf("expected read_only_token on /api/v1/lease, got %v", leaseErr)
	}

	// /proxy allows GET and blocks POST for read-only tokens.
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search?q=ok", nil, 200, nil, "Authorization", "Bearer "+roTok)
	var proxyErr map[string]any
	e.do(http.DefaultClient, "POST", "/proxy/serpapi/search", map[string]any{"q": "nope"}, 403, &proxyErr, "Authorization", "Bearer "+roTok)
	if proxyErr["error"].(map[string]any)["code"] != "read_only_token" {
		t.Fatalf("expected read_only_token on POST /proxy, got %v", proxyErr)
	}

	// /mcp with read-only token exposes ONLY read-only tools.
	ro := &mcpClient{e: e, tok: roTok}
	roNames := ro.toolNames()
	if !has(roNames, "scrapebox_list_jobs") || !has(roNames, "scrapebox_search_pages") || !has(roNames, "serpapi_google_search") || !has(roNames, "list_services") {
		t.Fatalf("read-only token missing expected read-only tools: %v", roNames)
	}
	for _, forbidden := range []string{"scrapebox_run_scrape", "get_key", "renew_key", "report_key", "proxy_request"} {
		if has(roNames, forbidden) {
			t.Fatalf("read-only token must not list %q: %v", forbidden, roNames)
		}
	}

	// Read-only token can call read-only upstream tool, cannot call write upstream tool.
	okRO := ro.call("tools/call", map[string]any{"name": "scrapebox_list_jobs", "arguments": map[string]any{}})
	if okRO["error"] != nil || okRO["result"].(map[string]any)["isError"] == true {
		t.Fatalf("read-only token should call read-only upstream tool: %v", okRO)
	}
	denyRO := ro.call("tools/call", map[string]any{"name": "scrapebox_run_scrape", "arguments": map[string]any{"url": "https://example.com"}})
	if denyRO["error"] == nil {
		t.Fatalf("read-only token must be denied write upstream tool, got %v", denyRO)
	}
	// search_tools on read-only token must not return write tools.
	roSearch, isErr := ro.tool("acnologia_search_tools", map[string]any{"query": "scrapebox"})
	if isErr {
		t.Fatalf("search_tools failed for read-only token: %v", roSearch)
	}
	for _, raw := range roSearch["tools"].([]any) {
		if raw.(map[string]any)["name"] == "scrapebox_run_scrape" {
			t.Fatalf("read-only search_tools leaked write tool: %v", roSearch)
		}
	}

	// 4. App-level MCPSettings.ReadOnly and ?read_only=1 also enforce read-only on a full token.
	var qRes map[string]any
	e.do(http.DefaultClient, "POST", "/mcp?read_only=1", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, 200, &qRes, "Authorization", "Bearer "+fullTok)
	for _, raw := range qRes["result"].(map[string]any)["tools"].([]any) {
		if raw.(map[string]any)["name"] == "scrapebox_run_scrape" {
			t.Fatal("?read_only=1 must hide write tools even for full token")
		}
	}

	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/mcp", appID), map[string]any{
		"core": true, "service_tools": true, "insights": true, "manage": true,
		"resources": true, "prompts": true, "read_only": true,
	}, 200, nil)
	fullAfterRO := full.toolNames()
	if has(fullAfterRO, "scrapebox_run_scrape") || has(fullAfterRO, "add_keys") || has(fullAfterRO, "get_key") {
		t.Fatalf("app-level read_only must hide write tools: %v", fullAfterRO)
	}
	if !has(fullAfterRO, "scrapebox_list_jobs") || !has(fullAfterRO, "list_keys") {
		t.Fatalf("app-level read_only should still show read-only tools: %v", fullAfterRO)
	}
}
