package providers

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Offer is one priced product or endpoint of a provider: what it costs, what
// is free, and the limits the provider publishes for it. The pool paces keys
// to stay under the limits, cost tracking uses the price, and the AI tools,
// the catalog and the Copy for LLM brief show it.
type Offer struct {
	Name string `json:"name"`
	// Match says which requests the offer covers. Nil means provider wide.
	Match *OfferMatch `json:"match,omitempty"`
	Price *Price      `json:"price,omitempty"`
	// Free means requests under this offer cost nothing (within its limits).
	Free   bool    `json:"free,omitempty"`
	Limits []Limit `json:"limits,omitempty"`
	// Concurrency is how many requests one key may have in flight under
	// this offer; 0 means not published.
	Concurrency int    `json:"concurrency,omitempty"`
	Notes       string `json:"notes,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	// CheckedAt is the date (YYYY-MM-DD) someone read SourceURL.
	CheckedAt string `json:"checked_at,omitempty"`
}

// OfferMatch selects requests by method and path, or by the names of spec
// examples. Path is relative to the base URL and matches as a prefix; a *
// stands for any text inside one path segment, and a trailing $ makes the
// match exact.
type OfferMatch struct {
	Method   string   `json:"method,omitempty"`
	Path     string   `json:"path,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Price is the cost of one Unit. Amount is per single unit: $5 per 1,000
// queries is 0.005.
type Price struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency,omitempty"` // default USD
	Unit     string  `json:"unit"`
	// PerRequest is how many units one request uses, when that is fixed
	// (Tavily's basic search is 1 credit). Units that are one per call
	// (request, search, url, check) default to 1.
	PerRequest float64 `json:"per_request,omitempty"`
}

// Limit is a published cap: Count units per window.
type Limit struct {
	Count int64  `json:"count"`
	Per   string `json:"per"` // second | minute | hour | day | month
	Unit  string `json:"unit,omitempty"`
}

// PriceUnits are the units an offer price may use.
var PriceUnits = []string{"request", "search", "query", "url", "page", "step", "minute", "check", "token", "character", "credit", "image", "second", "session", "run", "gb"}

// callUnits are spent once per request, so one request costs one unit.
var callUnits = map[string]bool{"request": true, "search": true, "query": true, "url": true, "page": true, "check": true}

// LimitWindows maps a limit's per to its length. A month is 30 days.
var LimitWindows = map[string]time.Duration{
	"second": time.Second, "minute": time.Minute, "hour": time.Hour,
	"day": 24 * time.Hour, "month": 30 * 24 * time.Hour,
}

