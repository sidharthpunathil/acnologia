package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Sharing between servers, the dashboard side. Admins may look; only a
// superadmin creates, edits, pauses, resumes, revokes, connects or
// disconnects (the routes sit in the superadmin group).

// ---- identity ----

func (s *Server) identityDTO(r *http.Request) (map[string]any, error) {
	id, _, err := s.selfIdentity(r)
	if err != nil {
		return nil, err
	}
	src := "request"
	switch {
	case s.Cfg.PublicURL != "":
		src = "env"
	case peer.SavedURL(r.Context(), s.Store) != "":
		src = "saved"
	}
	return map[string]any{"id": id.ID, "name": id.Name, "url": id.URL, "url_source": src, "version": id.Version,
		"public_key": id.PublicKey, "fingerprint": id.Fingerprint()}, nil
}

func (s *Server) getIdentity(w http.ResponseWriter, r *http.Request) {
	out, err := s.identityDTO(r)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) patchIdentity(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name *string `json:"name"`
		URL  *string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 80 {
			writeErr(w, 400, "invalid", "name must be 1 to 80 characters")
			return
		}
		if err := peer.SetName(r.Context(), s.Store, n); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	if in.URL != nil {
		u := strings.TrimRight(strings.TrimSpace(*in.URL), "/")
		if u != "" && !httpURLOK(u) {
			writeErr(w, 400, "invalid", "url must be an http(s) URL")
			return
		}
		if err := peer.SetURL(r.Context(), s.Store, u); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "identity.update", "", "")
	s.getIdentity(w, r)
}

