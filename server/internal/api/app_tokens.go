package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// An app can hold several named tokens, each revocable on its own. Usage
// and spend count toward the app and the token. A token may carry limits of
// its own inside the app's, and be pinned to caller tags.

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
		ReadOnly      bool   `json:"read_only"`
		// Limits and PinnedTags are the token's own controls (see
		// tokenControls).
		Limits     store.AppLimits   `json:"limits"`
		PinnedTags map[string]string `json:"pinned_tags"`
	}
	if !decode(w, r, &in) {
		return
	}
	name, msg := appTokenName(in.Name)
	if msg == "" {
		msg = tokenControlsError(in.Limits, in.PinnedTags)
	}
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
	t, err := s.Store.CreateAppTokenWith(r.Context(), a.ID, name, store.TokenPrefixHint(prefix), hash, userID(r), exp,
		store.AppTokenOptions{ReadOnly: in.ReadOnly, Limits: in.Limits, PinnedTags: in.PinnedTags})
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
	if in.ReadOnly {
		detail += ", read-only"
	}
	if exp != nil {
		detail += ", expires " + exp.Format(time.DateOnly)
	}
	if in.Limits.Any() {
		detail += ", " + limitsText(in.Limits)
	}
	if len(in.PinnedTags) > 0 {
		detail += ", pinned to " + store.FormatTags(in.PinnedTags)
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.token_create", a.Name, detail)
	writeJSON(w, 201, map[string]any{"token": tok, "app_token": t})
}

// tokenControlsError checks a token's own limits and pinned tags.
func tokenControlsError(l store.AppLimits, pinned map[string]string) string {
	if err := l.Validate(); err != nil {
		return err.Error()
	}
	if err := store.ValidateTags(pinned); err != nil {
		return "pinned_tags: " + err.Error()
	}
	return ""
}

// renameAppToken changes a token's name, its own limits or its pinned
// tags: each field sent replaces the old value, null clears limits and
// pinned_tags, and a field left out stays as it is.
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
		Name       *string         `json:"name"`
		Limits     json.RawMessage `json:"limits"`
		PinnedTags json.RawMessage `json:"pinned_tags"`
	}
	if !decode(w, r, &in) {
		return
	}
	var name string
	if in.Name != nil {
		var msg string
		if name, msg = appTokenName(*in.Name); msg != "" {
			writeErr(w, 400, "invalid", msg)
			return
		}
	}
	var limits store.AppLimits
	var pinned map[string]string
	if len(in.Limits) > 0 && json.Unmarshal(in.Limits, &limits) != nil {
		writeErr(w, 400, "invalid", "limits must be an object or null")
		return
	}
	if len(in.PinnedTags) > 0 && json.Unmarshal(in.PinnedTags, &pinned) != nil {
		writeErr(w, 400, "invalid", "pinned_tags must be an object of strings or null")
		return
	}
	if in.Name == nil && len(in.Limits) == 0 && len(in.PinnedTags) == 0 {
		writeErr(w, 400, "invalid", "send name, limits or pinned_tags")
		return
	}
	if msg := tokenControlsError(limits, pinned); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	ctx := r.Context()
	old, err := s.Store.GetAppToken(ctx, a.ID, tid)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var changes []string
	if in.Name != nil {
		if err := s.Store.RenameAppToken(ctx, a.ID, tid, name); err != nil {
			writeStoreErr(w, err)
			return
		}
		changes = append(changes, "token "+old.Name+" renamed to "+name)
	}
	if len(in.Limits) > 0 {
		if err := s.Store.SetAppTokenLimits(ctx, a.ID, tid, limits); err != nil {
			writeStoreErr(w, err)
			return
		}
		changes = append(changes, "token "+old.Name+" limits: "+limitsText(limits))
	}
	if len(in.PinnedTags) > 0 {
		if err := s.Store.SetAppTokenPinnedTags(ctx, a.ID, tid, pinned); err != nil {
			writeStoreErr(w, err)
			return
		}
		desc := "none"
		if len(pinned) > 0 {
			desc = store.FormatTags(pinned)
		}
		changes = append(changes, "token "+old.Name+" pinned tags: "+desc)
	}
	t, err := s.Store.GetAppToken(ctx, a.ID, tid)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	action := "app.token_update"
	if in.Name != nil && len(changes) == 1 {
		action = "app.token_rename"
	}
	s.Store.AddAudit(ctx, userFrom(r), action, a.Name, strings.Join(changes, "; "))
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
