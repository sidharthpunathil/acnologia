package catalog

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// backfillTokenPrices gives a built-in provider's stored model offers the
// structured token price (Model and Price) this release ships, so budgets
// count their token spend on installs seeded before prices were
// structured. Only an offer that still reads exactly as shipped is filled:
// same name, same notes (which state the price), no price and no model of
// its own. An offer a person or the offers refresh changed is left alone.
// It writes in place, with no new version, as the refresh does for
// checked_at, so the provider still reads as unedited.
func (c *Catalog) backfillTokenPrices(ctx context.Context, cur *store.Provider, b providers.Builtin) {
	if !cur.Builtin {
		return
	}
	shipped := map[string]providers.Offer{}
	for _, o := range b.Spec.Offers {
		if o.Model != "" && o.Price != nil && o.Price.Unit == "token" {
			shipped[strings.ToLower(o.Name)] = o
		}
	}
	if len(shipped) == 0 {
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(cur.Spec, &raw) != nil || raw["offers"] == nil {
		return
	}
	var offers []providers.Offer
	if json.Unmarshal(raw["offers"], &offers) != nil {
		return
	}
	changed := false
	for i, o := range offers {
		s, ok := shipped[strings.ToLower(o.Name)]
		if !ok || o.Price != nil || o.Model != "" || o.Free || strings.TrimSpace(o.Notes) != strings.TrimSpace(s.Notes) {
			continue
		}
		p := *s.Price
		offers[i].Model, offers[i].Price = s.Model, &p
		changed = true
	}
	if !changed {
		return
	}
	next, err := json.Marshal(offers)
	if err != nil {
		return
	}
	raw["offers"] = next
	spec, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if _, err := providers.DecodeSpec(spec); err != nil {
		return
	}
	if ok, err := c.Store.TouchProviderSpec(ctx, cur.Slug, cur.Version, spec); err != nil || !ok {
		slog.Warn("could not add token prices to built-in provider", "slug", cur.Slug, "err", err)
	}
}
