package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Sharing between servers, the public side (server to server). The
// well-known identity is open; preview and redeem need an invite secret and
// are rate limited globally, like the CLI device routes; manifest and leave
// need the share token. All are open in the access gate.

// Global limits on invite guesses: an invite secret is 32 random bytes, so
// these only bound load.
const (
	peerInviteLimit = 20 // preview and redeem calls a minute
	peerInviteDaily = 500
)

type peerLimiter struct {
	mu sync.Mutex
	l  windowLimit
}

func newPeerLimiter() *peerLimiter {
	return &peerLimiter{l: windowLimit{perMin: peerInviteLimit, perDay: peerInviteDaily}}
}

func (p *peerLimiter) take() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.l.take(time.Now())
}

// publicURL is how this server names itself to others: ACNOLOGIA_PUBLIC_URL,
// else the URL a superadmin saved, else the request origin.
func (s *Server) publicURL(r *http.Request) string {
	if s.Cfg.PublicURL != "" {
		return strings.TrimRight(s.Cfg.PublicURL, "/")
	}
	if u := peer.SavedURL(r.Context(), s.Store); u != "" {
		return u
	}
	return s.baseURL(r)
}

func (s *Server) selfIdentity(r *http.Request) (peer.Identity, []byte, error) {
	id, priv, err := peer.Self(r.Context(), s.Store, s.Pool.Box, s.Cfg.Version, s.publicURL(r))
	return id, priv, err
}

func (s *Server) wellKnown(w http.ResponseWriter, r *http.Request) {
	id, _, err := s.selfIdentity(r)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, id)
}

// shareGate refuses a share token whose share is not active. Expiry is
// checked here, on every request.
func (s *Server) shareGate(ctx context.Context, shareID string) *authFail {
	sh, err := s.Store.GetShare(ctx, shareID)
	if err != nil {
		return &authFail{Status: 401, Code: "unauthenticated", Message: "invalid app token"}
	}
	if sh.Status == store.ShareActive && sh.Expired(time.Now()) {
		if n, err := s.Store.SetShareStatus(ctx, sh.ID, store.ShareExpired); err == nil {
			sh = n
		}
	}
	switch sh.Status {
	case store.ShareActive:
		return nil
	case store.ShareRevoked:
		return &authFail{Status: 403, Code: "share_revoked", Message: "the owner revoked this share", Share: "revoked"}
	case store.SharePaused:
		return &authFail{Status: 403, Code: "share_paused", Message: "the owner paused this share", Share: "paused"}
	case store.ShareExpired:
		return &authFail{Status: 403, Code: "share_expired", Message: "this share expired", Share: "expired"}
	}
	return &authFail{Status: 401, Code: "unauthenticated", Message: "invalid app token"}
}

// inviteShare finds the pending share an invite secret opens, writing the
// error when there is none. It never burns the invite.
func (s *Server) inviteShare(w http.ResponseWriter, r *http.Request, shareID, secret string) (*store.Share, bool) {
	if !s.peerLimit.take() {
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "rate_limited", "too many invite attempts, try again in a minute")
		return nil, false
	}
	sh, err := s.Store.GetShare(r.Context(), shareID)
	if err != nil || secret == "" {
		writeErr(w, 404, "invite_invalid", "that invite is not valid")
		return nil, false
	}
	if sh.Status != store.SharePending || !sh.HasInvite() {
		writeErr(w, 409, "invite_used", "that invite was already used")
		return nil, false
	}
	if !s.Store.ShareInviteMatches(r.Context(), sh.ID, peer.InviteHash(secret)) {
		writeErr(w, 404, "invite_invalid", "that invite is not valid")
		return nil, false
	}
	if sh.InviteExpires != nil && !sh.InviteExpires.After(time.Now()) {
		writeErr(w, 410, "invite_expired", "that invite expired; ask for a new one")
		return nil, false
	}
	return sh, true
}

func (s *Server) peerPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in peer.PreviewRequest
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if !decode(w, r, &in) {
		return
	}
	sh, ok := s.inviteShare(w, r, in.ShareID, in.Invite)
	if !ok {
		return
	}
	m := s.buildManifest(r.Context(), sh)
	var exp *string
	if sh.InviteExpires != nil {
		v := sh.InviteExpires.Format(time.RFC3339)
		exp = &v
	}
	writeJSON(w, 200, peer.PreviewResponse{Name: sh.Name, Services: m.Services, Providers: sh.Providers, ExpiresAt: exp})
}

