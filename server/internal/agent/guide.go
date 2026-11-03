package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Caps for writing one provider's usage guide. It reads an API reference,
// which takes more pages than a pricing check.
const (
	GuideMaxTurns    = 12
	GuideMaxSearches = 4
	GuideMaxFetches  = 10
	guideMaxTokens   = 12000
)

// GuideRequest asks for one provider's usage guide to be written or rewritten.
type GuideRequest struct {
	Provider *store.Provider
	Caller   Caller
	// Instruction is optional guidance from the operator, such as "focus on
	// the chat endpoints".
	Instruction string
	// MaxCostUSD stops the research once it has spent this much; 0 is no cap.
	MaxCostUSD float64
	Progress   func(string)
}

// GuideResult is the guide the model wrote.
type GuideResult struct {
	// Verified is false when the model could not read the provider's docs;
	// Guide is then nil and nothing should change.
	Verified bool             `json:"verified"`
	Guide    *providers.Guide `json:"guide"`
	Summary  string           `json:"summary"`
	Model    string           `json:"model"`
	CostUSD  float64          `json:"cost_usd"`
}

// GuideRef is the ledger ref of a guide research.
func GuideRef(slug string) string { return "guide:" + slug }

const guideGuide = `You write the usage guide of one provider in Acnologia, a self-hosted API key pool. Apps and AI agents call the provider through Acnologia's proxy with their own app token; Acnologia adds the provider's API key and rotates keys on rate limits. The guide teaches a caller which endpoints to use and how to call them correctly the first time.

The guide: {summary (1 to 3 sentences: what the API is for and when to pick it), endpoints [{name, method (GET, POST, PUT, PATCH or DELETE), path (relative to the base URL, as in the examples, starting with /), description, params [{name, in: query | path | body | header, type, required, description}], body (an example JSON request body for POST, PUT or PATCH), response (what comes back and the fields that matter)}], notes ["..."], markdown (optional free form extra, rarely needed)}.

# How to work
1. Read the provider's API reference. Start from the URLs given; search only when one is missing or moved.
2. Pick the endpoints worth knowing, most useful first, usually 2 to 8. Give each the params a caller actually sets, an example body when it takes one, and what the response holds.
3. Write notes for facts an agent gets wrong without them: pagination, model names, streaming, error shapes, size limits, cost traps.
4. Call submit_guide once with the full guide.

# Hard rules
- Never describe auth, API keys or key headers: Acnologia adds the key and callers only send their app token.
- Paths are relative to the base URL given. Do not repeat the base URL's own path prefix.
- Base every endpoint and param on the docs. Never invent endpoints, params or model names.
- Never put an API key, token or secret in the guide.
- If you cannot read the provider's docs, call submit_guide with verified false and no guide.
- Your text between tool calls should be one short progress sentence.`

var guideTool = anthropic.BetaToolParam{
	Name:        "submit_guide",
	Description: anthropic.String("Submit the provider's full usage guide. The harness validates it and may send problems back for one more try."),
	InputSchema: schema(map[string]any{
		"guide":    map[string]any{"type": "object", "description": "The usage guide as described in the system prompt: summary, endpoints, notes, optional markdown."},
		"verified": map[string]any{"type": "boolean", "description": "true when you read the provider's API reference; false when you could not."},
		"summary":  map[string]any{"type": "string", "description": "One to three sentences for the operator: what the guide covers and the pages you read."},
	}, "verified", "summary"),
}

// ResearchGuide writes a provider's usage guide with the builder's model,
// web search and web fetch. It sends the provider's public metadata, base
// URL, examples and offers, never keys, and every request still passes the
// redaction middleware. Saving the guide is the caller's job.
func (a *Agent) ResearchGuide(ctx context.Context, req GuideRequest) (*GuideResult, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	p := req.Provider
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		return nil, fmt.Errorf("the provider's spec is unreadable: %w", err)
	}
	res := &GuideResult{}
	progress := a.refreshProgress(ctx, req.Progress)
	progress("Reading " + p.Name + "'s API reference")
	instruction := strings.TrimSpace(a.Store.Scrub(ctx, req.Instruction))
	loop := refreshLoop{guide: guideGuide, task: guideTask(p, spec, instruction), tool: guideTool, what: "guide",
		caller: req.Caller, ref: GuideRef(p.Slug), maxCost: req.MaxCostUSD, progress: progress,
		check:   func(raw []byte) (string, bool) { return checkGuide(spec, raw, res) },
		feature: FeatureBuilder, maxTurns: GuideMaxTurns, searches: GuideMaxSearches, fetches: GuideMaxFetches, maxTokens: guideMaxTokens}
	res.Model, res.CostUSD, err = a.runRefreshLoop(ctx, loop)
	return res, err
}

