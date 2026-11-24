package api

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type trashOut struct {
	OK        bool            `json:"ok"`
	RestoreID string          `json:"restore_id"`
	ExpiresAt time.Time       `json:"expires_at"`
	Trash     store.TrashItem `json:"trash"`
}

func (e *env) proxyStatus(tok string) int {
	req, _ := http.NewRequest("GET", e.srv.URL+"/proxy/serpapi/search.json?q=trash", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func (e *env) serviceSlugs(c *http.Client) []string {
	var out struct{ Services []store.Service }
	e.do(c, "GET", "/api/admin/services", nil, 200, &out)
	slugs := []string{}
	for _, s := range out.Services {
		slugs = append(slugs, s.Slug)
	}
	return slugs
}

func TestTrashKeyRestoreThroughProxy(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "trashkey1"})
	_, tok := e.app(map[string]any{"name": "caller"})
	if c := e.proxyStatus(tok); c != 200 {
		t.Fatalf("before: %d", c)
	}
	var del trashOut
	e.do(e.super, "DELETE", "/api/admin/keys/"+itoa(int(keys[0].ID)), nil, 200, &del)
	if !del.OK || !strings.HasPrefix(del.RestoreID, "trs_") || time.Until(del.ExpiresAt) < 6*24*time.Hour {
		t.Fatalf("delete answer: %+v", del)
	}
	// Gone from every view: the key list, the key itself, the proxy.
	var list struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &list)
	e.do(e.super, "GET", "/api/admin/keys", nil, 200, &list)
	if len(list.Keys) != 0 {
		t.Fatalf("trashed key listed: %+v", list.Keys)
	}
	e.do(e.super, "GET", "/api/admin/keys/"+itoa(int(keys[0].ID))+"/insights", nil, 404, nil)
	if c := e.proxyStatus(tok); c == 200 {
		t.Fatal("proxy still used the trashed key")
	}
	// The same secret can be added again while the first is in the trash.
	again := e.addKeys(map[string]any{"secret": "trashkey1"})
	if len(again) != 1 {
		t.Fatal("re-adding a trashed secret was skipped as a duplicate")
	}
	// Restoring now conflicts: the service has the secret again.
	e.do(e.super, "POST", "/api/admin/trash/"+del.RestoreID+"/restore", nil, 409, nil)
	e.do(e.super, "DELETE", "/api/admin/keys/"+itoa(int(again[0].ID)), nil, 200, nil)
	e.do(e.super, "POST", "/api/admin/trash/"+del.RestoreID+"/restore", nil, 200, nil)
	if c := e.proxyStatus(tok); c != 200 {
		t.Fatalf("restored key does not serve: %d", c)
	}
	e.do(e.super, "GET", "/api/admin/keys/"+itoa(int(keys[0].ID))+"/insights", nil, 200, nil)
	// Audited.
	var audit struct {
		Audit []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	seen := map[string]bool{}
	for _, a := range audit.Audit {
		seen[a.Action] = true
	}
	if !seen["key.delete"] || !seen["trash.restore"] {
		t.Fatalf("audit: %v", seen)
	}
}

