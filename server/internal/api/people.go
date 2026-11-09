package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Sharing with people who do not run Acnologia. The owner creates a share
// whose recipient is a person and gets a one time claim link
// (<public url>/claim/acnclaim_...). The person opens it; the SPA calls the
// public claim routes below, which are open in the access gate and rate
// limited like the peer invite routes. Redeeming burns the link and returns,
// exactly once, either a personal app token (proxy mode: keys never leave
// this server, pause and revoke refuse the next call) or the scoped keys
// themselves (reveal mode: they cannot be taken back, so ending the share
// produces a rotate checklist). Only the claim code's hash is stored.

// errNoRevealable refuses a reveal share whose scope shows no key.
const errNoRevealable = "nothing to reveal: the key scopes resolve to no key that can be shown (hidden, restricted, disabled, invalid and handed over keys are never revealed)"

// errRevealLimits explains why a reveal share takes no limits.
const errRevealLimits = "request and budget limits cannot apply to a reveal share: the person calls the provider with the keys directly, so this server never sees the requests. Use proxy mode for limits"

// shareInputCode is the error code for a share input message.
func shareInputCode(msg string) string {
	if msg == errNoRevealable {
		return "no_revealable_keys"
	}
	return "invalid"
}

// applyRecipientInput validates recipient, label and email onto sh.
func applyRecipientInput(in shareInput, sh *store.Share) string {
	if in.Reusable != nil {
		if sh.Status != "" && *in.Reusable != sh.Reusable {
			return "whether a link is reusable cannot change; create a new share instead"
		}
		sh.Reusable = *in.Reusable
	}
	if sh.Reusable && in.Recipient == nil && sh.Recipient == "" {
		// A reusable link is a share with people; saying so is optional.
		r := store.RecipientPerson
		in.Recipient = &r
	}
	if in.Recipient != nil {
		rc := strings.TrimSpace(*in.Recipient)
		if rc == "" {
			rc = store.RecipientServer
		}
		if rc != store.RecipientServer && rc != store.RecipientPerson {
			return "recipient must be server or person"
		}
		if sh.Status != "" && rc != sh.Recipient {
			return "the recipient of a share cannot change; create a new share instead"
		}
		sh.Recipient = rc
	}
	if sh.Recipient == "" {
		sh.Recipient = store.RecipientServer
	}
	if in.Label != nil {
		sh.Label = strings.TrimSpace(*in.Label)
	}
	if in.Email != nil {
		e := strings.TrimSpace(*in.Email)
		if e != "" && !looseEmail(e) {
			return "email must look like an email address (it is only a label; nothing is ever sent to it)"
		}
		sh.Email = e
	}
	if in.MaxClaims != nil {
		if *in.MaxClaims < 0 || *in.MaxClaims > 100000 {
			return "max_claims must be between 0 (no cap) and 100000"
		}
		sh.MaxClaims = *in.MaxClaims
	}
	if !sh.IsPerson() {
		if sh.Label != "" || sh.Email != "" {
			return "label and email are for a share with a person"
		}
		if sh.Reusable {
			return "a reusable link is for people; a share with another server uses an invite"
		}
		return ""
	}
	if !sh.Reusable && sh.MaxClaims != 0 {
		return "max_claims is for a reusable link; a one time link is claimed once"
	}
	if sh.Reusable {
		if sh.Email != "" {
			return "email is for a share with one person; a reusable link is for anyone who has it"
		}
		if sh.Label == "" {
			sh.Label = "Anyone with the link"
		}
	}
	if sh.Label == "" || len(sh.Label) > 80 {
		return "label must be 1 to 80 characters: who the share is for"
	}
	return ""
}

// looseEmail accepts anything shaped like a@b.c: the email is a label.
func looseEmail(e string) bool {
	if len(e) > 200 || strings.ContainsAny(e, " \t\r\n<>\",;") {
		return false
	}
	local, domain, ok := strings.Cut(e, "@")
	return ok && local != "" && strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".") && !strings.Contains(domain, "@")
}

