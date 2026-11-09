package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// toolOut is what a tool returns: text for the model, a one line summary for
// the transcript, and optionally a secret for the user only.
type toolOut struct {
	OK      bool
	Model   string
	Summary string
	Secret  string
	Link    string
	// Card and Entities feed the rich transcript row.
	Card     *chatCard
	Entities []chatEntity
	// Silent tools (ask_user, wait_for_build) write their own transcript
	// item, so runTool adds no tool row for them.
	Silent bool
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
	// ConfirmIf asks for the user's Confirm only for some inputs: changes
	// to where keys go or who may use them, which text the model read
	// (an event detail, a key note) must never be able to make on its own.
	ConfirmIf func(in map[string]any) bool
	// SecretFields are input fields holding credentials (a proxy URL with a
	// password), redacted from the stored conversation like key secrets.
	SecretFields []string
	Run          func(cs *chatSession, in map[string]any) toolOut
	// Describe explains a destructive action for the confirmation card.
	Describe func(cs *chatSession, in map[string]any) (string, error)
}

// secretsIn returns the key secrets present in a tool input, so they can be
// redacted from the stored conversation.
func (t *chatTool) secretsIn(in map[string]any) []string {
	out := []string{}
	for _, f := range append([]string{"secret"}, t.SecretFields...) {
		if s, ok := in[f].(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
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

// entitiesOf reads up to 60 named rows from a tool's JSON result.
func entitiesOf(model, typ string) []chatEntity {
	var list []map[string]any
	if json.Unmarshal([]byte(model), &list) != nil {
		return nil
	}
	out := []chatEntity{}
	for _, m := range list {
		if len(out) == 60 {
			break
		}
		id, _ := argNum(m, "id")
		e := chatEntity{Type: typ, ID: id, Slug: argStr(m, "slug"), Name: argStr(m, "name")}
		if e.Name != "" && (e.ID != 0 || e.Slug != "") {
			out = append(out, e)
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
	return cs.s.findService(cs.ctx, v)
}

// findService finds a service by id, slug or name.
func (s *Server) findService(ctx context.Context, v string) (*store.Service, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, errors.New("say which service (its slug, name or id)")
	}
	svcs, err := s.Store.ListServices(ctx)
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
	defer func() {
		chatTools = append(append(append(append(chatTools, opsTools()...), guideTools()...), providerTools()...), capabilityTools()...)
	}()
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
			Desc: "Providers Acnologia knows (built in, manual and AI built), with how many services use each.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				out := cs.simple("GET", "/api/admin/providers", nil, "Listed providers", func(m map[string]any) any {
					return rows(m["providers"], "slug", "name", "category", "status", "builtin", "services_count", "base_url", "auth_type")
				})
				out.Entities = entitiesOf(out.Model, "provider")
				return out
			},
		},
		{
			Name: "get_provider", Role: store.RoleMember, Required: []string{"slug"},
			Props: map[string]any{"slug": map[string]any{"type": "string"}},
			Desc:  "One provider's details: website, docs, base URL, auth placement, defaults, its offers (what is free, prices, published limits), and for a provider with many APIs on their own hosts, how many and in which groups. get_provider_spec has the full spec.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				return cs.simple("GET", "/api/admin/providers/"+url.PathEscape(argStr(in, "slug")), nil, "Read provider "+argStr(in, "slug"), func(m map[string]any) any {
					// Older responses wrapped the provider; today it is flat.
					p := m
					if inner, ok := m["provider"].(map[string]any); ok {
						p = inner
					}
					if raw, err := json.Marshal(p["spec"]); err == nil {
						if spec, err := providers.DecodeSpec(raw); err == nil {
							if len(spec.Offers) > 0 {
								p["offers"] = offerRows(spec.Offers)
							}
							if len(spec.Hosts) > 0 {
								groups := map[string]int{}
								for _, h := range spec.Hosts {
									groups[h.Group]++
								}
								p["apis"] = len(spec.Hosts)
								p["api_groups"] = groups
							}
						}
					}
					delete(p, "spec")
					delete(p, "versions")
					return p
				})
			},
		},
		{
			Name: "list_services", Role: store.RoleMember, Props: none,
			Desc: "Services with their provider, base URL, strategy and key counts.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				out := cs.simple("GET", "/api/admin/services", nil, "Listed services", func(m map[string]any) any {
					return rows(m["services"], "id", "slug", "name", "provider", "base_url", "auth_type", "strategy",
						"key_counts", "requests_24h", "max_concurrency", "target_trusted")
				})
				out.Entities = entitiesOf(out.Model, "service")
				return out
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
				out := cs.simple("GET", "/api/admin/apps", nil, "Listed apps", func(m map[string]any) any {
					return rows(m["apps"], "id", "name", "description", "mode", "allowed_services", "active", "lease_hidden", "token_count", "last_used_at")
				})
				out.Entities = entitiesOf(out.Model, "app")
				return out
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
				id, _ := argNum(out, "id")
				ent := chatEntity{Type: "service", ID: id, Slug: slug, Name: argStr(out, "name")}
				return toolOut{OK: true, Summary: "Created service " + slug, Link: fmt.Sprintf("/services/%v", out["id"]),
					Card: &chatCard{Type: "service", Service: &ent, Extra: map[string]any{"provider": out["provider"]}}, Entities: []chatEntity{ent},
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
				"egress_id":         map[string]any{"type": "integer", "description": "egress proxy id from list_egress_proxies; 0 for none"},
				"cache_ttl_sec":     map[string]any{"type": "integer", "description": "cache successful GET responses this long; 0 turns caching off"},
				"cost_per_request":  map[string]any{"type": "number", "description": "cost of one request, used for app budgets"},
			},
			Desc:      "Change a service's settings: name, base URL, rotation strategy, cooldown, attempts, concurrency, egress proxy, response cache and cost per request. A new base URL or egress proxy needs the user's Confirm.",
			ConfirmIf: hasAny("base_url", "auth_type", "auth_name", "provider", "egress_id", "target_trusted"),
			Describe:  describeServiceTarget,
			Run: func(cs *chatSession, in map[string]any) toolOut {
				sv, err := cs.resolveService(argStr(in, "service"))
				if err != nil {
					return toolFail(err.Error())
				}
				body := pick(in, "name", "description", "base_url", "strategy", "cooldown_sec", "max_attempts", "max_concurrency", "queue_timeout_sec",
					"egress_id", "cache_ttl_sec", "cost_per_request")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/services/%d", sv.ID), body, "Updated service "+sv.Slug, func(m map[string]any) any {
					return pick(m, "id", "slug", "name", "base_url", "strategy", "cooldown_sec", "max_attempts", "max_concurrency", "queue_timeout_sec",
						"target_trusted", "egress_id", "cache_ttl_sec", "cost_per_request")
				})
			},
		},
		{
			Name: "add_keys", Role: store.RoleAdmin, Required: []string{"service"},
			Props: map[string]any{
				"service": map[string]any{"type": "string", "description": "slug, name or id"},
				"batch":   map[string]any{"type": "string", "description": `"latest" for every key still in the vault from the most recent paste, or a batch id such as b2. Labels parsed from the paste are used.`},
				"key_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "vault refs such as k3, from [key k3 ...c21e] placeholders"},
				"keys": map[string]any{"type": "array", "description": "per key details; give ref for a pasted key", "items": map[string]any{
					"type": "object",
					"properties": withProps(keyInputProps, map[string]any{
						"ref":    map[string]any{"type": "string", "description": "vault ref such as k3"},
						"secret": map[string]any{"type": "string", "description": "only for a key that reached you as plain text, never a placeholder"},
					}),
				}},
			},
			Desc: "Add API keys to a service. Pasted keys reach you as placeholders [key k3 ...c21e]: pass batch \"latest\" to add every key from the last paste in one call, key_refs for some of them, or keys with ref plus account details, label, tier or notes. Labels from the paste are kept. Duplicates are skipped. New keys are health checked in the background.",
			Run:  addKeysTool,
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
				"egress_id":       map[string]any{"type": "integer", "description": "egress proxy for this key; 0 uses the service's"},
			}),
			Desc:      "Change a key: label, account details, status (active or disabled), tier, notes, weight, priority, quota limit, concurrency, or replace its secret. A new egress proxy needs the user's Confirm.",
			ConfirmIf: hasAny("egress_id"),
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "key_id")
				k, err := cs.s.Store.GetKey(cs.ctx, id)
				if err != nil || (k.Hidden && cs.user.Role != store.RoleSuperadmin) {
					return "", fmt.Errorf("no key %d", id)
				}
				eid, _ := argNum(in, "egress_id")
				return fmt.Sprintf("Send key %s through %s", k.Label, cs.egressName(eid, "the service's egress proxy")), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "key_id")
				body := pick(in, "label", "account_name", "account_email", "account_phone", "account_ref", "tier", "notes",
					"status", "weight", "priority", "quota_limit", "max_concurrency", "secret", "egress_id")
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
				"mode":             map[string]any{"type": "string", "enum": []string{"proxy", "lease", "both"}, "description": "proxy keeps keys inside Acnologia (best for most apps)"},
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
				appID, _ := argNum(app, "id")
				return toolOut{OK: true, Secret: tok, Summary: fmt.Sprintf("Created app %v", app["name"]), Link: "/apps",
					Entities: []chatEntity{{Type: "app", ID: appID, Name: argStr(app, "name")}},
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
				"limits": map[string]any{"type": "object", "description": "replaces all limits; omit a field or set it null for no limit",
					"properties": map[string]any{
						"daily_requests": map[string]any{"type": "integer"}, "monthly_requests": map[string]any{"type": "integer"},
						"daily_budget": map[string]any{"type": "number"}, "monthly_budget": map[string]any{"type": "number"},
					}},
			},
			Desc:      "Change an app's name, mode, allowed services, request and budget limits, or turn it on or off. A new mode or allowed services list needs the user's Confirm.",
			ConfirmIf: hasAny("mode", "allowed_services", "lease_hidden"),
			Describe:  describeAppAccess,
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				body := pick(in, "name", "description", "mode", "allowed_services", "active", "limits")
				return cs.simple("PATCH", fmt.Sprintf("/api/admin/apps/%d", id), body, fmt.Sprintf("Updated app %d", id), func(m map[string]any) any {
					return pick(m, "id", "name", "mode", "allowed_services", "active", "limits")
				})
			},
		},
		{
			Name: "search_providers", Role: store.RoleMember, Required: []string{"query"},
			Props: map[string]any{"query": map[string]any{"type": "string", "description": "a name, slug, website or docs URL, such as gemini or https://ai.google.dev/api"}},
			Desc:  "Search the provider catalog by name, slug, website and docs URL. Use it before starting a provider build: a built in provider is always better.",
			Run:   searchProvidersTool,
		},
		{
			Name: "provider_offers", Role: store.RoleMember,
			Props: map[string]any{
				"provider":  map[string]any{"type": "string", "description": "a provider slug or name to narrow to, such as tinyfish; empty for all"},
				"category":  map[string]any{"type": "string", "description": "a category such as Search, LLM or Voice; empty for all"},
				"free_only": map[string]any{"type": "boolean", "description": "only providers with at least one free offer"},
			},
			Desc: "Read only. Providers' offers from their specs: which products or endpoints are free, prices, published rate limits and concurrency, with the pricing page and the date it was checked. Use it for questions like which search provider is free or what a provider's limits are.",
			Run:  providerOffersTool,
		},
		{
			Name: "start_provider_build", Role: store.RoleAdmin, Required: []string{"name"},
			Props: map[string]any{
				"name":         map[string]any{"type": "string", "description": "the API or company name"},
				"context":      map[string]any{"type": "string", "description": "anything the user said about it"},
				"urls":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "docs or website URLs the user gave"},
				"test_key_ref": map[string]any{"type": "string", "description": "vault ref of one pasted key for the builder's live check, such as k1"},
			},
			Desc: "Start the AI provider builder for an API the catalog does not have (search_providers first). It researches the docs, writes and tests a provider and publishes it with a service in a few minutes. Then call wait_for_build with the run id.",
			Run:  startBuildTool,
		},
		{
			Name: "wait_for_build", Role: store.RoleMember, Required: []string{"run_id"},
			Props: map[string]any{
				"run_id":      map[string]any{"type": "integer"},
				"timeout_sec": map[string]any{"type": "integer", "description": "default and most 600"},
			},
			Desc: "Wait for a provider build to end while the user watches its progress card. Returns published (with provider and service), needs_review or failed with the reason.",
			Run:  waitBuildTool,
		},
		{
			Name: "ask_user", Role: store.RoleMember, Required: []string{"question", "kind"},
			Props: map[string]any{
				"question": map[string]any{"type": "string"},
				"kind":     map[string]any{"type": "string", "enum": []string{"names", "choice"}, "description": "names: a name for each pasted key; choice: pick one option"},
				"key_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "for names: the keys to name"},
				"batch":    map[string]any{"type": "string", "description": "for names: a batch id or latest instead of key_refs"},
				"suggestions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
					"description": "for names: a suggested name per ref"},
				"choices": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "for choice: 2 to 8 short options"},
			},
			Desc: "Ask the user a structured question shown as a form, then end your turn; the answer comes as the next message. Only when a decision is truly theirs: never for keys that already have labels from the paste.",
			Run:  askUserTool,
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
				a, err := cs.s.Store.GetApp(cs.ctx, id)
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
			Desc:  "Replace all of an app's tokens with one new token; every other token of the app stops working at once. To add a token without breaking the others use create_app_token. The new token is shown to the user only. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "app_id")
				a, err := cs.s.Store.GetApp(cs.ctx, id)
				if err != nil {
					return "", fmt.Errorf("no app %d", id)
				}
				return fmt.Sprintf("Rotate the tokens of app %s; a new token is created and all %d current tokens stop working immediately", a.Name, a.TokenCount), nil
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
		{
			Name: "list_app_tokens", Role: store.RoleMember, Required: []string{"app_id"},
			Props: map[string]any{"app_id": map[string]any{"type": "integer"}},
			Desc:  "An app's tokens: id, name, prefix hint, created, last used, expiry and whether revoked. Token values are never shown.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				return cs.simple("GET", fmt.Sprintf("/api/admin/apps/%d/tokens", id), nil, fmt.Sprintf("Listed tokens of app %d", id), func(m map[string]any) any {
					return rows(m["tokens"], "id", "name", "prefix", "created_at", "created_by_email", "last_used_at", "expires_at", "revoked_at")
				})
			},
		},
		{
			Name: "create_app_token", Role: store.RoleAdmin, Required: []string{"app_id", "name"},
			Props: map[string]any{
				"app_id":          map[string]any{"type": "integer"},
				"name":            map[string]any{"type": "string", "description": "what the token is for, such as ci or staging"},
				"expires_in_days": map[string]any{"type": "integer", "description": "optional; omit for a token that does not expire"},
			},
			Desc: "Add a named token to an app; its other tokens keep working. The token is shown to the user once; you never see it.",
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				code, out, msg := cs.api("POST", fmt.Sprintf("/api/admin/apps/%d/tokens", id), pick(in, "name", "expires_in_days"))
				if code >= 400 {
					return toolFail(msg)
				}
				tok, _ := out["token"].(string)
				meta, _ := out["app_token"].(map[string]any)
				return toolOut{OK: true, Secret: tok, Summary: fmt.Sprintf("Created token %s for app %d", argStr(meta, "name"), id), Link: "/apps",
					Model: asJSON(map[string]any{"app_token": pick(meta, "id", "name", "prefix", "expires_at"),
						"token": "shown to the user once in the chat; tell them to copy it now"})}
			},
		},
		{
			Name: "revoke_app_token", Role: store.RoleAdmin, Confirm: true, Required: []string{"app_id", "token_id"},
			Props: map[string]any{"app_id": map[string]any{"type": "integer"}, "token_id": map[string]any{"type": "integer", "description": "from list_app_tokens"}},
			Desc:  "Revoke one token of an app; it stops working at once and the app's other tokens keep working. The user must confirm.",
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				id, _ := argNum(in, "app_id")
				tid, _ := argNum(in, "token_id")
				a, err := cs.s.Store.GetApp(cs.ctx, id)
				if err != nil {
					return "", fmt.Errorf("no app %d", id)
				}
				t, err := cs.s.Store.GetAppToken(cs.ctx, id, tid)
				if err != nil {
					return "", fmt.Errorf("app %s has no token %d", a.Name, tid)
				}
				return fmt.Sprintf("Revoke token %s (%s) of app %s; it stops working immediately", t.Name, t.Prefix, a.Name), nil
			},
			Run: func(cs *chatSession, in map[string]any) toolOut {
				id, _ := argNum(in, "app_id")
				tid, _ := argNum(in, "token_id")
				return cs.simple("DELETE", fmt.Sprintf("/api/admin/apps/%d/tokens/%d", id, tid), nil, fmt.Sprintf("Revoked token %d of app %d", tid, id), func(m map[string]any) any {
					return pick(m, "id", "name", "prefix", "revoked_at")
				})
			},
		},
	}
}

