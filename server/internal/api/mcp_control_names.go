package api

// What an agent sees of the control MCP's tools, in one place, like
// mcp_names.go for the app MCP. Internally the tools keep their short names
// (list_keys, delete_service, ...), which the dashboard, groups and tests
// use. Agents see them as acnologia_<name>, so they cannot clash with
// another server's tools, and a call by the old name still works. The list
// tools page with limit and offset, and the tools whose result has a fixed
// shape state it as an output schema.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ctlIndex is ctlTools by internal name, filled with the table.
var ctlIndex map[string]*ctlTool

func indexCtlTools() {
	ctlIndex = make(map[string]*ctlTool, len(ctlTools))
	for i := range ctlTools {
		t := &ctlTools[i]
		if p, ok := ctlPaging[t.Name]; ok {
			t.Page = &p
		}
		ctlIndex[t.Name] = t
	}
}

// ctlCanonicalTool maps a name an agent sent to the internal name: the
// prefixed form of a control tool loses its prefix, anything else stays.
func ctlCanonicalTool(name string) string {
	if rest, ok := strings.CutPrefix(name, toolPrefix); ok && ctlIndex[rest] != nil {
		return rest
	}
	return name
}

// ctlPublicTool is the name agents see for a control tool.
func ctlPublicTool(name string) string {
	if ctlIndex[name] != nil {
		return toolPrefix + name
	}
	return name
}

// ctlProseNames are tool names that are also plain words or values in the
// texts (a "Web search", kind create_provider), so ctlPublicText leaves
// them alone. Texts that mean the tool name it with ctlPublicTool.
var ctlProseNames = map[string]bool{"search": true, "batch": true, "overview": true, "create_provider": true, "update_provider": true}

// ctlPublicText rewrites control tool names in text an agent reads
// (instructions, descriptions, prompts, errors) to their public form.
// Running it twice changes nothing.
func ctlPublicText(s string) string {
	return wordRe.ReplaceAllStringFunc(s, func(w string) string {
		if ctlProseNames[w] {
			return w
		}
		return ctlPublicTool(w)
	})
}

// ctlPublicSchema copies an input schema with its descriptions rewritten.
// The schema maps are shared between tools, so they are never changed.
func ctlPublicSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if s, ok := x.(string); ok && k == "description" {
				out[k] = ctlPublicText(s)
			} else {
				out[k] = ctlPublicSchema(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = ctlPublicSchema(x)
		}
		return out
	}
	return v
}

func ctlInstructions() string {
	return ctlPublicText("This is Acnologia's control MCP: it runs the server (services, keys, apps, providers, AI builds, sharing, usage) " +
		"as the signed in user who owns the admin API token, with that user's role (or the token's scope when lower). Every change is audited under that user. " +
		"Tools are named acnologia_<name>; the names without the prefix still work. " +
		"Tools the role cannot use are not listed. Services are named by id, slug or name. " +
		"call_service calls a provider API through the pooled keys as the app \"Control MCP\"; the key is never shown. " +
		ctlPublicTool("search") + " finds anything by text; " + ctlPublicTool("batch") + " runs up to 20 tools in one call; list_routes lists every admin route. " +
		"List tools return one page: pass limit and offset, and when has_more is true call again with offset set to next_offset; summary: true gives counts and the first rows. " +
		"Read tools take fields and filter to cut big results down, and the guide tools take format: markdown. Results are capped at 64 KB. " +
		"A failed call has isError and structuredContent {code, message, status, hint}: branch on code. " +
		"On a new server, checklist says what is left to set up and quickstart does it in one call. " +
		"Destructive tools (deletes, revokes, rotate, disconnect) do nothing on the first call: they say what would happen, then run when called again with confirm: true. Ask the user before confirming. " +
		"The app side, where an app's token leases keys and proxies calls, is the separate /mcp endpoint.")
}

// ---- paging ----

// ctlPage is how a list tool pages: the top level list it cuts, the page
// size when limit is not given, the largest page, and any query the route
// needs to return everything it can (its own limit).
type ctlPage struct {
	List     string
	Def, Max int
	Fetch    string
}

