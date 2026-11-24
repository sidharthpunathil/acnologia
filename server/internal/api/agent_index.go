package api

import (
	"net/http"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
)

// The agent index: where an agent that knows only this server's URL finds
// everything else. GET /api/v1 answers with it, and /.well-known/acnologia
// carries it as its agent block. Both are open in the access gate and hold
// nothing secret: the version (which /healthz shows too), whether the gate
// is on, and fixed paths.

// agentIndex describes this server to an agent.
type agentIndex struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	BaseURL string `json:"base_url"`
	// SetupRequired is null for a caller the access gate keeps out: whether
	// a server is still unclaimed is not for strangers.
	SetupRequired *bool          `json:"setup_required"`
	Gated         bool           `json:"gated"`
	Auth          agentAuth      `json:"auth"`
	Endpoints     agentEndpoints `json:"endpoints"`
	StartHere     []string       `json:"start_here"`
}

type agentAuth struct {
	AppToken   string `json:"app_token"`
	AdminToken string `json:"admin_token"`
	OAuth      string `json:"oauth"`
	// AccessHeader is the header that carries the access password, when
	// the gate is on.
	AccessHeader string `json:"access_header,omitempty"`
	// DocsNeedToken says the docs answer only with a token or the access
	// password, as they do behind the gate.
	DocsNeedToken bool `json:"docs_need_token"`
}

type agentEndpoints struct {
	MCP           string `json:"mcp"`
	MCPControl    string `json:"mcp_control"`
	OAuthMetadata string `json:"oauth_metadata"`
	LLMs          string `json:"llms"`
	LLMsFull      string `json:"llms_full"`
	Docs          string `json:"docs"`
	ErrorsDoc     string `json:"errors_doc"`
	OpenAPI       string `json:"openapi"`
	DeviceLogin   string `json:"device_login"`
	Me            string `json:"me"`
	Services      string `json:"services"`
	Index         string `json:"index"`
	WellKnown     string `json:"well_known"`
	Health        string `json:"health"`
}

func (s *Server) agentIndex(r *http.Request) agentIndex {
	base := s.publicURL(r)
	gated := s.gate.on()
	idx := agentIndex{
		Name:    "acnologia",
		Version: s.Cfg.Version,
		BaseURL: base,
		Gated:   gated,
		Auth: agentAuth{
			AppToken:      "Authorization: Bearer acn_... (or X-Acnologia-Token) on /api/v1, /proxy and /mcp",
			AdminToken:    "Authorization: Bearer acnadm_... on /api/admin and /mcp/control",
			OAuth:         "MCP clients can sign in to /mcp with OAuth 2.1; discovery at " + base + "/.well-known/oauth-protected-resource/mcp",
			DocsNeedToken: gated,
		},
		Endpoints: agentEndpoints{
			MCP:           base + "/mcp",
			MCPControl:    base + "/mcp/control",
			OAuthMetadata: base + "/.well-known/oauth-authorization-server",
			LLMs:          base + "/llms.txt",
			LLMsFull:      base + "/llms-full.txt",
			Docs:          base + "/api/docs",
			ErrorsDoc:     base + errorsDoc,
			OpenAPI:       base + "/api/openapi.json",
			DeviceLogin:   base + "/api/cli/device",
			Me:            base + "/api/v1/me",
			Services:      base + "/api/v1/services",
			Index:         base + "/api/v1",
			WellKnown:     base + "/.well-known/acnologia",
			Health:        base + "/healthz",
		},
	}
	if gated {
		idx.Auth.AccessHeader = accessHeader
	}
	if !gated || s.gatePasses(r) {
		if n, err := s.Store.CountUsers(r.Context()); err == nil {
			need := n == 0
			idx.SetupRequired = &need
		}
	}
	docs := "Read " + idx.Endpoints.LLMs + " for the overview and " + idx.Endpoints.LLMsFull + " for every endpoint."
	if gated {
		docs = "Read " + idx.Endpoints.LLMs + " and " + idx.Endpoints.LLMsFull + ", sending your token: this server is behind an access password, and the docs open to any valid app, MCP or admin token."
	}
	idx.StartHere = []string{
		docs,
		"With an app token (acn_...), GET " + idx.Endpoints.Me + " (or call the acnologia_whoami MCP tool) to learn the token's mode, limits and services.",
		"Connect an MCP client to " + idx.Endpoints.MCP + " with the app token, or let it sign in with OAuth.",
		"No token yet: a person creates an app in the dashboard and gives you its token. The acn CLI signs in through the browser with acn login, which uses " + idx.Endpoints.DeviceLogin + ".",
		"On an error, read error.hint and error.fix; error.doc_url explains the code, and " + idx.Endpoints.ErrorsDoc + " lists them all.",
	}
	return idx
}

// v1Index answers GET /api/v1: the agent index.
func (s *Server) v1Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.agentIndex(r))
}

// wellKnownDoc is /.well-known/acnologia: the peer identity other servers
// pair with, plus the agent index.
type wellKnownDoc struct {
	peer.Identity
	Agent agentIndex `json:"agent"`
}
