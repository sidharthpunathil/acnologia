package api

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type usageResp struct {
	Days      int    `json:"days"`
	AIEnabled bool   `json:"ai_enabled"`
	AIModel   string `json:"ai_model"`
	Totals    map[string]store.UsageTotals
	AI        store.AIUsageReport
	Provider  store.ProviderUsageReport
}

func TestUsageRecordsEveryFeatureAndAggregates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Assistant: two model calls in one turn (a tool call, then the answer).
	f := &fakeClaude{replies: []string{toolUse("tu_1", "list_services", `{}`), text("You have one service.")}}
	e.api.chatModel = f.converse
	thread := e.newChat(e.super)
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(thread))+"/messages", map[string]any{"text": "what services?"}, 200, nil)

	// Fill with AI, against a fake Anthropic endpoint.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn",
			"content":[{"type":"text","text":"{\"fields\":{\"name\":\"Site\"},\"notes\":\"\"}"}],
			"usage":{"input_tokens":1000,"output_tokens":100}}`))
	}))
	defer fake.Close()
	e.api.Agent = agent.New(agent.Config{APIKey: "test", BaseURL: fake.URL}, e.st, e.api.Catalog, e.api.Pool.Box)
	e.do(e.super, "POST", "/api/admin/ai/assist", map[string]any{"form": "app", "instruction": "a site"}, 200, nil)

	calls, err := e.st.ListAICalls(ctx, time.Now().Add(-time.Hour))
	if err != nil || len(calls) != 3 {
		t.Fatalf("want 3 ledger rows (2 assistant, 1 fill): %+v %v", calls, err)
	}
	if c := calls[0]; c.Feature != "fill" || c.UserEmail != "root@x.io" || c.Model != "claude-opus-5-5" || math.Abs(c.CostUSD-0.006) > 1e-12 {
		t.Fatalf("fill row: %+v", c)
	}
	for _, c := range calls[1:] {
		if c.Feature != "assistant" || c.Ref != agent.ThreadRef(thread) || c.UserID != 1 || c.InputTokens != 1 || c.OutputTokens != 1 || c.Model != "fake" {
			t.Fatalf("assistant row: %+v", c)
		}
	}

	// Provider spend: a priced service, one visible and one hidden key.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"cost_per_request": 0.01}, 200, nil)
	ks := e.addKeys(map[string]any{"secret": "visible1"}, map[string]any{"secret": "hidden01", "hidden": true})
	e.do(e.super, "PATCH", "/api/admin/keys/"+itoa(int(ks[1].ID)), map[string]any{"status": "disabled"}, 200, nil)
	appID, tok := e.app(map[string]any{"name": "site"})
	for i := 0; i < 3; i++ {
		e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q="+itoa(i), nil, 200, nil, "Authorization", "Bearer "+tok)
	}
	e.st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: 1, KeyID: ks[1].ID, AppID: appID, Cost: 0.5})

	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	member := client()
	e.do(member, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)

	var m usageResp
	e.do(member, "GET", "/api/admin/usage?days=7", nil, 200, &m)
	if m.Days != 7 || !m.AIEnabled || len(m.AI.ByDay) != 7 || len(m.Provider.ByDay) != 7 {
		t.Fatalf("shape: %+v", m)
	}
	aiWant := 0.006 + 2*agent.Cost("fake", agent.Tokens{Input: 1, Output: 1})
	today := m.Totals["today"]
	if today.AICalls != 3 || math.Abs(today.AICostUSD-aiWant) > 1e-9 {
		t.Fatalf("AI today: %+v", today)
	}
	if today.Requests != 3 || math.Abs(today.ProviderCostUSD-0.03) > 1e-9 {
		t.Fatalf("member must not see hidden spend: %+v", today)
	}
	if len(m.AI.ByFeature) != 2 || m.AI.ByFeature[0].Key != "fill" || m.AI.ByFeature[1].Calls != 2 {
		t.Fatalf("by feature: %+v", m.AI.ByFeature)
	}
	if len(m.AI.ByUser) != 1 || m.AI.ByUser[0].Key != "root@x.io" || m.AI.ByUser[0].Calls != 3 {
		t.Fatalf("by user: %+v", m.AI.ByUser)
	}
	if s := m.Provider.ByService; len(s) != 1 || s[0].Key != "serpapi" || s[0].Requests != 3 || math.Abs(s[0].CostUSD-0.03) > 1e-9 {
		t.Fatalf("by service: %+v", s)
	}
	if a := m.Provider.ByApp; len(a) != 1 || a[0].Key != "site" || a[0].ID != appID {
		t.Fatalf("by app: %+v", a)
	}
	if d := m.Provider.ByDay[6]; d.Requests != 3 || math.Abs(d.CostUSD-0.03) > 1e-9 {
		t.Fatalf("provider today: %+v", d)
	}

	var sup usageResp
	e.do(e.super, "GET", "/api/admin/usage", nil, 200, &sup)
	if sup.Days != 30 || sup.Totals["30d"].Requests != 4 || math.Abs(sup.Totals["30d"].ProviderCostUSD-0.53) > 1e-9 {
		t.Fatalf("superadmin totals: %+v", sup.Totals["30d"])
	}

	e.do(member, "GET", "/api/admin/usage?days=0", nil, 400, nil)
	e.do(member, "GET", "/api/admin/usage?days=abc", nil, 400, nil)
	e.do(client(), "GET", "/api/admin/usage", nil, 401, nil)
}
