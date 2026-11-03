package refresh

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// FieldChange is one field of an offer that changed, as display text.
type FieldChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// OfferChange is one offer present before and after with different fields.
type OfferChange struct {
	Name   string        `json:"name"`
	Fields []FieldChange `json:"fields"`
	// SourceURL is the page the new version of the offer cites.
	SourceURL string `json:"source_url"`
}

// Diff is how a proposed offers list differs from the current one. It
// ignores checked_at, which every check moves.
type Diff struct {
	Added   []providers.Offer `json:"added"`
	Removed []providers.Offer `json:"removed"`
	Changed []OfferChange     `json:"changed"`
}

// Empty reports whether nothing changed.
func (d Diff) Empty() bool { return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 }

// Summary is one line: "1 added (Free plan), 1 changed (Search: price)".
func (d Diff) Summary() string {
	if d.Empty() {
		return "no change"
	}
	parts := []string{}
	names := func(os []providers.Offer) string {
		n := []string{}
		for _, o := range os {
			n = append(n, o.Name)
		}
		return strings.Join(n, ", ")
	}
	if len(d.Added) > 0 {
		parts = append(parts, fmt.Sprintf("%d added (%s)", len(d.Added), names(d.Added)))
	}
	if len(d.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("%d removed (%s)", len(d.Removed), names(d.Removed)))
	}
	if len(d.Changed) > 0 {
		c := []string{}
		for _, ch := range d.Changed {
			f := []string{}
			for _, x := range ch.Fields {
				f = append(f, x.Field)
			}
			c = append(c, ch.Name+": "+strings.Join(f, ", "))
		}
		parts = append(parts, fmt.Sprintf("%d changed (%s)", len(d.Changed), strings.Join(c, "; ")))
	}
	return strings.Join(parts, ", ")
}

// DiffOffers compares offers by name, case insensitively.
func DiffOffers(cur, next []providers.Offer) Diff {
	d := Diff{Added: []providers.Offer{}, Removed: []providers.Offer{}, Changed: []OfferChange{}}
	key := offerKey
	old := map[string]providers.Offer{}
	for _, o := range cur {
		old[key(o)] = o
	}
	seen := map[string]bool{}
	for _, n := range next {
		k := key(n)
		seen[k] = true
		o, ok := old[k]
		if !ok {
			d.Added = append(d.Added, n)
			continue
		}
		if f := offerFields(o, n); len(f) > 0 {
			d.Changed = append(d.Changed, OfferChange{Name: n.Name, Fields: f, SourceURL: n.SourceURL})
		}
	}
	for _, o := range cur {
		if !seen[key(o)] {
			d.Removed = append(d.Removed, o)
		}
	}
	return d
}

func offerFields(a, b providers.Offer) []FieldChange {
	out := []FieldChange{}
	add := func(field string, x, y any, show func(any) string) {
		if !reflect.DeepEqual(x, y) {
			out = append(out, FieldChange{Field: field, From: show(x), To: show(y)})
		}
	}
	js := func(v any) string {
		b, _ := json.Marshal(v)
		if s := string(b); s != "null" && s != `""` && s != "0" && s != "[]" {
			return s
		}
		return "none"
	}
	plain := func(v any) string {
		s := fmt.Sprint(v)
		if s == "" || s == "0" {
			return "none"
		}
		return s
	}
	add("match", normMatch(a.Match), normMatch(b.Match), js)
	add("price", normPrice(a.Price), normPrice(b.Price), js)
	add("free", a.Free, b.Free, plain)
	add("limits", normLimits(a.Limits), normLimits(b.Limits), js)
	add("concurrency", a.Concurrency, b.Concurrency, plain)
	add("notes", strings.Join(strings.Fields(a.Notes), " "), strings.Join(strings.Fields(b.Notes), " "), plain)
	add("source_url", a.SourceURL, b.SourceURL, plain)
	return out
}

func normMatch(m *providers.OfferMatch) *providers.OfferMatch {
	if m == nil || (m.Method == "" && m.Path == "" && len(m.Examples) == 0) {
		return nil
	}
	c := *m
	c.Method = strings.ToUpper(c.Method)
	if len(c.Examples) == 0 {
		c.Examples = nil
	}
	return &c
}

func normPrice(p *providers.Price) *providers.Price {
	if p == nil {
		return nil
	}
	c := *p
	if c.Currency == "USD" {
		c.Currency = ""
	}
	return &c
}

