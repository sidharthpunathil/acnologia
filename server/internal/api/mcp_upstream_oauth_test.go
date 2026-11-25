package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/mcpclient"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// oauthMCPServer is an upstream MCP server that takes the fake provider's
// access tokens. A token it does not know is refused with HTTP 401, or,
// with rpcAuth set, with HTTP 200 and a JSON-RPC error, as some servers do.
func oauthMCPServer(t *testing.T, f *fakeOAuth, rpcAuth *atomic.Bool, sent *atomic.Value) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sent.Store(tok)
		f.mu.Lock()
		live := f.access[tok]
		f.mu.Unlock()
		reply := func(env map[string]any) {
			env["jsonrpc"], env["id"] = "2.0", req.ID
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(env)
		}
		if !live {
			if rpcAuth.Load() && len(req.ID) > 0 {
				reply(map[string]any{"error": map[string]any{"code": -32001, "message": "Unauthorized: invalid or expired token"}})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch req.Method {
		case "initialize":
			reply(map[string]any{"result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "fake-mcp", "version": "1"}}})
		case "tools/list":
			reply(map[string]any{"result": map[string]any{"tools": []map[string]any{{"name": "whoami", "description": "Who the token is.",
				"inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}}}})
		case "tools/call":
			reply(map[string]any{"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "you are ana"}}}})
		default:
			reply(map[string]any{"error": map[string]any{"code": -32601, "message": "no such method"}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpstreamMCPRefreshesOAuthTokens(t *testing.T) {
	o := newOAuthEnv(t)
	f := o.f
	o.setClient()
	var rpcAuth atomic.Bool
	var sent atomic.Value
	mcpSrv := oauthMCPServer(t, f, &rpcAuth, &sent)
	o.do(o.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", o.svc), map[string]any{"mcp_url": mcpSrv.URL + "/mcp"}, 200, nil)
	keyID := o.connect(t, o.path("/oauth/connect"), nil).KeyID

	// A sync whose token was revoked early refreshes it once and syncs.
	cur, _ := o.stored(t, keyID)
	f.kill(cur)
	before := f.refreshes.Load()
	o.do(o.super, "POST", o.path("/mcp/sync"), map[string]any{}, 200, nil)
	if f.refreshes.Load() != before+1 {
		t.Fatalf("sync did not refresh: %d", f.refreshes.Load()-before)
	}

	_, tok := o.app(map[string]any{"name": "mcp-app", "mode": "both"})
	m := &mcpClient{e: o.env, tok: tok}
	call := func() (map[string]any, bool) {
		t.Helper()
		res := m.call("tools/call", map[string]any{"name": "fakeo_whoami", "arguments": map[string]any{}})
		if res["error"] != nil {
			t.Fatalf("rpc error: %v", res["error"])
		}
		r := res["result"].(map[string]any)
		return r, r["isError"] == true
	}
	if r, isErr := call(); isErr {
		t.Fatalf("first call failed: %v", r)
	}

	// HTTP 401 for a revoked access token: one refresh, one retry on the
	// same key, and the call succeeds with the new token.
	for _, viaRPC := range []bool{false, true} {
		rpcAuth.Store(viaRPC)
		cur, _ = o.stored(t, keyID)
		f.kill(cur)
		before = f.refreshes.Load()
		if r, isErr := call(); isErr {
			t.Fatalf("rpc auth %v: call failed: %v", viaRPC, r)
		}
		now, _ := o.stored(t, keyID)
		if f.refreshes.Load() != before+1 || now == cur || sent.Load() != now {
			t.Fatalf("rpc auth %v: refreshes %d, sent %v, stored %q", viaRPC, f.refreshes.Load()-before, sent.Load(), now)
		}
	}

	// The grant is gone: the refresh is refused with invalid_grant, the key
	// is marked invalid and the call fails.
	f.mu.Lock()
	f.refresh = map[string]bool{}
	f.mu.Unlock()
	cur, _ = o.stored(t, keyID)
	f.kill(cur)
	if _, isErr := call(); !isErr {
		t.Fatal("a call with a dead grant succeeded")
	}
	k, _ := o.st.GetKey(context.Background(), keyID)
	if k.Status != store.StatusInvalid {
		t.Fatalf("key status %s, want invalid", k.Status)
	}
}

// A JSON-RPC auth error on HTTP 200 marks an API key like a 401 and the
// call moves to the next key.
func TestUpstreamMCPRPCAuthErrorRotates(t *testing.T) {
	e := newEnv(t)
	good, bad := "good-upstream-mcp-token-0001", "revoked-upstream-mcp-token-02"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		env := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch {
		case r.Header.Get("Authorization") != "Bearer "+good:
			env["error"] = map[string]any{"code": -32600, "message": "Authentication failed: invalid API key"}
		case req.Method == "initialize":
			env["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "x"}}
		case req.Method == "tools/list":
			env["result"] = map[string]any{"tools": []map[string]any{{"name": "ping", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}}}
		default:
			env["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer srv.Close()
	var svc map[string]any
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "mcp", "slug": "rpcauth", "name": "RPC auth", "mcp_url": srv.URL + "/mcp"}, 201, &svc)
	id := int(svc["id"].(float64))
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", id), map[string]any{"keys": []map[string]any{{"label": "good", "secret": good}}}, 201, nil)
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/mcp/sync", id), map[string]any{}, 200, nil)
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", id), map[string]any{"keys": []map[string]any{{"label": "bad", "secret": bad}}}, 201, nil)

	_, tok := e.app(map[string]any{"name": "rot", "mode": "both"})
	m := &mcpClient{e: e, tok: tok}
	for i := 0; i < 4; i++ {
		res := m.call("tools/call", map[string]any{"name": "rpcauth_ping", "arguments": map[string]any{}})
		if r, _ := res["result"].(map[string]any); r == nil || r["isError"] == true {
			t.Fatalf("call %d failed: %v", i, res)
		}
	}
	keys, _ := e.st.ListKeys(context.Background(), int64(id), true)
	for _, k := range keys {
		// One app's word is not enough to take a key out (pool/corroborate.go),
		// so the key is marked as an unconfirmed invalid, as a 401 would be.
		if k.Label == "bad" && k.Status != store.StatusInvalid && !strings.Contains(k.LastError, "invalid") {
			t.Fatalf("revoked key is %s (%q), want it judged invalid", k.Status, k.LastError)
		}
		if k.Label == "good" && k.Status != store.StatusActive {
			t.Fatalf("good key is %s", k.Status)
		}
	}
}

func TestMCPAuthRPCError(t *testing.T) {
	for msg, want := range map[string]bool{
		"Unauthorized":                            true,
		"Authentication required":                 true,
		"invalid_token: the access token expired": true,
		"Invalid API key provided":                true,
		"Request timed out":                       false,
		"Unauthorized: missing scope repo:write":  false,
		"permission denied for this resource":     false,
		"Tool not found":                          false,
	} {
		if got := mcpAuthRPCError(&mcpclient.RPCError{Code: -32001, Message: msg}); got != want {
			t.Errorf("%q: got %v want %v", msg, got, want)
		}
	}
	if mcpAuthRPCError(errors.New("unauthorized")) {
		t.Error("a plain error is not a JSON-RPC auth error")
	}
	if !mcpUnauthorized(401, nil) || mcpUnauthorized(0, &mcpclient.RPCError{Message: "unauthorized"}) || mcpUnauthorized(403, nil) {
		t.Error("mcpUnauthorized status handling")
	}
}
