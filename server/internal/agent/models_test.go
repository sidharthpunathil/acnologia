package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// fakeClaude is an Anthropic endpoint that records the model of every
// Messages call and serves a Models API list (or fails it).
type fakeClaude struct {
	mu     sync.Mutex
	models []string // model of each Messages call, with its system prompt's start
	// list is the /v1/models answer; empty fails the call with a 500.
	list      string
	listCalls atomic.Int32
}

func (f *fakeClaude) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/v1/models") {
		f.listCalls.Add(1)
		if f.list == "" {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"down"}}`))
			return
		}
		_, _ = w.Write([]byte(f.list))
		return
	}
	b, _ := io.ReadAll(r.Body)
	var req struct {
		Model string `json:"model"`
		Tools []any  `json:"tools"`
	}
	_ = json.Unmarshal(b, &req)
	f.mu.Lock()
	f.models = append(f.models, req.Model)
	f.mu.Unlock()
	// A refusal ends every caller at once: a builder run fails, Fill with
	// AI returns an error, the others return the message.
	_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"` + req.Model + `",
		"content":[{"type":"text","text":"no"}],"stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":1}}`))
}

func (f *fakeClaude) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.models) == 0 {
		return ""
	}
	return f.models[len(f.models)-1]
}

func (f *fakeClaude) reset() {
	f.mu.Lock()
	f.models = nil
	f.mu.Unlock()
}

func (f *fakeClaude) saw(model string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.models {
		if m == model {
			return true
		}
	}
	return false
}

func newModelAgent(t *testing.T, cfg Config) (*Agent, *store.User) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	u, err := st.CreateUser(context.Background(), "op@x.io", "Op", "hash", store.RoleSuperadmin)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, st, cat, box), u
}

func fakeServer(t *testing.T, f *fakeClaude) string {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestModelPrecedence(t *testing.T) {
	ctx := context.Background()
	a, _ := newModelAgent(t, Config{})
	if c := a.ModelFor(ctx, FeatureFill); c != (ModelChoice{BuiltinModel, SourceBuiltin}) {
		t.Fatalf("nothing set: %+v", c)
	}

	a, u := newModelAgent(t, Config{Model: "claude-sonnet-5-5"})
	if c := a.ModelFor(ctx, FeatureFill); c != (ModelChoice{"claude-sonnet-5-5", SourceEnv}) {
		t.Fatalf("env: %+v", c)
	}
	if _, _, _, err := a.SaveModels(ctx, ModelSettings{Default: "claude-fable-5-1", Fill: "claude-haiku-5-5"}, u.Email); err != nil {
		t.Fatal(err)
	}
	eff := a.EffectiveModels(ctx)
	want := map[string]ModelChoice{
		"default":        {"claude-fable-5-1", SourceSaved},
		FeatureFill:      {"claude-haiku-5-5", SourceOverride},
		FeatureAssistant: {"claude-fable-5-1", SourceSaved},
		FeatureBuilder:   {"claude-fable-5-1", SourceSaved},
		FeatureSummary:   {"claude-fable-5-1", SourceSaved},
		FeatureRefresh:   {"claude-fable-5-1", SourceSaved},
	}
	for k, w := range want {
		if eff[k] != w {
			t.Fatalf("%s: got %+v want %+v", k, eff[k], w)
		}
	}
	// A background worker resolves with no request.
	if c := a.ModelFor(context.Background(), FeatureRefresh); c.Model != "claude-fable-5-1" {
		t.Fatalf("refresh: %+v", c)
	}
	if a.Status().Model != "claude-fable-5-1" || a.Status().Models[FeatureFill] != "claude-haiku-5-5" {
		t.Fatalf("status: %+v", a.Status())
	}

	// Clearing the saved default falls back to the environment again, and
	// the setting survives a restart (a new Agent on the same store).
	if _, _, _, err := a.SaveModels(ctx, ModelSettings{Fill: "claude-haiku-5-5"}, u.Email); err != nil {
		t.Fatal(err)
	}
	b := New(Config{Model: "claude-sonnet-5-5"}, a.Store, a.Catalog, a.Box)
	if c := b.ModelFor(ctx, ""); c != (ModelChoice{"claude-sonnet-5-5", SourceEnv}) {
		t.Fatalf("after clearing: %+v", c)
	}
	if c := b.ModelFor(ctx, FeatureFill); c != (ModelChoice{"claude-haiku-5-5", SourceOverride}) {
		t.Fatalf("after restart: %+v", c)
	}
}

func TestModelSwitchNeedsNoRestart(t *testing.T) {
	ctx := context.Background()
	f := &fakeClaude{}
	a, u := newModelAgent(t, Config{APIKey: "test", BaseURL: fakeServer(t, f)})

	call := func(feature string) string {
		t.Helper()
		f.reset()
		switch feature {
		case FeatureAssistant:
			if _, err := a.Converse(ctx, "sys", []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("hi"))}, nil); err != nil {
				t.Fatal(err)
			}
		case FeatureSummary:
			if _, err := a.Summarize(ctx, "sys", "transcript"); err != nil {
				t.Fatal(err)
			}
		case FeatureFill:
			_, _ = a.Assist(ctx, u, "app", "a site", nil) // the refusal is an error; the call is what counts
		case FeatureBuilder:
			run, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", Context: "Example API"})
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, a, run.ID, func(r *store.AgentRun) bool { return r.Status == "failed" })
			got, _ := a.Store.GetRun(ctx, run.ID)
			if !f.saw(got.Model) {
				t.Fatalf("builder run recorded %s, last call %s", got.Model, f.last())
			}
			return got.Model
		}
		return f.last()
	}

	for _, feat := range []string{FeatureAssistant, FeatureSummary, FeatureFill, FeatureBuilder} {
		if got := call(feat); got != BuiltinModel {
			t.Fatalf("%s before: %s", feat, got)
		}
	}
	// Switch the default and give every feature but the assistant its own.
	_, _, _, err := a.SaveModels(ctx, ModelSettings{Default: "claude-sonnet-5-5", Builder: "claude-fable-5-1",
		Fill: "claude-haiku-5-5", Summary: "claude-haiku-5-5"}, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{FeatureAssistant: "claude-sonnet-5-5", FeatureSummary: "claude-haiku-5-5",
		FeatureFill: "claude-haiku-5-5", FeatureBuilder: "claude-fable-5-1"}
	for feat, w := range want {
		if got := call(feat); got != w {
			t.Fatalf("%s after the switch: %s, want %s", feat, got, w)
		}
	}
	// The ledger prices each call at the model that answered.
	calls, _ := a.Store.ListAICalls(ctx, time.Now().Add(-time.Hour))
	for _, c := range calls {
		if want := Cost(c.Model, Tokens{Input: 10, Output: 1}); math.Abs(c.CostUSD-want) > 1e-12 {
			t.Fatalf("%s priced %v, want %v", c.Model, c.CostUSD, want)
		}
	}
}

