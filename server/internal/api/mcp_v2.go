package api

// MCP v2: each app gets its own MCP server. An admin picks which tool sets
// the app sees (core leasing, tools generated from provider examples, read
// only key insights, key management), adds instructions, and can make
// get_key exclusive by default. Settings live in SQLite and apply on the
// next request, with no restart.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Set          string         `json:"-"`
	// For generated service tools.
	service string
	example *providers.Example
	params  []string
	// For tools synced from an upstream MCP server configured on a service or provider.
	upstreamMCP  bool
	upstreamTool string
	// upstreamRO is an upstream tool's effective read-only state: the
	// admin's override, else the upstream's readOnlyHint.
	upstreamRO bool
	// capability is set on a capability's tool: its arguments are the
	// capability's params, sent to /proxy/{capability}. routes are the
	// services that serve it.
	capability string
	routes     []string
	// routeCalls are the endpoints its routes call, "service METHOD path",
	// to find the service tools that do the same call.
	routeCalls []string
	// keywords are extra words search_tools matches: the service's slug,
	// name, provider, category, product group and hosts.
	keywords string
	// searchText is what search_tools reads as the tool's description when
	// set: the tool's own words, without the text every service tool shares.
	searchText string
}

// toolIsReadOnly reports whether a tool is safe to expose in read-only mode.
// call_tool is allowed because it only runs tools already permitted by mcpTool.
// An upstream MCP tool counts by its effective state, which an admin can
// set whatever the upstream server claims.
func toolIsReadOnly(t mcpTool) bool {
	if t.Name == "call_tool" {
		return true
	}
	if t.upstreamMCP {
		return t.upstreamRO
	}
	if t.Annotations == nil {
		return false
	}
	ro, _ := t.Annotations["readOnlyHint"].(bool)
	return ro
}

