package store

import (
	"context"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateV2IsIdempotent(t *testing.T) {
	s := openTest(t)
	if err := migrate(s.DB); err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}
}

func TestAppUsageAndLimits(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.CreateApp(ctx, &App{Name: "web", Mode: "both", Active: true}, "hash1")
	if err != nil {
		t.Fatal(err)
	}
	two, budget := int64(2), 0.05
	if err := s.SetAppLimits(ctx, a.ID, AppLimits{DailyRequests: &two, MonthlyBudget: &budget}); err != nil {
		t.Fatal(err)
	}
	l, err := s.GetAppLimits(ctx, a.ID)
	if err != nil || l.DailyRequests == nil || *l.DailyRequests != 2 || l.MonthlyBudget == nil || l.DailyBudget != nil {
		t.Fatalf("limits round trip: %+v %v", l, err)
	}
	_ = s.RecordAppUsage(ctx, a.ID, 0.01, false)
	_ = s.RecordAppUsage(ctx, a.ID, 0.01, true) // cache hit: no request, no cost
	u, err := s.GetAppUsage(ctx, a.ID, time.Now())
	if err != nil || u.TodayRequests != 1 || u.TodayHits != 1 || u.MonthCost < 0.0099 || u.MonthCost > 0.0101 {
		t.Fatalf("usage: %+v %v", u, err)
	}
	if hit := CheckLimits(l, u, time.Now()); hit != nil {
		t.Fatalf("not over yet, got %+v", hit)
	}
	_ = s.RecordAppUsage(ctx, a.ID, 0.01, false)
	u, _ = s.GetAppUsage(ctx, a.ID, time.Now())
	hit := CheckLimits(l, u, time.Now())
	if hit == nil || hit.Limit != "daily_requests" || !hit.ResetAt.After(time.Now()) {
		t.Fatalf("want daily_requests hit, got %+v", hit)
	}
}

func TestCheckLimitsResets(t *testing.T) {
	at := time.Date(2026, 3, 31, 15, 0, 0, 0, time.UTC)
	b := 1.0
	hit := CheckLimits(AppLimits{MonthlyBudget: &b}, AppUsage{MonthCost: 1}, at)
	if hit == nil || !hit.ResetAt.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("monthly reset wrong: %+v", hit)
	}
}

func TestGroupsAndSlugNamespace(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	g, err := s.CreateGroup(ctx, &ServiceGroup{Slug: "search", Name: "Search", Members: []string{"serp-a", "serp-b"}})
	if err != nil || len(g.Members) != 2 {
		t.Fatalf("create: %+v %v", g, err)
	}
	if taken, _ := s.SlugTaken(ctx, "search"); !taken {
		t.Fatal("group slug should be taken")
	}
	if taken, _ := s.SlugTaken(ctx, "free"); taken {
		t.Fatal("unused slug is free")
	}
	got, err := s.GetGroupBySlug(ctx, "search")
	if err != nil || got.Members[1] != "serp-b" {
		t.Fatalf("by slug: %+v %v", got, err)
	}
}

func TestTokens(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "a@b.c", "A", "x", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if _, err := s.CreateToken(ctx, &AdminToken{Name: "old", TokenPrefix: "acnadm_ab", Scope: ScopeRead, UserID: u.ID, ExpiresAt: &past}, "h-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToken(ctx, &AdminToken{Name: "ci", TokenPrefix: "acnadm_cd", Scope: ScopeWrite, UserID: u.ID}, "h-ci"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TokenByHash(ctx, "h-old"); err == nil {
		t.Fatal("expired tokens must not authenticate")
	}
	tok, owner, err := s.TokenByHash(ctx, "h-ci")
	if err != nil || owner.ID != u.ID || tok.Scope != ScopeWrite {
		t.Fatalf("lookup: %+v %v", tok, err)
	}
	list, _ := s.ListTokens(ctx, u.ID)
	if len(list) != 2 || list[0].UserEmail != "a@b.c" {
		t.Fatalf("list: %+v", list)
	}
}
