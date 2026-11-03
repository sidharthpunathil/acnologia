package refresh

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"

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
	key := func(o providers.Offer) string { return strings.ToLower(strings.TrimSpace(o.Name)) }
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

// AutoApply decides whether a change may publish without review. It may
// when every added or changed offer cites an http(s) source URL and the
// change does not remove every offer. It returns the reason when it may not.
func AutoApply(d Diff, next []providers.Offer) (bool, string) {
	if len(next) == 0 {
		return false, "the check would remove every offer"
	}
	for _, o := range d.Added {
		if !httpURL(o.SourceURL) {
			return false, fmt.Sprintf("the new offer %q cites no source page", o.Name)
		}
	}
	for _, c := range d.Changed {
		if !httpURL(c.SourceURL) {
			return false, fmt.Sprintf("the change to %q cites no source page", c.Name)
		}
	}
	return true, ""
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
// changed price cites an http(s) source page.
func AutoApplyPrices(changed []agent.ModelPrice) (bool, string) {
	for _, p := range changed {
		if !httpURL(p.SourceURL) {
			return false, fmt.Sprintf("the new price of %s cites no source page", p.Model)
		}
	}
	return true, ""
}
