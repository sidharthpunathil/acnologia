package api

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/refresh"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// fakeRefreshClaude answers the Messages API with one submit_offers or
// submit_prices call and records the models it was asked for.
type fakeRefreshClaude struct {
	mu     sync.Mutex
	models []string
	offers []providers.Offer
	prices []agent.ModelPrice
	// guide answers submit_guide; nil answers verified false.
	guide *providers.Guide
	// bodies keeps every request body.
	bodies []string
}

func (f *fakeRefreshClaude) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
		_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
		return
	}
	b, _ := io.ReadAll(r.Body)
	var req struct {
		Model string `json:"model"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(b, &req)
	f.mu.Lock()
	f.models = append(f.models, req.Model)
	f.bodies = append(f.bodies, string(b))
	f.mu.Unlock()
	name, input := "submit_offers", map[string]any{"offers": f.offers, "verified": true, "summary": "Read the pricing page."}
	for _, t := range req.Tools {
		switch t.Name {
		case "submit_prices":
			name, input = "submit_prices", map[string]any{"prices": f.prices, "verified": true, "summary": "Read the pricing page."}
		case "submit_guide":
			name, input = "submit_guide", map[string]any{"guide": f.guide, "verified": f.guide != nil, "summary": "Read the API reference."}
		}
	}
	resp := map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": req.Model,
		"content":     []any{map[string]any{"type": "tool_use", "id": "tu_1", "name": name, "input": input}},
		"stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 50_000, "output_tokens": 10_000}}
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeRefreshClaude) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.models...)
}

func (e *env) useFakeClaude() *fakeRefreshClaude {
	f := &fakeRefreshClaude{}
	srv := httptest.NewServer(f)
	e.t.Cleanup(srv.Close)
	e.api.Agent = agent.New(agent.Config{APIKey: "test", BaseURL: srv.URL}, e.st, e.api.Catalog, e.api.Pool.Box)
	return f
}

// waitRun polls a refresh run until it is no longer running.
func (e *env) waitRun(id int64) (store.RefreshRun, []store.RefreshItem) {
	e.t.Helper()
	e.api.Refresh.Wait()
	var out struct {
		Run   store.RefreshRun    `json:"run"`
		Items []store.RefreshItem `json:"items"`
	}
	for i := 0; i < 100; i++ {
		e.do(e.super, "GET", "/api/admin/providers/refresh/runs/"+id64(id), nil, 200, &out)
		if out.Run.Status != store.RefreshRunning {
			return out.Run, out.Items
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("the run never finished")
	return out.Run, out.Items
}

func id64(n int64) string { b, _ := json.Marshal(n); return string(b) }

func auditActions(t *testing.T, st *store.Store) map[string][]string {
	t.Helper()
	entries, err := st.ListAudit(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, a := range entries {
		out[a.Action] = append(out[a.Action], a.Target+" "+a.Detail)
	}
	return out
}

func TestProviderRefreshAPI(t *testing.T) {
	e := newEnv(t)
	fc := e.useFakeClaude()
	ctx := context.Background()
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	admin, member := client(), client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	e.do(member, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)

	// The refresh slot runs on its own model.
	e.do(e.super, "PUT", "/api/admin/ai/models", map[string]any{"refresh": "claude-haiku-5-5"}, 200, nil)

	// Roles: admins read, only a superadmin writes; members see nothing.
	var st struct {
		Settings  refresh.Settings `json:"settings"`
		AIEnabled bool             `json:"ai_enabled"`
		Model     struct {
			Model  string  `json:"model"`
			Source string  `json:"source"`
			Input  float64 `json:"input_price"`
		} `json:"model"`
		NextRun *time.Time `json:"next_run"`
		CanEdit bool       `json:"can_edit"`
	}
	e.do(admin, "GET", "/api/admin/providers/refresh/settings", nil, 200, &st)
	if st.CanEdit || !st.AIEnabled || st.Model.Model != "claude-haiku-5-5" || st.Model.Source != agent.SourceOverride || st.NextRun == nil {
		t.Fatalf("state: %+v", st)
	}
	e.do(member, "GET", "/api/admin/providers/refresh/settings", nil, 403, nil)
	e.do(admin, "GET", "/api/admin/providers/refresh/runs", nil, 200, nil)
	e.do(admin, "GET", "/api/admin/providers/refresh/reviews", nil, 200, nil)
	set := refresh.Defaults()
	set.Apply, set.ClaudePrices, set.Include, set.StaleDays = refresh.ApplyReview, false, []string{"serpapi"}, 0
	for _, c := range []*http.Client{admin, member} {
		e.do(c, "PUT", "/api/admin/providers/refresh/settings", set, 403, nil)
		e.do(c, "POST", "/api/admin/providers/refresh/run", nil, 403, nil)
		e.do(c, "POST", "/api/admin/providers/serpapi/refresh", nil, 403, nil)
		e.do(c, "POST", "/api/admin/providers/refresh/reviews/1/apply", nil, 403, nil)
		e.do(c, "POST", "/api/admin/providers/refresh/reviews/1/dismiss", nil, 403, nil)
	}
	e.do(e.super, "PUT", "/api/admin/providers/refresh/settings", map[string]any{"schedule": "hourly"}, 400, nil)
	e.do(e.super, "PUT", "/api/admin/providers/refresh/settings", set, 200, &st)
	if st.Settings.Apply != refresh.ApplyReview || !st.CanEdit {
		t.Fatalf("saved: %+v", st.Settings)
	}

	// Check now, on one provider: the answer repeats the offers, so nothing
	// changes but checked_at.
	p, _ := e.st.GetProvider(ctx, "serpapi")
	spec, _ := providers.DecodeSpec(p.Spec)
	fc.offers = spec.Offers
	var started struct {
		Run store.RefreshRun `json:"run"`
	}
	e.do(e.super, "POST", "/api/admin/providers/serpapi/refresh", nil, 202, &started)
	run, items := e.waitRun(started.Run.ID)
	if run.Trigger != store.RefreshOne || len(items) != 1 || items[0].Status != store.ItemUnchanged {
		t.Fatalf("check one: %+v %+v", run, items)
	}
	if got := fc.asked(); len(got) != 1 || got[0] != "claude-haiku-5-5" {
		t.Fatalf("models asked: %v", got)
	}
	// The cost is in the ledger under the refresh feature, at Haiku's price.
	calls, _ := e.st.ListAICalls(ctx, time.Now().Add(-time.Hour))
	if len(calls) != 1 || calls[0].Feature != "refresh" || calls[0].Ref != agent.RefreshRef(run.ID) || calls[0].UserEmail != "root@x.io" ||
		math.Abs(calls[0].CostUSD-0.01) > 1e-9 || math.Abs(items[0].CostUSD-0.01) > 1e-9 {
		t.Fatalf("ledger: %+v item %v", calls, items[0].CostUSD)
	}
	var prov struct {
		Version int                    `json:"version"`
		Refresh *store.ProviderRefresh `json:"refresh"`
	}
	e.do(member, "GET", "/api/admin/providers/serpapi", nil, 200, &prov)
	if prov.Version != p.Version || prov.Refresh == nil || prov.Refresh.Trigger != store.RefreshOne {
		t.Fatalf("provider refresh state: %+v", prov)
	}

	// A changed price waits for review (apply is review), then is dismissed.
	changed := append([]providers.Offer{}, spec.Offers...)
	changed[0].Notes = "Plans changed this month."
	changed[0].SourceURL = "https://serpapi.com/pricing"
	fc.offers = changed
	e.do(e.super, "POST", "/api/admin/providers/refresh/run", nil, 202, &started)
	_, items = e.waitRun(started.Run.ID)
	if len(items) != 1 || items[0].Status != store.ItemReview || items[0].ReviewID == nil {
		t.Fatalf("run: %+v", items)
	}
	var rv struct {
		Reviews []store.RefreshReview `json:"reviews"`
	}
	e.do(admin, "GET", "/api/admin/providers/refresh/reviews", nil, 200, &rv)
	if len(rv.Reviews) != 1 || !strings.Contains(string(rv.Reviews[0].Diff), "notes") {
		t.Fatalf("reviews: %+v", rv)
	}
	e.do(e.super, "POST", "/api/admin/providers/refresh/reviews/"+id64(rv.Reviews[0].ID)+"/dismiss", nil, 200, nil)
	e.do(e.super, "POST", "/api/admin/providers/refresh/reviews/"+id64(rv.Reviews[0].ID)+"/apply", nil, 409, nil)

	// The same change again, applied by hand: a new version labelled refresh.
	e.do(e.super, "POST", "/api/admin/providers/serpapi/refresh", nil, 202, &started)
	_, items = e.waitRun(started.Run.ID)
	var applied struct {
		Provider struct {
			Version int `json:"version"`
		} `json:"provider"`
	}
	e.do(e.super, "POST", "/api/admin/providers/refresh/reviews/"+id64(*items[0].ReviewID)+"/apply", nil, 200, &applied)
	if applied.Provider.Version != p.Version+1 {
		t.Fatalf("applied: %+v", applied)
	}
	vs, _ := e.st.ProviderVersions(ctx, "serpapi")
	if vs[0].Source != "refresh" {
		t.Fatalf("version source %s", vs[0].Source)
	}

	acts := auditActions(t, e.st)
	for _, a := range []string{"provider.refresh.settings", "provider.refresh.run", "provider.refresh.check", "provider.refresh.dismiss", "provider.refresh.apply"} {
		if len(acts[a]) == 0 {
			t.Fatalf("no %s audit entry: %v", a, acts)
		}
	}

	// The webhook event exists for subscriptions.
	found := false
	var types struct {
		Types []struct {
			Type string `json:"type"`
		} `json:"event_types"`
	}
	e.do(e.super, "GET", "/api/admin/webhooks", nil, 200, &types)
	for _, x := range types.Types {
		found = found || x.Type == "provider.offers_changed"
	}
	if !found {
		t.Fatalf("event types: %+v", types)
	}
}

func TestPriceOverridesAndPriceRefresh(t *testing.T) {
	e := newEnv(t)
	fc := e.useFakeClaude()
	ctx := context.Background()
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	e.do(e.super, "PUT", "/api/admin/ai/models", map[string]any{"refresh": "claude-haiku-5-5"}, 200, nil)

	// Only a superadmin edits prices; bad values are refused.
	body := map[string]any{"input": 1, "output": 2, "cache_write": 0, "cache_read": 0, "context_window": 500000, "source_url": "https://example.com/p"}
	e.do(admin, "PUT", "/api/admin/ai/prices/claude-haiku-5-5", body, 403, nil)
	e.do(e.super, "PUT", "/api/admin/ai/prices/claude-haiku-5-5", map[string]any{"input": -1, "output": 2}, 400, nil)
	e.do(e.super, "PUT", "/api/admin/ai/prices/claude-made-up-9", body, 400, nil)
	var models aiModelsResp
	e.do(e.super, "PUT", "/api/admin/ai/prices/claude-haiku-5-5", body, 200, &models)
	for _, m := range models.Models {
		if m.ID == "claude-haiku-5-5" && (m.InputPrice != 1 || m.OutputPrice != 2 || m.ContextWindow != 500000) {
			t.Fatalf("listed: %+v", m)
		}
	}
	if w := e.api.Agent.ContextWindowOf("claude-haiku-5-5"); w != 500000 {
		t.Fatalf("window %d", w)
	}

	// Cost accounting uses the override: 50K input at $1 and 10K output at
	// $2 per MTok is $0.07 (the built in Haiku price would be $0.01).
	p, _ := e.st.GetProvider(ctx, "serpapi")
	spec, _ := providers.DecodeSpec(p.Spec)
	fc.offers = spec.Offers
	var started struct {
		Run store.RefreshRun `json:"run"`
	}
	e.do(e.super, "POST", "/api/admin/providers/serpapi/refresh", nil, 202, &started)
	_, items := e.waitRun(started.Run.ID)
	if math.Abs(items[0].CostUSD-0.07) > 1e-9 {
		t.Fatalf("cost with override: %v", items[0].CostUSD)
	}
	// Reset to built in.
	e.do(e.super, "DELETE", "/api/admin/ai/prices/claude-haiku-5-5", nil, 200, nil)
	if pr, _ := e.api.Agent.PriceOf("claude-haiku-5-5"); pr.Input != 0.10 {
		t.Fatalf("reset: %+v", pr)
	}

	// The price refresh: the model reads the pricing page and proposes Opus
	// at $3 input with a source, so it applies on its own.
	set := refresh.Defaults()
	set.ClaudePrices, set.Include, set.MaxRunUSD, set.MaxMonthUSD = true, []string{}, 50, 100
	set.Scope = refresh.ScopeWithKeys // serpapi has no keys: only the price target runs
	e.do(e.super, "PUT", "/api/admin/providers/refresh/settings", set, 200, nil)
	cur := e.api.Agent.CurrentPrices(ctx)
	next := []agent.ModelPrice{}
	for _, c := range cur {
		if c.Model == "claude-opus-5-5" {
			c.Input, c.SourceURL = 3, agent.PricesSourceURL
		}
		next = append(next, c)
	}
	fc.prices = next
	e.do(e.super, "POST", "/api/admin/providers/refresh/run", nil, 202, &started)
	_, items = e.waitRun(started.Run.ID)
	if len(items) != 1 || items[0].Slug != refresh.PricesTarget || items[0].Status != store.ItemApplied {
		t.Fatalf("price run: %+v", items)
	}
	if pr, _ := e.api.Agent.PriceOf("claude-opus-5-5"); pr.Input != 3 {
		t.Fatalf("opus price: %+v", pr)
	}
	e.do(e.super, "GET", "/api/admin/ai/models", nil, 200, &models)
	var raw struct {
		Models []agent.ModelInfo `json:"models"`
	}
	e.do(e.super, "GET", "/api/admin/ai/models", nil, 200, &raw)
	for _, m := range raw.Models {
		if m.ID == "claude-opus-5-5" && (m.PriceSource != "refresh" || m.PriceURL != agent.PricesSourceURL || m.PriceChecked == "") {
			t.Fatalf("opus info: %+v", m)
		}
	}
	if acts := auditActions(t, e.st); len(acts["ai.prices"]) < 3 {
		t.Fatalf("price audits: %v", acts["ai.prices"])
	}
}
