package api

import (
	"fmt"

	"github.com/cogito-keys/cogito/internal/store"
)

// opsTools cover webhooks, egress proxies, service groups, app usage, the
// circuit breaker and the response cache.
func opsTools() []*chatTool {
	none := map[string]any{}
	idProp := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	webhookProps := map[string]any{
		"name":   map[string]any{"type": "string"},
		"url":    map[string]any{"type": "string", "description": "https endpoint that receives events"},
		"format": map[string]any{"type": "string", "description": "payload format; list_webhooks shows the choices"},
		"events": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "event types; list_webhooks shows the choices"},
		"active": map[string]any{"type": "boolean"},
	}
	egressProps := map[string]any{
		"name":   map[string]any{"type": "string"},
		"url":    map[string]any{"type": "string", "description": "proxy URL, for example http://user:pass@host:port or socks5://host:port"},
		"region": map[string]any{"type": "string"},
		"notes":  map[string]any{"type": "string"},
	}
	groupProps := map[string]any{
		"slug":        map[string]any{"type": "string"},
		"name":        map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
		"members":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "service slugs in fallback order"},
	}
	svcPath := func(cs *chatSession, in map[string]any, suffix string) (string, *store.Service, error) {
		sv, err := cs.resolveService(argStr(in, "service"))
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("/api/admin/services/%d%s", sv.ID, suffix), sv, nil
	}
	return []*chatTool{
		{
			Name: "list_webhooks", Role: store.RoleMember, Props: none,
			Desc: "Webhooks that notify other systems about key and service events, with the event types and formats available.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/webhooks", nil, "Listed webhooks", nil)
			},
		},
		{
			Name: "list_egress_proxies", Role: store.RoleMember, Props: none,
			Desc: "Egress proxies that services or keys can send their traffic through (URLs are shown only as hints).",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/egress", nil, "Listed egress proxies", nil)
			},
		},
		{
			Name: "list_groups", Role: store.RoleMember, Props: none,
			Desc: "Service groups: several services behind one name, used in order as fallbacks.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/groups", nil, "Listed service groups", nil)
			},
		},
		{
			Name: "app_usage", Role: store.RoleMember, Required: []string{"app_id"}, Props: map[string]any{"app_id": idProp("")},
			Desc: "An app's requests and spend today and this month, its limits, and whether a limit is hit.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				return cs.simple("GET", fmt.Sprintf("/api/admin/apps/%d/usage", id), nil, fmt.Sprintf("Read usage of app %d", id), nil)
			},
		},
		{
			Name: "service_breaker", Role: store.RoleMember, Required: []string{"service"},
			Props: map[string]any{"service": map[string]any{"type": "string"}},
			Desc:  "The circuit breaker state of a service (whether Acnologia paused it after repeated provider failures).",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				path, sv, err := svcPath(cs, in, "/breaker")
				if err != nil {
					return toolFail(err.Error())
				}
				return cs.simple("GET", path, nil, "Read the breaker of "+sv.Slug, nil)
			},
		},
		{
			Name: "cache_stats", Role: store.RoleMember, Props: none,
			Desc: "Response cache size and hit rates.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/cache", nil, "Read cache stats", nil)
			},
		},
		{
			Name: "create_webhook", Role: store.RoleAdmin, Required: []string{"name", "url"}, Props: webhookProps,
			Desc: "Create a webhook. Its signing secret is shown to the user once; you never see it.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				code, out, msg := cs.api("POST", "/api/admin/webhooks", pick(in, "name", "url", "format", "events", "active"))
				if code >= 400 {
					return toolFail(msg)
				}
				sec, _ := out["signing_secret"].(string)
				delete(out, "signing_secret")
				out["signing_secret"] = "shown to the user once in the chat; tell them to copy it now"
				return toolOut{OK: true, Secret: sec, Summary: "Created webhook " + argStr(in, "name"), Link: "/webhooks", Model: asJSON(out)}
			},
		},
		{
			Name: "update_webhook", Role: store.RoleAdmin, Required: []string{"webhook_id"},
			Props: withProps(webhookProps, map[string]any{"webhook_id": idProp("")}),
			Desc:  "Change a webhook's name, URL, format, events, or turn it on or off.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "webhook_id")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/webhooks/%d", id), pick(in, "name", "url", "format", "events", "active"), fmt.Sprintf("Updated webhook %d", id), nil)
			},
		},
		{
			Name: "test_webhook", Role: store.RoleAdmin, Required: []string{"webhook_id"}, Props: map[string]any{"webhook_id": idProp("")},
			Desc: "Send a test event to a webhook and report the result.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "webhook_id")
				return cs.simple("POST", fmt.Sprintf("/api/admin/webhooks/%d/test", id), nil, fmt.Sprintf("Tested webhook %d", id), nil)
			},
		},
		{
			Name: "create_egress_proxy", Role: store.RoleAdmin, Required: []string{"name", "url"}, Props: egressProps, SecretFields: []string{"url"},
			Desc: "Add an egress proxy that services or keys can route through.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("POST", "/api/admin/egress", pick(in, "name", "url", "region", "notes"), "Added egress proxy "+argStr(in, "name"), nil)
			},
		},
		{
			Name: "update_egress_proxy", Role: store.RoleAdmin, Required: []string{"proxy_id"}, SecretFields: []string{"url"},
			Props: withProps(egressProps, map[string]any{"proxy_id": idProp("")}),
			Desc:  "Change an egress proxy.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "proxy_id")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/egress/%d", id), pick(in, "name", "url", "region", "notes"), fmt.Sprintf("Updated egress proxy %d", id), nil)
			},
		},
		{
			Name: "test_egress_proxy", Role: store.RoleAdmin, Required: []string{"proxy_id"}, Props: map[string]any{"proxy_id": idProp("")},
			Desc: "Check an egress proxy works and learn its public IP.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "proxy_id")
				return cs.simple("POST", fmt.Sprintf("/api/admin/egress/%d/test", id), nil, fmt.Sprintf("Tested egress proxy %d", id), nil)
			},
		},
		{
			Name: "create_group", Role: store.RoleAdmin, Required: []string{"slug", "name", "members"}, Props: groupProps,
			Desc: "Create a service group: one name apps can call that falls back across several services in order.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("POST", "/api/admin/groups", pick(in, "slug", "name", "description", "members"), "Created group "+argStr(in, "slug"), nil)
			},
		},
		{
			Name: "update_group", Role: store.RoleAdmin, Required: []string{"group_id"},
			Props: withProps(groupProps, map[string]any{"group_id": idProp("")}),
			Desc:  "Change a service group's name, description or members.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "group_id")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/groups/%d", id), pick(in, "slug", "name", "description", "members"), fmt.Sprintf("Updated group %d", id), nil)
			},
		},
		{
			Name: "reset_breaker", Role: store.RoleAdmin, Required: []string{"service"}, Props: map[string]any{"service": map[string]any{"type": "string"}},
			Desc: "Close a service's circuit breaker so traffic flows again.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				path, sv, err := svcPath(cs, in, "/breaker/reset")
				if err != nil {
					return toolFail(err.Error())
				}
				return cs.simple("POST", path, nil, "Reset the breaker of "+sv.Slug, nil)
			},
		},
		{
			Name: "purge_cache", Role: store.RoleAdmin, Required: []string{"service"}, Props: map[string]any{"service": map[string]any{"type": "string"}},
			Desc: "Drop a service's cached responses.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				path, sv, err := svcPath(cs, in, "/cache/purge")
				if err != nil {
					return toolFail(err.Error())
				}
				return cs.simple("POST", path, nil, "Purged the cache of "+sv.Slug, nil)
			},
		},
		{
			Name: "app_mcp_settings", Role: store.RoleMember, Required: []string{"app_id"}, Props: map[string]any{"app_id": idProp("")},
			Desc: "An app's MCP server settings: which tool sets are on, every tool with whether it is enabled, and the endpoint URL.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				return cs.simple("GET", fmt.Sprintf("/api/admin/apps/%d/mcp", id), nil, fmt.Sprintf("Read MCP settings of app %d", id), nil)
			},
		},
		{
			Name: "update_app_mcp", Role: store.RoleAdmin, Required: []string{"app_id"}, Props: mcpProps(idProp),
			Desc: "Change an app's MCP server: tool sets (core, service_tools, insights), resources, prompts, exclusive leases by default, result size, extra instructions, or tools to hide. Only the fields you send change. Turning on the manage tool set needs enable_app_mcp_manage.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				if on, _ := in["manage"].(bool); on {
					return toolFail("turning on the manage tool set needs enable_app_mcp_manage, which asks the user to confirm")
				}
				return cs.patchMCP(in)
			},
		},
		{
			Name: "enable_app_mcp_manage", Role: store.RoleAdmin, Confirm: true, Required: []string{"app_id"}, Props: map[string]any{"app_id": idProp("")},
			Desc: "Turn on the manage tool set for an app's MCP server, which lets anyone holding that app's token manage keys through MCP. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "app_id")
				a, err := cs.s.Store.GetApp(cs.r.Context(), id)
				if err != nil {
					return "", fmt.Errorf("no app %d", id)
				}
				return fmt.Sprintf("Turn on key management tools in the MCP server of app %s; anyone with its token can then manage keys", a.Name), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.patchMCP(map[string]any{"app_id": in["app_id"], "manage": true})
			},
		},
		confirmDelete("delete_webhook", "webhook_id", "webhook", "/api/admin/webhooks/%d"),
		confirmDelete("delete_egress_proxy", "proxy_id", "egress proxy", "/api/admin/egress/%d"),
		confirmDelete("delete_group", "group_id", "service group", "/api/admin/groups/%d"),
	}
}

