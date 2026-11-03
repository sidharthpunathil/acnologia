package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// BuiltinModel is the model Acnologia uses when nothing else picks one.
const BuiltinModel = "claude-opus-5-5"

// The features that can run on their own model. The names match the AI
// usage ledger's features, plus summary for chat compaction summaries.
const (
	FeatureAssistant = "assistant"
	FeatureBuilder   = "builder"
	FeatureFill      = "fill"
	FeatureSummary   = "summary"
	// FeatureRefresh is the scheduled job that re-checks providers' offers,
	// limits and pricing. It resolves like the others:
	// a.ModelFor(context.Background(), FeatureRefresh).
	FeatureRefresh = "refresh"
)

// ModelFeatures lists the features in the order the Settings page shows them.
var ModelFeatures = []string{FeatureAssistant, FeatureBuilder, FeatureFill, FeatureSummary, FeatureRefresh}

// Where an effective model comes from, strongest first.
const (
	SourceOverride = "override" // the feature's own saved model
	SourceSaved    = "saved"    // the saved default model
	SourceEnv      = "env"      // ACNOLOGIA_AI_MODEL
	SourceBuiltin  = "builtin"  // BuiltinModel
)

// metaModels is the meta table key that holds the saved ModelSettings.
const metaModels = "ai_models"

