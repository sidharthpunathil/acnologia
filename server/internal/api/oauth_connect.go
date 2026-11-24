package api

// OAuth credentials, the sign in side. A service of an oauth2 provider
// pools signed in accounts instead of pasted keys:
//
//  1. An admin enters the OAuth client id and secret of the app they
//     registered with the provider (PUT /api/admin/services/{id}/oauth),
//     with the redirect URI this server shows: <public URL>/oauth/callback.
//  2. Connect (POST /api/admin/services/{id}/oauth/connect) makes a single
//     use state bound to the admin and the service, and a PKCE verifier,
//     and returns the provider's sign in URL. Agents cannot approve a
//     consent screen, so they hand the URL to a person and poll
//     GET /api/admin/oauth/connects/{cid}.
//  3. The provider sends the browser to /oauth/callback, which trades the
//     code for tokens and stores them encrypted next to a new key (or the
//     key being reconnected). Each account is one key, so rotation,
//     cooldowns, health and hidden keys work as for API keys.
//
// oauth_tokens.go keeps the access tokens current. Not to be confused with
// oauth.go, the authorization server MCP clients sign in to.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// oauthCallbackPath is where providers send the browser back. The redirect
// URI to register with the provider is the public URL plus this path.
const oauthCallbackPath = "/oauth/callback"

// oauthConnectTTL is how long a sign in link works.
const oauthConnectTTL = 15 * time.Minute

func init() {
	// The control MCP's table is built in mcp_control_tools.go's init,
	// which runs before this file's: add the OAuth tools to it.
	ctlTools = append(ctlTools, ctlOAuthCredTools()...)
	indexCtlTools()
}

// mountOAuthCredentials adds the OAuth credential routes and lets the pool
// fetch access tokens for OAuth keys.
func (s *Server) mountOAuthCredentials(r chi.Router) {
	s.Pool.Resolver = s.oauthResolve
	// The browser comes back here from the provider. It is open in the
	// access gate (the state is the credential), so the account's owner
	// can finish a sign in an admin sent them.
	r.Get(oauthCallbackPath, s.oauthCallback)
	r.Group(func(r chi.Router) {
		r.Use(jsonOnly, s.requireUser)
		r.Get("/api/admin/services/{id}/oauth", s.getServiceOAuth)
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(store.RoleAdmin))
			r.Put("/api/admin/services/{id}/oauth", s.putServiceOAuth)
			r.Post("/api/admin/services/{id}/oauth/connect", s.startOAuthConnect)
			r.Post("/api/admin/keys/{id}/oauth/reconnect", s.reconnectOAuthKey)
			r.Get("/api/admin/oauth/connects/{cid}", s.getOAuthConnect)
		})
	})
}

// oauthRedirectURI is the redirect URI to register with the provider.
func (s *Server) oauthRedirectURI(r *http.Request) string {
	return strings.TrimRight(s.publicURL(r), "/") + oauthCallbackPath
}

// oauthService loads the {id} service and its provider's OAuth block, or
// answers why it has none.
func (s *Server) oauthService(w http.ResponseWriter, r *http.Request) (*store.Service, *providers.Spec, bool) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return nil, nil, false
	}
	spec, ok := s.oauthSpec(w, sv)
	return sv, spec, ok
}

func (s *Server) oauthSpec(w http.ResponseWriter, sv *store.Service) (*providers.Spec, bool) {
	spec := providers.Get(sv.Provider)
	if sv.AuthType != providers.AuthOAuth2 || spec.Auth.OAuth == nil {
		writeErr(w, 400, "not_oauth_service", fmt.Sprintf("%s takes API keys, not OAuth sign in: add keys to it instead", sv.Slug))
		return nil, false
	}
	return spec, true
}

