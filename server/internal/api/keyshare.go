package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Shared keys (store/keyshare.go, catalog/keyshare.go): a service can use the
// keys of another service of the same account, and keys with the same secret
// in several services are shown as twins.

// setKeySource is PUT /api/admin/services/{id}/key-source with
// {"source": "<slug or id>"} to link, or {"source": null} to unlink.
func (s *Server) setKeySource(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var in struct {
		Source *string `json:"source"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Source == nil || strings.TrimSpace(*in.Source) == "" {
		if err := s.Store.SetKeySource(r.Context(), sv.ID, nil); err != nil {
			writeStoreErr(w, err)
			return
		}
		s.Store.AddAudit(r.Context(), userFrom(r), "service.key_source", sv.Slug, "uses only its own keys")
		s.writeService(w, r, sv.ID)
		return
	}
	src, err := s.findService(r.Context(), *in.Source)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	if msg := catalog.KeySourceProblem(sv, src); msg != "" {
		writeErr(w, 400, "cannot_share_keys", msg)
		return
	}
	if err := s.Store.SetKeySource(r.Context(), sv.ID, &src.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	// Services that took their keys from this one now take them from src,
	// so links never chain.
	if err := s.relinkBorrowers(r.Context(), sv, src); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.key_source", sv.Slug, "uses the keys of "+src.Slug)
	s.writeService(w, r, sv.ID)
}

func (s *Server) relinkBorrowers(ctx context.Context, sv, src *store.Service) error {
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return err
	}
	for _, b := range svcs {
		if b.KeySourceID == nil || *b.KeySourceID != sv.ID {
			continue
		}
		if b.ID == src.ID || !catalog.CanShareKeys(b, src) {
			if err := s.Store.SetKeySource(ctx, b.ID, nil); err != nil {
				return err
			}
			continue
		}
		if err := s.Store.SetKeySource(ctx, b.ID, &src.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) writeService(w http.ResponseWriter, r *http.Request, id int64) {
	sv, err := s.Store.GetService(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	_ = s.Store.FillStats(r.Context(), []*store.Service{sv}, isSuper(r), s.Pool.InFlight())
	s.fillLogos(r.Context(), sv)
	writeJSON(w, 200, sv)
}

// sharedKeysReport is GET /api/admin/keys/shared: secrets stored in more
// than one service, and services with no keys that could use the keys of a
// service of the same account.
func (s *Server) sharedKeysReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dups, err := s.Store.DuplicateKeyGroups(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	suggestions, err := s.keySourceSuggestions(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"duplicates": dups, "suggestions": suggestions})
}

type keySourceSuggestion struct {
	Service     string `json:"service"`
	ServiceName string `json:"service_name"`
	Source      string `json:"source"`
	SourceName  string `json:"source_name"`
	SourceKeys  int    `json:"source_keys"`
}

// keySourceSuggestions lists services that have no keys and no source but
// could use the keys of another service of the same account.
func (s *Server) keySourceSuggestions(ctx context.Context) ([]keySourceSuggestion, error) {
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Store.FillStats(ctx, svcs, false, nil); err != nil {
		return nil, err
	}
	out := []keySourceSuggestion{}
	for _, sv := range svcs {
		if sv.KeySourceID != nil || sv.KeyCounts.Total > 0 {
			continue
		}
		src, err := catalog.KeySourceFor(ctx, s.Store, sv)
		if err != nil {
			return nil, err
		}
		if src != nil {
			out = append(out, keySourceSuggestion{Service: sv.Slug, ServiceName: sv.Name, Source: src.Slug, SourceName: src.Name,
				SourceKeys: src.KeyCounts.Total - src.KeyCounts.Shared})
		}
	}
	return out, nil
}

// linkAllKeySources links every suggested service, for POST
// /api/admin/keys/shared/link. It returns what it linked.
func (s *Server) linkAllKeySources(w http.ResponseWriter, r *http.Request) {
	sugg, err := s.keySourceSuggestions(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	linked := []keySourceSuggestion{}
	for _, sg := range sugg {
		sv, err1 := s.Store.GetServiceBySlug(r.Context(), sg.Service)
		src, err2 := s.Store.GetServiceBySlug(r.Context(), sg.Source)
		if err := errors.Join(err1, err2); err != nil || catalog.KeySourceProblem(sv, src) != "" {
			continue
		}
		if err := s.Store.SetKeySource(r.Context(), sv.ID, &src.ID); err != nil {
			writeStoreErr(w, err)
			return
		}
		s.Store.AddAudit(r.Context(), userFrom(r), "service.key_source", sv.Slug, "uses the keys of "+src.Slug)
		linked = append(linked, sg)
	}
	writeJSON(w, 200, map[string]any{"linked": linked})
}

// sharedKeysOf lists the key source's keys a linked service uses, for the
// service's key list: visible to everyone who can see the service, so
// hidden and app restricted keys are left out, as the pool leaves them out.
func (s *Server) sharedKeysOf(ctx context.Context, sv *store.Service, own []*store.Key) []*store.Key {
	if sv.KeySourceID == nil {
		return []*store.Key{}
	}
	src, err := s.Store.GetService(ctx, *sv.KeySourceID)
	if err != nil || !catalog.CanShareKeys(sv, src) {
		return []*store.Key{}
	}
	keys, err := s.Store.ListKeys(ctx, src.ID, false)
	if err != nil {
		return []*store.Key{}
	}
	twin := map[int64]bool{}
	for _, k := range own {
		for _, t := range k.Twins {
			twin[t.KeyID] = true
		}
	}
	out := []*store.Key{}
	for _, k := range keys {
		if !k.Restricted && !twin[k.ID] {
			out = append(out, k)
		}
	}
	return out
}

// serviceFamily is the services one account's key serves: the same site and
// the same auth placement, such as every Omkar Cloud API. The catalog shows
// them as one card.
type serviceFamily struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// MainID is the service the others take keys from: the one with the
	// most keys of its own (the oldest on a tie), or the source they are
	// already linked to.
	MainID     int64   `json:"main_id"`
	ServiceIDs []int64 `json:"service_ids"`
	// Keys and Available count distinct secrets, so one key stored in two
	// services of the family counts once.
	Keys      int `json:"keys"`
	Available int `json:"available"`
	// Unlinked counts members that could use the main service's keys but
	// do not yet.
	Unlinked int `json:"unlinked"`
}

// serviceFamilies groups services by catalog.FamilyKey. Only groups of two
// or more are families.
func (s *Server) serviceFamilies(ctx context.Context, svcs []*store.Service, includeHidden bool) []serviceFamily {
	groups := map[string][]*store.Service{}
	order := []string{}
	for _, sv := range svcs {
		k := catalog.FamilyKey(sv)
		if k == "" {
			continue
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], sv)
	}
	out := []serviceFamily{}
	for _, k := range order {
		members := groups[k]
		if len(members) < 2 {
			continue
		}
		main := familyMain(members)
		f := serviceFamily{Key: k, Name: familyName(members, k), MainID: main.ID}
		ids := []int64{}
		for _, m := range members {
			f.ServiceIDs = append(f.ServiceIDs, m.ID)
			ids = append(ids, m.ID)
			if m.ID != main.ID && m.KeySourceID == nil && catalog.KeySourceProblem(m, main) == "" && main.KeyCounts.Total-main.KeyCounts.Shared > 0 {
				f.Unlinked++
			}
		}
		f.Keys, f.Available, _ = s.Store.DistinctKeyStats(ctx, ids, includeHidden)
		out = append(out, f)
	}
	return out
}

// familyMain picks the family's main service: the source most members are
// linked to, else the one with the most keys of its own, the oldest on a tie.
func familyMain(members []*store.Service) *store.Service {
	links := map[int64]int{}
	for _, m := range members {
		if m.KeySourceID != nil {
			links[*m.KeySourceID]++
		}
	}
	var best *store.Service
	score := func(sv *store.Service) (int, int) { return links[sv.ID], sv.KeyCounts.Total - sv.KeyCounts.Shared }
	for _, m := range members {
		if m.KeySourceID != nil {
			continue
		}
		if best == nil {
			best = m
			continue
		}
		bl, bk := score(best)
		ml, mk := score(m)
		if ml > bl || (ml == bl && mk > bk) || (ml == bl && mk == bk && m.ID < best.ID) {
			best = m
		}
	}
	if best == nil {
		best = members[0]
	}
	return best
}

// linkFamily is POST /services/families/link with {key}: every member that
// can, uses the main service's keys. Members keep their own keys, which go
// first; members linked elsewhere are moved to the main service.
func (s *Server) linkFamily(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	svcs, err := s.Store.ListServices(ctx)
	if err == nil {
		err = s.Store.FillStats(ctx, svcs, isSuper(r), nil)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	members := []*store.Service{}
	for _, sv := range svcs {
		if catalog.FamilyKey(sv) == in.Key {
			members = append(members, sv)
		}
	}
	if len(members) < 2 {
		writeErr(w, 404, "not_found", "no family of services with that key")
		return
	}
	main := familyMain(members)
	linked := []string{}
	for _, m := range members {
		if m.ID == main.ID || (m.KeySourceID != nil && *m.KeySourceID == main.ID) || catalog.KeySourceProblem(m, main) != "" {
			continue
		}
		if err := s.Store.SetKeySource(ctx, m.ID, &main.ID); err != nil {
			writeStoreErr(w, err)
			return
		}
		linked = append(linked, m.Slug)
	}
	if len(linked) > 0 {
		s.Store.AddAudit(ctx, userFrom(r), "service.key_source", strings.Join(linked, ","), "use the keys of "+main.Slug)
	}
	writeJSON(w, 200, map[string]any{"main": main.Slug, "linked": linked})
}

// familyName is the words every member's name starts with ("Omkar" for
// "Omkar Moz" and "Omkar Cloud TikTok Scraper"), else the family's site.
func familyName(members []*store.Service, key string) string {
	common := strings.Fields(members[0].Name)
	for _, m := range members[1:] {
		words := strings.Fields(m.Name)
		n := 0
		for n < len(common) && n < len(words) && strings.EqualFold(common[n], words[n]) {
			n++
		}
		common = common[:n]
	}
	if len(common) > 0 {
		return strings.Join(common, " ")
	}
	site, _, _ := strings.Cut(key, "|")
	return site
}
