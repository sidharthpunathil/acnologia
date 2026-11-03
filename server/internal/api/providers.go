package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// providerDTO is a provider plus fields flattened from its spec.
type providerDTO struct {
	*store.Provider
	BaseURL                string `json:"base_url"`
	AuthType               string `json:"auth_type"`
	AuthName               string `json:"auth_name"`
	DefaultCooldownSec     int    `json:"default_cooldown_sec"`
	DefaultMaxConcurrency  int    `json:"default_max_concurrency"`
	DefaultQueueTimeoutSec int    `json:"default_queue_timeout_sec"`
	// Refresh is when the offers refresh last checked and changed this
	// provider, and any proposal waiting for review. Nil when never checked.
	Refresh *store.ProviderRefresh `json:"refresh,omitempty"`
}

func providerLogoURL(slug string, rev int) *string {
	u := fmt.Sprintf("/api/logos/providers/%s?v=%d", slug, rev)
	return &u
}

func toDTO(p *store.Provider) providerDTO {
	if p.HasLogo {
		p.LogoURL = providerLogoURL(p.Slug, p.LogoRev)
	}
	d := providerDTO{Provider: p}
	if spec, err := providers.DecodeSpec(p.Spec); err == nil {
		d.BaseURL, d.AuthType, d.AuthName = spec.BaseURL, spec.Auth.Type, spec.Auth.Name
		d.DefaultCooldownSec, d.DefaultMaxConcurrency, d.DefaultQueueTimeoutSec = spec.Defaults.CooldownSec, spec.Defaults.MaxConcurrency, spec.Defaults.QueueTimeoutSec
	}
	return d
}

// fillLogos sets each service's logo_url: its own logo, else its provider's.
func (s *Server) fillLogos(ctx context.Context, svcs ...*store.Service) {
	revs, _ := s.Store.ProviderLogoRevs(ctx)
	for _, sv := range svcs {
		switch {
		case sv.HasLogo:
			u := fmt.Sprintf("/api/logos/services/%d?v=%d", sv.ID, sv.LogoRev)
			sv.LogoURL = &u
		default:
			// A peer service shows the owner's provider's logo when this
			// server has it, else the peer provider's.
			for _, slug := range []string{sv.DocProvider(), sv.Provider} {
				if rev, ok := revs[slug]; ok {
					sv.LogoURL = providerLogoURL(slug, rev)
					break
				}
			}
		}
	}
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListProviders(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	states, _ := s.Store.ProviderRefreshStates(r.Context())
	out := make([]providerDTO, 0, len(ps))
	for _, p := range ps {
		d := toDTO(p)
		d.Refresh = states[p.Slug]
		out = append(out, d)
	}
	writeJSON(w, 200, map[string]any{"providers": out, "strategies": pool.Strategies})
}

func (s *Server) getProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetProvider(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	versions, err := s.Store.ProviderVersions(r.Context(), p.Slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	d := toDTO(p)
	if states, err := s.Store.ProviderRefreshStates(r.Context()); err == nil {
		d.Refresh = states[p.Slug]
	}
	writeJSON(w, 200, struct {
		providerDTO
		Versions []store.ProviderVersion `json:"versions"`
	}{d, versions})
}

type providerBody struct {
	catalog.Input
	Note string `json:"note"`
}

func (s *Server) writePublishErr(w http.ResponseWriter, err error) {
	var ve *catalog.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, 400, map[string]any{
			"error":  apiError{Code: "invalid_provider", Message: ve.Error()},
			"errors": nonNilStrings(ve.Errors), "report": ve.Report,
		})
		return
	}
	writeStoreErr(w, err)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// publisherOf is the person behind a provider edit. Only a superadmin acting
// in person vouches for where the spec sends keys; an admin's (or the
// Assistant's) change to auth or the health check revokes trust in the
// provider's services (Catalog.publish).
func publisherOf(r *http.Request) catalog.Publisher {
	u := userFrom(r)
	p := catalog.Publisher{User: u, Super: isSuper(r) && !fromAssistant(r)}
	if u != nil {
		p.Name = u.Email
	}
	return p
}

func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	var body providerBody
	if !decode(w, r, &body) {
		return
	}
	if _, err := s.Store.GetProvider(r.Context(), strings.ToLower(strings.TrimSpace(body.Slug))); err == nil {
		writeErr(w, 409, "duplicate", "a provider with that slug already exists")
		return
	}
	p, rep, err := s.Catalog.Publish(r.Context(), body.Input, "manual", publisherOf(r), body.Note)
	if err != nil {
		s.writePublishErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.create", p.Slug, fmt.Sprintf("v%d", p.Version))
	writeJSON(w, 201, map[string]any{"provider": toDTO(p), "report": rep})
}

func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	cur, err := s.Store.GetProvider(r.Context(), slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var body providerBody
	if !decode(w, r, &body) {
		return
	}
	body.Slug = slug // slugs are stable: services reference them
	source := cur.Source
	if source == "builtin" || source == "ai" {
		source = "manual"
	}
	p, rep, err := s.Catalog.Publish(r.Context(), body.Input, source, publisherOf(r), body.Note)
	if err != nil {
		s.writePublishErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.update", p.Slug, fmt.Sprintf("v%d %s", p.Version, body.Note))
	writeJSON(w, 200, map[string]any{"provider": toDTO(p), "report": rep})
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if err := s.Catalog.Delete(r.Context(), slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeStoreErr(w, err)
			return
		}
		writeErr(w, 409, "in_use", err.Error())
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.delete", slug, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) validateProvider(w http.ResponseWriter, r *http.Request) {
	var in catalog.Input
	if !decode(w, r, &in) {
		return
	}
	if in.Slug == "" {
		in.Slug = "draft"
	}
	_, errs, rep := catalog.Check(&in)
	writeJSON(w, 200, map[string]any{"errors": nonNilStrings(errs), "report": rep})
}

func (s *Server) rollbackProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int `json:"version"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := s.Catalog.Rollback(r.Context(), chi.URLParam(r, "slug"), body.Version, publisherOf(r))
	if err != nil {
		s.writePublishErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.rollback", p.Slug, fmt.Sprintf("to v%d as v%d", body.Version, p.Version))
	writeJSON(w, 200, toDTO(p))
}

func (s *Server) liveCheckProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetProvider(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var body struct {
		Secret string `json:"secret"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Secret) == "" {
		writeErr(w, 400, "invalid", "paste a key to test with")
		return
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		writeErr(w, 400, "bad_spec", err.Error())
		return
	}
	writeJSON(w, 200, catalog.LiveCheck(r.Context(), spec, strings.TrimSpace(body.Secret)))
}

