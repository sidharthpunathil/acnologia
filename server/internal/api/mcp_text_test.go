package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The tools/call text block is compact JSON carrying the same data as
// structuredContent.
func TestMCPToolTextIsCompactJSON(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "mcptext01"})
	_, tok := e.app(map[string]any{"name": "agent"})
	m := &mcpClient{e: e, tok: tok}
	res := m.call("tools/call", map[string]any{"name": "list_services", "arguments": map[string]any{}})
	r := res["result"].(map[string]any)
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(text, "\n") {
		t.Fatalf("text block is not compact: %q", text)
	}
	var got any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("text block is not JSON: %v", err)
	}
	if !reflect.DeepEqual(got, r["structuredContent"]) {
		t.Fatalf("text %v differs from structuredContent %v", got, r["structuredContent"])
	}
}
