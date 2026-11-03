package api

// Flexible control MCP tools: result shaping on every read tool (fields,
// filter, limit, offset, and Markdown for the guides), search across
// everything the user can see, batch to run several tools in one call, and
// list_routes so api_request can reach any admin route. Like the rest of the
// control MCP, each of them goes through the /api/admin routes as the signed
// in user, so roles, validation and audit stay with the routes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// ---- result shaping ----

// shapeProps are the inputs every read tool (and api_request and batch
// items) take to cut a result down to what the model needs.
var shapeProps = map[string]any{
	"fields": aProp(map[string]any{"type": "string"},
		"Keep only these dot paths, from the top of the result; lists are passed through, so services.slug keeps the slug of every service. Empty keeps everything"),
	"filter": map[string]any{"type": "object", "additionalProperties": true,
		"description": "Keep list items whose fields match: {\"status\": \"active\", \"provider\": \"serp\"}. Text matches as a case insensitive substring, numbers and booleans exactly; dot paths reach nested fields. Applies to the top level lists"},
	"limit":  iProp("Return at most this many items of each top level list"),
	"offset": iProp("Skip this many items of each top level list first, for paging"),
}

// wantsShape reports whether in asks for any shaping.
func wantsShape(in map[string]any) bool {
	for _, k := range []string{"fields", "filter", "limit", "offset", "format"} {
		if v, ok := in[k]; ok && v != nil && v != "" {
			return true
		}
	}
	return false
}

// shapeResult applies the shaping inputs to a successful JSON tool result.
// Anything else (errors, confirm steps, plain text) passes through as is.
func shapeResult(res map[string]any, in map[string]any) map[string]any {
	if !wantsShape(in) || res["isError"] == true {
		return res
	}
	content, _ := res["content"].([]map[string]any)
	if len(content) != 1 {
		return res
	}
	text, _ := content[0]["text"].(string)
	if full, ok := res[ctlRawKey].(string); ok {
		text = full
	}
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		return res
	}
	// Guides: just the Markdown, which reads better than the JSON around it.
	if strings.EqualFold(argStr(in, "format"), "markdown") {
		if m, ok := v.(map[string]any); ok {
			if md, ok := m["markdown"].(string); ok {
				return ctlText(md, false)
			}
		}
	}
	filter, _ := in["filter"].(map[string]any)
	limit, offset := argInt(in, "limit"), argInt(in, "offset")
	counts := map[string]any{}
	switch t := v.(type) {
	case []any:
		var n int
		v, n = pageList(t, filter, offset, limit)
		counts["items"] = n
	case map[string]any:
		for k, x := range t {
			if arr, ok := x.([]any); ok && (filter != nil || limit > 0 || offset > 0) {
				var n int
				t[k], n = pageList(arr, filter, offset, limit)
				counts[k] = n
			}
		}
	}
	if fields := argStrings(in, "fields"); len(fields) > 0 {
		v = project(v, pathTree(fields))
	}
	if m, ok := v.(map[string]any); ok && len(counts) > 0 && (filter != nil || limit > 0 || offset > 0) {
		// How many matched before paging, so the model knows to page on.
		m["_matched"] = counts
	}
	b, _ := json.Marshal(v)
	return ctlText(string(b), false)
}

