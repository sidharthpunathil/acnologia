package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSyncAndCallJSONStateful(t *testing.T) {
	var initCount, listCount, callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-tok" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/json") || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			http.Error(w, "missing Accept", http.StatusNotAcceptable)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		switch req.Method {
		case "initialize":
			initCount.Add(1)
			w.Header().Set("Mcp-Session-Id", "sess-42")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo":      map[string]any{"name": "scrapebox", "version": "1.2.3"},
					"instructions":    "Every Scrapebox action is an MCP tool.",
				},
			})
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "sess-42" {
				http.Error(w, "missing session", 400)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "sess-42" {
				http.Error(w, "missing session", 400)
				return
			}
			listCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if req.Params["cursor"] == nil {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"tools": []map[string]any{
							{
								"name":        "scrape_url",
								"title":       "Scrape URL",
								"description": "Fetch a page",
								"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}, "required": []string{"url"}},
								"annotations": map[string]any{"readOnlyHint": true},
							},
						},
						"nextCursor": "p2",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "delete_job",
							"description": "Delete a job",
							"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true},
						},
					},
				},
			})
		case "tools/call":
			callCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "scraped ok"}},
				},
			})
		}
	}))
	defer srv.Close()

	auth := func(req *http.Request) { req.Header.Set("Authorization", "Bearer secret-tok") }
	res, err := Sync(context.Background(), srv.Client(), srv.URL, auth, nil, "1.0.0")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Session.SessionID != "sess-42" || res.Session.ServerName != "scrapebox" {
		t.Fatalf("unexpected session: %+v", res.Session)
	}
	if len(res.Tools) != 2 || res.Tools[0].Name != "scrape_url" || res.Tools[1].Name != "delete_job" {
		t.Fatalf("unexpected tools: %+v", res.Tools)
	}
	if listCount.Load() != 2 {
		t.Fatalf("expected 2 paginated list calls, got %d", listCount.Load())
	}

	callRes, err := CallTool(context.Background(), srv.Client(), &res.Session, auth, nil, "scrape_url", json.RawMessage(`{"url":"https://example.com"}`), 1<<20)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(callRes.Content) != 1 || callRes.IsError {
		t.Fatalf("unexpected call result: %+v", callRes)
	}
	if callCount.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", callCount.Load())
	}
}

func TestSSEResponseAndTrailingSlashRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			w.Header().Set("Location", "/mcp/")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Emit a notification first, then the real response matching id.
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		if req.Method == "initialize" {
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"protocolVersion\":\"2025-06-18\",\"serverInfo\":{\"name\":\"sse-srv\",\"version\":\"2.0\"}}}\n\n", req.ID)
			return
		}
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"tools\":[{\"name\":\"ping_tool\",\"description\":\"Ping\"}]}}\n\n", req.ID)
	}))
	defer srv.Close()

	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	res, err := Sync(context.Background(), client, srv.URL+"/mcp", nil, nil, "1.0")
	if err != nil {
		t.Fatalf("Sync with SSE + 307 trailing slash failed: %v", err)
	}
	if !strings.HasSuffix(res.Session.Endpoint, "/mcp/") {
		t.Fatalf("expected endpoint updated to /mcp/, got %q", res.Session.Endpoint)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "ping_tool" {
		t.Fatalf("unexpected tools: %+v", res.Tools)
	}
}

func TestCrossOriginRedirectBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example.com/mcp/")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	_, hres, err := Initialize(context.Background(), client, srv.URL+"/mcp", nil, nil, "1.0")
	if err == nil || hres.HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 error, got status=%d err=%v", hres.HTTPStatus, err)
	}
}
