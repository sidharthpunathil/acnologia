package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Services one account's key serves show as one family, with each key
// counted once, and one call links them all to the main service's keys.
func TestServiceFamilies(t *testing.T) {
	e, _ := newFeatureEnv(t)
	_, maps := e.upstream("omkar-maps", map[string]any{"name": "Omkar Maps"}, "acct-key")
	ctx := context.Background()
	// Made the old way: two more services, one holding the same key again.
	tiktok, _ := e.st.CreateService(ctx, &store.Service{Slug: "omkar-tiktok", Name: "Omkar TikTok", Provider: "generic",
		BaseURL: maps.BaseURL + "/tiktok", AuthType: "header", AuthName: "X-Key", Strategy: "round_robin", MaxAttempts: 3})
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", tiktok.ID), map[string]any{"keys": []map[string]any{{"secret": "acct-key"}}}, 201, nil)
	moz, _ := e.st.CreateService(ctx, &store.Service{Slug: "omkar-moz", Name: "Omkar Moz", Provider: "generic",
		BaseURL: maps.BaseURL + "/moz", AuthType: "header", AuthName: "X-Key", Strategy: "round_robin", MaxAttempts: 3})
	e.upstream("lonely", nil, "x1")

	var out struct {
		Families []serviceFamily `json:"families"`
	}
	e.do(e.super, "GET", "/api/admin/services", nil, 200, &out)
	if len(out.Families) != 1 {
		t.Fatalf("families: %+v", out.Families)
	}
	f := out.Families[0]
	if f.Name != "Omkar" || len(f.ServiceIDs) != 3 || f.Keys != 1 || f.Available != 1 || f.Unlinked != 2 {
		t.Fatalf("family: %+v", f)
	}

	var res struct {
		Main   string   `json:"main"`
		Linked []string `json:"linked"`
	}
	e.do(e.super, "POST", "/api/admin/services/families/link", map[string]any{"key": f.Key}, 200, &res)
	if res.Main != "omkar-maps" || len(res.Linked) != 2 {
		t.Fatalf("link: %+v", res)
	}
	if got, _ := e.st.GetService(ctx, moz.ID); got.KeySourceID == nil || *got.KeySourceID != maps.ID {
		t.Fatal("moz not linked")
	}
	e.do(e.super, "GET", "/api/admin/services", nil, 200, &out)
	if out.Families[0].Unlinked != 0 || out.Families[0].Keys != 1 {
		t.Fatalf("after link: %+v", out.Families[0])
	}
	e.do(e.super, "POST", "/api/admin/services/families/link", map[string]any{"key": "nope"}, 404, nil)
}

// A service on a provider host with a logo_url shows that logo.
func TestHostLogoFor(t *testing.T) {
	providers.Set("logo-test", &providers.Spec{BaseURL: "https://google-maps-scraper.omkar.cloud", Hosts: []providers.Host{
		{Name: "moz-scraper", URL: "https://moz-scraper.omkar.cloud", LogoURL: "https://www.omkar.cloud/images/tools/moz-scraper/logo.png"},
	}})
	sv := &store.Service{Provider: "logo-test", BaseURL: "https://moz-scraper.omkar.cloud"}
	if got := catalog.HostLogoFor(sv); got != "https://www.omkar.cloud/images/tools/moz-scraper/logo.png" {
		t.Fatalf("host logo: %q", got)
	}
	sv.BaseURL = "https://other.omkar.cloud"
	if got := catalog.HostLogoFor(sv); got != "" {
		t.Fatalf("no host should match: %q", got)
	}
}
