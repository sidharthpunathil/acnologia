package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/cogito-keys/cogito/internal/docsmd"
)

// mountDocs serves the Markdown docs. All of it is public, like /docs and
// /llms.txt: /docs and /docs/<slug> stay with the dashboard, which reads
// these routes to render the pages.
func (s *Server) mountDocs(r chi.Router) {
	r.Get("/api/docs", s.docsIndex)
	r.Get("/docs/{slug}.md", s.docMarkdown)
	r.Get("/docs/assets/{name}", s.docAsset)
}

// docsIndex lists every doc: slug, title, first paragraph and size in bytes.
func (s *Server) docsIndex(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"docs": docsmd.All()})
}

func (s *Server) docMarkdown(w http.ResponseWriter, r *http.Request) {
	d, ok := docsmd.Get(chi.URLParam(r, "slug"))
	if !ok {
		http.Error(w, "no such doc", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(d.Body)
}

func (s *Server) docAsset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	b, ok := docsmd.Asset(name)
	if !ok || !strings.HasSuffix(name, ".png") {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}
