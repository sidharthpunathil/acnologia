package api

// Control MCP tools for the access controls (per-token limits, tag budgets,
// deny rules, usage by tag, the server spend ceiling), background jobs, the
// demo data and the Try it panel. Each is one route, so roles, validation
// and audit stay with the routes in app_controls.go, jobs.go, demo.go and
// tryit.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

var (
	// tokenLimitsIn is a token's own limits, or an app's per-token default.
	tokenLimitsIn = map[string]any{"type": "object",
		"description": "Daily and monthly request caps and USD budgets; a missing or null field means no limit of its own, and {} clears them all",
		"properties": map[string]any{
			"daily_requests": iProp("Requests a day (UTC)"), "monthly_requests": iProp("Requests a month (UTC)"),
			"daily_budget": nProp("USD a day"), "monthly_budget": nProp("USD a month"),
		}}
	pinnedTagsIn = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
		"description": "Up to 5 caller tags the token carries on every call, such as {\"customer\": \"cust_A\"}; a call that sends another value for a pinned key is refused with tag_pinned. {} clears them"}
	tagBudgetIn = map[string]any{"type": "object", "required": []string{"key"}, "properties": map[string]any{
		"key":            sProp("Caller tag key, such as customer: each value of it gets these limits on its own"),
		"daily_requests": iProp("Requests a day per value"), "monthly_requests": iProp("Requests a month per value"),
		"daily_budget": nProp("USD a day per value"), "monthly_budget": nProp("USD a month per value"),
	}}
	denyRuleFields = map[string]any{
		"method":      sProp("HTTP method, or * for any (the default)"),
		"service":     sProp("A service, group or capability slug the rule covers; leave out with host for every target"),
		"host":        sProp("An upstream hostname the rule covers, for the forward proxy and /proxy calls to that host; not with service"),
		"path_prefix": sProp("Path the rule covers, with everything under it on whole segments; a trailing * makes it a plain string prefix"),
		"note":        sProp("Why the rule exists"),
	}
	asyncIn = bProp("Return at once with a job id instead of waiting: read the result with get_job, or watch for the job.done event")
)

// ctlAsyncTools are the tools whose route takes ?async=1.
var ctlAsyncTools = map[string]bool{"check_all_keys": true, "check_service_keys": true, "sync_service_mcp": true, "sync_peer": true}

// withAsync adds the async input to the tools whose route runs as a job.
func withAsync(tools []ctlTool) {
	for i := range tools {
		t := &tools[i]
		if !ctlAsyncTools[t.Name] {
			continue
		}
		t.Props = props(t.Props, map[string]any{"async": asyncIn})
		t.Query = append(t.Query, "async")
		t.Desc += " With async: true it answers at once with {job_id, status_url}; get_job reads the result."
	}
}

