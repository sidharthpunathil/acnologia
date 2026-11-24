package api

import (
	"fmt"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The app MCP's add_keys checks the new keys as a job and names it, like
// the admin add keys route.
func TestAppMCPAddKeysStartsCheckJob(t *testing.T) {
	e := newEnv(t)
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true, "manage": true}, 200, nil)
	m := &mcpClient{e: e, tok: tok}
	out, isErr := m.tool("add_keys", map[string]any{"service": "serpapi", "keys": []any{map[string]any{"secret": "mcpjobkey01"}}})
	if isErr {
		t.Fatalf("add_keys: %v", out)
	}
	id, _ := out["check_job_id"].(float64)
	if id == 0 {
		t.Fatalf("add_keys has no check_job_id: %v", out)
	}
	j := waitJob(t, e, e.super, fmt.Sprintf("/api/admin/jobs/%d", int64(id)))
	if j.Kind != "keys.check_new" || j.Status != store.JobDone || j.Via != store.ViaAppMCP {
		t.Fatalf("job %+v", j)
	}
}
