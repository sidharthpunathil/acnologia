package api

// The control MCP's tools, one row each. A tool is a dashboard route plus
// an input schema: the call becomes an in process request to that route as
// the token's owner (mcp_control.go), so the route decides roles,
// validation and audit. Role here only decides what is listed.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Tool kinds. They drive the MCP annotations, read only mode and the
// confirm step.
const (
	ctlRead        = "read"
	ctlWrite       = "write"
	ctlDestructive = "destructive"
)

type ctlTool struct {
	Name  string
	Title string
	Desc  string
	Group string
	Kind  string
	// Role is the lowest role the tool is listed for. The route checks again.
	Role   store.Role
	Method string
	// Path is the route, with {input} placeholders. {service} takes a
	// service id, slug or name and becomes the service id.
	Path     string
	Props    map[string]any
	Required []string
	// Query and Body name the inputs sent in the query string and in the
	// JSON body. A tool with Body fields always sends an object.
	Query []string
	Body  []string
	// Rename maps an input to the API's name for it (query or body).
	Rename map[string]string
	// OpenWorld marks tools that reach the internet.
	OpenWorld bool
	// Prepare builds the body itself, for routes that replace a whole
	// object: it reads the current one and merges the inputs.
	Prepare func(c *ctlCtx, in map[string]any, path string) (any, error)
	// Describe says what a destructive call would do, for the confirm step.
	Describe func(c *ctlCtx, in map[string]any) (string, error)
	// Run replaces the route call for tools that are not one route
	// (call_service, list_service_examples, api_request). It returns the
	// tool result.
	Run func(c *ctlCtx, in map[string]any) map[string]any
}

// ctlGroup is a tool group as the dashboard shows it.
type ctlGroup struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

var ctlGroups = []ctlGroup{
	{"overview", "Overview", true},
	{"services", "Services", true},
	{"keys", "Keys", true},
	{"apps", "Apps", true},
	{"providers", "Providers", true},
	{"call", "Call services", true},
	{"ai", "AI", true},
	{"routing", "Webhooks, egress and groups", true},
	{"sharing", "Sharing", true},
	{"refresh", "Offers refresh", true},
	{"admin", "Users and audit", false},
	{"secrets", "Secrets", false},
	{"raw", "Raw API", false},
}

// ---- schema helpers ----

