package catalog

import (
	"context"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The peer provider uses Acnologia's own mark, and an install seeded with
// the old drawn SVG gets the mark on its next boot.
func TestPeerProviderSeedLogo(t *testing.T) {
	data, mime := providers.SeedLogo(providers.PeerSlug)
	if data == nil || mime != "image/png" || SniffImage(data) != "image/png" || len(data) > MaxLogoBytes {
		t.Fatalf("peer seed logo %q %d bytes", mime, len(data))
	}
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
	if err := st.SetProviderLogo(ctx, providers.PeerSlug, []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "image/svg+xml"); err != nil {
		t.Fatal(err)
	}
	if err := c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	if _, got, err := st.ProviderLogo(ctx, providers.PeerSlug); err != nil || got != "image/png" {
		t.Fatalf("after boot %q %v", got, err)
	}
}
