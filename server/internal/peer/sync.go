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
	// FetchSiteLogo finds a logo on a provider's website, the fallback when
	// the owner serves none for an imported provider. Nil skips it.
	FetchSiteLogo func(ctx context.Context, site string) ([]byte, string, error)

	// OnHandover hears about handed over keys arriving or being deleted, for
	// audit and webhooks. Nil is fine.
	OnHandover func(ctx context.Context, ev HandoverEvent)
	// CheckHost vets the host of an owner's base URL for handed over keys on
	// a link that is not on a private network. Nil uses PublicHost.
	CheckHost func(ctx context.Context, host string) error

	// mu lets one sync at a time change local state, so two cannot both
	// create a service. It is never held across a call to another server:
	// a slow or hostile owner must not block disconnects, revoke pushes or
	// the other links.
	mu sync.Mutex

	// logoMu guards logoTried, when each imported provider's logo was last
	// asked for, so a missing logo is not fetched again on every sync.
	logoMu    sync.Mutex
	logoTried map[string]time.Time
}

// Limits on what one owner can make a receiver do in a sync.
const (
	// maxManifestEntries caps the services and the providers taken from one
	// manifest.
	maxManifestEntries = 200
	// callTimeout bounds each call to the owner.
	callTimeout = 20 * time.Second
	// logoBudget bounds all logo fetches of one sync, maxLogoFetches their
	// number, logoTimeout each one, and logoRetry how soon a missing logo is
	// asked for again.
	logoBudget     = 60 * time.Second
	maxLogoFetches = 20
	logoTimeout    = 10 * time.Second
	logoRetry      = 24 * time.Hour
)

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
	// Only the owner-bound message is ever signed: an older one could be
	// passed on to another server. An owner that cannot check it must
	// upgrade first.
	if owner.Redeem < RedeemVersion {
		return nil, errors.New("the owner's server runs an older release that cannot check this server's signature; ask its owner to upgrade")
	}
	nb := make([]byte, 16)
	_, _ = rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	req := RedeemRequest{
		Invite: inv.Secret, ShareID: inv.ShareID, ID: self.ID, Name: self.Name, URL: self.URL,
		PublicKey: self.PublicKey, Nonce: nonce, BoxPublicKey: self.BoxPublicKey, OwnerID: owner.ID,
		Signature: SignRedeemV3(priv, owner.ID, inv.ShareID, InviteHash(inv.Secret), nonce, self.ID, self.BoxPublicKey),
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
		Mode: res.Manifest.Mode,
	}, userID)
	if err != nil {
		return nil, err
	}
	if err := y.apply(ctx, link, res.Token, &res.Manifest); err != nil {
		return link, err
	}
	return y.Store.GetPeerLink(ctx, link.ID)
}

