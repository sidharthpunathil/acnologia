package api

// GET /api/openapi.json: an OpenAPI 3.1 description of every route the
// server has. The /api/admin routes are described from the control MCP's
// tool table (their inputs are typed there), and the rest, the consumer API,
// the proxy, MCP, OAuth, sign in and sharing, from apiDocs below. The spec
// is built from the live router, and TestOpenAPICoversEveryRoute fails when
// a route is neither described nor listed in openAPIExcluded, so the two
// cannot drift. list_routes reads the same descriptions. The spec holds no
// secrets: it is open like /docs.

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// apiDoc describes a route no control tool covers.
type apiDoc struct {
	Summary string
	Desc    string
	// Auth names the security scheme: admin (session or admin API token),
	// session (dashboard session only), app (app token), oauth (app token
	// or OAuth access token), share (a share token), or none.
	Auth string
	Role store.Role
	Tag  string
	// Body holds the JSON body's properties; Form marks a form encoded body.
	Body     map[string]any
	Required []string
	Form     bool
	Query    map[string]any
	// Produces is the response content type when it is not JSON.
	Produces string
}

// openAPIExcluded are routes the spec leaves out on purpose, with why.
var openAPIExcluded = map[string]string{
	"GET /robots.txt":           "crawler rules, not an API",
	"GET /docs/assets/{name}":   "images the docs pages embed",
	"* /api/v1/demo/upstream/*": "the fake provider behind the demo service; reached only through /proxy/demo with a demo key",
}

var anyObj = map[string]any{"type": "object", "description": "JSON object"}

