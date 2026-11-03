package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// guideGenTimeout bounds one guide research, which reads a few docs pages.
const guideGenTimeout = 5 * time.Minute

// guideGenOut is what writing a guide with AI returns. Updated is false when
// the model could not read the docs; the provider is then unchanged.
type guideGenOut struct {
	Provider providerDTO      `json:"provider"`
	Updated  bool             `json:"updated"`
	Verified bool             `json:"verified"`
	Summary  string           `json:"summary"`
	Guide    *providers.Guide `json:"guide"`
	Markdown string           `json:"markdown"`
	Model    string           `json:"model"`
	CostUSD  float64          `json:"cost_usd"`
}

// generateProviderGuide writes or rewrites a provider's usage guide with AI
// from its docs, examples and offers, and saves it as a new version.
// POST /api/admin/providers/{slug}/guide/generate, body {instruction?}.
func (s *Server) generateProviderGuide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Instruction string `json:"instruction"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, 400, "invalid_json", "body must be JSON: "+err.Error())
			return
		}
	}
	if len(body.Instruction) > 4000 {
		writeErr(w, 400, "invalid", "instruction must be under 4,000 characters")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), guideGenTimeout)
	defer cancel()
	out, err := s.writeProviderGuide(ctx, r, userFrom(r), chi.URLParam(r, "slug"), body.Instruction)
	if err != nil {
		var ve *catalog.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, 422, map[string]any{"error": apiError{Code: "invalid_spec", Message: ve.Error()}})
			return
		}
		writeAIErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// writeProviderGuide is the shared logic of the route and the Assistant's
// write_provider_guide tool. r is used only for the public URL.
func (s *Server) writeProviderGuide(ctx context.Context, r *http.Request, u *store.User, slug, instruction string) (*guideGenOut, error) {
	if s.Agent == nil || !s.Agent.Enabled() {
		return nil, agent.ErrDisabled
	}
	p, err := s.Store.GetProvider(ctx, slug)
	if err != nil {
		return nil, err
	}
	res, err := s.Agent.ResearchGuide(ctx, agent.GuideRequest{Provider: p, Caller: agent.CallerOf(u), Instruction: instruction})
	if err != nil {
		return nil, err
	}
	out := &guideGenOut{Verified: res.Verified, Summary: res.Summary, Model: res.Model, CostUSD: res.CostUSD}
	if res.Verified && res.Guide != nil {
		// Read the provider again and replace only its guide, so an edit
		// saved while the model was reading is kept.
		cur, err := s.Store.GetProvider(ctx, slug)
		if err != nil {
			return nil, err
		}
		raw, err := withGuide(cur.Spec, res.Guide)
		if err != nil {
			return nil, err
		}
		in := catalog.Input{Slug: cur.Slug, Name: cur.Name, Category: cur.Category, Description: cur.Description, Website: cur.Website,
			DocsURL: cur.DocsURL, Color: cur.Color, Status: cur.Status, Spec: raw}
		np, _, err := s.Catalog.PublishVersion(ctx, in, "ai", catalog.Publisher{Name: u.Email, User: u}, "usage guide written by AI")
		if err != nil {
			return nil, err
		}
		p, out.Updated = np, true
		s.Store.AddAudit(ctx, u, "provider.guide.generate", np.Slug, fmt.Sprintf("v%d, %s, $%.4f", np.Version, res.Model, res.CostUSD))
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		return nil, err
	}
	out.Provider = toDTO(p)
	out.Guide = spec.Guide
	out.Markdown = providers.RenderGuide(providers.GuideInput{
		Meta: providers.GuideMeta{Slug: p.Slug, Name: p.Name, Description: p.Description, Category: p.Category, DocsURL: p.DocsURL, Website: p.Website},
		Spec: spec, Ctx: providers.GuideContext{PublicURL: s.guidePublicURL(r), Admin: true},
	})
	return out, nil
}

// guidePublicURL is the base the guide's proxy URLs use.
func (s *Server) guidePublicURL(r *http.Request) string {
	if r != nil {
		return s.publicURL(r)
	}
	return strings.TrimRight(s.Cfg.PublicURL, "/")
}

// withGuide replaces the guide in a raw spec and keeps every other field
// byte for byte.
func withGuide(spec json.RawMessage, g *providers.Guide) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(spec, &m); err != nil {
		return nil, err
	}
	b, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	m["guide"] = b
	return json.Marshal(m)
}
