package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// OAuth 2.1 for /mcp, so hosted MCP clients (claude.ai and ChatGPT
// connectors, Claude Code, Cursor) connect by signing in instead of pasting
// an app token. It follows the MCP authorization spec:
//
//   - /.well-known/oauth-protected-resource[/mcp] (RFC 9728) names this
//     server as the authorization server for <base>/mcp, and a 401 from /mcp
//     points at it in WWW-Authenticate.
//   - /.well-known/oauth-authorization-server (RFC 8414) lists the endpoints.
//   - POST /oauth/register (RFC 7591) registers a public client. A client_id
//     that is an https URL is a client ID metadata document instead, fetched
//     with the SSRF safe client.
//   - GET /oauth/authorize checks the request (PKCE S256 required, the
//     resource must be <base>/mcp) and sends the browser to the dashboard's
//     consent page, /oauth/consent. A signed in admin picks the app the
//     connection acts as and whether it is read only.
//   - POST /oauth/token swaps the code (single use, 60 seconds) for an access
//     token (1 hour) and a refresh token (30 days, rotated on every use; a
//     spent one coming back revokes the whole grant). POST /oauth/revoke
//     (RFC 7009) revokes a grant.
//
// An access token authenticates on /mcp exactly as one of the app's tokens
// would, read only included, so every app rule applies unchanged. It works
// only on the resource it was issued for, and stops when the grant is
// revoked, the app is disabled or deleted, or the person who approved it is
// disabled, deleted or no longer an admin. /mcp/control keeps its admin API
// tokens.

const (
	oauthAccessPrefix  = "acno_"
	oauthRefreshPrefix = "acnr_"
	oauthCodePrefix    = "acnc_"
	oauthClientPrefix  = "acnoc_"

	oauthCodeTTL    = 60 * time.Second
	oauthAccessTTL  = time.Hour
	oauthRefreshTTL = 30 * 24 * time.Hour
	// oauthRequestTTL is how long a consent page stays good.
	oauthRequestTTL = 10 * time.Minute
	// oauthPendingCap bounds memory: at most this many consent pages open,
	// and oauthPendingPerClient from one address.
	oauthPendingCap       = 500
	oauthPendingPerClient = 20
	// oauthScope is the one scope: the app's MCP surface.
	oauthScope = "mcp"
	// A client ID metadata document is fetched again after this long.
	oauthCIMDFresh = 5 * time.Minute
	oauthCIMDMax   = 64 << 10
	oauthBodyMax   = 64 << 10
)

// oauthState is the authorization server's memory: consent requests in
// flight and the limits on the open endpoints.
type oauthState struct {
	once sync.Once
	mu   sync.Mutex
	// pending holds authorization requests waiting on the consent page, by
	// request id.
	pending map[string]*oauthPending
	// register limits client registrations per address.
	register *clientLimit
	// misses limits failed token and revoke requests per address.
	misses *clientLimit
	// fetch loads client ID metadata documents (public addresses only).
	fetch *http.Client
}

type oauthPending struct {
	client      *store.OAuthClient
	redirectURI string
	state       string
	challenge   string
	resource    string
	scope       string
	source      string
	created     time.Time
}

func (s *Server) oauthSt() *oauthState {
	o := &s.oauth
	o.once.Do(func() {
		o.pending = map[string]*oauthPending{}
		// 10 registrations a minute and 100 a day from one address, 60 a
		// minute and 2000 a day from all.
		o.register = newClientLimit(10, 100, 60, 2000)
		o.misses = newClientLimit(30, 500, 300, 20000)
		c := catalog.SafeClient(8 * time.Second)
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirects are not followed") }
		o.fetch = c
	})
	return o
}

