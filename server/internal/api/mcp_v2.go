package api

// MCP v2: each app gets its own MCP server. An admin picks which tool sets
// the app sees (core leasing, tools generated from provider examples, read
// only key insights, key management), adds instructions, and can make
// get_key exclusive by default. Settings live in SQLite and apply on the
// next request, with no restart.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Tool set names, as shown in the dashboard.
const (
	setCore     = "core"
	setService  = "service_tools"
	setInsights = "insights"
	setManage   = "manage"
)

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Set         string         `json:"-"`
	// For generated service tools.
	service string
	example *providers.Example
	params  []string
}

func readOnly(title string) map[string]any {
	return map[string]any{"title": title, "readOnlyHint": true, "openWorldHint": false}
}

var coreTools = []mcpTool{
	{Name: "list_services", Set: setCore, Annotations: readOnly("List services"),
		Description: "List the API services this app can use and whether each has a key available right now.",
		InputSchema: obj(map[string]any{})},
	{Name: "pool_status", Set: setCore, Annotations: readOnly("Pool status"),
		Description: "Check whether a service currently has an available key.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
	{Name: "get_key", Set: setCore, Annotations: map[string]any{"title": "Lease a key", "openWorldHint": false},
		Description: "Lease an API key for a service. Returns the secret, where to put it, and a lease_id. Always call report_key when done. Set exclusive=true to keep the key to yourself (no other caller gets it) until you report or the lease expires; use renew_key for long jobs.",
		InputSchema: obj(map[string]any{
			"service":         str("Service slug"),
			"exclusive":       map[string]any{"type": "boolean", "description": "Reserve the key for you alone until you report or it expires"},
			"exclude_key_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Key ids to skip"},
			"ttl_sec":         map[string]any{"type": "integer", "description": "How long to hold the key (default 120 to 300 seconds, max 86400)"},
			"wait_sec":        map[string]any{"type": "integer", "description": "Wait up to this many seconds (max 60) for a key when all are busy"},
		}, "service")},
	{Name: "renew_key", Set: setCore, Annotations: map[string]any{"title": "Renew a lease", "idempotentHint": true},
		Description: "Extend a lease you hold so a long job keeps its key.",
		InputSchema: obj(map[string]any{"lease_id": str("Lease id from get_key"), "ttl_sec": map[string]any{"type": "integer", "description": "New time to live from now, in seconds"}}, "lease_id")},
	{Name: "report_key", Set: setCore, Annotations: map[string]any{"title": "Report a lease"},
		Description: "Report how a leased key performed and release it. Use outcome rate_limited, busy, quota_exhausted, invalid or banned with rotate=true to get the next key.",
		InputSchema: obj(map[string]any{
			"lease_id":        str("Lease id from get_key"),
			"outcome":         map[string]any{"type": "string", "enum": []string{"ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error"}},
			"retry_after_sec": map[string]any{"type": "integer"},
			"message":         str("Error text from the provider"),
			"rotate":          map[string]any{"type": "boolean", "description": "Return a new lease when the outcome is a failure"},
		}, "lease_id", "outcome")},
	{Name: "service_guide", Set: setCore, Annotations: readOnly("Service guide"),
		Description: "How to call a service through Acnologia, as Markdown: endpoints with parameters, example requests, limits and pricing, and how errors are handled. Read it before calling a service you have not used.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
	{Name: "proxy_request", Set: setCore, Annotations: map[string]any{"title": "Call a service", "openWorldHint": true},
		Description: "Call any endpoint of a service through Acnologia. Acnologia picks a key, injects it, and retries with the next key on rate limits. The key is never shown to you.",
		InputSchema: obj(map[string]any{
			"service": str("Service slug"),
			"method":  map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, "default": "GET"},
			"path":    str("Path on the provider, for example /v1/search"),
			"query":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"body":    map[string]any{"description": "JSON body for POST, PUT or PATCH"},
		}, "service", "path")},
}

