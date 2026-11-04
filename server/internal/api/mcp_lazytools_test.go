package api

import (
	"context"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// tools/call looks a tool up without building the service tools when the
// name is a core, insight or manage tool. That is only safe while no service
// tool can take a static name, even one from a set that is turned off. This
// checks that the lazy lookup returns exactly the tool the full list would,
// for every static name, every service tool name and unknown names, across
// all settings, modes, allow lists and disabled lists, with services whose
// examples join into static names.
func TestLazyToolLookupMatchesFullList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	providers.Set("lazy-collide", &providers.Spec{Examples: []providers.Example{
		{Name: "request", Path: "/r"}, {Name: "keys", Path: "/k"}, {Name: "status", Path: "/s"}, {Name: "key", Path: "/g"},
	}})
	t.Cleanup(func() { providers.Remove("lazy-collide") })
	for _, slug := range []string{"proxy", "list", "pool", "add", "check", "get"} {
		if _, err := e.st.CreateService(ctx, &store.Service{Slug: slug, Name: slug, Provider: "lazy-collide", BaseURL: "http://127.0.0.1:1"}); err != nil {
			t.Fatal(err)
		}
	}
	names := map[string]bool{"nope": true, "": true}
	for _, set := range [][]mcpTool{coreTools, insightTools, manageTools} {
		for _, tl := range set {
			names[tl.Name] = true
		}
	}
	full := &store.App{Mode: "both"}
	renamed := false
	for _, tl := range e.api.serviceTools(ctx, full) {
		names[tl.Name] = true
		renamed = renamed || strings.HasPrefix(tl.Name, "proxy_request_")
		if isStaticTool(tl.Name) {
			t.Fatalf("service tool took static name %s", tl.Name)
		}
	}
	if !renamed {
		t.Fatal("no service tool was renamed away from proxy_request")
	}
	if len(names) < 20 {
		t.Fatalf("too few names %d", len(names))
	}
	apps := []*store.App{{Mode: "both"}, {Mode: "proxy"}, {Mode: "lease"}, {Mode: "both", AllowedServices: []string{"proxy", "gen"}}}
	disabled := [][]string{nil, {"proxy_request", "list_keys"}, {"proxy_request_" + "x"}}
	n := 0
	for _, a := range apps {
		for mask := 0; mask < 16; mask++ {
			for _, dis := range disabled {
				st := store.MCPSettings{Core: mask&1 != 0, ServiceTools: mask&2 != 0, Insights: mask&4 != 0, Manage: mask&8 != 0, DisabledTools: dis}
				for name := range names {
					want := findTool(e.api.mcpTools(ctx, a, st), name)
					got := e.api.mcpTool(ctx, a, st, name)
					if (want == nil) != (got == nil) || (want != nil && (want.Name != got.Name || want.Set != got.Set || want.service != got.service || want.example != nil && got.example.Path != want.example.Path)) {
						t.Fatalf("mismatch app=%+v st=%+v name=%q want=%v got=%v", a, st, name, want, got)
					}
					n++
				}
			}
		}
	}
	t.Logf("checked %d lookups over %d names", n, len(names))
}
