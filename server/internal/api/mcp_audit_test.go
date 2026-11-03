package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// check_keys with no service must not check every pool key against one
// service picked at random: that sends other services' secrets to its host.
func TestMCPCheckKeysOnlyChecksItsOwnService(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "serpkey001"})
	var mu sync.Mutex
	seen := []string{}
	rec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String()+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(rec.Close)
	// A second service, pointing at a host that records what it receives.
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "other", "base_url": rec.URL}, 201, nil)
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true, "manage": true}, 200, nil)
	m := &mcpClient{e: e, tok: tok}
	for _, args := range []map[string]any{{}, {"service": ""}} {
		for i := 0; i < 4; i++ {
			if _, isErr := m.tool("check_keys", args); !isErr {
				t.Fatalf("check_keys %v ran without a service", args)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if strings.Contains(s, "serpkey001") {
			t.Fatalf("a serpapi key went to another service's host: %s", s)
		}
	}
	// Naming the service still works.
	if out, isErr := m.tool("check_keys", map[string]any{"service": "serpapi"}); isErr || out["checked"].(float64) != 1 {
		t.Fatalf("check_keys serpapi: %v", out)
	}
}

func TestPoolCheckRefusesAnotherServicesKey(t *testing.T) {
	e := newEnv(t)
	ks := e.addKeys(map[string]any{"secret": "serpkey002"})
	k, _ := e.st.GetKey(context.Background(), ks[0].ID)
	other := &store.Service{ID: k.ServiceID + 100, Provider: "serpapi", BaseURL: "http://127.0.0.1:1", TargetTrusted: true}
	if err := e.api.Pool.Check(context.Background(), other, k); err != pool.ErrWrongService {
		t.Fatalf("want ErrWrongService, got %v", err)
	}
}

// Generated tool names stay unique: a service shared by another server, or
// any later service, never takes the name of an earlier local tool, and the
// sort order of the services does not decide who wins.
func TestServiceToolNamesAreUniqueAndLocalFirst(t *testing.T) {
	providers.Set("audit-local-maps", &providers.Spec{Examples: []providers.Example{{Name: "Maps geocode", Path: "/geocode"}}})
	providers.Set("audit-peer-maps", &providers.Spec{Examples: []providers.Example{{Name: "geocode", Path: "/x"}, {Name: "key", Path: "/k"}}})
	link := int64(1)
	local := &store.Service{ID: 5, Slug: "google", Provider: "audit-local-maps"}
	peer := &store.Service{ID: 2, Slug: "google-maps", Provider: "audit-peer-maps", PeerLinkID: &link}
	getKey := &store.Service{ID: 9, Slug: "get", Provider: "audit-peer-maps"}
	for _, order := range [][]*store.Service{{peer, local, getKey}, {local, getKey, peer}} {
		names := serviceToolNames(order, &store.App{})
		if names[toolKey{5, 0}] != "google_maps_geocode" {
			t.Fatalf("the local service lost its name: %v", names)
		}
		pn := names[toolKey{2, 0}]
		if pn == "" || pn == "google_maps_geocode" || !strings.HasPrefix(pn, "google_maps_geocode_") {
			t.Fatalf("peer tool name %q", pn)
		}
		// A service tool never shadows a built in tool.
		if n := names[toolKey{9, 1}]; n == "get_key" || n == "" {
			t.Fatalf("service tool took a built in name: %q", n)
		}
		again := serviceToolNames(order, &store.App{})
		if again[toolKey{2, 0}] != pn {
			t.Fatal("names are not stable")
		}
	}
}
