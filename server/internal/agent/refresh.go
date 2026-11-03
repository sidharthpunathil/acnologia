package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Caps for one provider's offers check. It reads a pricing page or two, so
// it stays small: a few searches, a few fetches, a handful of turns.
const (
	RefreshMaxTurns    = 8
	RefreshMaxSearches = 3
	RefreshMaxFetches  = 5
	refreshMaxSubmits  = 2
	refreshFetchTokens = 20000
	refreshMaxTokens   = 6000
)

// ErrRefreshBudget stops a check that reached its spending cap.
var ErrRefreshBudget = errors.New("the refresh budget ran out during this check")

// OffersRequest asks for one provider's offers to be re-checked.
type OffersRequest struct {
	Provider *store.Provider
	Caller   Caller
	// Ref ties the ledger rows to the refresh run, "refresh:12".
	Ref string
	// MaxCostUSD stops the check once it has spent this much; 0 is no cap.
	MaxCostUSD float64
	// Progress, when set, hears one short line per step.
	Progress func(string)
}

// OffersResult is what the check found.
type OffersResult struct {
	// Verified is false when the model could not read the provider's pages,
	// so the offers it returns are only the current ones.
	Verified bool              `json:"verified"`
	Offers   []providers.Offer `json:"offers"`
	Summary  string            `json:"summary"`
	Model    string            `json:"model"`
	CostUSD  float64           `json:"cost_usd"`
}

const refreshGuide = `You keep the offers of one provider in Acnologia, a self-hosted API key pool, up to date. Offers are the provider's priced products or endpoints, from its pricing and rate limit pages. Acnologia paces every key to stay under an offer's limits, prices each request from the matching offer, and shows free offers and limits to people and AI clients.

Each offer: name (the product name), match (omit for a provider wide offer; else {method, path} where path is relative to base_url and matches as a prefix, * matches inside one segment and a trailing $ makes it exact, or {examples: ["example name"]}), price {amount (USD per ONE unit: $5 per 1,000 is 0.005), currency (default USD), unit: request | search | query | url | page | step | minute | check | token | character | credit | image | second | session | run | gb, per_request (units one request uses when fixed)}, free (true when requests cost nothing), limits [{count, per: second | minute | hour | day | month, unit}], concurrency (in flight requests per key), notes (plan differences, daily allowances), source_url (the page you read).

Put only limits that hold for every key in limits: the pacer enforces them on all keys, so a free plan cap would throttle paid keys. Write plan dependent caps in notes.

# How to work
1. Read the provider's pricing page and rate limit page. Start from the URLs given. Use web_fetch on them; search only when a URL is missing or moved.
2. Compare what the pages say with the current offers.
3. Call submit_offers once with the FULL offers list.

# Hard rules
- Copy every offer the pages still support exactly as given: same name, match, notes and numbers. Change only what the pages show is different.
- Never guess a price or a limit. Leave out any number the pages do not publish.
- Every offer you add or change needs the source_url of the page that shows it.
- Never invent offers for products the provider does not sell.
- If you cannot read the provider's pages, call submit_offers with verified false and the current offers unchanged.
- You only update offers. Do not mention or change auth, the base URL or error rules.
- Your text between tool calls should be one short progress sentence.`

var offersTool = anthropic.BetaToolParam{
	Name:        "submit_offers",
	Description: anthropic.String("Submit the provider's full, current offers list. The harness validates it and may send problems back for one more try."),
	InputSchema: schema(map[string]any{
		"offers":   map[string]any{"type": "array", "description": "Every offer, in order, as described in the system prompt.", "items": map[string]any{"type": "object"}},
		"verified": map[string]any{"type": "boolean", "description": "true when you read the provider's pricing or limits pages; false when you could not."},
		"summary":  map[string]any{"type": "string", "description": "One to three sentences for the operator: what changed (or that nothing did) and the pages you read."},
	}, "offers", "verified", "summary"),
}