// apiDocs documents the routes outside the control MCP, keyed "METHOD
// path" with the router's path. A route registered for every method (the
// proxy, MCP, OAuth endpoints) is documented by the methods it serves.
var apiDocs = map[string]apiDoc{
	// ---- open ----
	"GET /healthz":               {Summary: "Health check", Desc: "Liveness and the server version: {ok, version}.", Auth: "none", Tag: "public"},
	"GET /llms.txt":              {Summary: "llms.txt", Desc: "Short guide to this server for language models.", Auth: "none", Tag: "public", Produces: "text/plain"},
	"GET /llms-full.txt":         {Summary: "llms-full.txt", Desc: "The full reference for language models.", Auth: "none", Tag: "public", Produces: "text/plain"},
	"GET /api/docs":              {Summary: "List docs", Desc: "Every doc page: slug, title, first paragraph and size.", Auth: "none", Tag: "public"},
	"GET /docs/{slug}.md":        {Summary: "Read a doc", Desc: "One doc page as Markdown.", Auth: "none", Tag: "public", Produces: "text/markdown"},
	"GET /api/openapi.json":      {Summary: "OpenAPI spec", Desc: "This document.", Auth: "none", Tag: "public"},
	"GET /api/v1":                {Summary: "Agent index", Desc: "Where an agent starts: version, setup_required, gated, auth hints, every endpoint URL and start_here steps.", Auth: "none", Tag: "public"},
	"GET /api/public/github":     {Summary: "GitHub stars", Desc: "The project's GitHub star count, cached.", Auth: "none", Tag: "public"},
	"GET /.well-known/acnologia": {Summary: "Server identity", Desc: "This server's public sharing identity: name, URL and keys.", Auth: "none", Tag: "sharing"},

	// ---- access gate ----
	"GET /api/gate":         {Summary: "Lock state", Desc: "Whether the access password lock is on and this browser is past it.", Auth: "none", Tag: "auth"},
	"POST /api/gate/unlock": {Summary: "Unlock", Desc: "Unlock the site with the access password; sets the gate cookie.", Auth: "none", Tag: "auth", Body: map[string]any{"password": sProp("The access password")}, Required: []string{"password"}},
	"POST /api/gate/lock":   {Summary: "Lock", Desc: "Forget the gate cookie.", Auth: "none", Tag: "auth"},

	// ---- sign in ----
	"GET /api/auth/state":   {Summary: "Sign in state", Desc: "{setup_required, user, password_only}.", Auth: "none", Tag: "auth"},
	"POST /api/auth/setup":  {Summary: "First setup", Desc: "Create the first superadmin and sign in. Only while there are no users.", Auth: "none", Tag: "auth", Body: map[string]any{"email": sProp("Email"), "name": sProp("Display name"), "password": sProp("Password, 10 to 72 characters")}, Required: []string{"email", "password"}},
	"POST /api/auth/login":  {Summary: "Sign in", Desc: "Sign in with email and password; sets the session cookie.", Auth: "none", Tag: "auth", Body: map[string]any{"email": sProp("Email"), "password": sProp("Password")}, Required: []string{"email", "password"}},
	"POST /api/auth/logout": {Summary: "Sign out", Desc: "End the dashboard session.", Auth: "session", Tag: "auth"},

	// ---- CLI sign in (device flow) ----
	"POST /api/cli/device":         {Summary: "Start a CLI sign in", Desc: "Start the device flow: a device code to poll and a user code to approve in the browser.", Auth: "none", Tag: "cli", Body: map[string]any{"client": sProp("Client name, such as acn"), "hostname": sProp("Machine name"), "os": sProp("Operating system")}},
	"POST /api/cli/device/token":   {Summary: "Poll a CLI sign in", Desc: "Poll with the device code until the sign in is approved; then the admin API token is in the answer.", Auth: "none", Tag: "cli", Body: map[string]any{"device_code": sProp("The device code")}, Required: []string{"device_code"}},
	"GET /api/cli/device/lookup":   {Summary: "Look up a CLI sign in", Desc: "What a user code asks for, for the approval page.", Auth: "session", Tag: "cli", Query: map[string]any{"user_code": sProp("The user code")}},
	"POST /api/cli/device/approve": {Summary: "Approve a CLI sign in", Desc: "Approve a user code with a token scope.", Auth: "session", Tag: "cli", Body: map[string]any{"user_code": sProp("The user code"), "scope": eProp("Token scope", "read", "write", "admin"), "label": sProp("Token name")}, Required: []string{"user_code"}},
	"POST /api/cli/device/deny":    {Summary: "Deny a CLI sign in", Desc: "Refuse a user code.", Auth: "session", Tag: "cli", Body: map[string]any{"user_code": sProp("The user code")}, Required: []string{"user_code"}},

	// ---- admin routes without a control tool ----
	"PATCH /api/admin/me":                               {Summary: "Update your account", Desc: "Change your name or password. Session only.", Auth: "session", Role: store.RoleMember, Tag: "account", Body: map[string]any{"name": sProp("Display name"), "password": sProp("New password"), "current_password": sProp("Current password")}},
	"GET /api/admin/tokens":                             {Summary: "List admin API tokens", Desc: "Your admin API tokens (all with ?all=1 for a superadmin). Session only.", Auth: "session", Role: store.RoleMember, Tag: "account", Query: map[string]any{"all": sProp("1 lists every user's tokens (superadmin)")}},
	"POST /api/admin/tokens":                            {Summary: "Create an admin API token", Desc: "The token is in the answer, shown once. Session only.", Auth: "session", Role: store.RoleMember, Tag: "account", Body: map[string]any{"name": sProp("Token name"), "scope": eProp("read acts as a member, write as an admin, admin as a superadmin", "read", "write", "admin"), "expires_in_days": iProp("0 never expires, up to 3650")}, Required: []string{"name", "scope"}},
	"DELETE /api/admin/tokens/{id}":                     {Summary: "Revoke an admin API token", Desc: "A session, or the token itself (logout).", Auth: "admin", Role: store.RoleMember, Tag: "account"},
	"GET /api/admin/services/{id}/keys":                 {Summary: "List a service's keys", Desc: "The keys of one service, never their secrets. The control tool list_keys takes service instead.", Auth: "admin", Role: store.RoleMember, Tag: "keys"},
	"GET /api/admin/mcp/control":                        {Summary: "Control MCP tools", Desc: "The control MCP endpoint, its groups and every tool with whether your role can use it.", Auth: "admin", Role: store.RoleMember, Tag: "mcp"},
	"GET /api/admin/chat/threads/{id}/images/{image}":   {Summary: "A chat image", Desc: "An image sent in one of your Assistant chats.", Auth: "admin", Role: store.RoleMember, Tag: "ai", Produces: "image/*"},
	"GET /api/admin/chat/threads/{id}/logos/{logo}":     {Summary: "A chat logo candidate", Desc: "A logo the Assistant found, for its ask form.", Auth: "admin", Role: store.RoleMember, Tag: "ai", Produces: "image/*"},
	"GET /api/logos/providers/{slug}":                   {Summary: "Provider logo", Auth: "admin", Role: store.RoleMember, Tag: "providers", Produces: "image/*"},
	"GET /api/logos/services/{id}":                      {Summary: "Service logo", Auth: "admin", Role: store.RoleMember, Tag: "services", Produces: "image/*"},
	"GET /api/oauth/requests/{rid}":                     {Summary: "An OAuth consent request", Desc: "What an MCP client asks for, for the consent page.", Auth: "session", Role: store.RoleMember, Tag: "oauth"},
	"POST /api/oauth/requests/{rid}/approve":            {Summary: "Approve an OAuth request", Desc: "Grant the MCP client access as an app.", Auth: "session", Role: store.RoleMember, Tag: "oauth", Body: map[string]any{"app_id": iProp("App the client acts as"), "new_app": sProp("Or the name of a new app"), "read_only": bProp("Grant read only access")}},
	"POST /api/oauth/requests/{rid}/deny":               {Summary: "Deny an OAuth request", Auth: "session", Role: store.RoleMember, Tag: "oauth"},
	"GET /api/oauth/apps/{id}/connections":              {Summary: "An app's OAuth connections", Desc: "MCP clients signed in to an app through OAuth.", Auth: "admin", Role: store.RoleMember, Tag: "oauth"},
	"DELETE /api/oauth/apps/{id}/connections/{grantId}": {Summary: "Revoke an OAuth connection", Auth: "admin", Role: store.RoleAdmin, Tag: "oauth"},

	// ---- consumer API ----
	"GET /api/v1/usage/tags":                          {Summary: "Use per caller tag (app)", Desc: "The app's own use per X-Acnologia-Meta tag value, most expensive first, with its tag budgets.", Auth: "app", Tag: "consumer", Query: map[string]any{"days": iProp("Days of history, default 30")}},
	"GET /api/v1/events/stream":                       {Summary: "Event stream (app)", Desc: "The app's events and the notices for services it may use, as server sent events. Resume with Last-Event-ID.", Auth: "app", Tag: "consumer", Produces: "text/event-stream"},
	"GET /api/admin/events/stream":                    {Summary: "Event stream", Desc: "Every event and notification as server sent events. Resume with Last-Event-ID.", Auth: "admin", Role: store.RoleMember, Tag: "insights", Produces: "text/event-stream"},
	"GET /api/admin/jobs":                             {Summary: "List jobs", Desc: "Background jobs started with ?async=1: check-all, service checks, peer and MCP syncs, new key checks.", Auth: "admin", Role: store.RoleMember, Tag: "insights"},
	"GET /api/admin/jobs/{id}":                        {Summary: "Get a job", Desc: "One job's status, progress, result or error.", Auth: "admin", Role: store.RoleMember, Tag: "insights"},
	"GET /api/admin/apps/{id}/controls":               {Summary: "App access controls", Desc: "The app's per-token default limits, tag budgets and deny rules.", Auth: "admin", Role: store.RoleAdmin, Tag: "apps"},
	"PUT /api/admin/apps/{id}/token-limits":           {Summary: "Set per-token default limits", Desc: "Daily and monthly request caps and USD budgets every token of the app gets unless it has its own.", Auth: "admin", Role: store.RoleAdmin, Tag: "apps", Body: map[string]any{"limits": anyObj}},
	"PUT /api/admin/apps/{id}/tag-budgets":            {Summary: "Set tag budgets", Desc: "Limits per caller tag value, enforced by reservation.", Auth: "admin", Role: store.RoleAdmin, Tag: "apps", Body: map[string]any{"budgets": aProp(anyObj, "The budgets: {key, daily_requests, monthly_requests, daily_budget, monthly_budget}; replaces the list")}},
	"GET /api/admin/apps/{id}/usage/tags":             {Summary: "Use per caller tag", Desc: "The app's use per X-Acnologia-Meta tag value.", Auth: "admin", Role: store.RoleAdmin, Tag: "apps", Query: map[string]any{"days": iProp("Days of history, default 30")}},
	"GET /api/admin/apps/{id}/deny-rules":             {Summary: "List deny rules", Desc: "The app's deny rules.", Auth: "admin", Role: store.RoleAdmin, Tag: "apps"},
	"POST /api/admin/apps/{id}/deny-rules":            {Summary: "Add a deny rule", Desc: "Block requests by method, service or host, and path prefix.", Auth: "admin", Role: store.RoleAdmin, Tag: "apps", Body: map[string]any{"method": sProp("HTTP method or *"), "service": sProp("Service slug"), "host": sProp("Host, for the forward proxy"), "path_prefix": sProp("Path prefix"), "note": sProp("Why")}},
	"DELETE /api/admin/apps/{id}/deny-rules/{ruleId}": {Summary: "Delete a deny rule", Auth: "admin", Role: store.RoleAdmin, Tag: "apps"},
	"GET /api/admin/settings/spend-ceiling":           {Summary: "Server spend ceiling", Desc: "The server's daily spend ceiling in USD, where it comes from, and today's spend.", Auth: "admin", Role: store.RoleSuperadmin, Tag: "settings"},
	"PUT /api/admin/settings/spend-ceiling":           {Summary: "Set the server spend ceiling", Desc: "An amount in USD, 0 for off, or null to fall back to ACNOLOGIA_DAILY_SPEND_CEILING_USD.", Auth: "admin", Role: store.RoleSuperadmin, Tag: "settings", Body: map[string]any{"usd": anyObj}},
	"POST /api/admin/services/{id}/try":               {Summary: "Try it", Desc: "Send one request through the proxy as a chosen app, or the internal Try it app, and get status, headers, latency and body. Logged as a try event.", Auth: "admin", Role: store.RoleAdmin, Tag: "services", Body: map[string]any{"method": sProp("HTTP method"), "path": sProp("Path on the service"), "query": anyObj, "body": anyObj, "app_id": iProp("App to try as"), "read_only": map[string]any{"type": "boolean"}}},
	"POST /api/admin/backup":                          {Summary: "Download a backup", Desc: "A consistent copy of the database as a file (acnologia-backup-<time>.db), streamed. Stored keys stay encrypted: restoring it needs the same ACNOLOGIA_MASTER_KEY, which is never in the copy. Dashboard sessions are left out. The control tool acnologia_backup_database writes the copy on the server instead.", Auth: "admin", Role: store.RoleSuperadmin, Tag: "settings", Produces: "application/vnd.sqlite3"},
	"GET /api/admin/demo":                             {Summary: "Demo data state", Desc: "Whether demo data is loaded, and what it made.", Auth: "admin", Role: store.RoleSuperadmin, Tag: "services"},
	"POST /api/admin/demo":                            {Summary: "Load demo data", Desc: "Add a demo service backed by a built-in fake provider, two keys, an app and a token, for a first call with no real key. Refused on a server with services unless force is true.", Auth: "admin", Role: store.RoleSuperadmin, Tag: "services", Body: map[string]any{"force": map[string]any{"type": "boolean"}}},
	"DELETE /api/admin/demo":                          {Summary: "Remove demo data", Desc: "Delete exactly what POST /api/admin/demo made.", Auth: "admin", Role: store.RoleSuperadmin, Tag: "services"},
	"GET /api/v1/me":                                  {Summary: "Who am I (app)", Desc: "The app the token belongs to, its mode and services; a stopped share says why.", Auth: "app", Tag: "consumer"},
	"GET /api/v1/proxy/ca.pem":                        {Summary: "Forward proxy CA", Desc: "The CA certificate apps trust to use Acnologia as their HTTPS_PROXY.", Auth: "none", Tag: "consumer", Produces: "application/x-pem-file"},
	"GET /api/v1/proxy":                               {Summary: "Forward proxy setup", Desc: "The HTTPS_PROXY URL for this app and the hosts that get keys.", Auth: "app", Tag: "consumer"},
	"GET /api/v1/services":                            {Summary: "List services (app)", Desc: "The services this app may use, with their proxy URLs.", Auth: "app", Tag: "consumer"},
	"GET /api/v1/services/{slug}/guide":               {Summary: "Service guide (app)", Desc: "How to call one service: endpoints, params, limits and errors.", Auth: "app", Tag: "consumer"},
	"GET /api/v1/capabilities":                        {Summary: "List capabilities (app)", Desc: "Capabilities this app may call at /proxy/<slug>.", Auth: "app", Tag: "consumer"},
	"POST /api/v1/lease":                              {Summary: "Lease a key", Desc: "Lease a pooled key for a service (lease or both mode apps only). Report the outcome when done.", Auth: "app", Tag: "consumer", Body: map[string]any{"service": sProp("Service slug"), "exclude_key_ids": aProp(map[string]any{"type": "integer"}, "Keys not to hand out"), "ttl_sec": iProp("Lease length"), "wait_sec": iProp("Seconds to wait for a free key"), "exclusive": bProp("Keep the key to this lease alone")}, Required: []string{"service"}},
	"POST /api/v1/leases/{id}/report":                 {Summary: "Report a lease", Desc: "Say how the leased key did; rotate asks for the next key.", Auth: "app", Tag: "consumer", Body: map[string]any{"outcome": eProp("What happened", "ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error"), "status_code": iProp("Provider HTTP status"), "retry_after_sec": iProp("Retry-After from the provider"), "message": sProp("Provider message"), "rotate": bProp("Lease the next key in the same call")}, Required: []string{"outcome"}},
	"POST /api/v1/leases/{id}/renew":                  {Summary: "Renew a lease", Auth: "app", Tag: "consumer", Body: map[string]any{"ttl_sec": iProp("New lease length")}},
	"GET /proxy/{service}/{path}":                     {Summary: "Proxy GET", Desc: "Call the provider through Acnologia: a pooled key is injected and rotated on rate limits. Any path and query after the service slug go to the provider as is. Read only tokens may only GET, HEAD and OPTIONS.", Auth: "app", Tag: "proxy", Produces: "*/*"},
	"POST /proxy/{service}/{path}":                    {Summary: "Proxy POST", Desc: "As GET; the body goes to the provider as is.", Auth: "app", Tag: "proxy", Produces: "*/*"},
	"PUT /proxy/{service}/{path}":                     {Summary: "Proxy PUT", Auth: "app", Tag: "proxy", Produces: "*/*"},
	"PATCH /proxy/{service}/{path}":                   {Summary: "Proxy PATCH", Auth: "app", Tag: "proxy", Produces: "*/*"},
	"DELETE /proxy/{service}/{path}":                  {Summary: "Proxy DELETE", Auth: "app", Tag: "proxy", Produces: "*/*"},
	"GET /proxy/{service}":                            {Summary: "Proxy GET (root)", Desc: "The provider's base URL itself, or a capability call.", Auth: "app", Tag: "proxy", Produces: "*/*"},
	"POST /proxy/{service}":                           {Summary: "Proxy POST (root)", Desc: "The provider's base URL, or a capability call with its params as the JSON body.", Auth: "app", Tag: "proxy", Produces: "*/*"},

	// ---- MCP ----
	"POST /mcp":           {Summary: "App MCP", Desc: "Streamable HTTP MCP (JSON-RPC 2.0) for apps: lease, proxy and the generated service tools.", Auth: "oauth", Tag: "mcp", Body: map[string]any{"jsonrpc": sProp("2.0"), "id": map[string]any{"description": "Request id"}, "method": sProp("JSON-RPC method"), "params": anyObj}},
	"DELETE /mcp":         {Summary: "End an MCP session", Auth: "oauth", Tag: "mcp"},
	"POST /mcp/control":   {Summary: "Control MCP", Desc: "Streamable HTTP MCP (JSON-RPC 2.0) with every dashboard feature as a tool, as the admin API token's owner. Tools are described with x-mcp-tool on the admin routes.", Auth: "admin_token", Tag: "mcp", Body: map[string]any{"jsonrpc": sProp("2.0"), "id": map[string]any{"description": "Request id"}, "method": sProp("JSON-RPC method"), "params": anyObj}},
	"DELETE /mcp/control": {Summary: "End a control MCP session", Auth: "admin_token", Tag: "mcp"},

	// ---- OAuth for /mcp ----
	"GET /.well-known/oauth-protected-resource":     {Summary: "Protected resource metadata", Desc: "RFC 9728 metadata for /mcp.", Auth: "none", Tag: "oauth"},
	"GET /.well-known/oauth-protected-resource/mcp": {Summary: "Protected resource metadata (/mcp)", Desc: "RFC 9728 metadata for /mcp.", Auth: "none", Tag: "oauth"},
	"GET /.well-known/oauth-authorization-server":   {Summary: "Authorization server metadata", Desc: "RFC 8414 metadata.", Auth: "none", Tag: "oauth"},
	"GET /oauth/authorize": {Summary: "Authorize", Desc: "Start the authorization code flow with PKCE; the browser goes on to the consent page.", Auth: "none", Tag: "oauth", Produces: "text/html",
		Query: map[string]any{"response_type": sProp("code"), "client_id": sProp("Client id from registration"), "redirect_uri": sProp("Registered redirect URI"),
			"code_challenge": sProp("PKCE challenge"), "code_challenge_method": sProp("S256"), "state": sProp("Opaque state"), "scope": sProp("Scope"), "resource": sProp("The MCP URL")}},
	"POST /oauth/register": {Summary: "Register a client", Desc: "RFC 7591 dynamic client registration.", Auth: "none", Tag: "oauth",
		Body: map[string]any{"client_name": sProp("Client name"), "redirect_uris": aProp(map[string]any{"type": "string"}, "Redirect URIs")}, Required: []string{"redirect_uris"}},
	"POST /oauth/token": {Summary: "Token", Desc: "Exchange a code (with the PKCE verifier) or a refresh token for an access token.", Auth: "none", Tag: "oauth", Form: true,
		Body: map[string]any{"grant_type": eProp("Grant type", "authorization_code", "refresh_token"), "code": sProp("Authorization code"), "redirect_uri": sProp("Redirect URI"),
			"code_verifier": sProp("PKCE verifier"), "client_id": sProp("Client id"), "refresh_token": sProp("Refresh token")}, Required: []string{"grant_type"}},
	"POST /oauth/revoke": {Summary: "Revoke a token", Desc: "RFC 7009 token revocation.", Auth: "none", Tag: "oauth", Form: true, Body: map[string]any{"token": sProp("The token")}, Required: []string{"token"}},

	// ---- sharing between servers and with people ----
	"POST /api/peer/v1/preview":              {Summary: "Preview an invite (server to server)", Desc: "What an invite offers. Part of the server to server sharing protocol.", Auth: "none", Tag: "sharing", Body: map[string]any{"share_id": sProp("Share id"), "invite": sProp("Invite code")}},
	"POST /api/peer/v1/redeem":               {Summary: "Redeem an invite (server to server)", Desc: "Part of the server to server sharing protocol.", Auth: "none", Tag: "sharing", Body: anyProps},
	"GET /api/peer/v1/manifest":              {Summary: "Share manifest", Desc: "The services and providers a share holds.", Auth: "share", Tag: "sharing"},
	"POST /api/peer/v1/leave":                {Summary: "Leave a share", Auth: "share", Tag: "sharing"},
	"GET /api/peer/v1/providers/{slug}/logo": {Summary: "Shared provider logo", Auth: "share", Tag: "sharing", Produces: "image/*"},
	"GET /api/peer/v1/keys":                  {Summary: "Handed over keys", Desc: "Sealed keys of a handover share.", Auth: "share", Tag: "sharing"},
	"POST /api/peer/v1/report":               {Summary: "Report handed over keys", Desc: "The receiver's key health report.", Auth: "share", Tag: "sharing", Body: anyProps},
	"POST /api/peer/v1/revoke":               {Summary: "Revoke push", Desc: "A signed revoke from the owner server.", Auth: "none", Tag: "sharing", Body: anyProps},
	"POST /api/claim/v1/preview":             {Summary: "Preview a claim link", Desc: "What a person's claim link offers.", Auth: "none", Tag: "sharing", Body: map[string]any{"code": sProp("Claim code")}, Required: []string{"code"}},
	"POST /api/claim/v1/redeem":              {Summary: "Claim a share", Desc: "Redeem a claim link for a token or the revealed keys.", Auth: "none", Tag: "sharing", Body: map[string]any{"code": sProp("Claim code")}, Required: []string{"code"}},
}

