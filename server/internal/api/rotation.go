package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Provider rotation: a service with rotation on shares its requests with the
// other services, also with rotation on, that serve the same endpoint. Two
// services serve the same endpoint when
//
//   - the request picks a catalog entry (/~amazon-scraper/products/details,
//     or any path on a service whose base URL is that entry's host) and the
//     other service's provider lists an entry of the same name: Omkar Cloud's
//     amazon-scraper and the amazon-scraper of a self-hosted server that runs
//     the same open source scraper, or
//   - the request is on a plain base URL and the other service is of the
//     same provider, also on a plain base URL: two accounts or two servers
//     of one API.
//
// Each candidate gets the path in its own form, so a request made as
// /proxy/omkar-amazon/products/details can be served by another service as
// ~amazon-scraper/products/details. Keys still rotate inside each service;
// rotation adds the step of moving on to another provider when one has no
// key, is out of quota, rate limited or failing.

const (
	RotationFallback   = "fallback"    // this service first, the others when it cannot serve
	RotationRoundRobin = "round_robin" // each request starts at the next service
)

// proxyTarget is one service a proxied request may go to, with the path it
// takes there (no leading slash, as the proxy keeps it).
type proxyTarget struct {
	sv   *store.Service
	path string
}

func singleTargets(svcs []*store.Service, path string) []proxyTarget {
	out := make([]proxyTarget, len(svcs))
	for i, sv := range svcs {
		out[i] = proxyTarget{sv: sv, path: path}
	}
	return out
}

// normBase is a base URL compared by scheme, host and path.
func normBase(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/")
}

// baseEntry names the catalog entry whose host is the service's base URL:
// an Omkar Amazon Scraper service at https://amazon-scraper.omkar.cloud is
// on entry amazon-scraper. Empty for a service on the provider's own base.
func baseEntry(sv *store.Service) string {
	b := normBase(sv.BaseURL)
	if b == "" {
		return ""
	}
	for _, h := range providers.Get(sv.Provider).Hosts {
		if !h.Relative() && normBase(h.URL) == b {
			return h.Name
		}
	}
	return ""
}

// rotationEntry says which endpoint a request on sv reaches: the catalog
// entry (empty when none) and the path inside it. ok is false for a path
// that picks no entry the provider lists.
func rotationEntry(sv *store.Service, path string) (entry, rest string, ok bool) {
	h, r, picked, err := providers.Get(sv.Provider).ResolveHost(path)
	if err != nil {
		return "", "", false
	}
	if picked {
		return h.Name, strings.TrimPrefix(r, "/"), true
	}
	return baseEntry(sv), path, true
}

// rotationPath is the path that reaches entry/rest on service c, and
// whether c serves it at all. origin is the service the request named.
func rotationPath(origin, c *store.Service, entry, rest string) (string, bool) {
	own := baseEntry(c)
	if entry == "" {
		// A plain endpoint: only another service of the same provider, also
		// on a plain base URL, has it.
		if c.Provider != origin.Provider || own != "" {
			return "", false
		}
		return rest, true
	}
	if own != "" {
		// A service on one entry's host serves that entry alone.
		if own != entry {
			return "", false
		}
		return rest, true
	}
	if _, ok := providers.Get(c.Provider).Host(entry); !ok {
		return "", false
	}
	p := providers.HostMarker + entry
	if rest != "" {
		p += "/" + rest
	}
	return p, true
}

// rotating caches the services with rotation on for a few seconds: the
// proxy reads them on every request to such a service.
type rotating struct {
	mu   sync.Mutex
	at   time.Time
	data []*store.Service
}

const rotatingTTL = 3 * time.Second

func (s *Server) rotatingServices(ctx context.Context) []*store.Service {
	rc := &s.rotating
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.data != nil && time.Since(rc.at) < rotatingTTL {
		return rc.data
	}
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		if rc.data != nil {
			return rc.data
		}
		return nil
	}
	out := []*store.Service{}
	for _, sv := range svcs {
		if sv.Rotation != "" {
			out = append(out, sv)
		}
	}
	rc.data, rc.at = out, time.Now()
	return out
}

// forgetRotating drops the cache after a service changes.
func (s *Server) forgetRotating() {
	s.rotating.mu.Lock()
	s.rotating.data = nil
	s.rotating.mu.Unlock()
}

// rotationPeers lists the other services that serve the same endpoint as
// path on sv, each with its own path. canUse filters them (nil allows all).
func (s *Server) rotationPeers(ctx context.Context, sv *store.Service, path string, canUse func(*store.Service) bool) []proxyTarget {
	if sv.PeerLinkID != nil || sv.Provider == providers.PeerSlug {
		return nil
	}
	entry, rest, ok := rotationEntry(sv, path)
	if !ok {
		return nil
	}
	var out []proxyTarget
	for _, c := range s.rotatingServices(ctx) {
		if c.ID == sv.ID || c.PeerLinkID != nil || c.Provider == providers.PeerSlug {
			continue
		}
		if canUse != nil && !canUse(c) {
			continue
		}
		if p, ok := rotationPath(sv, c, entry, rest); ok {
			out = append(out, proxyTarget{sv: c, path: p})
		}
	}
	return out
}

