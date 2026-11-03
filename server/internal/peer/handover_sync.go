package peer

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The receiver side of key handover. On every sync of a handover link the
// receiver fetches its sealed keys, opens them, stores them as hidden keys of
// local services whose provider is the owner's provider, renews the lease of
// keys it already holds and deletes the ones the owner no longer sends. Every
// handed over key is deleted (row and ciphertext) once its lease ends without
// renewal, with no network needed, and at once when the share stops or a
// signed revoke arrives.

// Wipe reasons.
const (
	WipeLeaseEnded   = "lease ended without renewal"
	WipeNotShared    = "no longer shared"
	WipeDisconnected = "disconnected"
	WipeRevokePush   = "revoke push from the owner"
)

// maxLease caps how long a receiver keeps a key without renewal, whatever
// the owner says.
const maxLease = 7 * 24 * time.Hour

// HandoverEvent tells the server about handed over keys arriving or going,
// for audit and webhooks. It never carries a secret.
type HandoverEvent struct {
	Link   *store.PeerLink
	Kind   string // "received" or "wiped"
	Keys   []string
	Reason string
}

func (y *Syncer) notify(ctx context.Context, ev HandoverEvent) {
	if y.OnHandover != nil && len(ev.Keys) > 0 {
		y.OnHandover(ctx, ev)
	}
}

// selfID is this server's id.
func (y *Syncer) selfID(ctx context.Context) (string, error) {
	priv, err := Keys(ctx, y.Store, y.Box)
	if err != nil {
		return "", err
	}
	return ServerID(priv.Public().(ed25519.PublicKey)), nil
}

func keyNames(keys []*store.Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Label)
	}
	return out
}

// wipe deletes handed over keys of a link and reports it.
func (y *Syncer) wipe(ctx context.Context, link *store.PeerLink, keys []*store.Key, reason string) int {
	if len(keys) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	n, err := y.Store.WipeKeys(ctx, ids)
	if err != nil {
		slog.Error("could not delete handed over keys", "peer", link.PeerID, "err", err)
		return 0
	}
	y.notify(ctx, HandoverEvent{Link: link, Kind: "wiped", Keys: keyNames(keys), Reason: reason})
	return n
}

// wipeLink deletes every key handed over through a link.
func (y *Syncer) wipeLink(ctx context.Context, link *store.PeerLink, reason string) int {
	keys, err := y.Store.HandoverKeys(ctx, link.ID)
	if err != nil {
		return 0
	}
	return y.wipe(ctx, link, keys, reason)
}

// WipeExpired deletes every handed over key whose lease ended. It needs no
// network, so a receiver cut off from the owner still lets go of the keys.
func (y *Syncer) WipeExpired(ctx context.Context, now time.Time) int {
	keys, err := y.Store.ExpiredHandoverKeys(ctx, now)
	if err != nil || len(keys) == 0 {
		return 0
	}
	byLink := map[int64][]*store.Key{}
	for _, k := range keys {
		byLink[*k.PeerLinkID] = append(byLink[*k.PeerLinkID], k)
	}
	n := 0
	for id, ks := range byLink {
		link, err := y.Store.GetPeerLink(ctx, id)
		if err != nil {
			link = &store.PeerLink{ID: id}
		}
		n += y.wipe(ctx, link, ks, WipeLeaseEnded)
	}
	return n
}

