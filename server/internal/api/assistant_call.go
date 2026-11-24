package api

// Tools that let the Assistant call a service for real and set provider
// rotation. call_service is how it learns what a provider serves when the
// docs are the provider itself: a self-hosted server that lists its own
// scrapers and their endpoints, for example, so their catalog can be written
// from that answer.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	// callServiceBody is the most of an answer call_service reads.
	callServiceBody = 8 << 20
	// callServiceText is the most of it the model gets back in one call.
	callServiceText = 60000
)

func callTools() []*chatTool {
	return []*chatTool{
		{
			Name: "call_service", Role: store.RoleAdmin, Required: []string{"service", "path"},
			MaxModel: callServiceText,
			Props: map[string]any{
				"service": map[string]any{"type": "string", "description": "service slug, name or id"},
				"method":  map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, "description": "GET by default"},
				"path":    map[string]any{"type": "string", "description": "path on the provider, such as /api/sources or /~amazon-scraper/products/details"},
				"query":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"body":    map[string]any{"type": "object", "description": "JSON body for POST, PUT or PATCH"},
				"items":   map[string]any{"type": "string", "description": "dot path to the list in a JSON answer, such as data.results; empty for a top level list"},
				"select":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "dot paths to keep from each list item, such as name, spec.title, spec.endpoints; empty keeps whole items"},
				"where":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "keep only items whose dot path equals this text, such as {\"kind\": \"scraper\"}"},
				"offset":  map[string]any{"type": "integer"},
				"limit":   map[string]any{"type": "integer", "description": "items per page, default 40, max 200"},
			},
			Desc: "Call one endpoint of a service through the proxy, as the Assistant app, and read the answer. Use it to learn what a provider really serves when its own API lists it (a self-hosted server's list of sources, scrapers or models), to check an endpoint, or to answer a question with live data. " +
				"For a long JSON list, page it with offset and limit and keep only the fields you need with select and where. GET runs at once and spends a request of quota on a paid provider, so make only the calls the user's request needs; other methods ask the user first.",
			ConfirmIf: func(in map[string]any) bool {
				m := strings.ToUpper(strings.TrimSpace(argStr(in, "method")))
				return m != "" && m != http.MethodGet
			},
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				return fmt.Sprintf("Send %s %s to %s through the proxy, with a pooled key.", strings.ToUpper(argStr(in, "method")), argStr(in, "path"), argStr(in, "service")), nil
			},
			Run: callServiceTool,
		},
		{
			Name: "service_rotation", Role: store.RoleMember, Required: []string{"service"},
			Props: map[string]any{"service": map[string]any{"type": "string", "description": "slug, name or id"}},
			Desc: "Read only. A service's provider rotation mode and the services that serve the same endpoints: other services of its provider, and services of other providers whose catalog lists the same entries (amazon-scraper on Omkar Cloud and on a self-hosted server). " +
				"Shows each one's rotation mode, free keys and the entries they share.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				out := cs.simple("GET", fmt.Sprintf("/api/admin/services/%d/rotation", sv.ID), nil, "Read rotation of "+sv.Slug, nil)
				out.Link = fmt.Sprintf("/services/%d", sv.ID)
				return out
			},
		},
		{
			Name: "set_rotation", Role: store.RoleAdmin, Required: []string{"services", "rotation"},
			Props: map[string]any{
				"services": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "service slugs; every one that should share requests, including the one the user is on"},
				"rotation": map[string]any{"type": "string", "enum": []string{"off", "fallback", "round_robin"}},
			},
			Desc: "Turn provider rotation on or off for services. A request to a service with rotation on can be served by any other service with rotation on that serves the same endpoint: fallback tries the named service first and the others when it has no key, is out of quota, rate limited or failing; round_robin spreads requests over all of them. " +
				"Services take part only when rotation is on for them too, so set it on every service that should share.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				names := argStrings(in, "services")
				if len(names) == 0 {
					return toolFail("list the services")
				}
				ids := []any{}
				for _, n := range names {
					sv, err := cs.resolveService(n)
					if err != nil {
						return toolFail(err.Error())
					}
					ids = append(ids, sv.ID)
				}
				mode := argStr(in, "rotation")
				return cs.simple("POST", "/api/admin/services/rotation", map[string]any{"services": ids, "rotation": mode},
					fmt.Sprintf("Set rotation %s on %d services", orDefault(mode, "off"), len(ids)), nil)
			},
		},
		{
			Name: "list_service_mcp_tools", Role: store.RoleMember, Required: []string{"service"},
			Props: map[string]any{"service": map[string]any{"type": "string", "description": "slug, name or id"}},
			Desc:  "Read only. Show a service's upstream MCP endpoint, server info, sync status, and synced upstream tools.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				out := cs.simple("GET", fmt.Sprintf("/api/admin/services/%d/mcp", sv.ID), nil, "Read MCP tools of "+sv.Slug, nil)
				out.Link = fmt.Sprintf("/services/%d", sv.ID)
				return out
			},
		},
		{
			Name: "sync_service_mcp", Role: store.RoleAdmin, Required: []string{"service"},
			Props: map[string]any{"service": map[string]any{"type": "string", "description": "slug, name or id"}},
			Desc:  "Connect to a service's upstream Streamable HTTP MCP endpoint with a pooled key, discover its tools, and save them so apps can call them through /mcp.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				out := cs.simple("POST", fmt.Sprintf("/api/admin/services/%d/mcp/sync", sv.ID), map[string]any{}, "Synced MCP tools for "+sv.Slug, nil)
				out.Link = fmt.Sprintf("/services/%d", sv.ID)
				return out
			},
		},
		{
			Name: "call_mcp_tool", Role: store.RoleAdmin, Confirm: true, Required: []string{"service", "tool"},
			MaxModel: callServiceText,
			Props: map[string]any{
				"service":   map[string]any{"type": "string", "description": "service slug, name or id"},
				"tool":      map[string]any{"type": "string", "description": "upstream tool name (or public MCP tool name) from list_service_mcp_tools"},
				"arguments": map[string]any{"type": "object", "description": "JSON arguments matching the tool's inputSchema"},
			},
			Desc: "Call one synced upstream MCP tool on a service through the key pool, as the Assistant app. Always asks the user to confirm before running.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				return fmt.Sprintf("Call upstream MCP tool %q on %s through the key pool.", argStr(in, "tool"), argStr(in, "service")), nil
			},
			Run: callUpstreamMCPAssistantTool,
		},
	}
}