func normLimits(l []providers.Limit) []providers.Limit {
	if len(l) == 0 {
		return nil
	}
	out := make([]providers.Limit, len(l))
	for i, x := range l {
		if x.Unit == "requests" || x.Unit == "request" {
			x.Unit = ""
		}
		out[i] = x
	}
	return out
}

// maxAutoRatio bounds how far a price may move on its own, either way.
const maxAutoRatio = 4.0

// AutoApply decides whether a change may publish without review. The
// refresh model reads pages anyone can write, so only a small, bounded
// change applies on its own: existing offers whose price moves within
// maxAutoRatio or whose limits loosen, each citing a page on one of the
// provider's own domains. Adding, removing or retargeting an offer, new
// notes, a free flip and any tighter limit wait for a superadmin. It
// returns the reason when it may not.
func AutoApply(d Diff, cur, next []providers.Offer, domains []string) (bool, string) {
	if len(next) == 0 {
		return false, "the check would remove every offer"
	}
	if len(d.Removed) > 0 {
		return false, fmt.Sprintf("the check would remove the offer %q", d.Removed[0].Name)
	}
	if len(d.Added) > 0 {
		if o := d.Added[0]; !httpURL(o.SourceURL) {
			return false, fmt.Sprintf("the new offer %q cites no source page", o.Name)
		}
		return false, fmt.Sprintf("the check adds the offer %q", d.Added[0].Name)
	}
	old := map[string]providers.Offer{}
	for _, o := range cur {
		old[offerKey(o)] = o
	}
	nu := map[string]providers.Offer{}
	for _, o := range next {
		nu[offerKey(o)] = o
	}
	for _, c := range d.Changed {
		if !httpURL(c.SourceURL) {
			return false, fmt.Sprintf("the change to %q cites no source page", c.Name)
		}
		if !agent.OnDomains(c.SourceURL, domains) {
			return false, fmt.Sprintf("the change to %q cites %s, which is not on the provider's own site", c.Name, c.SourceURL)
		}
		a, b := old[offerKey(providers.Offer{Name: c.Name})], nu[offerKey(providers.Offer{Name: c.Name})]
		for _, f := range c.Fields {
			switch f.Field {
			case "source_url":
			case "price":
				if !priceWithin(a.Price, b.Price) {
					return false, fmt.Sprintf("the price of %q moves more than %gx or changes unit", c.Name, maxAutoRatio)
				}
			case "limits":
				if !limitsLooser(a.Limits, b.Limits) {
					return false, fmt.Sprintf("the change tightens the limits of %q", c.Name)
				}
			case "concurrency":
				if (b.Concurrency != 0 && b.Concurrency < a.Concurrency) || (a.Concurrency == 0 && b.Concurrency > 0) {
					return false, fmt.Sprintf("the change tightens the concurrency of %q", c.Name)
				}
			case "match":
				return false, fmt.Sprintf("the change moves which requests %q covers", c.Name)
			case "free":
				return false, fmt.Sprintf("the change flips whether %q is free", c.Name)
			default:
				return false, fmt.Sprintf("the change rewrites the %s of %q", f.Field, c.Name)
			}
		}
	}
	return true, ""
}

func offerKey(o providers.Offer) string { return strings.ToLower(strings.TrimSpace(o.Name)) }

// priceWithin reports whether a price moved within maxAutoRatio and kept
// its unit, currency and per request use. A price appearing, vanishing or
// leaving zero is not within.
func priceWithin(a, b *providers.Price) bool {
	a, b = normPrice(a), normPrice(b)
	if a == nil || b == nil || a.Unit != b.Unit || a.Currency != b.Currency || a.PerRequest != b.PerRequest {
		return false
	}
	return ratioWithin(a.Amount, b.Amount)
}

func ratioWithin(from, to float64) bool {
	if from == to {
		return true
	}
	if from <= 0 || to <= 0 || math.IsNaN(from) || math.IsNaN(to) {
		return false
	}
	r := to / from
	return r <= maxAutoRatio && r >= 1/maxAutoRatio
}

// limitsLooser reports whether every new limit has an old one over the same
// window and unit with a count at least as high. Dropping a limit loosens.
func limitsLooser(a, b []providers.Limit) bool {
	old := map[string]int64{}
	for _, l := range normLimits(a) {
		old[l.Per+"|"+l.Unit] = l.Count
	}
	for _, l := range normLimits(b) {
		c, ok := old[l.Per+"|"+l.Unit]
		if !ok || l.Count < c {
			return false
		}
	}
	return true
}

