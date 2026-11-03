package agent

import (
	"context"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Enabled reports whether an Anthropic API key is configured.
func (a *Agent) Enabled() bool { return a.enabled }

// Model is the configured Claude model id.
func (a *Agent) Model() string { return a.model }

// Converse sends one turn of an assistant chat with custom tools. The caller
// runs the tool loop; this only wraps the client with Acnologia's model and
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

// Summarize asks Claude for a summary of an Assistant conversation, used to
// compact a long thread. No tools and no Acnologia rules: the transcript is
// data to summarize, not a conversation to continue.
func (a *Agent) Summarize(ctx context.Context, system, transcript string) (*anthropic.BetaMessage, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	return a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(a.model),
		MaxTokens:    4096,
		System:       []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(transcript))},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow},
		Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
		Betas:        []anthropic.AnthropicBeta{betaFallback},
	})
}

// ContextWindow is the context window, in tokens, of a Claude model id.
// Current models take 1M tokens; anything else (the older 200K generation,
// an unknown id) gets 200K, which compacts early rather than late.
func ContextWindow(model string) int {
	for _, p := range []string{"claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-mythos-5", "claude-haiku-5",
		"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-sonnet-4-6"} {
		if strings.HasPrefix(model, p) {
			return 1_000_000
		}
	}
	return 200_000
}