// Sync refreshes one link from its owner.
func (y *Syncer) Sync(ctx context.Context, id int64) (*store.PeerLink, error) {
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
	// Ask the owner without the lock.
	mctx, cancel := context.WithTimeout(ctx, callTimeout)
	m, err := FetchManifest(mctx, y.client(link.Trusted), link.PeerURL, token)
	cancel()
	if err != nil {
		y.mu.Lock()
		defer y.mu.Unlock()
		// The link may have changed while the owner answered.
		cur, lerr := y.Store.GetPeerLink(ctx, id)
		if lerr != nil {
			return nil, lerr
		}
		if len(cur.TokenEnc) == 0 {
			return cur, nil
		}
		link = cur
		now := time.Now().UTC()
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
	if link.Mode == ModeHandover {
		// Pause, expiry and revoke all delete handed over keys; resume hands
		// them over again.
		y.wipeLink(ctx, link, "share "+state+" by "+link.PeerName)
	}
	for _, id := range link.Services {
		if link.Mode == ModeHandover {
			break
		}
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
	link, err := y.Store.GetPeerLink(ctx, id)
	if err != nil {
		y.mu.Unlock()
		return nil, err
	}
	// Handed over keys go before the owner hears about it, so "left" on
	// the owner means they are gone.
	if link.Mode == ModeHandover {
		y.wipeLink(ctx, link, WipeDisconnected)
	}
	token := ""
	if len(link.TokenEnc) > 0 {
		token, _ = y.Box.Decrypt(link.TokenEnc)
	}
	if link.Mode != ModeHandover {
		for _, sid := range link.Services {
			_ = y.Store.DeleteServiceKeys(ctx, sid)
		}
	}
	err = y.Store.ClearPeerLinkToken(ctx, id, LinkDisconnected)
	y.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// Tell the owner (best effort) once nothing here waits on the answer.
	if token != "" {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := Leave(lctx, y.client(link.Trusted), link.PeerURL, token); err != nil {
			slog.Info("could not tell the owner about the disconnect", "peer", link.PeerID, "err", err)
		}
		cancel()
	}
	return y.Store.GetPeerLink(ctx, id)
}

// manifestStatus is the link status a manifest asks for.
func manifestStatus(m *Manifest) string {
	switch m.Status {
	case LinkActive, LinkPaused, LinkExpired, LinkRevoked:
		return m.Status
	}
	return LinkActive
}

// handoverFetch is the owner's answer for a handover link's keys, fetched
// before the lock is taken.
type handoverFetch struct {
	keys HandoverKeys
	err  error
}

// prefetch asks the owner for a handover link's keys when the manifest
// says the share is active. It holds no lock.
func (y *Syncer) prefetch(ctx context.Context, link *store.PeerLink, token string, m *Manifest) *handoverFetch {
	if link.Mode != ModeHandover || manifestStatus(m) != LinkActive {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	hk, err := FetchHandoverKeys(hctx, y.client(link.Trusted), link.PeerURL, token)
	return &handoverFetch{keys: hk, err: err}
}

// syncFollowUp is the network work left after a manifest is applied: logos
// to fetch and, for a handover link, the usage report.
type syncFollowUp struct {
	logos  []logoJob
	report bool
}

// apply brings a link in line with a manifest. It asks the owner for what
// the manifest needs first, takes the lock only for the local changes, and
// does the rest of the network work after letting go.
func (y *Syncer) apply(ctx context.Context, link *store.PeerLink, token string, m *Manifest) error {
	pre := y.prefetch(ctx, link, token, m)
	y.mu.Lock()
	cur, err := y.Store.GetPeerLink(ctx, link.ID)
	if err != nil {
		y.mu.Unlock()
		return err
	}
	if cur.Status == LinkDisconnected || cur.Status == LinkRevoked {
		// Disconnected or revoked while the owner answered: leave it so.
		y.mu.Unlock()
		return nil
	}
	*link = *cur
	fu, err := y.applyLocked(ctx, link, token, m, pre)
	y.mu.Unlock()
	if fu.report {
		y.report(ctx, link, token)
	}
	y.fetchLogos(ctx, link, token, fu.logos)
	return err
}

// applyLocked brings local services and providers in line with a manifest.
// New services are created; removed ones lose their key's use (disabled,
// not deleted, so events and app settings survive); status changes apply.
// The caller holds y.mu; nothing here calls the owner.
func (y *Syncer) applyLocked(ctx context.Context, link *store.PeerLink, token string, m *Manifest, pre *handoverFetch) (syncFollowUp, error) {
	now := time.Now().UTC()
	var problems []string
	var fu syncFollowUp
	prevActive := link.Status == LinkActive
	status := manifestStatus(m)
	if len(m.Services) > maxManifestEntries {
		problems = append(problems, fmt.Sprintf("the owner shares %d services; only the first %d are taken", len(m.Services), maxManifestEntries))
		m.Services = m.Services[:maxManifestEntries]
	}
	if len(m.Providers) > maxManifestEntries {
		problems = append(problems, fmt.Sprintf("the owner sent %d providers; only the first %d are taken", len(m.Providers), maxManifestEntries))
		m.Providers = m.Providers[:maxManifestEntries]
	}
	// Providers first, so a service made below already has its provider's
	// name and examples. A failed import never blocks the service: it works
	// through the owner's proxy either way.
	if status == LinkActive {
		var p []string
		p, fu.logos = y.syncProviders(ctx, link, m)
		problems = append(problems, p...)
	}
	if link.Mode == ModeHandover {
		var p []string
		p, fu.report = y.applyHandover(ctx, link, status, pre)
		problems = append(problems, p...)
		return fu, y.saveApplied(ctx, link, m, status, now, problems)
	}
	present := map[string]bool{}
	for _, ms := range m.Services {
		present[ms.Slug] = true
		if ms.Provider == providers.PeerSlug {
			continue // the owner must never share these; ignore if it does
		}
		if id, ok := link.Services[ms.Slug]; ok {
			if sv, err := y.Store.GetService(ctx, id); err == nil {
				if sv.PeerProvider != ms.Provider {
					_ = y.Store.SetServicePeerProvider(ctx, id, ms.Provider)
				}
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
	return fu, y.saveApplied(ctx, link, m, status, now, problems)
}

func (y *Syncer) saveApplied(ctx context.Context, link *store.PeerLink, m *Manifest, status string, now time.Time, problems []string) error {
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
	if !catalog.ValidSlug(ms.Slug) {
		return 0, errors.New("the owner sent an invalid service slug")
	}
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
	if err := y.Store.SetServicePeerProvider(ctx, created.ID, ms.Provider); err != nil {
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