var ctlPaging = map[string]ctlPage{
	"list_events":          {List: "events", Def: 100, Max: 500, Fetch: "limit=500"},
	"list_audit":           {List: "audit", Def: 200, Max: 500, Fetch: "limit=1000"},
	"list_services":        {List: "services", Def: 100, Max: 500},
	"list_keys":            {List: "keys", Def: 100, Max: 500},
	"list_apps":            {List: "apps", Def: 100, Max: 500},
	"list_app_tokens":      {List: "tokens", Def: 100, Max: 500},
	"list_providers":       {List: "providers", Def: 25, Max: 50},
	"list_ai_runs":         {List: "runs", Def: 50, Max: 50},
	"list_webhooks":        {List: "webhooks", Def: 100, Max: 500},
	"list_egress":          {List: "proxies", Def: 100, Max: 500},
	"list_groups":          {List: "groups", Def: 100, Max: 500},
	"list_shares":          {List: "shares", Def: 100, Max: 500},
	"list_peers":           {List: "peers", Def: 100, Max: 500},
	"list_users":           {List: "users", Def: 100, Max: 500},
	"list_refresh_runs":    {List: "runs", Def: 50, Max: 200},
	"list_refresh_reviews": {List: "reviews", Def: 50, Max: 200},
	"search":               {List: "results", Def: 50, Max: 200},
	"list_routes":          {List: "routes", Def: 100, Max: 300},
	"list_capabilities":    {List: "capabilities", Def: 100, Max: 500},
	"assistant_threads":    {List: "threads", Def: 50, Max: 200},
}

// pageProps are the paging inputs of a list tool.
func pageProps(p *ctlPage) map[string]any {
	return map[string]any{
		"limit":  iProp("Items per page, default " + itoa(p.Def) + ", at most " + itoa(p.Max)),
		"offset": iProp("Items to skip; pass next_offset from the last page to get the next one"),
		"summary": bProp("Return counts instead of the whole list: total, counts by status, kind, provider and similar fields, " +
			"and only the first " + itoa(summaryRows) + " rows (or limit rows). Use it first on a big list"),
	}
}

// fetchQuery is the extra query a list tool's route needs.
func (p *ctlPage) fetchQuery() url.Values {
	q, _ := url.ParseQuery(p.Fetch)
	return q
}