// hasAny is a ConfirmIf that holds when the input sets any of the fields.
func hasAny(fields ...string) func(in map[string]any) bool {
	return func(in map[string]any) bool {
		for _, f := range fields {
			if _, ok := in[f]; ok {
				return true
			}
		}
		return false
	}
}

// urlHost is the scheme and host of a URL without any credentials, for confirmations.
func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "an invalid URL"
	}
	return u.Scheme + "://" + u.Host
}

// egressName names an egress proxy for a confirmation; id 0 is none.
func (cs *chatSession) egressName(id int64, none string) string {
	if id <= 0 {
		return none
	}
	p, err := cs.s.Store.GetEgress(cs.ctx, id)
	if err != nil {
		return fmt.Sprintf("egress proxy %d (not found)", id)
	}
	return fmt.Sprintf("egress proxy %s (%d)", p.Name, p.ID)
}

// describeServiceTarget states where a service will send its keys after
// an update_service, in full, for the user's Confirm.
func describeServiceTarget(cs *chatSession, in map[string]any) (string, error) {
	sv, err := cs.resolveService(argStr(in, "service"))
	if err != nil {
		return "", err
	}
	base := sv.BaseURL
	if v, ok := in["base_url"].(string); ok {
		base = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	eid := int64(0)
	if sv.EgressID != nil {
		eid = *sv.EgressID
	}
	if v, ok := argNum(in, "egress_id"); ok {
		eid = v
	}
	return fmt.Sprintf("Send the keys of service %s to %s through %s. Hidden keys pause until a superadmin confirms the target in the service's edit form",
		sv.Slug, base, cs.egressName(eid, "no egress proxy")), nil
}

// describeAppAccess states an app's resulting mode and services in full.
func describeAppAccess(cs *chatSession, in map[string]any) (string, error) {
	id, _ := argNum(in, "app_id")
	a, err := cs.s.Store.GetApp(cs.ctx, id)
	if err != nil {
		return "", fmt.Errorf("no app %d", id)
	}
	mode := a.Mode
	if v, ok := in["mode"].(string); ok {
		mode = v
	}
	svcs := a.AllowedServices
	if v, ok := in["allowed_services"].([]any); ok {
		svcs = []string{}
		for _, x := range v {
			svcs = append(svcs, fmt.Sprint(x))
		}
	}
	allowed := "all services"
	if len(svcs) > 0 {
		allowed = strings.Join(svcs, ", ")
	}
	desc := fmt.Sprintf("Let app %s use %s in %s mode", a.Name, allowed, mode)
	if mode != "proxy" {
		desc += " (lease mode hands it plain key secrets)"
	}
	if v, ok := in["lease_hidden"].(bool); ok {
		desc += fmt.Sprintf(", lease_hidden %v", v)
	}
	return desc, nil
}
