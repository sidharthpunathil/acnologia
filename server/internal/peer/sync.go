package peer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// SyncInterval is how often the worker refreshes every link's manifest.
const SyncInterval = 10 * time.Minute

// Peer link statuses on the receiver.
const (
	LinkActive       = "active"
	LinkPaused       = "paused"
	LinkExpired      = "expired"
	LinkRevoked      = "revoked"
	LinkDisconnected = "disconnected"
)

// Syncer is the receiver side of sharing: it connects to an owner with an
// invite and keeps local services in step with the share's manifest.
type Syncer struct {
	Store   *store.Store
	Box     *crypto.Box
	Catalog *catalog.Catalog
	// Client returns the HTTP client for a link. trusted allows private
	// addresses (two servers on one network); otherwise only public ones.
	Client func(trusted bool) *http.Client

	mu sync.Mutex // one sync at a time, so two cannot both create a service
}

// DefaultClient is SafeClient for untrusted links and a plain client for
// trusted ones.
func DefaultClient(trusted bool) *http.Client {
	if trusted {
		return &http.Client{Timeout: 20 * time.Second}
	}
	return catalog.SafeClient(20 * time.Second)
}

func (y *Syncer) client(trusted bool) *http.Client {
	if y.Client != nil {
		return y.Client(trusted)
	}
	return DefaultClient(trusted)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// SlugPrefix turns a server name into a service slug prefix.
func SlugPrefix(name string) string {
	p := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(p) > 24 {
		p = strings.Trim(p[:24], "-")
	}
	if p == "" {
		p = "peer"
	}
	return p
}

var prefixRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// ValidPrefix reports whether p can start a service slug.
func ValidPrefix(p string) bool { return prefixRe.MatchString(p) }

// Connect redeems an invite and creates the link and its local services.
// self and priv are this server's identity and key; want, when set, is the
// owner id the person confirmed in the preview.
func (y *Syncer) Connect(ctx context.Context, code, prefix string, trusted bool, want string, self Identity, priv []byte, userID int64) (*store.PeerLink, error) {
	inv, err := ParseInvite(code)
	if err != nil {
		return nil, err
	}
	c := y.client(trusted)
	owner, err := FetchIdentity(ctx, c, inv.URL)
	if err != nil {
		return nil, err
	}
	if want != "" && owner.ID != want {
		return nil, errors.New("the server at that URL is not the one you confirmed")
	}
	if owner.ID == self.ID {
		return nil, errors.New("that invite is from this server")
	}
	nb := make([]byte, 16)
	_, _ = rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	req := RedeemRequest{
		Invite: inv.Secret, ShareID: inv.ShareID, ID: self.ID, Name: self.Name, URL: self.URL,
		PublicKey: self.PublicKey, Nonce: nonce,
		Signature: SignRedeem(priv, inv.ShareID, InviteHash(inv.Secret), nonce, self.ID),
	}
	res, err := Redeem(ctx, c, inv, req)
	if err != nil {
		return nil, err
	}
	if res.Owner.ID != owner.ID || res.Owner.PublicKey != owner.PublicKey {
		return nil, errors.New("the owner answered with a different identity")
	}
	if !store.IsAppToken(res.Token) {
		return nil, errors.New("the owner sent no usable token")
	}
	enc, err := y.Box.Encrypt(res.Token)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		prefix = SlugPrefix(owner.Name)
	}
	link, err := y.Store.CreatePeerLink(ctx, &store.PeerLink{
		PeerID: owner.ID, PeerName: owner.Name, PeerURL: inv.URL, PeerPublicKey: owner.PublicKey,
		RemoteShareID: inv.ShareID, TokenEnc: enc, Status: LinkActive, Prefix: prefix, Trusted: trusted,
	}, userID)
	if err != nil {
		return nil, err
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if err := y.apply(ctx, link, res.Token, &res.Manifest); err != nil {
		return link, err
	}
	return y.Store.GetPeerLink(ctx, link.ID)
}

// Sync refreshes one link from its owner.
func (y *Syncer) Sync(ctx context.Context, id int64) (*store.PeerLink, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	link, err := y.Store.GetPeerLink(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(link.TokenEnc) == 0 {
		return link, nil
	}
	token, err := y.Box.Decrypt(link.TokenEnc)
	if err != nil {
		return nil, err
	}
	m, err := FetchManifest(ctx, y.client(link.Trusted), link.PeerURL, token)
	now := time.Now().UTC()
	if err != nil {
		if re := AsRemote(err); re != nil && re.ShareState() != "" {
			return y.setStatus(ctx, link, re.ShareState())
		}
		link.LastError = err.Error()
		link.LastSyncAt = &now
		_ = y.Store.SavePeerLinkState(ctx, link)
		return link, err
	}
	if err := y.apply(ctx, link, token, &m); err != nil {
		return link, err
	}
	return y.Store.GetPeerLink(ctx, id)
}

// SyncAll refreshes every link that still holds a token.
func (y *Syncer) SyncAll(ctx context.Context) {
	links, err := y.Store.ListPeerLinks(ctx)
	if err != nil {
		return
	}
	for _, l := range links {
		if len(l.TokenEnc) == 0 || l.Status == LinkRevoked || l.Status == LinkDisconnected {
			continue
		}
		if _, err := y.Sync(ctx, l.ID); err != nil {
			slog.Warn("peer sync failed", "peer", l.PeerID, "err", err)
		}
	}
}

// MarkRemoteState applies a status the owner reported (from a proxied
// refusal) to the link behind a local service.
func (y *Syncer) MarkRemoteState(ctx context.Context, linkID int64, state string) {
	y.mu.Lock()
	defer y.mu.Unlock()
	link, err := y.Store.GetPeerLink(ctx, linkID)
	if err != nil || link.Status == state || link.Status == LinkRevoked || link.Status == LinkDisconnected {
		return
	}
	_, _ = y.setStatus(ctx, link, state)
}

// setStatus moves a link to paused, expired or revoked and disables its
// services at once. Revoked also deletes the token and the keys holding it.
func (y *Syncer) setStatus(ctx context.Context, link *store.PeerLink, state string) (*store.PeerLink, error) {
	now := time.Now().UTC()
	for _, id := range link.Services {
		if state == LinkRevoked {
			_ = y.Store.DeleteServiceKeys(ctx, id)
		} else {
			_ = y.Store.SetServiceKeysStatus(ctx, id, store.StatusDisabled, "share "+state+" by "+link.PeerName)
		}
	}
	link.Status, link.LastSyncAt, link.LastError = state, &now, ""
	if err := y.Store.SavePeerLinkState(ctx, link); err != nil {
		return nil, err
	}
	if state == LinkRevoked {
		if err := y.Store.ClearPeerLinkToken(ctx, link.ID, LinkRevoked); err != nil {
			return nil, err
		}
	}
	return y.Store.GetPeerLink(ctx, link.ID)
}

// Disconnect tells the owner (best effort), deletes the token and the keys
// holding it, and leaves the local services in place with no key.
func (y *Syncer) Disconnect(ctx context.Context, id int64) (*store.PeerLink, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	link, err := y.Store.GetPeerLink(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(link.TokenEnc) > 0 {
		if token, err := y.Box.Decrypt(link.TokenEnc); err == nil {
			lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := Leave(lctx, y.client(link.Trusted), link.PeerURL, token); err != nil {
				slog.Info("could not tell the owner about the disconnect", "peer", link.PeerID, "err", err)
			}
			cancel()
		}
	}
	for _, sid := range link.Services {
		_ = y.Store.DeleteServiceKeys(ctx, sid)
	}
	if err := y.Store.ClearPeerLinkToken(ctx, id, LinkDisconnected); err != nil {
		return nil, err
	}
	return y.Store.GetPeerLink(ctx, id)
}

// apply brings local services and providers in line with a manifest. New
// services are created; removed ones lose their key's use (disabled, not
// deleted, so events and app settings survive); status changes apply.
func (y *Syncer) apply(ctx context.Context, link *store.PeerLink, token string, m *Manifest) error {
	now := time.Now().UTC()
	var problems []string
	prevActive := link.Status == LinkActive
	status := m.Status
	switch status {
	case LinkActive, LinkPaused, LinkExpired, LinkRevoked:
	default:
		status = LinkActive
	}
	present := map[string]bool{}
	for _, ms := range m.Services {
		present[ms.Slug] = true
		if ms.Provider == providers.PeerSlug {
			continue // the owner must never share these; ignore if it does
		}
		if id, ok := link.Services[ms.Slug]; ok {
			if _, err := y.Store.GetService(ctx, id); err == nil {
				if status == LinkActive && (!prevActive || y.keysDisabled(ctx, id)) {
					_ = y.Store.SetServiceKeysStatus(ctx, id, store.StatusActive, "")
				}
				continue
			}
		}
		if status != LinkActive {
			continue
		}
		id, err := y.createService(ctx, link, ms, token)
		if err != nil {
			problems = append(problems, fmt.Sprintf("service %s: %v", ms.Slug, err))
			continue
		}
		link.Services[ms.Slug] = id
	}
	for slug, id := range link.Services {
		if !present[slug] || status != LinkActive {
			why := "no longer shared by " + link.PeerName
			if status != LinkActive {
				why = "share " + status + " by " + link.PeerName
			}
			_ = y.Store.SetServiceKeysStatus(ctx, id, store.StatusDisabled, why)
		}
	}
	imported := map[string]bool{}
	for _, p := range link.Providers {
		imported[p] = true
	}
	for _, mp := range m.Providers {
		if !mp.Shared || imported[mp.Slug] || status != LinkActive {
			continue
		}
		if err := y.importProvider(ctx, link, mp); err != nil {
			problems = append(problems, fmt.Sprintf("provider %s: %v", mp.Slug, err))
			continue
		}
		link.Providers = append(link.Providers, mp.Slug)
		imported[mp.Slug] = true
	}
	raw, _ := json.Marshal(m)
	link.Manifest = raw
	link.Status, link.LastSyncAt = status, &now
	link.LastError = strings.Join(problems, "; ")
	return y.Store.SavePeerLinkState(ctx, link)
}

func (y *Syncer) keysDisabled(ctx context.Context, serviceID int64) bool {
	keys, err := y.Store.ListKeys(ctx, serviceID, true)
	if err != nil {
		return false
	}
	for _, k := range keys {
		if k.Status == store.StatusDisabled || k.Status == store.StatusInvalid {
			return true
		}
	}
	return false
}

func (y *Syncer) createService(ctx context.Context, link *store.PeerLink, ms ManifestService, token string) (int64, error) {
	prov, err := y.Store.GetProvider(ctx, providers.PeerSlug)
	if err != nil {
		return 0, err
	}
	sv, err := catalog.ServiceDefaults(prov)
	if err != nil {
		return 0, err
	}
	slug := link.Prefix + "-" + ms.Slug
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	sv.Slug = catalog.UniqueServiceSlug(ctx, y.Store, slug)
	sv.Name = ms.Name
	sv.Category = ms.Category
	sv.Description = ms.Description
	sv.BaseURL = link.PeerURL + "/proxy/" + ms.Slug
	// The URL comes from an invite a superadmin confirmed, so the hidden
	// key (the share token) may be sent there.
	sv.TargetTrusted = true
	created, err := y.Store.CreateService(ctx, sv)
	if err != nil {
		return 0, err
	}
	if err := y.Store.SetServicePeer(ctx, created.ID, link.ID, ms.Slug); err != nil {
		return 0, err
	}
	enc, err := y.Box.Encrypt(token)
	if err != nil {
		return 0, err
	}
	if _, err := y.Store.CreateKey(ctx, store.NewKey{
		ServiceID: created.ID, Label: "Share from " + link.PeerName, SecretEnc: enc, SecretFP: y.Box.Fingerprint(token),
		Hint: crypto.Hint(token), Tier: "primary", Hidden: true, Weight: 1, Priority: 100,
		Notes: "The share token for " + link.PeerID + ". Managed on the Sharing page.",
	}); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// importProvider adds a shared provider spec as a local provider, unless one
// with that slug exists. It is imported once; later owner changes are not
// pulled in, so an owner cannot redirect keys this server adds to it.
func (y *Syncer) importProvider(ctx context.Context, link *store.PeerLink, mp ManifestProvider) error {
	if _, err := y.Store.GetProvider(ctx, mp.Slug); err == nil {
		return errors.New("a provider with that slug already exists here, skipped")
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if mp.Slug == providers.PeerSlug {
		return errors.New("cannot import the built in peer provider")
	}
	in := catalog.Input{Slug: mp.Slug, Name: mp.Name, Category: mp.Category, Description: mp.Description,
		Website: mp.Website, DocsURL: mp.DocsURL, Color: mp.Color, Status: "active", Spec: mp.Spec}
	_, _, err := y.Catalog.Publish(ctx, in, "peer:"+link.PeerID, "peer "+link.PeerName, fmt.Sprintf("imported from %s (v%d)", link.PeerName, mp.Version))
	return err
}