// applyHandover brings a handover link's keys in line with the owner. It
// returns problems worth showing on the link.
func (y *Syncer) applyHandover(ctx context.Context, link *store.PeerLink, token, status string) []string {
	if status != LinkActive {
		y.wipeLink(ctx, link, "share "+status+" by "+link.PeerName)
		return nil
	}
	c := y.client(link.Trusted)
	hk, err := FetchHandoverKeys(ctx, c, link.PeerURL, token)
	if err != nil {
		if re := AsRemote(err); re != nil && re.ShareState() != "" {
			y.wipeLink(ctx, link, "share "+re.ShareState()+" by "+link.PeerName)
			return nil
		}
		// Unreachable: keep the keys; their lease still ends them.
		return []string{"handed over keys: " + err.Error()}
	}
	priv, err := BoxKeys(ctx, y.Store, y.Box)
	if err != nil {
		return []string{"handed over keys: " + err.Error()}
	}
	self, err := y.selfID(ctx)
	if err != nil {
		return []string{"handed over keys: " + err.Error()}
	}
	var problems []string
	svcByRemote := map[string]int64{}
	for _, hs := range hk.Services {
		id, err := y.handoverService(ctx, link, hs)
		if err != nil {
			problems = append(problems, fmt.Sprintf("service %s: %v", hs.Slug, err))
			continue
		}
		svcByRemote[hs.Slug] = id
		link.Services[hs.Slug] = id
	}
	held, err := y.Store.HandoverKeys(ctx, link.ID)
	if err != nil {
		return append(problems, "handed over keys: "+err.Error())
	}
	byRemote := map[int64]*store.Key{}
	for _, k := range held {
		if k.PeerRemoteKeyID != nil {
			byRemote[*k.PeerRemoteKeyID] = k
		}
	}
	now := time.Now().UTC()
	lease := time.Duration(hk.LeaseSec) * time.Second
	if lease <= 0 || lease > maxLease {
		lease = maxLease
	}
	sent := map[int64]bool{}
	var received []string
	for _, k := range hk.Keys {
		sid, ok := svcByRemote[k.Service]
		if !ok {
			continue
		}
		secret, err := Open(priv, k.Sealed, link.RemoteShareID, k.ID, self)
		if err != nil {
			problems = append(problems, fmt.Sprintf("key %d of %s: %v", k.ID, k.Service, err))
			continue
		}
		until := now.Add(lease)
		if k.ValidUntil.Before(until) {
			until = k.ValidUntil.UTC()
		}
		if !until.After(now) {
			continue
		}
		sent[k.ID] = true
		if cur := byRemote[k.ID]; cur != nil {
			var enc []byte
			var fp, hint string
			if old, err := y.Box.Decrypt(cur.SecretEnc); err != nil || old != secret {
				if enc, err = y.Box.Encrypt(secret); err != nil {
					problems = append(problems, fmt.Sprintf("key %d of %s: %v", k.ID, k.Service, err))
					continue
				}
				fp, hint = y.Box.Fingerprint(secret), crypto.Hint(secret)
			}
			if err := y.Store.RenewHandoverKey(ctx, cur.ID, until, enc, fp, hint); err != nil {
				problems = append(problems, fmt.Sprintf("key %d of %s: %v", k.ID, k.Service, err))
			}
			continue
		}
		enc, err := y.Box.Encrypt(secret)
		if err != nil {
			problems = append(problems, fmt.Sprintf("key %d of %s: %v", k.ID, k.Service, err))
			continue
		}
		tier := k.Tier
		if tier != "backup" {
			tier = "primary"
		}
		label := crypto.Truncate("Handed over by "+link.PeerName+": "+k.Label, 120)
		_, err = y.Store.CreateKey(ctx, store.NewKey{
			ServiceID: sid, Label: label, SecretEnc: enc, SecretFP: y.Box.Fingerprint(secret), Hint: crypto.Hint(secret),
			Tier: tier, Hidden: true, Weight: 1, Priority: 100,
			Notes:    "Handed over by " + link.PeerID + " through share " + link.RemoteShareID + ". Deleted when its lease ends unless the owner renews it. Never revealed, leased or shared onward.",
			Handover: &store.KeyHandoverRef{LinkID: link.ID, RemoteKeyID: k.ID, ValidUntil: until},
		})
		if errors.Is(err, store.ErrDuplicate) {
			problems = append(problems, fmt.Sprintf("key %d of %s: this server already has that key", k.ID, k.Service))
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("key %d of %s: %v", k.ID, k.Service, err))
			continue
		}
		received = append(received, label)
	}
	gone := []*store.Key{}
	for _, k := range held {
		if k.PeerRemoteKeyID == nil || !sent[*k.PeerRemoteKeyID] {
			gone = append(gone, k)
		}
	}
	y.wipe(ctx, link, gone, WipeNotShared)
	y.notify(ctx, HandoverEvent{Link: link, Kind: "received", Keys: received})
	y.report(ctx, link, token)
	return problems
}

