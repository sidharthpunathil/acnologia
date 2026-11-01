package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
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
}

type AssistResult struct {
	Fields map[string]any `json:"fields"`
	Notes  string         `json:"notes"`
}

// Assist fills a form from a one line instruction.
func (a *Agent) Assist(ctx context.Context, form, instruction string, current map[string]any) (*AssistResult, error) {
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
	system := "You fill forms in Acnologia, a self-hosted API key pool, from a one line description. Return every field; keep current values that already fit. Be concise and practical.\n\n" + ctxInfo
	if r := a.rulesBlock(ctx); r != "" {
		system += "\n\n" + r
	}
	resp, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
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
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return nil, errors.New("Claude declined this request")
	}
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			var res AssistResult
			if err := json.Unmarshal([]byte(t.Text), &res); err != nil {
				return nil, fmt.Errorf("unexpected AI output: %v", err)
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
