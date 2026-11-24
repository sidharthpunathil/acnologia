package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const tokenKey ctxKey = 100

// tokenUser authenticates an admin API token. The request acts as the
// token's owner with the lower of the owner's role and the token's scope.
func (s *Server) tokenUser(w http.ResponseWriter, r *http.Request, tok string, next http.Handler) {
	t, owner, err := s.Store.TokenByHash(r.Context(), crypto.SHA256(tok))
	if err != nil {
		writeErr(w, 401, "unauthenticated", "this admin API token is invalid, expired or revoked; run acn login for a new one")
		return
	}
	role, _ := store.ScopeRole(t.Scope)
	u := *owner
	if !role.AtLeast(u.Role) {
		u.Role = role
	}
	ctx := context.WithValue(r.Context(), userKey, &u)
	ctx = context.WithValue(ctx, tokenKey, t)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// sessionOnly refuses requests authenticated with an API token.
func (s *Server) sessionOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(tokenKey) != nil {
			writeErr(w, 403, "session_required", "sign in to the dashboard for this; API tokens cannot do it")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionOrSelfToken lets a dashboard session through, and an API token only
// when the route's {id} is that token's own: `acn logout` revokes the token
// it signed in with, and a token can never touch another one.
func (s *Server) sessionOrSelfToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, ok := r.Context().Value(tokenKey).(*store.AdminToken)
		if ok && strconv.FormatInt(t.ID, 10) != chi.URLParam(r, "id") {
			writeErr(w, 403, "session_required", "an API token can revoke only itself; sign in to the dashboard for the others")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validAdminToken reports whether r carries a live admin API token
// (Authorization: Bearer acnadm_...). App tokens never count. A control MCP
// OAuth token counts only on /mcp/control itself and on the calls it makes
// in process.
func (s *Server) validAdminToken(r *http.Request) bool {
	if tok, ok := oauthBearer(r); ok && (inControlOAuth(r) || r.URL.Path == controlPath) {
		_, _, why := s.authControlOAuth(r, tok)
		return why == ""
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !store.IsAdminToken(tok) {
		return false
	}
	_, _, err := s.Store.TokenByHash(r.Context(), crypto.SHA256(tok))
	return err == nil
}

// controlOAuthKey marks a request that /mcp/control authenticated with an
// OAuth access token, and so the admin routes its tools call in process.
// Only mcpControl sets it, and nothing outside this package can, so an
// OAuth token reaches an admin route only through the control MCP.
type controlOAuthKey struct{}

func inControlOAuth(r *http.Request) bool {
	v, _ := r.Context().Value(controlOAuthKey{}).(bool)
	return v
}

// controlCaller authenticates a control MCP request: an admin API token, or
// an OAuth access token issued for /mcp/control. It returns the person the
// request acts as (their role lowered to the scope) and the request to go
// on with; or nil and why it is refused. An OAuth request is marked, so the
// admin routes the tools call accept the token, and attributed to OAuth with
// the grant in the client.
func (s *Server) controlCaller(r *http.Request) (*store.User, *http.Request, string) {
	tok, ok := oauthBearer(r)
	if !ok {
		u, msg := s.controlUser(r)
		return u, r, msg
	}
	u, g, why := s.authControlOAuth(r, tok)
	if u == nil {
		return nil, r, why
	}
	ctx := context.WithValue(r.Context(), controlOAuthKey{}, true)
	ctx = context.WithValue(ctx, controlGrantKey{}, g)
	return u, r.WithContext(ctx), ""
}

type controlGrantKey struct{}

// controlOAuthAttribution is a control MCP OAuth request's attribution:
// via oauth, with the grant in front of the client from initialize, the
// same shape as an app MCP OAuth call. Other requests keep theirs.
func controlOAuthAttribution(r *http.Request) *http.Request {
	g, ok := r.Context().Value(controlGrantKey{}).(*store.OAuthGrant)
	if !ok {
		return r
	}
	at := store.AttributionFrom(r.Context())
	at.Via = store.ViaOAuth
	at.Client = strings.TrimSpace(oauthGrantPrefix + g.ID + " " + at.Client)
	return r.WithContext(store.WithAttribution(r.Context(), at))
}

// controlOAuthUser is requireUser for an OAuth access token: it acts only
// on a call the control MCP made in process, and checks the token again
// there, so a connection revoked or a role lowered mid call stops at once.
// The token stands in for an admin API token with the connection's scope,
// so sessionOnly routes refuse it and role checks apply as for one.
func (s *Server) controlOAuthUser(w http.ResponseWriter, r *http.Request, tok string, next http.Handler) {
	if !inControlOAuth(r) {
		writeErr(w, 401, "unauthenticated", "an MCP access token (acno_) works only on the MCP endpoint it was issued for; the admin API takes a dashboard session or an admin API token")
		return
	}
	u, g, why := s.authControlOAuth(r, tok)
	if u == nil {
		writeErr(w, 401, "unauthenticated", why)
		return
	}
	ctx := context.WithValue(r.Context(), userKey, u)
	ctx = context.WithValue(ctx, tokenKey, &store.AdminToken{Name: "OAuth: " + g.ClientName, Scope: g.Access, UserID: u.ID, UserEmail: u.Email})
	next.ServeHTTP(w, r.WithContext(ctx))
}
