package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// An OAuth call has no app token id; its audit rows name the grant in the
// client instead, so they can be told apart and found.
func TestOAuthAuditNamesTheGrant(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "claude"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true, "manage": true}, 200, nil)
	f := registerClient(t, e)
	tok := f.connect(t, map[string]any{"app_id": appID})
	var conns struct {
		Connections []map[string]any `json:"connections"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/oauth/apps/%d/connections", appID), nil, 200, &conns)
	if len(conns.Connections) != 1 {
		t.Fatalf("connections: %v", conns)
	}
	grant, _ := conns.Connections[0]["id"].(string)
	if grant == "" {
		t.Fatalf("no grant id: %v", conns.Connections[0])
	}
	m := &mcpClient{e: e, tok: tok["access_token"].(string)}
	if out, isErr := m.tool("add_keys", map[string]any{"service": "serpapi", "keys": []any{map[string]any{"secret": "oauthkey001"}}}); isErr {
		t.Fatalf("add_keys over OAuth: %v", out)
	}
	var audit struct {
		Audit []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit?via=oauth&client="+oauthGrantPrefix+grant+"&action=key.create", nil, 200, &audit)
	if len(audit.Audit) != 1 {
		t.Fatalf("audit rows for the grant: %+v", audit.Audit)
	}
	a := audit.Audit[0]
	if !strings.HasPrefix(a.Client, oauthGrantPrefix+grant+" ") || a.TokenID != nil {
		t.Fatalf("audit row %+v", a)
	}
	// An app token call keeps its plain client and its token id.
	plainID, appTok := e.app(map[string]any{"name": "plain"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(plainID))+"/mcp", map[string]any{"core": true, "manage": true}, 200, nil)
	if out, isErr := (&mcpClient{e: e, tok: appTok}).tool("add_keys", map[string]any{"service": "serpapi", "keys": []any{map[string]any{"secret": "plainkey001"}}}); isErr {
		t.Fatalf("add_keys: %v", out)
	}
	e.do(e.super, "GET", "/api/admin/audit?via=app_mcp&action=key.create", nil, 200, &audit)
	if len(audit.Audit) != 1 || strings.HasPrefix(audit.Audit[0].Client, oauthGrantPrefix) || audit.Audit[0].TokenID == nil {
		t.Fatalf("app token audit row %+v", audit.Audit)
	}
}