// mountOAuth adds the authorization server's routes, the consent page's
// API and the connections list. The open routes are listed in gate.go.
func (s *Server) mountOAuth(r chi.Router) {
	r.HandleFunc("/.well-known/oauth-protected-resource", oauthOpen(http.MethodGet, s.oauthResourceMeta))
	r.HandleFunc("/.well-known/oauth-protected-resource/mcp", oauthOpen(http.MethodGet, s.oauthResourceMeta))
	r.HandleFunc("/.well-known/oauth-authorization-server", oauthOpen(http.MethodGet, s.oauthServerMeta))
	r.Get("/oauth/authorize", s.oauthAuthorize)
	r.HandleFunc("/oauth/register", oauthOpen(http.MethodPost, s.oauthRegister))
	r.HandleFunc("/oauth/token", oauthOpen(http.MethodPost, s.oauthToken))
	r.HandleFunc("/oauth/revoke", oauthOpen(http.MethodPost, s.oauthRevoke))
	r.Route("/api/oauth", func(r chi.Router) {
		r.Use(jsonOnly, s.requireUser)
		// The consent page: a dashboard session, never an API token.
		r.With(s.sessionOnly).Get("/requests/{rid}", s.oauthRequestInfo)
		r.With(s.sessionOnly).Post("/requests/{rid}/approve", s.oauthApprove)
		r.With(s.sessionOnly).Post("/requests/{rid}/deny", s.oauthDeny)
		r.Get("/apps/{id}/connections", s.listOAuthConnections)
		r.With(s.requireRole(store.RoleAdmin), s.notShareApp).Delete("/apps/{id}/connections/{grantId}", s.revokeOAuthConnection)
	})
}

// oauthOpen answers CORS for the endpoints browser based MCP clients call
// across origins, and allows one method. Nothing here reads a cookie.
func oauthOpen(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Access-Control-Allow-Methods", method+", OPTIONS")
		hd.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
		hd.Set("Access-Control-Max-Age", "600")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		case method, methodHeadFor(method):
			h(w, r)
		default:
			hd.Set("Allow", method+", OPTIONS")
			oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "use "+method)
		}
	}
}

func methodHeadFor(m string) string {
	if m == http.MethodGet {
		return http.MethodHead
	}
	return m
}

// oauthErr writes an OAuth error body (RFC 6749 section 5.2).
func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (s *Server) mcpResourceURL(r *http.Request) string { return s.publicURL(r) + "/mcp" }

func (s *Server) oauthResourceMeta(w http.ResponseWriter, r *http.Request) {
	base := s.publicURL(r)
	writeJSON(w, 200, map[string]any{
		"resource":                 base + "/mcp",
		"resource_name":            "Acnologia MCP",
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{oauthScope},
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   base + "/docs/mcp-oauth",
	})
}

func (s *Server) oauthServerMeta(w http.ResponseWriter, r *http.Request) {
	base := s.publicURL(r)
	writeJSON(w, 200, map[string]any{
		"issuer":                                         base,
		"authorization_endpoint":                         base + "/oauth/authorize",
		"token_endpoint":                                 base + "/oauth/token",
		"registration_endpoint":                          base + "/oauth/register",
		"revocation_endpoint":                            base + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"scopes_supported":                               []string{oauthScope},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          base + "/docs/mcp-oauth",
	})
}

// mcpChallenge sets WWW-Authenticate on a refused /mcp request: where the
// resource metadata is, and invalid_token when a token was sent, so a client
// holding an expired access token refreshes it without asking anyone.
func (s *Server) mcpChallenge(w http.ResponseWriter, r *http.Request, status int) {
	v := `Bearer realm="acnologia", resource_metadata="` + s.publicURL(r) + `/.well-known/oauth-protected-resource/mcp"`
	switch {
	case status != http.StatusUnauthorized:
	case r.Header.Get("Authorization") != "" || appToken(r) != "":
		v += `, error="invalid_token"`
	default:
		v += `, scope="` + oauthScope + `"`
	}
	w.Header().Set("WWW-Authenticate", v)
}

// oauthBearer returns the OAuth access token in Authorization: Bearer, if
// that is what the header holds.
func oauthBearer(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok, ok && strings.HasPrefix(tok, oauthAccessPrefix)
}

