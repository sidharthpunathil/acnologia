package agent

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Enabled reports whether an Anthropic API key is configured.
func (a *Agent) Enabled() bool { return a.enabled }

// Model is the configured Claude model id.
func (a *Agent) Model() string { return a.model }

// Converse sends one turn of an assistant chat with custom tools. The caller
// runs the tool loop; this only wraps the client with Cogito's model and
// defaults. No extended thinking, so history can be replayed without
// thinking signatures.
func (a *Agent) Converse(ctx context.Context, system string, messages []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	sys := []anthropic.BetaTextBlockParam{{Text: system}}
	if r := a.rulesBlock(ctx); r != "" {
		sys = append(sys, anthropic.BetaTextBlockParam{Text: r})
	}
	return a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(a.model),
		MaxTokens:    4096,
		System:       sys,
		Messages:     messages,
		Tools:        tools,
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortMedium},
		Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
		Betas:        []anthropic.AnthropicBeta{betaFallback},
	})
}