func readOnly(title string) map[string]any {
	return map[string]any{"title": title, "readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
}

// hints spells out every annotation, since an absent destructiveHint means
// true and makes clients ask before harmless calls such as report_key.
func hints(title string, destructive, idempotent, openWorld bool) map[string]any {
	return map[string]any{"title": title, "readOnlyHint": false, "destructiveHint": destructive,
		"idempotentHint": idempotent, "openWorldHint": openWorld}
}

func integer(desc string, lo, hi int) map[string]any {
	m := map[string]any{"type": "integer", "description": desc, "minimum": lo}
	if hi > 0 {
		m["maximum"] = hi
	}
	return m
}

// withTitles copies each tool's annotation title to its top level title,
// where clients since protocol 2025-06-18 read it.
func withTitles(ts []mcpTool) []mcpTool {
	for i := range ts {
		if t, _ := ts[i].Annotations["title"].(string); ts[i].Title == "" && t != "" {
			ts[i].Title = t
		}
	}
	return ts
}

var coreTools = withTitles([]mcpTool{
	{Name: "whoami", Set: setCore, Annotations: readOnly("Who am I"),
		Description: "What this connection may do: the token (kind, name, expiry, read-only), the app's mode (proxy, lease or both) and MCP tool sets, its limits with what is left of each, and the services it may call. Call it first when a call is refused, or to plan around limits.",
		InputSchema: obj(map[string]any{})},
	{Name: "list_services", Set: setCore, Annotations: readOnly("List services"),
		Description: "List the API services this app can use and whether each has a key available right now. Use a service's slug with service_guide, proxy_request and the other tools. Pages with limit and offset; has_more says when to ask for the next page.",
		InputSchema: obj(map[string]any{
			"limit":  integer("How many services to return (default 100, max 500)", 1, 500),
			"offset": integer("How many services to skip, from next_offset of the previous page", 0, 0),
		})},
	{Name: "pool_status", Set: setCore, Annotations: readOnly("Pool status"),
		Description: "Check whether a service currently has an available key.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
	{Name: "get_key", Set: setCore, Annotations: hints("Lease a key", false, false, false),
		Description: "Lease an API key for a service. Returns the secret, where to put it, and a lease_id. Always call report_key when done. Set exclusive=true to keep the key to yourself (no other caller gets it) until you report or the lease expires; use renew_key for long jobs.",
		InputSchema: obj(map[string]any{
			"service":         str("Service slug"),
			"exclusive":       map[string]any{"type": "boolean", "description": "Reserve the key for you alone until you report or it expires"},
			"exclude_key_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Key ids to skip"},
			"ttl_sec":         integer("How long to hold the key, in seconds (default 120 to 300)", 1, 86400),
			"wait_sec":        integer("Wait up to this many seconds for a key when all are busy", 0, 60),
		}, "service")},
	{Name: "renew_key", Set: setCore, Annotations: hints("Renew a lease", false, true, false),
		Description: "Extend a lease you hold so a long job keeps its key.",
		InputSchema: obj(map[string]any{"lease_id": str("Lease id from get_key"), "ttl_sec": integer("New time to live from now, in seconds", 1, 86400)}, "lease_id")},
	{Name: "report_key", Set: setCore, Annotations: hints("Report a lease", false, false, false),
		Description: "Report how a leased key performed and release it. Use outcome rate_limited, busy, quota_exhausted, invalid or banned with rotate=true to get the next key.",
		InputSchema: obj(map[string]any{
			"lease_id":        str("Lease id from get_key"),
			"outcome":         map[string]any{"type": "string", "enum": []string{"ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error"}},
			"retry_after_sec": integer("Seconds the provider said to wait, if it said", 0, 0),
			"message":         str("Error text from the provider"),
			"rotate":          map[string]any{"type": "boolean", "description": "Return a new lease when the outcome is a failure"},
		}, "lease_id", "outcome")},
	{Name: "service_guide", Set: setCore, Annotations: readOnly("Service guide"),
		Description: "How to call a service through Acnologia, as Markdown: endpoints with parameters, example requests, limits and pricing, and how errors are handled. Read it before calling a service you have not used.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
	{Name: "proxy_request", Set: setCore, Annotations: hints("Call a service", true, false, true),
		Description: "Call any endpoint of a service through Acnologia. Acnologia picks a key, injects it, and retries with the next key on rate limits. The key is never shown to you. Read service_guide first for the service's paths and parameters.",
		InputSchema: obj(map[string]any{
			"service":         str("Service slug"),
			"method":          map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, "default": "GET"},
			"path":            str("Path on the provider, for example /v1/search"),
			"query":           map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"body":            map[string]any{"description": "JSON body for POST, PUT or PATCH"},
			"idempotency_key": str(idempotencyArgDesc),
		}, "service", "path")},
})

var insightTools = withTitles([]mcpTool{
	{Name: "list_keys", Set: setInsights, Annotations: readOnly("List keys"),
		Description: "List keys with whether each works, when it was last used and last worked, and how much quota is left. Secrets are never included. Pages with limit and offset; has_more says when to ask for the next page.",
		InputSchema: obj(map[string]any{
			"service":  str("Optional service slug"),
			"validity": map[string]any{"type": "string", "enum": []string{"valid", "invalid", "unknown"}},
			"limit":    integer("How many keys to return (default 50, max 200)", 1, 200),
			"offset":   integer("How many keys to skip, from next_offset of the previous page", 0, 0),
		})},
	{Name: "key_details", Set: setInsights, Annotations: readOnly("Key details"),
		Description: "Details for one key: status, validity, quota, cooldown, last error, and recent events. No secret.",
		InputSchema: obj(map[string]any{"key_id": integer("Key id from list_keys", 1, 0)}, "key_id")},
	{Name: "service_stats", Set: setInsights, Annotations: readOnly("Service stats"),
		Description: "Key counts by state and requests in the last 24 hours for a service.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
})

var manageTools = withTitles([]mcpTool{
	{Name: "check_keys", Set: setManage, Annotations: hints("Check keys", false, true, true),
		Description: "Run the provider health check on every key of a service and update their status and quota.",
		InputSchema: obj(map[string]any{"service": str("Service slug")}, "service")},
	{Name: "set_key_status", Set: setManage, Annotations: hints("Enable or disable a key", true, true, false),
		Description: "Enable or disable a key. Disabled keys are never handed out.",
		InputSchema: obj(map[string]any{"key_id": integer("Key id from list_keys", 1, 0), "status": map[string]any{"type": "string", "enum": []string{"active", "disabled"}}}, "key_id", "status")},
	{Name: "add_keys", Set: setManage, Annotations: hints("Add keys", false, false, true),
		Description: "Add API keys to a service. Duplicates are skipped. New keys are health checked in the background as a job: check_job_id in the result names it, and list_keys shows each key's state once it is done. A provider whose credential has several parts (a login and a password for Basic auth, or an app id and an app key) takes parts instead of secret, or secret as login:password.",
		InputSchema: obj(map[string]any{
			"service": str("Service slug"),
			"keys": map[string]any{"type": "array", "items": obj(map[string]any{
				"label":  str("A name for the key, for example the account it belongs to"),
				"secret": str("The API key"),
				"parts": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
					"description": "The parts of a multi part credential by part key, such as {\"login\": \"...\", \"password\": \"...\"}"},
			}), "minItems": 1, "maxItems": 200},
		}, "service", "keys")},
})

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
	c := s.toolCatalog(ctx)
	v := c.view(a)
	out := make([]mcpTool, len(v.ids))
	for i, id := range v.ids {
		out[i] = c.tools[id]
	}
	return out
}

