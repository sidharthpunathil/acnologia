package api

// Usage guides: how an app or AI agent calls a provider through Acnologia.
// The dashboard reads them per provider or per service; apps read them for
// the services they may use, as JSON or raw Markdown.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// guideMeta is the catalog entry a guide shows for a provider.
func guideMeta(p *store.Provider) providers.GuideMeta {
	return providers.GuideMeta{Slug: p.Slug, Name: p.Name, Description: p.Description,
		Category: p.Category, DocsURL: p.DocsURL, Website: p.Website}
}

// serviceGuide builds the guide for one service, from the spec that
// describes it (the owner's provider for a peer service).
func (s *Server) serviceGuide(ctx context.Context, sv *store.Service, publicURL string, admin bool) providers.GuideDoc {
	meta := providers.GuideMeta{Slug: sv.DocProvider(), Name: sv.Name, Description: sv.Description, Category: sv.Category}
	if p, err := s.Store.GetProvider(ctx, sv.DocProvider()); err == nil {
		meta = guideMeta(p)
	}
	in := providers.GuideInput{Meta: meta, Spec: docSpec(sv),
		Ctx: providers.GuideContext{Service: sv.Slug, ServiceName: sv.Name, PublicURL: publicURL, Admin: admin}}
	return providers.BuildGuide(in)
}

func writeGuide(w http.ResponseWriter, r *http.Request, d providers.GuideDoc, admin bool) {
	md := d.Render(admin)
	if wantsMarkdown(r) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(md))
		return
	}
	writeJSON(w, 200, map[string]any{"markdown": md, "guide": d, "generated": d.Generated})
}

func wantsMarkdown(r *http.Request) bool {
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "md", "markdown":
		return true
	case "json":
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/markdown")
}

// providerGuide is GET /api/admin/providers/{slug}/guide. ?service= fills
// the proxy URL for that service, which must be built on this provider.
func (s *Server) providerGuide(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetProvider(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		writeErr(w, 500, "bad_spec", "the provider spec does not decode")
		return
	}
	gc := providers.GuideContext{PublicURL: s.publicURL(r), Admin: true}
	if slug := strings.TrimSpace(r.URL.Query().Get("service")); slug != "" {
		sv, err := s.Store.GetServiceBySlug(r.Context(), slug)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		if sv.Provider != p.Slug && sv.DocProvider() != p.Slug {
			writeErr(w, 400, "invalid", "service "+slug+" is not built on "+p.Slug)
			return
		}
		gc.Service, gc.ServiceName = sv.Slug, sv.Name
	}
	writeGuide(w, r, providers.BuildGuide(providers.GuideInput{Meta: guideMeta(p), Spec: spec, Ctx: gc}), true)
}

// adminServiceGuide is GET /api/admin/services/{id}/guide.
func (s *Server) adminServiceGuide(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	writeGuide(w, r, s.serviceGuide(r.Context(), sv, s.publicURL(r), true), true)
}

// v1ServiceGuide is GET /api/v1/services/{slug}/guide, for an app token. An
// app reads guides only for services it may use.
func (s *Server) v1ServiceGuide(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.serviceForApp(w, r, chi.URLParam(r, "slug"))
	if !ok {
		return
	}
	writeGuide(w, r, s.serviceGuide(r.Context(), sv, s.publicURL(r), false), false)
}

// guideURL is where an app fetches a service's guide.
func guideURL(base, slug string) string {
	return strings.TrimRight(base, "/") + "/api/v1/services/" + urlPathEscape(slug) + "/guide"
}

// mcpServiceGuide renders the guide for the service_guide tool and the
// guide resource, after the same allow check as every other MCP call.
func (s *Server) mcpServiceGuide(ctx context.Context, a *store.App, slug string) (providers.GuideDoc, error) {
	if !a.CanUse(slug) {
		return providers.GuideDoc{}, errors.New("this app cannot use service " + slug)
	}
	sv, err := s.Store.GetServiceBySlug(ctx, slug)
	if err != nil {
		return providers.GuideDoc{}, errors.New("no service named " + slug)
	}
	return s.serviceGuide(ctx, sv, mcpBase(ctx, s), false), nil
}