// rotationTargets is where a request to one service may go: the service
// alone, or with rotation on, it and the services that serve the same
// endpoint, in the order the rotation mode picks.
func (s *Server) rotationTargets(r *http.Request, a *store.App, sv *store.Service, path string) []proxyTarget {
	self := []proxyTarget{{sv: sv, path: path}}
	// A share reaches only what was shared, and a capability route only its
	// own service.
	if sv.Rotation == "" || a.ShareID != nil {
		return self
	}
	ctx := r.Context()
	peers := s.rotationPeers(ctx, sv, path, func(c *store.Service) bool { return a.CanUse(c.Slug) })
	if len(peers) == 0 {
		return self
	}
	entry, rest, _ := rotationEntry(sv, path)
	counter := "rotate:" + sv.Provider + "|" + entry
	if entry == "" {
		counter += "|" + rest
	}
	all := append(self, peers...)
	cands := make([]routeCand, len(all))
	for i, t := range all {
		cands[i] = s.candidate(ctx, i, t.sv, 1, r.Method, t.path)
	}
	var ordered []routeCand
	if sv.Rotation == RotationRoundRobin {
		ordered = orderRoutes(counter, StrategyRoundRobin, cands)
	} else {
		// This service first; the others take turns behind it.
		rest := orderRoutes(counter, StrategyRoundRobin, cands[1:])
		ordered = orderRoutes(counter, StrategyPriority, append([]routeCand{cands[0]}, rest...))
	}
	out := make([]proxyTarget, len(ordered))
	for i, c := range ordered {
		out[i] = all[c.idx]
	}
	return out
}

// rotationMatch is a service that serves some of the same endpoints as
// another: the entries they share, or the plain base URL ("") of one
// provider.
type rotationMatch struct {
	ID       int64    `json:"id"`
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Rotation string   `json:"rotation"`
	Keys     int      `json:"keys"`
	Shared   int      `json:"shared"`
	Entries  []string `json:"entries"`
}

// rotationMatches lists the services that serve endpoints sv serves, with
// or without rotation on: every entry of sv's catalog (or its own entry, or
// its plain base URL) checked against each other service.
func rotationMatches(sv *store.Service, svcs []*store.Service) []rotationMatch {
	if sv.PeerLinkID != nil || sv.Provider == providers.PeerSlug {
		return []rotationMatch{}
	}
	var entries []string
	if own := baseEntry(sv); own != "" {
		entries = []string{own}
	} else {
		entries = []string{""}
		for _, h := range providers.Get(sv.Provider).Hosts {
			entries = append(entries, h.Name)
		}
	}
	out := []rotationMatch{}
	for _, c := range svcs {
		if c.ID == sv.ID || c.PeerLinkID != nil || c.Provider == providers.PeerSlug {
			continue
		}
		m := rotationMatch{ID: c.ID, Slug: c.Slug, Name: c.Name, Provider: c.Provider, Rotation: c.Rotation, Keys: c.KeyCounts.Available, Entries: []string{}}
		for _, e := range entries {
			if _, ok := rotationPath(sv, c, e, ""); ok {
				m.Shared++
				if len(m.Entries) < 12 {
					m.Entries = append(m.Entries, e)
				}
			}
		}
		if m.Shared > 0 {
			out = append(out, m)
		}
	}
	return out
}

// serviceRotation is GET /services/{id}/rotation: the service's rotation
// mode and the services that serve the same endpoints.
func (s *Server) serviceRotation(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	svcs, err := s.Store.ListServices(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	_ = s.Store.FillStats(r.Context(), svcs, isSuper(r), nil)
	writeJSON(w, 200, map[string]any{"service": sv.Slug, "rotation": sv.Rotation, "matches": rotationMatches(sv, svcs)})
}

// setServicesRotation is POST /services/rotation {services, rotation}: one
// rotation mode for several services, by id or slug.
func (s *Server) setServicesRotation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Services []any  `json:"services"`
		Rotation string `json:"rotation"`
	}
	if !decode(w, r, &in) {
		return
	}
	mode := strings.TrimSpace(in.Rotation)
	if mode == "off" {
		mode = ""
	}
	if !validRotation[mode] {
		writeErr(w, 400, "invalid", "rotation must be off, fallback or round_robin")
		return
	}
	if len(in.Services) == 0 || len(in.Services) > 500 {
		writeErr(w, 400, "invalid", "list 1 to 500 services")
		return
	}
	var svcs []*store.Service
	for _, ref := range in.Services {
		var sv *store.Service
		var err error
		switch v := ref.(type) {
		case float64:
			sv, err = s.Store.GetService(r.Context(), int64(v))
		case string:
			sv, err = s.Store.GetServiceBySlug(r.Context(), strings.TrimSpace(v))
		default:
			err = store.ErrNotFound
		}
		if err != nil {
			writeErr(w, 404, "not_found", "no service "+strings.TrimSpace(fmtAny(ref)))
			return
		}
		svcs = append(svcs, sv)
	}
	changed := []string{}
	for _, sv := range svcs {
		if sv.Rotation == mode {
			continue
		}
		sv.Rotation = mode
		if err := s.Store.UpdateService(r.Context(), sv); err != nil {
			writeStoreErr(w, err)
			return
		}
		changed = append(changed, sv.Slug)
	}
	s.forgetRotating()
	if len(changed) > 0 {
		s.Store.AddAudit(r.Context(), userFrom(r), "service.rotation", strings.Join(changed, ","), orDefault(mode, "off"))
	}
	writeJSON(w, 200, map[string]any{"ok": true, "rotation": orDefault(mode, "off"), "changed": changed})
}

func fmtAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return "?"
}
