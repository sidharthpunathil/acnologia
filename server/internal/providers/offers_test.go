package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func offerSpec() Spec {
	return Spec{
		BaseURL: "https://api.example.com", Auth: Auth{Type: "bearer"},
		Quota: QuotaInfo{CostPerRequest: 0.01},
		Examples: []Example{
			{Name: "Search", Method: "GET", Path: "/search?q=x"},
			{Name: "Agent run", Method: "POST", Path: "/v1/run"},
		},
		Offers: []Offer{
			{Name: "Search", Match: &OfferMatch{Examples: []string{"Search"}}, Free: true, Price: &Price{Unit: "request"},
				Limits: []Limit{{Count: 30, Per: "minute"}}},
			{Name: "Models", Match: &OfferMatch{Method: "POST", Path: "/v1beta/models/*:generateContent$"}, Price: &Price{Amount: 0.002, Unit: "request"}},
			{Name: "Agent", Match: &OfferMatch{Method: "POST", Path: "/v1/run"}, Price: &Price{Amount: 0.016, Unit: "step"}, Concurrency: 2},
			{Name: "Credits", Match: &OfferMatch{Path: "/extract"}, Price: &Price{Amount: 0.008, Unit: "credit", PerRequest: 2}},
			{Name: "Account", Limits: []Limit{{Count: 1000, Per: "day"}}, Concurrency: 4},
		},
	}
}

func TestOffersMatchRequests(t *testing.T) {
	s := offerSpec()
	if errs := s.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	cases := []struct{ method, path, want string }{
		{"GET", "/search", "Search"},
		{"GET", "search", "Search"}, // the proxy passes paths without a leading slash
		{"POST", "/search", ""},     // the example is a GET
		{"GET", "/search/more", ""}, // examples match exactly
		{"POST", "/v1beta/models/gemini-2.5-flash:generateContent", "Models"},
		{"POST", "/v1beta/models/a/b:generateContent", ""}, // * stays inside a segment
		{"POST", "/v1beta/models/x:generateContentStream", ""},
		{"POST", "/v1/run-sse", "Agent"}, // a prefix
		{"PUT", "/extract/x", "Credits"}, // any method
		{"", "/search", ""},              // a lease sees wide offers only
	}
	for _, c := range cases {
		a := s.OffersFor(c.method, c.path)
		got := ""
		if a.Specific != nil {
			got = a.Specific.Name
		}
		if got != c.want {
			t.Errorf("%s %s: got %q want %q", c.method, c.path, got, c.want)
		}
		if len(a.Wide) != 1 || a.Wide[0].Name != "Account" {
			t.Errorf("%s %s: wide offers %v", c.method, c.path, a.Wide)
		}
	}
	if s.WideConcurrency() != 4 || !s.HasFreeOffer() {
		t.Fatal("wide concurrency or free flag")
	}
}

func TestOfferCost(t *testing.T) {
	s := offerSpec()
	cases := []struct {
		method, path string
		want         float64
	}{
		{"GET", "/search", 0}, // free
		{"POST", "/v1beta/models/m:generateContent", 0.002},
		{"POST", "/v1/run", 0.01},   // per step: unknown per request, legacy price
		{"POST", "/extract", 0.016}, // 2 credits at 0.008
		{"GET", "/other", 0.01},     // the wide offer has no price
		{"", "", 0.01},
	}
	for _, c := range cases {
		if got := s.CostFor(c.method, c.path); got != c.want {
			t.Errorf("%s %s: cost %v want %v", c.method, c.path, got, c.want)
		}
	}
	// No offers at all keeps the legacy behaviour.
	plain := Spec{Quota: QuotaInfo{CostPerRequest: 0.005}}
	if plain.CostFor("GET", "/x") != 0.005 {
		t.Fatal("legacy cost_per_request")
	}
	// A non USD price does not decide the cost.
	eur := Spec{Quota: QuotaInfo{CostPerRequest: 0.003}, Offers: []Offer{{Name: "x", Price: &Price{Amount: 1, Currency: "EUR", Unit: "request"}}}}
	if eur.CostFor("GET", "/") != 0.003 {
		t.Fatal("EUR offer should fall back")
	}
}

func TestOfferCostKnown(t *testing.T) {
	s := offerSpec()
	if c, ok := s.CostKnown("GET", "/search"); !ok || c != 0 {
		t.Fatalf("a free offer is a known price of 0: %v %v", c, ok)
	}
	if c, ok := s.CostKnown("GET", "/other"); !ok || c != 0.01 {
		t.Fatalf("quota.cost_per_request above 0 is known: %v %v", c, ok)
	}
	s.Quota.CostPerRequest = 0
	if c, ok := s.CostKnown("GET", "/other"); ok || c != 0 {
		t.Fatalf("no offer price and no cost_per_request is unknown, not free: %v %v", c, ok)
	}
	if _, ok := s.CostKnown("POST", "/v1/run"); ok {
		t.Fatal("a per step price with no per_request does not decide")
	}
}