func httpURLOK(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// ---- shares (owner side) ----

type shareInput struct {
	Name          *string          `json:"name"`
	Note          *string          `json:"note"`
	Services      *[]string        `json:"services"`
	Providers     *[]string        `json:"providers"`
	IncludeHidden *bool            `json:"include_hidden"`
	Limits        *store.AppLimits `json:"limits"`
	// KeyScopes replaces the key scopes, by service slug.
	KeyScopes *map[string]store.KeyScope `json:"key_scopes"`
	// ExpiresAt is RFC 3339, or "" to clear. EndsAt is the same timer under
	// its newer name, and wins when both are sent.
	ExpiresAt *string `json:"expires_at"`
	EndsAt    *string `json:"ends_at"`
	// EndAction is expire (default) or revoke: what the timer does.
	EndAction *string `json:"end_action"`
	// Mode is proxy (default) or handover; fixed once redeemed.
	Mode *string `json:"mode"`
	// Lease is the handover renewal window: 1h, 6h, 24h (default), 72h or 7d.
	Lease *string `json:"lease"`
	// PrivateNetwork lets the revoke push reach a receiver on a private address.
	PrivateNetwork *bool `json:"private_network"`
	// InviteValid is 1h, 24h or 7d (default 24h).
	InviteValid string `json:"invite_valid"`
	// Recipient is server (default) or person, fixed at creation.
	Recipient *string `json:"recipient"`
	// Label names a person; Email is an optional label, never mailed.
	Label *string `json:"label"`
	Email *string `json:"email"`
	// ClaimValid is a person's claim link validity: 1h, 24h (default) or
	// 7d. InviteValid is read when it is not set.
	ClaimValid string `json:"claim_valid"`
	// Reusable makes a person share an "anyone with the link" share: the
	// link works for everyone who has it, each claim a token of its own.
	// Fixed at creation; implies recipient person and mode proxy.
	Reusable *bool `json:"reusable"`
	// MaxClaims caps how many times a reusable link can be claimed; 0 or
	// unset is no cap.
	MaxClaims *int `json:"max_claims"`
}

var inviteValidity = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

// reusableLinkValidity adds a month for "anyone with the link" shares, which
// are meant to be posted somewhere and claimed over time.
var reusableLinkValidity = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

// apply validates the input onto sh, writing nothing. It returns a message
// for a 400 or "".
func (s *Server) applyShareInput(r *http.Request, in shareInput, sh *store.Share) string {
	if in.Name != nil {
		sh.Name = strings.TrimSpace(*in.Name)
	}
	if sh.Name == "" || len(sh.Name) > 80 {
		return "name must be 1 to 80 characters"
	}
	if in.Note != nil {
		sh.Note = crypto.Truncate(*in.Note, 500)
	}
	if in.Services != nil {
		seen := map[string]bool{}
		sh.Services = []string{}
		for _, slug := range *in.Services {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			sv, err := s.Store.GetServiceBySlug(r.Context(), slug)
			if err != nil {
				return "no service named " + slug
			}
			if sv.Provider == providers.PeerSlug || sv.PeerLinkID != nil {
				return "service " + slug + " comes from another server and cannot be shared onward"
			}
			sh.Services = append(sh.Services, slug)
		}
	}
	if in.Providers != nil {
		seen := map[string]bool{}
		sh.Providers = []string{}
		for _, slug := range *in.Providers {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			if slug == providers.PeerSlug {
				return "the Acnologia server provider cannot be shared"
			}
			if _, err := s.Store.GetProvider(r.Context(), slug); err != nil {
				return "no provider named " + slug
			}
			sh.Providers = append(sh.Providers, slug)
		}
	}
	if len(sh.Services) == 0 && len(sh.Providers) == 0 {
		return "share at least one service or provider"
	}
	if l := in.Limits; l != nil {
		if (l.DailyRequests != nil && *l.DailyRequests < 0) || (l.MonthlyRequests != nil && *l.MonthlyRequests < 0) ||
			(l.DailyBudget != nil && *l.DailyBudget < 0) || (l.MonthlyBudget != nil && *l.MonthlyBudget < 0) {
			return "limits must not be negative"
		}
		sh.Limits = *l
	}
	if msg := applyRecipientInput(in, sh); msg != "" {
		return msg
	}
	if msg := applyShareModeInput(in, sh); msg != "" {
		return msg
	}
	if sh.Reusable && sh.Mode != store.ShareModeProxy {
		return "a link anyone can open is proxy only: a forwarded link must never show raw keys"
	}
	ends, field := in.ExpiresAt, "expires_at"
	if in.EndsAt != nil {
		ends, field = in.EndsAt, "ends_at"
	}
	if ends != nil {
		if *ends == "" {
			sh.ExpiresAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, *ends)
			if err != nil {
				return field + " must be an RFC 3339 time"
			}
			t = t.UTC()
			sh.ExpiresAt = &t
		}
	}
	if in.IncludeHidden != nil {
		sh.IncludeHidden = *in.IncludeHidden
	}
	if in.KeyScopes != nil {
		scopes, msg := s.checkKeyScopes(r, *in.KeyScopes, sh)
		if msg != "" {
			return msg
		}
		sh.KeyScopes = scopes
	}
	// A service that is no longer shared loses its scope.
	shared := map[string]bool{}
	for _, slug := range sh.Services {
		shared[slug] = true
	}
	for slug := range sh.KeyScopes {
		if !shared[slug] {
			delete(sh.KeyScopes, slug)
		}
	}
	// A reveal share must have something to show, until it is claimed.
	if sh.IsReveal() && sh.ClaimedAt == nil && len(s.revealableKeys(r.Context(), sh)) == 0 {
		return errNoRevealable
	}
	return ""
}

// maxScopeCount bounds a count scope; no service has this many keys.
const maxScopeCount = 100000

// checkKeyScopes validates key scopes against the share's services. Pick
// mode keys must belong to their service, and hidden ones need
// include_hidden. It returns the cleaned scopes or a message for a 400.
func (s *Server) checkKeyScopes(r *http.Request, in map[string]store.KeyScope, sh *store.Share) (map[string]store.KeyScope, string) {
	shared := map[string]bool{}
	for _, slug := range sh.Services {
		shared[slug] = true
	}
	out := map[string]store.KeyScope{}
	for slug, sc := range in {
		if !shared[slug] {
			return nil, "key_scopes names " + slug + ", which this share does not share"
		}
		switch sc.Mode {
		case "", store.ScopeAll:
			continue
		case store.ScopeCount:
			if sc.Count < 1 || sc.Count > maxScopeCount {
				return nil, "the key count for " + slug + " must be at least 1"
			}
			out[slug] = store.KeyScope{Mode: store.ScopeCount, Count: sc.Count}
		case store.ScopePick:
			if len(sc.KeyIDs) == 0 {
				return nil, "pick at least one key for " + slug
			}
			sv, err := s.Store.GetServiceBySlug(r.Context(), slug)
			if err != nil {
				return nil, "no service named " + slug
			}
			seen := map[int64]bool{}
			ids := []int64{}
			for _, id := range sc.KeyIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				k, err := s.Store.GetKey(r.Context(), id)
				if err != nil || k.ServiceID != sv.ID {
					return nil, "key " + strconv.FormatInt(id, 10) + " is not a key of " + slug
				}
				if k.Hidden && sh.IsReveal() {
					return nil, "key " + strconv.FormatInt(id, 10) + " of " + slug + " is hidden; hidden keys are never revealed"
				}
				if k.Hidden && !sh.IncludeHidden {
					return nil, "key " + strconv.FormatInt(id, 10) + " of " + slug + " is hidden; turn on include_hidden to share it"
				}
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			out[slug] = store.KeyScope{Mode: store.ScopePick, KeyIDs: ids}
		default:
			return nil, "key scope mode must be all, count or pick"
		}
	}
	return out, ""
}

