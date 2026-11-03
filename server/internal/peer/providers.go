package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Provider specs from a peer. The receiver imports a provider the owner
// shared on purpose, and also the provider of every shared service it does
// not have (a dependency), so the service keeps that provider's name, logo,
// docs, examples and MCP tools. Each is imported once with source
// peer:<owner id> and never updated on its own: an owner that could change an
// imported spec could point the base URL somewhere else and collect keys this
// server later adds to it. A newer owner version is only reported; a
// superadmin pulls it with PullProvider. A provider this server already has
// is never touched.

// syncProviders imports what a manifest needs and records it. It returns the
// problems worth showing on the link.
func (y *Syncer) syncProviders(ctx context.Context, link *store.PeerLink, token string, m *Manifest) []string {
	var problems []string
	rows, err := y.Store.ListPeerProviders(ctx, link.ID)
	if err != nil {
		return []string{"providers: " + err.Error()}
	}
	known := map[string]*store.PeerProvider{}
	for _, r := range rows {
		known[r.Provider] = r
	}
	explicit := map[string]bool{}
	for _, p := range link.Providers {
		explicit[p] = true
	}
	// needs maps a provider slug to the owner's services built on it.
	needs := map[string][]string{}
	for _, ms := range m.Services {
		if ms.Provider != "" && ms.Provider != providers.PeerSlug {
			needs[ms.Provider] = append(needs[ms.Provider], ms.Slug)
		}
	}
	described := map[string]bool{}
	for _, mp := range m.Providers {
		described[mp.Slug] = true
		if mp.Slug == providers.PeerSlug {
			continue
		}
		forSvcs := needs[mp.Slug]
		if !mp.Shared && len(forSvcs) == 0 {
			continue
		}
		kind := store.PeerProviderDependency
		if mp.Shared {
			kind = store.PeerProviderShared
		}
		if row := known[mp.Slug]; row != nil {
			// Imported before through this link: only note the owner's newest
			// version and which services need it. The spec stays as it is.
			row.Kind, row.RemoteVersion, row.ForServices = kind, mp.Version, forSvcs
			if err := y.Store.SavePeerProvider(ctx, row); err != nil {
				problems = append(problems, fmt.Sprintf("provider %s: %v", mp.Slug, err))
			}
			if mp.Shared && !explicit[mp.Slug] {
				link.Providers = append(link.Providers, mp.Slug)
				explicit[mp.Slug] = true
			}
			y.ensureLogo(ctx, link, token, mp, false)
			continue
		}
		local, err := y.Store.GetProvider(ctx, mp.Slug)
		switch {
		case err == nil && explicit[mp.Slug] && local.Source == "peer:"+link.PeerID:
			// Imported by an older release that kept no row: adopt it, taking
			// the current versions as the baseline.
			if err := y.Store.SavePeerProvider(ctx, &store.PeerProvider{LinkID: link.ID, Provider: mp.Slug, Kind: kind, ForServices: forSvcs,
				ImportedVersion: mp.Version, RemoteVersion: mp.Version, LocalVersion: local.Version}); err != nil {
				problems = append(problems, fmt.Sprintf("provider %s: %v", mp.Slug, err))
			}
			continue
		case err == nil:
			// This server has its own provider with that slug: keep it. Only
			// an explicitly shared one is worth reporting; for a dependency the
			// local provider simply describes the service.
			if mp.Shared {
				problems = append(problems, fmt.Sprintf("provider %s: a provider with that slug already exists here, skipped", mp.Slug))
			}
			continue
		case !errors.Is(err, store.ErrNotFound):
			problems = append(problems, fmt.Sprintf("provider %s: %v", mp.Slug, err))
			continue
		}
		saved, err := y.importProvider(ctx, link, mp, "imported")
		if err != nil {
			what := "provider " + mp.Slug
			if !mp.Shared {
				what += " (for " + strings.Join(forSvcs, ", ") + ")"
			}
			problems = append(problems, fmt.Sprintf("%s: %v", what, err))
			continue
		}
		if err := y.Store.SavePeerProvider(ctx, &store.PeerProvider{LinkID: link.ID, Provider: mp.Slug, Kind: kind, ForServices: forSvcs,
			ImportedVersion: mp.Version, RemoteVersion: mp.Version, LocalVersion: saved.Version}); err != nil {
			problems = append(problems, fmt.Sprintf("provider %s: %v", mp.Slug, err))
		}
		if mp.Shared {
			link.Providers = append(link.Providers, mp.Slug)
			explicit[mp.Slug] = true
		}
		y.ensureLogo(ctx, link, token, mp, true)
	}
	// A shared service whose provider the owner did not describe still works
	// through the proxy; say why it shows no provider details.
	missing := []string{}
	for slug := range needs {
		if described[slug] {
			continue
		}
		if _, err := y.Store.GetProvider(ctx, slug); errors.Is(err, store.ErrNotFound) {
			missing = append(missing, slug)
		}
	}
	sort.Strings(missing)
	for _, slug := range missing {
		problems = append(problems, fmt.Sprintf("provider %s (for %s): the owner sent no spec", slug, strings.Join(needs[slug], ", ")))
	}
	return problems
}

