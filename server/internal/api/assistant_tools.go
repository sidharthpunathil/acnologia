package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/cogito-keys/cogito/internal/store"
)

// toolOut is what a tool returns: text for the model, a one line summary for
// the transcript, and optionally a secret for the user only.
type toolOut struct {
	OK      bool
	Model   string
	Summary string
	Secret  string
	Link    string
}

type chatTool struct {
	Name     string
	Desc     string
	Props    map[string]any
	Required []string
	// Role is the lowest role that gets this tool. The API enforces roles
	// again on every call; this only keeps the model from offering actions
	// the user cannot take.
	Role    store.Role
	Confirm bool
	Run     func(cs *chatSession, in map[string]any) toolOut
	// Describe explains a destructive action for the confirmation card.
	Describe func(cs *chatSession, in map[string]any) (string, error)
}

// secretsIn returns the key secrets present in a tool input, so they can be
// redacted from the stored conversation.
func (t *chatTool) secretsIn(in map[string]any) []string {
	out := []string{}
	if s, ok := in["secret"].(string); ok && strings.TrimSpace(s) != "" {
		out = append(out, strings.TrimSpace(s))
	}
	if ks, ok := in["keys"].([]any); ok {
		for _, k := range ks {
			if m, ok := k.(map[string]any); ok {
				if s, ok := m["secret"].(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
				}
			}
		}
	}
	return out
}

// ---- input helpers ----