// authOAuth authenticates an OAuth access token as the app its grant acts
// as, read only when the grant is.
func (s *Server) authOAuth(r *http.Request, tok string) (*store.App, *authFail) {
	unauth := func(msg string) *authFail { return &authFail{Status: 401, Code: "unauthenticated", Message: msg} }
	a, ti, u, err := s.Store.AppByOAuthToken(r.Context(), crypto.SHA256(tok))
	if err != nil {
		return nil, unauth("invalid access token")
	}
	g := ti.Grant
	if g.RevokedAt != nil {
		return nil, unauth("access token revoked")
	}
	if !ti.ExpiresAt.After(time.Now()) {
		return nil, unauth("access token expired")
	}
	if p, err := url.Parse(g.Resource); err != nil || strings.TrimRight(p.Path, "/") != strings.TrimRight(r.URL.Path, "/") {
		return nil, unauth("this access token is for " + g.Resource + " only")
	}
	if u.Disabled || !u.Role.AtLeast(store.RoleAdmin) {
		return nil, unauth("the person who approved this connection can no longer approve it")
	}
	if a.ShareID != nil {
		return nil, unauth("invalid access token")
	}
	if !a.Active {
		return nil, &authFail{Status: 403, Code: "unauthenticated", Message: "app is disabled"}
	}
	s.Store.TouchOAuthGrant(a.ID, g.ID)
	return a, nil
}

// Redirect URIs and client ids.

var schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

// badSchemes can never receive a code: they run script, read files or
// are plain network schemes that are not https.
var badSchemes = map[string]bool{"javascript": true, "data": true, "file": true, "vbscript": true, "blob": true, "about": true,
	"ftp": true, "ws": true, "wss": true, "http": true, "https": true}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// validRedirectURI accepts https, http on a loopback address (any port),
// and a private use scheme of a native app (cursor://, vscode://). Never a
// fragment.
func validRedirectURI(raw string) error {
	if raw == "" || len(raw) > 2000 {
		return errors.New("a redirect URI must be 1 to 2000 characters")
	}
	if strings.Contains(raw, "#") {
		return errors.New("a redirect URI must not have a fragment")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return errors.New("a redirect URI must be an absolute URL")
	}
	if u.User != nil {
		return errors.New("a redirect URI must not carry a user name or password")
	}
	switch scheme := u.Scheme; {
	case scheme == "https":
		if u.Host == "" {
			return errors.New("an https redirect URI needs a host")
		}
	case scheme == "http":
		if !isLoopbackHost(u.Hostname()) {
			return errors.New("an http redirect URI must be on localhost, 127.0.0.1 or [::1]; use https otherwise")
		}
	case !schemeRe.MatchString(scheme) || badSchemes[scheme]:
		return errors.New("redirect URI scheme " + scheme + " is not allowed")
	case u.Opaque == "" && u.Host == "" && u.Path == "":
		return errors.New("a redirect URI needs more than a scheme")
	}
	return nil
}