func TestTrashServiceInvisibleEverywhere(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "svckey01"}, map[string]any{"secret": "svckey02"})
	_, tok := e.app(map[string]any{"name": "caller"})
	var del trashOut
	e.do(e.super, "DELETE", "/api/admin/services/1", nil, 200, &del)
	if del.Trash.Keys != 2 || del.Trash.Kind != store.TrashService {
		t.Fatalf("trash: %+v", del.Trash)
	}
	if s := e.serviceSlugs(e.super); len(s) != 0 {
		t.Fatalf("listed: %v", s)
	}
	e.do(e.super, "GET", "/api/admin/services/1", nil, 404, nil)
	if c := e.proxyStatus(tok); c != 404 {
		t.Fatalf("proxy to a trashed service: %d", c)
	}
	// The app side: no service, no MCP tool, no lease.
	var svcs struct{ Services []map[string]any }
	e.do(http.DefaultClient, "GET", "/api/v1/services", nil, 200, &svcs, "Authorization", "Bearer "+tok)
	if len(svcs.Services) != 0 {
		t.Fatalf("v1 services: %v", svcs.Services)
	}
	var res map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200, &res, "Authorization", "Bearer "+tok)
	for _, tl := range res["result"].(map[string]any)["tools"].([]any) {
		if strings.Contains(tl.(map[string]any)["name"].(string), "serpapi") {
			t.Fatalf("MCP still lists %v", tl.(map[string]any)["name"])
		}
	}
	// The slug is free for a new service, and a restore then conflicts.
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serpapi", "base_url": e.upURL}, 201, nil)
	e.do(e.super, "POST", "/api/admin/trash/"+del.RestoreID+"/restore", nil, 409, nil)
	var svc store.Service
	e.do(e.super, "GET", "/api/admin/services", nil, 200, &struct{ Services []store.Service }{})
	sv, err := e.st.GetServiceBySlug(context.Background(), "serpapi")
	if err != nil {
		t.Fatal(err)
	}
	e.do(e.super, "DELETE", "/api/admin/services/"+itoa(int(sv.ID)), nil, 200, nil)
	e.do(e.super, "POST", "/api/admin/trash/"+del.RestoreID+"/restore", nil, 200, nil)
	e.do(e.super, "GET", "/api/admin/services/1", nil, 200, &svc)
	if svc.Slug != "serpapi" || svc.KeyCounts.Total != 2 {
		t.Fatalf("restored: %+v", svc)
	}
	if c := e.proxyStatus(tok); c != 200 {
		t.Fatalf("restored service does not serve: %d", c)
	}
}

func TestTrashAppTokensStopAndReturn(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "appkey01"})
	id, tok := e.app(map[string]any{"name": "gone"})
	var del trashOut
	e.do(e.super, "DELETE", "/api/admin/apps/"+itoa(int(id)), nil, 200, &del)
	if c := e.proxyStatus(tok); c != 401 {
		t.Fatalf("trashed app's token: %d", c)
	}
	var apps struct{ Apps []store.App }
	e.do(e.super, "GET", "/api/admin/apps", nil, 200, &apps)
	if len(apps.Apps) != 0 {
		t.Fatalf("listed: %+v", apps.Apps)
	}
	e.do(e.super, "POST", "/api/admin/trash/"+del.RestoreID+"/restore", nil, 200, nil)
	if c := e.proxyStatus(tok); c != 200 {
		t.Fatalf("restored app's token: %d", c)
	}
}

func TestTrashHiddenAndPurge(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "plainkey"}, map[string]any{"secret": "hiddenk1", "hidden": true})
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	var hid, plain trashOut
	e.do(e.super, "DELETE", "/api/admin/keys/"+itoa(int(keys[1].ID)), nil, 200, &hid)
	e.do(admin, "DELETE", "/api/admin/keys/"+itoa(int(keys[0].ID)), nil, 200, &plain)
	var list struct{ Items []store.TrashItem }
	e.do(admin, "GET", "/api/admin/trash", nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].ID != plain.RestoreID {
		t.Fatalf("admin sees %+v", list.Items)
	}
	e.do(admin, "POST", "/api/admin/trash/"+hid.RestoreID+"/restore", nil, 404, nil)
	e.do(e.super, "GET", "/api/admin/trash", nil, 200, &list)
	if len(list.Items) != 2 {
		t.Fatalf("superadmin sees %d", len(list.Items))
	}
	// Only a superadmin purges.
	e.do(admin, "DELETE", "/api/admin/trash/"+plain.RestoreID, nil, 403, nil)
	e.do(e.super, "DELETE", "/api/admin/trash/"+plain.RestoreID, nil, 200, nil)
	if _, err := e.st.GetTrash(context.Background(), plain.RestoreID); err == nil {
		t.Fatal("purged entry still there")
	}
	var n int
	_ = e.st.DB.QueryRow(`SELECT COUNT(*) FROM keys WHERE id=?`, keys[0].ID).Scan(&n)
	if n != 0 {
		t.Fatal("purge left the key row")
	}
	// The janitor purges what expired.
	if _, err := e.st.DB.Exec(`UPDATE trash SET expires_at=1`); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Prune(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	_ = e.st.DB.QueryRow(`SELECT COUNT(*) FROM keys WHERE id=?`, keys[1].ID).Scan(&n)
	if n != 0 {
		t.Fatal("janitor left the expired key")
	}
	e.do(e.super, "GET", "/api/admin/trash", nil, 200, &list)
	if len(list.Items) != 0 {
		t.Fatalf("trash after janitor: %+v", list.Items)
	}
}

