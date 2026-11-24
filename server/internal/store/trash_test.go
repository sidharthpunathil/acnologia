package store

import (
	"context"
	"testing"
	"time"
)

// trashFixture is a service with two keys (one shared by secret with a
// second service), a second service that borrows the first one's keys, and
// an app, before anything is trashed.
type trashFixture struct {
	s          *Store
	svc, other *Service
	k1, k2     *Key
	twin       *Key
	app        *App
}

func newTrashFixture(t *testing.T) *trashFixture {
	t.Helper()
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`INSERT INTO providers(slug,name,spec,created_at,updated_at) VALUES('custom','Custom','{}',0,0)`); err != nil {
		t.Fatal(err)
	}
	f := &trashFixture{s: s, svc: testService(t, s)}
	other, err := s.CreateService(ctx, &Service{Slug: "other", Name: "Other", Provider: "custom", BaseURL: "http://127.0.0.1:1",
		AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	f.other = other
	f.k1 = testKey(t, s, f.svc, "fp-one", false)
	f.k2 = testKey(t, s, f.svc, "fp-two", false)
	f.twin = testKey(t, s, other, "fp-one", false)
	if _, err := s.DB.Exec(`UPDATE services SET key_source_id=? WHERE id=?`, f.svc.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	eg, err := s.CreateEgress(ctx, &EgressProxy{Name: "eg", URLEnc: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE services SET egress_id=? WHERE id=?`, eg.ID, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE keys SET egress_id=?, cooldown_until=? WHERE id=?`, eg.ID, time.Now().Add(time.Hour).Unix(), f.k1.ID); err != nil {
		t.Fatal(err)
	}
	if f.app, err = s.CreateApp(ctx, &App{Name: "web", Mode: "both", Active: true}, "apphash"); err != nil {
		t.Fatal(err)
	}
	return f
}

// Each read path that leaves trashed rows out, checked before and after
// the trash and after a restore.
func TestTrashReadPaths(t *testing.T) {
	f := newTrashFixture(t)
	s, ctx := f.s, context.Background()

	type check struct {
		name string
		// visible reports whether the trashed items show.
		visible func() bool
	}
	keyIn := func(keys []*Key, id int64) bool {
		for _, k := range keys {
			if k.ID == id {
				return true
			}
		}
		return false
	}
	checks := []check{
		{"GetService", func() bool { _, err := s.GetService(ctx, f.svc.ID); return err == nil }},
		{"GetServiceBySlug", func() bool { _, err := s.GetServiceBySlug(ctx, "svc"); return err == nil }},
		{"ListServices", func() bool {
			l, _ := s.ListServices(ctx)
			for _, sv := range l {
				if sv.ID == f.svc.ID {
					return true
				}
			}
			return false
		}},
		{"key source of a borrower", func() bool { o, _ := s.GetService(ctx, f.other.ID); return o.KeySourceID != nil }},
		{"FillStats", func() bool {
			o, _ := s.GetService(ctx, f.other.ID)
			_ = s.FillStats(ctx, []*Service{o}, true, nil)
			return o.KeyCounts.Total > 1
		}},
		{"ListKeys", func() bool { l, _ := s.ListKeys(ctx, f.svc.ID, true); return keyIn(l, f.k1.ID) }},
		{"ListAllKeys", func() bool { l, _ := s.ListAllKeys(ctx, `WHERE service_id=?`, f.svc.ID); return keyIn(l, f.k1.ID) }},
		{"GetKey", func() bool { _, err := s.GetKey(ctx, f.k2.ID); return err == nil }},
		{"CandidateKeys", func() bool { l, _ := s.CandidateKeys(ctx, f.svc.ID); return keyIn(l, f.k2.ID) }},
		{"ScopeKeys", func() bool {
			r, _ := s.ScopeKeys(ctx, f.svc.ID, f.app.ID, true, nil)
			return r.Total > 0
		}},
		{"SharedCandidateKeys", func() bool { l, _ := s.SharedCandidateKeys(ctx, f.svc.ID, f.other.ID); return keyIn(l, f.k2.ID) }},
		{"FillTwins", func() bool {
			tw, _ := s.GetKey(ctx, f.twin.ID)
			_ = s.FillTwins(ctx, []*Key{tw}, true)
			return len(tw.Twins) > 0
		}},
		{"DuplicateKeyGroups", func() bool { g, _ := s.DuplicateKeyGroups(ctx); return len(g) > 0 }},
		{"DistinctKeyStats", func() bool { n, _, _ := s.DistinctKeyStats(ctx, []int64{f.svc.ID}, true); return n > 0 }},
		{"AvailableKeyCounts", func() bool { m, _ := s.AvailableKeyCounts(ctx); return m[f.svc.ID] > 0 }},
		{"KeysRestingUntil", func() bool { m, _ := s.KeysRestingUntil(ctx); _, ok := m[f.svc.ID]; return ok }},
		{"CoolingKeys", func() bool { l, _ := s.CoolingKeys(ctx, time.Now()); return len(l) > 0 }},
		{"ProvidersWithServices and usable keys", func() bool {
			// other keeps the provider in use, so only the counts move.
			p, _ := s.GetProvider(ctx, "custom")
			return p.ServicesCount == 2 && p.KeysCount == 3
		}},
		{"ListEgress counts", func() bool { l, _ := s.ListEgress(ctx); return l[0].Services == 1 && l[0].Keys == 1 }},
		{"GetApp", func() bool { _, err := s.GetApp(ctx, f.app.ID); return err == nil }},
		{"ListApps", func() bool { l, _ := s.ListApps(ctx); return len(l) == 1 }},
		{"AppsExist", func() bool { ok, _ := s.AppsExist(ctx, []int64{f.app.ID}); return ok }},
		{"AppByToken", func() bool { _, _, err := s.AppByToken(ctx, "apphash"); return err == nil }},
	}
	for _, c := range checks {
		if !c.visible() {
			t.Errorf("%s: not visible before the trash", c.name)
		}
	}
	svcItem, err := s.TrashServiceByID(ctx, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	appItem, err := s.TrashAppByID(ctx, f.app.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if c.visible() {
			t.Errorf("%s: a trashed item shows", c.name)
		}
	}
	if svcItem.Keys != 2 {
		t.Fatalf("service took %d keys", svcItem.Keys)
	}
	if _, err := s.RestoreTrash(ctx, svcItem.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, appItem.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if !c.visible() {
			t.Errorf("%s: not visible after the restore", c.name)
		}
	}
}

func TestTrashKeyConflictsAndOrder(t *testing.T) {
	f := newTrashFixture(t)
	s, ctx := f.s, context.Background()
	kItem, err := s.TrashKeyByID(ctx, f.k1.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Trashing again is not found; the same secret can be added again.
	if _, err := s.TrashKeyByID(ctx, f.k1.ID, nil); err != ErrNotFound {
		t.Fatalf("second trash: %v", err)
	}
	again := testKey(t, s, f.svc, "fp-one", false)
	if _, err := s.RestoreTrash(ctx, kItem.ID); err == nil {
		t.Fatal("restore over a live copy of the secret")
	}
	if err := s.DeleteKey(ctx, again.ID); err != nil {
		t.Fatal(err)
	}
	// A key whose service is in the trash waits for the service.
	svcItem, err := s.TrashServiceByID(ctx, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if svcItem.Keys != 1 {
		t.Fatalf("service took %d keys, the trashed one stays its own", svcItem.Keys)
	}
	if _, err := s.RestoreTrash(ctx, kItem.ID); err == nil {
		t.Fatal("key restored into a trashed service")
	}
	if _, err := s.RestoreTrash(ctx, svcItem.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(ctx, kItem.ID); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetKey(ctx, f.k1.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fp string
	_ = s.DB.QueryRow(`SELECT secret_fp FROM keys WHERE id=?`, k.ID).Scan(&fp)
	if fp != "fp-one" {
		t.Fatalf("fingerprint after restore: %q", fp)
	}
	// Purging a service takes its own trashed keys' entries along.
	if _, err := s.TrashKeyByID(ctx, f.k1.ID, nil); err != nil {
		t.Fatal(err)
	}
	svcItem, _ = s.TrashServiceByID(ctx, f.svc.ID, nil)
	if _, err := s.PurgeTrash(ctx, svcItem.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListTrash(ctx, true); len(l) != 0 {
		t.Fatalf("left in the trash: %+v", l)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM trash`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d trash rows left", n)
	}
}