// buildServiceTools makes the tools of every service (both REST examples/guide
// and synced upstream MCP tools) and capability, named as for an app that may
// use them all. Each app sees the ones it may use (toolCatalog.view), so a
// tool keeps its name whatever the app's access.
func buildServiceTools(svcs []*store.Service, caps []*store.Capability, svcMCP map[int64]*store.ServiceMCP) []mcpTool {
	all := &store.App{}
	names := serviceToolNames(svcs, all)
	out := []mcpTool{}
	seen := map[string]bool{}
	for _, set := range [][]mcpTool{coreTools, insightTools, manageTools, discoveryTools} {
		for _, t := range set {
			seen[t.Name], seen[toolPrefix+t.Name] = true, true
		}
	}
	for _, sv := range svcs {
		spec := docSpec(sv)
		for i, src := range toolSources(spec) {
			name := names[toolKey{sv.ID, i}]
			if name == "" {
				continue
			}
			seen[name] = true
			out = append(out, sourceTool(sv, spec, src, name))
		}
	}
	// Synced upstream MCP tools, named deterministically (local services first,
	// oldest first) without colliding with built-in or REST tools.
	if len(svcMCP) > 0 {
		ordered := make([]*store.Service, len(svcs))
		copy(ordered, svcs)
		sort.SliceStable(ordered, func(i, j int) bool {
			li, lj := ordered[i].PeerLinkID == nil, ordered[j].PeerLinkID == nil
			if li != lj {
				return li
			}
			return ordered[i].ID < ordered[j].ID
		})
		for _, sv := range ordered {
			sm := svcMCP[sv.ID]
			if sm == nil || len(sm.Tools) == 0 {
				continue
			}
			endpoint, _, _ := serviceMCPInfo(sv)
			if endpoint == "" {
				continue
			}
			for _, ut := range sm.Tools {
				base := upstreamToolName(sv, ut.Name)
				if base == "" {
					continue
				}
				name := base
				for n := 0; seen[name]; n++ {
					sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00mcp\x00%s\x00%d", sv.Slug, ut.Name, n)))
					suffix := "_" + hex.EncodeToString(sum[:])[:6]
					b := base
					if len(b) > 64-len(suffix) {
						b = b[:64-len(suffix)]
					}
					name = b + suffix
				}
				seen[name] = true
				out = append(out, upstreamMCPTool(sv, sm, ut, name))
			}
		}
	}
	return append(out, capabilityMCPTools(caps, out)...)
}

func upstreamMCPTool(sv *store.Service, sm *store.ServiceMCP, ut store.ServiceMCPTool, name string) mcpTool {
	title := strings.TrimSpace(ut.Title)
	if title == "" {
		title = ut.Name
	}
	fullTitle := sv.Name + ": " + title
	desc := strings.TrimSpace(ut.Description)
	if desc == "" {
		desc = fmt.Sprintf("%s on %s (upstream MCP tool).", title, sv.Name)
	}
	schema := ut.InputSchema
	if schema == nil {
		schema = obj(map[string]any{})
	}
	ann := map[string]any{}
	for k, v := range ut.Annotations {
		ann[k] = v
	}
	if _, ok := ann["title"]; !ok {
		ann["title"] = fullTitle
	}
	if _, ok := ann["openWorldHint"]; !ok {
		ann["openWorldHint"] = true
	}
	// Clients see the effective state, so an admin's override shows up as
	// the tool's hint too.
	ro := ut.ReadOnly()
	ann["readOnlyHint"] = ro
	return mcpTool{
		Name:         name,
		Title:        fullTitle,
		Description:  desc,
		InputSchema:  schema,
		OutputSchema: ut.OutputSchema,
		Annotations:  ann,
		Set:          setService,
		service:      sv.Slug,
		upstreamMCP:  true,
		upstreamTool: ut.Name,
		upstreamRO:   ro,
		keywords:     strings.Join([]string{sv.Slug, sv.Name, sv.Provider, sv.Category, sm.ServerName, ut.Name, ut.Title, "mcp"}, " "),
	}
}