func TestRunKeepsTheModelItStartedWith(t *testing.T) {
	ctx := context.Background()
	f := &fakeClaude{}
	a, u := newModelAgent(t, Config{APIKey: "test", BaseURL: fakeServer(t, f)})
	if _, _, _, err := a.SaveModels(ctx, ModelSettings{Builder: "claude-fable-5-1"}, u.Email); err != nil {
		t.Fatal(err)
	}
	run, err := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "queued", Title: "x",
		CreatedByID: u.ID, CreatedBy: u.Email, Model: a.ModelFor(ctx, FeatureBuilder).Model})
	if err != nil {
		t.Fatal(err)
	}
	// The setting changes while the run waits; the run does not follow.
	if _, _, _, err := a.SaveModels(ctx, ModelSettings{Builder: "claude-sonnet-5-5"}, u.Email); err != nil {
		t.Fatal(err)
	}
	a.execute(run.ID)
	if got, _ := a.Store.GetRun(ctx, run.ID); got.Model != "claude-fable-5-1" || f.last() != "claude-fable-5-1" {
		t.Fatalf("run model %s, called with %s", got.Model, f.last())
	}
}

const modelList = `{"data":[
	{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5","created_at":"2026-09-01T00:00:00Z","max_input_tokens":1000000,"max_tokens":128000,"lifecycle":"active"},
	{"type":"model","id":"claude-new-9","display_name":"Claude New 9","created_at":"2026-10-01T00:00:00Z","max_input_tokens":500000,"max_tokens":64000,"lifecycle":"active"},
	{"type":"model","id":"claude-old-1","display_name":"Claude Old 1","created_at":"2024-01-01T00:00:00Z","max_input_tokens":200000,"max_tokens":8000,"lifecycle":"retired"}
],"has_more":false,"first_id":"claude-opus-5-5","last_id":"claude-old-1"}`