// redirectMatches compares a request's redirect_uri with the registered
// ones: exactly, except that a loopback http URI may use any port (RFC 8252
// section 7.3), since a native client picks a free port each time.
func redirectMatches(registered []string, got string) bool {
	for _, r := range registered {
		if r == got {
			return true
		}
	}
	g, err := url.Parse(got)
	if err != nil || g.Scheme != "http" || !isLoopbackHost(g.Hostname()) || strings.Contains(got, "#") {
		return false
	}
	for _, r := range registered {
		u, err := url.Parse(r)
		if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) {
			continue
		}
		if u.Hostname() == g.Hostname() && u.Path == g.Path && u.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

// isMetadataClientID reports whether a client_id is a client ID metadata
// document URL: https, a host, a path, no fragment, query or user info.
func isMetadataClientID(id string) bool {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(id, "#") {
		return false
	}
	if u.Path == "" || u.Path == "/" {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// sameResource compares two resource URLs: scheme and host without case,
// the path without a trailing slash, and no query or fragment.
func sameResource(got, want string) bool {
	g, err1 := url.Parse(got)
	w, err2 := url.Parse(want)
	if err1 != nil || err2 != nil || g.RawQuery != "" || g.Fragment != "" {
		return false
	}
	return strings.EqualFold(g.Scheme, w.Scheme) && strings.EqualFold(g.Host, w.Host) &&
		strings.TrimRight(g.Path, "/") == strings.TrimRight(w.Path, "/")
}

func withParams(base string, kv ...string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func clipRunes(s string, n int) string {
	s = clip(s, 4*n)
	for utf8.RuneCountInString(s) > n {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return strings.ToValidUTF8(s, "")
}

// Registration (RFC 7591).

type clientMetadata struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

func (m *clientMetadata) check() error {
	if len(m.RedirectURIs) == 0 || len(m.RedirectURIs) > 10 {
		return errors.New("redirect_uris must list 1 to 10 URIs")
	}
	for _, u := range m.RedirectURIs {
		if err := validRedirectURI(u); err != nil {
			return err
		}
	}
	for _, g := range m.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return errors.New("grant_types may hold authorization_code and refresh_token only")
		}
	}
	for _, t := range m.ResponseTypes {
		if t != "code" {
			return errors.New("response_types may hold code only")
		}
	}
	return nil
}

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	o := s.oauthSt()
	if !o.register.take(clientKey(r), time.Now()) {
		w.Header().Set("Retry-After", "60")
		oauthErr(w, http.StatusTooManyRequests, "slow_down", "too many registrations from this address; try again later")
		return
	}
	var m clientMetadata
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, oauthBodyMax)).Decode(&m); err != nil {
		oauthErr(w, 400, "invalid_client_metadata", "send the client metadata as a JSON object")
		return
	}
	if err := m.check(); err != nil {
		code := "invalid_client_metadata"
		if strings.Contains(err.Error(), "redirect") {
			code = "invalid_redirect_uri"
		}
		oauthErr(w, 400, code, err.Error())
		return
	}
	c := &store.OAuthClient{
		ID:           crypto.RandomToken(oauthClientPrefix, 18),
		Name:         clipRunes(m.ClientName, 100),
		RedirectURIs: m.RedirectURIs,
		Kind:         store.OAuthClientDCR,
		CreatedAt:    time.Now().UTC(),
	}
	if c.Name == "" {
		c.Name = "Unnamed MCP client"
	}
	if err := s.Store.SaveOAuthClient(r.Context(), c); err != nil {
		oauthErr(w, 500, "server_error", "could not save the client")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      oauthScope,
	})
}

// oauthClient finds a client: a registered one, or a client ID metadata
// document, fetched again when the copy is older than oauthCIMDFresh.
func (s *Server) oauthClient(r *http.Request, id string) (*store.OAuthClient, error) {
	ctx := r.Context()
	if !isMetadataClientID(id) {
		c, err := s.Store.GetOAuthClient(ctx, id)
		if err != nil || c.Kind != store.OAuthClientDCR {
			return nil, errors.New("this app is not registered with this server: it should register again")
		}
		return c, nil
	}
	cached, _ := s.Store.GetOAuthClient(ctx, id)
	if cached != nil && cached.FetchedAt != nil && time.Since(*cached.FetchedAt) < oauthCIMDFresh {
		return cached, nil
	}
	m, err := s.fetchClientMetadata(r, id)
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC()
	c := &store.OAuthClient{ID: id, Name: clipRunes(m.ClientName, 100), RedirectURIs: m.RedirectURIs, Kind: store.OAuthClientCIMD, CreatedAt: at, FetchedAt: &at}
	if c.Name == "" {
		c.Name = oauthHost(id)
	}
	if cached != nil {
		c.CreatedAt = cached.CreatedAt
	}
	if err := s.Store.SaveOAuthClient(ctx, c); err != nil {
		return nil, errors.New("could not save the client")
	}
	return c, nil
}

