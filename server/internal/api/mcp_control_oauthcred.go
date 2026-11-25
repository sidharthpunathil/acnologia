package api

// Control MCP tools for OAuth credentials (oauth_connect.go): set a
// service's OAuth client, get a sign in link for a person to open, follow
// it, and reconnect an account whose grant is gone. An agent cannot click
// through a consent screen, so oauth_connect_url returns the link and a
// connect id to poll with oauth_connection_status.

import (
	"net/http"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func ctlOAuthCredTools() []ctlTool {
	m, a := store.RoleMember, store.RoleAdmin
	fieldsProp := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
		"description": "Extra values the provider sends with the token, by field key (oauth_settings lists them), such as {\"developer_token\": \"...\"} for Google Ads"}
	return []ctlTool{
		{Name: "oauth_settings", Title: "OAuth settings", Group: "keys", Kind: ctlRead, Role: m, Method: http.MethodGet, Path: "/api/admin/services/{service}/oauth",
			Desc:  "For a service that signs in with OAuth: whether its OAuth client is set, the redirect URI to register with the provider, the scopes, the extra fields, and the connected accounts with their state (never a token).",
			Props: map[string]any{"service": svcIn}, Required: []string{"service"}},
		{Name: "set_oauth_client", Title: "Set the OAuth client", Group: "keys", Kind: ctlWrite, Role: a, Method: http.MethodPut, Path: "/api/admin/services/{service}/oauth",
			Desc: "Set the client id and secret of the OAuth app registered with the provider for this service, and optionally the scopes to ask for and the tenant. Register the redirect URI oauth_settings shows first.",
			Props: map[string]any{
				"service":       svcIn,
				"client_id":     sProp("OAuth client id"),
				"client_secret": sProp("OAuth client secret; leave out to keep the stored one"),
				"scopes":        aProp(map[string]any{"type": "string"}, "Scopes to ask for; leave out to keep them, [] for the provider's defaults"),
				"tenant":        sProp("Only for providers whose OAuth URLs take a tenant (oauth_settings shows takes_tenant), such as Microsoft Graph: common, organizations, consumers, a tenant id or a domain. \"\" for the provider's default; leave out to keep it"),
			},
			Required: []string{"service", "client_id"}, Body: []string{"client_id", "client_secret", "scopes", "tenant"}},
		{Name: "oauth_connect_url", Title: "OAuth connect link", Group: "keys", Kind: ctlWrite, Role: a, Method: http.MethodPost, Path: "/api/admin/services/{service}/oauth/connect",
			Desc:  "Start connecting an account to an OAuth service. Returns a sign in url for a person to open in a browser (you cannot approve it yourself) and a connect_id; poll oauth_connection_status until status is done or failed. The link works once, for 15 minutes. The account becomes a key in the service's pool.",
			Props: map[string]any{"service": svcIn, "fields": fieldsProp}, Required: []string{"service"}, Body: []string{"fields"}},
		{Name: "oauth_connection_status", Title: "OAuth connect status", Group: "keys", Kind: ctlRead, Role: a, Method: http.MethodGet, Path: "/api/admin/oauth/connects/{connect_id}",
			Desc:  "How an OAuth sign in went: pending (not opened yet), exchanging, done (with key_id and account), failed (with error) or expired.",
			Props: map[string]any{"connect_id": sProp("connect_id from oauth_connect_url or oauth_reconnect")}, Required: []string{"connect_id"}},
		{Name: "list_app_oauth_connections", Title: "MCP sign ins for an app", Group: "apps", Kind: ctlRead, Role: m, Method: http.MethodGet, Path: "/api/oauth/apps/{app_id}/connections",
			Desc:  "MCP clients signed in to /mcp through OAuth as this app: client, read only or not, who approved it, created and last used. Never a token.",
			Props: map[string]any{"app_id": iProp("App id")}, Required: []string{"app_id"}},
		{Name: "revoke_app_oauth_connection", Title: "Revoke an app's MCP sign in", Group: "apps", Kind: ctlDestructive, Role: a, Method: http.MethodDelete, Path: "/api/oauth/apps/{app_id}/connections/{grant_id}",
			Desc:  "End one OAuth sign in to /mcp: its access and refresh tokens stop working at once. The client has to sign in again.",
			Props: map[string]any{"app_id": iProp("App id"), "grant_id": sProp("Connection id from list_app_oauth_connections")}, Required: []string{"app_id", "grant_id"},
			Describe: func(_ *ctlCtx, in map[string]any) (string, error) {
				return "end the MCP sign in " + argStr(in, "grant_id") + " of app " + argStr(in, "app_id") + "; its client has to sign in again", nil
			}},
		{Name: "list_control_connections", Title: "Control MCP sign ins", Group: "settings", Kind: ctlRead, Role: m, Method: http.MethodGet, Path: "/api/oauth/control/connections",
			Desc: "MCP clients signed in to /mcp/control through OAuth: your own, or everyone's for a superadmin, with their scope (read, write or admin). Never a token."},
		{Name: "revoke_control_connection", Title: "Revoke a control MCP sign in", Group: "settings", Kind: ctlDestructive, Role: m, Method: http.MethodDelete, Path: "/api/oauth/control/connections/{grant_id}",
			Desc:  "End one OAuth sign in to /mcp/control (your own, or anyone's for a superadmin). Its tokens stop working at once.",
			Props: map[string]any{"grant_id": sProp("Connection id from list_control_connections")}, Required: []string{"grant_id"},
			Describe: func(_ *ctlCtx, in map[string]any) (string, error) {
				return "end the control MCP sign in " + argStr(in, "grant_id") + "; its client has to sign in again", nil
			}},
		{Name: "oauth_reconnect", Title: "Reconnect an OAuth account", Group: "keys", Kind: ctlWrite, Role: a, Method: http.MethodPost, Path: "/api/admin/keys/{key_id}/oauth/reconnect",
			Desc:  "Get a sign in link that reconnects an OAuth key whose grant was revoked or expired (oauth_settings shows needs_reconnect). The key keeps its id and settings. Poll oauth_connection_status as for oauth_connect_url.",
			Props: map[string]any{"key_id": iProp("Key id"), "fields": fieldsProp}, Required: []string{"key_id"}, Body: []string{"fields"}},
	}
}
