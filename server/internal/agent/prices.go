package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// PricesSourceURL is Anthropic's official model pricing page: where the
// built in table comes from and what the price refresh reads.
const PricesSourceURL = "https://platform.claude.com/docs/en/about-claude/pricing"

// PriceSourceDomains are Anthropic's official sites: the only pages the
// price refresh reads, and the only sources a price change may cite to
// apply on its own. Subdomains such as platform.claude.com are included.
var PriceSourceDomains = []string{"anthropic.com", "claude.com"}

// BuiltinPricesChecked is when the built in price table was last read.
const BuiltinPricesChecked = "2026-11-03"

// metaPrices is the meta key holding PriceSettings.
const metaPrices = "ai_prices"

// PriceOverride replaces one model's built in price (and optionally its
// context window). Token prices are USD per million tokens. A zero cache
// price means the usual ratio to the input price: writes 1.25x, reads 0.1x.
type PriceOverride struct {
	Input         float64 `json:"input"`
	Output        float64 `json:"output"`
	CacheWrite    float64 `json:"cache_write"`
	CacheRead     float64 `json:"cache_read"`
	ContextWindow int     `json:"context_window,omitempty"`
	SourceURL     string  `json:"source_url,omitempty"`
	CheckedAt     string  `json:"checked_at,omitempty"`
	// Source is manual (Settings) or refresh (the price refresh).
	Source    string     `json:"source"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// PriceSettings is every override plus when the price refresh last read the
// pricing page.
type PriceSettings struct {
	Models map[string]PriceOverride `json:"models"`
	// CheckedAt (YYYY-MM-DD) and CheckedURL are the price refresh's last
	// successful read, whether or not it changed anything.
	CheckedAt  string `json:"checked_at,omitempty"`
	CheckedURL string `json:"checked_url,omitempty"`
}

type priceState struct {
	mu sync.RWMutex
	s  *PriceSettings
}

// Prices is the saved overrides.
func (a *Agent) Prices(ctx context.Context) PriceSettings {
	a.prices.mu.RLock()
	s := a.prices.s
	a.prices.mu.RUnlock()
	if s == nil {
		var loaded PriceSettings
		if raw := a.Store.GetMeta(ctx, metaPrices); raw != "" {
			if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
				slog.Warn("saved AI price overrides are unreadable, using the built in table", "err", err)
				loaded = PriceSettings{}
			}
		}
		a.prices.mu.Lock()
		if a.prices.s == nil {
			a.prices.s = &loaded
		}
		s = a.prices.s
		a.prices.mu.Unlock()
	}
	out := *s
	out.Models = map[string]PriceOverride{}
	for k, v := range s.Models {
		out.Models[k] = v
	}
	return out
}

func (a *Agent) savePrices(ctx context.Context, p PriceSettings) error {
	raw, _ := json.Marshal(p)
	if err := a.Store.SetMeta(ctx, metaPrices, string(raw)); err != nil {
		return err
	}
	a.prices.mu.Lock()
	a.prices.s = &p
	a.prices.mu.Unlock()
	return nil
}

// override finds a model's override, matching a dated id to its base.
func (a *Agent) override(model string) (PriceOverride, bool) {
	ps := a.Prices(context.Background())
	if o, ok := ps.Models[model]; ok {
		return o, true
	}
	if base := datedSuffix.ReplaceAllString(model, ""); base != model {
		o, ok := ps.Models[base]
		return o, ok
	}
	return PriceOverride{}, false
}

// PriceOf is the price a model is charged at now: its override, else the
// built in table. ok is false for a model neither knows.
func (a *Agent) PriceOf(model string) (Price, bool) {
	base, known := KnownPrice(model)
	o, ok := a.override(model)
	if !ok {
		return base, known
	}
	if !known {
		base = Price{WebSearch: defaultPrice.WebSearch}
	}
	p := base
	p.Input, p.Output = o.Input, o.Output
	p.CacheWrite, p.CacheRead = o.CacheWrite, o.CacheRead
	if p.CacheWrite == 0 {
		p.CacheWrite = o.Input * 1.25
	}
	if p.CacheRead == 0 {
		p.CacheRead = o.Input * 0.1
	}
	return p, true
}

// EffectivePrice is PriceOf with the default model's price for unknown models.
func (a *Agent) EffectivePrice(model string) Price {
	if p, ok := a.PriceOf(model); ok {
		return p
	}
	return defaultPrice
}

// CostOf is Cost at the price in effect now, overrides included.
func (a *Agent) CostOf(model string, t Tokens) float64 { return costAt(a.EffectivePrice(model), t) }

// ErrInvalidPrice marks a price override that failed validation.
var ErrInvalidPrice = errors.New("invalid price")

func validPrice(o PriceOverride) error {
	bad := func(f string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidPrice, fmt.Sprintf(f, args...))
	}
	for _, x := range []struct {
		name string
		v    float64
		min  float64
	}{{"input", o.Input, 0.0001}, {"output", o.Output, 0.0001}, {"cache_write", o.CacheWrite, 0}, {"cache_read", o.CacheRead, 0}} {
		if math.IsNaN(x.v) || x.v < x.min || x.v > 1000 {
			return bad("%s must be %g to 1000 USD per million tokens", x.name, x.min)
		}
	}
	if o.ContextWindow < 0 || o.ContextWindow > 100_000_000 {
		return bad("context_window must be 0 to 100,000,000 tokens")
	}
	if o.SourceURL != "" && !httpLink(o.SourceURL) {
		return bad("source_url must be an http(s) URL")
	}
	if o.CheckedAt != "" {
		if _, err := time.Parse("2006-01-02", o.CheckedAt); err != nil {
			return bad("checked_at must be a date like 2026-11-03")
		}
	}
	return nil
}

func httpLink(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// SetPrice saves one model's override and returns the one it replaced.
func (a *Agent) SetPrice(ctx context.Context, model string, o PriceOverride, by string) (*PriceOverride, error) {
	model = strings.TrimSpace(model)
	if !modelIDRe.MatchString(model) {
		return nil, fmt.Errorf("%w: %q is not a model id", ErrInvalidPrice, model)
	}
	if _, ok := KnownPrice(model); !ok {
		known := false
		for _, m := range a.ListModels(ctx).Models {
			known = known || m.ID == model
		}
		if _, ok := a.Prices(ctx).Models[model]; !known && !ok {
			return nil, fmt.Errorf("%w: %s is not an available model", ErrInvalidPrice, model)
		}
	}
	if err := validPrice(o); err != nil {
		return nil, err
	}
	if o.Source == "" {
		o.Source = "manual"
	}
	t := time.Now().UTC()
	o.UpdatedAt, o.UpdatedBy = &t, by
	ps := a.Prices(ctx)
	var old *PriceOverride
	if prev, ok := ps.Models[model]; ok {
		old = &prev
	}
	ps.Models[model] = o
	return old, a.savePrices(ctx, ps)
}

// ResetPrice drops a model's override, back to the built in table. It
// returns the override it removed, nil when there was none.
func (a *Agent) ResetPrice(ctx context.Context, model string) (*PriceOverride, error) {
	ps := a.Prices(ctx)
	prev, ok := ps.Models[model]
	if !ok {
		return nil, nil
	}
	delete(ps.Models, model)
	return &prev, a.savePrices(ctx, ps)
}

// ModelPrice is one model's token prices, for the price refresh.
type ModelPrice struct {
	Model      string  `json:"model"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
	SourceURL  string  `json:"source_url,omitempty"`
}