// confirmDelete builds a delete tool that waits for the user's Confirm.
func confirmDelete(name, idField, what, pathFmt string) *chatTool {
	return &chatTool{
		Name: name, Role: store.RoleAdmin, Confirm: true, Required: []string{idField},
		Props: map[string]any{idField: map[string]any{"type": "integer"}},
		Desc:  "Delete a " + what + ". The user must confirm.",
		Describe: func(cs *chatSession, in map[string]any) (string, error) {
			id, ok := argNum(in, idField)
			if !ok {
				return "", fmt.Errorf("give %s", idField)
			}
			return fmt.Sprintf("Delete %s %d", what, id), nil
		},
		Run: func(cs *chatSession, in map[string]any) toolOut {
			id, _ := argNum(in, idField)
			return cs.simple("DELETE", fmt.Sprintf(pathFmt, id), nil, fmt.Sprintf("Deleted %s %d", what, id), nil)
		},
	}
}

func mcpProps(idProp func(string) map[string]any) map[string]any {
	b := func(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
	return map[string]any{
		"app_id":            idProp(""),
		"core":              b("lease, report, renew and proxy tools"),
		"service_tools":     b("tools generated from each provider's examples"),
		"insights":          b("read only key insights"),
		"manage":            b("turn off key management tools (turning on needs enable_app_mcp_manage)"),
		"resources":         b(""),
		"prompts":           b(""),
		"exclusive_default": b("get_key is exclusive unless the call says otherwise"),
		"max_result_kb":     map[string]any{"type": "integer", "description": "cap on tool result size, 1 to 2048"},
		"instructions":      map[string]any{"type": "string", "description": "extra instructions sent to MCP clients"},
		"disabled_tools":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "tool names to hide; replaces the list"},
	}
}

// patchMCP merges the given fields into the app's MCP settings, since the
// API replaces the whole settings object.
func (cs *chatSession) patchMCP(in map[string]any) toolOut {
	id, _ := argNum(in, "app_id")
	path := fmt.Sprintf("/api/admin/apps/%d/mcp", id)
	code, cur, msg := cs.api("GET", path, nil)
	if code >= 400 {
		return toolFail(msg)
	}
	settings, _ := cur["settings"].(map[string]any)
	if settings == nil {
		return toolFail("could not read the current MCP settings")
	}
	for _, k := range []string{"core", "service_tools", "insights", "manage", "resources", "prompts",
		"exclusive_default", "max_result_kb", "instructions", "disabled_tools"} {
		if v, ok := in[k]; ok && v != nil {
			settings[k] = v
		}
	}
	delete(settings, "updated_at")
	return cs.simple("PUT", path, settings, fmt.Sprintf("Updated MCP settings of app %d", id), func(m map[string]any) any {
		return map[string]any{"settings": m["settings"], "endpoint": m["endpoint"]}
	})
}