// newClaim makes a claim code, its hash and its end. A reusable link may
// also last 30 days, and defaults to 7.
func newClaim(valid string, reusable bool) (code, hash string, exp time.Time, msg string) {
	table, def, choices := inviteValidity, 24*time.Hour, "1h, 24h or 7d"
	if reusable {
		table, def, choices = reusableLinkValidity, 7*24*time.Hour, "1h, 24h, 7d or 30d"
	}
	d, ok := table[valid]
	if valid == "" {
		d, ok = def, true
	}
	if !ok {
		return "", "", time.Time{}, "valid must be " + choices
	}
	code = crypto.RandomToken(store.ClaimPrefix, 24)
	return code, crypto.SHA256(code), time.Now().Add(d).UTC(), ""
}

// claimURL is the page a person opens to claim a share.
func (s *Server) claimURL(r *http.Request, code string) string {
	return s.publicURL(r) + "/claim/" + code
}

// createPersonShare finishes createShare for a person: a claim link
// instead of an invite.
func (s *Server) createPersonShare(w http.ResponseWriter, r *http.Request, in shareInput, sh *store.Share) {
	valid := in.ClaimValid
	if valid == "" {
		valid = in.InviteValid
	}
	code, hash, exp, msg := newClaim(valid, sh.Reusable)
	if msg != "" {
		writeErr(w, 400, "invalid", "claim_"+msg)
		return
	}
	sh.ClaimExpires = &exp
	created, err := s.Store.CreateShare(r.Context(), sh, hash, userID(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	who := "for " + created.Label + " (" + created.Mode + ")"
	if created.Reusable {
		who = "as a link anyone can claim" + maxClaimsNote(created)
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "share.created", created.Name, who+"; "+shareSummary(created))
	s.emitShare(notify.ShareCreated, created, "share "+created.Name+" with "+created.Label+" was created")
	writeJSON(w, 201, map[string]any{"share": s.newShareView(r, created, false), "claim_url": s.claimURL(r, code)})
}

// maxClaimsNote is " (up to N claims)" for a capped reusable link.
func maxClaimsNote(sh *store.Share) string {
	if sh.MaxClaims > 0 {
		return fmt.Sprintf(" (up to %d claims)", sh.MaxClaims)
	}
	return ""
}

// shareClaimLink issues a new claim link for an unclaimed person share, or
// for a live reusable one. The old link stops working; tokens already
// claimed through it keep working.
func (s *Server) shareClaimLink(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.loadShare(w, r)
	if !ok {
		return
	}
	var in struct {
		Valid string `json:"valid"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if !sh.IsPerson() {
		writeErr(w, 409, "not_person_share", "this share is for another server; issue a new invite instead")
		return
	}
	if sh.Reusable && sh.Status != store.SharePending && sh.Status != store.ShareActive {
		writeErr(w, 409, "share_ended", "this share is paused or has ended; resume it before issuing a new link")
		return
	}
	if !sh.Reusable && (sh.ClaimedAt != nil || sh.Status != store.SharePending) {
		writeErr(w, 409, "already_claimed", "this share was already claimed or has ended")
		return
	}
	code, hash, exp, msg := newClaim(in.Valid, sh.Reusable)
	if msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if err := s.Store.SetShareClaim(r.Context(), sh.ID, hash, exp); err != nil {
		if errors.Is(err, store.ErrClaimUsed) {
			writeErr(w, 409, "already_claimed", "this share was already claimed or has ended")
			return
		}
		writeStoreErr(w, err)
		return
	}
	sh, _ = s.Store.GetShare(r.Context(), sh.ID)
	s.Store.AddAudit(r.Context(), userFrom(r), "share.claim_link", sh.Name, "new claim link for "+sh.Label+", valid until "+exp.Format(time.RFC3339))
	s.emitShareData(notify.ShareClaimLink, sh, "a new claim link was issued for share "+sh.Name, map[string]any{"claim_expires_at": exp})
	writeJSON(w, 200, map[string]any{"share": s.newShareView(r, sh, false), "claim_url": s.claimURL(r, code), "claim_expires_at": exp})
}

// ---- reveal ----

type revealable struct {
	key *store.Key
	svc *store.Service
}

// revealableKeys are the keys a reveal share shows: in its key scope, in
// rotation, and never hidden (whatever include_hidden says), restricted to
// other apps, handed over by another server, or of a peer service.
func (s *Server) revealableKeys(ctx context.Context, sh *store.Share) []revealable {
	var appID int64
	if sh.AppID != nil {
		appID = *sh.AppID
	}
	out := []revealable{}
	for _, slug := range sh.Services {
		sv, err := s.Store.GetServiceBySlug(ctx, slug)
		if err != nil || sv.Provider == providers.PeerSlug || sv.PeerLinkID != nil {
			continue
		}
		var sc *store.KeyScope
		if v, ok := sh.KeyScopes[slug]; ok {
			sc = &v
		}
		keys, err := s.Store.ListKeys(ctx, sv.ID, false)
		if err != nil {
			continue
		}
		res := store.ResolveScope(sc, keys, appID, false)
		for _, k := range keys {
			if !res.IDs[k.ID] || k.Hidden || !k.InRotation() || k.IsHandedOver() {
				continue
			}
			out = append(out, revealable{key: k, svc: sv})
		}
	}
	return out
}

// revealedKeyView is one key a reveal share showed. Never a secret.
type revealedKeyView struct {
	KeyID      int64     `json:"key_id"`
	Service    string    `json:"service"`
	Label      string    `json:"label"`
	Provider   string    `json:"provider"`
	RevealedAt time.Time `json:"revealed_at"`
}

// fillRevealView adds the revealed keys, and on an ended reveal share the
// rotate checklist, to a share view.
func (s *Server) fillRevealView(ctx context.Context, v *shareView) {
	v.RevealedKeys = []revealedKeyView{}
	sh := v.Share
	if !sh.IsReveal() {
		return
	}
	rows, _ := s.Store.ListReveals(ctx, sh.ID)
	for _, rv := range rows {
		v.RevealedKeys = append(v.RevealedKeys, revealedKeyView{KeyID: rv.KeyID, Service: rv.Service, Label: rv.Label, Provider: rv.Provider, RevealedAt: rv.RevealedAt})
	}
	if (sh.Status != store.ShareRevoked && sh.Status != store.ShareExpired) || len(rows) == 0 {
		return
	}
	v.RotateChecklist = []rotateItem{}
	for _, rv := range rows {
		it := rotateItem{KeyID: rv.KeyID, Service: rv.Service, Label: rv.Label, Provider: rv.Provider, HandedAt: rv.RevealedAt}
		if p, err := s.Store.GetProvider(ctx, rv.Provider); err == nil {
			it.ProviderName, it.ProviderURL = p.Name, p.Website
		}
		if _, err := s.Store.GetKey(ctx, rv.KeyID); err == nil {
			it.KeyExists = true
		}
		v.RotateChecklist = append(v.RotateChecklist, it)
	}
}

// revealEnded audits the keys to rotate when a claimed reveal share ends:
// the person still has them, whatever this server does.
func (s *Server) revealEnded(ctx context.Context, sh *store.Share, why string) {
	if sh == nil || !sh.IsReveal() || sh.ClaimedAt == nil {
		return
	}
	rows, err := s.Store.ListReveals(ctx, sh.ID)
	if err != nil || len(rows) == 0 {
		return
	}
	names := make([]string, 0, len(rows))
	for _, rv := range rows {
		names = append(names, fmt.Sprintf("%s/%s (key %d)", rv.Service, rv.Label, rv.KeyID))
	}
	s.Store.AddAudit(ctx, nil, "share.rotate_keys", sh.Name, fmt.Sprintf("share with %s %s; %d revealed key(s) to rotate at the provider: %s",
		sh.Label, why, len(rows), strings.Join(names, ", ")))
}

// ---- public claim routes ----

// claimShare finds the share a claim code opens, after the rate limit,
// writing the error when there is none. It never burns the link. Only a
// code that opens nothing counts against the limit (secretLimit), so junk
// cannot spend the budget a real link needs; malformed input costs no
// lookup and is not counted either.
func (s *Server) claimShare(w http.ResponseWriter, r *http.Request) (*store.Share, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.claimLimit.allow(r) {
		writeRateLimited(w, "claim")
		return nil, "", false
	}
	var in struct {
		Code string `json:"code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !decode(w, r, &in) {
		return nil, "", false
	}
	code := strings.TrimSpace(in.Code)
	// The page may pass the whole link.
	if i := strings.LastIndex(code, "/claim/"); i >= 0 {
		code = code[i+len("/claim/"):]
	}
	if !strings.HasPrefix(code, store.ClaimPrefix) || len(code) > 200 {
		writeClaimErr(w, store.ErrClaimInvalid)
		return nil, "", false
	}
	hash := crypto.SHA256(code)
	sh, err := s.Store.ShareByClaim(r.Context(), hash)
	if err == nil {
		err = sh.CheckClaim(time.Now())
	}
	if err != nil {
		if !s.claimLimit.miss(r) {
			writeRateLimited(w, "claim")
		} else {
			writeClaimErr(w, err)
		}
		return nil, "", false
	}
	return sh, hash, true
}

func writeClaimErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrClaimInvalid):
		writeErr(w, 404, "claim_invalid", "that claim link is not valid")
	case errors.Is(err, store.ErrClaimUsed):
		writeErr(w, 409, "already_claimed", "that claim link was already used; it works once")
	case errors.Is(err, store.ErrClaimFull):
		writeErr(w, 410, "claim_full", "that link was claimed as many times as the owner allowed; ask the owner for a new link")
	case errors.Is(err, store.ErrClaimExpired):
		writeErr(w, 410, "claim_expired", "that claim link expired or the share ended; ask the owner for a new link")
	default:
		writeStoreErr(w, err)
	}
}

