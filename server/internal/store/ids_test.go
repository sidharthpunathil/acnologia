package store

import (
	"context"
	"strings"
	"testing"
)

// A new app never takes a deleted app's id, so it never shows the deleted
// app's events as its own.
func TestAppIDsAreNotReused(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a1, err := s.CreateApp(ctx, &App{Name: "alice", Active: true, TokenPrefix: "acn_a"}, "h1")
	if err != nil {
		t.Fatal(err)
	}
	s.AddEvent(ctx, NewEvent{Kind: "proxy", AppID: a1.ID, StatusCode: 200, Detail: "alice's call"})
	if err := s.DeleteApp(ctx, a1.ID); err != nil {
		t.Fatal(err)
	}
	a2, err := s.CreateApp(ctx, &App{Name: "bob", Active: true, TokenPrefix: "acn_b"}, "h2")
	if err != nil {
		t.Fatal(err)
	}
	if a2.ID == a1.ID {
		t.Fatalf("app id %d reused", a2.ID)
	}
	// A share's backing app takes the next id as well.
	sh, err := s.CreateShare(ctx, &Share{ID: "sh-ids", Name: "carol", Services: []string{}}, "invite", 0)
	if err != nil {
		t.Fatal(err)
	}
	if sh.AppID == nil || *sh.AppID <= a2.ID {
		t.Fatalf("share app id %v, want above %d", sh.AppID, a2.ID)
	}
	if err := s.DeleteApp(ctx, *sh.AppID); err != nil {
		t.Fatal(err)
	}
	// Once the deleted app's events are gone too, the counter still holds.
	if _, err := s.DB.Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}
	a3, err := s.CreateApp(ctx, &App{Name: "dave", Active: true, TokenPrefix: "acn_d"}, "h3")
	if err != nil {
		t.Fatal(err)
	}
	if a3.ID <= *sh.AppID {
		t.Fatalf("app id %d after deleting %d", a3.ID, *sh.AppID)
	}
}

// A new service never takes a deleted service's id, so its request counts
// and event list start empty.
func TestServiceIDsAreNotReused(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	mk := func(slug string) *Service {
		sv, err := s.CreateService(ctx, &Service{Slug: slug, Name: slug, Provider: "custom", BaseURL: "https://x", AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 3})
		if err != nil {
			t.Fatal(err)
		}
		return sv
	}
	old := mk("old")
	for i := 0; i < 5; i++ {
		s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: old.ID, StatusCode: 200})
	}
	if err := s.DeleteService(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	nw := mk("new")
	if nw.ID == old.ID {
		t.Fatalf("service id %d reused", nw.ID)
	}
	if err := s.FillStats(ctx, []*Service{nw}, false, nil); err != nil {
		t.Fatal(err)
	}
	evs, err := s.ListEvents(ctx, EventFilter{ServiceID: nw.ID})
	if err != nil {
		t.Fatal(err)
	}
	if nw.Requests24h != 0 || len(evs) != 0 {
		t.Fatalf("new service shows %d requests and %d events", nw.Requests24h, len(evs))
	}
	// A slug clash still fails, and burns no visible id.
	if _, err := s.CreateService(ctx, &Service{Slug: "new", Name: "dup", Provider: "custom", BaseURL: "https://x", AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin"}); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate slug: %v", err)
	}
}

// The high water reads use the events indexes, not a scan of every event.
func TestIDHighWaterPlans(t *testing.T) {
	s := openTest(t)
	for _, c := range []struct{ q, want string }{{appsHighWater, "events_app"}, {servicesHighWater, "events_service"}} {
		if plan := explain(t, s.DB, c.q); !strings.Contains(plan, c.want) {
			t.Errorf("plan %q does not use %s", plan, c.want)
		}
	}
}