// anyProps is the body of a protocol route whose fields are defined by the
// peer package.
var anyProps = map[string]any{}

// walkedRoute is one route of the live router, with chi's mount artifacts
// removed. All is set for a route registered for every method.
type walkedRoute struct {
	Method, Path string
	All          bool
}

func (s *Server) walkRoutes() []walkedRoute {
	routes, ok := s.handler().(chi.Routes)
	if !ok {
		return nil
	}
	methods := map[string][]string{}
	order := []string{}
	_ = chi.Walk(routes, func(method, p string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		p = strings.ReplaceAll(p, "/*/", "/")
		if len(p) > 1 {
			p = strings.TrimSuffix(p, "/")
		}
		if _, seen := methods[p]; !seen {
			order = append(order, p)
		}
		methods[p] = append(methods[p], method)
		return nil
	})
	out := []walkedRoute{}
	for _, p := range order {
		all := false
		for _, m := range methods[p] {
			if m == "CONNECT" || m == "TRACE" {
				all = true
			}
		}
		if all {
			out = append(out, walkedRoute{Method: "*", Path: p, All: true})
			continue
		}
		for _, m := range methods[p] {
			out = append(out, walkedRoute{Method: m, Path: p})
		}
	}
	return out
}

// specPath is a router path in OpenAPI form: chi's trailing wildcard is a
// {path} parameter.
func specPath(p string) string {
	if strings.HasSuffix(p, "/*") {
		return strings.TrimSuffix(p, "*") + "{path}"
	}
	return p
}

