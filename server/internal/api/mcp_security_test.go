package api

import (
	"net/http"
	"testing"
)

// Bad methods and paths from an MCP client must come back as tool errors,
// not panics turned into 500s by the recoverer.
func TestMCPProxyRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "mcpkey01"})
	_, tok := e.app(map[string]any{"name": "agent"})
	auth := []string{"Authorization", "Bearer " + tok}
	for i, args := range []map[string]any{
		{"service": "serpapi", "method": "G T", "path": "/search.json"},
		{"service": "serpapi", "method": "TRACE", "path": "/search.json"},
		{"service": "serpapi", "path": "/a b\x7f"},
	} {
		var res map[string]any
		e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
			"params": map[string]any{"name": "proxy_request", "arguments": args}}, 200, &res, auth...)
		r, _ := res["result"].(map[string]any)
		if r == nil || r["isError"] != true {
			t.Errorf("case %d: want a tool error, got %v", i, res)
		}
	}
}
