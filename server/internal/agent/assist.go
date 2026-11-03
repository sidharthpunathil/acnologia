package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// assistForms describes the fields "Fill with AI" may set on each form.
var assistForms = map[string]map[string]any{
	"app": {
		"name":             map[string]any{"type": "string", "description": "short human name, for example Marketing site"},
		"description":      map[string]any{"type": "string", "description": "one sentence"},
		"mode":             map[string]any{"type": "string", "enum": []string{"proxy", "lease", "both"}, "description": "proxy for websites and anything public (keys never leave Acnologia), lease when the code must call the provider itself, both for trusted backends"},
		"allowed_services": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "service slugs this app needs; empty means all"},
	},
	"service": {
		"slug":              map[string]any{"type": "string", "description": "lowercase, hyphens, used in /proxy/<slug>"},
		"name":              map[string]any{"type": "string"},
		"category":          map[string]any{"type": "string"},
		"description":       map[string]any{"type": "string"},
		"strategy":          map[string]any{"type": "string", "enum": []string{"round_robin", "fill_first", "least_used", "most_quota", "random"}},
		"cooldown_sec":      map[string]any{"type": "integer"},
		"max_attempts":      map[string]any{"type": "integer"},
		"max_concurrency":   map[string]any{"type": "integer", "description": "parallel requests per key, 0 = unlimited"},
		"queue_timeout_sec": map[string]any{"type": "integer"},
	},
	"provider": {
		"name":        map[string]any{"type": "string"},
		"category":    map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
		"website":     map[string]any{"type": "string"},
		"docs_url":    map[string]any{"type": "string"},
		"color":       map[string]any{"type": "string", "description": "brand color #rrggbb"},
	},
	// guide is a provider's usage guide (providers.Guide). Structured output
	// needs a closed schema, so an endpoint's example body is a JSON string
	// here and parsed back in assistGuideFields.
	"guide": {
		"summary": map[string]any{"type": "string", "description": "1 to 3 sentences: what the API is for and when to pick it"},
		"endpoints": map[string]any{"type": "array", "description": "the endpoints worth knowing, most useful first, paths relative to the base URL; never describe auth (Acnologia adds the key)",
			"items": closed(map[string]any{
				"name":        map[string]any{"type": "string"},
				"method":      map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
				"path":        map[string]any{"type": "string", "description": "starts with /"},
				"description": map[string]any{"type": "string"},
				"params": map[string]any{"type": "array", "items": closed(map[string]any{
					"name":        map[string]any{"type": "string"},
					"in":          map[string]any{"type": "string", "enum": []string{"query", "path", "body", "header"}},
					"type":        map[string]any{"type": "string"},
					"required":    map[string]any{"type": "boolean"},
					"description": map[string]any{"type": "string"},
				})},
				"body":     map[string]any{"type": "string", "description": "an example JSON request body as a JSON string, or empty"},
				"response": map[string]any{"type": "string", "description": "what comes back and the fields that matter"},
			})},
		"notes": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"description": "short facts an agent gets wrong without them: pagination, model names, streaming, error shapes, size limits, cost traps"},
	},
}

// closed is an object schema that requires every property and allows no
// others, as structured output wants.
func closed(props map[string]any) map[string]any {
	req := []string{}
	for k := range props {
		req = append(req, k)
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": req}
}

// assistGuideFields turns the endpoints' body strings back into JSON and
// drops empty ones, so the fields drop straight into a spec's guide.
func assistGuideFields(fields map[string]any) {
	eps, _ := fields["endpoints"].([]any)
	for _, e := range eps {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		s, _ := m["body"].(string)
		var v any
		if strings.TrimSpace(s) == "" || json.Unmarshal([]byte(s), &v) != nil {
			delete(m, "body")
			continue
		}
		m["body"] = v
	}
}

// guideContext tells Fill with AI what the provider's spec already says, so
// a guide is grounded in its base URL, docs URL and examples. current may
// name the provider as "provider"; nothing else is sent.
func (a *Agent) guideContext(ctx context.Context, current map[string]any) string {
	slug, _ := current["provider"].(string)
	if slug == "" {
		return ""
	}
	p, err := a.Store.GetProvider(ctx, slug)
	if err != nil {
		return ""
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		return ""
	}
	ex, _ := json.Marshal(spec.Examples)
	return fmt.Sprintf("\nProvider: %s (%s). Base URL, which paths are relative to: %s. Docs: %s\nSpec examples: %s", p.Name, p.Slug, spec.BaseURL, p.DocsURL, ex)
}

type AssistResult struct {
	Fields map[string]any `json:"fields"`
	Notes  string         `json:"notes"`
}

// Assist fills a form from a one line instruction.
func (a *Agent) Assist(ctx context.Context, by *store.User, form, instruction string, current map[string]any) (*AssistResult, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	props, ok := assistForms[form]
	if !ok {
		return nil, fmt.Errorf("unknown form %q", form)
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil, errors.New("describe what you want in a sentence")
	}
	fieldNames := []string{}
	for k := range props {
		fieldNames = append(fieldNames, k)
	}
	out := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"fields", "notes"},
		"properties": map[string]any{
			"fields": map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": fieldNames},
			"notes":  map[string]any{"type": "string", "description": "one short sentence explaining non-obvious choices"},
		},
	}
	cur, _ := json.Marshal(current)
	ctxInfo := a.assistContext(ctx)
	if form == "guide" {
		ctxInfo += a.guideContext(ctx, current) + "\nA guide teaches apps and AI agents to call the provider through Acnologia's proxy. Never describe auth or keys; base endpoints on the examples and what is known of the API, and never invent params."
	}
	system := "You fill forms in Acnologia, a self-hosted API key pool, from a one line description. Return every field; keep current values that already fit. Be concise and practical.\n\n" + ctxInfo
	if r := a.rulesBlock(ctx); r != "" {
		system += "\n\n" + r
	}
	resp, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.ModelFor(ctx, FeatureFill).Model),
		MaxTokens: 4000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages: []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(
			fmt.Sprintf("Form: %s\nCurrent values: %s\nRequest: %s", form, cur, instruction)))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: out},
		},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{betaFallback},
	})
	if err != nil {
		return nil, fmt.Errorf("AI request failed: %w", err)
	}
	a.Record(ctx, store.AIFeatureFill, CallerOf(by), "form:"+form, resp)
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return nil, errors.New("Claude declined this request")
	}
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			var res AssistResult
			if err := json.Unmarshal([]byte(t.Text), &res); err != nil {
				return nil, fmt.Errorf("unexpected AI output: %v", err)
			}
			if form == "guide" && res.Fields != nil {
				assistGuideFields(res.Fields)
			}
			return &res, nil
		}
	}
	return nil, errors.New("the AI returned no answer")
}

// assistContext lists what exists so suggestions reference real services.
func (a *Agent) assistContext(ctx context.Context) string {
	var b strings.Builder
	if svcs, err := a.Store.ListServices(ctx); err == nil && len(svcs) > 0 {
		b.WriteString("Existing services (slug: name, provider):\n")
		for _, s := range svcs {
			fmt.Fprintf(&b, "- %s: %s, %s\n", s.Slug, s.Name, s.Provider)
		}
	}
	if ps, err := a.Store.ListProviders(ctx); err == nil {
		b.WriteString("Providers:\n")
		for _, p := range ps {
			fmt.Fprintf(&b, "- %s: %s (%s)\n", p.Slug, p.Name, p.Category)
		}
	}
	return b.String()
}