// ResearchOffers re-checks one provider's offers with the refresh model. It
// sends the provider's public metadata and current offers, never keys, and
// every request still passes the redaction middleware.
func (a *Agent) ResearchOffers(ctx context.Context, req OffersRequest) (*OffersResult, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	p := req.Provider
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		return nil, fmt.Errorf("the provider's spec is unreadable: %w", err)
	}
	res := &OffersResult{}
	progress := a.refreshProgress(ctx, req.Progress)
	progress("Reading " + p.Name + "'s pricing pages")
	loop := refreshLoop{guide: refreshGuide, task: refreshTask(p, spec), tool: offersTool, what: "offers list",
		caller: req.Caller, ref: req.Ref, maxCost: req.MaxCostUSD, progress: progress,
		check: func(raw []byte) (string, bool) { return checkOffers(spec, raw, res) }}
	res.Model, res.CostUSD, err = a.runRefreshLoop(ctx, loop)
	if err == nil {
		progress("Comparing with the current offers")
	}
	return res, err
}

// refreshLoop is one short research with a single submit tool.
type refreshLoop struct {
	guide, task string
	tool        anthropic.BetaToolParam
	what        string
	caller      Caller
	ref         string
	maxCost     float64
	progress    func(string)
	// check validates a submit; it returns the problems and whether the
	// submit was accepted.
	check func(raw []byte) (string, bool)
}

func (a *Agent) refreshProgress(ctx context.Context, f func(string)) func(string) {
	return func(s string) {
		if f != nil {
			f(a.Store.Scrub(ctx, s))
		}
	}
}

// runRefreshLoop runs the model with web search, web fetch and the submit
// tool until a submit is accepted, the turns run out or the budget does. It
// returns the model that answered and what the calls cost.
func (a *Agent) runRefreshLoop(ctx context.Context, l refreshLoop) (string, float64, error) {
	model := a.ModelFor(ctx, FeatureRefresh).Model
	cost := 0.0
	system := []anthropic.BetaTextBlockParam{{Text: l.guide, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()}}
	if r := a.rulesBlock(ctx); r != "" {
		system = append(system, anthropic.BetaTextBlockParam{Text: r})
	}
	messages := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(l.task))}
	tool := l.tool
	tools := []anthropic.BetaToolUnionParam{
		{OfWebSearchTool20260209: &anthropic.BetaWebSearchTool20260209Param{MaxUses: anthropic.Int(RefreshMaxSearches)}},
		{OfWebFetchTool20260209: &anthropic.BetaWebFetchTool20260209Param{MaxUses: anthropic.Int(RefreshMaxFetches), MaxContentTokens: anthropic.Int(refreshFetchTokens)}},
		{OfTool: &tool},
	}
	submits := 0
	for turn := 1; turn <= RefreshMaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return model, cost, err
		}
		resp, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:     anthropic.Model(model),
			MaxTokens: refreshMaxTokens,
			System:    system,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return model, cost, refreshCallErr(err)
		}
		cost += a.Record(ctx, FeatureRefresh, l.caller, l.ref, resp)
		if string(resp.Model) != "" {
			model = string(resp.Model)
		}
		messages = append(messages, resp.ToParam())

		results := []anthropic.BetaContentBlockParamUnion{}
		done := false
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaServerToolUseBlock:
				in, _ := json.Marshal(b.Input)
				var q struct {
					Query string `json:"query"`
					URL   string `json:"url"`
				}
				_ = json.Unmarshal(in, &q)
				switch {
				case b.Name == "web_fetch" && q.URL != "":
					l.progress("Reading " + q.URL)
				case b.Name == "web_search" && q.Query != "":
					l.progress("Searching: " + q.Query)
				}
			case anthropic.BetaToolUseBlock:
				if b.Name != l.tool.Name {
					results = append(results, anthropic.NewBetaToolResultBlock(b.ID, "unknown tool "+b.Name, true))
					continue
				}
				submits++
				out, ok := l.check([]byte(b.JSON.Input.Raw()))
				if ok || submits >= refreshMaxSubmits {
					if !ok {
						return model, cost, errors.New("the " + l.what + " did not validate: " + out)
					}
					done = true
					results = append(results, anthropic.NewBetaToolResultBlock(b.ID, "Accepted.", false))
					continue
				}
				results = append(results, anthropic.NewBetaToolResultBlock(b.ID, out+"\nFix these and submit again.", true))
			}
		}
		if done {
			return model, cost, nil
		}
		if l.maxCost > 0 && cost >= l.maxCost {
			return model, cost, ErrRefreshBudget
		}
		switch resp.StopReason {
		case anthropic.BetaStopReasonToolUse:
			messages = append(messages, anthropic.NewBetaUserMessage(results...))
		case anthropic.BetaStopReasonPauseTurn:
			if len(results) > 0 {
				messages = append(messages, anthropic.NewBetaUserMessage(results...))
			}
		case anthropic.BetaStopReasonRefusal:
			return model, cost, fmt.Errorf("Claude declined this request (%s)", resp.StopDetails.Category)
		default:
			blocks := append(results, anthropic.NewBetaTextBlock(fmt.Sprintf(
				"Call %s now with the full %s, or with verified false if you could not read the pages.", l.tool.Name, l.what)))
			messages = append(messages, anthropic.NewBetaUserMessage(blocks...))
		}
	}
	return model, cost, fmt.Errorf("stopped after %d turns without a valid %s", RefreshMaxTurns, l.what)
}

