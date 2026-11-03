package agent

import (
	"context"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Enabled reports whether an Anthropic API key is configured.
func (a *Agent) Enabled() bool { return a.enabled }

// Model is the effective default Claude model id (models.go).
func (a *Agent) Model() string { return a.ModelFor(context.Background(), "").Model }

// Converse sends one turn of an assistant chat with custom tools on the
// Assistant's current model. See ConverseWith.
func (a *Agent) Converse(ctx context.Context, system string, messages []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
	return a.ConverseWith(ctx, a.ModelFor(ctx, FeatureAssistant).Model, system, messages, tools)
}

// ConverseWith sends one turn of an assistant chat with custom tools on the
// given model. The caller runs the tool loop and picks the model once per
// turn, so a switch on the Settings page never lands in the middle of one.
// No extended thinking, so history can be replayed without thinking
// signatures.
func (a *Agent) ConverseWith(ctx context.Context, model, system string, messages []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	sys := []anthropic.BetaTextBlockParam{{Text: system}}
	if r := a.rulesBlock(ctx); r != "" {
		sys = append(sys, anthropic.BetaTextBlockParam{Text: r})
	}
	return a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(model),
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
// compact a long thread, on the summary feature's model. No tools and no
// Acnologia rules: the transcript is data to summarize, not a conversation
// to continue.
func (a *Agent) Summarize(ctx context.Context, system, transcript string) (*anthropic.BetaMessage, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	return a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(a.ModelFor(ctx, FeatureSummary).Model),
		MaxTokens:    4096,
		System:       []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(transcript))},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow},
		Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
		Betas:        []anthropic.AnthropicBeta{betaFallback},
	})
}

// ContextWindow guesses the context window, in tokens, of a Claude model id
// from its name. Current models take 1M tokens; anything else (the older
// 200K generation, an unknown id) gets 200K, which compacts early rather
// than late. Agent.ContextWindowOf checks the model tables first.
func ContextWindow(model string) int {
	for _, p := range []string{"claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-mythos-5", "claude-haiku-5",
		"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-sonnet-4-6"} {
		if strings.HasPrefix(model, p) {
			return 1_000_000
		}
	}
	return 200_000
}
