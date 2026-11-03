package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

var testGuide = &providers.Guide{
	Summary: "Google results as JSON.",
	Endpoints: []providers.GuideEndpoint{{Name: "Google search", Method: "GET", Path: "/search.json", Description: "Run a search.",
		Params:   []providers.GuideParam{{Name: "q", In: "query", Type: "string", Required: true, Description: "Search terms"}},
		Response: "organic_results[] with title and link"}},
	Notes: []string{"Cached searches are free."},
}

// generateGuide calls the handler as the setup superadmin. The route itself
// is registered in server.go with the admin routes.
func (e *env) generateGuide(slug, body string) (int, guideGenOut) {
	e.t.Helper()
	u, err := e.st.GetUserByEmail(context.Background(), "root@x.io")
	if err != nil {
		e.t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Post("/api/admin/providers/{slug}/guide/generate", func(w http.ResponseWriter, req *http.Request) {
		e.api.generateProviderGuide(w, req.WithContext(context.WithValue(req.Context(), userKey, u)))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/providers/"+slug+"/guide/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var out guideGenOut
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestGenerateProviderGuideSavesANewVersion(t *testing.T) {
	e := newEnv(t)
	f := e.useFakeClaude()
	f.guide = testGuide
	ctx := context.Background()
	before, _ := e.st.GetProvider(ctx, "serpapi")

	code, out := e.generateGuide("serpapi", `{"instruction":"focus on Google search"}`)
	if code != 200 || !out.Updated || !out.Verified {
		t.Fatalf("generate: %d %+v", code, out)
	}
	if out.Provider.Version != before.Version+1 || out.Guide == nil || out.Guide.Endpoints[0].Name != "Google search" {
		t.Fatalf("result: %+v", out)
	}
	if !strings.Contains(out.Markdown, "Google search") || !strings.Contains(out.Markdown, "/proxy/") {
		t.Fatalf("markdown: %s", out.Markdown)
	}
	// The spec keeps everything else and gains the guide; the version is
	// labelled ai while the provider stays built in.
	p, _ := e.st.GetProvider(ctx, "serpapi")
	spec, _ := providers.DecodeSpec(p.Spec)
	if spec.Guide == nil || spec.Guide.Summary != testGuide.Summary || len(spec.Offers) == 0 || len(spec.Rules) == 0 {
		t.Fatalf("saved spec: %+v", spec)
	}
	if live, _ := providers.Lookup("serpapi"); live.Guide == nil {
		t.Fatal("the live registry did not get the guide")
	}
	vs, _ := e.st.ProviderVersions(ctx, "serpapi")
	if vs[0].Source != "ai" || vs[0].CreatedBy != "root@x.io" || p.Source != before.Source || !p.Builtin {
		t.Fatalf("version %+v, provider source %s", vs[0], p.Source)
	}
	// The builder's model is used, and the operator's note reaches it.
	if m := f.asked(); len(m) != 1 || m[0] != e.api.Agent.ModelFor(ctx, agent.FeatureBuilder).Model {
		t.Fatalf("models asked: %v", m)
	}
	if !strings.Contains(strings.Join(f.bodies, ""), "focus on Google search") {
		t.Fatal("the instruction did not reach the model")
	}
}

func TestGenerateProviderGuideUnverifiedChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.useFakeClaude() // no guide: the model says it could not read the docs
	before, _ := e.st.GetProvider(context.Background(), "serpapi")
	code, out := e.generateGuide("serpapi", "")
	if code != 200 || out.Updated || out.Verified || out.Provider.Version != before.Version {
		t.Fatalf("generate: %d %+v", code, out)
	}
}

func TestGenerateProviderGuideNeedsAIAndAProvider(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.generateGuide("serpapi", ""); code != 503 {
		t.Fatalf("without AI: %d", code)
	}
	e.useFakeClaude()
	if code, _ := e.generateGuide("nope", ""); code != 404 {
		t.Fatalf("unknown provider: %d", code)
	}
}

func TestAssistantReadsAndWritesGuides(t *testing.T) {
	e := newEnv(t)
	f := e.useFakeClaude()
	f.guide = testGuide
	chat := &fakeClaude{replies: []string{
		toolUse("tu_1", "provider_guide", `{"provider":"serpapi"}`),
		toolUse("tu_2", "write_provider_guide", `{"provider":"serpapi"}`),
		toolUse("tu_3", "provider_guide", `{"provider":"serpapi"}`),
		text("done"),
	}}
	e.api.chatModel = chat.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "write the serpapi guide"}, 200, &out)
	tools := 0
	for _, it := range out.Items {
		if it.Kind == "tool" {
			tools++
			if !it.OK {
				t.Fatalf("%s failed: %s", it.Tool, it.Summary)
			}
		}
	}
	if tools != 3 {
		t.Fatalf("ran %d tools", tools)
	}
	sent := strings.Join(chat.sent, "\n")
	// SerpAPI ships a written guide, so no read is the generated fallback
	// (the renderer tests cover that).
	for _, want := range []string{`\"updated\":true`, `\"endpoints\":1`, `\"generated\":false`, "Google search"} {
		if !strings.Contains(sent, want) {
			t.Errorf("tool results lack %q", want)
		}
	}

	// A member reads guides but is not offered the writer.
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	chat2 := &fakeClaude{}
	e.api.chatModel = chat2.converse
	e.do(m, "POST", "/api/admin/chat/threads/"+itoa(int(e.newChat(m)))+"/messages", map[string]any{"text": "hi"}, 200, nil)
	offered := strings.Join(chat2.tools[0], ",")
	if !strings.Contains(offered, "provider_guide") || strings.Contains(offered, "write_provider_guide") {
		t.Fatalf("member tools: %s", offered)
	}
}