var methods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (s *Spec) validateOffers(add func(string, ...any)) {
	if len(s.Offers) > 50 {
		add("offers: at most 50")
	}
	names := map[string]bool{}
	examples := map[string]bool{}
	for _, e := range s.Examples {
		examples[e.Name] = true
	}
	units := map[string]bool{}
	for _, u := range PriceUnits {
		units[u] = true
	}
	for i, o := range s.Offers {
		label := fmt.Sprintf("offers[%d] %q", i, o.Name)
		name := strings.TrimSpace(o.Name)
		switch {
		case name == "":
			add("offers[%d]: name is required", i)
		case len(name) > 80:
			add("%s: name must be at most 80 characters", label)
		case names[strings.ToLower(name)]:
			add("%s: name is used twice", label)
		}
		names[strings.ToLower(name)] = true
		if m := o.Match; m != nil {
			if m.Method != "" && !methods[strings.ToUpper(m.Method)] {
				add("%s: match.method %q is not an HTTP method", label, m.Method)
			}
			if m.Path != "" {
				if !strings.HasPrefix(m.Path, "/") {
					add("%s: match.path must start with /", label)
				}
				if len(m.Path) > 300 {
					add("%s: match.path is too long", label)
				}
			}
			for _, ex := range m.Examples {
				if !examples[ex] {
					add("%s: match.examples names %q, which is not an example", label, ex)
				}
			}
			if m.Method == "" && m.Path == "" && len(m.Examples) == 0 {
				add("%s: match needs a method, a path or examples; leave it out for a provider wide offer", label)
			}
		}
		if p := o.Price; p != nil {
			if p.Amount < 0 || p.Amount > 1000 || math.IsNaN(p.Amount) {
				add("%s: price.amount must be 0 to 1000", label)
			}
			if p.Currency != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(p.Currency) {
				add("%s: price.currency must be a 3 letter code such as USD", label)
			}
			if !units[p.Unit] {
				add("%s: price.unit must be one of %s", label, strings.Join(PriceUnits, ", "))
			}
			if p.PerRequest < 0 || p.PerRequest > 1e6 {
				add("%s: price.per_request must be 0 to 1000000", label)
			}
			if o.Free && p.Amount > 0 {
				add("%s: a free offer cannot have a price above 0", label)
			}
		}
		if len(o.Limits) > 10 {
			add("%s: at most 10 limits", label)
		}
		for j, l := range o.Limits {
			if l.Count < 1 || l.Count > 1e12 {
				add("%s: limits[%d].count must be at least 1", label, j)
			}
			if _, ok := LimitWindows[l.Per]; !ok {
				add("%s: limits[%d].per must be second, minute, hour, day or month", label, j)
			}
			if len(l.Unit) > 40 {
				add("%s: limits[%d].unit is too long", label, j)
			}
		}
		if o.Concurrency < 0 || o.Concurrency > 1000 {
			add("%s: concurrency must be 0 to 1000", label)
		}
		if len(o.Notes) > 500 {
			add("%s: notes must be at most 500 characters", label)
		}
		if o.SourceURL != "" {
			if u, err := url.Parse(o.SourceURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				add("%s: source_url must be an http(s) URL", label)
			}
		}
		if o.CheckedAt != "" {
			if _, err := time.Parse("2006-01-02", o.CheckedAt); err != nil || !dateRe.MatchString(o.CheckedAt) {
				add("%s: checked_at must be a date like 2026-11-02", label)
			}
		}
	}
}

// Wide reports whether the offer covers every request.
func (o *Offer) Wide() bool { return o.Match == nil }

// matches reports whether the offer's match selects the request.
func (s *Spec) offerMatches(o *Offer, method, path string) bool {
	m := o.Match
	if m == nil {
		return true
	}
	for _, name := range m.Examples {
		for _, e := range s.Examples {
			if e.Name == name && strings.EqualFold(e.Method, method) && pathMatches(e.Path+"$", path) {
				return true
			}
		}
	}
	if m.Method == "" && m.Path == "" {
		return false
	}
	if m.Method != "" && !strings.EqualFold(m.Method, method) {
		return false
	}
	return m.Path == "" || pathMatches(m.Path, path)
}

// pathMatches checks a request path against an offer pattern: a prefix by
// default, * inside a segment, and $ at the end for an exact match. A query
// string on an example path is ignored.
func pathMatches(pattern, path string) bool {
	exact := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	if i := strings.IndexByte(pattern, '?'); i >= 0 {
		pattern = pattern[:i]
	}
	norm := func(p string) string {
		if p != "/" {
			p = strings.TrimRight(p, "/")
		}
		return p
	}
	pattern, path = norm(pattern), norm(path)
	if !strings.Contains(pattern, "*") {
		if exact {
			return path == pattern
		}
		return strings.HasPrefix(path, pattern)
	}
	re, err := cachedRegex(globRegex(pattern, exact))
	return err == nil && re.MatchString(path)
}

func globRegex(pattern string, exact bool) string {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re := "^" + strings.Join(parts, "[^/]*")
	if exact {
		return re + "/?$"
	}
	return re
}

// Applied is the offers that apply to one request: the first offer whose
// match selects it (Specific) and every provider wide offer (Wide).
type Applied struct {
	Specific *Offer
	Wide     []*Offer
}

// All lists the applied offers, the specific one first.
func (a Applied) All() []*Offer {
	if a.Specific == nil {
		return a.Wide
	}
	return append([]*Offer{a.Specific}, a.Wide...)
}

