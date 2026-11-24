package api

// Request attribution: which token, which way in (dashboard, API, CLI,
// control MCP, app MCP, Assistant, OAuth) and which client program made a
// request. The auth middleware puts it on the context (store.Attribution)
// and AddAudit reads it, so the audit log tells an agent's change from a
// person's without each call site passing anything.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// userAgent is the request's User-Agent, trimmed for the audit log.
func userAgent(r *http.Request) string { return store.ClientName(r.Header.Get("User-Agent")) }

// fromCLI reports whether the acn command line made the request: it names
// itself in X-Requested-With, and in its User-Agent.
func fromCLI(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "acnologia-ctl" || strings.HasPrefix(r.Header.Get("User-Agent"), "acn/")
}

// sessionAttribution is a signed in session's: the dashboard, or the
// Assistant when it makes the call in process.
func sessionAttribution(r *http.Request) store.Attribution {
	if fromAssistant(r) {
		client := store.AttributionFrom(r.Context()).Client
		if client == "" {
			client = "assistant"
		}
		return store.Attribution{Via: store.ViaAssistant, Client: client}
	}
	// A chat turn runs the Assistant's tools on this request's context, so
	// what they do outside an in-process route call (app tools, service
	// tools) is the Assistant's too.
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/admin/chat/") {
		return store.Attribution{Via: store.ViaAssistant, Client: userAgent(r)}
	}
	return store.Attribution{Via: store.ViaDashboard, Client: userAgent(r)}
}

// attributeAdminToken runs after tokenUser: it notes the admin API token
// and whether the call came from the CLI, the control MCP or anything else.
func attributeAdminToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := store.Attribution{Via: store.ViaAPI, Client: userAgent(r)}
		if fromCLI(r) {
			at.Via = store.ViaCLI
		}
		// The control MCP calls admin routes in process; it set how the
		// call came in and the client on the outer request.
		if outer := store.AttributionFrom(r.Context()); outer.Via == store.ViaControlMCP {
			at.Via, at.Client = outer.Via, outer.Client
		}
		if t, ok := r.Context().Value(tokenKey).(*store.AdminToken); ok {
			at.TokenID = t.ID
		}
		next.ServeHTTP(w, r.WithContext(store.WithAttribution(r.Context(), at)))
	})
}

// appAttribution is an app request's outside MCP.
func appAttribution(r *http.Request, a *store.App) store.Attribution {
	via := store.ViaAPI
	if _, ok := oauthBearer(r); ok {
		via = store.ViaOAuth
	}
	return store.Attribution{Via: via, TokenID: a.TokenID, AppID: a.ID, Client: userAgent(r)}
}

// mcpAttribution is an app MCP request's, with the client from initialize.
func (s *Server) mcpAttribution(ctx context.Context, r *http.Request, req rpcReq, a *store.App) context.Context {
	via := store.ViaAppMCP
	if _, ok := oauthBearer(r); ok {
		via = store.ViaOAuth
	}
	return store.WithAttribution(ctx, store.Attribution{Via: via, TokenID: a.TokenID, AppID: a.ID, Client: mcpClientName(r, req)})
}

// controlAttribution marks a control MCP request, so the admin routes it
// calls in process record control_mcp and the client from initialize.
func (s *Server) controlAttribution(r *http.Request, req rpcReq) *http.Request {
	at := store.Attribution{Via: store.ViaControlMCP, Client: mcpClientName(r, req)}
	return r.WithContext(store.WithAttribution(r.Context(), at))
}

// mcpClients remembers the clientInfo each MCP credential sent with
// initialize: tool calls after it do not repeat it. Keyed by a hash of the
// credential (and the MCP session, when the client sends one).
var mcpClients = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// mcpClientCap bounds the memory; past it the map starts over.
const mcpClientCap = 4096

func mcpClientKey(r *http.Request) string {
	return crypto.SHA256(r.Header.Get("Authorization") + "\n" + appToken(r) + "\n" + r.Header.Get("Mcp-Session-Id"))
}

// mcpClientName is the MCP client's "name/version": from initialize's
// clientInfo, or remembered from the credential's last initialize, or the
// User-Agent.
func mcpClientName(r *http.Request, req rpcReq) string {
	key := mcpClientKey(r)
	if req.Method == "initialize" {
		var p struct {
			ClientInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if name := store.ClientName(p.ClientInfo.Name); name != "" {
			if v := store.ClientName(p.ClientInfo.Version); v != "" {
				name += "/" + v
			}
			name = store.ClientName(name)
			mcpClients.Lock()
			if len(mcpClients.m) >= mcpClientCap {
				mcpClients.m = map[string]string{}
			}
			mcpClients.m[key] = name
			mcpClients.Unlock()
			return name
		}
	}
	mcpClients.Lock()
	name, ok := mcpClients.m[key]
	mcpClients.Unlock()
	if ok {
		return name
	}
	return userAgent(r)
}
