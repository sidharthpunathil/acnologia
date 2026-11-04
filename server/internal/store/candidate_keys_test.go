package store

import (
	"context"
	"testing"
)

// CandidateKeys loads app lists only when some candidate is restricted. A
// restricted key must still be refused to an unlisted app and allowed to a
// listed one, and for every key and app the pick path (CandidateKeys) must
// agree with the full read (GetKey). The agreement check is what fails if
// AllowsApp ever starts reading AppIDs for an unrestricted key: the key in
// step two keeps its key_apps rows, which CandidateKeys does not load.
func TestCandidateKeysAppListsOnlyWhenRestricted(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	sv, err := s.CreateService(ctx, &Service{Slug: "s", Name: "S", Provider: "generic", BaseURL: "https://x.example", AuthType: "bearer",
		AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 1, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	a1, err := s.CreateApp(ctx, &App{Name: "a1", Mode: "both", Active: true}, "h1")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.CreateApp(ctx, &App{Name: "a2", Mode: "both", Active: true}, "h2")
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.CreateKey(ctx, NewKey{ServiceID: sv.ID, Label: "r", SecretEnc: []byte("c"), SecretFP: "fp1", Hint: "x", Tier: "primary",
		Weight: 1, Priority: 100, Restricted: true, AppIDs: []int64{a1.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateKey(ctx, NewKey{ServiceID: sv.ID, Label: "open", SecretEnc: []byte("c"), SecretFP: "fp2", Hint: "x", Tier: "primary", Weight: 1, Priority: 100}); err != nil {
		t.Fatal(err)
	}
	apps := []int64{a1.ID, a2.ID}
	// allowed returns, per app, whether the restricted test key may serve it
	// on the pick path, after checking that the pick path agrees with GetKey
	// for every candidate.
	allowed := func(step string) map[int64]bool {
		t.Helper()
		cands, err := s.CandidateKeys(ctx, sv.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]bool{}
		for _, c := range cands {
			full, err := s.GetKey(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, app := range apps {
				if got, want := c.AllowsApp(app), full.AllowsApp(app); got != want {
					t.Fatalf("%s: key %d app %d: pick path says %v, full read says %v", step, c.ID, app, got, want)
				}
				if c.ID == k.ID {
					out[app] = c.AllowsApp(app)
				}
			}
		}
		return out
	}

	if got := allowed("restricted to a1"); !got[a1.ID] || got[a2.ID] {
		t.Fatalf("restricted to a1: %v", got)
	}
	// Unrestricted, with its key_apps rows left in place: any app may use it.
	f := false
	if err := s.UpdateKey(ctx, k.ID, KeyPatch{Restricted: &f}); err != nil {
		t.Fatal(err)
	}
	if got := allowed("unrestricted"); !got[a1.ID] || !got[a2.ID] {
		t.Fatalf("unrestricted: %v", got)
	}
	// Restricted again with no apps: nobody, so no stale list survives.
	tr, none := true, []int64{}
	if err := s.UpdateKey(ctx, k.ID, KeyPatch{Restricted: &tr, AppIDs: &none}); err != nil {
		t.Fatal(err)
	}
	if got := allowed("restricted to none"); got[a1.ID] || got[a2.ID] {
		t.Fatalf("restricted to none: %v", got)
	}
	// Restricted to a2 only.
	only2 := []int64{a2.ID}
	if err := s.UpdateKey(ctx, k.ID, KeyPatch{AppIDs: &only2}); err != nil {
		t.Fatal(err)
	}
	if got := allowed("restricted to a2"); got[a1.ID] || !got[a2.ID] {
		t.Fatalf("restricted to a2: %v", got)
	}
}
