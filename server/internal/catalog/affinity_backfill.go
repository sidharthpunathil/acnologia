package catalog

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// affinityAddedNote labels the version that gave a built-in provider its
// affinity rules.
const affinityAddedNote = "built-in key affinity added"

// backfillAffinity gives a built-in provider seeded by an older release the
// affinity rules this release ships. Without them a job or file created on
// one key is polled with another and the provider answers 404, so they are
// added even after the spec was edited, as long as the stored spec has
// none. They are added once: an operator who removes them keeps them removed.
func (c *Catalog) backfillAffinity(ctx context.Context, b providers.Builtin) {
	if len(b.Spec.Affinity) == 0 {
		return
	}
	cur, err := c.Store.GetProvider(ctx, b.Slug)
	if err != nil || !cur.Builtin {
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(cur.Spec, &raw) != nil {
		return
	}
	if a, ok := raw["affinity"]; ok && string(a) != "null" {
		return
	}
	vs, err := c.Store.ProviderVersions(ctx, b.Slug)
	if err != nil {
		return
	}
	for _, v := range vs {
		if v.Note == affinityAddedNote {
			return
		}
	}
	aff, err := json.Marshal(b.Spec.Affinity)
	if err != nil {
		return
	}
	raw["affinity"] = aff
	spec, err := json.Marshal(raw)
	if err != nil {
		return
	}
	next := *cur
	next.Spec = spec
	if _, err := c.Store.SaveProvider(ctx, &next, "system", affinityAddedNote); err != nil {
		slog.Warn("could not add key affinity to built-in provider", "slug", cur.Slug, "err", err)
	}
}
