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

// SaveServiceMCP upserts the upstream MCP sync state for a service.
func (s *Store) SaveServiceMCP(ctx context.Context, m *ServiceMCP) error {
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	tools := m.Tools
	if tools == nil {
		tools = []ServiceMCPTool{}
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	var synced any
	if m.SyncedAt != nil {
		synced = m.SyncedAt.Unix()
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO service_mcp(
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
		m.ServiceID, m.ServerName, m.ServerVersion, m.ProtocolVersion, m.Instructions, string(raw), len(tools), synced, m.LastError)
	return err
}

// DeleteServiceMCP removes stored upstream MCP state for a service.
func (s *Store) DeleteServiceMCP(ctx context.Context, serviceID int64) error {
	if err := s.mcpSchema(ctx); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM service_mcp WHERE service_id=?`, serviceID)
	return err
}
