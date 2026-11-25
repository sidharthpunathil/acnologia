package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ServiceMCPTool is one tool discovered from a service's upstream MCP server.
type ServiceMCPTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	// Access is the admin's word on whether the tool only reads: "" follows
	// the upstream's readOnlyHint, "read" and "write" override it. The
	// upstream's annotations are its own claim and can change on any sync.
	Access string `json:"access,omitempty"`
}

// Upstream tool access overrides.
const (
	MCPToolAccessUpstream = ""
	MCPToolAccessRead     = "read"
	MCPToolAccessWrite    = "write"
)

// UpstreamReadOnly is what the upstream server says: its readOnlyHint.
func (t ServiceMCPTool) UpstreamReadOnly() bool {
	b, _ := t.Annotations["readOnlyHint"].(bool)
	return b
}

// ReadOnly is whether the tool counts as read-only here: the admin's
// override when set, else the upstream's hint.
func (t ServiceMCPTool) ReadOnly() bool {
	switch t.Access {
	case MCPToolAccessRead:
		return true
	case MCPToolAccessWrite:
		return false
	}
	return t.UpstreamReadOnly()
}

// ServiceMCP holds the last synced state and discovered tools of a service's
// upstream MCP server.
type ServiceMCP struct {
	ServiceID       int64            `json:"service_id"`
	ServerName      string           `json:"server_name"`
	ServerVersion   string           `json:"server_version"`
	ProtocolVersion string           `json:"protocol_version"`
	Instructions    string           `json:"instructions,omitempty"`
	Tools           []ServiceMCPTool `json:"tools"`
	ToolCount       int              `json:"tool_count"`
	SyncedAt        *time.Time       `json:"synced_at"`
	LastError       string           `json:"last_error,omitempty"`
}

