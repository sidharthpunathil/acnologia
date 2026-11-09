package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The catalog stays cheap with a very large number of providers: built once,
// then each request only checks its key. Run with ACN_SCALE=1 for the
// 100,000 tool size; the default is small enough for every test run.
func TestMCPCatalogScales(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	nProv, nEp := 40, 50
	if testing.Verbose() && scaleBig() {
		nProv, nEp = 1000, 100
	}
	words := []string{"linkedin", "tiktok", "amazon", "zillow", "maps", "weather", "news", "crypto", "flights", "hotels"}
	for p := 0; p < nProv; p++ {
		slug := fmt.Sprintf("scale-%d", p)
		eps := []providers.GuideEndpoint{}
		for i := 0; i < nEp; i++ {
			eps = append(eps, providers.GuideEndpoint{Group: words[(p+i)%len(words)] + " api", Name: fmt.Sprintf("op %d", i), Method: "GET",
				Path: fmt.Sprintf("/v1/op%d", i), Description: "Does " + words[i%len(words)] + " things.",
				Params: []providers.GuideParam{{Name: "q", In: "query", Required: true}}})
		}
		providers.Set(slug, &providers.Spec{BaseURL: e.upURL, Guide: &providers.Guide{Endpoints: eps}})
		t.Cleanup(func() { providers.Remove(slug) })
		if _, err := e.st.CreateService(ctx, &store.Service{Slug: slug, Name: slug, Provider: slug, BaseURL: e.upURL}); err != nil {
			t.Fatal(err)
		}
	}
	a := &store.App{Mode: "both"}
	st := store.DefaultMCPSettings()
	t0 := time.Now()
	n := len(e.api.serviceTools(ctx, a))
	build := time.Since(t0)
	t0 = time.Now()
	listed := e.api.mcpListedTools(ctx, a, st)
	list := time.Since(t0)
	b, _ := json.Marshal(listed)
	raw, _ := json.Marshal(map[string]any{"query": "linkdin op 7"})
	t0 = time.Now()
	out, err := e.api.searchTools(ctx, a, st, raw)
	first := time.Since(t0)
	t0 = time.Now()
	_, _ = e.api.searchTools(ctx, a, st, raw)
	again := time.Since(t0)
	if err != nil || len(out["tools"].([]map[string]any)) == 0 {
		t.Fatalf("search %v", err)
	}
	name := out["tools"].([]map[string]any)[0]["name"].(string)
	t0 = time.Now()
	tl := e.api.mcpTool(ctx, a, st, name)
	lookup := time.Since(t0)
	if tl == nil {
		t.Fatalf("lookup %s", name)
	}
	t.Logf("%d tools: build %v, tools/list %v (%d tools, %d bytes), first search %v (builds the index), search %v, lookup %v",
		n, build, list, len(listed), len(b), first, again, lookup)
	if len(listed) > 20 {
		t.Fatalf("auto mode listed %d tools", len(listed))
	}
}

func scaleBig() bool { return os.Getenv("ACN_SCALE") == "1" }
