package store

// Schema versioning. The database records its schema version in PRAGMA
// user_version: 0 for a database made before versioning, baselineVersion
// once the idempotent baseline has run, one more for each numbered step in
// migrations (store.go). A binary refuses a database newer than it knows,
// and, when Options.MigrationBackup is on, copies an existing database next
// to itself before a numbered step changes it.

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// migration is one numbered schema change.
type migration struct {
	// name says what the step does, for the log and errors.
	name string
	run  func(db *sql.DB) error
}

// SchemaTooNewError is returned by Open for a database whose schema version
// is newer than this binary knows: a newer release upgraded it, and running
// an older one against it could misread or damage it.
type SchemaTooNewError struct {
	Path  string
	Found int
	Known int
}

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("the database %s has schema version %d, but this binary only knows up to version %d: "+
		"a newer Acnologia release upgraded it. Upgrade this binary to that release or later. "+
		"To go back to this release instead, stop the server and restore the copy the upgrade took "+
		"(%s.pre-v%d-to-v*.bak next to the database, if it took one) as %s, removing its -wal and -shm files",
		e.Path, e.Found, e.Known, e.Path, e.Known, filepath.Base(e.Path))
}

// keepUpgradeBackups is how many pre-upgrade copies stay next to the
// database. Older ones are deleted after a new one is written.
const keepUpgradeBackups = 3

func userVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// setUserVersion records the version. PRAGMA takes no bound parameters,
// and v is always an int the code computed.
func setUserVersion(db *sql.DB, v int) error {
	_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, v))
	return err
}

// isEmpty reports a brand new database: no tables, indexes or triggers yet.
func isEmpty(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n)
	return n == 0, err
}

// upgradeBackupPrefix is the start of every pre-upgrade copy's file name.
func upgradeBackupPrefix(dbPath string) string {
	return filepath.Base(dbPath) + ".pre-v"
}

// backupBeforeUpgrade copies the database to <db>.pre-v<from>-to-v<to>.bak
// with VACUUM INTO, owner only, then keeps the newest keepUpgradeBackups
// copies. The copy is written under a temporary name and renamed, so a
// half written file never looks like a backup. A copy with the same name
// (from an upgrade that failed and rolled back) is replaced.
func backupBeforeUpgrade(db *sql.DB, dbPath string, from, to int) error {
	dest := fmt.Sprintf("%s.pre-v%d-to-v%d.bak", dbPath, from, to)
	tmp := dest + ".tmp"
	_ = os.Remove(tmp)
	if _, err := db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	slog.Info("database copied before the schema upgrade", "path", dest, "from", from, "to", to)
	pruneUpgradeBackups(dbPath)
	return nil
}

// pruneUpgradeBackups deletes all but the newest keepUpgradeBackups
// pre-upgrade copies of dbPath. Newest is the latest modification time,
// then the higher target version.
func pruneUpgradeBackups(dbPath string) {
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("could not list old database copies", "dir", dir, "err", err)
		return
	}
	type copyFile struct {
		name     string
		mod      int64
		from, to int
	}
	prefix := upgradeBackupPrefix(dbPath)
	var copies []copyFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".bak") {
			continue
		}
		var c copyFile
		if _, err := fmt.Sscanf(strings.TrimPrefix(name, prefix), "%d-to-v%d.bak", &c.from, &c.to); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c.name, c.mod = name, info.ModTime().UnixNano()
		copies = append(copies, c)
	}
	sort.Slice(copies, func(i, j int) bool {
		if copies[i].mod != copies[j].mod {
			return copies[i].mod > copies[j].mod
		}
		if copies[i].to != copies[j].to {
			return copies[i].to > copies[j].to
		}
		return copies[i].from > copies[j].from
	})
	for _, c := range copies[min(len(copies), keepUpgradeBackups):] {
		p := filepath.Join(dir, c.name)
		if err := os.Remove(p); err != nil {
			slog.Warn("could not delete an old database copy", "path", p, "err", err)
			continue
		}
		slog.Info("deleted an old database copy", "path", p)
	}
}
