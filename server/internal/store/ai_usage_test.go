package store

import (
	"context"
	"math"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAIUsageLedgerAndReport(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := migrate(s.DB); err != nil {
		t.Fatalf("migrate is not idempotent with the ledger: %v", err)
	}
	at := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return at.AddDate(0, 0, -n) }

	calls := []AICall{
		{TS: day(0), Feature: AIFeatureBuilder, UserID: 1, UserEmail: "a@x.io", Model: "claude-opus-5-5", InputTokens: 100, OutputTokens: 10, CostUSD: 1.5, Ref: "run:1"},
		{TS: day(0), Feature: AIFeatureAssistant, UserID: 2, UserEmail: "b@x.io", Model: "claude-opus-5-5", InputTokens: 50, CacheReadTokens: 7, CostUSD: 0.25, Ref: "thread:3"},
		{TS: day(3), Feature: AIFeatureFill, UserID: 1, UserEmail: "a@x.io", Model: "other", CacheWriteTokens: 9, WebSearches: 2, CostUSD: 0.5, Ref: "form:app"},
		{TS: day(20), Feature: AIFeatureBuilder, UserID: 1, UserEmail: "a@x.io", Model: "claude-opus-5-5", CostUSD: 2, Ref: "run:2"},
		{TS: day(60), Feature: AIFeatureBuilder, UserID: 1, UserEmail: "a@x.io", Model: "claude-opus-5-5", CostUSD: 100, Ref: "run:0"},
	}
	for _, c := range calls {
		if err := s.RecordAICall(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListAICalls(ctx, day(1))
	if err != nil || len(got) != 2 || got[0].Ref != "thread:3" || got[0].CacheReadTokens != 7 || got[0].UserEmail != "b@x.io" {
		t.Fatalf("ledger rows: %+v %v", got, err)
	}

	// Provider spend: a service with a visible and a hidden key, and an app.
	sv, err := s.CreateService(ctx, &Service{Slug: "svc", Name: "Svc", Provider: "serpapi", BaseURL: "https://x.io", AuthType: "query", AuthName: "k", Strategy: "round_robin"})
	if err != nil {
		t.Fatal(err)
	}
	vis, err := s.CreateKey(ctx, NewKey{ServiceID: sv.ID, Label: "v", SecretEnc: []byte("a"), SecretFP: "fa", Hint: "a"})
	if err != nil {
		t.Fatal(err)
	}
	hid, err := s.CreateKey(ctx, NewKey{ServiceID: sv.ID, Label: "h", SecretEnc: []byte("b"), SecretFP: "fb", Hint: "b", Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, &App{Name: "web", Mode: "both", Active: true}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	ev := func(n int, kind string, key int64, cost float64) {
		s.AddEvent(ctx, NewEvent{Kind: kind, ServiceID: sv.ID, KeyID: key, AppID: app.ID, Cost: cost})
		if _, err := s.DB.Exec(`UPDATE events SET ts=? WHERE id=(SELECT MAX(id) FROM events)`, day(n).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	ev(0, "proxy", vis.ID, 0.01)
	ev(0, "proxy", hid.ID, 0.04)
	ev(0, "cache_hit", 0, 0)
	ev(2, "lease", vis.ID, 0.02)
	ev(2, "rate_limited", vis.ID, 0) // not a request
	ev(40, "proxy", vis.ID, 9)       // outside 30 days

	rep, err := s.UsageReport(ctx, at, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Since != "2026-10-04" || rep.Until != "2026-11-02" || len(rep.AI.ByDay) != 30 || len(rep.Provider.ByDay) != 30 {
		t.Fatalf("range: %s..%s, %d/%d days", rep.Since, rep.Until, len(rep.AI.ByDay), len(rep.Provider.ByDay))
	}
	today, week, month := rep.Totals["today"], rep.Totals["7d"], rep.Totals["30d"]
	if today.AICalls != 2 || !near(today.AICostUSD, 1.75) || week.AICalls != 3 || !near(week.AICostUSD, 2.25) ||
		month.AICalls != 4 || !near(month.AICostUSD, 4.25) {
		t.Fatalf("AI totals: today %+v 7d %+v 30d %+v", today, week, month)
	}
	if today.Requests != 1 || !near(today.ProviderCostUSD, 0.01) || today.CacheHits != 1 ||
		week.Requests != 2 || !near(week.ProviderCostUSD, 0.03) || month.Requests != 2 {
		t.Fatalf("provider totals without hidden keys: today %+v 7d %+v 30d %+v", today, week, month)
	}
	if f := rep.AI.ByFeature; len(f) != 3 || f[0].Key != "builder" || f[0].Calls != 2 || !near(f[0].CostUSD, 3.5) || f[0].InputTokens != 100 {
		t.Fatalf("by feature: %+v", f)
	}
	if m := rep.AI.ByModel; len(m) != 2 || m[0].Key != "claude-opus-5-5" || m[1].Key != "other" || m[1].CacheWriteTokens != 9 || m[1].WebSearches != 2 {
		t.Fatalf("by model: %+v", m)
	}
	if u := rep.AI.ByUser; len(u) != 2 || u[0].Key != "a@x.io" || u[0].UserID != 1 || !near(u[0].CostUSD, 4) || u[0].Calls != 3 {
		t.Fatalf("by user: %+v", u)
	}
	last := rep.AI.ByDay[29]
	if last.Key != "2026-11-02" || last.Calls != 2 || !near(last.CostUSD, 1.75) || rep.AI.ByDay[26].Calls != 1 || rep.AI.ByDay[0].Calls != 0 {
		t.Fatalf("AI by day: last %+v", last)
	}
	pd := rep.Provider.ByDay
	if pd[29].Requests != 1 || pd[29].CacheHits != 1 || !near(pd[29].CostUSD, 0.01) || pd[27].Requests != 1 || !near(pd[27].CostUSD, 0.02) {
		t.Fatalf("provider by day: %+v %+v", pd[29], pd[27])
	}
	if sv := rep.Provider.ByService; len(sv) != 1 || sv[0].Key != "svc" || sv[0].Name != "Svc" || sv[0].Requests != 2 || sv[0].CacheHits != 1 || !near(sv[0].CostUSD, 0.03) {
		t.Fatalf("by service: %+v", sv)
	}
	if ap := rep.Provider.ByApp; len(ap) != 1 || ap[0].Key != "web" || ap[0].ID != app.ID || ap[0].Requests != 2 {
		t.Fatalf("by app: %+v", ap)
	}

	// A superadmin sees the hidden key's spend too.
	all, err := s.UsageReport(ctx, at, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	if tt := all.Totals["today"]; tt.Requests != 2 || !near(tt.ProviderCostUSD, 0.05) || !near(all.Provider.ByService[0].CostUSD, 0.07) {
		t.Fatalf("with hidden: %+v %+v", tt, all.Provider.ByService)
	}
	// A wider range reaches older rows.
	wide, _ := s.UsageReport(ctx, at, 90, false)
	if r := wide.Totals["range"]; r.AICalls != 5 || !near(r.ProviderCostUSD, 9.03) || len(wide.AI.ByDay) != 90 {
		t.Fatalf("90 day range: %+v", r)
	}
}