// ---- logos ----

type logoBody struct {
	DataBase64 string `json:"data_base64"`
	Mime       string `json:"mime"`
}

func decodeLogo(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	var b logoBody
	if !decode(w, r, &b) {
		return nil, "", false
	}
	raw := b.DataBase64
	if i := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && i > 0 {
		raw = raw[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		writeErr(w, 400, "bad_image", "data_base64 is not base64")
		return nil, "", false
	}
	if len(data) > catalog.MaxLogoBytes {
		writeErr(w, 413, "too_large", "logos must be 512 KB or smaller")
		return nil, "", false
	}
	mime := catalog.SniffImage(data)
	if mime == "" {
		writeErr(w, 400, "bad_image", "use a png, jpeg, webp, gif, svg or ico image")
		return nil, "", false
	}
	return data, mime, true
}

func serveLogo(w http.ResponseWriter, r *http.Request, data []byte, mime string, err error) {
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// SVGs can carry script: never let one run if opened directly.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	_, _ = w.Write(data)
}

func (s *Server) providerLogo(w http.ResponseWriter, r *http.Request) {
	data, mime, err := s.Store.ProviderLogo(r.Context(), chi.URLParam(r, "slug"))
	serveLogo(w, r, data, mime, err)
}

func (s *Server) serviceLogo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad_id", "bad id")
		return
	}
	data, mime, err := s.Store.ServiceLogo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		// Fall back to the provider logo.
		if sv, e := s.Store.GetService(r.Context(), id); e == nil {
			data, mime, err = s.Store.ProviderLogo(r.Context(), sv.DocProvider())
			if errors.Is(err, store.ErrNotFound) && sv.DocProvider() != sv.Provider {
				data, mime, err = s.Store.ProviderLogo(r.Context(), sv.Provider)
			}
		}
	}
	serveLogo(w, r, data, mime, err)
}

func (s *Server) putProviderLogo(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if _, err := s.Store.GetProvider(r.Context(), slug); err != nil {
		writeStoreErr(w, err)
		return
	}
	data, mime, ok := decodeLogo(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetProviderLogo(r.Context(), slug, data, mime); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.logo", slug, mime)
	p, _ := s.Store.GetProvider(r.Context(), slug)
	writeJSON(w, 200, toDTO(p))
}

func (s *Server) deleteProviderLogo(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if err := s.Store.SetProviderLogo(r.Context(), slug, nil, ""); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.logo_delete", slug, "")
	p, err := s.Store.GetProvider(r.Context(), slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, toDTO(p))
}

func (s *Server) fetchProviderLogo(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	p, err := s.Store.GetProvider(r.Context(), slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var data []byte
	var mime string
	if body.URL != "" && catalog.SniffImageURL(body.URL) {
		data, mime, err = catalog.DownloadImage(ctx, nil, body.URL)
	} else {
		site := body.URL
		if site == "" {
			site = p.Website
		}
		if site == "" {
			writeErr(w, 400, "no_website", "set the provider website first, or pass a url")
			return
		}
		data, mime, _, err = catalog.FetchLogo(ctx, site)
	}
	if err != nil {
		writeErr(w, 422, "logo_not_found", err.Error())
		return
	}
	if err := s.Store.SetProviderLogo(r.Context(), slug, data, mime); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "provider.logo_fetch", slug, mime)
	p, _ = s.Store.GetProvider(r.Context(), slug)
	writeJSON(w, 200, toDTO(p))
}

func (s *Server) putServiceLogo(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	data, mime, ok := decodeLogo(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetServiceLogo(r.Context(), sv.ID, data, mime); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.logo", sv.Slug, mime)
	s.getService(w, r)
}

func (s *Server) deleteServiceLogo(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetServiceLogo(r.Context(), sv.ID, nil, ""); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.logo_delete", sv.Slug, "")
	s.getService(w, r)
}