func argStr(in map[string]any, k string) string {
	switch v := in[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func argNum(in map[string]any, k string) (int64, bool) {
	switch v := in[k].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// pick copies the listed keys that are present in the input.
func pick(in map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := in[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func toolFail(msg string) toolOut { return toolOut{Model: msg, Summary: msg} }

// resolveService finds a service by id, slug or name.
func (cs *chatSession) resolveService(v string) (*store.Service, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, errors.New("say which service (its slug, name or id)")
	}
	svcs, err := cs.s.Store.ListServices(cs.r.Context())
	if err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(v, 10, 64)
	var byName []*store.Service
	for _, sv := range svcs {
		if sv.ID == id || strings.EqualFold(sv.Slug, v) {
			return sv, nil
		}
		if strings.EqualFold(sv.Name, v) {
			byName = append(byName, sv)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	if len(byName) > 1 {
		return nil, fmt.Errorf("several services are named %q, use the slug", v)
	}
	return nil, fmt.Errorf("no service %q; call list_services", v)
}

// rows trims a list of objects to the given fields.
func rows(list any, fields ...string) []map[string]any {
	out := []map[string]any{}
	arr, _ := list.([]any)
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, pick(m, fields...))
		}
	}
	return out
}

var keyFields = []string{"id", "service_slug", "label", "hint", "effective_status", "validity", "validity_reason", "tier", "hidden",
	"restricted", "account_name", "account_email", "account_phone", "account_ref", "quota_remaining", "quota_limit", "quota_unit",
	"usage_today", "last_used_at", "last_ok_at", "last_error", "notes"}

var keyInputProps = map[string]any{
	"label":         map[string]any{"type": "string"},
	"account_name":  map[string]any{"type": "string", "description": "name of the person or company the provider account belongs to"},
	"account_email": map[string]any{"type": "string", "description": "email of the provider account"},
	"account_phone": map[string]any{"type": "string", "description": "phone number of the provider account"},
	"account_ref":   map[string]any{"type": "string", "description": "any other identifier: username, account id, workspace"},
	"tier":          map[string]any{"type": "string", "enum": []string{"primary", "backup"}},
	"notes":         map[string]any{"type": "string"},
}

func withProps(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// ---- the tools ----

var chatTools []*chatTool

func chatToolByName(name string) *chatTool {
	for _, t := range chatTools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func chatToolParams(role store.Role) []anthropic.BetaToolUnionParam {
	out := []anthropic.BetaToolUnionParam{}
	for _, t := range chatTools {
		if !role.AtLeast(t.Role) {
			continue
		}
		tp := anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Desc),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: t.Props, Required: t.Required},
		}
		out = append(out, anthropic.BetaToolUnionParam{OfTool: &tp})
	}
	return out
}

// simple builds a tool result from an API call.
func (cs *chatSession) simple(method, path string, body any, summary string, shape func(map[string]any) any) toolOut {
	code, out, msg := cs.api(method, path, body)
	if code >= 400 {
		return toolFail(msg)
	}
	var v any = out
	if shape != nil {
		v = shape(out)
	}
	return toolOut{OK: true, Model: asJSON(v), Summary: summary}
}

func init() {
	none := map[string]any{}
	chatTools = []*chatTool{
		{
			Name: "overview", Role: store.RoleMember, Props: none,
			Desc: "Totals: services, keys by state, requests and rate limits in the last 24 hours, recent events.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/overview", nil, "Read the overview", func(m map[string]any) any {
					m["recent_events"] = rows(m["recent_events"], "ts", "kind", "service_slug", "key_label", "status_code", "detail")
					return m
				})
			},
		},
		{
			Name: "list_providers", Role: store.RoleMember, Props: none,
			Desc: "Providers Cogito knows (built in, manual and AI built), with how many services use each.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/providers", nil, "Listed providers", func(m map[string]any) any {
					return rows(m["providers"], "slug", "name", "category", "status", "builtin", "services_count", "base_url", "auth_type")
				})
			},
		},
		{
			Name: "get_provider", Role: store.RoleMember, Required: []string{"slug"},
			Props: map[string]any{"slug": map[string]any{"type": "string"}},
			Desc:  "One provider's details: website, docs, base URL, auth placement, defaults.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/providers/"+url.PathEscape(argStr(in, "slug")), nil, "Read provider "+argStr(in, "slug"), func(m map[string]any) any {
					if p, ok := m["provider"].(map[string]any); ok {
						delete(p, "spec")
						return p
					}
					delete(m, "spec")
					return m
				})
			},
		},
		{
			Name: "list_services", Role: store.RoleMember, Props: none,
			Desc: "Services with their provider, base URL, strategy and key counts.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/services", nil, "Listed services", func(m map[string]any) any {
					return rows(m["services"], "id", "slug", "name", "provider", "base_url", "auth_type", "strategy",
						"key_counts", "requests_24h", "max_concurrency", "target_trusted")
				})
			},
		},
		{
			Name: "list_keys", Role: store.RoleMember,
			Props: map[string]any{
				"service":  map[string]any{"type": "string", "description": "slug, name or id; empty for all services"},
				"query":    map[string]any{"type": "string", "description": "search label, hint, notes and account fields"},
				"validity": map[string]any{"type": "string", "enum": []string{"valid", "invalid", "unknown"}},
			},
			Desc: "Keys (never their secrets) with status, validity, quota, last use and who they belong to.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				q := url.Values{}
				if v := argStr(in, "service"); v != "" {
					sv, err := cs.resolveService(v)
					if err != nil {
						return toolFail(err.Error())
					}
					q.Set("service_id", strconv.FormatInt(sv.ID, 10))
				}
				if v := argStr(in, "query"); v != "" {
					q.Set("q", v)
				}
				if v := argStr(in, "validity"); v != "" {
					q.Set("validity", v)
				}
				code, out, msg := cs.api("GET", "/api/admin/keys?"+q.Encode(), nil)
				if code >= 400 {
					return toolFail(msg)
				}
				ks := rows(out["keys"], keyFields...)
				total := len(ks)
				if len(ks) > 100 {
					ks = ks[:100]
				}
				return toolOut{OK: true, Summary: fmt.Sprintf("Listed %d keys", total),
					Model: asJSON(map[string]any{"total": total, "keys": ks, "summary": out["summary"]})}
			},
		},
		{
			Name: "key_details", Role: store.RoleMember, Required: []string{"key_id"},
			Props: map[string]any{"key_id": map[string]any{"type": "integer"}},
			Desc:  "One key with recent events and 14 days of activity.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "key_id")
				return cs.simple("GET", fmt.Sprintf("/api/admin/keys/%d/insights", id), nil, fmt.Sprintf("Read key %d", id), func(m map[string]any) any {
					if k, ok := m["key"].(map[string]any); ok {
						m["key"] = pick(k, keyFields...)
					}
					evs := rows(m["events"], "ts", "kind", "app_name", "status_code", "latency_ms", "detail")
					if len(evs) > 10 {
						evs = evs[:10]
					}
					m["events"] = evs
					return m
				})
			},
		},
		{
			Name: "list_apps", Role: store.RoleMember, Props: none,
			Desc: "Apps (consumers) with mode, allowed services and whether they are active. Tokens are never shown.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/apps", nil, "Listed apps", func(m map[string]any) any {
					return rows(m["apps"], "id", "name", "description", "mode", "allowed_services", "active", "lease_hidden", "token_prefix", "last_used_at")
				})
			},
		},
		{
			Name: "list_events", Role: store.RoleMember,
			Props: map[string]any{
				"service": map[string]any{"type": "string"},
				"key_id":  map[string]any{"type": "integer"},
				"kind":    map[string]any{"type": "string", "description": "proxy, lease, rate_limited, quota_exhausted, invalid, error or health"},
				"limit":   map[string]any{"type": "integer", "description": "default 30, max 100"},
			},
			Desc: "Recent request and health check events.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				q := url.Values{}
				if v := argStr(in, "service"); v != "" {
					sv, err := cs.resolveService(v)
					if err != nil {
						return toolFail(err.Error())
					}
					q.Set("service_id", strconv.FormatInt(sv.ID, 10))
				}
				if id, ok := argNum(in, "key_id"); ok {
					q.Set("key_id", strconv.FormatInt(id, 10))
				}
				if v := argStr(in, "kind"); v != "" {
					q.Set("kind", v)
				}
				lim, ok := argNum(in, "limit")
				if !ok || lim <= 0 || lim > 100 {
					lim = 30
				}
				q.Set("limit", strconv.FormatInt(lim, 10))
				return cs.simple("GET", "/api/admin/events?"+q.Encode(), nil, "Read events", func(m map[string]any) any {
					return rows(m["events"], "ts", "kind", "service_slug", "key_id", "key_label", "app_name", "status_code", "latency_ms", "detail")
				})
			},
		},
		{
			Name: "get_ai_run", Role: store.RoleMember, Required: []string{"run_id"},
			Props: map[string]any{"run_id": map[string]any{"type": "integer"}},
			Desc:  "Status of an AI provider builder run.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "run_id")
				out := cs.simple("GET", fmt.Sprintf("/api/admin/ai/runs/%d", id), nil, fmt.Sprintf("Checked AI run %d", id), func(m map[string]any) any {
					if r, ok := m["run"].(map[string]any); ok {
						return pick(r, "id", "kind", "status", "title", "provider_slug", "summary", "error", "service_id")
					}
					return m
				})
				out.Link = fmt.Sprintf("/providers/ai/%d", id)
				return out
			},
		},

		// ---- changes (admin) ----
		{
			Name: "create_service", Role: store.RoleAdmin, Required: []string{"provider"},
			Props: map[string]any{
				"provider": map[string]any{"type": "string", "description": "provider slug from list_providers"},
				"slug":     map[string]any{"type": "string", "description": "optional; defaults from the provider"},
				"name":     map[string]any{"type": "string"},
				"base_url": map[string]any{"type": "string", "description": "only for generic providers or overrides"},
			},
			Desc: "Create a service from a provider, using its defaults.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				body := map[string]any{"provider": argStr(in, "provider")}
				for _, k := range []string{"slug", "name", "base_url"} {
					if v := argStr(in, k); v != "" {
						body[k] = v
					}
				}
				code, out, msg := cs.api("POST", "/api/admin/services", body)
				if code >= 400 {
					return toolFail(msg)
				}
				slug, _ := out["slug"].(string)
				return toolOut{OK: true, Summary: "Created service " + slug, Link: fmt.Sprintf("/services/%v", out["id"]),
					Model: asJSON(pick(out, "id", "slug", "name", "provider", "base_url", "auth_type", "auth_name", "strategy"))}
			},
		},
		{
			Name: "update_service", Role: store.RoleAdmin, Required: []string{"service"},
			Props: map[string]any{
				"service":           map[string]any{"type": "string"},
				"name":              map[string]any{"type": "string"},
				"description":       map[string]any{"type": "string"},
				"base_url":          map[string]any{"type": "string"},
				"strategy":          map[string]any{"type": "string", "enum": []string{"round_robin", "fill_first", "least_used", "most_quota", "random"}},
				"cooldown_sec":      map[string]any{"type": "integer"},
				"max_attempts":      map[string]any{"type": "integer"},
				"max_concurrency":   map[string]any{"type": "integer"},
				"queue_timeout_sec": map[string]any{"type": "integer"},
			},
			Desc: "Change a service's settings.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				body := pick(in, "name", "description", "base_url", "strategy", "cooldown_sec", "max_attempts", "max_concurrency", "queue_timeout_sec")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/services/%d", sv.ID), body, "Updated service "+sv.Slug, func(m map[string]any) any {
					return pick(m, "id", "slug", "name", "base_url", "strategy", "cooldown_sec", "max_attempts", "max_concurrency", "queue_timeout_sec", "target_trusted")
				})
			},
		},
		{
			Name: "add_keys", Role: store.RoleAdmin, Required: []string{"service", "keys"},
			Props: map[string]any{
				"service": map[string]any{"type": "string", "description": "slug, name or id"},
				"keys": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "required": []string{"secret"},
					"properties": withProps(keyInputProps, map[string]any{"secret": map[string]any{"type": "string", "description": "the API key exactly as given"}}),
				}},
			},
			Desc: "Add API keys to a service, with optional label, account details (name, email, phone, other id), tier and notes. Duplicates are skipped. New keys are health checked in the background.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				ks, _ := in["keys"].([]any)
				if len(ks) == 0 {
					return toolFail("no keys given")
				}
				code, out, msg := cs.api("POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"keys": ks})
				if code >= 400 {
					return toolFail(msg)
				}
				created := rows(out["keys"], "id", "label", "hint", "account_name", "account_email", "account_phone", "account_ref")
				skipped, _ := out["skipped"].([]any)
				sum := fmt.Sprintf("Added %d key(s) to %s", len(created), sv.Slug)
				if len(skipped) > 0 {
					sum += fmt.Sprintf(", skipped %d", len(skipped))
				}
				res := map[string]any{"service": sv.Slug, "created": created, "skipped": skipped}
				if wv, ok := out["warning"]; ok {
					res["warning"] = wv
				}
				return toolOut{OK: true, Summary: sum, Model: asJSON(res), Link: fmt.Sprintf("/services/%d", sv.ID)}
			},
		},
		{
			Name: "update_key", Role: store.RoleAdmin, Required: []string{"key_id"},
			Props: withProps(keyInputProps, map[string]any{
				"key_id":          map[string]any{"type": "integer"},
				"status":          map[string]any{"type": "string", "enum": []string{"active", "disabled"}},
				"weight":          map[string]any{"type": "integer"},
				"priority":        map[string]any{"type": "integer"},
				"quota_limit":     map[string]any{"type": "integer", "description": "-1 clears"},
				"max_concurrency": map[string]any{"type": "integer", "description": "-1 uses the service default"},
				"secret":          map[string]any{"type": "string", "description": "only to replace the key with a new secret the user gave"},
			}),
			Desc: "Change a key: label, account details, status (active or disabled), tier, notes, weight, priority, quota limit, concurrency, or replace its secret.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "key_id")
				body := pick(in, "label", "account_name", "account_email", "account_phone", "account_ref", "tier", "notes",
					"status", "weight", "priority", "quota_limit", "max_concurrency", "secret")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/keys/%d", id), body, fmt.Sprintf("Updated key %d", id), func(m map[string]any) any {
					return pick(m, keyFields...)
				})
			},
		},
		{
			Name: "check_keys", Role: store.RoleAdmin,
			Props: map[string]any{
				"key_id":  map[string]any{"type": "integer", "description": "check one key"},
				"service": map[string]any{"type": "string", "description": "check every key in a service"},
				"all":     map[string]any{"type": "boolean", "description": "check every key"},
			},
			Desc: "Run provider health checks: confirms keys work and refreshes quota. Give key_id, service, or all.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				if id, ok := argNum(in, "key_id"); ok {
					return cs.simple("POST", fmt.Sprintf("/api/admin/keys/%d/check", id), nil, fmt.Sprintf("Checked key %d", id), func(m map[string]any) any {
						return pick(m, keyFields...)
					})
				}
				shape := func(m map[string]any) any {
					m["keys"] = rows(m["keys"], "id", "label", "effective_status", "validity", "quota_remaining", "last_error")
					return m
				}
				if v := argStr(in, "service"); v != "" {
					sv, err := cs.resolveService(v)
					if err != nil {
						return toolFail(err.Error())
					}
					return cs.simple("POST", fmt.Sprintf("/api/admin/services/%d/check", sv.ID), nil, "Checked keys in "+sv.Slug, shape)
				}
				if b, _ := in["all"].(bool); b {
					return cs.simple("POST", "/api/admin/keys/check-all", nil, "Checked all keys", shape)
				}
				return toolFail("give key_id, service, or all: true")
			},
		},
		{
			Name: "reset_key", Role: store.RoleAdmin, Required: []string{"key_id"},
			Props: map[string]any{"key_id": map[string]any{"type": "integer"}},
			Desc:  "Put a key back to active and clear its error and cooldown.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "key_id")
				return cs.simple("POST", fmt.Sprintf("/api/admin/keys/%d/reset", id), nil, fmt.Sprintf("Reset key %d", id), func(m map[string]any) any {
					return pick(m, keyFields...)
				})
			},
		},
		{
			Name: "create_app", Role: store.RoleAdmin, Required: []string{"name"},
			Props: map[string]any{
				"name":             map[string]any{"type": "string"},
				"description":      map[string]any{"type": "string"},
				"mode":             map[string]any{"type": "string", "enum": []string{"proxy", "lease", "both"}, "description": "proxy keeps keys inside Cogito (best for most apps)"},
				"allowed_services": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "service slugs; empty means all"},
			},
			Desc: "Create an app (a consumer such as a website or agent). Its token is shown to the user once; you never see it.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				body := pick(in, "name", "description", "mode", "allowed_services")
				code, out, msg := cs.api("POST", "/api/admin/apps", body)
				if code >= 400 {
					return toolFail(msg)
				}
				app, _ := out["app"].(map[string]any)
				tok, _ := out["token"].(string)
				return toolOut{OK: true, Secret: tok, Summary: fmt.Sprintf("Created app %v", app["name"]), Link: "/apps",
					Model: asJSON(map[string]any{"app": pick(app, "id", "name", "mode", "allowed_services"),
						"token": "shown to the user once in the chat; tell them to copy it now"})}
			},
		},
		{
			Name: "update_app", Role: store.RoleAdmin, Required: []string{"app_id"},
			Props: map[string]any{
				"app_id":           map[string]any{"type": "integer"},
				"name":             map[string]any{"type": "string"},
				"description":      map[string]any{"type": "string"},
				"mode":             map[string]any{"type": "string", "enum": []string{"proxy", "lease", "both"}},
				"allowed_services": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"active":           map[string]any{"type": "boolean"},
			},
			Desc: "Change an app's name, mode, allowed services, or turn it on or off.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				body := pick(in, "name", "description", "mode", "allowed_services", "active")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/apps/%d", id), body, fmt.Sprintf("Updated app %d", id), func(m map[string]any) any {
					return pick(m, "id", "name", "mode", "allowed_services", "active")
				})
			},
		},
		{
			Name: "start_provider_build", Role: store.RoleAdmin, Required: []string{"name"},
			Props: map[string]any{
				"name":    map[string]any{"type": "string", "description": "the API or company name"},
				"context": map[string]any{"type": "string", "description": "anything the user said about it"},
				"urls":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "docs or website URLs the user gave"},
			},
			Desc: "Start the AI provider builder for an API Cogito does not know yet. It researches the docs and publishes a provider in a few minutes; then create a service and add keys. Returns a run id to check with get_ai_run.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				ctxText := argStr(in, "name")
				if c := argStr(in, "context"); c != "" {
					ctxText += ". " + c
				}
				body := map[string]any{"kind": "create_provider", "context": ctxText}
				if u, ok := in["urls"].([]any); ok {
					body["urls"] = u
				}
				code, out, msg := cs.api("POST", "/api/admin/ai/runs", body)
				if code >= 400 {
					return toolFail(msg)
				}
				id := out["id"]
				return toolOut{OK: true, Summary: fmt.Sprintf("Started AI provider build %v", id), Link: fmt.Sprintf("/providers/ai/%v", id),
					Model: asJSON(pick(out, "id", "status", "title"))}
			},
		},

		// ---- destructive (admin, needs confirmation) ----
		{
			Name: "delete_key", Role: store.RoleAdmin, Confirm: true, Required: []string{"key_id"},
			Props: map[string]any{"key_id": map[string]any{"type": "integer"}},
			Desc:  "Delete a key permanently. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "key_id")
				code, out, msg := cs.api("GET", fmt.Sprintf("/api/admin/keys/%d/insights", id), nil)
				if code >= 400 {
					return "", errors.New(msg)
				}
				k, _ := out["key"].(map[string]any)
				sv, _ := out["service"].(map[string]any)
				return fmt.Sprintf("Delete key %v (%v, %v) from %v", id, k["label"], k["hint"], sv["slug"]), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "key_id")
				return cs.simple("DELETE", fmt.Sprintf("/api/admin/keys/%d", id), nil, fmt.Sprintf("Deleted key %d", id), nil)
			},
		},
		{
			Name: "delete_service", Role: store.RoleAdmin, Confirm: true, Required: []string{"service"},
			Props: map[string]any{"service": map[string]any{"type": "string"}},
			Desc:  "Delete a service and all its keys. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Delete service %s (%s) and all of its keys", sv.Slug, sv.Name), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				return cs.simple("DELETE", fmt.Sprintf("/api/admin/services/%d", sv.ID), nil, "Deleted service "+sv.Slug, nil)
			},
		},
		{
			Name: "delete_app", Role: store.RoleAdmin, Confirm: true, Required: []string{"app_id"},
			Props: map[string]any{"app_id": map[string]any{"type": "integer"}},
			Desc:  "Delete an app; its token stops working. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "app_id")
				a, err := cs.s.Store.GetApp(cs.r.Context(), id)
				if err != nil {
					return "", fmt.Errorf("no app %d", id)
				}
				return fmt.Sprintf("Delete app %s; its token stops working", a.Name), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				return cs.simple("DELETE", fmt.Sprintf("/api/admin/apps/%d", id), nil, fmt.Sprintf("Deleted app %d", id), nil)
			},
		},
		{
			Name: "rotate_app_token", Role: store.RoleAdmin, Confirm: true, Required: []string{"app_id"},
			Props: map[string]any{"app_id": map[string]any{"type": "integer"}},
			Desc:  "Replace an app's token; the old one stops working at once. The new token is shown to the user only. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "app_id")
				a, err := cs.s.Store.GetApp(cs.r.Context(), id)
				if err != nil {
					return "", fmt.Errorf("no app %d", id)
				}
				return fmt.Sprintf("Rotate the token of app %s; the current token stops working immediately", a.Name), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				code, out, msg := cs.api("POST", fmt.Sprintf("/api/admin/apps/%d/rotate-token", id), nil)
				if code >= 400 {
					return toolFail(msg)
				}
				tok, _ := out["token"].(string)
				return toolOut{OK: true, Secret: tok, Summary: fmt.Sprintf("Rotated the token of app %d", id), Link: "/apps",
					Model: `{"token":"shown to the user once in the chat; tell them to copy it now"}`}
			},
		},
	}
}