// pageResult cuts one page out of a list tool's result: the filter input
// first, then offset and limit, then fields; the paging block is always
// added. Anything that is not a JSON object holding the list goes to
// shapeResult as is.
func pageResult(res map[string]any, in map[string]any, p *ctlPage) map[string]any {
	if res["isError"] == true {
		return res
	}
	content, _ := res["content"].([]map[string]any)
	if len(content) != 1 {
		return shapeResult(res, in)
	}
	text, _ := content[0]["text"].(string)
	if full, ok := res[ctlRawKey].(string); ok {
		text = full
	}
	var m map[string]any
	if json.Unmarshal([]byte(text), &m) != nil || m == nil {
		return shapeResult(res, in)
	}
	arr, ok := m[p.List].([]any)
	if !ok && m[p.List] != nil {
		return shapeResult(res, in)
	}
	filter, _ := in["filter"].(map[string]any)
	matched, _ := pageList(arr, filter, 0, 0)
	def := p.Def
	summary, _ := in["summary"].(bool)
	if summary {
		// Counts over every match, and only the first few rows.
		m["summarized"] = true
		m["counts"] = facetCounts(matched)
		def = summaryRows
	}
	items, info := paginate(matched, argInt(in, "offset"), argInt(in, "limit"), def, p.Max)
	fields := argStrings(in, "fields")
	render := func(items []any, info pageInfo) []byte {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		out[p.List] = items
		if len(fields) > 0 {
			keep := fields
			if summary {
				keep = append(append([]string{}, fields...), "summarized", "counts")
			}
			out, _ = project(out, pathTree(keep)).(map[string]any)
		}
		b, _ := json.Marshal(info.into(out))
		return b
	}
	b := render(items, info)
	if len(b) > ctlResultCap && len(items) > 1 {
		// Cut the page to the most rows that fit, so the result stays whole
		// JSON and next_offset picks up where it stopped.
		lo, hi := 1, len(items)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if len(render(items[:mid], pageInfo{total: info.total, count: mid, offset: info.offset})) <= ctlResultCap-512 {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		m["trimmed"] = fmt.Sprintf("this page was cut from %d to %d items to stay under the %d KB result cap; pass fields to keep only what you need, "+
			"a smaller limit, or summary: true for counts and the first rows", len(items), lo, ctlResultCap>>10)
		b = render(items[:lo], pageInfo{total: info.total, count: lo, offset: info.offset})
	}
	return ctlText(string(b), false)
}

// summaryRows is the page size of a list tool called with summary: true.
const summaryRows = 5

// facetFields are the list item fields summary mode counts by, when an item
// has them as text or a boolean.
var facetFields = []string{"status", "effective_status", "validity", "kind", "mode", "provider", "service_slug", "role",
	"type", "category", "active", "tier", "recipient", "auth_type", "strategy", "rotation", "method", "tool"}

// facetCounts counts the items by each facet field: {status: {active: 3,
// disabled: 1}}. A field with more than 25 values is left out, as it says
// little.
func facetCounts(items []any) map[string]any {
	counts := map[string]map[string]int{}
	for _, x := range items {
		row, ok := x.(map[string]any)
		if !ok {
			continue
		}
		for _, f := range facetFields {
			var v string
			switch t := row[f].(type) {
			case string:
				v = t
			case bool:
				v = fmt.Sprint(t)
			default:
				continue
			}
			if v == "" {
				v = "(none)"
			}
			if counts[f] == nil {
				counts[f] = map[string]int{}
			}
			counts[f][v]++
		}
	}
	out := map[string]any{}
	for f, c := range counts {
		if len(c) <= 25 {
			out[f] = c
		}
	}
	return out
}

// ---- output schemas ----
//
// Only tools whose result has a fixed shape, each checked against a real
// result in TestControlMCPResultsMatchOutputSchemas. fields can drop any
// field a read tool returns, so only the paging block is required.

func ctlPaged(list string, item map[string]any, desc string) map[string]any {
	return outObj(withPage(map[string]any{list: listOf(item, desc)}), "total", "count", "offset", "has_more")
}

var ctlOutputs = map[string]map[string]any{
	"whoami": outObj(map[string]any{
		"id": typed("integer", "User id"), "email": typed("string", "Sign in email"), "name": typed("string", "Display name"),
		"role": typed("string", "The role this connection has"),
	}),
	"list_services": ctlPaged("services", outObj(map[string]any{
		"id": typed("integer", "Service id"), "slug": typed("string", "Service slug"), "name": typed("string", "Display name"),
		"provider": typed("string", "Provider slug"),
	}), "Services on this page"),
	"list_keys": ctlPaged("keys", outObj(map[string]any{
		"id": typed("integer", "Key id"), "service_id": typed("integer", "Service id"), "service_slug": typed("string", "Service slug"),
		"label": typed("string", "Key label"), "status": typed("string", "The key's status"),
	}), "Keys on this page"),
	"list_events": ctlPaged("events", outObj(map[string]any{
		"id": typed("integer", "Event id"), "ts": typed("string", "When it happened"), "kind": typed("string", "Event kind"),
		"service_id": anyOf("Service id, when the event has one"), "key_id": anyOf("Key id, when the event has one"),
	}), "Events on this page, newest first"),
	"list_apps": ctlPaged("apps", outObj(map[string]any{
		"id": typed("integer", "App id"), "name": typed("string", "App name"), "mode": typed("string", "proxy, lease or both"),
		"active": typed("boolean", "Whether its tokens work"),
	}), "Apps on this page"),
	"list_app_tokens": ctlPaged("tokens", outObj(map[string]any{
		"id": typed("integer", "Token id"), "name": typed("string", "Token name"), "prefix": typed("string", "The token's first characters"),
	}), "Tokens on this page"),
	"list_providers": ctlPaged("providers", outObj(map[string]any{
		"slug": typed("string", "Provider slug"), "name": typed("string", "Display name"),
	}), "Providers on this page"),
	"list_webhooks": ctlPaged("webhooks", outObj(map[string]any{
		"id": typed("integer", "Webhook id"), "name": typed("string", "Webhook name"), "active": typed("boolean", "Whether it sends events"),
	}), "Webhooks on this page"),
	"list_egress": ctlPaged("proxies", outObj(map[string]any{
		"id": typed("integer", "Egress proxy id"), "name": typed("string", "Proxy name"),
	}), "Egress proxies on this page"),
	"list_groups": ctlPaged("groups", outObj(map[string]any{
		"id": typed("integer", "Group id"), "slug": typed("string", "Group slug"), "name": typed("string", "Display name"),
	}), "Service groups on this page"),
	"list_shares": ctlPaged("shares", outObj(map[string]any{
		"id": typed("string", "Share id"), "name": typed("string", "Share name"), "status": typed("string", "Share status"),
	}), "Shares on this page"),
	"list_users": ctlPaged("users", outObj(map[string]any{
		"id": typed("integer", "User id"), "email": typed("string", "Sign in email"), "role": typed("string", "Role"),
	}), "Users on this page"),
	"list_audit": ctlPaged("audit", outObj(map[string]any{
		"id": typed("integer", "Entry id"), "ts": typed("string", "When it happened"), "action": typed("string", "What was done"),
	}), "Audit entries on this page, newest first"),
	"search": ctlPaged("results", outObj(map[string]any{
		"type": typed("string", "What kind of thing matched"), "id": anyOf("Its id: a number, or a slug or string id"),
		"name": anyOf("Its display name"), "matched": typed("string", "The field that matched"),
		"value": typed("string", "The matching text, clipped"), "details_tool": typed("string", "The tool that shows the details"),
	}), "Hits on this page"),
}
