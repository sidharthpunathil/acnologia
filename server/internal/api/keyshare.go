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
