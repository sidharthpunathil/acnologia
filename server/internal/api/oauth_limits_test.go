package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// mcpProxyCall runs proxy_request over /mcp with a bearer token and extra
// header pairs, and returns the tools/call result.
func mcpProxyCall(e *env, tok, service, path string, hdr ...string) map[string]any {
	e.t.Helper()
	var res map[string]any
	h := append([]string{"Authorization", "Bearer " + tok}, hdr...)
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "proxy_request", "arguments": map[string]any{"service": service, "path": path}}}, 200, &res, h...)
	r, _ := res["result"].(map[string]any)
	return r
}

// Each OAuth grant gets the app's per-token default as a cap of its own:
// N concurrent calls on one grant capped at M reach the upstream exactly M
// times, a second grant has its own M, and nothing stays reserved.
func TestOAuthGrantTokenDefaultConcurrency(t *testing.T) {
	e, s := newFeatureEnv(t)
	up, _ := e.upstream("slow", nil, "k1")
	block := make(chan struct{})
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		<-block
		return 200, nil, `{"ok":true}`
	}
	appID, _ := e.app(map[string]any{"name": "claude"})
	const n, m = 20, 4
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/apps/%d/token-limits", appID), map[string]any{"limits": map[string]any{"daily_requests": m}}, 200, nil)
	f := registerClient(t, e)
	first := f.connect(t, map[string]any{"app_id": appID})["access_token"].(string)

	done := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mcpProxyCall(e, first, "slow", "q")
			done <- struct{}{}
		}()
	}
	// The refused calls answer while the admitted ones wait on the upstream.
	for i := 0; i < n-m; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			close(block)
			t.Fatalf("refused calls are waiting, %d upstream calls", up.total.Load())
		}
	}
	close(block)
	wg.Wait()
	if got := int(up.total.Load()); got != m {
		t.Fatalf("upstream calls %d, want exactly %d", got, m)
	}
	res := mcpProxyCall(e, first, "slow", "q")
	sc, _ := res["structuredContent"].(map[string]any)
	if sc["status"] != float64(429) || !strings.Contains(fmt.Sprint(sc["body"]), "token_limit_reached") || int(up.total.Load()) != m {
		t.Fatalf("over the grant's cap: %v, %d upstream calls", res, up.total.Load())
	}
	// Another sign in is another grant, with a cap of its own.
	second := f.connect(t, map[string]any{"app_id": appID})["access_token"].(string)
	mcpProxyCall(e, second, "slow", "q")
	if got := int(up.total.Load()); got != m+1 {
		t.Fatalf("second grant: upstream calls %d, want %d", got, m+1)
	}
	if msg := admissionsHeld(s, appID); msg != "" {
		t.Fatal(msg)
	}
	var grants int
	if err := e.st.DB.QueryRow(`SELECT COUNT(DISTINCT grant_id) FROM oauth_grant_usage WHERE app_id=?`, appID).Scan(&grants); err != nil || grants != 2 {
		t.Fatalf("grant usage rows for %d grants (%v), want 2", grants, err)
	}
}
