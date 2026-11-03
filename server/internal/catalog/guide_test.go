package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// An install seeded before guides existed gets the built-in guide on boot,
// even after its spec was edited, and only once.
func TestBackfillGuide(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	c := &Catalog{Store: st}
	old := []byte(`{"base_url":"https://api.acme.test","auth":{"type":"bearer"},"rules":[]}`)
	for _, note := range []string{"built-in", "operator edit"} {
		if _, err := st.SaveProvider(ctx, &store.Provider{Slug: "acme", Name: "Acme", Builtin: true, Source: "builtin", Status: "active", Spec: old}, "system", note); err != nil {
			t.Fatal(err)
		}
	}
	b := providers.Builtin{Slug: "acme", Spec: providers.Spec{Guide: &providers.Guide{Summary: "Acme finds things."}}}
	c.backfillGuide(ctx, b)
	p, _ := st.GetProvider(ctx, "acme")
	var spec providers.Spec
	if err := json.Unmarshal(p.Spec, &spec); err != nil || spec.Guide == nil || spec.Guide.Summary != "Acme finds things." || p.Version != 3 {
		t.Fatalf("guide not added: v%d %s", p.Version, p.Spec)
	}
	if spec.BaseURL != "https://api.acme.test" {
		t.Fatal("the rest of the spec must be kept")
	}

	// The operator removes it: the next boot leaves it removed.
	p.Spec = old
	if _, err := st.SaveProvider(ctx, p, "root", "drop guide"); err != nil {
		t.Fatal(err)
	}
	c.backfillGuide(ctx, b)
	if p, _ = st.GetProvider(ctx, "acme"); p.Version != 4 {
		t.Fatalf("guide was added twice: v%d", p.Version)
	}
}
