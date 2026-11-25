package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// X-Acnologia-Meta on /mcp tags the tool calls it runs: usage and tag
// budgets count them, the header never reaches the provider, a pinned key
// sent with another value is a tag_pinned isError result, and a malformed
// header is 400 bad_meta.
func TestMCPCallerTags(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("svc", nil, "k1")
	var leaked atomic.Bool
	up.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		if r.Header.Get(metaHeader) != "" {
			leaked.Store(true)
		}
		return 200, nil, `{"ok":true}`
	}
	appID, tok := e.app(map[string]any{"name": "a"})
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/tag-budgets", appID),
		map[string]any{"budgets": []map[string]any{{"key": "customer", "daily_requests": 1}}}, 200, nil)

	res := mcpProxyCall(e, tok, "svc", "x", metaHeader, "customer=c1,workspace=w1")
	if sc, _ := res["structuredContent"].(map[string]any); sc["status"] != float64(200) {
		t.Fatalf("tagged call: %v", res)
	}
	// The budget of customer=c1 is spent; another customer goes on.
	res = mcpProxyCall(e, tok, "svc", "x", metaHeader, "customer=c1")
	if sc, _ := res["structuredContent"].(map[string]any); sc["status"] != float64(429) || !strings.Contains(fmt.Sprint(sc["body"]), "tag_limit_reached") {
		t.Fatalf("over the tag budget: %v", res)
	}
	if sc, _ := mcpProxyCall(e, tok, "svc", "x", metaHeader, "customer=c2")["structuredContent"].(map[string]any); sc["status"] != float64(200) {
		t.Fatal("another customer was refused")
	}
	var u struct {
		Tags []struct {
			Key, Value string
			Requests   int64
		} `json:"tags"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/apps/%d/usage/tags", appID), nil, 200, &u)
	got := map[string]int64{}
	for _, r := range u.Tags {
		got[r.Key+"="+r.Value] = r.Requests
	}
	if got["customer=c1"] != 1 || got["workspace=w1"] != 1 || got["customer=c2"] != 1 {
		t.Fatalf("usage by tag: %v", got)
	}
	if leaked.Load() {
		t.Fatal("X-Acnologia-Meta reached the provider")
	}

	pinned, _ := e.newToken(appID, map[string]any{"name": "cust-a", "pinned_tags": map[string]string{"customer": "cust_A"}})
	res = mcpProxyCall(e, pinned, "svc", "x", metaHeader, "customer=cust_B")
	meta, _ := res["_meta"].(map[string]any)
	info, _ := meta["acnologia/error"].(map[string]any)
	if res["isError"] != true || info["code"] != "tag_pinned" {
		t.Fatalf("retag over MCP: %v", res)
	}
	if sc, _ := mcpProxyCall(e, pinned, "svc", "x", metaHeader, "customer=cust_A")["structuredContent"].(map[string]any); sc["status"] != float64(200) {
		t.Fatal("the pinned value was refused")
	}

	var bad map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 400, &bad,
		"Authorization", "Bearer "+tok, metaHeader, "Customer=bad value")
	if fmt.Sprint(bad["error"].(map[string]any)["code"]) != "bad_meta" {
		t.Fatalf("malformed header: %v", bad)
	}
}