func ctlAgentTools() []ctlTool {
	m, a, sa := store.RoleMember, store.RoleAdmin, store.RoleSuperadmin
	get, post, put, patch, del := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete
	appIn := iProp("App id")

	return []ctlTool{
		// ---- apps: access controls ----
		{Name: "app_controls", Title: "App access controls", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps/{app_id}/controls",
			Desc:  "An app's finer access controls in one read: the per-token default limits, the tag budgets and the deny rules.",
			Props: map[string]any{"app_id": appIn}, Required: []string{"app_id"}},
		{Name: "set_token_limits", Title: "Set per-token default limits", Group: "apps", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/apps/{app_id}/token-limits",
			Desc: "Set the limits every token of an app gets unless the token sets that field itself, so each agent or each person who claimed a share link has a cap of its own inside the app's. " +
				"Replaces the default; {} removes it. Works on a share's backing app too. A token's own limits are set with create_app_token or update_app_token.",
			Props: map[string]any{"app_id": appIn, "limits": tokenLimitsIn}, Required: []string{"app_id", "limits"}, Body: []string{"limits"}},
		{Name: "set_tag_budgets", Title: "Set tag budgets", Group: "apps", Kind: ctlWrite, Role: a, Method: put, Path: "/api/admin/apps/{app_id}/tag-budgets",
			Desc: "Replace an app's tag budgets: limits for each value of a caller tag key (X-Acnologia-Meta), such as $2 a day for every customer. One budget per key, at most 10, each with at least one limit. " +
				"An empty list removes them all. A request without the key is not under its budget; pin the tag on the token (update_app_token pinned_tags) so a caller cannot skip it.",
			Props: map[string]any{"app_id": appIn, "budgets": aProp(tagBudgetIn, "The budgets; replaces the list")}, Required: []string{"app_id", "budgets"}, Body: []string{"budgets"}},
		{Name: "tag_usage", Title: "Usage by caller tag", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps/{app_id}/usage/tags",
			Desc:  "An app's requests and spend per caller tag value (X-Acnologia-Meta), most expensive first, with its tag budgets: what each of the app's own customers used.",
			Props: map[string]any{"app_id": appIn, "days": iProp("Days of history, 1 to 366, default 30")}, Required: []string{"app_id"}, Query: []string{"days"}},
		{Name: "list_deny_rules", Title: "List deny rules", Group: "apps", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/apps/{app_id}/deny-rules",
			Desc:  "An app's deny rules: each blocks the app's matching requests (method, service or host, path prefix) with 403 denied_by_rule, whatever its other access allows.",
			Props: map[string]any{"app_id": appIn}, Required: []string{"app_id"}},
		{Name: "add_deny_rule", Title: "Add a deny rule", Group: "apps", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/apps/{app_id}/deny-rules",
			Desc: "Block an app's requests that match a method, a target (service, group or capability slug, or an upstream host, or neither for every target) and a path prefix. " +
				"It applies to /proxy, the forward proxy, capabilities, provider rotation and upstream MCP tools (as POST /tools/<tool name>). Returns the rule as stored. At most 100 rules an app.",
			Props: props(denyRuleFields, map[string]any{"app_id": appIn}), Required: []string{"app_id"}, Body: keysOf(denyRuleFields)},
		{Name: "delete_deny_rule", Title: "Delete a deny rule", Group: "apps", Kind: ctlDestructive, Role: a, Method: del, Path: "/api/admin/apps/{app_id}/deny-rules/{rule_id}",
			Desc:  "Remove one deny rule of an app; the requests it blocked go through again.",
			Props: map[string]any{"app_id": appIn, "rule_id": iProp("Rule id from list_deny_rules")}, Required: []string{"app_id", "rule_id"},
			Describe: func(c *ctlCtx, in map[string]any) (string, error) {
				rl, err := ctlFind(c, "/api/admin/apps/"+argStr(in, "app_id")+"/deny-rules", "rules", "id", argStr(in, "rule_id"))
				if err != nil {
					return "", fmt.Errorf("app %s has no deny rule %s", argStr(in, "app_id"), argStr(in, "rule_id"))
				}
				return fmt.Sprintf("delete deny rule %s (%s) of app %s; the requests it blocks go through again", argStr(in, "rule_id"), describeDenyRow(rl), argStr(in, "app_id")), nil
			}},
		{Name: "update_app_token", Title: "Update an app token", Group: "apps", Kind: ctlWrite, Role: a, Method: patch, Path: "/api/admin/apps/{app_id}/tokens/{token_id}",
			Desc: "Change one token of an app: its name, its own limits, or its pinned caller tags. Only the fields you send change; limits {} or pinned_tags {} clears them. " +
				"A limit the token leaves unset comes from the app's per-token default (set_token_limits).",
			Props: map[string]any{"app_id": appIn, "token_id": iProp("Token id from list_app_tokens"), "name": sProp("New name"),
				"limits": tokenLimitsIn, "pinned_tags": pinnedTagsIn},
			Required: []string{"app_id", "token_id"}, Body: []string{"name", "limits", "pinned_tags"}},
		{Name: "get_spend_ceiling", Title: "Server spend ceiling", Group: "apps", Kind: ctlRead, Role: a, Method: get, Path: "/api/admin/settings/spend-ceiling",
			Desc: "The server's daily spend ceiling in USD across every app, where it comes from (env or saved), and what the server spent today (UTC). At the ceiling every metered call is refused until midnight UTC."},
		{Name: "set_spend_ceiling", Title: "Set the server spend ceiling", Group: "apps", Kind: ctlWrite, Role: sa, Method: put, Path: "/api/admin/settings/spend-ceiling",
			Desc:    "Save the server's daily spend ceiling: usd in dollars, 0 for no ceiling, or reset: true to go back to ACNOLOGIA_DAILY_SPEND_CEILING_USD.",
			Props:   map[string]any{"usd": nProp("Dollars a day across every app; 0 turns the ceiling off"), "reset": bProp("Drop the saved ceiling and use the environment's")},
			BodyDoc: map[string]any{"usd": map[string]any{"type": []string{"number", "null"}, "description": "Dollars a day, 0 for none, null for the environment's"}},
			Prepare: func(_ *ctlCtx, in map[string]any, _ string) (any, error) {
				if reset, _ := in["reset"].(bool); reset {
					return map[string]any{"usd": nil}, nil
				}
				v, ok := in["usd"]
				if !ok || v == nil {
					return nil, errors.New("usd is required, or reset: true")
				}
				return map[string]any{"usd": v}, nil
			}},

		// ---- overview: jobs ----
		{Name: "list_jobs", Title: "List jobs", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/jobs",
			Desc: "Background jobs, newest first, without their results: checks of every key, a service's keys or new keys, peer syncs and MCP syncs, each with status (queued, running, done, failed) and progress. " +
				"Your own jobs; a superadmin sees everyone's unless mine is true.",
			Props: map[string]any{
				"kind":   sProp("Only this kind: keys.check_all, keys.check, keys.check_new, peer.sync or service.mcp_sync"),
				"status": eProp("Only jobs with this status", "queued", "running", "done", "failed"),
				"limit":  iProp("Most jobs, up to 200"),
				"mine":   bProp("Only jobs you started (for a superadmin)"),
			},
			Query: []string{"kind", "status", "limit", "mine"}},
		{Name: "get_job", Title: "Get a job", Group: "overview", Kind: ctlRead, Role: m, Method: get, Path: "/api/admin/jobs/{job_id}",
			Desc:  "One background job: status, progress (done of total), and when it ended the result the synchronous call would have answered, or the error. Poll it after a tool called with async: true.",
			Props: map[string]any{"job_id": iProp("Job id")}, Required: []string{"job_id"}},

		// ---- services: demo data ----
		{Name: "demo_status", Title: "Demo data", Group: "services", Kind: ctlRead, Role: sa, Method: get, Path: "/api/admin/demo",
			Desc: "Whether demo data is loaded, and the service and app it made."},
		{Name: "load_demo", Title: "Load demo data", Group: "services", Kind: ctlWrite, Role: sa, Method: post, Path: "/api/admin/demo",
			Desc: "Add a demo service backed by a fake provider this server answers itself, two keys, an app and its token, so a first call through the proxy works before any real key exists. " +
				"The token is in the result, shown once. Refused on a server that has services unless force is true.",
			Props: map[string]any{"force": bProp("Load it even though the server has services")}, Body: []string{"force"}},
		{Name: "remove_demo", Title: "Remove demo data", Group: "services", Kind: ctlDestructive, Role: sa, Method: del, Path: "/api/admin/demo",
			Desc: "Delete exactly what load_demo made: the demo service with its keys, and the demo app with its tokens.",
			Describe: func(c *ctlCtx, _ map[string]any) (string, error) {
				code, out, msg := c.apiJSON(get, "/api/admin/demo", nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				if out["loaded"] != true {
					return "", errors.New("no demo data is loaded")
				}
				return fmt.Sprintf("remove the demo data: service %s with its keys, and app %s with its tokens", argStr(out, "service_slug"), argStr(out, "app_id")), nil
			}},

		// ---- call: try it ----
		{Name: "try_service", Title: "Try a service", Group: "call", Kind: ctlWrite, Role: a, Method: post, Path: "/api/admin/services/{service}/try", OpenWorld: true,
			Desc: "Send one request through the proxy the way an app's request goes, and see what came back: status, headers, latency and the body (keys scrubbed). " +
				"It runs as app_id (with that app's access and limits, read only when its token is) or the internal Try it app, and is logged as a try event. " +
				"call_service is the plain way to call a provider; this one is for checking what a given app would get.",
			Props: map[string]any{
				"service":   svcIn,
				"method":    eProp("HTTP method, default GET", "GET", "POST", "PUT", "PATCH", "DELETE"),
				"path":      sProp("Path on the provider, such as /v1/search"),
				"query":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Query parameters"},
				"body":      map[string]any{"description": "JSON body for POST, PUT or PATCH: an object, or JSON text"},
				"app_id":    iProp("Run as this app; leave out for the internal Try it app"),
				"read_only": bProp("Run as a read-only token would: GET only"),
			},
			Required: []string{"service", "path"},
			BodyDoc: map[string]any{"method": sProp("HTTP method"), "path": sProp("Path on the provider"), "query": map[string]any{"type": "object"},
				"body": sProp("JSON body as text"), "app_id": iProp("App to run as"), "read_only": bProp("GET only")},
			Prepare: func(_ *ctlCtx, in map[string]any, _ string) (any, error) {
				out := pick(in, "method", "path", "query", "app_id", "read_only")
				switch b := in["body"].(type) {
				case nil:
				case string:
					out["body"] = b
				default:
					raw, err := json.Marshal(b)
					if err != nil {
						return nil, fmt.Errorf("body: %w", err)
					}
					out["body"] = string(raw)
				}
				return out, nil
			}},
	}
}

// describeDenyRow is a deny rule row in one line, as the route describes it.
func describeDenyRow(r map[string]any) string {
	parts := []string{orDefault(argStr(r, "method"), "*")}
	switch {
	case argStr(r, "service") != "":
		parts = append(parts, argStr(r, "service"))
	case argStr(r, "host") != "":
		parts = append(parts, argStr(r, "host"))
	default:
		parts = append(parts, "any target")
	}
	if p := argStr(r, "path_prefix"); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}