func (s *Server) peerRedeem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in peer.RedeemRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if !decode(w, r, &in) {
		return
	}
	if _, ok := s.inviteShare(w, r, in.ShareID, in.Invite); !ok {
		return
	}
	pub, err := peer.ParsePublicKey(in.PublicKey)
	if err != nil || peer.ServerID(pub) != in.ID {
		writeErr(w, 400, "bad_identity", "the server id does not match its public key")
		return
	}
	if len(in.Nonce) < 16 || len(in.Nonce) > 128 || !peer.VerifyRedeem(pub, in.Signature, in.ShareID, peer.InviteHash(in.Invite), in.Nonce, in.ID) {
		writeErr(w, 401, "bad_signature", "the redeem request is not signed by that server")
		return
	}
	self, _, err := s.selfIdentity(r)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if in.ID == self.ID {
		writeErr(w, 400, "self_redeem", "a server cannot redeem its own invite")
		return
	}
	name := strings.TrimSpace(crypto.Truncate(in.Name, 100))
	url := strings.TrimRight(crypto.Truncate(in.URL, 500), "/")
	tok, prefix, hash := newAppToken()
	sh, err := s.Store.RedeemShare(r.Context(), in.ShareID, peer.InviteHash(in.Invite),
		store.PeerBinding{ID: in.ID, Name: name, URL: url, PublicKey: in.PublicKey}, prefix, hash)
	switch {
	case errors.Is(err, store.ErrInviteUsed):
		writeErr(w, 409, "invite_used", "that invite was already used")
		return
	case errors.Is(err, store.ErrInviteExpired):
		writeErr(w, 410, "invite_expired", "that invite expired; ask for a new one")
		return
	case errors.Is(err, store.ErrInviteInvalid):
		writeErr(w, 404, "invite_invalid", "that invite is not valid")
		return
	case err != nil:
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), nil, "share.redeemed", sh.Name, "by "+in.ID+" ("+name+")")
	s.emitShare(notify.ShareRedeemed, sh, "share "+sh.Name+" was redeemed by "+name)
	writeJSON(w, 200, peer.RedeemResponse{Token: tok, Owner: self, Manifest: s.buildManifest(r.Context(), sh)})
}

// shareFromApp loads the share behind the requesting app token.
func (s *Server) shareFromApp(w http.ResponseWriter, r *http.Request) (*store.Share, bool) {
	a := appFrom(r)
	if a == nil || a.ShareID == nil {
		writeErr(w, 403, "not_a_share", "this token is not a share token")
		return nil, false
	}
	sh, err := s.Store.GetShare(r.Context(), *a.ShareID)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return sh, true
}

func (s *Server) peerManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sh, ok := s.shareFromApp(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, s.buildManifest(r.Context(), sh))
}

func (s *Server) peerLeave(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFromApp(w, r)
	if !ok {
		return
	}
	sh, err := s.Store.MarkShareLeft(r.Context(), sh.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), nil, "share.revoked", sh.Name, "left by peer "+sh.PeerID)
	s.emitShare(notify.ShareRevoked, sh, "share "+sh.Name+" was left by "+sh.PeerName)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// buildManifest describes a share: services with availability and provider
// specs. It never carries key data.
func (s *Server) buildManifest(ctx context.Context, sh *store.Share) peer.Manifest {
	m := peer.Manifest{ShareID: sh.ID, Name: sh.Name, Status: sh.Status, Limits: sh.Limits,
		Services: []peer.ManifestService{}, Providers: []peer.ManifestProvider{}}
	if sh.ExpiresAt != nil {
		v := sh.ExpiresAt.Format(time.RFC3339)
		m.ExpiresAt = &v
	}
	svcs, err := s.Store.ListServices(ctx)
	if err == nil {
		err = s.Store.FillStats(ctx, svcs, sh.IncludeHidden, s.Pool.InFlight())
	}
	if err != nil {
		return m
	}
	want := map[string]bool{}
	for _, slug := range sh.Services {
		want[slug] = true
	}
	provs := map[string]bool{}
	for _, sv := range svcs {
		if !want[sv.Slug] || sv.Provider == providers.PeerSlug {
			continue
		}
		m.Services = append(m.Services, peer.ManifestService{
			Slug: sv.Slug, Name: sv.Name, Provider: sv.Provider, Category: sv.Category, Description: sv.Description,
			Available: max(sv.KeyCounts.Available-sv.KeyCounts.Busy, 0),
		})
		if _, ok := provs[sv.Provider]; !ok {
			provs[sv.Provider] = false
		}
	}
	for _, p := range sh.Providers {
		provs[p] = true
	}
	slugs := make([]string, 0, len(provs))
	for slug := range provs {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		shared := provs[slug]
		if slug == providers.PeerSlug {
			continue
		}
		p, err := s.Store.GetProvider(ctx, slug)
		if err != nil {
			continue
		}
		m.Providers = append(m.Providers, peer.ManifestProvider{
			Slug: p.Slug, Name: p.Name, Category: p.Category, Description: p.Description, Website: p.Website,
			DocsURL: p.DocsURL, Color: p.Color, Version: p.Version, Shared: shared, Spec: p.Spec,
		})
	}
	return m
}

func (s *Server) emitShare(typ string, sh *store.Share, msg string) {
	if s.Pool.Notify == nil {
		return
	}
	var appID int64
	if sh.AppID != nil {
		appID = *sh.AppID
	}
	s.Pool.Notify.Emit(notify.Event{Type: typ, AppID: appID, App: sh.Name, Message: msg,
		Data: map[string]any{"share_id": sh.ID, "peer_id": sh.PeerID, "status": sh.Status}})
}
