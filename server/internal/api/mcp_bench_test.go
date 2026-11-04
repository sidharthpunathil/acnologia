package api

// MCP tools/call benchmarks, served in process (no network):
//
//	go test ./internal/api -run XXX -bench 'MCPCall' -benchmem -count=10

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpCatalogProviders are built in providers that ship tool examples, so
// each one adds generated service tools.
var mcpCatalogProviders = []string{"youcom", "exa", "tavily", "deepseek", "linkup", "groq", "gemini", "tinyfish-search", "perplexity",
	"elevenlabs", "jina", "anthropic", "searchapi", "openai", "firecrawl", "mistral", "together", "tinyfish-fetch", "cohere",
	"cerebras", "openrouter", "brave-search", "serpapi", "serper", "xai", "fireworks"}

const mcpBenchProxyBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"proxy_request","arguments":{"service":"gen","method":"GET","path":"/v1/small"}}}`

func (e *hpEnv) addMCPCatalog(tb testing.TB) {
	tb.Helper()
	super := client()
	e.call(tb, super, "POST", "/api/auth/login", map[string]any{"email": "root@x.io", "password": "supersecret1"}, nil)
	for _, p := range mcpCatalogProviders {
		e.call(tb, super, "POST", "/api/admin/services", map[string]any{"provider": p, "slug": p}, nil)
	}
}

func (e *hpEnv) mcpInProc(tb testing.TB, h http.Handler, body string) []byte {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		tb.Fatalf("mcp: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func benchMCPCall(b *testing.B, e *hpEnv, want string) {
	h := e.api.Router()
	if out := e.mcpInProc(b, h, mcpBenchProxyBody); !bytes.Contains(out, []byte(want)) {
		b.Fatalf("mcp answer lacks %q: %.300s", want, out)
	}
	b.ReportAllocs()
	for b.Loop() {
		e.mcpInProc(b, h, mcpBenchProxyBody)
	}
}

// BenchmarkMCPCallProxy is proxy_request with only the bench service.
func BenchmarkMCPCallProxy(b *testing.B) {
	benchMCPCall(b, newHPEnv(b), "hello")
}

// BenchmarkMCPCallProxyCatalog is the same call on a server that also has
// the catalog providers configured, so tools/list would hold every one of
// their example tools.
func BenchmarkMCPCallProxyCatalog(b *testing.B) {
	e := newHPEnv(b)
	e.addMCPCatalog(b)
	benchMCPCall(b, e, "hello")
}

// mcpBigJSON is about 150 KB of search results.
var mcpBigJSON = func() []byte {
	var sb strings.Builder
	sb.WriteString(`{"results":[`)
	for i := range 600 {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"title":"Result number %d about something","url":"https://example.com/page/%d","snippet":"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor %d","score":0.%d}`, i, i, i, i)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}()

type mcpBigJSONUpstream struct{}

func (mcpBigJSONUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(mcpBigJSON)
}

// BenchmarkMCPCallProxyBigJSON is proxy_request returning a large JSON body,
// where encoding the text content block dominates.
func BenchmarkMCPCallProxyBigJSON(b *testing.B) {
	e := newHPEnvWith(b, mcpBigJSONUpstream{}, "sk-bench-key-0000-abcdefgh", "sk-bench-key-0001-abcdefgh")
	benchMCPCall(b, e, "Result number 599")
}