// capabilityMCPTools makes one tool per capability the app may call, with the
// capability's params as its input. A name a service tool already took gets
// a "_any" suffix.
func capabilityMCPTools(cs []*store.Capability, taken []mcpTool) []mcpTool {
	used := map[string]bool{}
	for _, set := range [][]mcpTool{coreTools, insightTools, manageTools, discoveryTools, taken} {
		for _, t := range set {
			used[t.Name], used[toolPrefix+t.Name] = true, true
		}
	}
	out := []mcpTool{}
	for _, c := range cs {
		name := toolName(c.Slug)
		for used[name] {
			name = toolName(name + "_any")
		}
		used[name] = true
		props := map[string]any{}
		required := []string{}
		for _, p := range c.Params {
			typ := orDefault(p.Type, "string")
			prop := map[string]any{"type": typ}
			if p.Description != "" {
				prop["description"] = p.Description
			}
			if p.Default != nil {
				prop["default"] = p.Default
			}
			props[p.Name] = prop
			if p.Required && p.Default == nil {
				required = append(required, p.Name)
			}
		}
		routes := []string{}
		// A capability only reads when every route it may take is a GET or
		// HEAD, so read-only apps can use it.
		reads := true
		calls := []string{}
		for _, rt := range c.Routes {
			if !rt.Disabled {
				routes = append(routes, rt.Service)
				m := strings.ToUpper(orDefault(rt.Method, http.MethodGet))
				if m != http.MethodGet && m != http.MethodHead {
					reads = false
				}
				calls = append(calls, endpointKey(rt.Service, m, rt.Path))
			}
		}
		reads = reads && len(routes) > 0
		desc := strings.TrimSpace(c.Description + " Served by " + strings.Join(routes, ", ") +
			": Acnologia picks one (" + c.Strategy + "), rotates keys, and moves to the next provider when one cannot serve the request.")
		ann := map[string]any{"title": c.Name, "openWorldHint": true, "readOnlyHint": reads}
		if reads {
			ann["destructiveHint"] = false
		}
		out = append(out, mcpTool{
			Name: name, Title: c.Name, Description: desc, Set: setService,
			InputSchema: obj(props, required...),
			Annotations: ann,
			capability:  c.Slug,
			routes:      routes,
			routeCalls:  calls,
			keywords:    strings.Join(append([]string{c.Slug, c.Name}, routes...), " "),
			searchText:  c.Description,
		})
	}
	return out
}

var placeholderRe = regexp.MustCompile(`\{\{?[^{}]*\}?\}`)

// endpointKey names one endpoint of a service, with every path placeholder
// ({id} in a provider example, {{param}} in a capability route) alike.
func endpointKey(service, method, path string) string {
	p := strings.ToLower(strings.Trim(placeholderRe.ReplaceAllString(path, "*"), "/"))
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return service + " " + strings.ToUpper(orDefault(method, http.MethodGet)) + " " + p
}

type toolKey struct {
	service int64
	example int
}

