package agent

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/cogito-keys/cogito/internal/store"
)

// Price is a model's list price in USD. Token prices are per million tokens,
// web search per 1,000 searches.
type Price struct {
	Input      float64 `json:"input"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
	Output     float64 `json:"output"`
	WebSearch  float64 `json:"web_search"`
}

// defaultPrice is Claude Opus 5.5: $4 input, $20 output, $0.20 cache reads per
// MTok, cache writes at 1.25x input, web search $10 per 1,000. Models not in
// prices are charged at this rate.
var defaultPrice = Price{Input: 4, CacheWrite: 5, CacheRead: 0.20, Output: 20, WebSearch: 10}

// prices is the one place model prices live. Keys are model ids; a dated or
// suffixed id (claude-opus-5-5-20261001) matches its base entry.
var prices = map[string]Price{
	"claude-opus-5-5": defaultPrice,
}

// PriceFor returns the price of a model, falling back to the default.
func PriceFor(model string) Price {
	if p, ok := prices[model]; ok {
		return p
	}
	best := ""
	for id := range prices {
		if strings.HasPrefix(model, id) && len(id) > len(best) {
			best = id
		}
	}
	if best != "" {
		return prices[best]
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

// Cost is the USD cost of usage at a model's price.
func Cost(model string, t Tokens) float64 {
	p := PriceFor(model)
	return (float64(t.Input)*p.Input+float64(t.CacheWrite)*p.CacheWrite+float64(t.CacheRead)*p.CacheRead+
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
		model = a.model
	}
	t := TokensOf(resp.Usage)
	cost := Cost(model, t)
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
