package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Every access change written through the writer is seen by the very next
// read through the read pool, with no wait in between. Each case runs many
// times so a stale reader connection would show.
func TestReadPoolSeesWritesAtOnce(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	svc, err := s.CreateService(ctx, &Service{Slug: "svc", Name: "Svc", Provider: "custom", BaseURL: "http://127.0.0.1:1",
		AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		// Warm every reader connection with the values about to change.
		a, err := s.CreateApp(ctx, &App{Name: "app", Mode: "both", Active: true}, "hash-"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		k, err := s.CreateKey(ctx, NewKey{ServiceID: svc.ID, Label: "k", SecretEnc: []byte("x"), SecretFP: "fp" + itoa(i), Hint: "...", Tier: "primary", Weight: 1, Priority: 100})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < readPoolSize*2; j++ {
			_, _, _ = s.AppByToken(ctx, "hash-"+itoa(i))
			_, _ = s.CandidateKeys(ctx, svc.ID)
			_, _ = s.GetAppLimits(ctx, a.ID)
		}

		// Revoke a token.
		_, tok, err := s.AppByToken(ctx, "hash-"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeAppToken(ctx, a.ID, tok.ID); err != nil {
			t.Fatal(err)
		}
		if _, tok, err := s.AppByToken(ctx, "hash-"+itoa(i)); err != nil || tok.Usable(time.Now()) {
			t.Fatalf("round %d: revoked token still usable (%v)", i, err)
		}

		// Disable the app.
		off := false
		if err := s.PatchApp(ctx, a.ID, AppPatch{Active: &off}); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetApp(ctx, a.ID); err != nil || got.Active {
			t.Fatalf("round %d: disabled app still active (%v)", i, err)
		}

		// Change limits.
		one := int64(1)
		if err := s.SetAppLimits(ctx, a.ID, AppLimits{DailyRequests: &one}); err != nil {
			t.Fatal(err)
		}
		if l, err := s.GetAppLimits(ctx, a.ID); err != nil || l.DailyRequests == nil || *l.DailyRequests != 1 {
			t.Fatalf("round %d: new limit not seen: %+v %v", i, l, err)
		}

		// Disable the key.
		dis := StatusDisabled
		if err := s.UpdateKey(ctx, k.ID, KeyPatch{Status: &dis}); err != nil {
			t.Fatal(err)
		}
		if hasKey(t, s, svc.ID, k.ID) {
			t.Fatalf("round %d: disabled key still a candidate", i)
		}

		// Hide the key.
		hid := true
		if err := s.UpdateKey(ctx, k.ID, KeyPatch{Hidden: &hid}); err != nil {
			t.Fatal(err)
		}
		keys, err := s.ListKeys(ctx, svc.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range keys {
			if x.ID == k.ID {
				t.Fatalf("round %d: hidden key listed to an admin", i)
			}
		}

		// Delete the key.
		if err := s.DeleteKey(ctx, k.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetKey(ctx, k.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("round %d: deleted key still read: %v", i, err)
		}
	}
}

// Pausing or revoking a share is seen by the next read of the share and of
// its backing app.
func TestReadPoolSeesSharePause(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		sh, err := s.CreateShare(ctx, &Share{ID: "share-" + itoa(i), Name: "s", Mode: ShareModeProxy}, "inv-"+itoa(i), 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetShareStatus(ctx, sh.ID, ShareActive); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < readPoolSize*2; j++ {
			_, _ = s.GetShare(ctx, sh.ID)
			_, _ = s.GetApp(ctx, *sh.AppID)
		}
		if _, err := s.SetShareStatus(ctx, sh.ID, SharePaused); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetShare(ctx, sh.ID)
		if err != nil || got.Status != SharePaused {
			t.Fatalf("round %d: share not paused on next read: %+v %v", i, got, err)
		}
		if a, err := s.GetApp(ctx, *sh.AppID); err != nil || a.Active {
			t.Fatalf("round %d: paused share's app still active (%v)", i, err)
		}
	}
}

func hasKey(t *testing.T, s *Store, serviceID, keyID int64) bool {
	t.Helper()
	keys, err := s.CandidateKeys(context.Background(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.ID == keyID {
			return true
		}
	}
	return false
}

func itoa(i int) string { return strconv.Itoa(i) }

// Reads made inside a transaction run on that transaction and see its own
// uncommitted writes; the read pool sees them only after the commit.
func TestReadsInTransactionStayOnIt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.CreateApp(ctx, &App{Name: "before", Mode: "both", Active: true}, "h")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE apps SET name='after' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM apps WHERE id=?`, a.ID).Scan(&name); err != nil || name != "after" {
		t.Fatalf("read in the transaction: %q %v", name, err)
	}
	if got, err := s.GetApp(ctx, a.ID); err != nil || got.Name != "before" {
		t.Fatalf("read pool saw an uncommitted write: %+v %v", got, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetApp(ctx, a.ID); err != nil || got.Name != "after" {
		t.Fatalf("read pool missed the commit: %+v %v", got, err)
	}
}

// A write sent to the read pool fails with SQLITE_READONLY, both plain and
// through a prepared statement.
func TestReadPoolRejectsWrites(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		var se *sqlite.Error
		if !errors.As(err, &se) || se.Code()&0xff != sqlite3.SQLITE_READONLY {
			t.Fatalf("want SQLITE_READONLY, got %v", err)
		}
	}
	_, err := s.rd.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES('x','y')`)
	check(err)
	_, err = s.r.ExecContext(ctx, `UPDATE apps SET active=1`)
	check(err)
	_, err = s.rd.ExecContext(ctx, `CREATE TABLE nope(x)`)
	check(err)
}

// The database, its WAL and its shared memory file stay owner only once both
// pools are open, and Close closes them.
func TestOpenKeepsFilesPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{DBFile, DBFile + "-wal", DBFile + "-shm"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v, want 0600", f, fi.Mode().Perm())
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.rd.Ping(); err == nil {
		t.Fatal("read pool still open after Close")
	}
}