// Priced is the offer whose price decides the request's cost: the specific
// one, else the first provider wide offer.
func (a Applied) Priced() *Offer {
	if a.Specific != nil {
		return a.Specific
	}
	if len(a.Wide) > 0 {
		return a.Wide[0]
	}
	return nil
}

// OffersFor returns the offers that apply to a request. An empty method
// (a lease, where Acnologia never sees the request) gets the wide offers only.
func (s *Spec) OffersFor(method, path string) Applied {
	var a Applied
	if len(s.Offers) == 0 {
		return a
	}
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for i := range s.Offers {
		o := &s.Offers[i]
		if o.Wide() {
			a.Wide = append(a.Wide, o)
			continue
		}
		if a.Specific == nil && method != "" && s.offerMatches(o, method, path) {
			a.Specific = o
		}
	}
	return a
}

// RequestCost is the USD cost of one successful request under the offer,
// and whether the offer decides it. A free offer costs 0. An offer priced in
// a unit whose use per request is unknown (tokens, minutes) does not decide,
// so the caller falls back to quota.cost_per_request.
func (o *Offer) RequestCost() (float64, bool) {
	if o == nil {
		return 0, false
	}
	if o.Free {
		return 0, true
	}
	p := o.Price
	if p == nil || (p.Currency != "" && p.Currency != "USD") {
		return 0, false
	}
	n := p.PerRequest
	if n == 0 && callUnits[p.Unit] {
		n = 1
	}
	if n == 0 {
		return 0, false
	}
	return p.Amount * n, true
}

// CostFor is the provider's USD cost for one successful request: the
// matching offer's price, else quota.cost_per_request.
func (s *Spec) CostFor(method, path string) float64 {
	if c, ok := s.OffersFor(method, path).Priced().RequestCost(); ok {
		return c
	}
	return s.Quota.CostPerRequest
}

// WideConcurrency is the smallest concurrency a provider wide offer sets, or 0.
func (s *Spec) WideConcurrency() int {
	n := 0
	for i := range s.Offers {
		if c := s.Offers[i].Concurrency; s.Offers[i].Wide() && c > 0 && (n == 0 || c < n) {
			n = c
		}
	}
	return n
}

// HasFreeOffer reports whether any offer is free.
func (s *Spec) HasFreeOffer() bool {
	for _, o := range s.Offers {
		if o.Free {
			return true
		}
	}
	return false
}

// Summary is one line describing the offer, for tool output and prompts:
// "TinySearch (POST /search): free, 30 requests/minute, 500 requests/hour".
func (o *Offer) Summary() string {
	var b strings.Builder
	b.WriteString(o.Name)
	if m := o.Match; m != nil {
		var sel []string
		if m.Method != "" || m.Path != "" {
			sel = append(sel, strings.TrimSpace(strings.ToUpper(m.Method)+" "+m.Path))
		}
		if len(m.Examples) > 0 {
			sel = append(sel, "examples "+strings.Join(m.Examples, ", "))
		}
		b.WriteString(" (" + strings.Join(sel, "; ") + ")")
	} else {
		b.WriteString(" (all requests)")
	}
	var parts []string
	switch {
	case o.Free:
		parts = append(parts, "free")
	case o.Price != nil:
		cur := o.Price.Currency
		if cur == "" || cur == "USD" {
			parts = append(parts, fmt.Sprintf("$%s per %s", trimFloat(o.Price.Amount), o.Price.Unit))
		} else {
			parts = append(parts, fmt.Sprintf("%s %s per %s", trimFloat(o.Price.Amount), cur, o.Price.Unit))
		}
	}
	for _, l := range o.Limits {
		parts = append(parts, l.String())
	}
	if o.Concurrency > 0 {
		parts = append(parts, fmt.Sprintf("%d concurrent", o.Concurrency))
	}
	if len(parts) > 0 {
		b.WriteString(": " + strings.Join(parts, ", "))
	}
	return b.String()
}

func (l Limit) String() string {
	u := l.Unit
	if u == "" {
		u = "requests"
	}
	return fmt.Sprintf("%d %s/%s", l.Count, u, l.Per)
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.6f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}