func TestAdminIdempotencyKey(t *testing.T) {
	e := newEnv(t)
	create := func(body map[string]any, want int, key string) (map[string]any, *http.Response) {
		var out map[string]any
		resp := e.do(e.super, "POST", "/api/admin/apps", body, want, &out, "Idempotency-Key", key)
		return out, resp
	}
	a, _ := create(map[string]any{"name": "once"}, 201, "k-1")
	b, resp := create(map[string]any{"name": "once"}, 201, "k-1")
	if resp.Header.Get("Idempotent-Replayed") != "true" || a["token"] != b["token"] {
		t.Fatalf("not replayed: %v %v", a["token"], b["token"])
	}
	var apps struct{ Apps []store.App }
	e.do(e.super, "GET", "/api/admin/apps", nil, 200, &apps)
	if len(apps.Apps) != 1 {
		t.Fatalf("%d apps", len(apps.Apps))
	}
	// Same key, other body: refused.
	create(map[string]any{"name": "twice"}, 422, "k-1")
	// A 4xx is kept too; a different key runs again.
	create(map[string]any{"name": ""}, 400, "k-2")
	create(map[string]any{"name": ""}, 400, "k-2")
	create(map[string]any{"name": "other"}, 201, "k-3")
	// The kept answer is encrypted at rest: no token in plain text.
	var raw []byte
	_ = e.st.DB.QueryRow(`SELECT body_enc FROM admin_idempotency WHERE key='k-1'`).Scan(&raw)
	if len(raw) == 0 || strings.Contains(string(raw), a["token"].(string)) {
		t.Fatal("answer stored in plain text")
	}
	// Scoped to the caller: an API token of the same user is another scope.
	tok := adminToken(e, "admin")
	var other map[string]any
	e.do(http.DefaultClient, "POST", "/api/admin/apps", map[string]any{"name": "once"}, 201, &other, "Authorization", "Bearer "+tok, "Idempotency-Key", "k-1")
	if other["token"] == a["token"] {
		t.Fatal("a different token got the session's answer")
	}
	// The control MCP passes idempotency_key.
	c := &ctlClient{e: e, tok: tok, path: "/mcp/control?groups=all"}
	sc1, _, _ := c.tool("create_app", map[string]any{"name": "mcp", "idempotency_key": "m-1"})
	sc2, _, _ := c.tool("create_app", map[string]any{"name": "mcp", "idempotency_key": "m-1"})
	if sc1["token"] == nil || sc1["token"] != sc2["token"] {
		t.Fatalf("MCP idempotency: %v %v", sc1["token"], sc2["token"])
	}
}

