package catalog

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// CanShareKeys reports whether svc may use source's keys: both send the key
// the same way (auth type and name) to the same site (providers.HostSite of
// their base URLs, such as omkar.cloud), neither is a service shared by
// another server, and they are not the same service. The pool checks it
// again on every pick, so a base URL changed after linking stops the
// sharing instead of moving keys to a new host.
func CanShareKeys(svc, source *store.Service) bool {
	if svc == nil || source == nil || svc.ID == source.ID {
		return false
	}
	if svc.PeerLinkID != nil || source.PeerLinkID != nil || svc.Provider == providers.PeerSlug || source.Provider == providers.PeerSlug {
		return false
	}
	if svc.AuthType != source.AuthType || svc.AuthName != source.AuthName {
		return false
	}
	site := shareSite(svc.BaseURL)
	return site != "" && site == shareSite(source.BaseURL)
}

// shareSite is the site keys may be shared within: providers.HostSite for a
// named host, or the exact host and port for an IP address or a name with
// no dot, where nothing ties two ports to one owner.
func shareSite(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	h := u.Hostname()
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return strings.ToLower(u.Host)
	}
	return providers.HostSite(base)
}

// KeySourceProblem says why svc cannot use source's keys, or "" when it can.
func KeySourceProblem(svc, source *store.Service) string {
	switch {
	case svc.ID == source.ID:
		return "a service cannot use its own keys as a source"
	case source.KeySourceID != nil:
		return fmt.Sprintf("%s takes its keys from %s; link to that service instead", source.Slug, source.KeySourceSlug)
	case svc.PeerLinkID != nil || source.PeerLinkID != nil:
		return "services shared by another server cannot share keys"
	case svc.AuthType != source.AuthType || svc.AuthName != source.AuthName:
		return fmt.Sprintf("%s sends its key as %s %s and %s as %s %s, so one key would not fit both",
			svc.Slug, svc.AuthType, svc.AuthName, source.Slug, source.AuthType, source.AuthName)
	case shareSite(svc.BaseURL) == "" || shareSite(svc.BaseURL) != shareSite(source.BaseURL):
		return fmt.Sprintf("%s goes to %s and %s to %s; keys are shared only within one site",
			svc.Slug, shareSite(svc.BaseURL), source.Slug, shareSite(source.BaseURL))
	}
	return ""
}

// KeySourceFor picks the service svc should take keys from: among the
// services it may share with that have keys of their own and no source of
// their own, the one with the most keys (ties go to the oldest). Nil when
// there is none.
func KeySourceFor(ctx context.Context, st *store.Store, svc *store.Service) (*store.Service, error) {
	svcs, err := st.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	if err := st.FillStats(ctx, svcs, false, nil); err != nil {
		return nil, err
	}
	var best *store.Service
	for _, c := range svcs {
		if c.KeySourceID != nil || c.KeyCounts.Total-c.KeyCounts.Shared == 0 || !CanShareKeys(svc, c) {
			continue
		}
		if best == nil || c.KeyCounts.Total > best.KeyCounts.Total || (c.KeyCounts.Total == best.KeyCounts.Total && c.ID < best.ID) {
			best = c
		}
	}
	return best, nil
}

// AutoLinkKeys links a service that has no keys yet to the service of the
// same account that has them, so a new service for another API of a
// provider already in the pool works at once. It returns the source, or nil
// when it linked nothing.
func AutoLinkKeys(ctx context.Context, st *store.Store, svc *store.Service) (*store.Service, error) {
	if svc.KeySourceID != nil {
		return nil, nil
	}
	own, err := st.ListKeys(ctx, svc.ID, true)
	if err != nil || len(own) > 0 {
		return nil, err
	}
	src, err := KeySourceFor(ctx, st, svc)
	if err != nil || src == nil {
		return nil, err
	}
	if err := st.SetKeySource(ctx, svc.ID, &src.ID); err != nil {
		return nil, err
	}
	st.AddAudit(ctx, nil, "service.key_source", svc.Slug, "uses the keys of "+src.Slug+" (same account, linked automatically)")
	return src, nil
}