func TestCatalogMergesTheModelsAPI(t *testing.T) {
	ctx := context.Background()
	f := &fakeClaude{list: modelList}
	a, u := newModelAgent(t, Config{APIKey: "test", BaseURL: fakeServer(t, f)})
	cat := a.ListModels(ctx)
	if cat.Source != "api" || cat.Note != "" {
		t.Fatalf("source %q note %q", cat.Source, cat.Note)
	}
	byID := map[string]ModelInfo{}
	for _, m := range cat.Models {
		byID[m.ID] = m
	}
	if m := byID["claude-haiku-5-5"]; !m.Builtin || !m.PricingKnown || m.InputPrice != 0.10 || m.OutputPrice != 0.50 || m.ContextWindow != 1_000_000 {
		t.Fatalf("haiku: %+v", m)
	}
	if m := byID["claude-new-9"]; m.Builtin || m.PricingKnown || m.ContextWindow != 500_000 || m.DisplayName != "Claude New 9" {
		t.Fatalf("unknown model: %+v", m)
	}
	if _, ok := byID["claude-old-1"]; ok {
		t.Fatal("retired models are left out")
	}
	if a.ContextWindowOf("claude-new-9") != 500_000 {
		t.Fatalf("window of a listed model: %d", a.ContextWindowOf("claude-new-9"))
	}
	// Cached: a second load does not call the API again.
	a.ListModels(ctx)
	if n := f.listCalls.Load(); n != 1 {
		t.Fatalf("Models API called %d times", n)
	}

	// A model only the API knows is accepted, with a warning.
	_, saved, warnings, err := a.SaveModels(ctx, ModelSettings{Assistant: "claude-new-9"}, u.Email)
	if err != nil || saved.Assistant != "claude-new-9" || len(warnings) != 1 || !strings.Contains(warnings[0], "claude-new-9") {
		t.Fatalf("saved %+v warnings %v err %v", saved, warnings, err)
	}
	for _, bad := range []string{"claude-bogus-1", "claude-old-1", "Not A Model!"} {
		if _, _, _, err := a.SaveModels(ctx, ModelSettings{Default: bad}, u.Email); !errors.Is(err, ErrInvalidModel) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if a.ModelFor(ctx, FeatureAssistant).Model != "claude-new-9" {
		t.Fatal("a rejected save must not change the settings")
	}
}

func TestCatalogFallsBackToTheBuiltinTable(t *testing.T) {
	ctx := context.Background()
	f := &fakeClaude{} // the Models API answers 500
	a, u := newModelAgent(t, Config{APIKey: "test", BaseURL: fakeServer(t, f)})
	cat := a.ListModels(ctx)
	if cat.Source != "builtin" || !strings.Contains(cat.Note, "Models API") || len(cat.Models) != len(builtinModels) {
		t.Fatalf("fallback: %+v", cat)
	}
	for _, m := range cat.Models {
		if !m.PricingKnown || m.ContextWindow == 0 {
			t.Fatalf("built in model without price or window: %+v", m)
		}
	}
	// Built in models still save; one the list cannot confirm does not.
	if _, _, _, err := a.SaveModels(ctx, ModelSettings{Default: "claude-sonnet-5-5"}, u.Email); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.SaveModels(ctx, ModelSettings{Default: "claude-new-9"}, u.Email); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("unconfirmed model: %v", err)
	}
	// A failure is not retried on every load.
	a.ListModels(ctx)
	if n := f.listCalls.Load(); n != 1 {
		t.Fatalf("Models API called %d times", n)
	}

	// No key: the built in table, and the setting still saves.
	b, u2 := newModelAgent(t, Config{})
	if cat := b.ListModels(ctx); cat.Source != "builtin" || !strings.Contains(cat.Note, "ANTHROPIC_API_KEY") {
		t.Fatalf("no key: %+v", cat)
	}
	if _, _, _, err := b.SaveModels(ctx, ModelSettings{Fill: "claude-haiku-5-5"}, u2.Email); err != nil {
		t.Fatal(err)
	}
}

func TestPricesAndWindowsOfTheChosenModel(t *testing.T) {
	cases := map[string][2]float64{
		"claude-opus-5-5": {4, 20}, "claude-sonnet-5-5": {2, 10}, "claude-haiku-5-5": {0.10, 0.50}, "claude-fable-5-1": {10, 50},
		"claude-sonnet-5-5-20261001": {2, 10},
	}
	for id, w := range cases {
		p, ok := KnownPrice(id)
		if !ok || p.Input != w[0] || p.Output != w[1] || p.CacheWrite != p.Input*1.25 {
			t.Fatalf("%s: %+v %v", id, p, ok)
		}
	}
	if _, ok := KnownPrice("claude-opus-5-7"); ok {
		t.Fatal("an unknown model must not borrow a neighbour's price")
	}
	// A million output tokens on Sonnet 5.5 is $10.
	if c := Cost("claude-sonnet-5-5", Tokens{Output: 1_000_000}); math.Abs(c-10) > 1e-9 {
		t.Fatalf("sonnet cost %v", c)
	}
	// Haiku 5.5 charges 5x past 100K prompt tokens.
	if c := Cost("claude-haiku-5-5", Tokens{Input: 200_000, Output: 1000}); math.Abs(c-(200_000*0.5+1000*2.5)/1e6) > 1e-12 {
		t.Fatalf("long haiku prompt cost %v", c)
	}
	a, _ := newModelAgent(t, Config{})
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"} {
		if a.ContextWindowOf(id) != 1_000_000 {
			t.Fatalf("%s window %d", id, a.ContextWindowOf(id))
		}
	}
	if a.ContextWindowOf("claude-haiku-4-5") != 200_000 {
		t.Fatal("older models fall back to 200K")
	}
}