func callUpstreamMCPAssistantTool(cs *chatSession, in map[string]any) toolOut {
	sv, err := cs.resolveService(argStr(in, "service"))
	if err != nil {
		return toolFail(err.Error())
	}
	want := strings.TrimSpace(argStr(in, "tool"))
	if want == "" {
		return toolFail("give the tool name to call")
	}
	a, err := cs.assistantApp()
	if err != nil {
		return toolFail(err.Error())
	}
	if !a.Active {
		return toolFail("the Assistant app is turned off; turn it on in Apps to call services")
	}
	cat := cs.s.toolCatalog(cs.ctx)
	var target *mcpTool
	for i := range cat.tools {
		t := &cat.tools[i]
		if t.upstreamMCP && t.service == sv.Slug && (t.Name == want || t.upstreamTool == want) {
			cp := *t
			target = &cp
			break
		}
	}
	if target == nil {
		return toolFail(fmt.Sprintf("no synced MCP tool %q on %s; run sync_service_mcp first", want, sv.Slug))
	}
	var rawArgs json.RawMessage
	if args, ok := in["arguments"]; ok && args != nil {
		rawArgs, _ = json.Marshal(args)
	}
	ctx := store.WithAppToken(context.WithValue(cs.ctx, appKey, a), a.TokenID)
	st, _ := cs.s.Store.GetMCPSettings(ctx, a.ID)
	res, err := cs.s.callUpstreamMCPTool(ctx, cs.r, a, st, target, rawArgs)
	if err != nil {
		return toolFail(err.Error())
	}
	b := asJSON(res)
	if len(b) > callServiceText {
		b = b[:callServiceText] + "\n...(cut)"
	}
	isErr, _ := res["isError"].(bool)
	return toolOut{
		OK:      !isErr,
		Model:   b,
		Summary: fmt.Sprintf("Called MCP tool %s on %s", target.upstreamTool, sv.Slug),
		Link:    fmt.Sprintf("/services/%d", sv.ID),
		Entities: []chatEntity{{Type: "service", ID: sv.ID, Slug: sv.Slug, Name: sv.Name}},
	}
}