type oauthConnectionView struct {
	KeyID          int64      `json:"key_id"`
	Label          string     `json:"label"`
	Account        string     `json:"account"`
	Status         string     `json:"status"`
	Hidden         bool       `json:"hidden"`
	Scopes         []string   `json:"scopes"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RefreshedAt    *time.Time `json:"refreshed_at,omitempty"`
	ConnectedAt    time.Time  `json:"connected_at"`
	LastError      string     `json:"last_error,omitempty"`
	NeedsReconnect bool       `json:"needs_reconnect"`
}

type oauthSettingsView struct {
	ServiceID       int64                      `json:"service_id"`
	Service         string                     `json:"service"`
	Provider        string                     `json:"provider"`
	Configured      bool                       `json:"configured"`
	ClientID        string                     `json:"client_id"`
	HasClientSecret bool                       `json:"has_client_secret"`
	Scopes          []string                   `json:"scopes"`
	DefaultScopes   []string                   `json:"default_scopes"`
	AllowedScopes   []string                   `json:"allowed_scopes"`
	RedirectURI     string                     `json:"redirect_uri"`
	PublicURLSet    bool                       `json:"public_url_set"`
	PKCE            bool                       `json:"pkce"`
	AuthorizeURL    string                     `json:"authorize_url"`
	Fields          []providers.CredentialPart `json:"fields"`
	Connections     []oauthConnectionView      `json:"connections"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// getServiceOAuth is GET /api/admin/services/{id}/oauth: the OAuth client
// (never its secret), the redirect URI to register, and the connected
// accounts (never their tokens).
func (s *Server) getServiceOAuth(w http.ResponseWriter, r *http.Request) {
	sv, spec, ok := s.oauthService(w, r)
	if !ok {
		return
	}
	cfg := spec.Auth.OAuth
	v := oauthSettingsView{
		ServiceID: sv.ID, Service: sv.Slug, Provider: sv.Provider,
		Scopes: cfg.Scopes, DefaultScopes: orEmpty(cfg.Scopes), AllowedScopes: orEmpty(cfg.AllowedScopes),
		RedirectURI: s.oauthRedirectURI(r), PublicURLSet: s.Cfg.PublicURL != "", PKCE: cfg.PKCE, AuthorizeURL: cfg.AuthorizeURL,
		Fields: spec.Auth.Parts(), Connections: []oauthConnectionView{},
	}
	if c, err := s.Store.GetServiceOAuthClient(r.Context(), sv.ID); err == nil {
		v.Configured, v.ClientID, v.HasClientSecret = c.ClientID != "", c.ClientID, len(c.ClientSecretEnc) > 0
		if len(c.Scopes) > 0 {
			v.Scopes = c.Scopes
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	v.Scopes = orEmpty(v.Scopes)
	keys, err := s.Store.ListKeys(r.Context(), sv.ID, isSuper(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	byID := map[int64]*store.Key{}
	for _, k := range keys {
		byID[k.ID] = k
	}
	creds, err := s.Store.ListOAuthCredentials(r.Context(), sv.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	for _, c := range creds {
		k := byID[c.KeyID]
		if k == nil {
			continue // hidden from this user, or deleted
		}
		v.Connections = append(v.Connections, oauthConnectionView{
			KeyID: k.ID, Label: k.Label, Account: c.Account, Status: k.Status, Hidden: k.Hidden, Scopes: c.Scopes,
			ExpiresAt: timePtr(c.ExpiresAt), RefreshedAt: timePtr(c.RefreshedAt), ConnectedAt: c.ConnectedAt,
			LastError: c.LastError, NeedsReconnect: k.Status == store.StatusInvalid,
		})
	}
	writeJSON(w, 200, v)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type oauthClientInput struct {
	ClientID     string    `json:"client_id"`
	ClientSecret *string   `json:"client_secret"`
	Scopes       *[]string `json:"scopes"`
}

// putServiceOAuth is PUT /api/admin/services/{id}/oauth: the OAuth client
// the service signs in with. Leaving client_secret out keeps the stored
// one; leaving scopes out keeps them, and an empty list uses the
// provider's.
func (s *Server) putServiceOAuth(w http.ResponseWriter, r *http.Request) {
	sv, spec, ok := s.oauthService(w, r)
	if !ok {
		return
	}
	var in oauthClientInput
	if !decode(w, r, &in) {
		return
	}
	in.ClientID = strings.TrimSpace(in.ClientID)
	if in.ClientID == "" || len(in.ClientID) > 512 || strings.ContainsAny(in.ClientID, "\r\n") {
		writeErr(w, 400, "invalid_oauth_client", "client_id is required: one line, at most 512 characters")
		return
	}
	c := store.ServiceOAuthClient{ServiceID: sv.ID, ClientID: in.ClientID, UpdatedBy: userFrom(r).ID}
	old, err := s.Store.GetServiceOAuthClient(r.Context(), sv.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	if old != nil {
		c.Scopes = old.Scopes
	}
	if in.Scopes != nil {
		scopes := []string{}
		for _, sc := range *in.Scopes {
			if sc = strings.TrimSpace(sc); sc != "" {
				scopes = append(scopes, sc)
			}
		}
		if msg := spec.Auth.OAuth.ScopeProblem(scopes); msg != "" {
			writeErr(w, 400, "invalid_oauth_client", "scopes: "+msg)
			return
		}
		c.Scopes = scopes
	}
	if in.ClientSecret != nil {
		sec := strings.TrimSpace(*in.ClientSecret)
		if len(sec) > 2048 || strings.ContainsAny(sec, "\r\n") {
			writeErr(w, 400, "invalid_oauth_client", "client_secret must be one line, at most 2048 characters")
			return
		}
		if sec != "" {
			if c.ClientSecretEnc, err = s.Pool.Box.Encrypt(sec); err != nil {
				writeStoreErr(w, err)
				return
			}
		}
	}
	if err := s.Store.SetServiceOAuthClient(r.Context(), c); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.oauth_client", sv.Slug, "client "+in.ClientID+", scopes "+strings.Join(c.Scopes, " "))
	s.getServiceOAuth(w, r)
}

type oauthConnectInput struct {
	// Fields are the provider's extra values for this account, such as a
	// Google Ads developer token.
	Fields map[string]string `json:"fields"`
}

// decodeOptional reads a JSON body when there is one.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, 400, "bad_json", err.Error())
		return false
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return true
	}
	if err := json.Unmarshal(b, v); err != nil {
		writeErr(w, 400, "bad_json", err.Error())
		return false
	}
	return true
}

// startOAuthConnect is POST /api/admin/services/{id}/oauth/connect.
func (s *Server) startOAuthConnect(w http.ResponseWriter, r *http.Request) {
	sv, spec, ok := s.oauthService(w, r)
	if !ok {
		return
	}
	var in oauthConnectInput
	if !decodeOptional(w, r, &in) {
		return
	}
	s.beginOAuthConnect(w, r, sv, spec, 0, in.Fields)
}

// reconnectOAuthKey is POST /api/admin/keys/{id}/oauth/reconnect: sign the
// key's account in again (after its grant was revoked or expired). The
// key keeps its id, label, settings and fields unless new fields are given.
func (s *Server) reconnectOAuthKey(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	spec, ok := s.oauthSpec(w, sv)
	if !ok {
		return
	}
	var in oauthConnectInput
	if !decodeOptional(w, r, &in) {
		return
	}
	if len(in.Fields) == 0 {
		stored, err := s.Pool.Box.Decrypt(k.SecretEnc)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		in.Fields = providers.OAuthFields(stored)
	}
	s.beginOAuthConnect(w, r, sv, spec, k.ID, in.Fields)
}

type oauthConnectView struct {
	ConnectID   string    `json:"connect_id"`
	ServiceID   int64     `json:"service_id"`
	Service     string    `json:"service,omitempty"`
	Status      string    `json:"status"`
	URL         string    `json:"url,omitempty"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
	StatusURL   string    `json:"status_url"`
	KeyID       int64     `json:"key_id,omitempty"`
	Account     string    `json:"account,omitempty"`
	Error       string    `json:"error,omitempty"`
	Next        string    `json:"next,omitempty"`
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthAuthorizeURL is the provider's sign in URL for one connect.
func oauthAuthorizeURL(cfg *providers.OAuthConfig, clientID, redirect, state, verifier string, scopes []string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range cfg.AuthorizeParams {
		q.Set(k, v)
	}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, cfg.Separator()))
	}
	if cfg.PKCE {
		q.Set("code_challenge", pkceChallenge(verifier))
		q.Set("code_challenge_method", "S256")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Server) beginOAuthConnect(w http.ResponseWriter, r *http.Request, sv *store.Service, spec *providers.Spec, keyID int64, fields map[string]string) {
	ctx := r.Context()
	cl, err := s.Store.GetServiceOAuthClient(ctx, sv.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && cl.ClientID == "") {
		writeErr(w, 409, "oauth_not_configured", fmt.Sprintf("%s has no OAuth client yet: register an app with the provider using the redirect URI %s, then set its client id and secret", sv.Slug, s.oauthRedirectURI(r)))
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	clean := map[string]string{}
	for k, v := range fields {
		if v = strings.TrimSpace(v); v != "" {
			clean[k] = v
		}
	}
	if msg := spec.Auth.OAuthFieldsProblem(clean); msg != "" {
		writeErr(w, 400, "invalid_oauth_fields", msg)
		return
	}
	cfg := spec.Auth.OAuth
	scopes := cl.Scopes
	if len(scopes) == 0 {
		scopes = cfg.Scopes
	}
	state := crypto.RandomToken("", 32)
	verifier := crypto.RandomToken("", 48)
	verifierEnc, err := s.Pool.Box.Encrypt(verifier)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	fb, _ := json.Marshal(clean)
	fieldsEnc, err := s.Pool.Box.Encrypt(string(fb))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	redirect := s.oauthRedirectURI(r)
	link, err := oauthAuthorizeURL(cfg, cl.ClientID, redirect, state, verifier, scopes)
	if err != nil {
		writeErr(w, 400, "not_oauth_service", "the provider's authorize_url is not a URL")
		return
	}
	now := time.Now()
	c := store.OAuthConnect{
		ID: crypto.RandomToken("oc_", 12), StateHash: crypto.SHA256(state), ServiceID: sv.ID, UserID: userFrom(r).ID, KeyID: keyID,
		VerifierEnc: verifierEnc, FieldsEnc: fieldsEnc, Scopes: scopes, RedirectURI: redirect, CreatedAt: now, ExpiresAt: now.Add(oauthConnectTTL),
	}
	if err := s.Store.CreateOAuthConnect(ctx, c); err != nil {
		writeStoreErr(w, err)
		return
	}
	what := "connect an account"
	if keyID > 0 {
		what = fmt.Sprintf("reconnect key %d", keyID)
	}
	s.Store.AddAudit(ctx, userFrom(r), "key.oauth_connect_start", sv.Slug, what)
	writeJSON(w, 201, oauthConnectView{
		ConnectID: c.ID, ServiceID: sv.ID, Service: sv.Slug, Status: store.OAuthConnectPending, URL: link, RedirectURI: redirect,
		ExpiresAt: c.ExpiresAt, StatusURL: "/api/admin/oauth/connects/" + c.ID, KeyID: keyID,
		Next: "Open url in a browser signed in to the account (or send it to the account's owner) and approve. Then poll status_url until status is done or failed. The link works once, for 15 minutes.",
	})
}

// getOAuthConnect is GET /api/admin/oauth/connects/{cid}: how a sign in
// went. Status is pending, exchanging, done, failed or expired.
func (s *Server) getOAuthConnect(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetOAuthConnect(r.Context(), chi.URLParam(r, "cid"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	sv, err := s.Store.GetService(r.Context(), c.ServiceID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v := oauthConnectView{ConnectID: c.ID, ServiceID: c.ServiceID, Service: sv.Slug, Status: c.Status, RedirectURI: c.RedirectURI,
		ExpiresAt: c.ExpiresAt, StatusURL: "/api/admin/oauth/connects/" + c.ID, KeyID: c.KeyID, Account: c.Account, Error: c.Error}
	if c.Status == store.OAuthConnectPending && time.Now().After(c.ExpiresAt) {
		v.Status = "expired"
		v.Next = "Start again: the link was not used in time."
	}
	if c.Status == store.OAuthConnectDone {
		if k, err := s.Store.GetKey(r.Context(), c.KeyID); err == nil && k.Hidden && !isSuper(r) {
			v.KeyID = 0
		}
	}
	writeJSON(w, 200, v)
}

// ---- the callback ----

var oauthCredPage = template.Must(template.New("oauth").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{--bg:#f7f7f5;--card:#fff;--fg:#1b1b18;--muted:#6b6b66;--line:#e4e4df;--ok:#1f7a4d;--bad:#b3261e}
@media (prefers-color-scheme:dark){:root{--bg:#121211;--card:#1c1c1a;--fg:#ededea;--muted:#a3a39d;--line:#2e2e2b;--ok:#5fc48f;--bad:#f2867d}}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,-apple-system,Segoe UI,sans-serif;padding:16px;box-sizing:border-box}
main{max-width:420px;width:100%;background:var(--card);border:1px solid var(--line);border-radius:12px;padding:24px}
h1{font-size:18px;margin:0 0 8px;color:{{if .OK}}var(--ok){{else}}var(--bad){{end}}}
p{margin:8px 0;color:var(--muted);overflow-wrap:anywhere}
a{color:var(--fg)}
</style></head><body><main>
<h1>{{.Title}}</h1>
{{range .Lines}}<p>{{.}}</p>{{end}}
{{if .Link}}<p><a href="{{.Link}}">Open the service</a></p>{{end}}
</main></body></html>`))

type oauthPageData struct {
	OK    bool
	Title string
	Lines []string
	Link  string
}

func writeOAuthPage(w http.ResponseWriter, status int, d oauthPageData) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = oauthCredPage.Execute(w, d)
}

// oauthCallback is GET /oauth/callback, where the provider sends the
// browser after the account's owner approved (or refused). The state is
// single use and expires; the code is traded with the PKCE verifier.
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		writeOAuthPage(w, 400, oauthPageData{Title: "Sign in not finished", Lines: []string{"This page needs the state the provider sends back. Start again from the service page."}})
		return
	}
	c, err := s.Store.TakeOAuthConnect(ctx, crypto.SHA256(state), time.Now())
	if err != nil {
		writeOAuthPage(w, 400, oauthPageData{Title: "Link not valid", Lines: []string{"This sign in link is unknown, already used or expired. Start again from the service page."}})
		return
	}
	link := "/services/" + strconv.FormatInt(c.ServiceID, 10)
	fail := func(msg string) {
		_ = s.Store.FinishOAuthConnect(context.WithoutCancel(ctx), c.ID, store.OAuthConnectFailed, c.KeyID, "", msg)
		writeOAuthPage(w, 400, oauthPageData{Title: "Could not connect the account", Lines: []string{s.Store.Scrub(ctx, msg), "Start again from the service page."}, Link: link})
	}
	if e := q.Get("error"); e != "" {
		msg := "the provider said " + crypto.Truncate(e, 100)
		if d := q.Get("error_description"); d != "" {
			msg += ": " + crypto.Truncate(d, 300)
		}
		fail(msg)
		return
	}
	code := q.Get("code")
	if code == "" {
		fail("the provider sent no code")
		return
	}
	sv, err := s.Store.GetService(ctx, c.ServiceID)
	if err != nil {
		fail("the service is gone")
		return
	}
	spec := providers.Get(sv.Provider)
	if sv.AuthType != providers.AuthOAuth2 || spec.Auth.OAuth == nil {
		fail("the service no longer signs in with OAuth")
		return
	}
	cfg := spec.Auth.OAuth
	cl, err := s.loadOAuthClient(ctx, sv.ID)
	if err != nil {
		fail("the service's OAuth client is not set")
		return
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.RedirectURI}}
	if cfg.PKCE {
		verifier, err := s.Pool.Box.Decrypt(c.VerifierEnc)
		if err != nil {
			fail("cannot read the sign in's PKCE verifier")
			return
		}
		form.Set("code_verifier", verifier)
	}
	t, err := s.oauthTokenCall(ctx, cfg, cl, form)
	if err != nil {
		fail("the code could not be traded for tokens: " + err.Error())
		return
	}
	fields := map[string]string{}
	if len(c.FieldsEnc) > 0 {
		if fb, err := s.Pool.Box.Decrypt(c.FieldsEnc); err == nil {
			_ = json.Unmarshal([]byte(fb), &fields)
		}
	}
	account := s.oauthAccount(ctx, cfg, t)
	k, created, err := s.storeOAuthConnection(context.WithoutCancel(ctx), sv, spec, c, t, account, fields)
	if err != nil {
		fail("the account could not be stored: " + err.Error())
		return
	}
	_ = s.Store.FinishOAuthConnect(context.WithoutCancel(ctx), c.ID, store.OAuthConnectDone, k.ID, account, "")
	by, _ := s.Store.GetUser(ctx, c.UserID)
	what := "reconnected"
	if created {
		what = "connected"
	}
	s.Store.AddAudit(ctx, by, "key.oauth_connect", sv.Slug, fmt.Sprintf("%s key %d (%s)", what, k.ID, orDefault(account, "account not named")))
	name := account
	if name == "" {
		name = "The account"
	}
	lines := []string{fmt.Sprintf("%s is %s to %s. You can close this tab.", name, what, sv.Name)}
	if t.RefreshToken == "" && !t.expiry(time.Now()).IsZero() {
		lines = append(lines, "The provider sent no refresh token, so this account stops working when its access token expires. Check the provider's offline access settings, then reconnect.")
	}
	writeOAuthPage(w, 200, oauthPageData{OK: true, Title: "Account " + what, Lines: lines, Link: link})
}

// storeOAuthConnection stores the tokens of a signed in account: on the key
// being reconnected, on the key that already holds the same account, or
// on a new key. It reports whether the key is new.
func (s *Server) storeOAuthConnection(ctx context.Context, sv *store.Service, spec *providers.Spec, c *store.OAuthConnect,
	t *oauthTokens, account string, fields map[string]string) (*store.Key, bool, error) {
	box := s.Pool.Box
	var target *store.Key
	if c.KeyID > 0 {
		k, err := s.Store.GetKey(ctx, c.KeyID)
		if err != nil || k.ServiceID != sv.ID {
			return nil, false, errors.New("the key being reconnected is gone")
		}
		target = k
	} else if account != "" {
		creds, err := s.Store.ListOAuthCredentials(ctx, sv.ID)
		if err != nil {
			return nil, false, err
		}
		for _, cr := range creds {
			if strings.EqualFold(cr.Account, account) {
				if k, err := s.Store.GetKey(ctx, cr.KeyID); err == nil && k.ServiceID == sv.ID {
					target = k
				}
				break
			}
		}
	}
	accessEnc, err := box.Encrypt(t.AccessToken)
	if err != nil {
		return nil, false, err
	}
	var refreshEnc []byte
	if t.RefreshToken != "" {
		if refreshEnc, err = box.Encrypt(t.RefreshToken); err != nil {
			return nil, false, err
		}
	}
	scopes := c.Scopes
	if t.Scope != "" {
		scopes = strings.FieldsFunc(t.Scope, func(r rune) bool { return r == ' ' || r == ',' })
	}
	cred := store.OAuthCredential{ServiceID: sv.ID, Account: account, Scopes: scopes, RefreshEnc: refreshEnc, AccessEnc: accessEnc,
		ExpiresAt: t.expiry(time.Now()), ConnectedBy: c.UserID}
	auth := serviceAuth(sv)
	if target != nil {
		cred.KeyID = target.ID
		if old, err := s.Store.GetOAuthCredential(ctx, target.ID); err == nil {
			if len(refreshEnc) == 0 {
				// A second sign in may come without a refresh token: keep the first.
				cred.RefreshEnc = old.RefreshEnc
			}
			if account == "" {
				cred.Account = old.Account
			}
		}
		stored, err := box.Decrypt(target.SecretEnc)
		if err != nil {
			return nil, false, err
		}
		if len(fields) > 0 {
			var m map[string]any
			_ = json.Unmarshal([]byte(stored), &m)
			conn, _ := m[providers.OAuthConnectionField].(string)
			if conn == "" {
				conn = crypto.RandomToken("", 12)
			}
			if next := auth.OAuthStoredSecret(conn, fields); next != stored {
				enc, err := box.Encrypt(next)
				if err != nil {
					return nil, false, err
				}
				if err := s.Store.UpdateKey(ctx, target.ID, store.KeyPatch{SecretEnc: enc, SecretFP: box.Fingerprint(next), Hint: target.Hint}); err != nil {
					return nil, false, err
				}
			}
		}
		if err := s.Store.PutOAuthCredential(ctx, cred); err != nil {
			return nil, false, err
		}
		// The account works again: back into rotation. A disabled or banned
		// key stays as a person left it.
		active, zero := store.StatusActive, 0
		_ = s.Store.ApplyEffect(ctx, target.ID, store.KeyEffect{Status: &active, ClearCooldown: true, ConsecutiveFails: &zero, LastError: ptrStr(""), MarkOK: true})
		return target, false, nil
	}
	conn := crypto.RandomToken("", 12)
	stored := auth.OAuthStoredSecret(conn, fields)
	enc, err := box.Encrypt(stored)
	if err != nil {
		return nil, false, err
	}
	hint := "..." + conn[len(conn)-4:]
	label := account
	if label == "" {
		label = fmt.Sprintf("%s account %s", sv.Name, hint[3:])
	}
	nk := store.NewKey{ServiceID: sv.ID, Label: label, SecretEnc: enc, SecretFP: box.Fingerprint(stored), Hint: hint,
		Tier: "primary", Weight: 1, Priority: 100, CreatedBy: c.UserID}
	if strings.Contains(account, "@") {
		nk.Account.Email = account
	} else {
		nk.Account.Name = account
	}
	k, err := s.Store.CreateKey(ctx, nk)
	if err != nil {
		return nil, false, err
	}
	cred.KeyID = k.ID
	if err := s.Store.PutOAuthCredential(ctx, cred); err != nil {
		_ = s.Store.DeleteKey(ctx, k.ID)
		return nil, false, err
	}
	return k, true, nil
}