// serviceToolNames gives every service example a unique tool name. A slug
// and example can join into the same name as another service's ("google" +
// "Maps geocode" and "google-maps" + "geocode"), and a service shared by
// another server picks its own slug and examples. So the built in tool
// names are reserved, this server's own services are named first, oldest
// first, and a name already taken gets a suffix derived from the service
// slug and example. A peer service can never take a local tool's name, and
// the names do not depend on how the services are sorted.
func serviceToolNames(svcs []*store.Service, a *store.App) map[toolKey]string {
	seen := map[string]bool{}
	for _, set := range [][]mcpTool{coreTools, insightTools, manageTools, discoveryTools} {
		for _, t := range set {
			seen[t.Name], seen[toolPrefix+t.Name] = true, true
		}
	}
	ordered := make([]*store.Service, 0, len(svcs))
	for _, sv := range svcs {
		if a.CanUse(sv.Slug) {
			ordered = append(ordered, sv)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		li, lj := ordered[i].PeerLinkID == nil, ordered[j].PeerLinkID == nil
		if li != lj {
			return li
		}
		return ordered[i].ID < ordered[j].ID
	})
	names := map[toolKey]string{}
	for _, sv := range ordered {
		for i, src := range toolSources(docSpec(sv)) {
			name := toolName(sv.Slug, src.name)
			if name == "" {
				continue
			}
			for n := 0; seen[name]; n++ {
				sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", sv.Slug, src.name, n)))
				suffix := "_" + hex.EncodeToString(sum[:])[:6]
				base := toolName(sv.Slug, src.name)
				if len(base) > 64-len(suffix) {
					base = base[:64-len(suffix)]
				}
				name = base + suffix
			}
			seen[name] = true
			names[toolKey{sv.ID, i}] = name
		}
	}
	return names
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
	return s.mcpToolSet(ctx, a, st, true)
}

// isStaticTool reports whether name is a core, insight or manage tool.
// serviceToolNames never gives a service tool one of these names.
func isStaticTool(name string) bool {
	for _, set := range [][]mcpTool{coreTools, insightTools, manageTools, discoveryTools} {
		if findTool(set, name) != nil {
			return true
		}
	}
	return false
}

// mcpTool finds the tool a tools/call names, with the same settings and
// mode checks as mcpTools. A static tool name never matches a service tool,
// so the service tools (every service's examples) are built only when the
// name is not a static one.
func (s *Server) mcpTool(ctx context.Context, a *store.App, st store.MCPSettings, name string) *mcpTool {
	name = canonicalTool(name)
	if isStaticTool(name) {
		return findTool(s.mcpToolSet(ctx, a, st, false), name)
	}
	if !st.ServiceTools || !a.AllowsProxy() {
		return nil
	}
	if disabledSet(st.DisabledTools)[name] {
		return nil
	}
	c := s.toolCatalog(ctx)
	id, ok := c.byName[name]
	if !ok || !c.view(a).allowed[id] {
		return nil
	}
	t := c.tools[id]
	if st.ReadOnly && !toolIsReadOnly(t) {
		return nil
	}
	return &t
}