// importProvider publishes an owner's provider spec as a local provider.
func (y *Syncer) importProvider(ctx context.Context, link *store.PeerLink, mp ManifestProvider, verb string) (*store.Provider, error) {
	if mp.Slug == providers.PeerSlug {
		return nil, errors.New("cannot import the built in peer provider")
	}
	in := catalog.Input{Slug: mp.Slug, Name: mp.Name, Category: mp.Category, Description: mp.Description,
		Website: mp.Website, DocsURL: mp.DocsURL, Color: mp.Color, Status: "active", Spec: mp.Spec}
	p, _, err := y.Catalog.Publish(ctx, in, "peer:"+link.PeerID, "peer "+link.PeerName, fmt.Sprintf("%s from %s (v%d)", verb, link.PeerName, mp.Version))
	return p, err
}

// ensureLogo gives an imported provider a logo when it has none: the owner's
// own logo through the share, else (on first import only) the one the
// catalog finds on the provider's website. Best effort; failures are logged.
func (y *Syncer) ensureLogo(ctx context.Context, link *store.PeerLink, token string, mp ManifestProvider, firstImport bool) {
	if _, _, err := y.Store.ProviderLogo(ctx, mp.Slug); !errors.Is(err, store.ErrNotFound) {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, mime, err := FetchProviderLogo(lctx, y.client(link.Trusted), link.PeerURL, token, mp.Slug)
	if err != nil && firstImport && mp.Website != "" && y.FetchSiteLogo != nil {
		data, mime, err = y.FetchSiteLogo(lctx, mp.Website)
	}
	if err != nil {
		slog.Info("no logo for imported provider", "provider", mp.Slug, "peer", link.PeerID, "err", err)
		return
	}
	if err := y.Store.SetProviderLogo(ctx, mp.Slug, data, mime); err != nil {
		slog.Warn("could not save imported provider logo", "provider", mp.Slug, "err", err)
	}
}

// SiteLogo finds a logo on a provider's website with the catalog's logo
// discovery (public addresses only, no token).
func SiteLogo(ctx context.Context, site string) ([]byte, string, error) {
	data, mime, _, err := catalog.FetchLogo(ctx, site)
	return data, mime, err
}

// BaseURLChangeError means pulling an update would send this provider's
// requests, and the keys added here, to a different base URL.
type BaseURLChangeError struct{ From, To string }

func (e *BaseURLChangeError) Error() string {
	return fmt.Sprintf("the update changes the base URL from %s to %s", e.From, e.To)
}

// ErrEditedHere means the imported provider was edited on this server since
// the import, so an update would overwrite those edits.
var ErrEditedHere = errors.New("this provider was edited on this server since the import; update it by hand")

// PullProvider updates an imported provider to the owner's current spec. It
// is only ever a superadmin's explicit action. When the base URL would
// change, allowBaseURL must be set (the person saw the new URL).
func (y *Syncer) PullProvider(ctx context.Context, linkID int64, slug string, allowBaseURL bool) (*store.PeerProvider, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	link, err := y.Store.GetPeerLink(ctx, linkID)
	if err != nil {
		return nil, err
	}
	row, err := y.Store.GetPeerProvider(ctx, linkID, slug)
	if err != nil {
		return nil, err
	}
	if len(link.TokenEnc) == 0 || link.Status != LinkActive {
		return nil, errors.New("this connection is not active")
	}
	local, err := y.Store.GetProvider(ctx, slug)
	if err != nil {
		return nil, err
	}
	if local.Version != row.LocalVersion || local.Source != "peer:"+link.PeerID {
		return nil, ErrEditedHere
	}
	token, err := y.Box.Decrypt(link.TokenEnc)
	if err != nil {
		return nil, err
	}
	m, err := FetchManifest(ctx, y.client(link.Trusted), link.PeerURL, token)
	if err != nil {
		return nil, err
	}
	var mp *ManifestProvider
	for i := range m.Providers {
		if m.Providers[i].Slug == slug {
			mp = &m.Providers[i]
		}
	}
	if mp == nil {
		return nil, errors.New("the owner no longer sends this provider")
	}
	if !allowBaseURL {
		if from, to := specBaseURL(local.Spec), specBaseURL(mp.Spec); from != to {
			return nil, &BaseURLChangeError{From: from, To: to}
		}
	}
	saved, err := y.importProvider(ctx, link, *mp, "updated")
	if err != nil {
		return nil, err
	}
	row.ImportedVersion, row.RemoteVersion, row.LocalVersion = mp.Version, mp.Version, saved.Version
	if err := y.Store.SavePeerProvider(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

func specBaseURL(raw json.RawMessage) string {
	s, err := providers.DecodeSpec(raw)
	if err != nil {
		return ""
	}
	return s.BaseURL
}
