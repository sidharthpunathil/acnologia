package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// An app can hold several named tokens, each revocable on its own. Usage,
// spend and limits stay per app.

const maxAppTokenName = 64

func appTokenName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", "name is required"
	}
	if utf8.RuneCountInString(name) > maxAppTokenName {
		return "", "name must be at most 64 characters"
	}
	return name, ""
}

func userID(r *http.Request) int64 {
	if u := userFrom(r); u != nil {
		return u.ID
	}
	return 0
}

// tokenApp loads the app named by {id}, writing the error when it fails.
func (s *Server) tokenApp(w http.ResponseWriter, r *http.Request) (*store.App, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return nil, false
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return a, true
}

func tokenIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "tokenId"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad_id", "token id must be a number")
		return 0, false
	}
	return id, true
}

func (s *Server) listAppTokens(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	toks, err := s.Store.ListAppTokens(r.Context(), a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": toks})
}

func (s *Server) createAppToken(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	var in struct {
		Name          string `json:"name"`
		ExpiresInDays *int   `json:"expires_in_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	name, msg := appTokenName(in.Name)
	if msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	var exp *time.Time
	if in.ExpiresInDays != nil {
		if *in.ExpiresInDays < 1 || *in.ExpiresInDays > 3650 {
			writeErr(w, 400, "invalid", "expires_in_days must be 1 to 3650")
			return
		}
		t := time.Now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour).UTC()
		exp = &t
	}
	tok, prefix, hash := newAppToken()
	t, err := s.Store.CreateAppToken(r.Context(), a.ID, name, store.TokenPrefixHint(prefix), hash, userID(r), exp)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// Whoever creates a token holds it. As with a rotation, an app keeps its
	// access to hidden keys only when a superadmin made the token.
	if a.LeaseHidden && !isSuper(r) {
		if err := s.Store.ClearLeaseHidden(r.Context(), a.ID); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	detail := "token " + name
	if exp != nil {
		detail += ", expires " + exp.Format(time.DateOnly)
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.token_create", a.Name, detail)
	writeJSON(w, 201, map[string]any{"token": tok, "app_token": t})
}

func (s *Server) renameAppToken(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	tid, ok := tokenIDParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	name, msg := appTokenName(in.Name)
	if msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	old, err := s.Store.GetAppToken(r.Context(), a.ID, tid)
	if err == nil {
		err = s.Store.RenameAppToken(r.Context(), a.ID, tid, name)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	t, err := s.Store.GetAppToken(r.Context(), a.ID, tid)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.token_rename", a.Name, "token "+old.Name+" renamed to "+name)
	writeJSON(w, 200, t)
}

func (s *Server) revokeAppToken(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	tid, ok := tokenIDParam(w, r)
	if !ok {
		return
	}
	t, err := s.Store.GetAppToken(r.Context(), a.ID, tid)
	if err == nil {
		err = s.Store.RevokeAppToken(r.Context(), a.ID, tid)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	t, err = s.Store.GetAppToken(r.Context(), a.ID, tid)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.token_revoke", a.Name, "token "+t.Name+" ("+t.Prefix+")")
	writeJSON(w, 200, t)
}