// cleanOffers makes the model's offers safe to store and show: names and
// notes lose control and invisible format characters and runs of
// whitespace, and a source_url that is not a plain http(s) URL is dropped,
// so a change citing it waits for review.
func cleanOffers(offers []providers.Offer) []providers.Offer {
	out := make([]providers.Offer, len(offers))
	for i, o := range offers {
		o.Name = cleanText(o.Name)
		o.Notes = cleanText(o.Notes)
		o.SourceURL = strings.TrimSpace(o.SourceURL)
		if u, err := url.Parse(o.SourceURL); o.SourceURL != "" && (err != nil || u.User != nil || !httpURL(o.SourceURL)) {
			o.SourceURL = ""
		}
		out[i] = o
	}
	return out
}

func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// keepCheckedAt copies only checked_at from a no change proposal onto the
// current offers, so order, name spelling and every other field stay as
// stored.
func keepCheckedAt(cur, next []providers.Offer) []providers.Offer {
	at := map[string]string{}
	for _, o := range next {
		at[offerKey(o)] = o.CheckedAt
	}
	out := append([]providers.Offer{}, cur...)
	for i := range out {
		if c, ok := at[offerKey(out[i])]; ok && c != "" {
			out[i].CheckedAt = c
		}
	}
	return out
}

func httpURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// DiffPrices compares proposed model prices with the current ones and
// returns the diff (one change per model, by field) and the proposed
// entries that changed. Models the proposal leaves out keep their price.
func DiffPrices(cur, next []agent.ModelPrice) (Diff, []agent.ModelPrice) {
	d := Diff{Added: []providers.Offer{}, Removed: []providers.Offer{}, Changed: []OfferChange{}}
	old := map[string]agent.ModelPrice{}
	for _, p := range cur {
		old[p.Model] = p
	}
	changed := []agent.ModelPrice{}
	usd := func(v float64) string { return "$" + strconv.FormatFloat(v, 'f', -1, 64) + " / MTok" }
	for _, n := range next {
		o, ok := old[n.Model]
		if !ok {
			continue
		}
		f := []FieldChange{}
		for _, x := range []struct {
			name string
			a, b float64
		}{{"input", o.Input, n.Input}, {"output", o.Output, n.Output}, {"cache_write", o.CacheWrite, n.CacheWrite}, {"cache_read", o.CacheRead, n.CacheRead}} {
			if math.Abs(x.a-x.b) > 1e-9 {
				f = append(f, FieldChange{Field: x.name, From: usd(x.a), To: usd(x.b)})
			}
		}
		if len(f) > 0 {
			d.Changed = append(d.Changed, OfferChange{Name: n.Model, Fields: f, SourceURL: n.SourceURL})
			changed = append(changed, n)
		}
	}
	return d, changed
}

// AutoApplyPrices allows a price change to apply on its own only when every
// changed price cites an https page on Anthropic's own sites and moves each
// field within maxAutoRatio of the current price.
func AutoApplyPrices(cur, changed []agent.ModelPrice) (bool, string) {
	old := map[string]agent.ModelPrice{}
	for _, p := range cur {
		old[p.Model] = p
	}
	for _, p := range changed {
		if !httpURL(p.SourceURL) {
			return false, fmt.Sprintf("the new price of %s cites no source page", p.Model)
		}
		if u, _ := url.Parse(strings.TrimSpace(p.SourceURL)); u.Scheme != "https" || !agent.OnDomains(p.SourceURL, agent.PriceSourceDomains) {
			return false, fmt.Sprintf("the new price of %s cites %s, which is not an Anthropic page", p.Model, p.SourceURL)
		}
		o, ok := old[p.Model]
		if !ok {
			return false, fmt.Sprintf("%s has no current price to compare with", p.Model)
		}
		for _, x := range []struct {
			name string
			a, b float64
		}{{"input", o.Input, p.Input}, {"output", o.Output, p.Output}, {"cache_write", o.CacheWrite, p.CacheWrite}, {"cache_read", o.CacheRead, p.CacheRead}} {
			if math.Abs(x.a-x.b) > 1e-9 && !ratioWithin(x.a, x.b) {
				return false, fmt.Sprintf("the %s price of %s moves more than %gx", x.name, p.Model, maxAutoRatio)
			}
		}
	}
	return true, ""
}
