package api

import (
	"context"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// The Assistant changes a provider's metadata and spec by chat, as a new
// version, and asks before a change that moves where keys go.
func TestAssistantUpdatesAProvider(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	before, err := e.st.GetProvider(ctx, "serpapi")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "update_provider", `{"slug":"serpapi","name":"SerpAPI Search","description":"Google results as JSON.","category":"Search","spec_patch":{"defaults":{"cache_ttl_sec":600}}}`),
		toolUse("tu_2", "update_provider", `{"slug":"serpapi","spec_patch":{"auth":{"type":"header","name":"X-Key"}}}`),
		text("Renamed it. Press Confirm for the auth change."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "rename serpapi and cache for 10 minutes"}, 200, &out)

	p, _ := e.st.GetProvider(ctx, "serpapi")
	if p.Name != "SerpAPI Search" || p.Description != "Google results as JSON." || p.Version != before.Version+1 {
		t.Fatalf("metadata not published as a new version: %+v", p)
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil || spec.Defaults.CacheTTLSec != 600 {
		t.Fatalf("spec patch not applied: %+v %v", spec.Defaults, err)
	}
	// The merge patch kept the rest of the spec.
	if spec.Auth.Type == "" || len(spec.Tests) == 0 {
		t.Fatal("the spec patch dropped fields it did not name")
	}
	pend := pendingIn(out)
	c, ok := pend["update_provider"]
	if !ok || !strings.Contains(c.Text, "change auth") {
		t.Fatalf("the auth change ran without a confirmation: %+v", out.Items)
	}
	if spec.Auth.Name == "X-Key" {
		t.Fatal("auth changed before confirm")
	}
}

func TestMergePatch(t *testing.T) {
	got := asJSON(mergePatch(map[string]any{"a": 1.0, "b": map[string]any{"c": 2.0, "d": 3.0}, "e": []any{1.0}},
		map[string]any{"b": map[string]any{"c": nil, "x": 4.0}, "e": []any{2.0, 3.0}, "f": "new"}))
	if got != `{"a":1,"b":{"d":3,"x":4},"e":[2,3],"f":"new"}` {
		t.Fatal(got)
	}
}

// The Assistant builds a capability and tests it for real, as its own app.
func TestAssistantBuildsAndTriesACapability(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "capkey01"})
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "save_capability", `{"slug":"web-search","name":"Web search","strategy":"cheapest",
			"params":[{"name":"query","type":"string","required":true}],
			"routes":[{"service":"serpapi","path":"/search.json","query":{"q":"{{query}}"},"output":{"fields":{"query":"q"}}}]}`),
		toolUse("tu_2", "try_capability", `{"slug":"web-search","params":{"query":"coffee"}}`),
		text("It works."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "one web search name for my apps"}, 200, &out)

	c, err := e.st.GetCapabilityBySlug(context.Background(), "web-search")
	if err != nil || c.Strategy != "cheapest" || len(c.Routes) != 1 {
		t.Fatalf("capability not saved: %+v %v", c, err)
	}
	var tried *chatItem
	for i, it := range out.Items {
		if it.Kind == "tool" && it.Tool == "try_capability" {
			tried = &out.Items[i]
		}
	}
	if tried == nil || !tried.OK || !strings.Contains(tried.Summary, "200 from serpapi") {
		t.Fatalf("try_capability: %+v", out.Items)
	}
	// It ran as the Assistant app, which now shows on the Apps page.
	apps, _ := e.st.ListApps(context.Background())
	found := false
	for _, a := range apps {
		found = found || a.Name == "Assistant"
	}
	if !found {
		t.Fatal("no Assistant app")
	}
}
