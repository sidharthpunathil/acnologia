package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Provider specs between servers. The owner serves the logo of any provider
// in a share's manifest to the share token; the receiver lists the providers
// it imported through a link and, on a superadmin's explicit request, pulls
// a newer owner version.

// peerProviderLogo serves the logo of a provider the share describes (shared
// on purpose or used by a shared service), and nothing else.
func (s *Server) peerProviderLogo(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.shareFromApp(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")
	m := s.buildManifest(r.Context(), sh)
	for _, p := range m.Providers {
		if p.Slug == slug {
			data, mime, err := s.Store.ProviderLogo(r.Context(), slug)
			serveLogo(w, r, data, mime, err)
			return
		}
	}
	writeErr(w, 404, "not_found", "not found")
}

// peerProviderView is an imported provider as the dashboard shows it.
type peerProviderView struct {
	*store.PeerProvider
	Name string `json:"name"`
	// Services are the local services that needed it, by remote slug.
	Services map[string]int64 `json:"services"`
	// EditedHere is true once someone here changed the imported spec; an
	// update would overwrite that, so none is offered.
	EditedHere      bool `json:"edited_here"`
	UpdateAvailable bool `json:"update_available"`
	// BaseURL and RemoteBaseURL differ when an update would send requests
	// (and the keys added here) somewhere else.
	BaseURL       string `json:"base_url"`
	RemoteBaseURL string `json:"remote_base_url"`
	Missing       bool   `json:"missing"`
}

func (s *Server) peerProviderViews(r *http.Request, link *store.PeerLink) ([]peerProviderView, error) {
	rows, err := s.Store.ListPeerProviders(r.Context(), link.ID)
	if err != nil {
		return nil, err
	}
	var m peer.Manifest
	_ = json.Unmarshal(link.Manifest, &m)
	remote := map[string]peer.ManifestProvider{}
	for _, p := range m.Providers {
		remote[p.Slug] = p
	}
	out := make([]peerProviderView, 0, len(rows))
	for _, row := range rows {
		v := peerProviderView{PeerProvider: row, Name: row.Provider, Services: map[string]int64{}}
		for _, slug := range row.ForServices {
			if id, ok := link.Services[slug]; ok {
				v.Services[slug] = id
			}
		}
		p, err := s.Store.GetProvider(r.Context(), row.Provider)
		if err != nil {
			v.Missing = true
			out = append(out, v)
			continue
		}
		v.Name = p.Name
		v.EditedHere = p.Version != row.LocalVersion || p.Source != "peer:"+link.PeerID
		v.UpdateAvailable = !v.EditedHere && row.RemoteVersion > row.ImportedVersion
		if spec, err := providers.DecodeSpec(p.Spec); err == nil {
			v.BaseURL = spec.BaseURL
		}
		if mp, ok := remote[row.Provider]; ok {
			if spec, err := providers.DecodeSpec(mp.Spec); err == nil {
				v.RemoteBaseURL = spec.BaseURL
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// listPeerProviders lists the provider specs imported through a link.
func (s *Server) listPeerProviders(w http.ResponseWriter, r *http.Request) {
	id, ok := s.peerLinkID(w, r)
	if !ok {
		return
	}
	link, err := s.Store.GetPeerLink(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	views, err := s.peerProviderViews(r, link)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"providers": views})
}

// pullPeerProvider updates an imported provider to the owner's current spec.
// Body: {"allow_base_url_change": bool}.
func (s *Server) pullPeerProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := s.peerLinkID(w, r)
	if !ok {
		return
	}
	var in struct {
		AllowBaseURLChange bool `json:"allow_base_url_change"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	slug := chi.URLParam(r, "slug")
	_, err := s.peers.PullProvider(r.Context(), id, slug, in.AllowBaseURLChange)
	var moved *peer.BaseURLChangeError
	switch {
	case errors.As(err, &moved):
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "base_url_changes", "message": err.Error()},
			"from": moved.From, "to": moved.To})
		return
	case errors.Is(err, peer.ErrEditedHere):
		writeErr(w, 409, "edited_here", err.Error())
		return
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, "not_found", "not found")
		return
	case peer.AsRemote(err) != nil:
		writeErr(w, 502, "peer_error", err.Error())
		return
	case err != nil:
		writeErr(w, 400, "pull_failed", err.Error())
		return
	}
	link, err := s.Store.GetPeerLink(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.pulled", slug, "from peer "+link.PeerID+" ("+link.PeerName+")")
	views, err := s.peerProviderViews(r, link)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"providers": views})
}
