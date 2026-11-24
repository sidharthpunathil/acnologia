package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// seedEvidence writes ok successes and fail provider failures for one call
// of a service, then folds them as the worker does.
func seedEvidence(t *testing.T, st *store.Store, sv *store.Service, method, path string, ok, fail int, latency int64) {
	t.Helper()
	now := time.Now().Unix()
	for i := 0; i < ok+fail; i++ {
		status := 200
		if i >= ok {
			status = 503
		}
		if _, err := st.DB.Exec(`INSERT INTO events(ts,kind,service_id,status_code,latency_ms,detail) VALUES(?,?,?,?,?,?)`,
			now, "proxy", sv.ID, status, latency, method+" "+path); err != nil {
			t.Fatal(err)
		}
	}
}

func foldEvidence(t *testing.T, st *store.Store) {
	t.Helper()
	svcs, _ := st.ListServices(context.Background())
	specs := map[int64]*providers.Spec{}
	for _, sv := range svcs {
		specs[sv.ID] = providers.DocSpec(sv.Provider, sv.PeerProvider)
	}
	_, err := st.FoldEndpointStats(context.Background(), func(id int64, method, path string) string {
		if spec := specs[id]; spec != nil {
			k, _ := spec.EndpointKey(method, path)
			return k
		}
		return ""
	}, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
}

// TestSearchToolsEvidence: equally good matches with free keys are ordered
// by measured success rate, and a tool with enough calls shows them.
func TestSearchToolsEvidence(t *testing.T) {
	e := newEnv(t)
	catalogProvider(t, "catalog-test", 0)
	a := catalogService(t, e, "lnka")
	b := catalogService(t, e, "lnkb")
	c := catalogService(t, e, "lnkc")
	for i, sv := range []*store.Service{a, b, c} {
		e.do(e.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "evkey0" + itoa(i)}}}, 201, nil)
	}
	appID, tok := e.app(map[string]any{"name": "agent"})
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true, "service_tools": true, "tool_mode": "search"}, 200, nil)
	m := &mcpClient{e: e, tok: tok}

	// lnka fails half its calls, lnkb answers all, lnkc has too few calls.
	seedEvidence(t, e.st, a, "GET", "/linkedin/jobs", 15, 15, 400)
	seedEvidence(t, e.st, b, "GET", "/linkedin/jobs", 30, 0, 200)
	seedEvidence(t, e.st, c, "GET", "/linkedin/jobs", 3, 0, 100)
	foldEvidence(t, e.st)

	out, isErr := m.tool("search_tools", map[string]any{"query": "linkedin jobs", "limit": 3})
	if isErr {
		t.Fatal(out)
	}
	found := out["tools"].([]any)
	if len(found) < 3 {
		t.Fatalf("found %v", found)
	}
	names := []string{}
	for _, f := range found {
		names = append(names, f.(map[string]any)["name"].(string))
	}
	if names[0] != "lnkb_linkedin_jobs_search_jobs" || names[1] != "lnka_linkedin_jobs_search_jobs" || names[2] != "lnkc_linkedin_jobs_search_jobs" {
		t.Fatalf("order %v, want lnkb, lnka, then lnkc with too few calls", names)
	}
	ev, ok := found[0].(map[string]any)["evidence"].(map[string]any)
	if !ok || ev["success_rate"].(float64) != 1 || ev["p50_ms"].(float64) <= 0 || ev["last_ok"] == nil || ev["calls"].(float64) != 30 {
		t.Fatalf("evidence %v", found[0].(map[string]any)["evidence"])
	}
	if found[1].(map[string]any)["evidence"].(map[string]any)["success_rate"].(float64) != 0.5 {
		t.Fatalf("lnka evidence %v", found[1].(map[string]any)["evidence"])
	}
	if _, has := found[2].(map[string]any)["evidence"]; has {
		t.Fatal("a tool under the minimum sample shows evidence")
	}

	// The reliable strategy puts lnkb first for the same call, then lnka,
	// then lnkc whose rate is unknown.
	order := e.api.orderGroup(context.Background(), "g", StrategyReliable, []*store.Service{c, a, b}, "GET", "linkedin/jobs")
	if order[0].Slug != "lnkb" || order[1].Slug != "lnka" || order[2].Slug != "lnkc" {
		t.Fatalf("reliable order %s %s %s", order[0].Slug, order[1].Slug, order[2].Slug)
	}
	// A call with no endpoint stats falls back to the service's.
	order = e.api.orderGroup(context.Background(), "g", StrategyReliable, []*store.Service{a, b}, "GET", "linkedin/in/someone")
	if order[0].Slug != "lnkb" {
		t.Fatalf("service level fallback picked %s", order[0].Slug)
	}

	// The service guide, as service_guide shows it, carries the measured
	// record and a stored sample answer under the endpoint.
	if err := e.st.SaveExampleResponse(context.Background(), store.ExampleResponse{Provider: "catalog-test", Example: "GET /linkedin/jobs",
		Path: "/linkedin/jobs", Status: 200, ContentType: "application/json"}, []byte(`{"jobs":[{"title":"Go developer","company":"Acme"}]}`)); err != nil {
		t.Fatal(err)
	}
	out, isErr = m.tool("service_guide", map[string]any{"service": "lnkb"})
	if isErr {
		t.Fatal(out)
	}
	md, _ := out["markdown"].(string)
	if !strings.Contains(md, "Measured here over the last 7 days (30 calls): 100% success") || !strings.Contains(md, `"company": "Acme"`) {
		t.Fatalf("guide lacks evidence or the sample:\n%s", md)
	}
	var g struct {
		Guide providers.GuideDoc `json:"guide"`
	}
	e.do(e.super, "GET", "/api/admin/services/"+itoa(int(a.ID))+"/guide", nil, 200, &g)
	if len(g.Guide.Evidence) != 1 || g.Guide.Evidence[0].SuccessRate != 0.5 || len(g.Guide.Responses) != 1 {
		t.Fatalf("guide JSON evidence %+v responses %d", g.Guide.Evidence, len(g.Guide.Responses))
	}
}

func TestReliableOrder(t *testing.T) {
	ev := func(rate float64, p50 int64, known bool) func() (float64, int64, bool) {
		return func() (float64, int64, bool) { return rate, p50, known }
	}
	cands := []routeCand{
		{idx: 0, evidence: ev(0, 0, false)},
		{idx: 1, evidence: ev(0.9, 300, true)},
		{idx: 2, evidence: ev(0.99, 900, true)},
		{idx: 3, evidence: ev(0.9, 100, true)},
		{idx: 4},
	}
	got := orderRoutes("x", StrategyReliable, cands)
	want := []int{2, 3, 1, 0, 4}
	for i, c := range got {
		if c.idx != want[i] {
			t.Fatalf("order %v, want %v", idxs(got), want)
		}
	}
	if !routingStrategies[StrategyReliable] {
		t.Fatal("reliable is not a known strategy")
	}
}

func idxs(cs []routeCand) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.idx
	}
	return out
}
