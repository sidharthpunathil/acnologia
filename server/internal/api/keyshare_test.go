package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// One account, several APIs: a second service on the same site with the same
// auth placement uses the first one's keys without adding them again.
func TestServiceUsesKeysOfTheSameAccount(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, maps := e.upstream("omkar-maps", nil, "acct-key")
	ctx := context.Background()

	// A new service on the same site, same header, no keys: linked at once.
	var tiktok store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "omkar-tiktok", "name": "Omkar TikTok",
		"base_url": maps.BaseURL + "/tiktok", "auth_type": "header", "auth_name": "X-Key"}, 201, &tiktok)
	if tiktok.KeySourceID == nil || *tiktok.KeySourceID != maps.ID || tiktok.KeyCounts.Available != 1 || tiktok.KeyCounts.Shared != 1 {
		t.Fatalf("not linked on create: source=%v counts=%+v", tiktok.KeySourceID, tiktok.KeyCounts)
	}
	// A service with another auth placement is left alone.
	var other store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "other", "name": "Other",
		"base_url": maps.BaseURL + "/o", "auth_type": "query", "auth_name": "key"}, 201, &other)
	if other.KeySourceID != nil {
		t.Fatal("a service with another auth placement must not be linked")
	}

	// The borrowed key serves the linked service's requests.
	_, tok := e.app(map[string]any{"name": "app"})
	r, body := call(t, e, "GET", "/proxy/omkar-tiktok/videos", tok)
	if r.StatusCode != 200 || up.hitsFor("acct-key") == 0 {
		t.Fatalf("linked service did not use the shared key: %d %s", r.StatusCode, body)
	}

	// The catalog card counts the shared key.
	var list struct{ Services []store.Service }
	e.do(e.super, "GET", "/api/admin/services", nil, 200, &list)
	for _, sv := range list.Services {
		if sv.Slug == "omkar-tiktok" && (sv.KeyCounts.Available != 1 || sv.KeySourceSlug != "omkar-maps") {
			t.Fatalf("card counts: %+v source=%q", sv.KeyCounts, sv.KeySourceSlug)
		}
	}

	// The same secret added to the linked service is one key, shown as twins.
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", tiktok.ID), map[string]any{"keys": []map[string]any{{"secret": "acct-key", "label": "dup"}}}, 201, nil)
	var keys struct {
		Keys   []store.Key `json:"keys"`
		Shared []store.Key `json:"shared"`
	}
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/services/%d/keys", tiktok.ID), nil, 200, &keys)
	if len(keys.Keys) != 1 || len(keys.Keys[0].Twins) != 1 || keys.Keys[0].Twins[0].ServiceSlug != "omkar-maps" || len(keys.Shared) != 0 {
		t.Fatalf("twins: %+v shared=%d", keys.Keys, len(keys.Shared))
	}
	sv, _ := e.st.GetService(ctx, tiktok.ID)
	_ = e.st.FillStats(ctx, []*store.Service{sv}, true, nil)
	if sv.KeyCounts.Total != 1 {
		t.Fatalf("a twin must be counted once: %+v", sv.KeyCounts)
	}

	// Moving the base URL to another site stops the sharing at once.
	e.do(e.super, "DELETE", fmt.Sprintf("/api/admin/keys/%d", keys.Keys[0].ID), nil, 200, nil)
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", tiktok.ID), map[string]any{"base_url": "https://elsewhere.example.com"}, 200, nil)
	before := up.hitsFor("acct-key")
	if r, _ := call(t, e, "GET", "/proxy/omkar-tiktok/videos", tok); r.StatusCode == 200 || up.hitsFor("acct-key") != before {
		t.Fatalf("the key followed the base URL to another site: %d", r.StatusCode)
	}

	// Linking by hand checks the same rules.
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/services/%d/key-source", other.ID), map[string]any{"source": "omkar-maps"}, 400, nil)
	e.do(e.super, "PUT", fmt.Sprintf("/api/admin/services/%d/key-source", tiktok.ID), map[string]any{"source": nil}, 200, nil)
	if sv, _ := e.st.GetService(ctx, tiktok.ID); sv.KeySourceID != nil {
		t.Fatal("unlink did not clear the source")
	}
}

func TestSharedKeysReportAndLinkAll(t *testing.T) {
	e, _ := newFeatureEnv(t)
	_, maps := e.upstream("omkar-maps", nil, "acct-key")
	// Made before sharing existed: a service with no keys and no source,
	// and a twin of the key in a third service.
	sv, err := e.st.CreateService(context.Background(), &store.Service{Slug: "omkar-ads", Name: "Omkar Ads", Provider: "generic",
		BaseURL: maps.BaseURL + "/ads", AuthType: "header", AuthName: "X-Key", Strategy: "round_robin", MaxAttempts: 3, CooldownSec: 60})
	if err != nil {
		t.Fatal(err)
	}
	e.upstream("dup", nil, "acct-key")
	var rep struct {
		Duplicates  [][]store.KeyTwin     `json:"duplicates"`
		Suggestions []keySourceSuggestion `json:"suggestions"`
	}
	e.do(e.super, "GET", "/api/admin/keys/shared", nil, 200, &rep)
	if len(rep.Duplicates) != 1 || len(rep.Duplicates[0]) != 2 {
		t.Fatalf("duplicates: %+v", rep.Duplicates)
	}
	if len(rep.Suggestions) != 1 || rep.Suggestions[0].Service != "omkar-ads" || rep.Suggestions[0].Source != "omkar-maps" {
		t.Fatalf("suggestions: %+v", rep.Suggestions)
	}
	e.do(e.super, "POST", "/api/admin/keys/shared/link", nil, 200, nil)
	if got, _ := e.st.GetService(context.Background(), sv.ID); got.KeySourceID == nil || *got.KeySourceID != maps.ID {
		t.Fatal("link all did not link omkar-ads")
	}
	_, tok := e.app(map[string]any{"name": "app"})
	if r, _ := call(t, e, "GET", "/proxy/omkar-ads/x", tok); r.StatusCode != http.StatusOK {
		t.Fatalf("linked service: %d", r.StatusCode)
	}
}