func (s *Server) newInvite(r *http.Request, sh *store.Share, valid string) (string, string, time.Time, string) {
	d, ok := inviteValidity[valid]
	if valid == "" {
		d, ok = 24*time.Hour, true
	}
	if !ok {
		return "", "", time.Time{}, "invite_valid must be 1h, 24h or 7d"
	}
	secret := peer.NewInviteSecret()
	code := peer.Invite{URL: s.publicURL(r), ShareID: sh.ID, Secret: secret}.Encode()
	return code, peer.InviteHash(secret), time.Now().Add(d).UTC(), ""
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	var in shareInput
	if !decode(w, r, &in) {
		return
	}
	sh := &store.Share{ID: crypto.RandomToken("shr_", 12)}
	if msg := s.applyShareInput(r, in, sh); msg != "" {
		writeErr(w, 400, shareInputCode(msg), msg)
		return
	}
	if sh.IsPerson() {
		s.createPersonShare(w, r, in, sh)
		return
	}
	code, hash, exp, msg := s.newInvite(r, sh, in.InviteValid)
	if msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	sh.InviteExpires = &exp
	created, err := s.Store.CreateShare(r.Context(), sh, hash, userID(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "share.created", created.Name, shareSummary(created))
	s.emitShare(notify.ShareCreated, created, "share "+created.Name+" was created")
	writeJSON(w, 201, map[string]any{"share": s.newShareView(r, created, false), "invite": code})
}

// shareSummary is the audit detail of a share: what it shares and the key
// scope modes. Never key data.
func shareSummary(sh *store.Share) string {
	out := "services: " + strings.Join(sh.Services, ", ") + "; providers: " + strings.Join(sh.Providers, ", ")
	if len(sh.KeyScopes) > 0 {
		slugs := make([]string, 0, len(sh.KeyScopes))
		for slug := range sh.KeyScopes {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		parts := make([]string, 0, len(slugs))
		for _, slug := range slugs {
			sc := sh.KeyScopes[slug]
			switch sc.Mode {
			case store.ScopeCount:
				parts = append(parts, slug+" up to "+strconv.Itoa(sc.Count))
			case store.ScopePick:
				parts = append(parts, slug+" "+strconv.Itoa(len(sc.KeyIDs))+" picked")
			}
		}
		out += "; key scopes: " + strings.Join(parts, ", ")
	}
	return out
}

func (s *Server) loadShare(w http.ResponseWriter, r *http.Request) (*store.Share, bool) {
	sh, err := s.Store.GetShare(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return sh, true
}

// shareView adds usage to a share: today, the last 14 days and last use,
// and how many keys of each shared service its scope resolves to.
type shareView struct {
	*store.Share
	Usage       shareUsage                 `json:"usage"`
	ServiceKeys map[string]serviceKeyCount `json:"service_keys"`
	// Lease is the handover lease as a choice (1h, 6h, 24h, 72h, 7d).
	Lease string `json:"lease"`
	// EndsAt is the share's timer, the same value as expires_at.
	EndsAt *time.Time `json:"ends_at"`
	// Handover is set on handover shares: what was handed over, and the
	// last revoke push.
	Handover *handoverView `json:"handover"`
	// RotateChecklist lists, on a revoked handover share or an ended
	// reveal share, every key the other side saw: rotate them at the
	// provider.
	RotateChecklist []rotateItem `json:"rotate_checklist,omitempty"`
	// RevealedKeys lists the keys a reveal share showed. Never a secret.
	RevealedKeys []revealedKeyView `json:"revealed_keys"`
}

// serviceKeyCount is one shared service's keys: KeysShared the scope
// resolves to now, of KeysTotal the share could use without a scope.
type serviceKeyCount struct {
	KeysShared int `json:"keys_shared"`
	KeysTotal  int `json:"keys_total"`
}

func (s *Server) newShareView(r *http.Request, sh *store.Share, perService bool) shareView {
	v := shareView{Share: sh, Usage: s.shareUsage(r, sh, perService), ServiceKeys: s.shareKeyCounts(r.Context(), sh)}
	s.fillModeView(r.Context(), &v)
	return v
}

// shareKeyCounts resolves each shared service's key scope.
func (s *Server) shareKeyCounts(ctx context.Context, sh *store.Share) map[string]serviceKeyCount {
	out := map[string]serviceKeyCount{}
	var appID int64
	if sh.AppID != nil {
		appID = *sh.AppID
	}
	for _, slug := range sh.Services {
		sv, err := s.Store.GetServiceBySlug(ctx, slug)
		if err != nil {
			continue
		}
		var sc *store.KeyScope
		if v, ok := sh.KeyScopes[slug]; ok {
			sc = &v
		}
		res, err := s.Store.ScopeKeys(ctx, sv.ID, appID, sh.IncludeHidden && sv.TargetTrusted, sc)
		if err != nil {
			continue
		}
		out[slug] = serviceKeyCount{KeysShared: len(res.IDs), KeysTotal: res.Total}
	}
	return out
}

type shareUsage struct {
	TodayRequests int64            `json:"today_requests"`
	TodayCost     float64          `json:"today_cost"`
	MonthRequests int64            `json:"month_requests"`
	MonthCost     float64          `json:"month_cost"`
	LastUsedAt    *time.Time       `json:"last_used_at"`
	History       []store.UsageDay `json:"history"`
	PerService    map[string]int64 `json:"per_service,omitempty"`
	LimitHit      *store.LimitHit  `json:"limit_hit"`
}

func (s *Server) shareUsage(r *http.Request, sh *store.Share, perService bool) shareUsage {
	u := shareUsage{History: []store.UsageDay{}}
	if sh.AppID == nil {
		return u
	}
	ctx := r.Context()
	if au, err := s.Store.GetAppUsage(ctx, *sh.AppID, time.Now()); err == nil {
		u.TodayRequests, u.TodayCost, u.MonthRequests, u.MonthCost = au.TodayRequests, au.TodayCost, au.MonthRequests, au.MonthCost
		u.LimitHit = store.CheckLimits(sh.Limits, au, time.Now())
	}
	if h, err := s.Store.AppUsageHistory(ctx, *sh.AppID, 14); err == nil {
		u.History = h
	}
	if a, err := s.Store.GetApp(ctx, *sh.AppID); err == nil {
		u.LastUsedAt = a.LastUsedAt
	}
	if perService {
		u.PerService, _ = s.Store.ShareServiceCounts(ctx, *sh.AppID, time.Now().AddDate(0, 0, -14))
	}
	return u
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	shs, err := s.Store.ListShares(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := make([]shareView, 0, len(shs))
	for _, sh := range shs {
		out = append(out, s.newShareView(r, sh, false))
	}
	writeJSON(w, 200, map[string]any{"shares": out})
}

func (s *Server) getShare(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.loadShare(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, s.newShareView(r, sh, true))
}

func (s *Server) updateShare(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.loadShare(w, r)
	if !ok {
		return
	}
	if sh.Status == store.ShareRevoked {
		writeErr(w, 409, "share_revoked", "a revoked share cannot change")
		return
	}
	var in shareInput
	if !decode(w, r, &in) {
		return
	}
	if msg := s.applyShareInput(r, in, sh); msg != "" {
		writeErr(w, 400, shareInputCode(msg), msg)
		return
	}
	if err := s.Store.UpdateShare(r.Context(), sh); err != nil {
		writeStoreErr(w, err)
		return
	}
	sh, _ = s.Store.GetShare(r.Context(), sh.ID)
	s.Store.AddAudit(r.Context(), userFrom(r), "share.updated", sh.Name, shareSummary(sh))
	s.emitShare(notify.ShareUpdated, sh, "share "+sh.Name+" was updated")
	writeJSON(w, 200, s.newShareView(r, sh, true))
}

func (s *Server) shareStatusHandler(status, event, verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sh, ok := s.loadShare(w, r)
		if !ok {
			return
		}
		switch {
		case sh.Status == store.ShareRevoked:
			writeErr(w, 409, "share_revoked", "this share is already revoked")
			return
		case status == store.SharePaused && sh.Status != store.ShareActive:
			writeErr(w, 409, "not_active", "only an active share can be paused")
			return
		case status == store.ShareActive && sh.Status != store.SharePaused:
			writeErr(w, 409, "not_paused", "only a paused share can be resumed")
			return
		}
		sh, err := s.Store.SetShareStatus(r.Context(), sh.ID, status)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.Store.AddAudit(r.Context(), userFrom(r), event, sh.Name, "")
		s.emitShare(event, sh, "share "+sh.Name+" was "+verb)
		if status == store.ShareRevoked && sh.IsHandover() {
			// Tell the receiver to delete the keys now; the lease is the backstop.
			s.pushRevoke(r.Context(), sh, s.publicURL(r))
			sh, _ = s.Store.GetShare(r.Context(), sh.ID)
		}
		if status == store.ShareRevoked {
			s.revealEnded(r.Context(), sh, "revoked")
		}
		writeJSON(w, 200, s.newShareView(r, sh, false))
	}
}

func (s *Server) newShareInvite(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.loadShare(w, r)
	if !ok {
		return
	}
	var in struct {
		InviteValid string `json:"invite_valid"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if sh.IsPerson() {
		writeErr(w, 409, "person_share", "this share is for a person; issue a new claim link instead")
		return
	}
	if sh.Status != store.SharePending {
		writeErr(w, 409, "not_pending", "this share was already redeemed")
		return
	}
	code, hash, exp, msg := s.newInvite(r, sh, in.InviteValid)
	if msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if err := s.Store.SetShareInvite(r.Context(), sh.ID, hash, exp); err != nil {
		if errors.Is(err, store.ErrInviteUsed) {
			writeErr(w, 409, "not_pending", "this share was already redeemed")
			return
		}
		writeStoreErr(w, err)
		return
	}
	sh, _ = s.Store.GetShare(r.Context(), sh.ID)
	s.Store.AddAudit(r.Context(), userFrom(r), "share.invite", sh.Name, "new invite, valid until "+exp.Format(time.RFC3339))
	writeJSON(w, 200, map[string]any{"share": s.newShareView(r, sh, false), "invite": code})
}

// ---- peer links (receiver side) ----

type peerLinkView struct {
	*store.PeerLink
	Fingerprint   string           `json:"fingerprint"`
	LocalServices []*store.Service `json:"local_services"`
	// Handover lists the keys a handover share gave this server, with the
	// end of each lease. Never a secret.
	Handover *linkHandoverView `json:"handover"`
}

type linkHandoverView struct {
	Keys []linkHandoverKey `json:"keys"`
}

type linkHandoverKey struct {
	KeyID       int64      `json:"key_id"`
	RemoteKeyID *int64     `json:"remote_key_id"`
	Service     string     `json:"service"`
	Label       string     `json:"label"`
	ValidUntil  *time.Time `json:"valid_until"`
	Status      string     `json:"status"`
}

func (s *Server) peerLinkView(r *http.Request, l *store.PeerLink) peerLinkView {
	v := peerLinkView{PeerLink: l, LocalServices: []*store.Service{}}
	if l.Mode == store.ShareModeHandover {
		v.Handover = &linkHandoverView{Keys: []linkHandoverKey{}}
		keys, _ := s.Store.HandoverKeys(r.Context(), l.ID)
		slugs := map[int64]string{}
		for _, k := range keys {
			if _, ok := slugs[k.ServiceID]; !ok {
				if sv, err := s.Store.GetService(r.Context(), k.ServiceID); err == nil {
					slugs[k.ServiceID] = sv.Slug
				}
			}
			v.Handover.Keys = append(v.Handover.Keys, linkHandoverKey{KeyID: k.ID, RemoteKeyID: k.PeerRemoteKeyID, Service: slugs[k.ServiceID],
				Label: k.Label, ValidUntil: k.PeerValidUntil, Status: k.EffectiveStatus})
		}
	}
	if pub, err := peer.ParsePublicKey(l.PeerPublicKey); err == nil {
		v.Fingerprint = peer.Fingerprint(pub)
	}
	for _, id := range l.Services {
		if sv, err := s.Store.GetService(r.Context(), id); err == nil {
			v.LocalServices = append(v.LocalServices, sv)
		}
	}
	return v
}

func (s *Server) listPeers(w http.ResponseWriter, r *http.Request) {
	links, err := s.Store.ListPeerLinks(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := make([]peerLinkView, 0, len(links))
	for _, l := range links {
		out = append(out, s.peerLinkView(r, l))
	}
	writeJSON(w, 200, map[string]any{"peers": out})
}

func (s *Server) previewPeer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Invite  string `json:"invite"`
		Trusted bool   `json:"trusted"`
	}
	if !decode(w, r, &in) {
		return
	}
	inv, err := peer.ParseInvite(in.Invite)
	if err != nil {
		writeErr(w, 400, "bad_invite", err.Error())
		return
	}
	c := s.peers.Client
	if c == nil {
		c = peer.DefaultClient
	}
	owner, err := peer.FetchIdentity(r.Context(), c(in.Trusted), inv.URL)
	if err != nil {
		writeErr(w, 502, "peer_unreachable", s.Store.Scrub(r.Context(), err.Error()))
		return
	}
	self, _, _ := s.selfIdentity(r)
	if owner.ID == self.ID {
		writeErr(w, 400, "self_invite", "that invite is from this server")
		return
	}
	pv, err := peer.Preview(r.Context(), c(in.Trusted), inv)
	if err != nil {
		writeErr(w, 502, "preview_failed", s.Store.Scrub(r.Context(), err.Error()))
		return
	}
	writeJSON(w, 200, map[string]any{"owner": owner, "fingerprint": owner.Fingerprint(), "share": pv,
		"prefix": peer.SlugPrefix(owner.Name), "url": inv.URL})
}

func (s *Server) connectPeer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Invite  string `json:"invite"`
		Prefix  string `json:"prefix"`
		Trusted bool   `json:"trusted"`
		OwnerID string `json:"owner_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Prefix = strings.TrimSpace(strings.ToLower(in.Prefix))
	if in.Prefix != "" && !peer.ValidPrefix(in.Prefix) {
		writeErr(w, 400, "invalid", "prefix must be lowercase letters, digits and hyphens (max 24)")
		return
	}
	self, priv, err := s.selfIdentity(r)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	link, err := s.peers.Connect(r.Context(), in.Invite, in.Prefix, in.Trusted, in.OwnerID, self, priv, userID(r))
	if err != nil && link == nil {
		code := 502
		if errors.Is(err, peer.ErrBadInvite) {
			code = 400
		}
		if re := peer.AsRemote(err); re != nil && re.Status < 500 {
			code = re.Status
		}
		writeErr(w, code, "connect_failed", s.Store.Scrub(r.Context(), err.Error()))
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "peer.connect", link.PeerName, link.PeerID)
	writeJSON(w, 201, s.peerLinkView(r, link))
}

func (s *Server) peerLinkID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad_id", "id must be a number")
		return 0, false
	}
	return id, true
}

func (s *Server) syncPeer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.peerLinkID(w, r)
	if !ok {
		return
	}
	link, err := s.peers.Sync(r.Context(), id)
	if err != nil && link == nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, s.peerLinkView(r, link))
}

