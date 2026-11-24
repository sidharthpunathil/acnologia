package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The dashboard's consent page (/oauth/consent?request=<id>) reads the
// authorization request, then approves or denies it. Approving needs what
// creating one of the app's tokens needs: an admin, an app that does not
// back a share, and a dashboard session. The answer is the URL to send the
// browser to, with the code or the error.

func (s *Server) oauthPendingFor(r *http.Request) (string, *oauthPending) {
	id := chi.URLParam(r, "rid")
	o := s.oauthSt()
	o.mu.Lock()
	defer o.mu.Unlock()
	p := o.pending[id]
	if p == nil || time.Since(p.created) > oauthRequestTTL {
		delete(o.pending, id)
		return id, nil
	}
	return id, p
}

// takePending removes a request so only one decision uses it.
func (s *Server) takePending(id string, want *oauthPending) bool {
	o := s.oauthSt()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pending[id] != want {
		return false
	}
	delete(o.pending, id)
	return true
}

func writeExpiredRequest(w http.ResponseWriter) {
	writeErr(w, 404, "request_expired", "This sign in request expired or was already used. Start the connection again from your app.")
}

func canApproveOAuth(r *http.Request) bool {
	u := userFrom(r)
	return u != nil && u.Role.AtLeast(store.RoleAdmin) && !fromAssistant(r)
}

func (s *Server) oauthRequestInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, p := s.oauthPendingFor(r)
	if p == nil {
		writeExpiredRequest(w)
		return
	}
	ok := canApproveOAuth(r)
	apps := []map[string]any{}
	if ok {
		list, err := s.Store.ListApps(r.Context())
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		for _, a := range list {
			apps = append(apps, map[string]any{"id": a.ID, "name": a.Name, "description": a.Description, "active": a.Active})
		}
	}
	verified := ""
	if p.client.Kind == store.OAuthClientCIMD {
		verified = oauthHost(p.client.ID)
	}
	writeJSON(w, 200, map[string]any{
		"request": map[string]any{
			"id":              id,
			"client_name":     p.client.Name,
			"client_id":       p.client.ID,
			"verified_domain": verified,
			"redirect_uri":    p.redirectURI,
			"redirect_host":   redirectLabel(p.redirectURI),
			"resource":        p.resource,
			"scope":           p.scope,
			"expires_at":      p.created.Add(oauthRequestTTL).UTC(),
		},
		"apps":        apps,
		"can_approve": ok,
	})
}

// redirectLabel is what the consent page says the browser goes back to.
func redirectLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Host != "" {
		return u.Host
	}
	return u.Scheme + ":"
}

func (s *Server) oauthApprove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		AppID    int64  `json:"app_id"`
		NewApp   string `json:"new_app"`
		ReadOnly bool   `json:"read_only"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !canApproveOAuth(r) {
		writeErr(w, 403, "forbidden", "only an admin can connect an app")
		return
	}
	id, p := s.oauthPendingFor(r)
	if p == nil {
		writeExpiredRequest(w)
		return
	}
	ctx := r.Context()
	newName := strings.TrimSpace(in.NewApp)
	var a *store.App
	switch {
	case newName != "":
		if len([]rune(newName)) > 100 {
			writeErr(w, 400, "invalid", "the app name must be at most 100 characters")
			return
		}
	case in.AppID > 0:
		var err error
		if a, err = s.Store.GetApp(ctx, in.AppID); err != nil {
			writeStoreErr(w, err)
			return
		}
		if a.ShareID != nil {
			writeErr(w, 409, "share_app", "this app backs a share to another server; pick another app")
			return
		}
	default:
		writeErr(w, 400, "invalid", "pick an app or name a new one")
		return
	}
	if !s.takePending(id, p) {
		writeExpiredRequest(w)
		return
	}
	u := userFrom(r)
	if a == nil {
		created, err := s.Store.CreateBareApp(ctx, &store.App{Name: newName, Mode: "both", Active: true}, crypto.RandomToken("", 24))
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		a = created
		s.Store.AddAudit(ctx, u, "app.create", a.Name, "for MCP client "+p.client.Name)
	}
	// As with a new app token: whoever creates a credential holds it, so an
	// app keeps access to hidden keys only when a superadmin connects it.
	if a.LeaseHidden && !isSuper(r) {
		if err := s.Store.ClearLeaseHidden(ctx, a.ID); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	code := crypto.RandomToken(oauthCodePrefix, 32)
	if err := s.Store.CreateOAuthCode(ctx, &store.OAuthCode{
		Hash: crypto.SHA256(code), ClientID: p.client.ID, RedirectURI: p.redirectURI, CodeChallenge: p.challenge,
		Resource: p.resource, Scope: p.scope, AppID: a.ID, UserID: u.ID, ReadOnly: in.ReadOnly,
		ExpiresAt: time.Now().Add(oauthCodeTTL),
	}); err != nil {
		writeStoreErr(w, err)
		return
	}
	detail := "client " + clientLabel(p.client.Name, p.client.ID)
	if in.ReadOnly {
		detail += ", read-only"
	}
	s.Store.AddAudit(ctx, u, "oauth.grant", a.Name, detail)
	writeJSON(w, 200, map[string]any{
		"redirect": withParams(p.redirectURI, "code", code, "state", p.state, "iss", s.publicURL(r)),
		"app":      map[string]any{"id": a.ID, "name": a.Name},
	})
}

func (s *Server) oauthDeny(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, p := s.oauthPendingFor(r)
	if p == nil || !s.takePending(id, p) {
		writeExpiredRequest(w)
		return
	}
	writeJSON(w, 200, map[string]any{
		"redirect": withParams(p.redirectURI, "error", "access_denied", "error_description", "the request was denied",
			"state", p.state, "iss", s.publicURL(r)),
	})
}

// listOAuthConnections lists an app's OAuth connections, for whoever may
// see its tokens.
func (s *Server) listOAuthConnections(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	list, err := s.Store.ListOAuthGrants(r.Context(), a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connections": list})
}

func (s *Server) revokeOAuthConnection(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	g, err := s.Store.GetOAuthGrant(r.Context(), chi.URLParam(r, "grantId"))
	if err != nil || g.AppID != a.ID {
		writeErr(w, 404, "not_found", "not found")
		return
	}
	s.oauthRevokeGrant(r, g.ID, userFrom(r), "oauth.revoke", "revoked from the dashboard")
	g, err = s.Store.GetOAuthGrant(r.Context(), g.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, g)
}