// report tells the owner which of its keys this server holds, with request
// counts and pool status. Best effort.
func (y *Syncer) report(ctx context.Context, link *store.PeerLink, token string) {
	keys, err := y.Store.HandoverKeys(ctx, link.ID)
	if err != nil {
		return
	}
	rep := UsageReport{Keys: []KeyReport{}}
	for _, k := range keys {
		if k.PeerRemoteKeyID == nil {
			continue
		}
		rep.Keys = append(rep.Keys, KeyReport{ID: *k.PeerRemoteKeyID, Requests: k.UsageTotal, Status: k.EffectiveStatus})
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := SendReport(rctx, y.client(link.Trusted), link.PeerURL, token, rep); err != nil {
		slog.Info("could not report handed over keys to the owner", "peer", link.PeerID, "err", err)
	}
}

// handoverService finds or makes the local service for one of the owner's
// services, with the owner's base URL and auth.
func (y *Syncer) handoverService(ctx context.Context, link *store.PeerLink, hs HandoverService) (int64, error) {
	if hs.Provider == providers.PeerSlug || hs.Provider == "" {
		return 0, errors.New("cannot take keys of this provider")
	}
	switch hs.AuthType {
	case "query", "header", "bearer":
	default:
		return 0, errors.New("unknown auth type " + hs.AuthType)
	}
	if err := y.checkBaseURL(ctx, hs.BaseURL, link.Trusted); err != nil {
		return 0, err
	}
	if id, ok := link.Services[hs.Slug]; ok {
		if sv, err := y.Store.GetService(ctx, id); err == nil && sv.PeerLinkID != nil && *sv.PeerLinkID == link.ID {
			if sv.BaseURL != hs.BaseURL || sv.AuthType != hs.AuthType || sv.AuthName != hs.AuthName {
				sv.BaseURL, sv.AuthType, sv.AuthName = hs.BaseURL, hs.AuthType, hs.AuthName
				if err := y.Store.UpdateService(ctx, sv); err != nil {
					return 0, err
				}
			}
			return id, nil
		}
	}
	prov, err := y.Store.GetProvider(ctx, hs.Provider)
	if err != nil {
		return 0, fmt.Errorf("provider %s is not on this server", hs.Provider)
	}
	sv, err := catalog.ServiceDefaults(prov)
	if err != nil {
		return 0, err
	}
	slug := link.Prefix + "-" + hs.Slug
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	sv.Slug = catalog.UniqueServiceSlug(ctx, y.Store, slug)
	if hs.Name != "" {
		sv.Name = hs.Name
	}
	sv.BaseURL, sv.AuthType, sv.AuthName = hs.BaseURL, hs.AuthType, hs.AuthName
	// Handed over keys are hidden; the target is the owner's own choice for
	// its own keys, confirmed with the invite.
	sv.TargetTrusted = true
	created, err := y.Store.CreateService(ctx, sv)
	if err != nil {
		return 0, err
	}
	if err := y.Store.SetServicePeer(ctx, created.ID, link.ID, hs.Slug); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// checkBaseURL refuses a base URL that is not http(s), and, unless the link
// is on a private network, one whose host is not a public address: the
// owner chooses this URL and this server's apps send requests to it.
func (y *Syncer) checkBaseURL(ctx context.Context, raw string, trusted bool) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("the owner sent no usable base URL")
	}
	if trusted {
		return nil
	}
	if y.CheckHost != nil {
		return y.CheckHost(ctx, u.Hostname())
	}
	return PublicHost(ctx, u.Hostname())
}

// PublicHost refuses a host that is or resolves to a non-public address.
func PublicHost(ctx context.Context, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if catalog.BlockedIP(ip) {
			return errors.New("the owner's base URL is not a public address")
		}
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(rctx, "ip", host)
	if err != nil {
		return fmt.Errorf("cannot resolve the owner's base URL host: %w", err)
	}
	for _, a := range addrs {
		if catalog.BlockedIP(a) {
			return errors.New("the owner's base URL is not a public address")
		}
	}
	return nil
}

// Revoke errors.
var (
	ErrRevokeUnknown   = errors.New("no share from that server with that id")
	ErrRevokeSignature = errors.New("the revoke is not signed by the owner")
	ErrRevokeStale     = errors.New("the revoke's timestamp is too far from this server's clock")
	ErrRevokeReplay    = errors.New("that revoke was already used")
	ErrRevokeReceiver  = errors.New("the revoke is for another server")
)

// HandleRevoke applies a revoke pushed by an owner: the signature must be
// the pinned owner key's, the timestamp within RevokeSkew and the nonce
// new. It deletes every handed over key of the link and marks it revoked.
func (y *Syncer) HandleRevoke(ctx context.Context, in RevokeRequest, now time.Time) (int, error) {
	self, err := y.selfID(ctx)
	if err != nil {
		return 0, err
	}
	if in.ReceiverID != self {
		return 0, ErrRevokeReceiver
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	link, err := y.Store.PeerLinkByShare(ctx, in.OwnerID, in.ShareID)
	if err != nil {
		return 0, ErrRevokeUnknown
	}
	pub, err := ParsePublicKey(link.PeerPublicKey)
	if err != nil || ServerID(pub) != link.PeerID || !VerifyRevoke(pub, in) {
		return 0, ErrRevokeSignature
	}
	if d := now.Sub(time.Unix(in.TS, 0)); d > RevokeSkew || d < -RevokeSkew {
		return 0, ErrRevokeStale
	}
	if len(in.Nonce) < 16 || len(in.Nonce) > 128 {
		return 0, ErrRevokeSignature
	}
	fresh, err := y.Store.UseNonce(ctx, "revoke:"+link.PeerID, in.Nonce, now.Add(2*RevokeSkew))
	if err != nil {
		return 0, err
	}
	if !fresh {
		return 0, ErrRevokeReplay
	}
	n := y.wipeLink(ctx, link, WipeRevokePush)
	if link.Status != LinkDisconnected {
		if _, err := y.setStatus(ctx, link, LinkRevoked); err != nil {
			return n, err
		}
	}
	return n, nil
}