// checkGuide parses a submit_guide call and validates the guide inside the
// provider's spec. A verified guide needs at least one endpoint.
func checkGuide(spec *providers.Spec, raw []byte, res *GuideResult) (string, bool) {
	var in struct {
		Guide    *providers.Guide `json:"guide"`
		Verified bool             `json:"verified"`
		Summary  string           `json:"summary"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "INVALID_JSON: " + err.Error(), false
	}
	if !in.Verified {
		res.Verified, res.Guide, res.Summary = false, nil, strings.TrimSpace(in.Summary)
		return "", true
	}
	if in.Guide == nil {
		return "Not accepted:\n- guide is required when verified is true", false
	}
	cand := *spec
	cand.Guide = in.Guide
	problems := GuideGaps(&cand)
	for _, e := range cand.Validate() {
		if strings.HasPrefix(e, "guide") {
			problems = append(problems, e)
		}
	}
	if len(problems) > 0 {
		return "Not accepted:\n- " + strings.Join(problems, "\n- "), false
	}
	res.Verified, res.Guide, res.Summary = true, in.Guide, strings.TrimSpace(in.Summary)
	return "", true
}

// GuideGaps names what a spec's guide lacks for a caller to use it: no guide
// at all, or one without endpoints. Field level problems come from Validate.
func GuideGaps(s *providers.Spec) []string {
	if s.Guide == nil {
		return []string{"guide is missing: add a summary, the main endpoints with their params, and notes from the API reference"}
	}
	if len(s.Guide.Endpoints) == 0 {
		return []string{"guide has no endpoints: list the main endpoints with their params"}
	}
	return nil
}

// guideTask is the first message: the provider, what the spec already knows
// and where to read. Only public metadata goes out.
func guideTask(p *store.Provider, spec *providers.Spec, instruction string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\n", time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "Task: write the usage guide of %s (%s).\n", p.Name, p.Slug)
	if p.Description != "" {
		fmt.Fprintf(&b, "What it is: %s\n", p.Description)
	}
	fmt.Fprintf(&b, "Base URL (paths are relative to it): %s\n", spec.BaseURL)
	urls := []string{}
	seen := map[string]bool{}
	for _, u := range []string{p.DocsURL, p.Website} {
		if u = strings.TrimSpace(u); u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	if len(urls) > 0 {
		b.WriteString("\nPages to start from (API reference first):\n")
		for _, u := range urls {
			b.WriteString("- " + u + "\n")
		}
	}
	if len(spec.Examples) > 0 {
		ex, _ := json.MarshalIndent(spec.Examples, "", "  ")
		fmt.Fprintf(&b, "\nExample requests in the spec:\n```json\n%s\n```\n", ex)
	}
	if len(spec.Offers) > 0 {
		names := []string{}
		for i := range spec.Offers {
			names = append(names, spec.Offers[i].Name+": "+spec.Offers[i].Summary())
		}
		fmt.Fprintf(&b, "\nOffers (pricing is shown separately; mention cost traps only): %s\n", strings.Join(names, "; "))
	}
	if spec.Guide != nil {
		cur, _ := json.MarshalIndent(spec.Guide, "", "  ")
		fmt.Fprintf(&b, "\nCurrent guide (keep what the docs still support, fix what is wrong, fill what is missing):\n```json\n%s\n```\n", cur)
	}
	if instruction != "" {
		fmt.Fprintf(&b, "\nOperator's notes:\n<notes>\n%s\n</notes>\n", instruction)
	}
	b.WriteString("\nRead the docs, then call submit_guide with the full guide.")
	return b.String()
}