func sProp(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
func iProp(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
func bProp(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func nProp(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
func eProp(d string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals, "description": d}
}
func aProp(item map[string]any, d string) map[string]any {
	return map[string]any{"type": "array", "items": item, "description": d}
}

// props merges property maps; later maps win.
func props(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func keysOf(m map[string]any, drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	out := []string{}
	for k := range m {
		if !skip[k] {
			out = append(out, k)
		}
	}
	return out
}

var (
	svcIn = sProp("Service id, slug or name")

	serviceFields = map[string]any{
		"slug":              sProp("Lowercase letters, digits and hyphens, used in /proxy/<slug>"),
		"name":              sProp("Display name"),
		"category":          sProp("Category, such as Search or LLM"),
		"description":       sProp("Short description"),
		"base_url":          sProp("Provider base URL, http(s)"),
		"auth_type":         eProp("Where the key goes", "query", "header", "bearer"),
		"auth_name":         sProp("Query parameter or header name for the key (Authorization for bearer)"),
		"strategy":          eProp("How keys are picked", "round_robin", "fill_first", "least_used", "most_quota", "random"),
		"cooldown_sec":      iProp("Seconds a rate limited key rests, 1 to 86400"),
		"max_attempts":      iProp("Keys tried per request, 1 to 20"),
		"max_concurrency":   iProp("Parallel requests per key, 0 for unlimited, up to 1000"),
		"queue_timeout_sec": iProp("Seconds a request waits for a free key, 0 to 300"),
		"egress_id":         iProp("Egress proxy id from list_egress; 0 connects directly"),
		"cache_ttl_sec":     iProp("Cache successful GET responses this long, 0 to 86400; 0 turns caching off, -1 uses the provider default"),
		"cost_per_request":  nProp("USD cost of one request, 0 to 100, used for budgets; -1 uses the provider default"),
		"icon":              sProp("Icon name"),
	}

	keyInFields = map[string]any{
		"label":           sProp("A name for the key, such as the account it belongs to"),
		"tier":            eProp("Backup keys are used only when no primary key is free", "primary", "backup"),
		"hidden":          bProp("Hide the key from everyone but superadmins (superadmin only)"),
		"restricted":      bProp("Only the apps in app_ids may use the key"),
		"app_ids":         aProp(map[string]any{"type": "integer"}, "Apps allowed to use a restricted key"),
		"weight":          iProp("Share of traffic, 1 to 1000"),
		"priority":        iProp("Lower goes first with fill_first"),
		"notes":           sProp("Free text notes"),
		"quota_limit":     iProp("Known quota limit; -1 clears it"),
		"max_concurrency": iProp("Parallel requests for this key; -1 uses the service default, 0 unlimited"),
		"egress_id":       iProp("Egress proxy for this key; 0 uses the service's"),
		"account_name":    sProp("Person or company the provider account belongs to"),
		"account_email":   sProp("Email of the provider account"),
		"account_phone":   sProp("Phone of the provider account"),
		"account_ref":     sProp("Any other account identifier: username, account id, workspace"),
	}

	appFields = map[string]any{
		"name":             sProp("App name"),
		"description":      sProp("What the app is"),
		"mode":             eProp("proxy keeps keys inside Acnologia (best for most apps), lease hands the key to the app, both allows either", "proxy", "lease", "both"),
		"allowed_services": aProp(map[string]any{"type": "string"}, "Service slugs the app may use; empty means all"),
		"active":           bProp("Off stops the app's tokens working"),
		"lease_hidden":     bProp("Let the app lease hidden keys (superadmin only)"),
		"limits": map[string]any{"type": "object", "description": "Replaces all four limits; a missing or null field means no limit",
			"properties": map[string]any{
				"daily_requests": map[string]any{"type": "integer"}, "monthly_requests": map[string]any{"type": "integer"},
				"daily_budget": map[string]any{"type": "number"}, "monthly_budget": map[string]any{"type": "number"},
			}},
	}

	providerFields = map[string]any{
		"slug":        sProp("Provider slug, lowercase letters, digits and hyphens"),
		"name":        sProp("Display name"),
		"category":    sProp("Category, such as Search or LLM"),
		"description": sProp("Short description"),
		"website":     sProp("Website URL"),
		"docs_url":    sProp("API docs URL"),
		"color":       sProp("Brand color, #rrggbb"),
		"status":      sProp("Provider status"),
		"spec": map[string]any{"type": "object",
			"description": "The provider spec: base_url, auth, health check, quota, classification rules, examples, offers and guide. Read get_provider for a working one",
			"properties":  map[string]any{"guide": guideSchema}},
	}

	// guideSchema is spec.guide: how to use the provider's API.
	guideSchema = map[string]any{"type": "object",
		"description": "How to use the API: always write one when adding a provider, with a summary and the main endpoints with their params and an example body",
		"properties": map[string]any{
			"summary": sProp("What the API does and how to start, in a few sentences"),
			"endpoints": aProp(map[string]any{"type": "object", "properties": map[string]any{
				"name":        sProp("Short name, such as Web search"),
				"method":      sProp("HTTP method"),
				"path":        sProp("Path on the provider"),
				"description": sProp("What the endpoint does"),
				"params": aProp(map[string]any{"type": "object", "properties": map[string]any{
					"name":        sProp("Parameter name"),
					"in":          eProp("Where it goes", "query", "path", "header", "body"),
					"type":        sProp("Type, such as string or integer"),
					"required":    bProp("Whether it is required"),
					"description": sProp("What it does"),
				}}, "Parameters"),
				"body":     map[string]any{"description": "Example JSON body"},
				"response": map[string]any{"description": "Example or outline of the response"},
			}}, "The main endpoints"),
			"notes":    aProp(map[string]any{"type": "string"}, "Tips, limits and gotchas"),
			"markdown": sProp("Longer guide in Markdown"),
		}}

	webhookFields = map[string]any{
		"name":   sProp("Webhook name"),
		"url":    sProp("https endpoint that receives events"),
		"format": sProp("Payload format; list_webhooks shows the choices"),
		"events": aProp(map[string]any{"type": "string"}, "Event types; list_webhooks shows the choices"),
		"active": bProp("Turn the webhook on or off"),
	}

	egressFields = map[string]any{
		"name":   sProp("Proxy name"),
		"url":    sProp("Proxy URL, such as http://user:pass@host:port or socks5://host:port"),
		"region": sProp("Region label"),
		"notes":  sProp("Free text notes"),
	}

	groupFields = map[string]any{
		"slug":        sProp("Group slug apps call, like a service slug"),
		"name":        sProp("Display name"),
		"description": sProp("Short description"),
		"members":     aProp(map[string]any{"type": "string"}, "Service slugs in fallback order"),
	}

	shareFields = map[string]any{
		"name":            sProp("Share name, 1 to 80 characters"),
		"note":            sProp("Note for the receiver"),
		"services":        aProp(map[string]any{"type": "string"}, "Service slugs to share"),
		"providers":       aProp(map[string]any{"type": "string"}, "Provider slugs to share (the definition, not keys)"),
		"include_hidden":  bProp("Include hidden keys"),
		"limits":          appFields["limits"],
		"key_scopes":      map[string]any{"type": "object", "description": "Per service slug: {mode: all|count|pick, count, key_ids}. Replaces the scopes", "additionalProperties": map[string]any{"type": "object"}},
		"ends_at":         sProp("RFC 3339 time the share ends; empty clears it"),
		"end_action":      eProp("What happens when ends_at passes", "expire", "revoke"),
		"mode":            eProp("proxy (default), handover (another server holds the keys) or reveal (a person sees the keys); fixed once redeemed", "proxy", "handover", "reveal"),
		"lease":           eProp("Handover renewal window", "1h", "6h", "24h", "72h", "7d"),
		"private_network": bProp("Let the revoke push reach a receiver on a private address"),
		"recipient":       eProp("Another server (default) or a person; fixed at creation", "server", "person"),
		"label":           sProp("The person's name, for a person share"),
		"email":           sProp("The person's email, a label only, never mailed"),
		"invite_valid":    eProp("How long the server invite works", "1h", "24h", "7d"),
		"claim_valid":     eProp("How long a person's claim link works", "1h", "24h", "7d"),
	}

	refreshFields = map[string]any{
		"schedule":      eProp("When runs happen", "off", "weekly", "biweekly", "monthly", "days"),
		"every_days":    iProp("Days between runs with schedule days"),
		"hour":          iProp("UTC hour a scheduled run starts"),
		"scope":         eProp("Which providers are checked", "with_keys", "with_services", "all"),
		"include":       aProp(map[string]any{"type": "string"}, "Provider slugs always checked; replaces the list"),
		"exclude":       aProp(map[string]any{"type": "string"}, "Provider slugs never checked; replaces the list"),
		"include_peer":  bProp("Also check providers copied from another server"),
		"claude_prices": bProp("Also re-read Anthropic's model prices"),
		"apply":         eProp("Publish changes on their own, or wait for review", "auto", "review"),
		"max_run_usd":   nProp("Spend cap per run, USD"),
		"max_month_usd": nProp("Spend cap per month, USD"),
		"max_providers": iProp("Most providers one run checks"),
		"stale_days":    iProp("Skip providers checked within this many days"),
	}

	mcpFields = map[string]any{
		"core":              bProp("Lease, report, renew and proxy tools"),
		"service_tools":     bProp("Tools generated from each provider's examples"),
		"insights":          bProp("Read only key insights"),
		"manage":            bProp("Key management tools for anyone holding the app's token"),
		"resources":         bProp("MCP resources"),
		"prompts":           bProp("MCP prompts"),
		"exclusive_default": bProp("get_key is exclusive unless the call says otherwise"),
		"max_result_kb":     iProp("Cap on tool result size, 1 to 2048"),
		"instructions":      sProp("Extra instructions sent to MCP clients"),
		"disabled_tools":    aProp(map[string]any{"type": "string"}, "Tool names to hide; replaces the list"),
	}
)

// ---- describe helpers for the confirm step ----

// ctlFind reads a list route and returns the row whose field equals id.
func ctlFind(c *ctlCtx, path, list, field, id string) (map[string]any, error) {
	code, out, msg := c.apiJSON(http.MethodGet, path, nil)
	if code >= 400 {
		return nil, errors.New(msg)
	}
	arr, _ := out[list].([]any)
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok && argStr(m, field) == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("not found: %s %s", field, id)
}

func describeIn(path, list, what, input, label string) func(c *ctlCtx, in map[string]any) (string, error) {
	return func(c *ctlCtx, in map[string]any) (string, error) {
		id := argStr(in, input)
		m, err := ctlFind(c, path, list, "id", id)
		if err != nil {
			return "", fmt.Errorf("no %s %s", what, id)
		}
		return fmt.Sprintf("delete %s %s (%s)", what, id, argStr(m, label)), nil
	}
}

// ---- the table ----

// ctlTools is filled in init: the tools call routes whose router serves
// /mcp/control, which reads this table.
var ctlTools []ctlTool

func init() { ctlTools = buildCtlTools() }

func ctlToolByName(name string) *ctlTool {
	for i := range ctlTools {
		if ctlTools[i].Name == name {
			return &ctlTools[i]
		}
	}
	return nil
}

func buildCtlTools() []ctlTool {
	m, a, sa := store.RoleMember, store.RoleAdmin, store.RoleSuperadmin
	get, post, put, patch, del := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete
	id := func(d string) map[string]any { return iProp(d) }

	tools := []ctlTool{
		// ---- overview ----
		{Name: "overview", Title: "Overview", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/overview",
			Desc: "Totals: services, keys by state, requests and rate limits in the last 24 hours, and recent events."},
		{Name: "usage_report", Title: "Usage report", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/usage",
			Desc:  "Requests, errors and spend per day, service and app over the last days, plus AI spend.",
			Props: map[string]any{"days": iProp("Days to cover, default 30")}, Query: []string{"days"}},
		{Name: "list_events", Title: "List events", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/events",
			Desc: "Recent request and health check events, newest first.",
			Props: map[string]any{
				"service": sProp("Only this service: id, slug or name"),
				"key_id":  iProp("Only this key"),
				"kind":    sProp("Event kind: proxy, lease, rate_limited, quota_exhausted, invalid, error or health"),
				"limit":   iProp("How many events"),
			},
			Query: []string{"service", "key_id", "kind", "limit"}, Rename: map[string]string{"service": "service_id"}},
		{Name: "settings_summary", Title: "Server settings", Group: "overview", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/settings",
			Desc: "The server's configuration from its environment, with secrets shown only as set or not set."},
		{Name: "whoami", Title: "Who am I", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/me",
			Desc: "The signed in user this connection acts as, with the role it has (the lower of the user's role and the token scope)."},

		// ---- services ----
		{Name: "list_services", Title: "List services", Group: "services", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/services",
			Desc: "Services with their provider, base URL, strategy, key counts and requests in the last 24 hours."},
		{Name: "get_service", Title: "Get a service", Group: "services", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/services/{service}",
			Desc: "One service's settings and key counts.", Props: map[string]any{"service": svcIn}, Required: []string{"service"}},
		{Name: "create_service", Title: "Create a service", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services",
			Desc:  "Create a service from a provider, using its defaults; any field given overrides them. list_providers shows the providers.",
			Props: props(serviceFields, map[string]any{"provider": sProp("Provider slug from list_providers")}), Required: []string{"provider"},
			Body: append(keysOf(serviceFields), "provider")},
		{Name: "update_service", Title: "Update a service", Group: "services", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/services/{service}",
			Desc:  "Change a service's settings. Only the fields you send change.",
			Props: props(serviceFields, map[string]any{"service": svcIn, "provider": sProp("Move the service to another provider")}), Required: []string{"service"},
			Body: append(keysOf(serviceFields), "provider")},
		{Name: "delete_service", Title: "Delete a service", Group: "services", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/services/{service}",
			Desc: "Delete a service and all of its keys.", Props: map[string]any{"service": svcIn}, Required: []string{"service"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				sv, err := c.s.findService(c.r.Context(), argStr(in, "service"))
				if err != nil {
					return "", err
				}
				code, out, msg := c.apiJSON(get, fmt.Sprintf("/api/admin/services/%d", sv.ID), nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				kc, _ := out["key_counts"].(map[string]any)
				n, _ := argNum(kc, "total")
				return fmt.Sprintf("delete service %s (%s) and its %d keys", sv.Slug, sv.Name, n), nil
			}},
		{Name: "service_breaker", Title: "Service breaker", Group: "services", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/services/{service}/breaker",
			Desc: "The circuit breaker of a service: whether Acnologia paused it after repeated provider failures.", Props: map[string]any{"service": svcIn}, Required: []string{"service"}},
		{Name: "reset_breaker", Title: "Reset a breaker", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/{service}/breaker/reset",
			Desc: "Close a service's circuit breaker so traffic flows again.", Props: map[string]any{"service": svcIn}, Required: []string{"service"}},
		{Name: "cache_stats", Title: "Cache stats", Group: "services", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/cache",
			Desc: "Response cache size and hit rates."},
		{Name: "purge_cache", Title: "Purge a cache", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/{service}/cache/purge",
			Desc: "Drop a service's cached responses.", Props: map[string]any{"service": svcIn}, Required: []string{"service"}},
		{Name: "check_service_keys", Title: "Check a service's keys", Group: "services", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/{service}/check", OpenWorld: true,
			Desc: "Run the provider health check on every key of a service: confirms keys work and refreshes quota.", Props: map[string]any{"service": svcIn}, Required: []string{"service"}},

		// ---- keys ----
		{Name: "list_keys", Title: "List keys", Group: "keys", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/keys",
			Desc: "Keys across services (never their secrets) with status, validity, quota, last use and, for admins, who they belong to.",
			Props: map[string]any{
				"service":  sProp("Only this service: id, slug or name"),
				"query":    sProp("Search label, hint, notes and account fields"),
				"validity": eProp("Only keys with this validity", "valid", "invalid", "unknown"),
				"sort":     eProp("Sort order", "quota", "validity", "label"),
			},
			Query: []string{"service", "query", "validity", "sort"}, Rename: map[string]string{"service": "service_id", "query": "q"}},
		{Name: "key_details", Title: "Key details", Group: "keys", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/keys/{key_id}/insights",
			Desc: "One key with its service, recent events and 14 days of activity. No secret.", Props: map[string]any{"key_id": id("Key id")}, Required: []string{"key_id"}},
		{Name: "add_keys", Title: "Add keys", Group: "keys", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/{service}/keys",
			Desc: "Add API keys to a service: keys as objects, or text holding pasted keys, a CSV, a .env file or a JSON array (both may be sent). Duplicates are skipped. New keys are health checked in the background.",
			Props: map[string]any{
				"service": svcIn,
				"keys":    aProp(map[string]any{"type": "object", "properties": props(keyInFields, map[string]any{"secret": sProp("The API key")}), "required": []string{"secret"}}, "One object per key"),
				"text":    sProp("Pasted keys in any common format; labels found in it are kept"),
			},
			Required: []string{"service"}, Body: []string{"keys", "text"}},
		{Name: "update_key", Title: "Update a key", Group: "keys", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/keys/{key_id}",
			Desc: "Change a key: label, status, tier, weight, priority, quota limit, concurrency, egress, account details, notes, or replace its secret. Only the fields you send change.",
			Props: props(keyInFields, map[string]any{
				"key_id": id("Key id"),
				"status": eProp("active hands the key out, disabled never does", "active", "disabled"),
				"secret": sProp("Replace the key with a new secret"),
			}),
			Required: []string{"key_id"}, Body: append(keysOf(keyInFields), "status", "secret")},
		{Name: "delete_key", Title: "Delete a key", Group: "keys", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/keys/{key_id}",
			Desc: "Delete a key permanently.", Props: map[string]any{"key_id": id("Key id")}, Required: []string{"key_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				code, out, msg := c.apiJSON(get, "/api/admin/keys/"+argStr(in, "key_id")+"/insights", nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				k, _ := out["key"].(map[string]any)
				sv, _ := out["service"].(map[string]any)
				return fmt.Sprintf("delete key %s (%s, %s) from service %s", argStr(in, "key_id"), argStr(k, "label"), argStr(k, "hint"), argStr(sv, "slug")), nil
			}},
		{Name: "check_key", Title: "Check a key", Group: "keys", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/keys/{key_id}/check", OpenWorld: true,
			Desc: "Run the provider health check on one key and update its status and quota.", Props: map[string]any{"key_id": id("Key id")}, Required: []string{"key_id"}},
		{Name: "check_all_keys", Title: "Check all keys", Group: "keys", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/keys/check-all", OpenWorld: true,
			Desc: "Run the provider health check on every key of every service."},
		{Name: "reset_key", Title: "Reset a key", Group: "keys", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/keys/{key_id}/reset",
			Desc: "Put a key back to active and clear its error and cooldown.", Props: map[string]any{"key_id": id("Key id")}, Required: []string{"key_id"}},

		// ---- apps ----
		{Name: "list_apps", Title: "List apps", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps",
			Desc: "Apps (consumers) with mode, allowed services, limits, token count and whether they are active. Tokens are never shown."},
		{Name: "create_app", Title: "Create an app", Group: "apps", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/apps",
			Desc:  "Create an app (a website, script or agent that calls services). The result holds its token, shown only this once: hand it to the user.",
			Props: appFields, Required: []string{"name"}, Body: keysOf(appFields)},
		{Name: "update_app", Title: "Update an app", Group: "apps", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/apps/{app_id}",
			Desc:  "Change an app's name, mode, allowed services, limits, or turn it on or off. Only the fields you send change. Apps backing a share are changed with the sharing tools.",
			Props: props(appFields, map[string]any{"app_id": id("App id")}), Required: []string{"app_id"}, Body: keysOf(appFields)},
		{Name: "delete_app", Title: "Delete an app", Group: "apps", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/apps/{app_id}",
			Desc: "Delete an app; its tokens stop working.", Props: map[string]any{"app_id": id("App id")}, Required: []string{"app_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				app, err := ctlFind(c, "/api/admin/apps", "apps", "id", argStr(in, "app_id"))
				if err != nil {
					return "", fmt.Errorf("no app %s", argStr(in, "app_id"))
				}
				return fmt.Sprintf("delete app %s (id %s); its %s tokens stop working", argStr(app, "name"), argStr(in, "app_id"), orDefault(argStr(app, "token_count"), "0")), nil
			}},
		{Name: "rotate_app_token", Title: "Rotate an app's tokens", Group: "apps", Kind: ctlDestructive, Role: a, Method: post, Path: "/api/admin/apps/{app_id}/rotate-token",
			Desc:  "Replace all of an app's tokens with one new token; every current token stops working at once. To add a token without breaking the others use create_app_token. The new token is in the result, shown once.",
			Props: map[string]any{"app_id": id("App id")}, Required: []string{"app_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				app, err := ctlFind(c, "/api/admin/apps", "apps", "id", argStr(in, "app_id"))
				if err != nil {
					return "", fmt.Errorf("no app %s", argStr(in, "app_id"))
				}
				return fmt.Sprintf("rotate the tokens of app %s: a new token is made and its %s current tokens stop working", argStr(app, "name"), orDefault(argStr(app, "token_count"), "0")), nil
			}},
		{Name: "list_app_tokens", Title: "List app tokens", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps/{app_id}/tokens",
			Desc: "An app's tokens: id, name, prefix, created, last used, expiry and whether revoked. Values are never shown.", Props: map[string]any{"app_id": id("App id")}, Required: []string{"app_id"}},
		{Name: "create_app_token", Title: "Create an app token", Group: "apps", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/apps/{app_id}/tokens",
			Desc:     "Add a named token to an app; its other tokens keep working. The token is in the result, shown once.",
			Props:    map[string]any{"app_id": id("App id"), "name": sProp("What the token is for, such as ci or staging"), "expires_in_days": iProp("Days until it expires; omit for no expiry")},
			Required: []string{"app_id", "name"}, Body: []string{"name", "expires_in_days"}},
		{Name: "revoke_app_token", Title: "Revoke an app token", Group: "apps", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/apps/{app_id}/tokens/{token_id}",
			Desc:  "Revoke one token of an app; it stops working at once and the app's other tokens keep working.",
			Props: map[string]any{"app_id": id("App id"), "token_id": id("Token id from list_app_tokens")}, Required: []string{"app_id", "token_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				t, err := ctlFind(c, "/api/admin/apps/"+argStr(in, "app_id")+"/tokens", "tokens", "id", argStr(in, "token_id"))
				if err != nil {
					return "", fmt.Errorf("app %s has no token %s", argStr(in, "app_id"), argStr(in, "token_id"))
				}
				return fmt.Sprintf("revoke token %s (%s...) of app %s; it stops working at once", argStr(t, "name"), argStr(t, "prefix"), argStr(in, "app_id")), nil
			}},
		{Name: "app_usage", Title: "App usage", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps/{app_id}/usage",
			Desc:  "An app's requests and spend today and this month, its limits, whether a limit is hit, and daily history.",
			Props: map[string]any{"app_id": id("App id"), "days": iProp("Days of history, 1 to 366")}, Required: []string{"app_id"}, Query: []string{"days"}},
		{Name: "get_app_mcp", Title: "App MCP settings", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps/{app_id}/mcp",
			Desc: "An app's MCP server: which tool sets are on, every tool with whether it is enabled, and its /mcp endpoint URL.", Props: map[string]any{"app_id": id("App id")}, Required: []string{"app_id"}},
		{Name: "update_app_mcp", Title: "Update app MCP settings", Group: "apps", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/apps/{app_id}/mcp",
			Desc:  "Change an app's MCP server: tool sets, resources, prompts, exclusive leases, result size, instructions or hidden tools. Only the fields you send change.",
			Props: props(mcpFields, map[string]any{"app_id": id("App id")}), Required: []string{"app_id"},
			Prepare: func(c *ctlCtx, in map[string]any, path string) (any, error) {
				return c.merge(path, "settings", in, keysOf(mcpFields))
			}},

		// ---- providers ----
		{Name: "list_providers", Title: "List providers", Group: "providers", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/providers",
			Desc: "Providers Acnologia knows (built in, manual, AI built and copied from other servers), with how many services use each."},
		{Name: "get_provider", Title: "Get a provider", Group: "providers", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/providers/{slug}",
			Desc: "One provider with its full spec (auth, health check, quota, examples, offers) and version history.", Props: map[string]any{"slug": sProp("Provider slug")}, Required: []string{"slug"}},
		{Name: "provider_guide", Title: "Provider usage guide", Group: "providers", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/providers/{slug}/guide",
			Desc:  "How to call a provider through Acnologia, as Markdown and as the structured guide: endpoints with params, example bodies, what the response holds, limits, pricing and how errors are handled. Read it before call_service on a provider you have not used. generated is true when the provider has no written guide and this one was built from its examples. Pass service to fill in that service's proxy URL.",
			Props: map[string]any{"slug": sProp("Provider slug"), "service": sProp("Optional service slug built on this provider")}, Required: []string{"slug"}, Query: []string{"service"}},
		{Name: "write_provider_guide", Title: "Write a provider guide with AI", Group: "providers", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/providers/{slug}/guide/generate", OpenWorld: true,
			Desc:  "Have the AI write or rewrite a provider's usage guide from its docs, examples and offers, and save it as a new provider version (only the guide changes; roll back with rollback_provider). Takes up to a few minutes. Use it after adding a provider without a guide, or when a guide is thin or out of date.",
			Props: map[string]any{"slug": sProp("Provider slug"), "instruction": sProp("Optional: what to focus on or fix, under 4,000 characters")}, Required: []string{"slug"}, Body: []string{"instruction"}},
		{Name: "validate_provider", Title: "Validate a provider", Group: "providers", Kind: ctlRead, Role: m, Method: post, Path: "/api/admin/providers/validate",
			Desc:  "Check a provider definition without saving it. Returns the problems found and the fixture test report. Include spec.guide (summary plus the main endpoints with params and an example body).",
			Props: providerFields, Required: []string{"spec"}, Body: keysOf(providerFields)},
		{Name: "create_provider", Title: "Create a provider", Group: "providers", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/providers",
			Desc:  "Publish a new provider from a full definition. It is validated and its fixtures run first. Always write spec.guide: a summary plus the main endpoints with their params and an example body. To have the AI write a provider, use start_provider_build.",
			Props: props(providerFields, map[string]any{"note": sProp("Version note")}), Required: []string{"slug", "name", "spec"}, Body: append(keysOf(providerFields), "note")},
		{Name: "update_provider", Title: "Update a provider", Group: "providers", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/providers/{slug}",
			Desc:  "Publish a new version of a provider. Send the whole definition (read get_provider first); the slug never changes. Keep spec.guide, and write one if it is missing: a summary plus the main endpoints with their params and an example body.",
			Props: props(providerFields, map[string]any{"note": sProp("Version note")}), Required: []string{"slug", "name", "spec"}, Body: append(keysOf(providerFields, "slug"), "note")},
		{Name: "delete_provider", Title: "Delete a provider", Group: "providers", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/providers/{slug}",
			Desc: "Delete a provider no service uses.", Props: map[string]any{"slug": sProp("Provider slug")}, Required: []string{"slug"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				p, err := ctlFind(c, "/api/admin/providers", "providers", "slug", argStr(in, "slug"))
				if err != nil {
					return "", fmt.Errorf("no provider %s", argStr(in, "slug"))
				}
				return fmt.Sprintf("delete provider %s (%s)", argStr(in, "slug"), argStr(p, "name")), nil
			}},
		{Name: "rollback_provider", Title: "Roll back a provider", Group: "providers", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/providers/{slug}/rollback",
			Desc:  "Publish an earlier version of a provider again. get_provider lists the versions.",
			Props: map[string]any{"slug": sProp("Provider slug"), "version": iProp("Version number to go back to")}, Required: []string{"slug", "version"}, Body: []string{"version"}},
		{Name: "live_check_provider", Title: "Live check a provider", Group: "providers", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/providers/{slug}/live-check", OpenWorld: true,
			Desc:  "Call the real provider with a key to confirm the provider's auth and health check work.",
			Props: map[string]any{"slug": sProp("Provider slug"), "secret": sProp("An API key for the provider")}, Required: []string{"slug", "secret"}, Body: []string{"secret"}},
		{Name: "fetch_provider_logo", Title: "Fetch a provider logo", Group: "providers", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/providers/{slug}/logo/fetch", OpenWorld: true,
			Desc:  "Download a provider's logo from an image URL or from a website (the provider's own website when no url is given).",
			Props: map[string]any{"slug": sProp("Provider slug"), "url": sProp("Image or website URL")}, Required: []string{"slug"}, Body: []string{"url"}},
		{Name: "check_provider_offers", Title: "Check a provider's offers", Group: "providers", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/providers/{slug}/refresh", OpenWorld: true,
			Desc: "Re-check one provider's offers (free tiers, prices, limits) against its pricing pages now, with AI. Returns the refresh run.", Props: map[string]any{"slug": sProp("Provider slug")}, Required: []string{"slug"}},

		// ---- call ----
		{Name: "call_service", Title: "Call a service", Group: "call", Kind: ctlWrite, Role: a, OpenWorld: true,
			Desc: "Call a provider API (search, LLM, voice and the rest) through Acnologia's proxy: it picks a pooled key, injects it, rotates on rate limits and records usage. The key is never shown. Calls run as the app \"Control MCP\", made on first use, so its limits and usage apply. Read service_guide first for the endpoints and params; list_service_examples shows ready to use arguments.",
			Props: map[string]any{
				"service": svcIn,
				"method":  eProp("HTTP method, default GET", "GET", "POST", "PUT", "PATCH", "DELETE"),
				"path":    sProp("Path on the provider, such as /v1/search"),
				"query":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters"},
				"body":    map[string]any{"description": "JSON body for POST, PUT or PATCH"},
				"headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Extra request headers; the key header is set by Acnologia"},
			},
			Required: []string{"service", "path"}, Run: ctlCallService},
		{Name: "service_guide", Title: "Service usage guide", Group: "call", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/services/{service}/guide",
			Desc:  "How to call one service: its real proxy URL, endpoints with params and example bodies, response fields, limits and error handling, as Markdown and as the structured guide. Read it before the first call_service on a service.",
			Props: map[string]any{"service": svcIn}, Required: []string{"service"}},
		{Name: "list_service_examples", Title: "Service examples", Group: "call", Kind: ctlRead, Role: m,
			Desc:  "A service's documented example calls, each with ready to use call_service arguments, plus its quota unit and offers.",
			Props: map[string]any{"service": svcIn}, Required: []string{"service"}, Run: ctlServiceExamples},

		// ---- ai ----
		{Name: "ai_status", Title: "AI status", Group: "ai", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/ai/status",
			Desc: "Whether AI features are on, the model, and spend."},
		{Name: "ai_models", Title: "AI models", Group: "ai", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/ai/models",
			Desc: "The Claude model each AI feature uses, the choices and their prices."},
		{Name: "ai_assist", Title: "Fill a form with AI", Group: "ai", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/ai/assist", OpenWorld: true,
			Desc: "Ask Claude to fill an app, service or provider form from one sentence. Returns suggested fields; nothing is saved.",
			Props: map[string]any{
				"form":        eProp("Which form", "app", "service", "provider"),
				"instruction": sProp("What you want, in a sentence"),
				"current":     map[string]any{"type": "object", "description": "Current field values to keep where they fit"},
			},
			Required: []string{"form", "instruction"}, Body: []string{"form", "instruction", "current"}},
		{Name: "start_provider_build", Title: "Start a provider build", Group: "ai", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/ai/runs", OpenWorld: true,
			Desc: "Start the AI provider builder: it reads the docs, writes and tests a provider, and (for create_provider) publishes it with a service. Poll get_ai_run with the run id. Check list_providers first: a built in provider is better.",
			Props: map[string]any{
				"kind":           eProp("Build a new provider or review an existing one", "create_provider", "update_provider"),
				"provider_slug":  sProp("The provider to review, for update_provider"),
				"context":        sProp("The API or company name and anything known about it"),
				"urls":           aProp(map[string]any{"type": "string"}, "Docs or website URLs"),
				"github":         sProp("A GitHub repository of an SDK or the API"),
				"test_key":       sProp("An API key for the live check"),
				"test_keys":      aProp(map[string]any{"type": "string"}, "More keys; all are added to the service a publish creates"),
				"auto_publish":   bProp("Publish without review (default true for create_provider, false for update_provider)"),
				"create_service": bProp("Also create a service when publishing a new provider"),
			},
			Required: []string{"kind"}, Body: []string{"kind", "provider_slug", "context", "urls", "github", "test_key", "test_keys", "auto_publish", "create_service"}},
		{Name: "list_ai_runs", Title: "List AI runs", Group: "ai", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/ai/runs",
			Desc: "Recent AI provider builder runs with their status."},
		{Name: "get_ai_run", Title: "Get an AI run", Group: "ai", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/ai/runs/{run_id}",
			Desc:  "One AI run with its steps: status, result, error and what it published.",
			Props: map[string]any{"run_id": id("Run id"), "after": iProp("Only steps after this step id")}, Required: []string{"run_id"}, Query: []string{"after"}},
		{Name: "cancel_ai_run", Title: "Cancel an AI run", Group: "ai", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/ai/runs/{run_id}/cancel",
			Desc: "Stop a running AI build.", Props: map[string]any{"run_id": id("Run id")}, Required: []string{"run_id"}},
		{Name: "publish_ai_run", Title: "Publish an AI run", Group: "ai", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/ai/runs/{run_id}/publish",
			Desc: "Publish the provider an AI run wrote and is waiting for review.", Props: map[string]any{"run_id": id("Run id")}, Required: []string{"run_id"}},
		{Name: "delete_ai_run", Title: "Delete an AI run", Group: "ai", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/ai/runs/{run_id}",
			Desc: "Delete an AI run and its log.", Props: map[string]any{"run_id": id("Run id")}, Required: []string{"run_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				code, out, msg := c.apiJSON(get, "/api/admin/ai/runs/"+argStr(in, "run_id"), nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				run, _ := out["run"].(map[string]any)
				return fmt.Sprintf("delete AI run %s (%s, %s)", argStr(in, "run_id"), argStr(run, "title"), argStr(run, "status")), nil
			}},

		// ---- routing ----
		{Name: "list_webhooks", Title: "List webhooks", Group: "routing", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/webhooks",
			Desc: "Webhooks that notify other systems of key and service events, with the event types and formats to choose from."},
		{Name: "create_webhook", Title: "Create a webhook", Group: "routing", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/webhooks",
			Desc:  "Create a webhook. Its signing secret is in the result, shown once.",
			Props: webhookFields, Required: []string{"name", "url"}, Body: keysOf(webhookFields)},
		{Name: "update_webhook", Title: "Update a webhook", Group: "routing", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/webhooks/{webhook_id}",
			Desc:  "Change a webhook's name, URL, format or events, or turn it on or off. Only the fields you send change.",
			Props: props(webhookFields, map[string]any{"webhook_id": id("Webhook id")}), Required: []string{"webhook_id"}, Body: keysOf(webhookFields)},
		{Name: "delete_webhook", Title: "Delete a webhook", Group: "routing", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/webhooks/{webhook_id}",
			Desc: "Delete a webhook.", Props: map[string]any{"webhook_id": id("Webhook id")}, Required: []string{"webhook_id"},
			Describe: describeIn("/api/admin/webhooks", "webhooks", "webhook", "webhook_id", "name")},
		{Name: "test_webhook", Title: "Test a webhook", Group: "routing", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/webhooks/{webhook_id}/test", OpenWorld: true,
			Desc: "Send a test event to a webhook and report the result.", Props: map[string]any{"webhook_id": id("Webhook id")}, Required: []string{"webhook_id"}},
		{Name: "list_egress", Title: "List egress proxies", Group: "routing", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/egress",
			Desc: "Egress proxies that services or keys send their traffic through. URLs show only as hints."},
		{Name: "create_egress", Title: "Create an egress proxy", Group: "routing", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/egress",
			Desc:  "Add an egress proxy that services or keys can route through.",
			Props: egressFields, Required: []string{"name", "url"}, Body: keysOf(egressFields)},
		{Name: "update_egress", Title: "Update an egress proxy", Group: "routing", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/egress/{proxy_id}",
			Desc:  "Change an egress proxy. Only the fields you send change.",
			Props: props(egressFields, map[string]any{"proxy_id": id("Egress proxy id")}), Required: []string{"proxy_id"}, Body: keysOf(egressFields)},
		{Name: "delete_egress", Title: "Delete an egress proxy", Group: "routing", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/egress/{proxy_id}",
			Desc: "Delete an egress proxy.", Props: map[string]any{"proxy_id": id("Egress proxy id")}, Required: []string{"proxy_id"},
			Describe: describeIn("/api/admin/egress", "proxies", "egress proxy", "proxy_id", "name")},
		{Name: "test_egress", Title: "Test an egress proxy", Group: "routing", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/egress/{proxy_id}/test", OpenWorld: true,
			Desc: "Check an egress proxy works and learn its public IP.", Props: map[string]any{"proxy_id": id("Egress proxy id")}, Required: []string{"proxy_id"}},
		{Name: "list_groups", Title: "List service groups", Group: "routing", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/groups",
			Desc: "Service groups: several services behind one name, tried in order as fallbacks."},
		{Name: "create_group", Title: "Create a service group", Group: "routing", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/groups",
			Desc:  "Create a service group: one slug apps call that falls back across several services in order.",
			Props: groupFields, Required: []string{"slug", "name", "members"}, Body: keysOf(groupFields)},
		{Name: "update_group", Title: "Update a service group", Group: "routing", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/groups/{group_id}",
			Desc:  "Change a service group. Only the fields you send change; members replaces the list.",
			Props: props(groupFields, map[string]any{"group_id": id("Group id")}), Required: []string{"group_id"}, Body: keysOf(groupFields)},
		{Name: "delete_group", Title: "Delete a service group", Group: "routing", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/groups/{group_id}",
			Desc: "Delete a service group. Its services stay.", Props: map[string]any{"group_id": id("Group id")}, Required: []string{"group_id"},
			Describe: describeIn("/api/admin/groups", "groups", "service group", "group_id", "slug")},

		// ---- sharing ----
		{Name: "get_identity", Title: "Server identity", Group: "sharing", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/identity",
			Desc: "This server's sharing identity: name, public URL and fingerprint."},
		{Name: "update_identity", Title: "Update server identity", Group: "sharing", Kind: ctlWrite, Role: sa, Method: patch, Path: "/api/admin/identity",
			Desc:  "Change the name and public URL other servers see.",
			Props: map[string]any{"name": sProp("Server name"), "url": sProp("Public URL other servers reach this one at")}, Body: []string{"name", "url"}},
		{Name: "list_shares", Title: "List shares", Group: "sharing", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/shares",
			Desc: "Shares with other servers and with people: status, services, mode, timer and usage."},
		{Name: "get_share", Title: "Get a share", Group: "sharing", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/shares/{share_id}",
			Desc: "One share with usage per service, key counts, handover and revealed keys.", Props: map[string]any{"share_id": sProp("Share id")}, Required: []string{"share_id"}},
		{Name: "create_share", Title: "Create a share", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/shares",
			Desc:  "Share services (and provider definitions) with another server, which gets an invite code, or with a person (recipient person), who gets a claim link. Both are in the result.",
			Props: shareFields, Required: []string{"name"}, Body: keysOf(shareFields)},
		{Name: "update_share", Title: "Update a share", Group: "sharing", Kind: ctlWrite, Role: sa, Method: patch, Path: "/api/admin/shares/{share_id}",
			Desc:  "Change a share's services, limits, key scopes, timer or note. Only the fields you send change.",
			Props: props(shareFields, map[string]any{"share_id": sProp("Share id")}), Required: []string{"share_id"}, Body: keysOf(shareFields, "recipient", "invite_valid", "claim_valid")},
		{Name: "pause_share", Title: "Pause a share", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/shares/{share_id}/pause",
			Desc: "Pause an active share; its token stops working until resumed.", Props: map[string]any{"share_id": sProp("Share id")}, Required: []string{"share_id"}},
		{Name: "resume_share", Title: "Resume a share", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/shares/{share_id}/resume",
			Desc: "Resume a paused share.", Props: map[string]any{"share_id": sProp("Share id")}, Required: []string{"share_id"}},
		{Name: "revoke_share", Title: "Revoke a share", Group: "sharing", Kind: ctlDestructive, Role: sa, Method: post, Path: "/api/admin/shares/{share_id}/revoke", OpenWorld: true,
			Desc: "Revoke a share for good. A handover share also tells the receiver to delete the keys.", Props: map[string]any{"share_id": sProp("Share id")}, Required: []string{"share_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				code, out, msg := c.apiJSON(get, "/api/admin/shares/"+argStr(in, "share_id"), nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				who := argStr(out, "peer_name")
				if argStr(out, "recipient") == "person" {
					who = argStr(out, "label")
				}
				return fmt.Sprintf("revoke share %s (%s, %s mode, status %s, receiver %s) for good", argStr(out, "name"), argStr(in, "share_id"), argStr(out, "mode"), argStr(out, "status"), orDefault(who, "not redeemed yet")), nil
			}},
		{Name: "extend_share", Title: "Extend a share", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/shares/{share_id}/extend",
			Desc:  "Move a share's end time, or clear it.",
			Props: map[string]any{"share_id": sProp("Share id"), "ends_at": sProp("New RFC 3339 end time; empty for no end")}, Required: []string{"share_id", "ends_at"}, Body: []string{"ends_at"}},
		{Name: "share_invite", Title: "New share invite", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/shares/{share_id}/invite",
			Desc:  "Issue a new invite code for a share with another server; older codes stop working.",
			Props: map[string]any{"share_id": sProp("Share id"), "invite_valid": eProp("How long it works", "1h", "24h", "7d")}, Required: []string{"share_id"}, Body: []string{"invite_valid"}},
		{Name: "share_claim_link", Title: "New claim link", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/shares/{share_id}/claim-link",
			Desc:  "Issue a new claim link for a share with a person; older links stop working.",
			Props: map[string]any{"share_id": sProp("Share id"), "valid": eProp("How long it works", "1h", "24h", "7d")}, Required: []string{"share_id"}, Body: []string{"valid"}},
		{Name: "list_peers", Title: "List connected servers", Group: "sharing", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/peers",
			Desc: "Servers that share with this one, with their services and handed over keys."},
		{Name: "preview_peer", Title: "Preview an invite", Group: "sharing", Kind: ctlRead, Role: sa, Method: post, Path: "/api/admin/peers/preview", OpenWorld: true,
			Desc:  "Look at what an invite from another server offers, without connecting.",
			Props: map[string]any{"invite": sProp("Invite code from the other server"), "trusted": bProp("Allow a server on a private address")}, Required: []string{"invite"}, Body: []string{"invite", "trusted"}},
		{Name: "connect_peer", Title: "Connect a server", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/peers", OpenWorld: true,
			Desc: "Redeem an invite from another server: its shared services appear here.",
			Props: map[string]any{
				"invite":   sProp("Invite code"),
				"prefix":   sProp("Prefix for the local service slugs"),
				"trusted":  bProp("Allow a server on a private address"),
				"owner_id": sProp("The owner's server id from preview_peer, to pin it"),
			},
			Required: []string{"invite"}, Body: []string{"invite", "prefix", "trusted", "owner_id"}},
		{Name: "sync_peer", Title: "Sync a server", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/peers/{peer_id}/sync", OpenWorld: true,
			Desc: "Fetch a connected server's manifest again now.", Props: map[string]any{"peer_id": id("Connected server id from list_peers")}, Required: []string{"peer_id"}},
		{Name: "disconnect_peer", Title: "Disconnect a server", Group: "sharing", Kind: ctlDestructive, Role: sa, Method: del, Path: "/api/admin/peers/{peer_id}", OpenWorld: true,
			Desc: "Leave a server's share: its services and handed over keys go away here.", Props: map[string]any{"peer_id": id("Connected server id from list_peers")}, Required: []string{"peer_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				p, err := ctlFind(c, "/api/admin/peers", "peers", "id", argStr(in, "peer_id"))
				if err != nil {
					return "", fmt.Errorf("no connected server %s", argStr(in, "peer_id"))
				}
				n := 0
				if l, ok := p["local_services"].([]any); ok {
					n = len(l)
				}
				return fmt.Sprintf("disconnect from server %s (%s); its %d services here go away", argStr(p, "peer_name"), argStr(p, "peer_url"), n), nil
			}},
		{Name: "list_peer_providers", Title: "Connected server providers", Group: "sharing", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/peers/{peer_id}/providers",
			Desc: "Provider definitions a connected server shares, and whether each is here, edited or has an update.", Props: map[string]any{"peer_id": id("Connected server id")}, Required: []string{"peer_id"}},
		{Name: "pull_peer_provider", Title: "Pull a provider", Group: "sharing", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/peers/{peer_id}/providers/{slug}/pull", OpenWorld: true,
			Desc: "Copy or update a provider definition from a connected server.",
			Props: map[string]any{
				"peer_id": id("Connected server id"), "slug": sProp("Provider slug"),
				"allow_base_url_change": bProp("Accept a different base URL than the local copy has"),
			},
			Required: []string{"peer_id", "slug"}, Body: []string{"allow_base_url_change"}},

		// ---- refresh ----
		{Name: "refresh_settings", Title: "Offers refresh settings", Group: "refresh", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/providers/refresh/settings",
			Desc: "How provider offers are kept current: schedule, scope, budget, last and next run, pending reviews."},
		{Name: "update_refresh_settings", Title: "Update offers refresh", Group: "refresh", Kind: ctlWrite, Role: sa, Method: put, Path: "/api/admin/providers/refresh/settings",
			Desc:  "Change the offers refresh schedule, scope, apply mode or budget. Only the fields you send change.",
			Props: refreshFields,
			Prepare: func(c *ctlCtx, in map[string]any, path string) (any, error) {
				return c.merge(path, "settings", in, keysOf(refreshFields))
			}},
		{Name: "run_refresh", Title: "Run offers refresh", Group: "refresh", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/providers/refresh/run", OpenWorld: true,
			Desc: "Start an offers refresh run now over the providers in scope. Uses AI and counts against the budget."},
		{Name: "list_refresh_runs", Title: "List refresh runs", Group: "refresh", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/providers/refresh/runs",
			Desc: "Recent offers refresh runs with what they checked and changed."},
		{Name: "list_refresh_reviews", Title: "List refresh reviews", Group: "refresh", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/providers/refresh/reviews",
			Desc:  "Offers changes waiting for review (or all with status all).",
			Props: map[string]any{"status": sProp("pending (default), applied, dismissed or all")}, Query: []string{"status"}},
		{Name: "apply_refresh_review", Title: "Apply a refresh review", Group: "refresh", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/providers/refresh/reviews/{review_id}/apply",
			Desc: "Publish the offers change a review holds.", Props: map[string]any{"review_id": id("Review id")}, Required: []string{"review_id"}},
		{Name: "dismiss_refresh_review", Title: "Dismiss a refresh review", Group: "refresh", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/providers/refresh/reviews/{review_id}/dismiss",
			Desc: "Drop the offers change a review holds.", Props: map[string]any{"review_id": id("Review id")}, Required: []string{"review_id"}},

		// ---- admin ----
		{Name: "list_users", Title: "List users", Group: "admin", Kind: ctlRead, Role: sa, Method: get, Path: "/api/admin/users",
			Desc: "Dashboard users with their roles."},
		{Name: "create_user", Title: "Create a user", Group: "admin", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/users",
			Desc: "Create a dashboard user.",
			Props: map[string]any{
				"email": sProp("Email, used to sign in"), "name": sProp("Display name"),
				"password": sProp("Initial password"), "role": eProp("Role", "member", "admin", "superadmin"),
			},
			Required: []string{"email", "password", "role"}, Body: []string{"email", "name", "password", "role"}},
		{Name: "update_user", Title: "Update a user", Group: "admin", Kind: ctlWrite, Role: sa, Method: patch, Path: "/api/admin/users/{user_id}",
			Desc: "Change a user's name, role or password, or disable them. Only the fields you send change.",
			Props: map[string]any{
				"user_id": id("User id"), "name": sProp("Display name"), "role": eProp("Role", "member", "admin", "superadmin"),
				"disabled": bProp("Disable or enable sign in"), "password": sProp("New password"),
			},
			Required: []string{"user_id"}, Body: []string{"name", "role", "disabled", "password"}},
		{Name: "delete_user", Title: "Delete a user", Group: "admin", Kind: ctlDestructive, Role: sa, Method: del, Path: "/api/admin/users/{user_id}",
			Desc: "Delete a dashboard user.", Props: map[string]any{"user_id": id("User id")}, Required: []string{"user_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				u, err := ctlFind(c, "/api/admin/users", "users", "id", argStr(in, "user_id"))
				if err != nil {
					return "", fmt.Errorf("no user %s", argStr(in, "user_id"))
				}
				return fmt.Sprintf("delete user %s (%s)", argStr(u, "email"), argStr(u, "role")), nil
			}},
		{Name: "list_audit", Title: "Audit log", Group: "admin", Kind: ctlRead, Role: sa, Method: get, Path: "/api/admin/audit",
			Desc: "Who changed what, newest first.", Props: map[string]any{"limit": iProp("How many entries")}, Query: []string{"limit"}},

		// ---- secrets ----
		{Name: "reveal_key", Title: "Reveal a key", Group: "secrets", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/keys/{key_id}/reveal",
			Desc: "Return a key's secret. Audited. Only use it when the user asks for the secret.", Props: map[string]any{"key_id": id("Key id")}, Required: []string{"key_id"}},

		// ---- raw ----
		{Name: "api_request", Title: "Call the admin API", Group: "raw", Kind: ctlDestructive, Role: m, Method: "", Path: "",
			Desc: "Call any /api/admin/... route not covered by another tool, as the signed in user. /api/admin/tokens and PATCH /api/admin/me are refused. DELETE needs confirm.",
			Props: map[string]any{
				"method": eProp("HTTP method", "GET", "POST", "PUT", "PATCH", "DELETE"),
				"path":   sProp("Path starting with /api/admin/"),
				"query":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters"},
				"body":   map[string]any{"description": "JSON body for POST, PUT or PATCH"},
			},
			Required: []string{"method", "path"}, Run: ctlRawRequest,
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				return "send " + strings.ToUpper(argStr(in, "method")) + " " + argStr(in, "path"), nil
			}},
	}
	return tools
}
