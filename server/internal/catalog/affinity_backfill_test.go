package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// An install seeded before key affinity existed gets the built-in rules on
// boot, even after its spec was edited, and only once.
func TestBackfillAffinity(t *testing.T) {
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
	rules := []providers.AffinityRule{{Create: providers.AffinityCreate{Method: "POST", Path: "/v1/jobs$"}, ID: "id",
		Follow: []providers.AffinityFollow{{Path: "/v1/jobs/{id}"}}}}
	b := providers.Builtin{Slug: "acme", Spec: providers.Spec{Affinity: rules}}
	c.backfillAffinity(ctx, b)
	p, _ := st.GetProvider(ctx, "acme")
	var spec providers.Spec
	if err := json.Unmarshal(p.Spec, &spec); err != nil || len(spec.Affinity) != 1 || spec.Affinity[0].Follow[0].Path != "/v1/jobs/{id}" || p.Version != 3 {
		t.Fatalf("affinity not added: v%d %s", p.Version, p.Spec)
	}
	if spec.BaseURL != "https://api.acme.test" {
		t.Fatal("the rest of the spec must be kept")
	}
	p.Spec = old
	if _, err := st.SaveProvider(ctx, p, "root", "drop affinity"); err != nil {
		t.Fatal(err)
	}
	c.backfillAffinity(ctx, b)
	if p, _ = st.GetProvider(ctx, "acme"); p.Version != 4 {
		t.Fatalf("affinity was added twice: v%d", p.Version)
	}
}