func (s *Server) mcpToolSet(ctx context.Context, a *store.App, st store.MCPSettings, withService bool) []mcpTool {
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
		all = append(all, discoveryTools...)
		if withService {
			all = append(all, s.serviceTools(ctx, a)...)
		}
	}
	if st.Insights {
		all = append(all, insightTools...)
	}
	if st.Manage {
		all = append(all, manageTools...)
	}
	off := disabledSet(st.DisabledTools)
	out := make([]mcpTool, 0, len(all))
	for _, t := range all {
		if off[t.Name] {
			continue
		}
		if st.ReadOnly && !toolIsReadOnly(t) {
			continue
		}
		out = append(out, t)
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
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	if err := checkToolArgs(t, args); err != nil {
		return nil, err
	}
	if t.capability != "" {
		if ro, _ := ctx.Value(mcpReadOnlyKey).(bool); ro && toolIsReadOnly(*t) {
			// A read-only token may only send GET through the proxy, and
			// every route of this capability reads, so the params go in
			// the query, where the capability takes them too.
			q := map[string]string{}
			for k, v := range args {
				if sv, ok := v.(string); ok {
					q[k] = sv
				} else if v != nil {
					b, _ := json.Marshal(v)
					q[k] = string(b)
				}
			}
			return s.mcpProxy(ctx, r, t.capability, http.MethodGet, "", q, nil, nil)
		}
		body := raw
		if len(args) == 0 {
			body = json.RawMessage(`{}`)
		}
		return s.mcpProxy(ctx, r, t.capability, http.MethodPost, "", nil, body, nil)
	}
	var hdr map[string]string
	idemArg := hasProp(t.InputSchema, "idempotency_key") && !slices.Contains(t.params, "idempotency_key")
	if k, ok := args["idempotency_key"].(string); ok && idemArg && strings.TrimSpace(k) != "" {
		hdr = map[string]string{"Idempotency-Key": strings.TrimSpace(k)}
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
	return s.mcpProxy(ctx, r, t.service, orDefault(t.example.Method, "GET"), path, query, body, hdr)
}

// capControlArgs are the capability's own controls (pin one route, skip
// the output mapping), which it takes next to its params.
var capControlArgs = map[string]bool{"_service": true, "_raw": true}

// checkToolArgs rejects argument names a generated service tool or a
// capability does not take, before anything is sent upstream: dropping
// them quietly would answer a different question than the one asked. A
// schema that allows other properties takes anything; an upstream MCP
// tool's schema is the upstream's to enforce.
func checkToolArgs(t *mcpTool, args map[string]any) error {
	if t.upstreamMCP || len(args) == 0 {
		return nil
	}
	if ap, ok := t.InputSchema["additionalProperties"]; ok && ap != false {
		return nil
	}
	props, _ := t.InputSchema["properties"].(map[string]any)
	var unknown []string
	for k := range args {
		if _, ok := props[k]; ok || (t.capability != "" && capControlArgs[k]) {
			continue
		}
		unknown = append(unknown, k)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	accepted := make([]string, 0, len(props))
	for k := range props {
		accepted = append(accepted, k)
	}
	sort.Strings(accepted)
	msg := fmt.Sprintf("%s does not take %s. It takes: %s.", t.Name, quoteList(unknown), strings.Join(accepted, ", "))
	if _, ok := props["extra_query"]; ok {
		msg += " Put other query parameters in extra_query."
	}
	return errors.New(msg)
}

func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	return strings.Join(q, ", ")
}

func hasProp(schema map[string]any, name string) bool {
	props, _ := schema["properties"].(map[string]any)
	_, ok := props[name]
	return ok
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
		return nil, nil, fmt.Errorf("no key %d for this app; list_keys shows the key ids", id)
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
		Limit    int    `json:"limit"`
		Offset   int    `json:"offset"`
		Keys     []struct {
			Label  string            `json:"label"`
			Secret string            `json:"secret"`
			Parts  map[string]string `json:"parts"`
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
		matched := keys[:0:0]
		for _, k := range keys {
			if args.Validity == "" || k.Validity == args.Validity {
				matched = append(matched, k)
			}
		}
		page, pg := paginate(matched, args.Offset, args.Limit, 50, 200)
		list := make([]map[string]any, 0, len(page))
		for _, k := range page {
			list = append(list, s.mcpKeyView(k, bySvc[k.ServiceID]))
		}
		return pg.into(map[string]any{"keys": list}), nil
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
		// Each key is checked only against its own service: a check sends
		// the secret to the service's base URL.
		if args.Service == "" || !a.CanUse(args.Service) {
			return nil, fmt.Errorf("this app cannot use service %q", args.Service)
		}
		sv, err := s.Store.GetServiceBySlug(ctx, args.Service)
		if err != nil {
			return nil, fmt.Errorf("no service named %q", args.Service)
		}
		keys, err := s.Store.ListKeys(ctx, sv.ID, a.LeaseHidden)
		if err != nil {
			return nil, err
		}
		s.fillInFlight(keys...)
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
		auth := serviceAuth(sv)
		for _, in := range args.Keys {
			secret, err := auth.NormalizeSecret(in.Secret, in.Parts)
			if err != nil {
				skipped++
				continue
			}
			enc, err := s.Pool.Box.Encrypt(secret)
			if err != nil {
				return nil, err
			}
			hint := auth.SecretHint(secret)
			label := strings.TrimSpace(in.Label)
			if label == "" {
				label = sv.Name + " key " + hint[3:]
			}
			k, err := s.Store.CreateKey(ctx, store.NewKey{ServiceID: sv.ID, Label: label, SecretEnc: enc, SecretFP: s.Pool.Box.Fingerprint(secret),
				Hint: hint, Tier: "primary", Weight: 1, Priority: 100})
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
		out := map[string]any{"added": added, "skipped": skipped}
		// The new keys are checked in the background as a job, the same one
		// the admin add keys route starts (jobs.go). The job carries this
		// call's attribution; it has no request of its own.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp", nil)
		if err != nil {
			go s.checkMany(sv, created)
		} else if job := s.checkNewKeys(req, sv, created); job != nil {
			out["check_job_id"] = job.ID
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown tool %q", name)
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
		return text("text/markdown", publicText(d.Render(false))), nil
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
			"how_to_call": publicText("Use proxy_request with service=" + sv.Slug + " and a path, or the generated tool for an example. Acnologia adds the key."),
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
		return map[string]any{"messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": publicText(t)}}}}
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