func TestOfferTokenCost(t *testing.T) {
	s := Spec{Offers: []Offer{
		{Name: "Search", Match: &OfferMatch{Path: "/search"}, Price: &Price{Amount: 0.01, Unit: "request"}},
		{Name: "Big", Match: &OfferMatch{Method: "POST", Path: "/v1/chat"}, Model: "big", Price: &Price{Amount: 3e-6, OutputAmount: 15e-6, Unit: "token"}},
		{Name: "Big preview", Match: &OfferMatch{Method: "POST", Path: "/v1/chat"}, Model: "big-preview", Price: &Price{Amount: 1e-6, Unit: "token"}},
		{Name: "Any", Match: &OfferMatch{Method: "POST", Path: "/v1/chat"}, Price: &Price{Amount: 1e-7, Unit: "token"}},
		{Name: "Free", Match: &OfferMatch{Method: "POST", Path: "/v1/chat"}, Model: "tiny", Free: true},
	}}
	if errs := s.validateOffersOnly(); len(errs) > 0 {
		t.Fatal(errs)
	}
	near := func(a, b float64) bool { return a-b < 1e-12 && b-a < 1e-12 }
	cases := []struct {
		model string
		want  float64
	}{
		{"big", 1000*3e-6 + 200*15e-6},
		{"BIG-20261101", 1000*3e-6 + 200*15e-6}, // a dated variant
		{"vendor/big", 1000*3e-6 + 200*15e-6},
		{"big-preview", 1200 * 1e-6}, // an exact match beats the variant rule
		{"other", 1200 * 1e-7},       // the offer with no model
		{"", 1200 * 1e-7},
		{"tiny", 1200 * 1e-7}, // a free offer prices nothing
	}
	for _, c := range cases {
		got, ok := s.TokenCost("POST", "v1/chat", c.model, 1000, 200)
		if !ok || !near(got, c.want) {
			t.Errorf("%q: %v %v, want %v", c.model, got, ok, c.want)
		}
	}
	if _, ok := s.TokenCost("POST", "/search", "big", 1000, 200); ok {
		t.Fatal("an endpoint with no token offer has no token cost")
	}
	if _, ok := s.TokenCost("", "/v1/chat", "big", 1000, 200); ok {
		t.Fatal("a lease never sees tokens")
	}
	if !s.HasTokenPrices() {
		t.Fatal("HasTokenPrices")
	}
	bad := Spec{Offers: []Offer{{Name: "x", Price: &Price{Amount: 1, OutputAmount: 2, Unit: "request"}}}}
	if got := strings.Join(bad.validateOffersOnly(), "\n"); !strings.Contains(got, "output_amount applies to unit token only") {
		t.Fatalf("validation: %s", got)
	}
}

func (s *Spec) validateOffersOnly() []string {
	var errs []string
	s.validateOffers(func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) })
	return errs
}

func TestOfferValidation(t *testing.T) {
	bad := Spec{BaseURL: "https://x.io", Auth: Auth{Type: "bearer"}, Offers: []Offer{
		{Name: ""},
		{Name: "dup"}, {Name: "DUP"},
		{Name: "m", Match: &OfferMatch{Method: "FETCH", Path: "search", Examples: []string{"nope"}}},
		{Name: "empty match", Match: &OfferMatch{}},
		{Name: "p", Free: true, Price: &Price{Amount: 2, Currency: "usd", Unit: "banana"}},
		{Name: "l", Limits: []Limit{{Count: 0, Per: "week"}}, Concurrency: -1},
		{Name: "s", SourceURL: "javascript:alert(1)", CheckedAt: "yesterday"},
	}}
	got := strings.Join(bad.Validate(), "\n")
	for _, want := range []string{
		"offers[0]: name is required", "name is used twice", "match.method", "match.path must start with /",
		`names "nope"`, "match needs a method", "free offer cannot have a price", "price.currency", "price.unit",
		"count must be at least 1", "per must be second", "concurrency must be 0 to 1000", "source_url", "checked_at",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestOffersAreOptionalAndRoundTrip(t *testing.T) {
	old := []byte(`{"base_url":"https://x.io","auth":{"type":"bearer"},"quota":{"unit":"requests","cost_per_request":0.002,"free_tier":"some"}}`)
	s, err := DecodeSpec(old)
	if err != nil || len(s.Offers) != 0 || len(s.Validate()) != 0 || s.CostFor("GET", "/") != 0.002 {
		t.Fatalf("spec without offers: %v %+v", err, s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "offers") {
		t.Fatal("empty offers should be omitted")
	}
	o := offerSpec().Offers[0]
	if got := o.Summary(); got != "Search (examples Search): free, 30 requests/minute" {
		t.Fatalf("summary = %q", got)
	}
}

// Built-in offers must say where their numbers come from.
func TestBuiltinOffersCiteTheirSource(t *testing.T) {
	seen := 0
	for _, b := range Builtins() {
		for _, o := range b.Spec.Offers {
			seen++
			if o.SourceURL == "" || o.CheckedAt == "" {
				t.Errorf("%s/%s: source_url and checked_at are required for built-in offers", b.Slug, o.Name)
			}
		}
	}
	if seen < 10 {
		t.Fatalf("only %d built-in offers", seen)
	}
}

func TestTinyFishOffers(t *testing.T) {
	var search, fetch *Spec
	for _, b := range Builtins() {
		s := b.Spec
		switch b.Slug {
		case "tinyfish-search":
			search = &s
		case "tinyfish-fetch":
			fetch = &s
		}
		if strings.HasPrefix(b.Slug, "tinyfish") {
			if logo, _ := SeedLogo(b.Slug); logo == nil {
				t.Errorf("%s has no seed logo", b.Slug)
			}
		}
	}
	if search == nil || fetch == nil {
		t.Fatal("TinyFish builtins missing")
	}
	a := search.OffersFor("GET", "/")
	if a.Specific == nil || !a.Specific.Free || len(a.Specific.Limits) != 2 || search.CostFor("GET", "/") != 0 {
		t.Fatalf("TinySearch offer: %+v", a.Specific)
	}
	if search.OffersFor("GET", "/usage").Specific != nil {
		t.Fatal("the usage endpoint is not TinySearch")
	}
	if f := fetch.OffersFor("POST", "/").Specific; f == nil || f.Name != "TinyFetch" || f.Limits[1].Count != 1000 || f.Limits[1].Per != "day" {
		t.Fatalf("TinyFetch offer: %+v", f)
	}
}