func scanServiceMCP(row interface{ Scan(...any) error }) (*ServiceMCP, error) {
	var m ServiceMCP
	var toolsRaw string
	var synced sql.NullInt64
	if err := row.Scan(&m.ServiceID, &m.ServerName, &m.ServerVersion, &m.ProtocolVersion,
		&m.Instructions, &toolsRaw, &m.ToolCount, &synced, &m.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Tools = []ServiceMCPTool{}
	if toolsRaw != "" {
		_ = json.Unmarshal([]byte(toolsRaw), &m.Tools)
	}
	if m.Tools == nil {
		m.Tools = []ServiceMCPTool{}
	}
	m.SyncedAt = toTime(synced)
	return &m, nil
}

// GetServiceMCP returns the stored upstream MCP sync state for one service,
// or ErrNotFound if it has never been synced.
func (s *Store) GetServiceMCP(ctx context.Context, serviceID int64) (*ServiceMCP, error) {
	if err := s.mcpSchema(ctx); err != nil {
		return nil, err
	}
	return scanServiceMCP(s.r.QueryRowContext(ctx,
		`SELECT service_id, server_name, server_version, protocol_version, instructions, tools_json, tool_count, synced_at, last_error
		FROM service_mcp WHERE service_id=?`, serviceID))
}

// ListServiceMCP returns all stored upstream MCP sync states keyed by service ID.
func (s *Store) ListServiceMCP(ctx context.Context) (map[int64]*ServiceMCP, error) {
	if err := s.mcpSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx,
		`SELECT service_id, server_name, server_version, protocol_version, instructions, tools_json, tool_count, synced_at, last_error
		FROM service_mcp`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*ServiceMCP{}
	for rows.Next() {
		m, err := scanServiceMCP(rows)
		if err != nil {
			return nil, err
		}
		out[m.ServiceID] = m
	}
	return out, rows.Err()
}

// storedToolAccess reads, inside tx, the access override of every tool the
// service has stored, by tool name. A tool with no override maps to "".
func storedToolAccess(ctx context.Context, tx *sql.Tx, serviceID int64) (map[string]string, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT tools_json FROM service_mcp WHERE service_id=?`, serviceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tools []ServiceMCPTool
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &tools); err != nil {
			return nil, err
		}
	}
	out := make(map[string]string, len(tools))
	for _, t := range tools {
		out[t.Name] = t.Access
	}
	return out, nil
}

// SaveServiceMCP upserts the upstream MCP sync state for a service. An
// admin's access override is never lost to it: for every tool the service
// already has stored, the stored override wins over m's, read and written
// in one transaction. So a sync that read the tools before an override
// changed, and saves after, keeps the new override. A tool new to the
// service keeps m's Access.
func (s *Store) SaveServiceMCP(ctx context.Context, m *ServiceMCP) error {
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	tools := append([]ServiceMCPTool{}, m.Tools...)
	var synced any
	if m.SyncedAt != nil {
		synced = m.SyncedAt.Unix()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := storedToolAccess(ctx, tx, m.ServiceID)
	if err != nil {
		return err
	}
	for i := range tools {
		if a, ok := stored[tools[i].Name]; ok {
			tools[i].Access = a
		}
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_mcp(
		service_id, server_name, server_version, protocol_version, instructions, tools_json, tool_count, synced_at, last_error
	) VALUES(?,?,?,?,?,?,?,?,?)
	ON CONFLICT(service_id) DO UPDATE SET
		server_name=excluded.server_name,
		server_version=excluded.server_version,
		protocol_version=excluded.protocol_version,
		instructions=excluded.instructions,
		tools_json=excluded.tools_json,
		tool_count=excluded.tool_count,
		synced_at=excluded.synced_at,
		last_error=excluded.last_error`,
		m.ServiceID, m.ServerName, m.ServerVersion, m.ProtocolVersion, m.Instructions, string(raw), len(tools), synced, m.LastError); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteServiceMCP removes stored upstream MCP state for a service.
func (s *Store) DeleteServiceMCP(ctx context.Context, serviceID int64) error {
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM service_mcp WHERE service_id=?`, serviceID)
	return err
}

// SetServiceMCPToolAccess sets the access override of one synced upstream
// tool. It returns ErrNotFound when the service has no tool by that name.
// The tools are read and written in one transaction, so a sync that saves
// at the same moment either comes first (and the override lands on its
// tools) or after (and keeps the override, see SaveServiceMCP).
func (s *Store) SetServiceMCPToolAccess(ctx context.Context, serviceID int64, name, access string) error {
	switch access {
	case MCPToolAccessUpstream, MCPToolAccessRead, MCPToolAccessWrite:
	default:
		return errors.New("access must be empty, read or write")
	}
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT tools_json FROM service_mcp WHERE service_id=?`, serviceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var tools []ServiceMCPTool
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &tools); err != nil {
			return err
		}
	}
	found := false
	for i := range tools {
		if tools[i].Name == name {
			tools[i].Access = access
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	out, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	// Only the tools change, so the server details stay as the last sync
	// left them.
	if _, err := tx.ExecContext(ctx, `UPDATE service_mcp SET tools_json=? WHERE service_id=?`, string(out), serviceID); err != nil {
		return err
	}
	return tx.Commit()
}

// KeysRestingUntil gives, per service, when its first cooling key is free
// again: the earliest cooldown of its active, visible keys still cooling.
// One query covers every service, so search_tools can say when a service
// whose keys all rest comes back without a query per tool.
func (s *Store) KeysRestingUntil(ctx context.Context) (map[int64]time.Time, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT service_id, MIN(cooldown_until) FROM keys
		WHERE hidden=0 AND deleted_at IS NULL AND status=? AND cooldown_until>? GROUP BY service_id`, StatusActive, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id, until int64
		if err := rows.Scan(&id, &until); err != nil {
			return nil, err
		}
		out[id] = time.Unix(until, 0).UTC()
	}
	return out, rows.Err()
}