func TestAdminIdempotencyInFlight(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	scope := "u1|session"
	if _, ok, err := e.st.ClaimAdminIdem(ctx, scope, "busy", "fp", time.Minute); err != nil || !ok {
		t.Fatal("claim", err)
	}
	if ent, ok, _ := e.st.ClaimAdminIdem(ctx, scope, "busy", "fp", time.Minute); ok || ent.State != store.AdminIdemRunning {
		t.Fatal("second claim should see the running one")
	}
	// Through the route: a running claim with this request's fingerprint answers 409.
	r, _ := http.NewRequest("POST", "/api/admin/apps", strings.NewReader(`{"name":"x"}`))
	fp := adminIdemFingerprint(r, []byte(`{"name":"x"}`))
	_, _, _ = e.st.ClaimAdminIdem(ctx, scope, "busy2", fp, time.Minute)
	resp := e.do(e.super, "POST", "/api/admin/apps", map[string]any{"name": "x"}, 409, nil, "Idempotency-Key", "busy2")
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "drykey01"})
	appID, _ := e.app(map[string]any{"name": "dry"})
	var share struct {
		Share struct {
			ID string `json:"id"`
		} `json:"share"`
	}
	e.do(e.super, "POST", "/api/admin/shares", map[string]any{"name": "s1", "services": []string{"serpapi"}}, 201, &share)
	var tok struct {
		Info struct {
			ID int64 `json:"id"`
		} `json:"info"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "t", "scope": "read"}, 201, &tok)
	var atoks struct {
		Tokens []store.AppToken `json:"tokens"`
	}
	e.do(e.super, "GET", "/api/admin/apps/"+itoa(int(appID))+"/tokens", nil, 200, &atoks)

	snapshot := func() string {
		var b strings.Builder
		for _, q := range []string{`SELECT COUNT(*) FROM services WHERE deleted_at IS NULL`, `SELECT COUNT(*) FROM keys WHERE deleted_at IS NULL`,
			`SELECT COUNT(*) FROM apps WHERE deleted_at IS NULL`, `SELECT COUNT(*) FROM app_tokens WHERE revoked_at IS NULL`,
			`SELECT COUNT(*) FROM admin_tokens`, `SELECT COUNT(*) FROM shares WHERE status<>'revoked'`, `SELECT COUNT(*) FROM trash`, `SELECT COUNT(*) FROM webhooks`} {
			var n int
			if err := e.st.DB.QueryRow(q).Scan(&n); err != nil {
				t.Fatal(q, err)
			}
			b.WriteString(itoa(n) + ",")
		}
		return b.String()
	}
	before := snapshot()
	cases := []struct {
		method, path string
		body         any
		would        string
	}{
		{"POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serp2", "base_url": e.upURL}, "create service serp2"},
		{"DELETE", "/api/admin/services/1", nil, "move service serpapi"},
		{"POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "drykey01"}, {"secret": "newdry02"}}}, "add 1 keys to service serpapi and skip 1"},
		{"DELETE", "/api/admin/keys/" + itoa(int(keys[0].ID)), nil, "move key"},
		{"POST", "/api/admin/apps", map[string]any{"name": "nope"}, "create app nope"},
		{"DELETE", "/api/admin/apps/" + itoa(int(appID)), nil, "move app dry"},
		{"POST", "/api/admin/tokens", map[string]any{"name": "t2", "scope": "write"}, "create admin API token t2"},
		{"DELETE", "/api/admin/tokens/" + itoa(int(tok.Info.ID)), nil, "revoke admin API token t"},
		{"POST", "/api/admin/apps/" + itoa(int(appID)) + "/tokens", map[string]any{"name": "ci"}, "add token ci"},
		{"DELETE", "/api/admin/apps/" + itoa(int(appID)) + "/tokens/" + itoa(int(atoks.Tokens[0].ID)), nil, "revoke token"},
		{"POST", "/api/admin/shares", map[string]any{"name": "s2", "services": []string{"serpapi"}}, "create share s2"},
		{"POST", "/api/admin/shares/" + share.Share.ID + "/revoke", nil, "revoke share s1"},
		{"POST", "/api/admin/webhooks", map[string]any{"name": "w", "url": "https://example.com/h"}, "send POST /api/admin/webhooks"},
	}
	for _, c := range cases {
		var out map[string]any
		e.do(e.super, c.method, c.path+"?dry_run=1", c.body, 200, &out)
		if out["dry_run"] != true || !strings.Contains(out["would"].(string), c.would) {
			t.Errorf("%s %s: %v", c.method, c.path, out)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("a dry run wrote: %s -> %s", before, after)
	}
	// Validation still runs, and so do the route's own checks.
	e.do(e.super, "POST", "/api/admin/services?dry_run=1", map[string]any{"provider": "nope"}, 400, nil)
	e.do(e.super, "DELETE", "/api/admin/keys/99999?dry_run=1", nil, 404, nil)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	member := client()
	e.do(member, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	e.do(member, "DELETE", "/api/admin/services/1?dry_run=1", nil, 403, nil)
	tokAdmin := adminToken(e, "write")
	e.do(http.DefaultClient, "POST", "/api/admin/tokens?dry_run=1", map[string]any{"name": "x", "scope": "read"}, 403, nil, "Authorization", "Bearer "+tokAdmin)
	// The control MCP's dry_run.
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}
	before = snapshot()
	sc, _, isErr := c.tool("create_app", map[string]any{"name": "viamcp", "dry_run": true})
	if isErr || sc["dry_run"] != true {
		t.Fatalf("mcp dry run: %v", sc)
	}
	if after := snapshot(); after != before {
		t.Fatal("the MCP dry run wrote")
	}
}

func TestVersionAndBackup(t *testing.T) {
	e := newEnv(t)
	latestRelease.mu.Lock()
	latestRelease.fetch = func(context.Context, string) (*releaseInfo, error) {
		return &releaseInfo{Tag: "v9.9.9", URL: "https://example.com/r"}, nil
	}
	latestRelease.at = time.Time{}
	latestRelease.mu.Unlock()
	t.Cleanup(func() {
		latestRelease.mu.Lock()
		latestRelease.fetch, latestRelease.at = fetchLatestRelease, time.Time{}
		latestRelease.mu.Unlock()
	})
	var v map[string]any
	e.do(e.super, "GET", "/api/admin/version", nil, 200, &v)
	if v["current"] != "test" || v["latest"] != "v9.9.9" || v["upgrade_command"] == "" {
		t.Fatalf("version: %v", v)
	}
	if b := newerVersion("v1.2.3", "v1.10.0"); b == nil || !*b {
		t.Fatal("1.10.0 is newer than 1.2.3")
	}

	e.addKeys(map[string]any{"secret": "backupk1"})
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/admin/backup", nil)
	req.Header.Set("X-Requested-With", "test")
	resp, err := e.super.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "acnologia-backup-") {
		t.Fatalf("backup: %d %v", resp.StatusCode, resp.Header)
	}
	dest := filepath.Join(t.TempDir(), "b.db")
	f, _ := os.Create(dest)
	_, _ = io.Copy(f, resp.Body)
	f.Close()
	db, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var keys, sessions int
	_ = db.QueryRow(`SELECT COUNT(*) FROM keys`).Scan(&keys)
	_ = db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions)
	if keys != 1 || sessions != 0 {
		t.Fatalf("backup holds %d keys, %d sessions", keys, sessions)
	}
	var secret []byte
	_ = db.QueryRow(`SELECT secret_enc FROM keys`).Scan(&secret)
	if strings.Contains(string(secret), "backupk1") {
		t.Fatal("secret in plain text")
	}
	// No scratch copy is left behind.
	left, _ := filepath.Glob(filepath.Join(e.api.Cfg.DataDir, ".backup-*"))
	if len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// Admins may not.
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	e.do(admin, "POST", "/api/admin/backup", nil, 403, nil)

	// The control MCP writes it on the server.
	e.api.Cfg.DataDir = t.TempDir()
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}
	sc, text, isErr := c.tool("backup_database", nil)
	if isErr {
		t.Fatal(text)
	}
	if fi, err := os.Stat(sc["path"].(string)); err != nil || fi.Size() == 0 {
		t.Fatalf("backup file: %v %v", sc, err)
	}
	if sc, _, isErr := c.tool("server_version", nil); isErr || sc["current"] != "test" {
		t.Fatalf("server_version: %v", sc)
	}
}

func TestTrashControlTools(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "ctltrash"})
	c := &ctlClient{e: e, tok: adminToken(e, "admin"), path: "/mcp/control?groups=all"}
	sc, text, isErr := c.tool("delete_key", map[string]any{"key_id": keys[0].ID, "confirm": true})
	if isErr || sc["restore_id"] == nil {
		t.Fatalf("delete_key: %s", text)
	}
	id := sc["restore_id"].(string)
	sc, _, _ = c.tool("list_trash", nil)
	if items, _ := sc["items"].([]any); len(items) != 1 {
		t.Fatalf("list_trash: %v", sc)
	}
	if _, text, isErr := c.tool("restore_trash", map[string]any{"trash_id": id}); isErr {
		t.Fatal(text)
	}
	sc, _, _ = c.tool("delete_key", map[string]any{"key_id": keys[0].ID, "confirm": true})
	id = sc["restore_id"].(string)
	if sc, _, _ := c.tool("purge_trash", map[string]any{"trash_id": id}); sc["confirm_required"] != true {
		t.Fatalf("purge needs confirm: %v", sc)
	}
	if _, text, isErr := c.tool("purge_trash", map[string]any{"trash_id": id, "confirm": true}); isErr {
		t.Fatal(text)
	}
}
