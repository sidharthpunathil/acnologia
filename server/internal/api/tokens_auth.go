package api

import (
	"context"
	"net/http"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const tokenKey ctxKey = 100

// tokenUser authenticates an admin API token. The request acts as the
// token's owner with the lower of the owner's role and the token's scope.
func (s *Server) tokenUser(w http.ResponseWriter, r *http.Request, tok string, next http.Handler) {
	t, owner, err := s.Store.TokenByHash(r.Context(), crypto.SHA256(tok))
	if err != nil {
		writeErr(w, 401, "unauthenticated", "API token is invalid, expired or revoked")
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
