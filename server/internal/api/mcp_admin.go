package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func (s *Server) loadApp(w http.ResponseWriter, r *http.Request) (*store.App, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return nil, false
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return a, true
}

// mcpView is the settings plus what the app's MCP server exposes with them.
func (s *Server) mcpView(ctx context.Context, a *store.App, st store.MCPSettings) map[string]any {
	tools := s.mcpTools(ctx, a, st)
	// Show disabled tools too, so they can be switched back on.
	all := s.mcpTools(ctx, a, store.MCPSettings{Core: st.Core, ServiceTools: st.ServiceTools, Insights: st.Insights, Manage: st.Manage})
	enabled := map[string]bool{}
	for _, t := range tools {
		enabled[t.Name] = true
	}
	listed := map[string]bool{}
	for _, t := range s.mcpListedTools(ctx, a, st) {
		listed[t.Name] = true
	}
	list := []map[string]any{}
	for _, t := range all {
		list = append(list, map[string]any{"name": t.Name, "title": t.Title, "description": t.Description, "set": t.Set, "enabled": enabled[t.Name], "listed": listed[t.Name]})
	}
	effective := toolModeAll
	if s.searchModeFor(ctx, a, st) {
		effective = toolModeSearch
	}
	base := s.Cfg.PublicURL
	if base == "" {
		base = "http://localhost" + s.Cfg.Addr
	}
	return map[string]any{"settings": st, "tools": list, "endpoint": strings.TrimRight(base, "/") + "/mcp",
		// listing says what tools/list shows right now: in search mode only
		// the tools marked listed, the rest found with search_tools.
		"listing": map[string]any{"mode": effective, "listed": len(listed), "total": len(tools), "auto_max": mcpAutoListMax}}
}

func (s *Server) getAppMCP(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	st, err := s.Store.GetMCPSettings(r.Context(), a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, s.mcpView(r.Context(), a, st))
}

func (s *Server) putAppMCP(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var in store.MCPSettings
	if !decode(w, r, &in) {
		return
	}
	if in.MaxResultKB <= 0 {
		in.MaxResultKB = 200
	}
	if in.MaxResultKB > 2048 {
		writeErr(w, 400, "invalid", "max_result_kb must be 2048 or less")
		return
	}
	if len(in.Instructions) > 8000 {
		writeErr(w, 400, "invalid", "instructions must be under 8,000 characters")
		return
	}
	if in.DisabledTools == nil {
		in.DisabledTools = []string{}
	}
	switch in.ToolMode {
	case "":
		in.ToolMode = toolModeAuto
	case toolModeAuto, toolModeAll, toolModeSearch:
	default:
		writeErr(w, 400, "invalid", "tool_mode must be auto, all or search")
		return
	}
	if err := s.Store.SetMCPSettings(r.Context(), a.ID, in); err != nil {
		writeStoreErr(w, err)
		return
	}
	detail := []string{}
	for name, on := range map[string]bool{"core": in.Core, "service_tools": in.ServiceTools, "insights": in.Insights, "manage": in.Manage} {
		if on {
			detail = append(detail, name)
		}
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.mcp", a.Name, "tool sets: "+strings.Join(detail, ", ")+"; tool mode: "+in.ToolMode)
	st, _ := s.Store.GetMCPSettings(r.Context(), a.ID)
	writeJSON(w, 200, s.mcpView(r.Context(), a, st))
}
