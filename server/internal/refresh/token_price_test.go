package refresh

import (
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// TestRefreshKeepsStructuredTokenPrices: a check that drops a model offer's
// model or output price, or restates it per million tokens, waits for a
// superadmin instead of applying on its own; a small move of both prices
// still applies.
func TestRefreshKeepsStructuredTokenPrices(t *testing.T) {
	domains := agent.ProviderDomains("https://vendor.example", "", "")
	cur := func() []providers.Offer {
		return []providers.Offer{{Name: "Model A", Match: &providers.OfferMatch{Method: "POST", Path: "/v1/chat"}, Model: "model-a",
			Price: &providers.Price{Amount: 2e-6, Currency: "USD", Unit: "token", OutputAmount: 10e-6},
			Notes: "Model model-a: $2 input and $10 output per million tokens.", SourceURL: "https://vendor.example/pricing"}}
	}
	cases := map[string]struct {
		mut  func(o *providers.Offer)
		want bool
	}{
		"unchanged":              {func(o *providers.Offer) {}, true},
		"both prices move a bit": {func(o *providers.Offer) { o.Price.Amount, o.Price.OutputAmount = 2.5e-6, 12e-6 }, true},
		"model dropped":          {func(o *providers.Offer) { o.Model = "" }, false},
		"output price dropped":   {func(o *providers.Offer) { o.Price.OutputAmount = 0 }, false},
		"price dropped":          {func(o *providers.Offer) { o.Price = nil }, false},
		"per million by mistake": {func(o *providers.Offer) { o.Price.Amount, o.Price.OutputAmount = 2, 10 }, false},
	}
	for name, c := range cases {
		next := cur()
		c.mut(&next[0])
		d := DiffOffers(cur(), next)
		ok, why := AutoApply(d, cur(), next, domains)
		if ok != c.want {
			t.Errorf("%s: auto %v (%s), want %v", name, ok, why, c.want)
		}
	}
}