var insightTools = []mcpTool{
	{Name: "list_keys", Set: setInsights, Annotations: readOnly("List keys"),
		Description: "List keys with whether each works, when it was last used and last worked, and how much quota is left. Secrets are never included.",
		InputSchema: obj(map[string]any{
			"service":  str("Optional service slug"),
			"validity": map[string]any{"type": "string", "enum": []string{"valid", "invalid", "unknown"}},
		})},
	{Name: "key_details", Set: setInsights, Annotations: readOnly("Key details"),
		Description: "Details for one key: status, validity, quota, cooldown, last error, and recent events. No secret.",
		InputSchema: obj(map[string]any{"key_id": map[string]any{"type": "integer"}}, "key_id")},
	{Name: "service_stats", Set: setInsights, Annotations: readOnly("Service stats"),
		Description: "Key counts by state and requests in the last 24 hours for a service.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
}

var manageTools = []mcpTool{
	{Name: "check_keys", Set: setManage, Annotations: map[string]any{"title": "Check keys", "openWorldHint": true},
		Description: "Run the provider health check on every key of a service and update their status and quota.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
	{Name: "set_key_status", Set: setManage, Annotations: map[string]any{"title": "Enable or disable a key", "destructiveHint": true, "idempotentHint": true},
		Description: "Enable or disable a key. Disabled keys are never handed out.",
		InputSchema: obj(map[string]any{"key_id": map[string]any{"type": "integer"}, "status": map[string]any{"type": "string", "enum": []string{"active", "disabled"}}}, "key_id", "status")},
	{Name: "add_keys", Set: setManage, Annotations: map[string]any{"title": "Add keys"},
		Description: "Add API keys to a service. Duplicates are skipped. New keys are health checked in the background.",
		InputSchema: obj(map[string]any{
			"service": str("Service slug"),
			"keys": map[string]any{"type": "array", "items": obj(map[string]any{
				"label":  str("A name for the key, for example the account it belongs to"),
				"secret": str("The API key"),
			}, "secret")},
		}, "service", "keys")},
}

var toolNameRe = regexp.MustCompile(`[^a-z0-9_]+`)

func toolName(parts ...string) string {
	n := strings.Trim(toolNameRe.ReplaceAllString(strings.ToLower(strings.Join(parts, "_")), "_"), "_")
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

var pathParamRe = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// serviceTools turns each provider example into a ready to call tool, for
// example "acme_search" with a q parameter, so agents need not know paths.
func (s *Server) serviceTools(ctx context.Context, a *store.App) []mcpTool {
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return nil
	}
	out := []mcpTool{}
	seen := map[string]bool{}
	for _, sv := range svcs {
		if !a.CanUse(sv.Slug) {
			continue
		}
		spec := docSpec(sv)
		for i := range spec.Examples {
			ex := spec.Examples[i]
			name := toolName(sv.Slug, ex.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			props := map[string]any{}
			required := []string{}
			params := []string{}
			for _, m := range pathParamRe.FindAllStringSubmatch(ex.Path, -1) {
				props[m[1]] = str("Path parameter")
				required = append(required, m[1])
			}
			keys := make([]string, 0, len(ex.Query))
			for k := range ex.Query {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				props[k] = map[string]any{"type": "string", "description": fmt.Sprintf("Query parameter (example: %s)", ex.Query[k])}
				params = append(params, k)
			}
			props["extra_query"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Any other query parameters"}
			if ex.Body != nil {
				eb, _ := json.Marshal(ex.Body)
				props["body"] = map[string]any{"type": "object", "description": "JSON body. Example: " + string(eb)}
				required = append(required, "body")
			}
			desc := fmt.Sprintf("%s on %s (%s %s). Acnologia adds a pooled key and rotates on rate limits.", ex.Name, sv.Name, strings.ToUpper(orDefault(ex.Method, "GET")), ex.Path)
			desc += offerHint(spec, orDefault(ex.Method, "GET"), ex.Path)
			if sv.Description != "" {
				desc += " " + sv.Description
			}
			exCopy := ex
			out = append(out, mcpTool{
				Name: name, Title: sv.Name + ": " + ex.Name, Description: desc, Set: setService,
				InputSchema: obj(props, required...),
				Annotations: map[string]any{"title": sv.Name + ": " + ex.Name, "openWorldHint": true, "readOnlyHint": strings.EqualFold(orDefault(ex.Method, "GET"), "GET")},
				service:     sv.Slug, example: &exCopy, params: params,
			})
		}
	}
	return out
}

// docSpec is the spec whose examples and offers describe a service: for a
// service shared by another server, the owner's provider when this server
// has it (imported or its own). Classification, auth and key pacing always
// use the service's own provider.
func docSpec(sv *store.Service) *providers.Spec {
	if sv.PeerProvider != "" {
		if spec, ok := providers.Lookup(sv.PeerProvider); ok {
			return spec
		}
	}
	return providers.Get(sv.Provider)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// mcpTools returns the tools this app may see, honoring its settings.
func (s *Server) mcpTools(ctx context.Context, a *store.App, st store.MCPSettings) []mcpTool {
	var all []mcpTool
	if st.Core {
		for _, t := range coreTools {
			if (t.Name == "get_key" || t.Name == "renew_key" || t.Name == "report_key") && !a.AllowsLease() {
				continue
			}
			if t.Name == "proxy_request" && !a.AllowsProxy() {
				continue
			}
			all = append(all, t)
		}
	}
	if st.ServiceTools && a.AllowsProxy() {
		all = append(all, s.serviceTools(ctx, a)...)
	}
	if st.Insights {
		all = append(all, insightTools...)
	}
	if st.Manage {
		all = append(all, manageTools...)
	}
	off := map[string]bool{}
	for _, n := range st.DisabledTools {
		off[n] = true
	}
	out := make([]mcpTool, 0, len(all))
	for _, t := range all {
		if !off[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

func findTool(tools []mcpTool, name string) *mcpTool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// callServiceTool fills the example's path and query from the arguments and proxies it.
func (s *Server) callServiceTool(ctx context.Context, r *http.Request, t *mcpTool, raw json.RawMessage) (map[string]any, error) {
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	path := t.example.Path
	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		v, _ := args[m[1]].(string)
		if v == "" {
			return nil, fmt.Errorf("%s is required", m[1])
		}
		path = strings.ReplaceAll(path, m[0], urlPathEscape(v))
	}
	query := map[string]string{}
	for _, k := range t.params {
		if v, ok := args[k]; ok && v != nil {
			query[k] = fmt.Sprint(v)
		}
	}
	if extra, ok := args["extra_query"].(map[string]any); ok {
		for k, v := range extra {
			query[k] = fmt.Sprint(v)
		}
	}
	var body json.RawMessage
	if b, ok := args["body"]; ok && b != nil {
		body, _ = json.Marshal(b)
	}
	return s.mcpProxy(ctx, r, t.service, orDefault(t.example.Method, "GET"), path, query, body)
}

// ---- insights and manage ----

func (s *Server) mcpKeyView(k *store.Key, sv *store.Service) map[string]any {
	return map[string]any{
		"key_id": k.ID, "label": k.Label, "hint": k.Hint, "service": sv.Slug,
		"validity": k.Validity, "validity_reason": k.ValidityReason, "status": k.EffectiveStatus,
		"tier": k.Tier, "last_used_at": k.LastUsedAt, "last_ok_at": k.LastOKAt, "last_checked_at": k.LastCheckedAt,
		"quota_remaining": k.QuotaRemaining, "quota_limit": k.QuotaLimit, "quota_unit": providers.Get(sv.Provider).Quota.Unit,
		"quota_reset_at": k.QuotaResetAt, "cooldown_until": k.CooldownUntil, "in_flight": k.InFlight,
		"usage_today": k.UsageToday, "usage_total": k.UsageTotal, "last_error": k.LastError,
	}
}

// appKeys lists keys of services the app can use. Hidden keys are included
// only when a superadmin cleared the app for hidden keys.
func (s *Server) appKeys(ctx context.Context, a *store.App, service string) ([]*store.Key, map[int64]*store.Service, error) {
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return nil, nil, err
	}
	bySvc := map[int64]*store.Service{}
	var out []*store.Key
	for _, sv := range svcs {
		if !a.CanUse(sv.Slug) || (service != "" && sv.Slug != service) {
			continue
		}
		keys, err := s.Store.ListKeys(ctx, sv.ID, a.LeaseHidden)
		if err != nil {
			return nil, nil, err
		}
		bySvc[sv.ID] = sv
		out = append(out, keys...)
	}
	s.fillInFlight(out...)
	return out, bySvc, nil
}

func (s *Server) appKey(ctx context.Context, a *store.App, id int64) (*store.Key, *store.Service, error) {
	k, err := s.Store.GetKey(ctx, id)
	if err != nil || (k.Hidden && !a.LeaseHidden) {
		return nil, nil, fmt.Errorf("no key %d", id)
	}
	sv, err := s.Store.GetService(ctx, k.ServiceID)
	if err != nil || !a.CanUse(sv.Slug) {
		return nil, nil, fmt.Errorf("no key %d", id)
	}
	s.fillInFlight(k)
	return k, sv, nil
}

func (s *Server) mcpAdminCall(ctx context.Context, a *store.App, name string, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		Service  string `json:"service"`
		Validity string `json:"validity"`
		KeyID    int64  `json:"key_id"`
		Status   string `json:"status"`
		Keys     []struct {
			Label  string `json:"label"`
			Secret string `json:"secret"`
		} `json:"keys"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	actor := "MCP app " + a.Name
	switch name {
	case "list_keys":
		keys, bySvc, err := s.appKeys(ctx, a, args.Service)
		if err != nil {
			return nil, err
		}
		list := []map[string]any{}
		for _, k := range keys {
			if args.Validity != "" && k.Validity != args.Validity {
				continue
			}
			list = append(list, s.mcpKeyView(k, bySvc[k.ServiceID]))
		}
		return map[string]any{"keys": list, "count": len(list)}, nil
	case "key_details":
		k, sv, err := s.appKey(ctx, a, args.KeyID)
		if err != nil {
			return nil, err
		}
		evs, _ := s.Store.ListEvents(ctx, store.EventFilter{KeyID: k.ID, Limit: 10})
		recent := []map[string]any{}
		for _, e := range evs {
			recent = append(recent, map[string]any{"ts": e.TS, "kind": e.Kind, "status_code": e.StatusCode, "detail": e.Detail})
		}
		v := s.mcpKeyView(k, sv)
		v["recent_events"] = recent
		return v, nil
	case "service_stats":
		if !a.CanUse(args.Service) {
			return nil, fmt.Errorf("this app cannot use service %q", args.Service)
		}
		sv, err := s.Store.GetServiceBySlug(ctx, args.Service)
		if err != nil {
			return nil, fmt.Errorf("no service named %q", args.Service)
		}
		if err := s.Store.FillStats(ctx, []*store.Service{sv}, a.LeaseHidden, s.Pool.InFlight()); err != nil {
			return nil, err
		}
		return map[string]any{"service": sv.Slug, "name": sv.Name, "keys": sv.KeyCounts, "in_flight": sv.InFlight, "requests_24h": sv.Requests24h,
			"strategy": sv.Strategy, "max_concurrency": sv.MaxConcurrency}, nil
	case "check_keys":
		keys, bySvc, err := s.appKeys(ctx, a, args.Service)
		if err != nil {
			return nil, err
		}
		sv, ok := firstService(bySvc)
		if !ok {
			return nil, fmt.Errorf("no service named %q for this app", args.Service)
		}
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		sum := s.checkKeys(cctx, sv, keys)
		s.Store.AddAudit(ctx, nil, "keys.check", sv.Slug, actor+": "+sum.Message)
		return map[string]any{"message": sum.Message, "checked": sum.Checked, "working": sum.Valid, "out_of_quota": sum.Exhausted,
			"invalid": sum.Invalid, "unreachable": sum.Failed, "skipped": sum.Skipped}, nil
	case "set_key_status":
		k, sv, err := s.appKey(ctx, a, args.KeyID)
		if err != nil {
			return nil, err
		}
		if args.Status != store.StatusActive && args.Status != store.StatusDisabled {
			return nil, errors.New("status must be active or disabled")
		}
		st := args.Status
		if err := s.Store.UpdateKey(ctx, k.ID, store.KeyPatch{Status: &st}); err != nil {
			return nil, err
		}
		s.Store.AddAudit(ctx, nil, "key.update", sv.Slug+"/"+k.Label, actor+": status="+st)
		nk, _ := s.Store.GetKey(ctx, k.ID)
		s.fillInFlight(nk)
		return s.mcpKeyView(nk, sv), nil
	case "add_keys":
		if !a.CanUse(args.Service) {
			return nil, fmt.Errorf("this app cannot use service %q", args.Service)
		}
		sv, err := s.Store.GetServiceBySlug(ctx, args.Service)
		if err != nil {
			return nil, fmt.Errorf("no service named %q", args.Service)
		}
		if len(args.Keys) == 0 || len(args.Keys) > 200 {
			return nil, errors.New("send between 1 and 200 keys")
		}
		added, skipped := []map[string]any{}, 0
		created := []*store.Key{}
		for _, in := range args.Keys {
			secret := strings.TrimSpace(in.Secret)
			if secret == "" {
				skipped++
				continue
			}
			enc, err := s.Pool.Box.Encrypt(secret)
			if err != nil {
				return nil, err
			}
			label := strings.TrimSpace(in.Label)
			if label == "" {
				label = sv.Name + " key " + crypto.Hint(secret)[3:]
			}
			k, err := s.Store.CreateKey(ctx, store.NewKey{ServiceID: sv.ID, Label: label, SecretEnc: enc, SecretFP: s.Pool.Box.Fingerprint(secret),
				Hint: crypto.Hint(secret), Tier: "primary", Weight: 1, Priority: 100})
			if errors.Is(err, store.ErrDuplicate) {
				skipped++
				continue
			}
			if err != nil {
				return nil, err
			}
			created = append(created, k)
			added = append(added, map[string]any{"key_id": k.ID, "label": k.Label, "hint": k.Hint})
		}
		s.Store.AddAudit(ctx, nil, "key.create", sv.Slug, fmt.Sprintf("%s: %d added, %d skipped", actor, len(added), skipped))
		go s.checkMany(sv, created)
		return map[string]any{"added": added, "skipped": skipped}, nil
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func firstService(m map[int64]*store.Service) (*store.Service, bool) {
	for _, sv := range m {
		return sv, true
	}
	return nil, false
}

// ---- resources and prompts ----

func (s *Server) mcpResources(ctx context.Context, a *store.App) []map[string]any {
	out := []map[string]any{
		{"uri": "acnologia://services", "name": "services", "title": "Services", "mimeType": "application/json",
			"description": "Services this app can use, with availability and how the key is sent."},
		{"uri": "acnologia://llms.txt", "name": "llms.txt", "title": "Acnologia guide for agents", "mimeType": "text/plain"},
	}
	svcs, _ := s.Store.ListServices(ctx)
	for _, sv := range svcs {
		if a.CanUse(sv.Slug) {
			out = append(out, map[string]any{"uri": "acnologia://services/" + sv.Slug, "name": sv.Slug, "title": sv.Name,
				"mimeType": "application/json", "description": "How to call " + sv.Name + " through Acnologia"})
			out = append(out, map[string]any{"uri": "acnologia://services/" + sv.Slug + "/guide", "name": sv.Slug + "_guide", "title": sv.Name + " guide",
				"mimeType": "text/markdown", "description": "Usage guide for " + sv.Name + ": endpoints, examples, limits and errors"})
		}
	}
	return out
}

func (s *Server) mcpReadResource(ctx context.Context, a *store.App, uri string) (map[string]any, error) {
	text := func(mime, body string) map[string]any {
		return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mime, "text": body}}}
	}
	// Clients may have saved URIs from before the rename, under cogito://.
	if rest, ok := strings.CutPrefix(uri, "cogito://"); ok {
		uri = "acnologia://" + rest
	}
	switch {
	case uri == "acnologia://llms.txt":
		return text("text/plain", strings.ReplaceAll(string(s.LLMsTxt), "{{BASE_URL}}", s.Cfg.PublicURL)), nil
	case uri == "acnologia://services":
		res, err := s.mcpCall(ctx, nil, "list_services", nil)
		if err != nil {
			return nil, err
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return text("application/json", string(b)), nil
	case strings.HasPrefix(uri, "acnologia://services/") && strings.HasSuffix(uri, "/guide"):
		slug := strings.TrimSuffix(strings.TrimPrefix(uri, "acnologia://services/"), "/guide")
		d, err := s.mcpServiceGuide(ctx, a, slug)
		if err != nil {
			return nil, fmt.Errorf("no resource %s", uri)
		}
		return text("text/markdown", d.Render(false)), nil
	case strings.HasPrefix(uri, "acnologia://services/"):
		slug := strings.TrimPrefix(uri, "acnologia://services/")
		if !a.CanUse(slug) {
			return nil, fmt.Errorf("no resource %s", uri)
		}
		sv, err := s.Store.GetServiceBySlug(ctx, slug)
		if err != nil {
			return nil, fmt.Errorf("no resource %s", uri)
		}
		spec := docSpec(sv)
		examples := []map[string]any{}
		for _, ex := range spec.Examples {
			examples = append(examples, map[string]any{"name": ex.Name, "method": orDefault(ex.Method, "GET"), "path": ex.Path, "query": ex.Query, "body": ex.Body})
		}
		doc := map[string]any{
			"slug": sv.Slug, "name": sv.Name, "category": sv.Category, "description": sv.Description,
			"base_url": sv.BaseURL, "auth": map[string]any{"type": sv.AuthType, "name": sv.AuthName},
			"quota_unit": spec.Quota.Unit, "max_concurrency_per_key": sv.MaxConcurrency, "examples": examples,
			"offers":      offerRows(spec.Offers),
			"how_to_call": "Use proxy_request with service=" + sv.Slug + " and a path, or the generated tool for an example. Acnologia adds the key.",
			"guide":       "acnologia://services/" + sv.Slug + "/guide",
		}
		if p, err := s.Store.GetProvider(ctx, sv.DocProvider()); err == nil && strings.HasPrefix(p.DocsURL, "https://") {
			doc["docs_url"] = p.DocsURL
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		return text("application/json", string(b)), nil
	}
	return nil, fmt.Errorf("no resource %s", uri)
}

var mcpPrompts = []map[string]any{
	{"name": "use_service", "title": "Use a service through Acnologia",
		"description": "Instructions for calling one service with pooled keys.",
		"arguments":   []map[string]any{{"name": "service", "description": "Service slug", "required": true}}},
	{"name": "key_health_report", "title": "Key health report",
		"description": "Summarize which keys work, which are failing and how much quota is left.",
		"arguments":   []map[string]any{{"name": "service", "description": "Optional service slug", "required": false}}},
}

func (s *Server) mcpGetPrompt(ctx context.Context, a *store.App, st store.MCPSettings, name string, args map[string]string) (map[string]any, error) {
	msg := func(t string) map[string]any {
		return map[string]any{"messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": t}}}}
	}
	switch name {
	case "use_service":
		svc := args["service"]
		if !a.CanUse(svc) {
			return nil, fmt.Errorf("this app cannot use service %q", svc)
		}
		return msg(fmt.Sprintf("You can call the %q service through Acnologia, which holds a pool of API keys and rotates them on rate limits. "+
			"First call service_guide with service=%[4]q (or read acnologia://services/%[1]s/guide) to learn its endpoints. Prefer the generated tools named %[3]s_*, or proxy_request with service=%[4]q. "+
			"Never ask for or print an API key. If a call returns a rate limit, try again: Acnologia already rotated keys for you. "+
			"Only lease a key with get_key when you must call the provider yourself, and always call report_key afterwards.", svc, svc, toolName(svc), svc)), nil
	case "key_health_report":
		if !st.Insights {
			return nil, errors.New("key insights are not enabled for this app")
		}
		scope := "every service"
		if args["service"] != "" {
			scope = "the " + args["service"] + " service"
		}
		return msg("Call list_keys for " + scope + ". Report: keys that are invalid (with the reason), keys that are out of quota or below 10 percent (with the reset time), keys not used in 7 days, and keys cooling down. End with one recommended action."), nil
	}
	return nil, fmt.Errorf("no prompt named %q", name)
}

func urlPathEscape(s string) string {
	r := strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23", " ", "%20")
	return r.Replace(s)
}