func argInt(in map[string]any, k string) int {
	switch n := in[k].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func argStrings(in map[string]any, k string) []string {
	out := []string{}
	switch v := in[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// pageList filters a list, then applies offset and limit. It returns the
// list and how many items matched the filter.
func pageList(arr []any, filter map[string]any, offset, limit int) ([]any, int) {
	out := []any{}
	for _, x := range arr {
		if matches(x, filter) {
			out = append(out, x)
		}
	}
	n := len(out)
	if offset > 0 {
		if offset >= len(out) {
			out = []any{}
		} else {
			out = out[offset:]
		}
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, n
}

func matches(x any, filter map[string]any) bool {
	for path, want := range filter {
		got, ok := getPath(x, path)
		if !ok {
			return false
		}
		if ws, isStr := want.(string); isStr {
			if !strings.Contains(strings.ToLower(fmt.Sprint(got)), strings.ToLower(ws)) {
				return false
			}
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

func getPath(x any, path string) (any, bool) {
	cur := x
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// pathTree turns ["a.b", "a.c", "d"] into {a: {b: nil, c: nil}, d: nil};
// a nil leaf keeps the whole value.
func pathTree(paths []string) map[string]any {
	root := map[string]any{}
	for _, p := range paths {
		node := root
		segs := strings.Split(p, ".")
		for i, s := range segs {
			if i == len(segs)-1 {
				if _, exists := node[s]; !exists {
					node[s] = nil
				}
				break
			}
			next, ok := node[s].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[s] = next
			}
			node = next
		}
	}
	return root
}

// project keeps the paths in tree; lists are passed through.
func project(v any, tree map[string]any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			out = append(out, project(x, tree))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, sub := range tree {
			x, ok := t[k]
			if !ok {
				continue
			}
			if st, ok := sub.(map[string]any); ok {
				out[k] = project(x, st)
			} else {
				out[k] = x
			}
		}
		return out
	}
	return v
}

// ---- search ----

// searchSource is one kind of thing search looks through: its list route,
// the field holding the list, its id and display fields, and the fields
// matched. Secret bearing fields (webhook URLs, key secrets) are never
// matched or returned.
type searchSource struct {
	Type, Path, List, ID, Name, Tool string
	Match                            []string
}

var searchSources = []searchSource{
	{"service", "/api/admin/services", "services", "id", "name", "get_service", []string{"slug", "name", "provider", "category", "description", "base_url"}},
	{"key", "/api/admin/keys", "keys", "id", "label", "key_details", []string{"label", "service_slug", "account_name", "account_email", "account_ref", "notes", "tier"}},
	{"app", "/api/admin/apps", "apps", "id", "name", "list_apps", []string{"name", "description"}},
	{"provider", "/api/admin/providers", "providers", "slug", "name", "get_provider", []string{"slug", "name", "category", "description", "website"}},
	{"share", "/api/admin/shares", "shares", "id", "name", "get_share", []string{"name", "label", "email", "note", "peer_name", "services"}},
	{"peer", "/api/admin/peers", "peers", "id", "name", "list_peers", []string{"name", "url", "server_id"}},
	{"webhook", "/api/admin/webhooks", "webhooks", "id", "name", "list_webhooks", []string{"name", "format"}},
	{"egress", "/api/admin/egress", "proxies", "id", "name", "list_egress", []string{"name", "kind"}},
	{"group", "/api/admin/groups", "groups", "id", "name", "list_groups", []string{"slug", "name", "members"}},
	{"user", "/api/admin/users", "users", "id", "email", "list_users", []string{"email", "name", "role"}},
}

func searchTypes() []string {
	out := make([]string, 0, len(searchSources))
	for _, s := range searchSources {
		out = append(out, s.Type)
	}
	return out
}

// ctlSearch is search: one query across every list the user can read.
func ctlSearch(c *ctlCtx, in map[string]any) map[string]any {
	q := strings.ToLower(strings.TrimSpace(argStr(in, "query")))
	if q == "" {
		return ctlFail("query is required")
	}
	want := map[string]bool{}
	for _, t := range argStrings(in, "types") {
		want[strings.ToLower(t)] = true
	}
	limit := argInt(in, "limit")
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	hits := []map[string]any{}
	searched, skipped := []string{}, map[string]string{}
	for _, src := range searchSources {
		if len(want) > 0 && !want[src.Type] {
			continue
		}
		code, out, msg := c.apiJSON(http.MethodGet, src.Path, nil)
		if code >= 400 {
			// A role that cannot read a list just does not search it.
			skipped[src.Type] = msg
			continue
		}
		searched = append(searched, src.Type)
		list, _ := out[src.List].([]any)
		for _, x := range list {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			for _, f := range src.Match {
				val, ok := m[f]
				if !ok || val == nil {
					continue
				}
				text := fmt.Sprint(val)
				if b, err := json.Marshal(val); err == nil && !isScalar(val) {
					text = string(b)
				}
				if strings.Contains(strings.ToLower(text), q) {
					hits = append(hits, map[string]any{"type": src.Type, "id": m[src.ID], "name": m[src.Name],
						"matched": f, "value": clipText(text, 160), "details_tool": src.Tool})
					break
				}
			}
		}
	}
	total := len(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return ctlJSON(map[string]any{"query": q, "results": hits, "total": total, "searched": searched, "skipped": skipped})
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, bool, int, int64:
		return true
	}
	return false
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// ---- batch ----

const batchMax = 20

// ctlBatch is batch: several tool calls in one request, in order. Each call
// is checked like a call of its own (role, groups, read only, and confirm
// for destructive tools), so batch can do nothing a single call could not.
func ctlBatch(c *ctlCtx, in map[string]any) map[string]any {
	calls, _ := in["calls"].([]any)
	if len(calls) == 0 {
		return ctlFail("calls is required: a list of {tool, arguments}")
	}
	if len(calls) > batchMax {
		return ctlFail(fmt.Sprintf("at most %d calls in one batch", batchMax))
	}
	stop, _ := in["stop_on_error"].(bool)
	results := []map[string]any{}
	failed := 0
	for i, x := range calls {
		item, _ := x.(map[string]any)
		name := argStr(item, "tool")
		args, _ := item["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		entry := map[string]any{"index": i, "tool": name}
		t := ctlToolByName(name)
		var res map[string]any
		switch {
		case t == nil:
			res = ctlFail("unknown tool: " + name)
		case t.Name == "batch":
			res = ctlFail("a batch cannot contain batch")
		default:
			res = c.call(t, args)
		}
		ok := res["isError"] != true
		entry["ok"] = ok
		entry["result"] = resultValue(res)
		if sc, has := res["structuredContent"]; has {
			entry["structured"] = sc
		}
		results = append(results, entry)
		if !ok {
			failed++
			if stop {
				break
			}
		}
	}
	return ctlJSON(map[string]any{"results": results, "ran": len(results), "failed": failed})
}

// resultValue is a tool result's text, decoded when it is JSON.
func resultValue(res map[string]any) any {
	content, _ := res["content"].([]map[string]any)
	if len(content) == 0 {
		return nil
	}
	text, _ := content[0]["text"].(string)
	var v any
	if json.Unmarshal([]byte(text), &v) == nil {
		return v
	}
	return text
}

// ---- routes ----

// ctlListRoutes is list_routes: every /api/admin route, for api_request.
func ctlListRoutes(c *ctlCtx, in map[string]any) map[string]any {
	routes, ok := c.s.handler().(chi.Routes)
	if !ok {
		return ctlFail("routes are not available")
	}
	q := strings.ToLower(strings.TrimSpace(argStr(in, "contains")))
	covered := map[string]string{}
	for _, t := range ctlTools {
		if t.Path != "" {
			covered[t.Method+" "+t.Path] = t.Name
		}
	}
	type route struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Tool   string `json:"tool,omitempty"`
	}
	out := []route{}
	_ = chi.Walk(routes, func(method, p string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		p = strings.ReplaceAll(p, "/*/", "/")
		if !strings.HasPrefix(p, "/api/admin/") {
			return nil
		}
		if q != "" && !strings.Contains(strings.ToLower(method+" "+p), q) {
			return nil
		}
		out = append(out, route{Method: method, Path: p, Tool: toolFor(covered, method, p)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return ctlJSON(map[string]any{"routes": out, "count": len(out),
		"note": "tool names the dedicated tool for a route; call the others with api_request (paths with {id} take the id). The route still checks your role."})
}

// toolFor finds the tool whose path matches a route, placeholders aside.
func toolFor(covered map[string]string, method, p string) string {
	norm := func(s string) string {
		segs := strings.Split(s, "/")
		for i, seg := range segs {
			if strings.HasPrefix(seg, "{") {
				segs[i] = "{}"
			}
		}
		return strings.Join(segs, "/")
	}
	for k, name := range covered {
		m, path, _ := strings.Cut(k, " ")
		if m == method && norm(path) == norm(p) {
			return name
		}
	}
	return ""
}

// ctlFlexTools are the tools in this file, plus the settings added with
// them, appended to the table.
func ctlFlexTools() []ctlTool {
	m, a, sa := store.RoleMember, store.RoleAdmin, store.RoleSuperadmin
	return []ctlTool{
		{Name: "search", Title: "Search everything", Group: "power", Kind: ctlRead, Role: m, Run: ctlSearch,
			Desc: "Find services, keys, apps, providers, shares, connected servers, webhooks, egress proxies, groups and users by text in one call. " +
				"Each hit has its type, id, name, the field that matched and the tool that shows the details. Lists your role cannot read are skipped and named.",
			Props: map[string]any{
				"query": sProp("Text to look for, case insensitive"),
				"types": aProp(map[string]any{"type": "string", "enum": searchTypes()}, "Only these kinds; empty searches all"),
				"limit": iProp("Most results, default 50, up to 200"),
			}, Required: []string{"query"}},
		{Name: "batch", Title: "Run several tools", Group: "power", Kind: ctlWrite, Role: m, Run: ctlBatch,
			Desc: fmt.Sprintf("Run up to %d tool calls in one request, in order, and get every result back. Each call is checked as if made alone: your role, the connection's groups and read only setting, and confirm: true for destructive tools. Use it to read many things at once or to make a set of related changes.", batchMax),
			Props: map[string]any{
				"calls": aProp(map[string]any{"type": "object", "properties": map[string]any{
					"tool": sProp("Tool name"), "arguments": map[string]any{"type": "object", "description": "The tool's arguments"},
				}, "required": []string{"tool"}}, "The calls to run"),
				"stop_on_error": bProp("Stop at the first failed call; by default every call runs"),
			}, Required: []string{"calls"}},
		{Name: "list_routes", Title: "List admin routes", Group: "power", Kind: ctlRead, Role: m, Run: ctlListRoutes,
			Desc:  "Every /api/admin route this server has, with the tool that covers it when there is one. For anything without a tool, call api_request (in the raw group).",
			Props: map[string]any{"contains": sProp("Only routes whose method or path contain this text")}},
		{Name: "health_settings", Title: "Health check interval", Group: "overview", Kind: ctlRead, Role: a, Method: http.MethodGet, Path: "/api/admin/health/settings",
			Desc: "How often every key is checked against its provider in the background: the interval in effect, whether it was set on the Settings page or comes from ACNOLOGIA_HEALTH_INTERVAL, and the choices."},
		{Name: "update_health_settings", Title: "Set the health check interval", Group: "overview", Kind: ctlWrite, Role: sa, Method: http.MethodPut, Path: "/api/admin/health/settings",
			Desc: "Change how often keys are health checked. Applies within a minute, no restart. \"\" goes back to ACNOLOGIA_HEALTH_INTERVAL.",
			// Not Required: "" is a real value (back to the environment).
			Props: map[string]any{"interval": eProp("New interval", "", "off", "15m", "30m", "1h", "6h", "12h", "24h", "7d")}, Body: []string{"interval"}},
	}
}
