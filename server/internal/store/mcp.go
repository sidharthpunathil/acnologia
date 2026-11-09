package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// MCP settings and exclusive leases live in their own tables, created on
// first use so this feature does not touch the shared schema file.
var mcpSchemaReady sync.Map // *sql.DB -> struct{}, once its tables exist

func (s *Store) mcpSchema(ctx context.Context) error {
	if _, ok := mcpSchemaReady.Load(s.DB); ok {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS app_mcp(
  app_id INTEGER PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
  settings TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS lease_exclusive(
  lease_id TEXT PRIMARY KEY REFERENCES leases(id) ON DELETE CASCADE
);`)
	if err == nil {
		mcpSchemaReady.Store(s.DB, struct{}{})
	}
	return err
}

// MCPSettings controls what one app's MCP server exposes.
type MCPSettings struct {
	// Tool sets: core (lease, report, renew, proxy), service tools generated
	// from provider examples, read only key insights, and key management.
	Core         bool `json:"core"`
	ServiceTools bool `json:"service_tools"`
	Insights     bool `json:"insights"`
	Manage       bool `json:"manage"`
	// Resources and prompts.
	Resources bool `json:"resources"`
	Prompts   bool `json:"prompts"`
	// ExclusiveDefault makes get_key exclusive unless the call says otherwise.
	ExclusiveDefault bool `json:"exclusive_default"`
	// MaxResultKB caps tool result bodies returned to the model.
	MaxResultKB int `json:"max_result_kb"`
	// Instructions are appended to the server instructions sent on initialize.
	Instructions string `json:"instructions"`
	// DisabledTools hides individual tools by name.
	DisabledTools []string `json:"disabled_tools"`
	// ToolMode is how tools/list shows the service tools: "all", "search"
	// (core tools plus search_tools and call_tool), or "auto" (all while
	// there are few, search past that).
	ToolMode  string     `json:"tool_mode"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func DefaultMCPSettings() MCPSettings {
	return MCPSettings{Core: true, ServiceTools: true, Resources: true, Prompts: true, MaxResultKB: 200, DisabledTools: []string{}, ToolMode: "auto"}
}

func (s *Store) GetMCPSettings(ctx context.Context, appID int64) (MCPSettings, error) {
	if err := s.mcpSchema(ctx); err != nil {
		return MCPSettings{}, err
	}
	var raw string
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT settings, updated_at FROM app_mcp WHERE app_id=?`, appID).Scan(&raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultMCPSettings(), nil
	}
	if err != nil {
		return MCPSettings{}, err
	}
	st := DefaultMCPSettings()
	_ = json.Unmarshal([]byte(raw), &st)
	if st.DisabledTools == nil {
		st.DisabledTools = []string{}
	}
	t := time.Unix(at, 0).UTC()
	st.UpdatedAt = &t
	return st, nil
}

func (s *Store) SetMCPSettings(ctx context.Context, appID int64, st MCPSettings) error {
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	st.UpdatedAt = nil
	b, _ := json.Marshal(st)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO app_mcp(app_id,settings,updated_at) VALUES(?,?,?)
		ON CONFLICT(app_id) DO UPDATE SET settings=excluded.settings, updated_at=excluded.updated_at`, appID, string(b), now())
	return err
}

func (s *Store) MarkLeaseExclusive(ctx context.Context, leaseID string) error {
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO lease_exclusive(lease_id) VALUES(?)`, leaseID)
	return err
}

// IsLeaseExclusive reports whether a lease was issued with exclusive use of its key.
func (s *Store) IsLeaseExclusive(ctx context.Context, leaseID string) (bool, error) {
	if err := s.mcpSchema(ctx); err != nil {
		return false, err
	}
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lease_exclusive WHERE lease_id=?`, leaseID).Scan(&n)
	return n > 0, err
}

func (s *Store) ExclusiveLeaseIDs(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	if err := s.mcpSchema(ctx); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT lease_id FROM lease_exclusive`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RenewLease moves an open lease's expiry. It fails for reported or expired leases.
func (s *Store) RenewLease(ctx context.Context, id string, appID int64, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE leases SET expires_at=? WHERE id=? AND app_id=? AND outcome='' AND expires_at>?`,
		expires.Unix(), id, appID, now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