func callServiceTool(cs *chatSession, in map[string]any) toolOut {
	sv, err := cs.resolveService(argStr(in, "service"))
	if err != nil {
		return toolFail(err.Error())
	}
	path := strings.TrimSpace(argStr(in, "path"))
	if path == "" {
		return toolFail("give the path to call")
	}
	a, err := cs.assistantApp()
	if err != nil {
		return toolFail(err.Error())
	}
	if !a.Active {
		return toolFail("the Assistant app is turned off; turn it on in Apps to call services")
	}
	query := map[string]string{}
	if q, ok := in["query"].(map[string]any); ok {
		for k, v := range q {
			query[k] = fmt.Sprint(v)
		}
	}
	var body json.RawMessage
	if b, ok := in["body"]; ok && b != nil {
		body, _ = json.Marshal(b)
	}
	method := strings.ToUpper(orDefault(strings.TrimSpace(argStr(in, "method")), http.MethodGet))
	ctx := store.WithAppToken(context.WithValue(cs.ctx, appKey, a), a.TokenID)
	rec, err := cs.s.proxyCapture(ctx, cs.r, sv.Slug, method, path, query, body, nil, callServiceBody)
	if err != nil {
		return toolFail(err.Error())
	}
	raw := rec.Body.Bytes()
	cut := len(raw) > callServiceBody
	if cut {
		raw = raw[:callServiceBody]
	}
	out := map[string]any{"status": rec.Code, "service": orDefault(rec.Header().Get("X-Acnologia-Service"), sv.Slug),
		"content_type": rec.Header().Get("Content-Type"), "bytes": len(raw)}
	summary := fmt.Sprintf("%s %s on %s: %d", method, path, sv.Slug, rec.Code)
	var doc any
	if !cut && json.Unmarshal(raw, &doc) == nil {
		shapeAnswer(out, doc, in)
	} else {
		text := string(raw)
		if len(text) > callServiceText-2000 {
			text = text[:callServiceText-2000]
			out["note"] = "the answer is longer than shown; it is not JSON, or too large to page"
		}
		out["body"] = text
	}
	if n, ok := out["total"].(int); ok {
		summary += fmt.Sprintf(", %d items", n)
	}
	b := asJSON(out)
	if len(b) > callServiceText {
		b = b[:callServiceText] + "\n...(cut: narrow it with select, where or a smaller limit)"
	}
	return toolOut{OK: rec.Code < 400, Model: b, Summary: summary, Link: fmt.Sprintf("/services/%d", sv.ID),
		Entities: []chatEntity{{Type: "service", ID: sv.ID, Slug: sv.Slug, Name: sv.Name}}}
}

// shapeAnswer puts a JSON answer into out: the list at items (or the top
// level list) paged, filtered and cut to the selected fields, or the whole
// document when it holds no list.
func shapeAnswer(out map[string]any, doc any, in map[string]any) {
	list, ok := doc.([]any)
	at := strings.TrimSpace(argStr(in, "items"))
	if at != "" {
		v, found := dotGet(doc, at)
		list, ok = v.([]any)
		if !found || !ok {
			out["note"] = "no list at " + at + "; the whole answer is below"
			out["body"] = doc
			return
		}
	}
	if !ok {
		out["body"] = doc
		return
	}
	if where, _ := in["where"].(map[string]any); len(where) > 0 {
		kept := list[:0:0]
		for _, it := range list {
			match := true
			for k, want := range where {
				v, _ := dotGet(it, k)
				if fmt.Sprint(v) != fmt.Sprint(want) {
					match = false
					break
				}
			}
			if match {
				kept = append(kept, it)
			}
		}
		list = kept
	}
	offset, _ := argNum(in, "offset")
	limit, _ := argNum(in, "limit")
	if limit <= 0 {
		limit = 40
	}
	limit = min(limit, 200)
	offset = max(offset, 0)
	total := len(list)
	end := min(int(offset+limit), total)
	page := []any{}
	if int(offset) < total {
		page = list[offset:end]
	}
	sel := argStrings(in, "select")
	items := make([]any, 0, len(page))
	for _, it := range page {
		if len(sel) == 0 {
			items = append(items, it)
			continue
		}
		row := map[string]any{}
		for _, p := range sel {
			if v, found := dotGet(it, p); found {
				row[p] = v
			}
		}
		items = append(items, row)
	}
	out["total"] = total
	out["offset"] = offset
	out["items"] = items
	if end < total {
		out["next_offset"] = end
	}
}

// dotGet reads a dot path (a.b.0.c) from decoded JSON.
func dotGet(v any, path string) (any, bool) {
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			nv, ok := x[part]
			if !ok {
				return nil, false
			}
			v = nv
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}