type claimService struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Category    string `json:"category"`
	Description string `json:"description"`
	// LogoURL is the logo inlined as a data: URI (see logoDataURI), since
	// the logo routes need a dashboard session; null shows initials.
	LogoURL *string `json:"logo_url"`
}

type claimProvider struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func (s *Server) claimServices(ctx context.Context, sh *store.Share) ([]claimService, []*store.Service) {
	out, svcs := []claimService{}, []*store.Service{}
	for _, slug := range sh.Services {
		sv, err := s.Store.GetServiceBySlug(ctx, slug)
		if err != nil || sv.Provider == providers.PeerSlug {
			continue
		}
		out = append(out, claimService{Slug: sv.Slug, Name: sv.Name, Provider: sv.Provider, Category: sv.Category, Description: sv.Description,
			LogoURL: s.logoDataURI(ctx, sv)})
		svcs = append(svcs, sv)
	}
	return out, svcs
}

// claimPreview answers POST /api/claim/v1/preview: what is shared, by whom,
// until when. It never burns the link and carries no secret.
func (s *Server) claimPreview(w http.ResponseWriter, r *http.Request) {
	sh, _, ok := s.claimShare(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	self, _, _ := s.selfIdentity(r)
	svcs, _ := s.claimServices(ctx, sh)
	provs := []claimProvider{}
	for _, slug := range sh.Providers {
		if p, err := s.Store.GetProvider(ctx, slug); err == nil {
			provs = append(provs, claimProvider{Slug: p.Slug, Name: p.Name})
		}
	}
	keyCount := 0
	if sh.IsReveal() {
		keyCount = len(s.revealableKeys(ctx, sh))
	}
	writeJSON(w, 200, map[string]any{
		"owner": map[string]any{"name": self.Name, "id": self.ID, "url": self.URL},
		"share": map[string]any{"name": sh.Name, "label": sh.Label, "mode": sh.Mode, "services": svcs, "providers": provs,
			"ends_at": sh.ExpiresAt, "limits": sh.Limits, "key_count": keyCount, "reusable": sh.Reusable},
		"claim_expires_at": sh.ClaimExpires,
	})
}

type claimExample struct {
	Name   string            `json:"name"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   any               `json:"body,omitempty"`
}

type claimTokenService struct {
	Slug     string         `json:"slug"`
	Name     string         `json:"name"`
	Provider string         `json:"provider"`
	Category string         `json:"category"`
	ProxyURL string         `json:"proxy_url"`
	Auth     authDTO        `json:"auth"`
	Examples []claimExample `json:"examples"`
	// Guide is the service's usage guide as Markdown, the same one an app
	// reads from /api/v1/services/{slug}/guide, so the person can keep it
	// or hand it to an AI agent.
	Guide string `json:"guide"`
}

type claimRevealedKey struct {
	Service  string  `json:"service"`
	Label    string  `json:"label"`
	Secret   string  `json:"secret"`
	Provider string  `json:"provider"`
	BaseURL  string  `json:"base_url"`
	Auth     authDTO `json:"auth"`
}

// claimRedeem answers POST /api/claim/v1/redeem: it burns the link and
// returns the token, or for reveal the keys, exactly once.
func (s *Server) claimRedeem(w http.ResponseWriter, r *http.Request) {
	sh, hash, ok := s.claimShare(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if sh.IsReveal() {
		s.claimReveal(w, r, sh, hash)
		return
	}
	tok, prefix, tokHash := newAppToken()
	claimed, err := s.Store.RedeemClaim(ctx, hash, store.ClaimRedeem{TokenPrefix: prefix, TokenHash: tokHash})
	if err != nil {
		writeClaimErr(w, err)
		return
	}
	if claimed.Reusable {
		n := fmt.Sprintf("claim %d", claimed.ClaimCount)
		if claimed.MaxClaims > 0 {
			n += fmt.Sprintf(" of %d", claimed.MaxClaims)
		}
		s.Store.AddAudit(ctx, nil, "share.claimed", claimed.Name, "claimed through its link ("+n+", proxy) from "+clientIP(r))
		s.emitShare(notify.ShareClaimed, claimed, "share "+claimed.Name+" was claimed through its link ("+n+")")
	} else {
		s.Store.AddAudit(ctx, nil, "share.claimed", claimed.Name, "claimed by "+claimed.Label+" (proxy)")
		s.emitShare(notify.ShareClaimed, claimed, "share "+claimed.Name+" was claimed by "+claimed.Label)
	}
	base := s.publicURL(r)
	_, svcs := s.claimServices(ctx, claimed)
	out := make([]claimTokenService, 0, len(svcs))
	for _, sv := range svcs {
		exs := []claimExample{}
		for _, ex := range docSpec(sv).Examples {
			exs = append(exs, claimExample{Name: ex.Name, Method: orDefault(ex.Method, "GET"), Path: ex.Path, Query: ex.Query, Body: ex.Body})
		}
		out = append(out, claimTokenService{Slug: sv.Slug, Name: sv.Name, Provider: sv.Provider, Category: sv.Category,
			ProxyURL: base + "/proxy/" + sv.Slug, Auth: authDTO{Type: sv.AuthType, Name: sv.AuthName, Prefix: authPrefix(sv)}, Examples: exs,
			Guide: s.serviceGuide(ctx, sv, base, false).Render(false)})
	}
	writeJSON(w, 200, map[string]any{"token": tok, "base_url": base + "/proxy", "services": out, "mcp_url": base + "/mcp",
		"forward_proxy_url": s.forwardProxyURL(authorityOf(base)), "ca_url": base + "/api/v1/proxy/ca.pem",
		"limits": claimed.Limits, "ends_at": claimed.ExpiresAt})
}

// claimReveal decrypts the scoped keys, burns the link while recording each
// reveal, and only then returns the secrets.
func (s *Server) claimReveal(w http.ResponseWriter, r *http.Request, sh *store.Share, hash string) {
	ctx := r.Context()
	keys := []claimRevealedKey{}
	recs := []store.RevealRecord{}
	for _, it := range s.revealableKeys(ctx, sh) {
		secret, err := s.Pool.Box.Decrypt(it.key.SecretEnc)
		if err != nil {
			continue
		}
		keys = append(keys, claimRevealedKey{Service: it.svc.Slug, Label: it.key.Label, Secret: secret, Provider: it.svc.Provider,
			BaseURL: it.svc.BaseURL, Auth: authDTO{Type: it.svc.AuthType, Name: it.svc.AuthName, Prefix: authPrefix(it.svc)}})
		recs = append(recs, store.RevealRecord{KeyID: it.key.ID, Service: it.svc.Slug, Provider: it.svc.Provider, Label: it.key.Label})
	}
	if len(keys) == 0 {
		writeErr(w, 409, "nothing_to_reveal", "no key is in this share's scope right now; ask the owner")
		return
	}
	claimed, err := s.Store.RedeemClaim(ctx, hash, store.ClaimRedeem{Reveals: recs})
	if err != nil {
		writeClaimErr(w, err)
		return
	}
	s.Store.AddAudit(ctx, nil, "share.claimed", claimed.Name, fmt.Sprintf("claimed by %s (reveal, %d key(s))", claimed.Label, len(recs)))
	s.emitShare(notify.ShareClaimed, claimed, "share "+claimed.Name+" was claimed by "+claimed.Label)
	for _, rc := range recs {
		s.Store.AddAudit(ctx, nil, "share.revealed", claimed.Name, fmt.Sprintf("key %d (%s) revealed to %s", rc.KeyID, rc.Service, claimed.Label))
		s.emitShareData(notify.ShareRevealed, claimed, fmt.Sprintf("key %d of share %s was revealed to %s", rc.KeyID, claimed.Name, claimed.Label),
			map[string]any{"key_id": rc.KeyID, "service": rc.Service})
	}
	writeJSON(w, 200, map[string]any{"keys": keys, "ends_at": claimed.ExpiresAt})
}

// ---- GET /api/v1/me ----

type meService struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Category string `json:"category"`
	// Available counts keys that could serve a request now. For an app that
	// backs no share it is 1 or 0: ordinary apps never learn pool sizes.
	Available int `json:"available"`
	// LogoURL is the logo inlined as a data: URI, or null.
	LogoURL *string `json:"logo_url"`
}

type meUsage struct {
	TodayRequests int64           `json:"today_requests"`
	TodayCost     float64         `json:"today_cost"`
	MonthRequests int64           `json:"month_requests"`
	MonthCost     float64         `json:"month_cost"`
	LastUsedAt    *time.Time      `json:"last_used_at"`
	LimitHit      *store.LimitHit `json:"limit_hit"`
}

type meOwner struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type meShare struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	OwnerName string     `json:"owner_name"`
	OwnerID   string     `json:"owner_id"`
	Recipient string     `json:"recipient"`
	Label     string     `json:"label"`
	Status    string     `json:"status"`
	Mode      string     `json:"mode"`
	EndsAt    *time.Time `json:"ends_at"`
	Owner     meOwner    `json:"owner"`
}

// v1Me tells an app token holder about its own access: limits and use,
// the services it may call, and for a share, the share's state. A share
// token whose share is paused, expired or revoked still learns that state
// here (and nothing it could use).
func (s *Server) v1Me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	a, f := s.authAppFull(r)
	if f != nil {
		if f.Share == "" {
			f.write(w)
			return
		}
		a2, _, err := s.Store.AppByToken(ctx, crypto.SHA256(appToken(r)))
		if err != nil || a2.ShareID == nil {
			f.write(w)
			return
		}
		a = a2
	}
	out := map[string]any{"app": map[string]any{"id": a.ID, "name": a.Name}, "services": []meService{}, "share": nil}
	limits, _ := s.Store.GetAppLimits(ctx, a.ID)
	out["limits"] = limits
	usage := meUsage{}
	if u, err := s.Store.GetAppUsage(ctx, a.ID, time.Now()); err == nil {
		usage = meUsage{TodayRequests: u.TodayRequests, TodayCost: u.TodayCost, MonthRequests: u.MonthRequests, MonthCost: u.MonthCost,
			LimitHit: store.CheckLimits(limits, u, time.Now())}
	}
	if fresh, err := s.Store.GetApp(ctx, a.ID); err == nil {
		usage.LastUsedAt = fresh.LastUsedAt
	}
	out["usage"] = usage
	out["limit_hit"] = usage.LimitHit
	if a.ShareID != nil {
		sh, err := s.Store.GetShare(ctx, *a.ShareID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		self, _, _ := s.selfIdentity(r)
		out["app"] = map[string]any{"id": a.ID, "name": sh.Name}
		out["share"] = meShare{ID: sh.ID, Name: sh.Name, OwnerName: self.Name, OwnerID: self.ID, Recipient: sh.Recipient, Label: sh.Label,
			Status: sh.Status, Mode: sh.Mode, EndsAt: sh.ExpiresAt, Owner: meOwner{Name: self.Name, ID: self.ID}}
		if f == nil {
			svcs := []meService{}
			for _, ms := range s.buildManifest(ctx, sh).Services {
				var logo *string
				if sv, err := s.Store.GetServiceBySlug(ctx, ms.Slug); err == nil {
					logo = s.logoDataURI(ctx, sv)
				}
				svcs = append(svcs, meService{Slug: ms.Slug, Name: ms.Name, Provider: ms.Provider, Category: ms.Category, Available: ms.Available, LogoURL: logo})
			}
			out["services"] = svcs
		}
		writeJSON(w, 200, out)
		return
	}
	svcs, err := s.Store.ListServices(ctx)
	if err == nil {
		err = s.Store.FillStats(ctx, svcs, a.LeaseHidden, s.Pool.InFlight())
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	list := []meService{}
	for _, sv := range svcs {
		if !a.CanUse(sv.Slug) {
			continue
		}
		avail := 0
		if sv.KeyCounts.Available > sv.KeyCounts.Busy {
			avail = 1
		}
		list = append(list, meService{Slug: sv.Slug, Name: sv.Name, Provider: sv.Provider, Category: sv.Category, Available: avail,
			LogoURL: s.logoDataURI(ctx, sv)})
	}
	out["services"] = list
	writeJSON(w, 200, out)
}

// maxInlineLogo caps a logo inlined into a claim or My access answer. The
// seeded provider logos are a few kilobytes; a larger upload shows initials.
const maxInlineLogo = 48 << 10

// inlineLogoTypes are the image types inlined. They render in an <img> only,
// where an SVG cannot run script.
var inlineLogoTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
	"image/svg+xml": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
}

// logoDataURI returns the logo a person should see for a service as a data:
// URI: the service's own logo, else its provider's (for a peer service, the
// owner's provider first). People hold a claim link or a share token, not a
// dashboard session, so they cannot load the /api/logos routes.
func (s *Server) logoDataURI(ctx context.Context, sv *store.Service) *string {
	try := func(data []byte, mime string, err error) *string {
		mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
		if err != nil || len(data) == 0 || len(data) > maxInlineLogo || !inlineLogoTypes[mime] {
			return nil
		}
		u := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return &u
	}
	if u := try(s.Store.ServiceLogo(ctx, sv.ID)); u != nil {
		return u
	}
	for _, slug := range []string{sv.DocProvider(), sv.Provider} {
		if u := try(s.Store.ProviderLogo(ctx, slug)); u != nil {
			return u
		}
	}
	return nil
}