// checkOffers parses a submit_offers call and validates the offers inside
// the provider's spec. On success it fills res and stamps checked_at.
func checkOffers(spec *providers.Spec, raw []byte, res *OffersResult) (string, bool) {
	var in struct {
		Offers   []providers.Offer `json:"offers"`
		Verified bool              `json:"verified"`
		Summary  string            `json:"summary"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "INVALID_JSON: " + err.Error(), false
	}
	if in.Offers == nil {
		in.Offers = []providers.Offer{}
	}
	cand := *spec
	cand.Offers = in.Offers
	var problems []string
	for _, e := range cand.Validate() {
		if strings.HasPrefix(e, "offers") {
			problems = append(problems, e)
		}
	}
	if len(problems) > 0 {
		return "Not accepted:\n- " + strings.Join(problems, "\n- "), false
	}
	if in.Verified {
		today := time.Now().UTC().Format("2006-01-02")
		for i := range in.Offers {
			in.Offers[i].CheckedAt = today
		}
	}
	res.Offers, res.Verified, res.Summary = in.Offers, in.Verified, strings.TrimSpace(in.Summary)
	return "", true
}

// refreshTask is the first message: the provider, its current offers and
// where to read. Only public metadata goes out.
func refreshTask(p *store.Provider, spec *providers.Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "Task: re-check the offers of %s (%s).\n", p.Name, p.Slug)
	if p.Description != "" {
		fmt.Fprintf(&b, "What it is: %s\n", p.Description)
	}
	fmt.Fprintf(&b, "Base URL: %s\n", spec.BaseURL)
	urls := []string{}
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	for _, o := range spec.Offers {
		add(o.SourceURL)
	}
	add(p.Website)
	add(p.DocsURL)
	if len(urls) > 0 {
		b.WriteString("\nPages to start from (pricing and limits first):\n")
		for _, u := range urls {
			b.WriteString("- " + u + "\n")
		}
	}
	if spec.Quota.FreeTier != "" {
		fmt.Fprintf(&b, "\nFree tier as recorded: %s\n", spec.Quota.FreeTier)
	}
	if len(spec.Examples) > 0 {
		names := []string{}
		for _, e := range spec.Examples {
			names = append(names, fmt.Sprintf("%s (%s %s)", e.Name, strings.ToUpper(e.Method), e.Path))
		}
		fmt.Fprintf(&b, "\nExamples an offer may match by name: %s\n", strings.Join(names, "; "))
	}
	cur, _ := json.MarshalIndent(spec.Offers, "", "  ")
	if len(spec.Offers) == 0 {
		cur = []byte("[]")
	}
	fmt.Fprintf(&b, "\nCurrent offers:\n```json\n%s\n```\n", cur)
	b.WriteString("\nRead the pages, then call submit_offers with the full list.")
	return b.String()
}

// RefreshRef is the ledger ref of a refresh run.
func RefreshRef(runID int64) string { return fmt.Sprintf("refresh:%d", runID) }

// refreshCallErr turns a failed Claude call into one short line for the run
// history, without the raw response body.
func refreshCallErr(err error) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("AI request failed: %w", err)
	}
	why := ""
	switch apiErr.StatusCode {
	case 401, 403:
		why = ": check ANTHROPIC_API_KEY"
	case 404:
		why = ": the refresh model may not exist; pick another in Settings"
	case 429, 529:
		why = ": Anthropic is busy or rate limited; the next run tries again"
	}
	return fmt.Errorf("AI request failed: Anthropic answered %d%s", apiErr.StatusCode, why)
}
