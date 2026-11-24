package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// rawDB opens the database file in dir directly, around the store.
func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", writerDSN(filepath.Join(dir, DBFile)))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func dbVersion(t *testing.T, dir string) int {
	t.Helper()
	db := rawDB(t, dir)
	defer db.Close()
	v, err := userVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// upgradeCopies lists the pre-upgrade copies in dir, sorted.
func upgradeCopies(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), DBFile+".pre-v") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// countingSteps returns n numbered steps; each adds one to runs[i] and to a
// counter table, so a step that ran twice shows in the data too.
func countingSteps(n int, runs []int) []migration {
	steps := make([]migration, n)
	for i := range steps {
		steps[i] = migration{name: fmt.Sprintf("step %d", i+1), run: func(db *sql.DB) error {
			runs[i]++
			if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS step_runs(step INTEGER NOT NULL)`); err != nil {
				return err
			}
			_, err := db.Exec(`INSERT INTO step_runs(step) VALUES(?)`, i+1)
			return err
		}}
	}
	return steps
}

func addService(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO services(id,slug,name,provider,base_url,auth_type,auth_name,strategy,cooldown_sec,max_attempts,created_at) VALUES(7,'openai','OpenAI','openai','https://x','bearer','Authorization','round_robin',30,3,1)`); err != nil {
		t.Fatal(err)
	}
}

func TestFreshDatabaseGetsLatestVersion(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.SchemaVersion(); got != LatestSchemaVersion() {
		t.Fatalf("SchemaVersion %d, want %d", got, LatestSchemaVersion())
	}
	s.Close()
	if got := dbVersion(t, dir); got != LatestSchemaVersion() {
		t.Fatalf("user_version %d, want %d", got, LatestSchemaVersion())
	}
}

// A database made before versioning (user_version 0, and here missing a
// column the baseline adds) gets the baseline and keeps its rows.
func TestPreVersioningDatabaseUpgrades(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	addService(t, s)
	s.Close()
	db := rawDB(t, dir)
	for _, q := range []string{`ALTER TABLE keys DROP COLUMN last_ok_at`, `PRAGMA user_version=0`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.SchemaVersion(); got != LatestSchemaVersion() {
		t.Fatalf("SchemaVersion %d, want %d", got, LatestSchemaVersion())
	}
	var slug string
	if err := s.DB.QueryRow(`SELECT slug FROM services WHERE id=7`).Scan(&slug); err != nil || slug != "openai" {
		t.Fatalf("service lost in the upgrade: %q %v", slug, err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('keys') WHERE name='last_ok_at'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("baseline did not add keys.last_ok_at back: %d %v", n, err)
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	newer := LatestSchemaVersion() + 5
	db := rawDB(t, dir)
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, newer)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = OpenWith(dir, Options{MigrationBackup: true})
	var tooNew *SchemaTooNewError
	if !errors.As(err, &tooNew) {
		t.Fatalf("want SchemaTooNewError, got %v", err)
	}
	if tooNew.Found != newer || tooNew.Known != LatestSchemaVersion() {
		t.Fatalf("versions in the error: %+v", tooNew)
	}
	msg := err.Error()
	for _, want := range []string{
		fmt.Sprintf("schema version %d", newer),
		fmt.Sprintf("up to version %d", LatestSchemaVersion()),
		"Upgrade this binary", "restore",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q lacks %q", msg, want)
		}
	}
	if strings.HasPrefix(msg, "migrate:") {
		t.Fatalf("error should read on its own: %q", msg)
	}
	if got := dbVersion(t, dir); got != newer {
		t.Fatalf("refused open changed user_version to %d", got)
	}
	if c := upgradeCopies(t, dir); len(c) != 0 {
		t.Fatalf("refused open made copies: %v", c)
	}
}

func TestNumberedStepRunsOnce(t *testing.T) {
	dir := t.TempDir()
	runs := make([]int, 2)
	steps := countingSteps(2, runs)
	for range 3 {
		s, err := open(dir, Options{}, steps)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.SchemaVersion(); got != baselineVersion+2 {
			t.Fatalf("SchemaVersion %d, want %d", got, baselineVersion+2)
		}
		s.Close()
	}
	if runs[0] != 1 || runs[1] != 1 {
		t.Fatalf("steps ran %v times, want once each", runs)
	}
	db := rawDB(t, dir)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM step_runs`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("step_runs rows %d %v, want 2", n, err)
	}
}

// A failing step rolls back the steps before it and leaves the version.
func TestFailedStepRollsBack(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	runs := make([]int, 1)
	steps := append(countingSteps(1, runs), migration{name: "boom", run: func(*sql.DB) error { return errors.New("boom") }})
	if _, err := open(dir, Options{}, steps); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want the step's error, got %v", err)
	}
	if got := dbVersion(t, dir); got != baselineVersion {
		t.Fatalf("user_version %d after a failed upgrade, want %d", got, baselineVersion)
	}
	db := rawDB(t, dir)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='step_runs'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the first step's table survived the rollback: %d %v", n, err)
	}
}

func TestUpgradeCopiesAreTakenAndPruned(t *testing.T) {
	dir := t.TempDir()
	runs := make([]int, 4)
	all := countingSteps(4, runs)

	// A brand new database is never copied.
	s, err := open(dir, Options{MigrationBackup: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	addService(t, s)
	s.Close()
	if c := upgradeCopies(t, dir); len(c) != 0 {
		t.Fatalf("fresh database copied: %v", c)
	}

	// Without the option nothing is copied either.
	s, err = open(dir, Options{}, all[:1])
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if c := upgradeCopies(t, dir); len(c) != 0 {
		t.Fatalf("copied with the option off: %v", c)
	}

	for n := 2; n <= 4; n++ {
		s, err := open(dir, Options{MigrationBackup: true}, all[:n])
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	// One more upgrade, with nothing new to run: no copy.
	s, err = open(dir, Options{MigrationBackup: true}, all)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	want := []string{
		DBFile + ".pre-v2-to-v3.bak",
		DBFile + ".pre-v3-to-v4.bak",
		DBFile + ".pre-v4-to-v5.bak",
	}
	if got := upgradeCopies(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("copies %v, want %v", got, want)
	}

	// Make one more upgrade: the oldest copy goes, three stay.
	more := append(all, countingSteps(1, make([]int, 1))...)
	s, err = open(dir, Options{MigrationBackup: true}, more)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	want = []string{
		DBFile + ".pre-v3-to-v4.bak",
		DBFile + ".pre-v4-to-v5.bak",
		DBFile + ".pre-v5-to-v6.bak",
	}
	if got := upgradeCopies(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("copies %v, want %v", got, want)
	}

	// The newest copy is the database as it was before that upgrade.
	cp := filepath.Join(dir, want[2])
	if fi, err := os.Stat(cp); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("copy mode: %v %v", fi.Mode(), err)
	}
	db, err := sql.Open("sqlite", "file:"+uriPath(cp)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, err := userVersion(db)
	if err != nil || v != 5 {
		t.Fatalf("copy at version %d %v, want 5", v, err)
	}
	var slug string
	if err := db.QueryRowContext(context.Background(), `SELECT slug FROM services WHERE id=7`).Scan(&slug); err != nil || slug != "openai" {
		t.Fatalf("copy lacks the data: %q %v", slug, err)
	}
}
