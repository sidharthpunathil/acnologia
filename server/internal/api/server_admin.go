package api

// Server administration for agents and the Settings page:
//
// GET /api/admin/version is the running version, the newest release on
// GitHub (asked through the public only SafeClient and cached; null when
// GitHub does not answer) and the command that upgrades this kind of
// install.
//
// POST /api/admin/backup (superadmin) streams a consistent copy of the
// database: VACUUM INTO a file in the data directory, streamed, then
// deleted. The copy holds the stored keys encrypted with the master key and
// never the master key itself: restoring it needs the same
// ACNOLOGIA_MASTER_KEY. Dashboard sessions are left out of it. The control
// MCP's backup tool writes the same copy under <data dir>/backups and
// answers with its path and size, since an MCP answer cannot carry a file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
)

const (
	releaseTTL     = 6 * time.Hour
	releaseMissTTL = 30 * time.Minute
	// backupDirName is where the control MCP's backups go, in the data dir.
	backupDirName = "backups"
)

// releaseInfo is the newest published release.
type releaseInfo struct {
	Tag string
	URL string
}

// latestRelease caches the newest release. fetch asks GitHub; tests
// replace it.
var latestRelease = struct {
	mu    sync.Mutex
	repo  string
	info  *releaseInfo
	at    time.Time
	fetch func(ctx context.Context, repo string) (*releaseInfo, error)
}{fetch: fetchLatestRelease}

func fetchLatestRelease(ctx context.Context, repo string) (*releaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "acnologia")
	resp, err := catalog.SafeClient(8 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var out struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if out.Tag == "" {
		return nil, nil
	}
	return &releaseInfo{Tag: out.Tag, URL: out.URL}, nil
}

func newestRelease(ctx context.Context, repo string) (*releaseInfo, time.Time) {
	c := &latestRelease
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := releaseTTL
	if c.info == nil {
		ttl = releaseMissTTL
	}
	if c.repo == repo && !c.at.IsZero() && time.Since(c.at) < ttl {
		return c.info, c.at
	}
	info, err := c.fetch(ctx, repo)
	if err != nil && c.repo == repo && c.info != nil {
		info = c.info // keep the last answer through a blip
	}
	c.repo, c.info, c.at = repo, info, time.Now().UTC()
	return info, c.at
}

// versionParts reads v1.2.3 (with or without the v, any suffix after a
// dash ignored) as numbers; ok is false for anything else, such as dev.
func versionParts(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newerVersion reports whether latest is newer than current; nil when
// either is not a version number.
func newerVersion(current, latest string) *bool {
	a, ok1 := versionParts(current)
	b, ok2 := versionParts(latest)
	if !ok1 || !ok2 {
		return nil
	}
	newer := false
	for i := range a {
		if a[i] != b[i] {
			newer = b[i] > a[i]
			break
		}
	}
	return &newer
}

// inContainer reports whether the server runs in a container.
func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil || os.Getenv("container") != ""
}

// upgradeCommand is how this kind of install upgrades.
func upgradeCommand() string {
	if inContainer() {
		return "docker compose pull && docker compose up -d"
	}
	return "curl -fsSL https://raw.githubusercontent.com/" + githubRepo() + "/main/deploy/install-server.sh | sudo bash"
}

func (s *Server) adminVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.versionInfo(r.Context()))
}

func (s *Server) versionInfo(ctx context.Context) map[string]any {
	repo := githubRepo()
	info, at := newestRelease(ctx, repo)
	out := map[string]any{
		"current": s.Cfg.Version, "latest": nil, "release_url": nil, "update_available": nil,
		"upgrade_command": upgradeCommand(), "upgrade_doc": "/docs/configuration.md#upgrading",
		"checked_at": at, "repo": repo,
	}
	if info != nil {
		out["latest"], out["release_url"] = info.Tag, info.URL
		if n := newerVersion(s.Cfg.Version, info.Tag); n != nil {
			out["update_available"] = *n
		}
	}
	return out
}

// backupName is the copy's file name, with the time it was made.
func backupName(t time.Time) string {
	return "acnologia-backup-" + t.UTC().Format("2006-01-02T150405Z") + ".db"
}

// writeBackup makes a copy in dir and returns its path and size.
func (s *Server) writeBackup(ctx context.Context, dir string) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	name := backupName(time.Now())
	dest := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
		dest = filepath.Join(dir, strings.TrimSuffix(name, ".db")+"-"+strconv.Itoa(i)+".db")
	}
	if err := s.Store.Backup(ctx, dest); err != nil {
		_ = os.Remove(dest)
		return "", 0, err
	}
	_ = os.Chmod(dest, 0o600)
	fi, err := os.Stat(dest)
	if err != nil {
		return "", 0, err
	}
	return dest, fi.Size(), nil
}

func (s *Server) adminBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// A scratch copy beside the database, so it is on the same disk and as
	// private; deleted once streamed.
	dir, err := os.MkdirTemp(s.Cfg.DataDir, ".backup-")
	if err != nil {
		writeErr(w, 500, "backup_failed", "could not make a scratch folder in the data directory: "+err.Error())
		return
	}
	defer os.RemoveAll(dir)
	path, size, err := s.writeBackup(ctx, dir)
	if err != nil {
		writeErr(w, 500, "backup_failed", "could not copy the database: "+err.Error())
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, 500, "backup_failed", err.Error())
		return
	}
	defer f.Close()
	if err := s.Store.AddAuditErr(ctx, userFrom(r), "server.backup", filepath.Base(path), fmt.Sprintf("%d bytes, downloaded", size)); err != nil {
		writeErr(w, 500, "backup_failed", "could not record the backup in the audit log")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}