var routeParamRe = regexp.MustCompile(`\{[^}]+\}`)

func normRoute(method, p string) string {
	return method + " " + routeParamRe.ReplaceAllString(p, "{}")
}

// ctlRouteTools maps "METHOD path" (placeholders blanked) to the control
// tool that calls the route.
func ctlRouteTools() map[string]*ctlTool {
	out := map[string]*ctlTool{}
	for i := range ctlTools {
		t := &ctlTools[i]
		if t.Path == "" || t.Method == "" {
			continue
		}
		k := normRoute(t.Method, t.Path)
		if _, ok := out[k]; !ok {
			out[k] = t
		}
	}
	return out
}

// ---- operations ----

func security(auth string) []map[string]any {
	switch auth {
	case "admin":
		return []map[string]any{{"adminToken": []string{}}, {"session": []string{}}}
	case "admin_token":
		return []map[string]any{{"adminToken": []string{}}}
	case "session":
		return []map[string]any{{"session": []string{}}}
	case "app":
		return []map[string]any{{"appToken": []string{}}, {"appTokenHeader": []string{}}}
	case "oauth":
		return []map[string]any{{"appToken": []string{}}, {"oauth": []string{}}}
	case "share":
		return []map[string]any{{"appToken": []string{}}}
	}
	return []map[string]any{}
}

