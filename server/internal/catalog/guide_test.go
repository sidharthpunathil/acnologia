package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// An install seeded before guides shipped, edited or not, gets the built-in
// guide on its next boot; a guide the operator wrote is kept.
func TestBootBackfillsGuides(t *testing.T) {
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
	strip := func(slug string, guide any) {
		p, err := st.GetProvider(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		_ = json.Unmarshal(p.Spec, &raw)
		if raw["guide"] == nil {
			t.Fatalf("%s seeded without a guide", slug)
		}
		delete(raw, "guide")
		if guide != nil {
			raw["guide"] = guide
		}
		p.Spec, _ = json.Marshal(raw)
		if _, err := st.SaveProvider(ctx, p, "someone", "edit"); err != nil {
			t.Fatal(err)
		}
	}
	strip("openai", nil)
	strip("anthropic", map[string]any{"summary": "mine"})
	if err := c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	summary := func(slug string) string {
		p, _ := st.GetProvider(ctx, slug)
		var raw struct {
			Guide *struct {
				Summary string `json:"summary"`
			} `json:"guide"`
		}
		_ = json.Unmarshal(p.Spec, &raw)
		if raw.Guide == nil {
			return ""
		}
		return raw.Guide.Summary
	}
	if s := summary("openai"); s == "" {
		t.Fatal("openai guide not backfilled")
	}
	if s := summary("anthropic"); s != "mine" {
		t.Fatalf("operator guide replaced: %q", s)
	}
}
