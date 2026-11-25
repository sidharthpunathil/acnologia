package catalog

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestBackfillTokenPrices: an install seeded before token prices were
// structured gets them on boot, in place, except on an offer someone
// changed; a sample call's token cost then comes out of the spec.
func TestBackfillTokenPrices(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	c := &Catalog{Store: st}
	if err := c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProvider(ctx, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	// Make it look like an older release: no model, no price; and one
	// offer whose notes a person edited.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(p.Spec, &raw)
	var offers []providers.Offer
	_ = json.Unmarshal(raw["offers"], &offers)
	for i := range offers {
		offers[i].Model, offers[i].Price = "", nil
		if offers[i].Name == "Claude Opus 5.5" {
			offers[i].Notes = "Our negotiated rate."
		}
	}
	raw["offers"], _ = json.Marshal(offers)
	old, _ := json.Marshal(raw)
	if ok, err := st.TouchProviderSpec(ctx, "anthropic", p.Version, old); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := st.GetProvider(ctx, "anthropic")
	if after.Version != p.Version {
		t.Fatalf("the backfill made version %d", after.Version)
	}
	spec := providers.Get("anthropic")
	priced := map[string]bool{}
	for _, o := range spec.Offers {
		priced[o.Name] = o.Price != nil && o.Model != ""
	}
	if !priced["Claude Sonnet 5.5"] || !priced["Claude Haiku 5.5"] || priced["Claude Opus 5.5"] {
		t.Fatalf("priced offers %v", priced)
	}
	// 1,000 input and 500 output tokens on Sonnet 5.5 ($2 and $10 per million).
	cost, ok := spec.TokenCost("POST", "/v1/messages", "claude-sonnet-5-5-20261101", 1000, 500)
	if !ok || math.Abs(cost-0.007) > 1e-12 {
		t.Fatalf("token cost %v %v, want 0.007", cost, ok)
	}
	if _, ok := spec.TokenCost("POST", "/v1/messages", "claude-opus-5-5", 1000, 500); ok {
		t.Fatal("the edited offer was priced")
	}
}

// TestBackfilledOpenAIPricesChatCompletions: OpenAI offers an older install
// stored without prices are backfilled on boot, and then price Chat
// Completions calls as well as the Responses API they match.
func TestBackfilledOpenAIPricesChatCompletions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	c := &Catalog{Store: st}
	if err := c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProvider(ctx, "openai")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(p.Spec, &raw)
	var offers []providers.Offer
	_ = json.Unmarshal(raw["offers"], &offers)
	for i := range offers {
		offers[i].Model, offers[i].Price = "", nil
	}
	raw["offers"], _ = json.Marshal(offers)
	old, _ := json.Marshal(raw)
	if ok, err := st.TouchProviderSpec(ctx, "openai", p.Version, old); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	// 1,000 input and 500 output tokens on GPT-6 Luna ($0.10 and $0.50 per million).
	cost, ok := providers.Get("openai").TokenCost("POST", "/v1/chat/completions", "gpt-6-luna", 1000, 500)
	if !ok || math.Abs(cost-0.00035) > 1e-12 {
		t.Fatalf("chat completions token cost %v %v, want 0.00035", cost, ok)
	}
}