// disconnectPeer disconnects a link; a link already disconnected or revoked
// is removed from the list (its local services stay, keyless).
func (s *Server) disconnectPeer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.peerLinkID(w, r)
	if !ok {
		return
	}
	link, err := s.Store.GetPeerLink(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if len(link.TokenEnc) == 0 {
		if err := s.Store.DeletePeerLink(r.Context(), id); err != nil {
			writeStoreErr(w, err)
			return
		}
		s.Store.AddAudit(r.Context(), userFrom(r), "peer.remove", link.PeerName, link.PeerID)
		writeJSON(w, 200, map[string]any{"ok": true, "removed": true})
		return
	}
	link, err = s.peers.Disconnect(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "peer.disconnect", link.PeerName, link.PeerID)
	writeJSON(w, 200, s.peerLinkView(r, link))
}

// notShareApp refuses Apps page changes to an app that backs a share: its
// allowed services, mode and tokens are the share's, managed on Sharing.
func (s *Server) notShareApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err == nil {
			if a, err := s.Store.GetApp(r.Context(), id); err == nil && a.ShareID != nil {
				writeErr(w, 409, "share_app", "this app backs a share to another server; manage it on the Sharing page")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// PeerSyncer is the receiver side of sharing, for the worker's periodic sync.
func (s *Server) PeerSyncer() *peer.Syncer { return s.peers }