func (s *Server) fetchClientMetadata(r *http.Request, id string) (*clientMetadata, error) {
	fail := func(why string) (*clientMetadata, error) {
		return nil, errors.New("could not use the client metadata document at " + id + ": " + why)
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, id, nil)
	if err != nil {
		return fail("bad URL")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.oauthSt().fetch.Do(req)
	if err != nil {
		return fail("it could not be fetched")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("it answered " + strconv.Itoa(resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, oauthCIMDMax+1))
	if err != nil || len(b) > oauthCIMDMax {
		return fail("it is too large or could not be read")
	}
	var m clientMetadata
	if err := json.Unmarshal(b, &m); err != nil {
		return fail("it is not a JSON object")
	}
	if m.ClientID != id {
		return fail("its client_id does not match its URL")
	}
	if m.TokenEndpointAuthMethod != "" && m.TokenEndpointAuthMethod != "none" {
		return fail("only public clients (token_endpoint_auth_method none) are supported")
	}
	if err := m.check(); err != nil {
		return fail(err.Error())
	}
	return &m, nil
}

// clientLabel names a client in the audit log: its name, and its URL when
// the client_id is a metadata document. A registered client's id is an
// opaque string that says nothing more.
func clientLabel(name, id string) string {
	if isMetadataClientID(id) {
		return name + " (" + id + ")"
	}
	return name
}

func oauthHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Authorization.

// oauthPage answers an authorization request that cannot go back to the
// client, because the client or its redirect URI is unknown.
func oauthPage(w http.ResponseWriter, status int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cannot connect</title><style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 16px;color:#1c1c1c;background:#fff}
@media (prefers-color-scheme:dark){body{color:#eee;background:#141414}}</style></head>
<body><h1 style="font-size:1.25rem">Cannot connect this app</h1><p>%s</p><p>Go back to the app and start the connection again.</p></body></html>`,
		html.EscapeString(msg))
}

var challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	c, err := s.oauthClient(r, q.Get("client_id"))
	if err != nil {
		oauthPage(w, 400, err.Error())
		return
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if !redirectMatches(c.RedirectURIs, redirect) {
		oauthPage(w, 400, "The redirect URI does not match any this app registered.")
		return
	}
	state := q.Get("state")
	iss := s.publicURL(r)
	fail := func(code, desc string) {
		http.Redirect(w, r, withParams(redirect, "error", code, "error_description", desc, "state", state, "iss", iss), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "response_type must be code")
		return
	}
	if q.Get("code_challenge_method") != "S256" || !challengeRe.MatchString(q.Get("code_challenge")) {
		fail("invalid_request", "PKCE is required: send code_challenge with code_challenge_method S256")
		return
	}
	resource := s.mcpResourceURL(r)
	switch rs := q["resource"]; {
	case len(rs) > 1:
		fail("invalid_target", "send one resource")
		return
	case len(rs) == 1 && !sameResource(rs[0], resource):
		fail("invalid_target", "resource must be "+resource)
		return
	}
	o := s.oauthSt()
	src := clientKey(r)
	id := crypto.RandomToken("", 24)
	now := time.Now()
	o.mu.Lock()
	mine := 0
	for k, p := range o.pending {
		if now.Sub(p.created) > oauthRequestTTL {
			delete(o.pending, k)
		} else if p.source == src {
			mine++
		}
	}
	if len(o.pending) >= oauthPendingCap || mine >= oauthPendingPerClient {
		o.mu.Unlock()
		fail("temporarily_unavailable", "too many sign ins in progress; try again in a few minutes")
		return
	}
	o.pending[id] = &oauthPending{client: c, redirectURI: redirect, state: state, challenge: q.Get("code_challenge"),
		resource: resource, scope: oauthScope, source: src, created: now}
	o.mu.Unlock()
	http.Redirect(w, r, "/oauth/consent?request="+url.QueryEscape(id), http.StatusFound)
}

// Token endpoint.

type tokenPair struct {
	access, refresh string
	p               store.OAuthTokenPair
}

func newTokenPair() tokenPair {
	now := time.Now()
	a := crypto.RandomToken(oauthAccessPrefix, 32)
	rf := crypto.RandomToken(oauthRefreshPrefix, 32)
	return tokenPair{access: a, refresh: rf, p: store.OAuthTokenPair{
		AccessHash: crypto.SHA256(a), RefreshHash: crypto.SHA256(rf),
		AccessExpiry: now.Add(oauthAccessTTL), RefreshExpiry: now.Add(oauthRefreshTTL),
	}}
}

func writeTokens(w http.ResponseWriter, t tokenPair, scope string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	writeJSON(w, 200, map[string]any{
		"access_token":  t.access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL.Seconds()),
		"refresh_token": t.refresh,
		"scope":         scope,
	})
}

// tokenForm parses an x-www-form-urlencoded token or revoke request and
// returns the client_id, from the form or HTTP Basic (a public client has
// no secret; one sent anyway is ignored).
func tokenForm(w http.ResponseWriter, r *http.Request) (url.Values, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, oauthBodyMax)
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		oauthErr(w, 400, "invalid_request", "send application/x-www-form-urlencoded")
		return nil, "", false
	}
	if err := r.ParseForm(); err != nil {
		oauthErr(w, 400, "invalid_request", "could not read the form")
		return nil, "", false
	}
	f := r.PostForm
	id := f.Get("client_id")
	if u, _, ok := r.BasicAuth(); ok && id == "" {
		id, _ = url.QueryUnescape(u)
	}
	return f, id, true
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	o := s.oauthSt()
	key := clientKey(r)
	if !o.misses.allow(key, time.Now()) {
		w.Header().Set("Retry-After", "60")
		oauthErr(w, http.StatusTooManyRequests, "slow_down", "too many failed token requests from this address")
		return
	}
	f, clientID, ok := tokenForm(w, r)
	if !ok {
		return
	}
	bad := func(code, desc string) {
		o.misses.take(key, time.Now())
		oauthErr(w, 400, code, desc)
	}
	if clientID == "" {
		bad("invalid_client", "client_id is required")
		return
	}
	switch f.Get("grant_type") {
	case "authorization_code":
		s.oauthCodeGrant(w, r, f, clientID, bad)
	case "refresh_token":
		s.oauthRefreshGrant(w, r, f, clientID, bad)
	default:
		bad("unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, c := range verifier {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func (s *Server) oauthCodeGrant(w http.ResponseWriter, r *http.Request, f url.Values, clientID string, bad func(code, desc string)) {
	ctx := r.Context()
	hash := crypto.SHA256(f.Get("code"))
	c, used, err := s.Store.GetOAuthCode(ctx, hash)
	if err != nil || f.Get("code") == "" {
		bad("invalid_grant", "the code is unknown")
		return
	}
	if c.ClientID != clientID {
		bad("invalid_grant", "the code was issued to another client")
		return
	}
	if used {
		// A code used twice: whoever holds the tokens from the first use may
		// not be the client. Revoke them.
		if c.GrantID != "" {
			s.oauthRevokeGrant(r, c.GrantID, nil, "oauth.code_reuse", "authorization code used twice")
		}
		bad("invalid_grant", "the code was already used")
		return
	}
	if !c.ExpiresAt.After(time.Now()) {
		bad("invalid_grant", "the code expired")
		return
	}
	if f.Get("redirect_uri") != c.RedirectURI {
		bad("invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	if !pkceMatches(f.Get("code_verifier"), c.CodeChallenge) {
		bad("invalid_grant", "code_verifier does not match the code_challenge")
		return
	}
	if rs := f["resource"]; len(rs) > 1 || (len(rs) == 1 && !sameResource(rs[0], c.Resource)) {
		bad("invalid_target", "resource does not match the authorization request")
		return
	}
	name := clientID
	if cl, err := s.Store.GetOAuthClient(ctx, clientID); err == nil && cl.Name != "" {
		name = cl.Name
	}
	g := &store.OAuthGrant{ID: crypto.RandomToken("", 16), ClientID: clientID, ClientName: name, AppID: c.AppID, UserID: c.UserID,
		ReadOnly: c.ReadOnly, Scope: c.Scope, Resource: c.Resource}
	t := newTokenPair()
	switch err := s.Store.RedeemOAuthCode(ctx, hash, g, t.p); {
	case errors.Is(err, store.ErrOAuthUsed):
		s.Store.AddAudit(ctx, nil, "oauth.code_reuse", name, "authorization code used twice; its connection was revoked")
		bad("invalid_grant", "the code was already used")
		return
	case errors.Is(err, store.ErrOAuthExpired), errors.Is(err, store.ErrNotFound):
		bad("invalid_grant", "the code expired")
		return
	case err != nil:
		oauthErr(w, 500, "server_error", "could not issue tokens")
		return
	}
	writeTokens(w, t, g.Scope)
}

func (s *Server) oauthRefreshGrant(w http.ResponseWriter, r *http.Request, f url.Values, clientID string, bad func(code, desc string)) {
	ctx := r.Context()
	raw := f.Get("refresh_token")
	ti, err := s.Store.OAuthTokenByHash(ctx, crypto.SHA256(raw))
	if err != nil || raw == "" || ti.Kind != store.OAuthRefresh {
		bad("invalid_grant", "the refresh token is unknown")
		return
	}
	g := ti.Grant
	if g.ClientID != clientID {
		bad("invalid_grant", "the refresh token was issued to another client")
		return
	}
	if g.RevokedAt != nil {
		bad("invalid_grant", "this connection was revoked: sign in again")
		return
	}
	if ti.Spent {
		s.oauthRevokeGrant(r, g.ID, nil, "oauth.refresh_reuse", "a used refresh token came back; the connection was revoked")
		bad("invalid_grant", "the refresh token was already used: sign in again")
		return
	}
	if !ti.ExpiresAt.After(time.Now()) {
		bad("invalid_grant", "the refresh token expired: sign in again")
		return
	}
	if rs := f["resource"]; len(rs) > 1 || (len(rs) == 1 && !sameResource(rs[0], g.Resource)) {
		bad("invalid_target", "resource does not match this connection")
		return
	}
	if u, err := s.Store.GetUser(ctx, g.UserID); err != nil || u.Disabled || !u.Role.AtLeast(store.RoleAdmin) {
		bad("invalid_grant", "the person who approved this connection can no longer approve it")
		return
	}
	a, err := s.Store.GetApp(ctx, g.AppID)
	if err != nil || a.ShareID != nil {
		bad("invalid_grant", "the app is gone")
		return
	}
	if !a.Active {
		bad("invalid_grant", "the app is disabled")
		return
	}
	t := newTokenPair()
	switch err := s.Store.RotateOAuthRefresh(ctx, ti.ID, g.ID, t.p); {
	case errors.Is(err, store.ErrOAuthUsed):
		s.Store.AddAudit(ctx, nil, "oauth.refresh_reuse", a.Name, "client "+g.ClientName+": a used refresh token came back; the connection was revoked")
		bad("invalid_grant", "the refresh token was already used: sign in again")
		return
	case errors.Is(err, store.ErrOAuthRevoked):
		bad("invalid_grant", "this connection was revoked: sign in again")
		return
	case err != nil:
		oauthErr(w, 500, "server_error", "could not issue tokens")
		return
	}
	writeTokens(w, t, g.Scope)
}

// oauthRevokeGrant revokes a grant and audits it once, as by (nil for the
// client or the server itself).
func (s *Server) oauthRevokeGrant(r *http.Request, grantID string, by *store.User, action, why string) (*store.OAuthGrant, bool) {
	ctx := r.Context()
	g, err := s.Store.GetOAuthGrant(ctx, grantID)
	if err != nil {
		return nil, false
	}
	done, err := s.Store.RevokeOAuthGrant(ctx, grantID)
	if err != nil || !done {
		return g, false
	}
	target := strconv.FormatInt(g.AppID, 10)
	if a, err := s.Store.GetApp(ctx, g.AppID); err == nil {
		target = a.Name
	}
	s.Store.AddAudit(ctx, by, action, target, "client "+clientLabel(g.ClientName, g.ClientID)+", approved by "+g.UserEmail+": "+why)
	return g, true
}

// oauthRevoke is RFC 7009: revoking either token revokes the connection.
// The answer is 200 whether or not the token was known.
func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	o := s.oauthSt()
	key := clientKey(r)
	if !o.misses.allow(key, time.Now()) {
		w.Header().Set("Retry-After", "60")
		oauthErr(w, http.StatusTooManyRequests, "slow_down", "too many failed requests from this address")
		return
	}
	f, clientID, ok := tokenForm(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	ti, err := s.Store.OAuthTokenByHash(r.Context(), crypto.SHA256(f.Get("token")))
	if err != nil || f.Get("token") == "" {
		o.misses.take(key, time.Now())
		w.WriteHeader(http.StatusOK)
		return
	}
	if clientID != "" && clientID != ti.Grant.ClientID {
		oauthErr(w, 401, "invalid_client", "this token belongs to another client")
		return
	}
	s.oauthRevokeGrant(r, ti.Grant.ID, nil, "oauth.revoke", "revoked by the client")
	w.WriteHeader(http.StatusOK)
}