// ModelSettings is the model choice saved from the Settings page. An empty
// field means "use the default": for a feature, the saved default; for the
// default, ACNOLOGIA_AI_MODEL and then BuiltinModel.
type ModelSettings struct {
	Default   string     `json:"default"`
	Assistant string     `json:"assistant"`
	Builder   string     `json:"builder"`
	Fill      string     `json:"fill"`
	Summary   string     `json:"summary"`
	Refresh   string     `json:"refresh"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// Field returns the saved model for a feature ("default" or "" for the default).
func (m ModelSettings) Field(feature string) string {
	switch feature {
	case FeatureAssistant:
		return m.Assistant
	case FeatureBuilder:
		return m.Builder
	case FeatureFill:
		return m.Fill
	case FeatureSummary:
		return m.Summary
	case FeatureRefresh:
		return m.Refresh
	}
	return m.Default
}

func (m *ModelSettings) set(feature, v string) {
	switch feature {
	case FeatureAssistant:
		m.Assistant = v
	case FeatureBuilder:
		m.Builder = v
	case FeatureFill:
		m.Fill = v
	case FeatureSummary:
		m.Summary = v
	case FeatureRefresh:
		m.Refresh = v
	default:
		m.Default = v
	}
}

// ModelChoice is the model a feature runs on and why.
type ModelChoice struct {
	Model  string `json:"model"`
	Source string `json:"source"`
}

// modelState is the in memory copy of the saved settings and the Models API
// cache. The settings are read on first use and replaced on save, so a
// switch takes effect on the next call without a restart.
type modelState struct {
	mu       sync.RWMutex
	settings *ModelSettings

	listMu    sync.Mutex
	listed    []anthropic.ModelInfo
	listedAt  time.Time
	listErrAt time.Time
}

const (
	// modelListTTL is how long a Models API answer is reused.
	modelListTTL = time.Hour
	// modelListRetry is how long a failed Models API call is not retried.
	modelListRetry = 5 * time.Minute
)

// Models is the saved model settings.
func (a *Agent) Models(ctx context.Context) ModelSettings {
	a.models.mu.RLock()
	s := a.models.settings
	a.models.mu.RUnlock()
	if s != nil {
		return *s
	}
	var m ModelSettings
	if raw := a.Store.GetMeta(ctx, metaModels); raw != "" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			slog.Warn("saved AI model settings are unreadable, using the defaults", "err", err)
			m = ModelSettings{}
		}
	}
	a.models.mu.Lock()
	if a.models.settings == nil {
		a.models.settings = &m
	}
	m = *a.models.settings
	a.models.mu.Unlock()
	return m
}

// ModelFor is the model a feature runs on now: the feature's override, then
// the saved default, then ACNOLOGIA_AI_MODEL, then BuiltinModel. Pass "" or
// "default" for the default model. It needs no request: a background worker
// calls it with context.Background(). An unknown feature name gets the
// default model.
func (a *Agent) ModelFor(ctx context.Context, feature string) ModelChoice {
	s := a.Models(ctx)
	if feature != "" && feature != "default" {
		if v := s.Field(feature); v != "" {
			return ModelChoice{Model: v, Source: SourceOverride}
		}
	}
	if s.Default != "" {
		return ModelChoice{Model: s.Default, Source: SourceSaved}
	}
	if a.envModel != "" {
		return ModelChoice{Model: a.envModel, Source: SourceEnv}
	}
	return ModelChoice{Model: BuiltinModel, Source: SourceBuiltin}
}

// EffectiveModels is ModelFor for the default and every feature.
func (a *Agent) EffectiveModels(ctx context.Context) map[string]ModelChoice {
	out := map[string]ModelChoice{"default": a.ModelFor(ctx, "")}
	for _, f := range ModelFeatures {
		out[f] = a.ModelFor(ctx, f)
	}
	return out
}

// EnvModel is ACNOLOGIA_AI_MODEL, empty when it is not set.
func (a *Agent) EnvModel() string { return a.envModel }

// ModelInfo is one model the Settings page offers.
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	// ContextWindow is the input context window in tokens.
	ContextWindow int `json:"context_window"`
	// InputPrice and OutputPrice are USD per million tokens. Zero when
	// PricingKnown is false.
	InputPrice   float64 `json:"input_price"`
	OutputPrice  float64 `json:"output_price"`
	PricingKnown bool    `json:"pricing_known"`
	// Builtin is set for models in Acnologia's own table.
	Builtin bool `json:"builtin"`
}

// ModelCatalog is the list of models to pick from.
type ModelCatalog struct {
	Models []ModelInfo `json:"models"`
	// Source is "api" when the list came from Anthropic's Models API, or
	// "builtin" when it is Acnologia's own table.
	Source string `json:"source"`
	// Note says why the list is the built in table, when it is.
	Note string `json:"note,omitempty"`
}

// builtinModels is the table of current models Acnologia knows: the list
// shown when the Models API is not available, and the context windows used
// for compaction. Prices live in pricing.go.
var builtinModels = []struct {
	id, name string
	window   int
}{
	{"claude-opus-5-5", "Claude Opus 5.5", 1_000_000},
	{"claude-sonnet-5-5", "Claude Sonnet 5.5", 1_000_000},
	{"claude-haiku-5-5", "Claude Haiku 5.5", 1_000_000},
	{"claude-fable-5-1", "Claude Fable 5.1", 1_000_000},
}

func builtinInfo(id string) (ModelInfo, bool) {
	for _, b := range builtinModels {
		if b.id == id {
			return modelInfo(b.id, b.name, b.window, true), true
		}
	}
	return ModelInfo{}, false
}

func modelInfo(id, name string, window int, builtin bool) ModelInfo {
	m := ModelInfo{ID: id, DisplayName: name, ContextWindow: window, Builtin: builtin}
	if p, ok := KnownPrice(id); ok {
		m.InputPrice, m.OutputPrice, m.PricingKnown = p.Input, p.Output, true
	}
	return m
}

func builtinCatalog(note string) ModelCatalog {
	out := ModelCatalog{Source: "builtin", Note: note}
	for _, b := range builtinModels {
		out.Models = append(out.Models, modelInfo(b.id, b.name, b.window, true))
	}
	return out
}

// fetchModels returns the Models API's list, cached for an hour. A failure
// is remembered for a few minutes so a broken network does not slow every
// Settings load.
func (a *Agent) fetchModels(ctx context.Context) ([]anthropic.ModelInfo, error) {
	st := &a.models
	st.listMu.Lock()
	defer st.listMu.Unlock()
	if st.listed != nil && time.Since(st.listedAt) < modelListTTL {
		return st.listed, nil
	}
	if !st.listErrAt.IsZero() && time.Since(st.listErrAt) < modelListRetry {
		return nil, errors.New("the Models API failed a moment ago")
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := []anthropic.ModelInfo{}
	iter := a.client.Models.ListAutoPaging(lctx, anthropic.ModelListParams{Limit: anthropic.Int(1000)}, option.WithMaxRetries(0))
	for iter.Next() {
		out = append(out, iter.Current())
	}
	if err := iter.Err(); err != nil {
		st.listErrAt = time.Now()
		slog.Warn("listing Claude models failed, using the built in table", "err", err)
		return nil, err
	}
	st.listed, st.listedAt, st.listErrAt = out, time.Now(), time.Time{}
	return out, nil
}

// cachedListing is the last Models API answer, if any, without calling it.
func (a *Agent) cachedListing() []anthropic.ModelInfo {
	a.models.listMu.Lock()
	defer a.models.listMu.Unlock()
	return a.models.listed
}

// ListModels is the models to pick from: the Models API's list merged with the
// built in table (which supplies prices and known context windows), or the
// built in table alone when there is no API key or the call fails. Retired
// models are left out.
func (a *Agent) ListModels(ctx context.Context) ModelCatalog {
	if !a.enabled {
		return builtinCatalog("ANTHROPIC_API_KEY is not set, so this is Acnologia's built in list.")
	}
	listed, err := a.fetchModels(ctx)
	if err != nil {
		return builtinCatalog("Could not reach Anthropic's Models API, so this is Acnologia's built in list.")
	}
	out := ModelCatalog{Source: "api"}
	seen := map[string]bool{}
	// The built in models first, in their order, then the rest as listed.
	for _, b := range builtinModels {
		m, _ := builtinInfo(b.id)
		out.Models = append(out.Models, m)
		seen[b.id] = true
	}
	for _, l := range listed {
		if seen[l.ID] || l.Lifecycle == "retired" {
			continue
		}
		seen[l.ID] = true
		window := int(l.MaxInputTokens)
		if window <= 0 {
			window = ContextWindow(l.ID)
		}
		name := l.DisplayName
		if name == "" {
			name = l.ID
		}
		out.Models = append(out.Models, modelInfo(l.ID, name, window, false))
	}
	return out
}

// ContextWindowOf is a model's context window in tokens: the built in
// table, then what the Models API reported, then the ContextWindow guess.
func (a *Agent) ContextWindowOf(model string) int {
	if m, ok := builtinInfo(model); ok {
		return m.ContextWindow
	}
	for _, l := range a.cachedListing() {
		if l.ID == model && l.MaxInputTokens > 0 {
			return int(l.MaxInputTokens)
		}
	}
	return ContextWindow(model)
}

var modelIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:@-]{1,99}$`)

