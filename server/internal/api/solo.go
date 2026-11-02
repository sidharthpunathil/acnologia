package api

import (
	"context"
	"net/http"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Password only sign in. With the access gate on and
// ACNOLOGIA_ACCESS_SIGNS_IN true (the default), the access password is the
// whole login: a browser past the lock screen is signed in as the owner, the
// oldest active superadmin, without an email form. Someone who wants separate
// accounts behind the gate sets ACNOLOGIA_ACCESS_SIGNS_IN=false.

// soloMode reports whether the access password alone signs a browser in.
func (s *Server) soloMode() bool { return s.gate.on() && s.Cfg.AccessSignsIn }

// soloOwner returns the account a password only browser acts as, creating a
// superadmin when the database has no users yet. The new account's password
// is random and never shown: the access password is how it signs in.
func (s *Server) soloOwner(ctx context.Context) (*store.User, error) {
	s.userMu.Lock()
	defer s.userMu.Unlock()
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Role == store.RoleSuperadmin && !u.Disabled {
			return u, nil
		}
	}
	if len(users) > 0 {
		return nil, store.ErrNotFound
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(crypto.RandomToken("", 32)), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u, err := s.Store.CreateUser(ctx, "owner@acnologia.local", "Owner", string(hash), store.RoleSuperadmin)
	if err != nil {
		return nil, err
	}
	s.Store.AddAudit(ctx, u, "setup", u.Email, "created the owner for password only sign in")
	return u, nil
}

// soloSignIn signs a browser that passed the lock screen in as the owner and
// returns the user and session hash. Only the gate cookie counts: a script
// sending the access header or an admin token gets no session. It returns
// nil when it does not apply.
func (s *Server) soloSignIn(w http.ResponseWriter, r *http.Request) (*store.User, string) {
	if !s.soloMode() {
		return nil, ""
	}
	if c, err := r.Cookie(gateCookie); err != nil || !s.gate.valid(c.Value) {
		return nil, ""
	}
	u, err := s.soloOwner(r.Context())
	if err != nil {
		return nil, ""
	}
	tok := crypto.RandomToken("", 32)
	h := crypto.SHA256(tok)
	if err := s.Store.CreateSession(r.Context(), h, u.ID, sessionTTL); err != nil {
		return nil, ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.secureCookie(r), MaxAge: int(sessionTTL.Seconds()),
	})
	s.Store.TouchLogin(r.Context(), u.ID)
	return u, h
}