// CurrentPrices is the price in effect for every model Acnologia prices:
// the built in models, models with an override and the models in use.
func (a *Agent) CurrentPrices(ctx context.Context) []ModelPrice {
	ids := map[string]bool{}
	for _, b := range builtinModels {
		ids[b.id] = true
	}
	for id := range a.Prices(ctx).Models {
		ids[id] = true
	}
	for _, c := range a.EffectiveModels(ctx) {
		if _, ok := a.PriceOf(c.Model); ok {
			ids[c.Model] = true
		}
	}
	out := []ModelPrice{}
	for id := range ids {
		p, ok := a.PriceOf(id)
		if !ok {
			continue
		}
		out = append(out, ModelPrice{Model: id, Input: p.Input, Output: p.Output, CacheWrite: p.CacheWrite, CacheRead: p.CacheRead})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// ApplyPrices saves refreshed prices as overrides, keeping any context
// window override, and marks the pricing page as read today.
func (a *Agent) ApplyPrices(ctx context.Context, prices []ModelPrice, by string) error {
	ps := a.Prices(ctx)
	t := time.Now().UTC()
	today := t.Format("2006-01-02")
	for _, p := range prices {
		o := ps.Models[p.Model]
		o.Input, o.Output, o.CacheWrite, o.CacheRead = p.Input, p.Output, p.CacheWrite, p.CacheRead
		o.SourceURL, o.CheckedAt, o.Source = p.SourceURL, today, "refresh"
		o.UpdatedAt, o.UpdatedBy = &t, by
		if err := validPrice(o); err != nil {
			return fmt.Errorf("%s: %w", p.Model, err)
		}
		ps.Models[p.Model] = o
	}
	ps.CheckedAt, ps.CheckedURL = today, firstURL(prices)
	return a.savePrices(ctx, ps)
}

// MarkPricesChecked records a read of the pricing page that changed nothing.
func (a *Agent) MarkPricesChecked(ctx context.Context, source string) error {
	ps := a.Prices(ctx)
	ps.CheckedAt, ps.CheckedURL = time.Now().UTC().Format("2006-01-02"), source
	if ps.CheckedURL == "" {
		ps.CheckedURL = PricesSourceURL
	}
	return a.savePrices(ctx, ps)
}

// PricesCheckedAt is the last time the price refresh read the pricing page,
// zero when it never has.
func (a *Agent) PricesCheckedAt(ctx context.Context) time.Time {
	t, _ := time.Parse("2006-01-02", a.Prices(ctx).CheckedAt)
	return t
}

func firstURL(ps []ModelPrice) string {
	for _, p := range ps {
		if p.SourceURL != "" {
			return p.SourceURL
		}
	}
	return PricesSourceURL
}

// PricesRequest asks for Claude's model prices to be re-read.
type PricesRequest struct {
	Caller     Caller
	Ref        string
	MaxCostUSD float64
	Progress   func(string)
}

// PricesResult is what the price check found: the full list for the models
// asked about.
type PricesResult struct {
	Verified bool         `json:"verified"`
	Prices   []ModelPrice `json:"prices"`
	Summary  string       `json:"summary"`
	Model    string       `json:"model"`
	CostUSD  float64      `json:"cost_usd"`
}

const pricesGuide = `You keep Acnologia's table of Claude model prices up to date. Acnologia uses it to account for what its own AI features cost.

For each model you are given, read Anthropic's official pricing page and report its list prices in USD per million tokens: input (base input tokens), output, cache_write (5 minute cache writes) and cache_read (cache hits and refreshes), with the source_url of the page that shows them.

# Hard rules
- Use only Anthropic's official pages (platform.claude.com, docs.claude.com, docs.anthropic.com, anthropic.com, claude.com).
- Report every model you were given, in the same units. Copy a price unchanged when the page still shows it.
- Never guess a price. If the page does not list a model, copy its current prices unchanged.
- If you cannot read the pricing page, call submit_prices with verified false and the current prices unchanged.
- Your text between tool calls should be one short progress sentence.`

var pricesTool = anthropic.BetaToolParam{
	Name:        "submit_prices",
	Description: anthropic.String("Submit the prices of every model you were given. The harness validates them and may send problems back for one more try."),
	InputSchema: schema(map[string]any{
		"prices": map[string]any{"type": "array", "description": "One entry per model: model, input, output, cache_write, cache_read (USD per million tokens), source_url.", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model": map[string]any{"type": "string"}, "input": map[string]any{"type": "number"}, "output": map[string]any{"type": "number"},
				"cache_write": map[string]any{"type": "number"}, "cache_read": map[string]any{"type": "number"}, "source_url": map[string]any{"type": "string"},
			},
			"required": []string{"model", "input", "output"},
		}},
		"verified": map[string]any{"type": "boolean", "description": "true when you read the pricing page; false when you could not."},
		"summary":  map[string]any{"type": "string", "description": "One or two sentences: what changed, or that nothing did, and the page you read."},
	}, "prices", "verified", "summary"),
}