// ErrInvalidModel marks a model setting that failed validation.
var ErrInvalidModel = errors.New("invalid model")

// SaveModels validates and saves the model settings and returns the old
// settings, the new ones and warnings about models Acnologia does not know
// the price of. Each field is a model id or empty for "use the default".
// An id is accepted when it is in the built in table or the Models API's
// list, or when it is already in use (saved before, or ACNOLOGIA_AI_MODEL).
func (a *Agent) SaveModels(ctx context.Context, in ModelSettings, by string) (old, saved ModelSettings, warnings []string, err error) {
	old = a.Models(ctx)
	cat := a.ListModels(ctx)
	known := map[string]ModelInfo{}
	for _, m := range cat.Models {
		known[m.ID] = m
	}
	next := ModelSettings{}
	for _, f := range append([]string{"default"}, ModelFeatures...) {
		v := strings.TrimSpace(in.Field(f))
		if v == "" {
			continue
		}
		label := f
		if label == "default" {
			label = "the default model"
		}
		if !modelIDRe.MatchString(v) {
			return old, old, nil, fmt.Errorf("%w: %q is not a model id (%s)", ErrInvalidModel, v, label)
		}
		m, ok := known[v]
		switch {
		case ok && m.Builtin:
		case ok:
			if !m.PricingKnown {
				warnings = append(warnings, fmt.Sprintf("%s is not in Acnologia's price table, so its cost is estimated at %s prices.", v, BuiltinModel))
			}
		case v == old.Field(f) || v == a.envModel:
			// Already in use: keep it even if the list cannot confirm it now.
		default:
			return old, old, nil, fmt.Errorf("%w: %s is not an available model (%s). Pick one from the list.", ErrInvalidModel, v, label)
		}
		next.set(f, v)
	}
	t := time.Now().UTC()
	next.UpdatedAt, next.UpdatedBy = &t, by
	raw, _ := json.Marshal(next)
	if err := a.Store.SetMeta(ctx, metaModels, string(raw)); err != nil {
		return old, old, nil, err
	}
	a.models.mu.Lock()
	a.models.settings = &next
	a.models.mu.Unlock()
	return old, next, dedupe(warnings), nil
}

// dedupe keeps the first of each warning.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
