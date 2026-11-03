package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func TestGuideGapsAndCheckGuide(t *testing.T) {
	spec := &providers.Spec{BaseURL: "https://api.x.io", Auth: providers.Auth{Type: "bearer"}}
	if g := GuideGaps(spec); len(g) != 1 || !strings.Contains(g[0], "guide is missing") {
		t.Fatalf("no guide: %v", g)
	}
	spec.Guide = &providers.Guide{Summary: "x"}
	if g := GuideGaps(spec); len(g) != 1 || !strings.Contains(g[0], "no endpoints") {
		t.Fatalf("thin guide: %v", g)
	}
	var res GuideResult
	if msg, ok := checkGuide(spec, []byte(`{"verified":true,"guide":{"summary":"x"}}`), &res); ok || !strings.Contains(msg, "no endpoints") {
		t.Fatalf("thin guide accepted: %s", msg)
	}
	if msg, ok := checkGuide(spec, []byte(`{"verified":true,"guide":{"summary":"x","endpoints":[{"name":"S","method":"FETCH","path":"/s"}]}}`), &res); ok || !strings.Contains(msg, "method must be") {
		t.Fatalf("bad method accepted: %s", msg)
	}
	if _, ok := checkGuide(spec, []byte(`{"verified":true,"summary":"read","guide":{"summary":"x","endpoints":[{"name":"S","method":"GET","path":"/s"}]}}`), &res); !ok || res.Guide == nil || !res.Verified {
		t.Fatalf("good guide: %+v", res)
	}
	if _, ok := checkGuide(spec, []byte(`{"verified":false,"summary":"could not read"}`), &res); !ok || res.Guide != nil || res.Verified {
		t.Fatalf("unverified: %+v", res)
	}
}

// The builder must write a guide: a submit without one is sent back, and
// the guide it then writes is what gets published.
func TestBuilderRequiresAndPublishesTheGuide(t *testing.T) {
	spec := `{"base_url":"http://127.0.0.1:9","auth":{"type":"query","name":"key"},"use_standard_rules":true,` +
		`"examples":[{"name":"Echo","method":"GET","path":"/echo","query":{"q":"hi"}}],` +
		`"tests":[{"name":"ok","status":200,"body":"{}","expect":"ok"},{"name":"429","status":429,"expect":"rate_limited"}]`
	guide := `,"guide":{"summary":"Echoes text.","endpoints":[{"name":"Echo","method":"GET","path":"/echo","description":"Echo q back.",` +
		`"params":[{"name":"q","in":"query","type":"string","required":true,"description":"Text"}],"response":"{q}"}],"notes":["Max 1 KB."]}`
	provider := func(spec string) string {
		return `{"slug":"echo","name":"Echo","category":"Test","description":"d","website":"http://127.0.0.1:9","docs_url":"http://127.0.0.1:9/docs","color":"#000000","spec":` + spec + `}`
	}
	f := &recordingAnthropic{fallback: `{"content":[{"type":"text","text":"{\"title\":\"Echo\",\"description\":\"Echo API\"}"}],"stop_reason":"end_turn"}`}
	f.replies = []string{
		`{"content":[{"type":"tool_use","id":"toolu_1","name":"submit_provider","input":{"summary":"s","provider":` + provider(spec+`}`) + `}}],"stop_reason":"tool_use"}`,
		`{"content":[{"type":"tool_use","id":"toolu_2","name":"submit_provider","input":{"summary":"s","provider":` + provider(spec+guide+`}`) + `}}],"stop_reason":"tool_use"}`,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a, u := newLiveAgent(t, srv.URL)
	ctx := context.Background()
	no := false
	run, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", URLs: []string{"http://127.0.0.1:9/docs"}, CreateService: &no})
	if err != nil {
		t.Fatal(err)
	}
	var got *store.AgentRun
	for i := 0; i < 1000; i++ {
		got, _ = a.Store.GetRun(ctx, run.ID)
		if got.Status == "published" || got.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Status != "published" {
		t.Fatalf("run %s: %s", got.Status, got.Error)
	}
	if sent := f.all(); !strings.Contains(sent, "guide is missing") {
		t.Fatal("the first submit was not sent back for its missing guide")
	}
	p, err := a.Store.GetProvider(ctx, "echo")
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := providers.DecodeSpec(p.Spec)
	if sp.Guide == nil || len(sp.Guide.Endpoints) != 1 || sp.Guide.Endpoints[0].Params[0].Name != "q" {
		t.Fatalf("published guide: %+v", sp.Guide)
	}
}

// Fill with AI on the guide form returns fields that drop into a spec: the
// example body string comes back as JSON.
func TestAssistFillsAGuide(t *testing.T) {
	fields := `{\"fields\":{\"summary\":\"Echo.\",\"notes\":[],\"endpoints\":[{\"name\":\"Echo\",\"method\":\"POST\",\"path\":\"/echo\",\"description\":\"d\",\"params\":[],\"body\":\"{\\\"q\\\":\\\"hi\\\"}\",\"response\":\"r\"},{\"name\":\"Ping\",\"method\":\"GET\",\"path\":\"/ping\",\"description\":\"d\",\"params\":[],\"body\":\"\",\"response\":\"r\"}]},\"notes\":\"\"}`
	f := &recordingAnthropic{fallback: `{"content":[{"type":"text","text":"` + fields + `"}],"stop_reason":"end_turn"}`}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a, u := newLiveAgent(t, srv.URL)
	res, err := a.Assist(context.Background(), u, "guide", "write the guide", map[string]any{"provider": "serpapi"})
	if err != nil {
		t.Fatal(err)
	}
	eps := res.Fields["endpoints"].([]any)
	body, ok := eps[0].(map[string]any)["body"].(map[string]any)
	if !ok || body["q"] != "hi" {
		t.Fatalf("body: %#v", eps[0])
	}
	if _, has := eps[1].(map[string]any)["body"]; has {
		t.Fatal("an empty body should be dropped")
	}
	if sent := f.all(); !strings.Contains(sent, "serpapi.com") || !strings.Contains(sent, "Never describe auth") {
		t.Fatalf("the request lacks the provider context: %s", sent)
	}
}
