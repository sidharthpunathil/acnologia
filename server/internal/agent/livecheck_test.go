package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// draftJSON is a complete provider draft built from SerpAPI's spec, with
// mut applied to the spec.
func draftJSON(t *testing.T, a *Agent, slug string, mut func(spec map[string]any)) []byte {
	t.Helper()
	p, err := a.Store.GetProvider(context.Background(), "serpapi")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(p.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(spec)
	}
	raw, _ := json.Marshal(map[string]any{"provider": map[string]any{
		"slug": slug, "name": "Search Co", "category": "Search", "description": "Search as JSON.",
		"website": "", "docs_url": "", "color": "#123456", "spec": spec,
	}, "summary": "A search API."})
	return raw
}

func TestLiveCheckRefusesSpecsThatFailTheSchema(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	run, err := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "running",
		Input: store.AgentInput{URLs: []string{"https://docs.example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(map[string]any){
		"dot health path": func(s map[string]any) {
			s["base_url"] = "https://api.example.com"
			s["health"].(map[string]any)["path"] = ".attacker.test/c"
		},
		"userinfo health path": func(s map[string]any) {
			s["base_url"] = "https://api.example.com"
			s["health"].(map[string]any)["path"] = "@attacker.test/c"
		},
		"delete health": func(s map[string]any) {
			s["base_url"] = "https://api.example.com"
			s["health"].(map[string]any)["method"] = "DELETE"
		},
		"userinfo example path": func(s map[string]any) {
			s["base_url"] = "https://api.example.com"
			s["examples"] = []any{map[string]any{"name": "x", "method": "GET", "path": "@attacker.test/c", "safe": true}}
		},
	} {
		s := &runState{run: run, testKey: "sk-test"}
		out, isErr, _ := a.runTool(ctx, s, "live_check", draftJSON(t, a, "searchco", mut))
		if !isErr || !strings.Contains(out, "schema problems") || s.live != nil || s.liveChecks != 0 {
			t.Errorf("%s: live check ran (isErr=%v live=%v): %s", name, isErr, s.live != nil, out)
		}
	}
}

func TestUnpinnedRunSkipsLiveCheckAndWaitsForReview(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	run, err := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "running",
		Input: store.AgentInput{Context: "Search Co", AutoPublish: true, CreateService: true, HasTestKey: true}})
	if err != nil {
		t.Fatal(err)
	}
	s := &runState{run: run, testKey: "sk-test"}
	raw := draftJSON(t, a, "searchco", func(spec map[string]any) { spec["base_url"] = "https://evil.example" })
	if out, isErr, _ := a.runTool(ctx, s, "live_check", raw); !isErr || s.live != nil {
		t.Fatalf("live check ran with nothing to pin to: %s", out)
	}
	out, isErr, done := a.runTool(ctx, s, "submit_provider", raw)
	if isErr || !done || s.accepted == nil || !s.unpinned || s.live != nil {
		t.Fatalf("submit: isErr=%v done=%v unpinned=%v live=%v: %s", isErr, done, s.unpinned, s.live != nil, out)
	}
	a.finish(ctx, s)
	got, _ := a.Store.GetRun(ctx, run.ID)
	if got.Status != "needs_review" {
		t.Fatalf("status %s, want needs_review", got.Status)
	}
	if _, err := a.Store.GetProvider(ctx, "searchco"); err == nil {
		t.Fatal("an unpinned run published")
	}
	svcs, _ := a.Store.ListServices(ctx)
	for _, sv := range svcs {
		if strings.Contains(sv.BaseURL, "evil.example") {
			t.Fatal("a service was created at the unvouched host")
		}
	}
}
