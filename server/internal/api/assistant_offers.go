package api

import (
	"fmt"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// providerOffersTool answers "which search provider is free?" and "what are
// TinyFish's limits?" from the offers in provider specs. Read only.
func providerOffersTool(cs *chatSession, in map[string]any) toolOut {
	ps, err := cs.s.Store.ListProviders(cs.ctx)
	if err != nil {
		return toolFail(err.Error())
	}
	q := strings.ToLower(strings.TrimSpace(argStr(in, "provider")))
	cat := strings.ToLower(strings.TrimSpace(argStr(in, "category")))
	freeOnly, _ := in["free_only"].(bool)
	outRows := []map[string]any{}
	ents := []chatEntity{}
	for _, p := range ps {
		if q != "" && !strings.Contains(strings.ToLower(p.Slug), q) && !strings.Contains(strings.ToLower(p.Name), q) {
			continue
		}
		if cat != "" && strings.ToLower(p.Category) != cat {
			continue
		}
		spec, err := providers.DecodeSpec(p.Spec)
		if err != nil {
			continue
		}
		if freeOnly && !spec.HasFreeOffer() {
			continue
		}
		row := map[string]any{"slug": p.Slug, "name": p.Name, "category": p.Category, "services_count": p.ServicesCount,
			"has_free_offer": spec.HasFreeOffer(), "offers": offerRows(spec.Offers)}
		if spec.Quota.FreeTier != "" {
			row["free_tier"] = spec.Quota.FreeTier
		}
		if spec.Quota.CostPerRequest > 0 {
			row["default_cost_per_request_usd"] = spec.Quota.CostPerRequest
		}
		outRows = append(outRows, row)
		ents = append(ents, chatEntity{Type: "provider", Slug: p.Slug, Name: p.Name})
	}
	sum := fmt.Sprintf("Read offers for %d provider(s)", len(outRows))
	return toolOut{OK: true, Summary: sum, Model: asJSON(map[string]any{
		"providers": outRows,
		"note":      "Offers come from each provider's pricing page (source_url, checked_at). Acnologia paces every key under an offer's limits and prices requests from it.",
	}), Entities: ents}
}

// offerRows is the compact form of offers for tool output.
func offerRows(offers []providers.Offer) []map[string]any {
	out := []map[string]any{}
	for i := range offers {
		o := &offers[i]
		r := map[string]any{"name": o.Name, "summary": o.Summary(), "free": o.Free}
		for k, v := range map[string]string{"notes": o.Notes, "source_url": o.SourceURL, "checked_at": o.CheckedAt} {
			if v != "" {
				r[k] = v
			}
		}
		out = append(out, r)
	}
	return out
}

// offerHint is a short sentence for an MCP tool description naming the
// offer that applies to a request: whether it is free and its limits.
func offerHint(spec *providers.Spec, method, path string) string {
	o := spec.OffersFor(method, path).Specific
	if o == nil {
		return ""
	}
	var parts []string
	switch {
	case o.Free:
		parts = append(parts, o.Name+" is free")
	case o.Price != nil:
		parts = append(parts, fmt.Sprintf("%s costs %s per %s", o.Name, usd(o.Price.Amount), o.Price.Unit))
	}
	var lim []string
	for _, l := range o.Limits {
		lim = append(lim, l.String())
	}
	if o.Concurrency > 0 {
		lim = append(lim, fmt.Sprintf("%d at a time", o.Concurrency))
	}
	if len(lim) > 0 {
		parts = append(parts, "limits per key: "+strings.Join(lim, ", ")+"; stay under them")
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "; ") + "."
}

func usd(f float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
	return "$" + s
}
