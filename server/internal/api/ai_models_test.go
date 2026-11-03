package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
)

type aiModelsResp struct {
	Models []struct {
		ID            string  `json:"id"`
		ContextWindow int     `json:"context_window"`
		InputPrice    float64 `json:"input_price"`
		OutputPrice   float64 `json:"output_price"`
		PricingKnown  bool    `json:"pricing_known"`
	} `json:"models"`
	Source    string              `json:"source"`
	Note      string              `json:"note"`
	Settings  agent.ModelSettings `json:"settings"`
	Effective map[string]struct {
		Model         string `json:"model"`
		Source        string `json:"source"`
		ContextWindow int    `json:"context_window"`
	} `json:"effective"`
	CompactTokens  int      `json:"compact_tokens"`
	CompactFromEnv bool     `json:"compact_from_env"`
	CanEdit        bool     `json:"can_edit"`
	Warnings       []string `json:"warnings"`
}

// useModelsAPI points the env's agent at a fake Anthropic server whose
// Models API lists list.
func (e *env) useModelsAPI(list string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(list))
	}))
	e.t.Cleanup(srv.Close)
	e.api.Agent = agent.New(agent.Config{APIKey: "test", BaseURL: srv.URL}, e.st, e.api.Catalog, e.api.Pool.Box)
}

func TestAIModelsRolesValidationAndAudit(t *testing.T) {
	e := newEnv(t)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	admin, member := client(), client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	e.do(member, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)

	// No API key in this env: the built in list, and the built in default.
	var got aiModelsResp
	e.do(admin, "GET", "/api/admin/ai/models", nil, 200, &got)
	if got.Source != "builtin" || got.Note == "" || len(got.Models) != 4 || got.CanEdit {
		t.Fatalf("admin view: %+v", got)
	}
	if d := got.Effective["default"]; d.Model != agent.BuiltinModel || d.Source != agent.SourceBuiltin || d.ContextWindow != 1_000_000 {
		t.Fatalf("default: %+v", d)
	}
	e.do(member, "GET", "/api/admin/ai/models", nil, 403, nil)

	body := map[string]any{"default": "claude-sonnet-5-5", "fill": "claude-haiku-5-5", "summary": "claude-haiku-5-5", "refresh": "claude-haiku-5-5"}
	e.do(admin, "PUT", "/api/admin/ai/models", body, 403, nil)
	e.do(member, "PUT", "/api/admin/ai/models", body, 403, nil)

	var saved aiModelsResp
	e.do(e.super, "PUT", "/api/admin/ai/models", body, 200, &saved)
	want := map[string][2]string{
		"default":   {"claude-sonnet-5-5", agent.SourceSaved},
		"assistant": {"claude-sonnet-5-5", agent.SourceSaved},
		"builder":   {"claude-sonnet-5-5", agent.SourceSaved},
		"fill":      {"claude-haiku-5-5", agent.SourceOverride},
		"summary":   {"claude-haiku-5-5", agent.SourceOverride},
		"refresh":   {"claude-haiku-5-5", agent.SourceOverride},
	}
	for k, w := range want {
		if c := saved.Effective[k]; c.Model != w[0] || c.Source != w[1] {
			t.Fatalf("%s: %+v want %v", k, c, w)
		}
	}
	if !saved.CanEdit || saved.Settings.UpdatedBy != "root@x.io" {
		t.Fatalf("saved: %+v", saved)
	}

	// The AI status reports the effective default.
	var st agent.Status
	e.do(member, "GET", "/api/admin/ai/status", nil, 200, &st)
	if st.Model != "claude-sonnet-5-5" || st.Models["fill"] != "claude-haiku-5-5" {
		t.Fatalf("status: %+v", st)
	}

	// Unknown and malformed ids are refused and change nothing.
	e.do(e.super, "PUT", "/api/admin/ai/models", map[string]any{"default": "claude-made-up-7"}, 400, nil)
	e.do(e.super, "PUT", "/api/admin/ai/models", map[string]any{"builder": "../etc"}, 400, nil)
	if m := e.api.Agent.ModelFor(context.Background(), "default").Model; m != "claude-sonnet-5-5" {
		t.Fatalf("a refused save changed the default to %s", m)
	}

	// One audit entry per save, with the old and the new values.
	entries, err := e.st.ListAudit(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for _, a := range entries {
		if a.Action == "ai.models" {
			details = append(details, a.Detail)
		}
	}
	if len(details) != 1 || !strings.Contains(details[0], "default: (default) -> claude-sonnet-5-5") ||
		!strings.Contains(details[0], "fill: (default) -> claude-haiku-5-5") {
		t.Fatalf("audit: %v", details)
	}

	// Back to the defaults: everything empty.
	e.do(e.super, "PUT", "/api/admin/ai/models", map[string]any{}, 200, &saved)
	if c := saved.Effective["fill"]; c.Model != agent.BuiltinModel || c.Source != agent.SourceBuiltin {
		t.Fatalf("after clearing: %+v", c)
	}
}

func TestCompactionFollowsTheAssistantModel(t *testing.T) {
	e := newEnv(t)
	e.useModelsAPI(`{"data":[{"type":"model","id":"claude-small-1","display_name":"Claude Small 1","created_at":"2026-10-01T00:00:00Z",
		"max_input_tokens":200000,"max_tokens":8000,"lifecycle":"active"}],"has_more":false}`)
	var got aiModelsResp
	e.do(e.super, "GET", "/api/admin/ai/models", nil, 200, &got)
	if got.Source != "api" || got.CompactTokens != 600_000 || got.CompactFromEnv {
		t.Fatalf("before: source %s compact %d", got.Source, got.CompactTokens)
	}
	// A model only the API lists saves with a warning, and the threshold
	// becomes 60% of its window.
	e.do(e.super, "PUT", "/api/admin/ai/models", map[string]any{"assistant": "claude-small-1"}, 200, &got)
	if len(got.Warnings) != 1 || got.CompactTokens != 120_000 || got.Effective["assistant"].ContextWindow != 200_000 {
		t.Fatalf("after: %+v", got)
	}
	if e.api.compactAt() != 120_000 {
		t.Fatalf("compactAt %d", e.api.compactAt())
	}
	var sum struct {
		Env []envSetting `json:"env"`
	}
	e.do(e.super, "GET", "/api/admin/settings", nil, 200, &sum)
	for _, s := range sum.Env {
		if s.Name == "ACNOLOGIA_CHAT_COMPACT_TOKENS" && s.Value != strconv.Itoa(120_000) {
			t.Fatalf("settings row: %+v", s)
		}
		if s.Name == "ACNOLOGIA_AI_MODEL" && (s.Set || s.Value != agent.BuiltinModel) {
			t.Fatalf("model row: %+v", s)
		}
	}
	// A fixed size from the environment wins over the model.
	e.api.Cfg.ChatCompactTokens = 50_000
	e.do(e.super, "GET", "/api/admin/ai/models", nil, 200, &got)
	if got.CompactTokens != 50_000 || !got.CompactFromEnv {
		t.Fatalf("env size: %+v", got)
	}
}
