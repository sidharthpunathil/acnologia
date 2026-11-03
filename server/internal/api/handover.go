package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Share modes and timers. A proxy share keeps every key on this server. A
// handover share seals its keys to the receiver, which calls the provider
// itself; such a key cannot be taken back, so every handed over key carries
// a lease the receiver must renew on each sync, and revoke pushes a signed
// message that makes a cooperating receiver delete the keys at once. Both
// modes have a timer (ends_at) that expires or revokes the share.

// leaseChoices are the handover leases a share may have.
var leaseChoices = map[string]int{
	"1h": 3600, "6h": 6 * 3600, "24h": 24 * 3600, "72h": 72 * 3600, "7d": 7 * 24 * 3600,
}

// leaseName is the choice name for a lease in seconds, or "" for none.
func leaseName(sec int) string {
	for k, v := range leaseChoices {
		if v == sec {
			return k
		}
	}
	return ""
}

// errHandoverLimits explains why a handover share takes no request or
// budget limits.
const errHandoverLimits = "request and budget limits cannot apply to a handover share: the other server calls the provider itself, so this server never sees the requests. Use a proxy share for limits; timers and the lease still apply"

// applyShareModeInput validates mode, lease, end action and the private
// network flag onto sh.
func applyShareModeInput(in shareInput, sh *store.Share) string {
	if in.Mode != nil {
		m := strings.TrimSpace(*in.Mode)
		if m == "" {
			m = store.ShareModeProxy
		}
		if m != store.ShareModeProxy && m != store.ShareModeHandover && m != store.ShareModeReveal {
			return "mode must be proxy, handover or reveal"
		}
		if m == store.ShareModeHandover && sh.IsPerson() {
			return "handover is for another server; a share with a person is proxy or reveal"
		}
		if m == store.ShareModeReveal && !sh.IsPerson() {
			return "reveal is for a person; a share with another server is proxy or handover"
		}
		if sh.Status != "" && sh.Status != store.SharePending && m != sh.Mode {
			return "the mode of a share cannot change after it is redeemed; create a new share instead"
		}
		sh.Mode = m
	}
	if sh.Mode == "" {
		sh.Mode = store.ShareModeProxy
	}
	if in.Lease != nil {
		sec, ok := leaseChoices[strings.TrimSpace(*in.Lease)]
		if !ok {
			return "lease must be 1h, 6h, 24h, 72h or 7d"
		}
		sh.LeaseSec = sec
	}
	if sh.LeaseSec <= 0 {
		sh.LeaseSec = store.DefaultLeaseSec
	}
	if in.EndAction != nil {
		a := strings.TrimSpace(*in.EndAction)
		if a == "" {
			a = store.EndExpire
		}
		if a != store.EndExpire && a != store.EndRevoke {
			return "end_action must be expire or revoke"
		}
		sh.EndAction = a
	}
	if sh.EndAction == "" {
		sh.EndAction = store.EndExpire
	}
	if in.PrivateNetwork != nil {
		sh.PrivateNetwork = *in.PrivateNetwork
	}
	if sh.IsHandover() && sh.Limits.Any() {
		return errHandoverLimits
	}
	if sh.IsReveal() && sh.Limits.Any() {
		return errRevealLimits
	}
	return ""
}

// ---- views ----

// handoverView is what a handover share shows about its keys. Never a secret.
type handoverView struct {
	Keys           []handoverKeyView `json:"keys"`
	LastRevokePush *store.RevokePush `json:"last_revoke_push"`
}

type handoverKeyView struct {
	KeyID            int64      `json:"key_id"`
	Service          string     `json:"service"`
	Label            string     `json:"label"`
	Provider         string     `json:"provider"`
	Live             bool       `json:"live"`
	HandedAt         time.Time  `json:"handed_at"`
	RenewedAt        time.Time  `json:"renewed_at"`
	ValidUntil       time.Time  `json:"valid_until"`
	WipedAt          *time.Time `json:"wiped_at"`
	WipeReason       string     `json:"wipe_reason"`
	ReportedRequests *int64     `json:"reported_requests"`
	ReportedStatus   string     `json:"reported_status"`
	ReportedAt       *time.Time `json:"reported_at"`
}

