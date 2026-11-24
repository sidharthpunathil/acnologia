package api

// Control MCP tools for driving the admin API safely: the trash, the server
// version and backups, and two inputs every write tool takes. dry_run says
// what the call would do and changes nothing (dryrun.go); idempotency_key
// makes a retry return the first answer instead of writing twice
// (admin_idem.go). Both travel to the route in the request context, so the
// route answers exactly as it would to ?dry_run=1 or an Idempotency-Key
// header.

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	dryRunArgDesc  = "Set true to see what the call would do without doing it: the answer says what would change, and nothing does"
	adminIdemDesc  = "Optional: a retry with the same key within 24 hours gets the first answer back instead of running again. Use one per logical change on creates"
	trashIDArgDesc = "Trash id (trs_...) from acnologia_list_trash, or the restore_id a delete answered with"
)

// withSafetyTools adds the safety tools to the table and gives every write
// tool that is one admin route the dry_run and idempotency_key inputs.
func withSafetyTools(tools []ctlTool) []ctlTool {
	for i := range tools {
		t := &tools[i]
		if t.Kind == ctlRead || t.Run != nil || !isWriteMethod(t.Method) || !strings.HasPrefix(t.Path, "/api/admin/") {
			continue
		}
		orig := *t
		t.Props = props(t.Props, map[string]any{"dry_run": bProp(dryRunArgDesc), "idempotency_key": sProp(adminIdemDesc)})
		t.Run = func(c *ctlCtx, in map[string]any) map[string]any {
			key := strings.TrimSpace(argStr(in, "idempotency_key"))
			dry, _ := in["dry_run"].(bool)
			o := orig
			if key == "" && !dry {
				return c.callRaw(&o, in)
			}
			ctx := c.r.Context()
			if key != "" {
				ctx = withAdminIdemKey(ctx, key)
			}
			if dry {
				ctx = withDryRun(ctx)
			}
			c2 := *c
			c2.r = c.r.WithContext(ctx)
			return c2.callRaw(&o, in)
		}
	}
	return append(tools, ctlSafetyTools()...)
}

func ctlSafetyTools() []ctlTool {
	m, a, sa := store.RoleMember, store.RoleAdmin, store.RoleSuperadmin
	return []ctlTool{
		{Name: "list_trash", Title: "List the trash", Group: "services", Kind: ctlRead, Role: a, Method: http.MethodGet, Path: "/api/admin/trash",
			Desc: "Deleted keys, services and apps, kept 7 days before they go for good: id, kind, name, who deleted it and when it expires. Never a secret. Items holding hidden keys are listed for superadmins only."},
		{Name: "restore_trash", Title: "Restore from the trash", Group: "services", Kind: ctlWrite, Role: a, Method: http.MethodPost, Path: "/api/admin/trash/{trash_id}/restore",
			Desc:  "Bring a deleted key, service or app back as it was: a key rejoins its pool, a service comes back with its keys, an app's tokens work again.",
			Props: map[string]any{"trash_id": sProp(trashIDArgDesc)}, Required: []string{"trash_id"}},
		{Name: "purge_trash", Title: "Delete from the trash for good", Group: "services", Kind: ctlDestructive, Role: sa, Method: http.MethodDelete, Path: "/api/admin/trash/{trash_id}",
			Desc:  "Delete a trashed item for good now, before its 7 days are up. It can no longer be restored.",
			Props: map[string]any{"trash_id": sProp(trashIDArgDesc)}, Required: []string{"trash_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				it, err := ctlFind(c, "/api/admin/trash", "items", "id", argStr(in, "trash_id"))
				if err != nil {
					return "", err
				}
				return "delete " + argStr(it, "kind") + " " + argStr(it, "name") + " for good; it can no longer be restored", nil
			}},
		{Name: "server_version", Title: "Server version", Group: "overview", Kind: ctlRead, Role: m, Method: http.MethodGet, Path: "/api/admin/version",
			Desc: "The running version, the newest release, whether an update is available and the command that upgrades this kind of install."},
		{Name: "backup_database", Title: "Back up the database", Group: "admin", Kind: ctlWrite, Role: sa, Run: ctlBackup,
			Desc: "Write a consistent copy of the database to the server's data directory (backups/acnologia-backup-<time>.db) and return its path and size. The file stays on the server: copy it off yourself, or download one with POST /api/admin/backup. Keys in it stay encrypted and restoring needs the same ACNOLOGIA_MASTER_KEY, which is never in the copy."},
	}
}

// ctlBackup is backup_database: an MCP answer cannot carry a database, so
// the copy is written on the server.
func ctlBackup(c *ctlCtx, _ map[string]any) map[string]any {
	if c.user.Role != store.RoleSuperadmin {
		return ctlFailCode("forbidden", "backup_database needs the superadmin role")
	}
	path, size, err := c.s.writeBackup(c.r.Context(), filepath.Join(c.s.Cfg.DataDir, backupDirName))
	if err != nil {
		return ctlFailCode("backup_failed", "could not copy the database: "+err.Error())
	}
	if err := c.s.Store.AddAuditErr(c.r.Context(), c.user, "server.backup", filepath.Base(path), "written on the server"); err != nil {
		return ctlFailCode("backup_failed", "could not record the backup in the audit log")
	}
	return ctlJSON(map[string]any{
		"path": path, "file": filepath.Base(path), "bytes": size,
		"note": "Copy the file off the server and keep ACNOLOGIA_MASTER_KEY separately: the keys in it are encrypted with it. Delete old copies in " + filepath.Dir(path) + " when you have them elsewhere.",
	})
}