func responses(produces string) map[string]any {
	ok := map[string]any{"description": "OK"}
	switch produces {
	case "":
		ok["content"] = map[string]any{"application/json": map[string]any{"schema": anyObj}}
	case "*/*":
		ok["description"] = "The provider's answer, relayed"
	default:
		ok["content"] = map[string]any{produces: map[string]any{"schema": map[string]any{"type": "string"}}}
	}
	errResp := map[string]any{"description": "An error: {error: {code, message, hint, fix}}",
		"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
	return map[string]any{"200": ok, "default": errResp}
}

func jsonBody(props map[string]any, required []string, form bool) map[string]any {
	schema := ctlPublicSchema(obj(props, required...))
	ct := "application/json"
	if form {
		ct = "application/x-www-form-urlencoded"
	}
	return map[string]any{"required": true, "content": map[string]any{ct: map[string]any{"schema": schema}}}
}

func pathParams(p string, desc func(i int, name string) (string, string)) []map[string]any {
	out := []map[string]any{}
	for i, m := range routeParamRe.FindAllString(p, -1) {
		name := m[1 : len(m)-1]
		d, typ := desc(i, name)
		out = append(out, map[string]any{"name": name, "in": "path", "required": true, "description": d, "schema": map[string]any{"type": typ}})
	}
	return out
}

// toolBody is the JSON body a tool sends its route, as properties and the
// required ones; nil when it sends none.
func toolBody(t *ctlTool) (map[string]any, []string) {
	if t.BodyDoc != nil {
		return t.BodyDoc, nil
	}
	names := t.Body
	if len(names) == 0 && t.Prepare != nil {
		skip := map[string]bool{"confirm": true, "dry_run": true, "idempotency_key": true}
		for _, m := range routeParamRe.FindAllString(t.Path, -1) {
			skip[m[1:len(m)-1]] = true
		}
		for _, q := range t.Query {
			skip[q] = true
		}
		names = keysOf(t.Props)
		kept := names[:0]
		for _, n := range names {
			if !skip[n] {
				kept = append(kept, n)
			}
		}
		names = kept
	}
	if len(names) == 0 {
		return nil, nil
	}
	req := map[string]bool{}
	for _, r := range t.Required {
		req[r] = true
	}
	props := map[string]any{}
	required := []string{}
	for _, n := range names {
		k := n
		if r, ok := t.Rename[n]; ok {
			k = r
		}
		if p, ok := t.Props[n]; ok {
			props[k] = p
		} else {
			props[k] = map[string]any{}
		}
		if req[n] {
			required = append(required, k)
		}
	}
	sort.Strings(required)
	return props, required
}

// toolOp describes the route at specP from the tool that calls it.
func toolOp(t *ctlTool, method, specP string) map[string]any {
	toolPH := routeParamRe.FindAllString(t.Path, -1)
	params := pathParams(specP, func(i int, name string) (string, string) {
		if i < len(toolPH) {
			in := toolPH[i][1 : len(toolPH[i])-1]
			switch in {
			case "service":
				return "Service id", "integer"
			case "capability":
				return "Capability id", "integer"
			}
			if p, ok := t.Props[in].(map[string]any); ok {
				d, _ := p["description"].(string)
				typ, _ := p["type"].(string)
				return ctlPublicText(d), orDefault(typ, "string")
			}
		}
		return name, "string"
	})
	for _, q := range t.Query {
		k, desc, typ := q, "", "string"
		if r, ok := t.Rename[q]; ok {
			k = r
		}
		if p, ok := t.Props[q].(map[string]any); ok {
			desc, _ = p["description"].(string)
			typ = orDefault(fmtType(p["type"]), "string")
		}
		if q == "service" {
			desc, typ = "Service id", "integer"
		}
		params = append(params, map[string]any{"name": k, "in": "query", "description": ctlPublicText(desc), "schema": map[string]any{"type": typ}})
	}
	op := map[string]any{
		"operationId": ctlPublicTool(t.Name),
		"summary":     t.Title,
		"description": ctlPublicText(t.Desc) + " Needs the " + string(t.Role) + " role.",
		"tags":        []string{t.Group},
		"security":    security("admin"),
		"responses":   responses(""),
		"x-mcp-tool":  ctlPublicTool(t.Name),
		"x-role":      string(t.Role),
		"x-kind":      t.Kind,
	}
	if isWriteMethod(method) && strings.HasPrefix(specP, "/api/admin/") {
		params = append(params, safetyParams()...)
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		if props, req := toolBody(t); props != nil {
			op["requestBody"] = jsonBody(props, req, false)
		}
	}
	return op
}

func fmtType(v any) string {
	s, _ := v.(string)
	return s
}

func docOp(d apiDoc, method, specP string) map[string]any {
	params := pathParams(specP, func(_ int, name string) (string, string) {
		if strings.EqualFold(name, "id") || strings.HasSuffix(name, "Id") {
			return name, "integer"
		}
		if name == "path" {
			return "Path on the provider", "string"
		}
		return name, "string"
	})
	qs := keysOf(d.Query)
	sort.Strings(qs)
	for _, k := range qs {
		params = append(params, map[string]any{"name": k, "in": "query", "description": d.Query[k].(map[string]any)["description"], "schema": map[string]any{"type": "string"}})
	}
	desc := d.Desc
	if d.Role != "" {
		desc = strings.TrimSpace(desc + " Needs the " + string(d.Role) + " role.")
	}
	op := map[string]any{
		"operationId": opID(method, specP),
		"summary":     d.Summary,
		"tags":        []string{d.Tag},
		"security":    security(d.Auth),
		"responses":   responses(d.Produces),
	}
	if desc != "" {
		op["description"] = desc
	}
	if isWriteMethod(method) && strings.HasPrefix(specP, "/api/admin/") {
		params = append(params, safetyParams()...)
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if d.Body != nil {
		op["requestBody"] = jsonBody(d.Body, d.Required, d.Form)
	}
	return op
}

// safetyParams are what every admin write takes: a dry run and an
// Idempotency-Key (dryrun.go, admin_idem.go).
func safetyParams() []map[string]any {
	return []map[string]any{
		{"name": "dry_run", "in": "query", "description": "1 says what the call would do and changes nothing", "schema": map[string]any{"type": "string", "enum": []string{"1"}}},
		{"name": "Idempotency-Key", "in": "header", "description": "A retry with the same key within 24 hours gets the first answer back instead of running again", "schema": map[string]any{"type": "string", "maxLength": adminIdemMaxKey}},
	}
}

func opID(method, p string) string {
	r := strings.NewReplacer("/", "_", "{", "", "}", "", ".", "_", "-", "_")
	return strings.ToLower(method) + strings.TrimRight(r.Replace(p), "_")
}

// routeOps returns the operations of the live router keyed "METHOD path"
// (OpenAPI paths), and the routes that have none.
func (s *Server) routeOps() (map[string]map[string]any, []string) {
	tools := ctlRouteTools()
	ops := map[string]map[string]any{}
	missing := []string{}
	for _, rt := range s.walkRoutes() {
		sp := specPath(rt.Path)
		if rt.All {
			found := false
			for k, d := range apiDocs {
				m, p, _ := strings.Cut(k, " ")
				if p == sp {
					ops[m+" "+p] = docOp(d, m, p)
					found = true
				}
			}
			if _, excluded := openAPIExcluded["* "+rt.Path]; !found && !excluded {
				missing = append(missing, "* "+rt.Path)
			}
			continue
		}
		key := rt.Method + " " + sp
		if t := tools[normRoute(rt.Method, rt.Path)]; t != nil && strings.HasPrefix(rt.Path, "/api/admin/") {
			ops[key] = toolOp(t, rt.Method, sp)
			continue
		}
		if d, ok := apiDocs[key]; ok {
			ops[key] = docOp(d, rt.Method, sp)
			continue
		}
		if _, ok := openAPIExcluded[key]; !ok {
			missing = append(missing, key)
		}
	}
	return ops, missing
}

// openAPISpec builds the OpenAPI 3.1 document.
func (s *Server) openAPISpec(base string) map[string]any {
	ops, _ := s.routeOps()
	paths := map[string]any{}
	for k, op := range ops {
		m, p, _ := strings.Cut(k, " ")
		item, _ := paths[p].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[p] = item
		}
		item[strings.ToLower(m)] = op
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Acnologia",
			"version": s.Cfg.Version,
			"description": "A self-hosted API key server. The /api/admin routes run the dashboard: each one names the control MCP tool (x-mcp-tool) that calls it at /mcp/control, " +
				"and the role it needs (x-role). Apps call providers through /proxy/{service} and the consumer API under /api/v1 with an app token, and never see a key. " +
				"Errors are {error: {code, message}}, with hint or fix on some.",
		},
		"servers": []map[string]any{{"url": base}},
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"adminToken":     map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "acnadm_...", "description": "Admin API token: the dashboard's Settings, acn login, or ACNOLOGIA_BOOTSTRAP_TOKEN on a fresh server"},
				"session":        map[string]any{"type": "apiKey", "in": "cookie", "name": sessionCookie, "description": "Dashboard session; mutating calls also need a JSON content type or X-Requested-With"},
				"appToken":       map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "acn_...", "description": "App token"},
				"appTokenHeader": map[string]any{"type": "apiKey", "in": "header", "name": "X-Acnologia-Token", "description": "App token in a header"},
				"oauth": map[string]any{"type": "oauth2", "description": "OAuth 2.1 with PKCE for /mcp", "flows": map[string]any{"authorizationCode": map[string]any{
					"authorizationUrl": base + "/oauth/authorize", "tokenUrl": base + "/oauth/token", "scopes": map[string]any{oauthScope: "Act as an app"}}}},
			},
			"schemas": map[string]any{
				"Error": obj(map[string]any{"error": obj(map[string]any{
					"code":    typed("string", "Machine readable code, such as not_found or invalid"),
					"message": typed("string", "What went wrong"),
					"hint":    typed("string", "What to do about it, when known"),
					"fix":     anyOf("A concrete fix, when known"),
				}, "code", "message")}, "error"),
			},
		},
	}
}

// openAPIJSON is GET /api/openapi.json.
func (s *Server) openAPIJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, 200, s.openAPISpec(s.baseURL(r)))
}
