package store

import (
	"context"
	"strings"
	"testing"
)

// A new app token never takes a deleted token's id, so events.token_id
// never shows a deleted token's calls under a new one.
func TestAppTokenIDsAreNotReused(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	maxTok := func() int64 {
		t.Helper()
		var id int64
		if err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM app_tokens`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	a1, err := s.CreateApp(ctx, &App{Name: "alice", Active: true, TokenPrefix: "acn_a"}, "h1")
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.CreateAppToken(ctx, a1.ID, "second", "p", "h2", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.AddEvent(WithAppToken(ctx, t2.ID), NewEvent{Kind: "proxy", AppID: a1.ID, StatusCode: 200, Detail: "alice's call"})
	// Deleting the app takes its tokens with it (ON DELETE CASCADE).
	if err := s.DeleteApp(ctx, a1.ID); err != nil {
		t.Fatal(err)
	}
	if n := maxTok(); n != 0 {
		t.Fatalf("tokens left after the app delete: max id %d", n)
	}
	a2, tok, err := s.CreateAppWithToken(ctx, &App{Name: "bob", Active: true, TokenPrefix: "acn_b"}, "Default", "p", "h3", 0)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ID <= t2.ID {
		t.Fatalf("token id %d after deleting %d", tok.ID, t2.ID)
	}
	if _, at, err := s.AppByToken(ctx, "h3"); err != nil || at.ID != tok.ID {
		t.Fatalf("AppByToken: %v %+v, want token %d", err, at, tok.ID)
	}

	// A rotation's new token goes above too, once the events are gone.
	if _, err := s.DB.Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(ctx, a2.ID); err != nil {
		t.Fatal(err)
	}
	a3, err := s.CreateApp(ctx, &App{Name: "carol", Active: true, TokenPrefix: "acn_c"}, "h4")
	if err != nil {
		t.Fatal(err)
	}
	toks, err := s.ListAppTokens(ctx, a3.ID)
	if err != nil || len(toks) != 1 || toks[0].ID <= tok.ID {
		t.Fatalf("carol's token %+v (%v), want above %d", toks, err, tok.ID)
	}
	rot, err := s.RotateAppToken(ctx, a3.ID, "rotated", "acn_c2", "h5", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if rot.ID <= toks[0].ID {
		t.Fatalf("rotated token id %d, want above %d", rot.ID, toks[0].ID)
	}

	// An insert that lets SQLite pick the id (as share and people claims
	// do) is moved above the counter by the trigger, even after the newest
	// row is deleted.
	if _, err := s.DB.Exec(`DELETE FROM app_tokens WHERE id=?`, rot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO app_tokens(app_id,name,prefix,hash,created_at) VALUES(?,?,?,?,?)`, a3.ID, "claim", "p", "h6", now()); err != nil {
		t.Fatal(err)
	}
	var claimID int64
	if err := s.DB.QueryRow(`SELECT id FROM app_tokens WHERE hash='h6'`).Scan(&claimID); err != nil {
		t.Fatal(err)
	}
	if claimID <= rot.ID {
		t.Fatalf("claimed token id %d, want above %d", claimID, rot.ID)
	}
	next, err := s.CreateAppToken(ctx, a3.ID, "next", "p", "h7", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID <= claimID {
		t.Fatalf("token id %d after the claim took %d", next.ID, claimID)
	}

	// A database from before the counter: its events carry a token id
	// above every stored token. The first start seeds the counter from them.
	if _, err := s.DB.Exec(`INSERT INTO events(ts,kind,app_id,token_id,status_code,latency_ms,detail) VALUES(?,?,?,?,?,?,?)`,
		now(), "proxy", a3.ID, next.ID+100, 200, 0, "old token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DELETE FROM meta WHERE k='app_tokens_seq'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	late, err := s.CreateAppToken(ctx, a3.ID, "late", "p", "h8", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if late.ID <= next.ID+100 {
		t.Fatalf("token id %d, want above the events' %d", late.ID, next.ID+100)
	}
}

// The token high water read touches meta and the app_tokens primary key
// only, never the events table (which has no token_id index).
func TestAppTokenHighWaterPlan(t *testing.T) {
	s := openTest(t)
	plan := explain(t, s.DB, appTokensHighWater)
	if strings.Contains(plan, "events") {
		t.Fatalf("plan %q reads events", plan)
	}
}