// ResearchPrices re-reads Anthropic's pricing page with the refresh model.
func (a *Agent) ResearchPrices(ctx context.Context, req PricesRequest) (*PricesResult, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	cur := a.CurrentPrices(ctx)
	known := map[string]bool{}
	for _, p := range cur {
		known[p.Model] = true
	}
	res := &PricesResult{}
	progress := a.refreshProgress(ctx, req.Progress)
	progress("Reading Anthropic's pricing page")
	table, _ := json.MarshalIndent(cur, "", "  ")
	task := fmt.Sprintf("Today is %s.\n\nTask: re-check the prices of these Claude models.\n\nStart from %s.\n\nCurrent prices (USD per million tokens):\n```json\n%s\n```\n\nRead the page, then call submit_prices with every model above.",
		time.Now().UTC().Format("2006-01-02"), PricesSourceURL, table)
	loop := refreshLoop{guide: pricesGuide, task: task, tool: pricesTool, what: "price list",
		caller: req.Caller, ref: req.Ref, maxCost: req.MaxCostUSD, progress: progress, domains: PriceSourceDomains,
		check: func(raw []byte) (string, bool) { return checkPrices(known, raw, res) }}
	var err error
	res.Model, res.CostUSD, err = a.runRefreshLoop(ctx, loop)
	return res, err
}

func checkPrices(known map[string]bool, raw []byte, res *PricesResult) (string, bool) {
	var in struct {
		Prices   []ModelPrice `json:"prices"`
		Verified bool         `json:"verified"`
		Summary  string       `json:"summary"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "INVALID_JSON: " + err.Error(), false
	}
	problems := []string{}
	out := []ModelPrice{}
	for _, p := range in.Prices {
		if !known[p.Model] {
			continue // only the models asked about
		}
		if err := validPrice(PriceOverride{Input: p.Input, Output: p.Output, CacheWrite: p.CacheWrite, CacheRead: p.CacheRead, SourceURL: p.SourceURL}); err != nil {
			problems = append(problems, p.Model+": "+err.Error())
			continue
		}
		if p.CacheWrite == 0 {
			p.CacheWrite = p.Input * 1.25
		}
		if p.CacheRead == 0 {
			p.CacheRead = p.Input * 0.1
		}
		out = append(out, p)
	}
	if len(problems) > 0 {
		return "Not accepted:\n- " + strings.Join(problems, "\n- "), false
	}
	res.Prices, res.Verified, res.Summary = out, in.Verified, strings.TrimSpace(in.Summary)
	return "", true
}