// rotateItem is one key to rotate at its provider after a handover share
// was revoked: the receiver saw it and could have kept a copy.
type rotateItem struct {
	KeyID        int64  `json:"key_id"`
	Service      string `json:"service"`
	Label        string `json:"label"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	// ProviderURL is the provider's website. KeyPageURL would name its key
	// management page, but no provider spec records one yet, so it is
	// always omitted.
	ProviderURL string     `json:"provider_url"`
	KeyPageURL  string     `json:"key_page_url,omitempty"`
	HandedAt    time.Time  `json:"handed_at"`
	WipedAt     *time.Time `json:"wiped_at"`
	// KeyExists is false when the key was already deleted here.
	KeyExists bool `json:"key_exists"`
}

// fillModeView adds the mode, timer and handover parts to a share view.
func (s *Server) fillModeView(ctx context.Context, v *shareView) {
	sh := v.Share
	v.Lease = leaseName(sh.LeaseSec)
	v.EndsAt = sh.ExpiresAt
	s.fillRevealView(ctx, v)
	if !sh.IsHandover() {
		return
	}
	hv := &handoverView{Keys: []handoverKeyView{}, LastRevokePush: sh.LastRevokePush}
	rows, _ := s.Store.ListHandovers(ctx, sh.ID)
	for _, h := range rows {
		hv.Keys = append(hv.Keys, handoverKeyView{KeyID: h.KeyID, Service: h.Service, Label: h.Label, Provider: h.Provider, Live: h.Live(),
			HandedAt: h.HandedAt, RenewedAt: h.RenewedAt, ValidUntil: h.ValidUntil, WipedAt: h.WipedAt, WipeReason: h.WipeReason,
			ReportedRequests: h.ReportedRequests, ReportedStatus: h.ReportedStatus, ReportedAt: h.ReportedAt})
	}
	v.Handover = hv
	if sh.Status != store.ShareRevoked {
		return
	}
	v.RotateChecklist = s.rotateChecklist(ctx, rows)
}

// rotateChecklist lists handed over keys to rotate at their provider.
func (s *Server) rotateChecklist(ctx context.Context, rows []*store.Handover) []rotateItem {
	out := []rotateItem{}
	for _, h := range rows {
		it := rotateItem{KeyID: h.KeyID, Service: h.Service, Label: h.Label, Provider: h.Provider, HandedAt: h.HandedAt, WipedAt: h.WipedAt}
		if p, err := s.Store.GetProvider(ctx, h.Provider); err == nil {
			it.ProviderName, it.ProviderURL = p.Name, p.Website
		}
		if _, err := s.Store.GetKey(ctx, h.KeyID); err == nil {
			it.KeyExists = true
		}
		out = append(out, it)
	}
	return out
}

// fillHandovers marks keys this server handed over to other servers.
func (s *Server) fillHandovers(ctx context.Context, keys ...*store.Key) {
	if len(keys) == 0 {
		return
	}
	ids := make([]int64, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	m, err := s.Store.KeyHandovers(ctx, ids)
	if err != nil {
		return
	}
	rv, err := s.Store.KeyReveals(ctx, ids)
	if err != nil {
		return
	}
	for _, k := range keys {
		k.Handovers = m[k.ID]
		k.RevealedTo = rv[k.ID]
	}
}

// ---- owner: delivery and reports ----

// peerKeys answers GET /api/peer/v1/keys: the share's keys sealed to the
// receiver. The share gate already refused a share that is not active.
func (s *Server) peerKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sh, ok := s.shareFromApp(w, r)
	if !ok {
		return
	}
	if !sh.IsHandover() {
		writeErr(w, 409, "not_handover", "this share is proxy only; its keys never leave the owner")
		return
	}
	if sh.PeerBoxPublicKey == "" || sh.PeerID == "" {
		writeErr(w, 409, "no_box_key", "the other server gave no key to seal to")
		return
	}
	out, err := s.handoverKeys(r.Context(), sh)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// handoverKeys seals the keys a handover share gives and records each in
// the exposure table. A key handed over for the first time (or again after
// it was wiped) is audited; renewals are not.
func (s *Server) handoverKeys(ctx context.Context, sh *store.Share) (peer.HandoverKeys, error) {
	out := peer.HandoverKeys{ShareID: sh.ID, LeaseSec: sh.LeaseSec, Services: []peer.HandoverService{}, Keys: []peer.HandoverKey{}}
	var appID int64
	if sh.AppID != nil {
		appID = *sh.AppID
	}
	until := time.Now().UTC().Add(time.Duration(sh.LeaseSec) * time.Second).Truncate(time.Second)
	var delivered []string
	// Every key this share was ever given, wiped or not: a count scope
	// never hands over more than N of them.
	past, err := s.Store.ListHandovers(ctx, sh.ID)
	if err != nil {
		return out, err
	}
	given := make(map[int64]bool, len(past))
	for _, h := range past {
		given[h.KeyID] = true
	}
	for _, slug := range sh.Services {
		sv, err := s.Store.GetServiceBySlug(ctx, slug)
		if err != nil || sv.Provider == providers.PeerSlug || sv.PeerLinkID != nil {
			continue
		}
		var sc *store.KeyScope
		if v, ok := sh.KeyScopes[slug]; ok {
			sc = &v
		}
		keys, err := s.Store.ListKeys(ctx, sv.ID, true)
		if err != nil {
			return out, err
		}
		res := store.ResolveHandoverScope(sc, keys, appID, sh.IncludeHidden && sv.TargetTrusted, given)
		out.Services = append(out.Services, peer.HandoverService{Slug: sv.Slug, Name: sv.Name, Provider: sv.Provider,
			BaseURL: sv.BaseURL, AuthType: sv.AuthType, AuthName: sv.AuthName})
		for _, k := range keys {
			if !res.IDs[k.ID] || !k.InRotation() || k.IsHandedOver() {
				continue
			}
			secret, err := s.Pool.Box.Decrypt(k.SecretEnc)
			if err != nil {
				continue
			}
			sealed, err := peer.Seal(sh.PeerBoxPublicKey, sh.ID, k.ID, sh.PeerID, secret)
			if err != nil {
				return out, err
			}
			out.Keys = append(out.Keys, peer.HandoverKey{ID: k.ID, Service: sv.Slug, Label: k.Label, Tier: k.Tier, Sealed: sealed, ValidUntil: until})
			first, err := s.Store.RecordHandover(ctx, sh.ID, sh.PeerID, store.HandoverRecord{KeyID: k.ID, Service: sv.Slug,
				Provider: sv.Provider, Label: k.Label, ValidUntil: until})
			if err != nil {
				return out, err
			}
			if first {
				delivered = append(delivered, fmt.Sprintf("%s/%s", sv.Slug, k.Label))
			}
		}
	}
	if len(delivered) > 0 {
		detail := fmt.Sprintf("%d key(s) handed over to %s: %s", len(delivered), sh.PeerID, strings.Join(delivered, ", "))
		s.Store.AddAudit(ctx, nil, "share.handover", sh.Name, detail)
		s.emitShareData(notify.ShareHandover, sh, "keys of share "+sh.Name+" were handed over to "+sh.PeerName,
			map[string]any{"keys": delivered})
	}
	return out, nil
}

// peerReport takes the receiver's report: per key requests and status, and
// which keys it still holds.
func (s *Server) peerReport(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFromApp(w, r)
	if !ok {
		return
	}
	var in peer.UsageReport
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if !decode(w, r, &in) {
		return
	}
	if !sh.IsHandover() {
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	held := make([]store.ReportedKey, 0, len(in.Keys))
	for _, k := range in.Keys {
		held = append(held, store.ReportedKey{KeyID: k.ID, Requests: k.Requests, Status: reportedStatus(k.Status)})
	}
	gone, err := s.Store.ApplyHandoverReport(r.Context(), sh.ID, held)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.recordWiped(r.Context(), sh, gone, "reported deleted by "+sh.PeerName)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// reportedStatus keeps a receiver's status word to the known key statuses.
func reportedStatus(st string) string {
	switch st {
	case store.StatusActive, store.StatusDisabled, store.StatusExhausted, store.StatusInvalid, store.StatusBanned, "cooldown":
		return st
	}
	return "unknown"
}

// recordWiped audits and announces handed over keys that are gone.
func (s *Server) recordWiped(ctx context.Context, sh *store.Share, gone []*store.Handover, why string) {
	if len(gone) == 0 {
		return
	}
	names := make([]string, 0, len(gone))
	for _, h := range gone {
		names = append(names, h.Service+"/"+h.Label)
	}
	s.Store.AddAudit(ctx, nil, "share.handover_wiped", sh.Name, fmt.Sprintf("%d key(s): %s (%s)", len(gone), strings.Join(names, ", "), why))
	s.emitShareData(notify.ShareHandoverWiped, sh, "handed over keys of share "+sh.Name+" are gone: "+why,
		map[string]any{"keys": names, "reason": why})
}

// ---- owner: revoke push ----

// pushClient is the client for calls to a receiver: SafeClient unless the
// share was marked as on a private network.
func (s *Server) pushClient(private bool) *http.Client {
	if s.peers.Client != nil {
		return s.peers.Client(private)
	}
	return peer.DefaultClient(private)
}

// pushRevoke tells a handover share's receiver to delete the keys at once,
// signed with this server's identity key, and records the outcome. Best
// effort: if it fails, the lease still ends the keys.
func (s *Server) pushRevoke(ctx context.Context, sh *store.Share, selfURL string) {
	if !sh.IsHandover() || sh.PeerURL == "" || sh.PeerID == "" {
		return
	}
	self, priv, err := peer.Self(ctx, s.Store, s.Pool.Box, s.Cfg.Version, selfURL)
	ok, detail := false, ""
	if err == nil {
		req := peer.SignRevoke(priv, self.ID, sh.ID, sh.PeerID, time.Now())
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var res peer.RevokeResponse
		res, err = peer.PushRevoke(pctx, s.pushClient(sh.PrivateNetwork), sh.PeerURL, req)
		cancel()
		if err == nil && res.OK {
			ok = true
			detail = fmt.Sprintf("wiped %d key(s) at %s", res.Wiped, time.Now().UTC().Format(time.RFC3339))
		}
	}
	if !ok {
		// The lease is the latest any cooperating receiver keeps a key.
		last := time.Now().UTC()
		if rows, err := s.Store.ListHandovers(ctx, sh.ID); err == nil {
			for _, h := range rows {
				if h.Live() && h.ValidUntil.After(last) {
					last = h.ValidUntil
				}
			}
		}
		why := "the other server did not confirm"
		if err != nil {
			why = s.Store.Scrub(ctx, err.Error())
		}
		detail = fmt.Sprintf("not reached (%s); the keys are deleted by %s when their lease ends", why, last.Format(time.RFC3339))
	}
	_ = s.Store.SetRevokePush(ctx, sh.ID, ok, detail)
	s.Store.AddAudit(ctx, nil, "share.revoke_push", sh.Name, detail)
	s.emitShareData(notify.ShareRevokePush, sh, "revoke push for share "+sh.Name+": "+detail, map[string]any{"ok": ok, "detail": detail})
	if ok {
		gone, _ := s.Store.MarkHandoversWiped(ctx, sh.ID, nil, "revoke push confirmed", time.Now())
		s.recordWiped(ctx, sh, gone, "revoke push confirmed by "+sh.PeerName)
	}
}

// ---- receiver: revoke push ----

// peerRevoke takes a signed revoke from an owner and deletes the keys it
// handed over. Public; the signature is the authentication.
func (s *Server) peerRevoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Only failed attempts count, per address: anyone can call this route,
	// and junk from one address must not hold back an owner's signed revoke.
	ip := clientIP(r)
	if s.revokeLimit.blocked(ip) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "rate_limited", "too many failed revoke attempts, try again in a minute")
		return
	}
	var in peer.RevokeRequest
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if !decode(w, r, &in) {
		s.revokeLimit.fail(ip)
		return
	}
	n, err := s.peers.HandleRevoke(r.Context(), in, time.Now())
	if err != nil {
		s.revokeLimit.fail(ip)
	}
	switch {
	case errors.Is(err, peer.ErrRevokeUnknown):
		writeErr(w, 404, "unknown_share", err.Error())
		return
	case errors.Is(err, peer.ErrRevokeSignature), errors.Is(err, peer.ErrRevokeReceiver):
		writeErr(w, 401, "bad_signature", err.Error())
		return
	case errors.Is(err, peer.ErrRevokeStale):
		writeErr(w, 400, "stale", err.Error())
		return
	case errors.Is(err, peer.ErrRevokeReplay):
		writeErr(w, 409, "replay", err.Error())
		return
	case err != nil:
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), nil, "peer.revoked", in.OwnerID, fmt.Sprintf("revoke push for share %s, %d handed over key(s) deleted", in.ShareID, n))
	writeJSON(w, 200, peer.RevokeResponse{OK: true, Wiped: n})
}

// onHandover audits and announces handed over keys arriving or going on
// this server (the receiver).
func (s *Server) onHandover(ctx context.Context, ev peer.HandoverEvent) {
	target := ev.Link.PeerName
	if target == "" {
		target = ev.Link.PeerID
	}
	typ, action, msg := notify.PeerHandoverWiped, "peer.handover_wiped", fmt.Sprintf("%d handed over key(s) from %s deleted: %s", len(ev.Keys), target, ev.Reason)
	if ev.Kind == "received" {
		typ, action, msg = notify.PeerHandoverReceived, "peer.handover_received", fmt.Sprintf("%d key(s) handed over by %s", len(ev.Keys), target)
	}
	detail := msg + " (" + strings.Join(ev.Keys, ", ") + ")"
	s.Store.AddAudit(ctx, nil, action, target, detail)
	if s.Pool.Notify != nil {
		s.Pool.Notify.Emit(notify.Event{Type: typ, Message: msg,
			Data: map[string]any{"peer_id": ev.Link.PeerID, "link_id": ev.Link.ID, "keys": ev.Keys, "reason": ev.Reason}})
	}
}

// ---- timers ----

// endShare applies a share's end action when its timer passed: expire (can
// be extended back) or revoke (for good, with a revoke push for handover).
func (s *Server) endShare(ctx context.Context, sh *store.Share, selfURL string) (*store.Share, error) {
	if sh.EndAction == store.EndRevoke {
		n, err := s.Store.SetShareStatus(ctx, sh.ID, store.ShareRevoked)
		if err != nil {
			return nil, err
		}
		s.Store.AddAudit(ctx, nil, "share.revoked", n.Name, "timer ended")
		s.emitShareData(notify.ShareRevoked, n, "share "+n.Name+" was revoked: its timer ended", map[string]any{"reason": "timer"})
		s.pushRevoke(ctx, n, selfURL)
		s.revealEnded(ctx, n, "revoked by its timer")
		return s.Store.GetShare(ctx, n.ID)
	}
	n, err := s.Store.SetShareStatus(ctx, sh.ID, store.ShareExpired)
	if err != nil {
		return nil, err
	}
	s.Store.AddAudit(ctx, nil, "share.expired", n.Name, "timer ended")
	s.emitShareData(notify.ShareExpired, n, "share "+n.Name+" expired: its timer ended", map[string]any{"reason": "timer"})
	s.revealEnded(ctx, n, "expired")
	return n, nil
}

// extendShare moves a share's timer later. An expired share becomes active
// again; for handover the keys are handed over again on the next sync.
func (s *Server) extendShare(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.loadShare(w, r)
	if !ok {
		return
	}
	var in struct {
		EndsAt *string `json:"ends_at"`
	}
	if !decode(w, r, &in) {
		return
	}
	if sh.Status == store.ShareRevoked {
		writeErr(w, 409, "share_revoked", "a revoked share cannot be extended")
		return
	}
	if in.EndsAt == nil {
		writeErr(w, 400, "invalid", "ends_at is required: an RFC 3339 time, or \"\" for no end")
		return
	}
	var next *time.Time
	if *in.EndsAt != "" {
		t, err := time.Parse(time.RFC3339, *in.EndsAt)
		if err != nil {
			writeErr(w, 400, "invalid", "ends_at must be an RFC 3339 time")
			return
		}
		t = t.UTC()
		if !t.After(time.Now()) {
			writeErr(w, 400, "invalid", "ends_at must be in the future")
			return
		}
		if sh.ExpiresAt != nil && !t.After(*sh.ExpiresAt) {
			writeErr(w, 400, "invalid", "ends_at must be later than the current end; edit the share to shorten it")
			return
		}
		next = &t
	}
	was := sh.Status
	sh.ExpiresAt = next
	if err := s.Store.UpdateShare(r.Context(), sh); err != nil {
		writeStoreErr(w, err)
		return
	}
	sh, _ = s.Store.GetShare(r.Context(), sh.ID)
	detail := "no end"
	if next != nil {
		detail = "ends " + next.Format(time.RFC3339)
	}
	if was == store.ShareExpired && sh.Status == store.ShareActive {
		detail += "; active again"
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "share.extended", sh.Name, detail)
	s.emitShareData(notify.ShareUpdated, sh, "share "+sh.Name+" was extended: "+detail, map[string]any{"ends_at": next})
	writeJSON(w, 200, s.newShareView(r, sh, true))
}

// SharingTicker is the worker's once a minute sharing housekeeping.
func (s *Server) SharingTicker() interface{ Tick(context.Context) } { return sharingTicker{s} }

type sharingTicker struct{ s *Server }

// Tick deletes handed over keys whose lease ended (receiver, no network
// first), applies ended timers (owner) and notes leases that lapsed on the
// owner's exposure records.
func (t sharingTicker) Tick(ctx context.Context) { t.s.sharingTick(ctx, time.Now()) }

func (s *Server) sharingTick(ctx context.Context, now time.Time) {
	if n := s.peers.WipeExpired(ctx, now); n > 0 {
		slog.Info("deleted handed over keys whose lease ended", "count", n)
	}
	selfURL := s.Cfg.PublicURL
	if selfURL == "" {
		selfURL = peer.SavedURL(ctx, s.Store)
	}
	if ended, err := s.Store.EndedShares(ctx, now); err == nil {
		for _, sh := range ended {
			if _, err := s.endShare(ctx, sh, selfURL); err != nil {
				slog.Warn("could not end share", "share", sh.ID, "err", err)
			}
		}
	}
	lapsed, err := s.Store.LapsedHandovers(ctx, now)
	if err != nil || len(lapsed) == 0 {
		return
	}
	byShare := map[string][]int64{}
	for _, h := range lapsed {
		byShare[h.ShareID] = append(byShare[h.ShareID], h.KeyID)
	}
	ids := make([]string, 0, len(byShare))
	for id := range byShare {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sh, err := s.Store.GetShare(ctx, id)
		if err != nil {
			continue
		}
		gone, _ := s.Store.MarkHandoversWiped(ctx, id, byShare[id], "lease ended without renewal", now)
		s.recordWiped(ctx, sh, gone, "lease ended without renewal")
	}
}

func (s *Server) emitShareData(typ string, sh *store.Share, msg string, extra map[string]any) {
	if s.Pool.Notify == nil {
		return
	}
	var appID int64
	if sh.AppID != nil {
		appID = *sh.AppID
	}
	data := map[string]any{"share_id": sh.ID, "peer_id": sh.PeerID, "status": sh.Status, "mode": sh.Mode, "recipient": sh.Recipient}
	for k, v := range extra {
		data[k] = v
	}
	s.Pool.Notify.Emit(notify.Event{Type: typ, AppID: appID, App: sh.Name, Message: msg, Data: data})
}
