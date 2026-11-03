package agent

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Price is a model's list price in USD. Token prices are per million tokens,
// web search per 1,000 searches.
type Price struct {
	Input      float64 `json:"input"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
	Output     float64 `json:"output"`
	WebSearch  float64 `json:"web_search"`
	// LongPromptAbove, when set, is the prompt size in tokens past which
	// every token price is multiplied by LongPromptFactor.
	LongPromptAbove  int64   `json:"long_prompt_above,omitempty"`
	LongPromptFactor float64 `json:"long_prompt_factor,omitempty"`
}

// price builds a Price with cache writes at 1.25x input and web search at
// $10 per 1,000.
func price(in, cacheRead, out float64) Price {
	return Price{Input: in, CacheWrite: in * 1.25, CacheRead: cacheRead, Output: out, WebSearch: 10}
}

// defaultPrice is Claude Opus 5.5: $4 input, $20 output, $0.20 cache reads per
// MTok, cache writes at 1.25x input, web search $10 per 1,000. Models not in
// prices are charged at this rate.
var defaultPrice = price(4, 0.20, 20)

// prices is the one place model prices live, Anthropic's first party list
// prices. Keys are model ids; a dated id (claude-opus-5-5-20261001) matches
// its base entry.
var prices = map[string]Price{
	"claude-opus-5-5":   defaultPrice,
	"claude-sonnet-5-5": price(2, 0.20, 10),
	// Haiku 5.5 also names builder runs. Prompts over 100K tokens cost 5x
	// ($0.50 input, $2.50 output).
	"claude-haiku-5-5": {Input: 0.10, CacheWrite: 0.125, CacheRead: 0.01, Output: 0.50, WebSearch: 10,
		LongPromptAbove: 100_000, LongPromptFactor: 5},
	"claude-fable-5-1": price(10, 0.25, 50),
	// The previous generation, for models the Models API still lists.
	"claude-fable-5":    price(10, 1, 50),
	"claude-opus-5":     price(5, 0.50, 25),
	"claude-opus-4-8":   price(5, 0.50, 25),
	"claude-opus-4-7":   price(5, 0.50, 25),
	"claude-opus-4-6":   price(5, 0.50, 25),
	"claude-sonnet-5":   price(2, 0.20, 10),
	"claude-sonnet-4-6": price(3, 0.30, 15),
	"claude-haiku-4-5":  price(1, 0.10, 5),
}

// datedSuffix is the -YYYYMMDD snapshot suffix of a dated model id.
var datedSuffix = regexp.MustCompile(`-\d{8}$`)

// KnownPrice returns the price of a model in the table, matching a dated id
// to its base entry. ok is false for a model Acnologia has no price for.
func KnownPrice(model string) (Price, bool) {
	if p, ok := prices[model]; ok {
		return p, true
	}
	if base := datedSuffix.ReplaceAllString(model, ""); base != model {
		if p, ok := prices[base]; ok {
			return p, true
		}
	}
	return Price{}, false
}

// PriceFor returns the price of a model, falling back to the default.
func PriceFor(model string) Price {
	if p, ok := KnownPrice(model); ok {
		return p
	}
	return defaultPrice
}

// Tokens is the billable usage of one Claude call.
type Tokens struct {
	Input       int64
	CacheWrite  int64
	CacheRead   int64
	Output      int64
	WebSearches int64
}

// TokensOf reads the billable usage off an API response.
func TokensOf(u anthropic.BetaUsage) Tokens {
	return Tokens{Input: u.InputTokens, CacheWrite: u.CacheCreationInputTokens, CacheRead: u.CacheReadInputTokens,
		Output: u.OutputTokens, WebSearches: u.ServerToolUse.WebSearchRequests}
}

// Cost is the USD cost of usage at a model's built in price. Agent.CostOf
// also applies the price overrides saved in Settings.
func Cost(model string, t Tokens) float64 { return costAt(PriceFor(model), t) }

func costAt(p Price, t Tokens) float64 {
	f := 1.0
	if p.LongPromptAbove > 0 && t.Input+t.CacheWrite+t.CacheRead > p.LongPromptAbove {
		f = p.LongPromptFactor
	}
	return f*(float64(t.Input)*p.Input+float64(t.CacheWrite)*p.CacheWrite+float64(t.CacheRead)*p.CacheRead+
		float64(t.Output)*p.Output)/1e6 + float64(t.WebSearches)*p.WebSearch/1000
}

// Caller is who a Claude call was made for.
type Caller struct {
	ID    int64
	Email string
}

// CallerOf turns a user into a Caller; nil means the system.
func CallerOf(u *store.User) Caller {
	if u == nil {
		return Caller{Email: "system"}
	}
	return Caller{ID: u.ID, Email: u.Email}
}

// Record writes one Claude call to the usage ledger and returns its cost.
// The model is the one that answered, which can differ from the configured
// one when the server falls back.
func (a *Agent) Record(ctx context.Context, feature string, by Caller, ref string, resp *anthropic.BetaMessage) float64 {
	if resp == nil {
		return 0
	}
	model := string(resp.Model)
	if model == "" {
		model = a.ModelFor(ctx, feature).Model
	}
	t := TokensOf(resp.Usage)
	cost := a.CostOf(model, t)
	err := a.Store.RecordAICall(context.WithoutCancel(ctx), store.AICall{Feature: feature, UserID: by.ID, UserEmail: by.Email, Model: model,
		InputTokens: t.Input, CacheWriteTokens: t.CacheWrite, CacheReadTokens: t.CacheRead, OutputTokens: t.Output,
		WebSearches: t.WebSearches, CostUSD: cost, Ref: ref})
	if err != nil {
		slog.Warn("recording AI usage failed", "feature", feature, "err", err)
	}
	return cost
}

func runRef(id int64) string    { return "run:" + strconv.FormatInt(id, 10) }
func ThreadRef(id int64) string { return "thread:" + strconv.FormatInt(id, 10) }
