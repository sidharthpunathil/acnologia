package api

// Measured evidence in the catalog: search_tools results carry a tool's
// success rate, median latency and last success once it has enough calls
// (store/endpoint_stats.go), ties after availability go to the higher
// success rate, and the reliable routing strategy orders providers by it.

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// toolEvidenceOut is a tool's evidence as search_tools shows it.
type toolEvidenceOut struct {
	SuccessRate float64    `json:"success_rate"`
	P50MS       int64      `json:"p50_ms"`
	LastOK      *time.Time `json:"last_ok,omitempty"`
	// Calls is the sample: calls that counted for or against the provider
	// over the last EvidenceWindowDays days.
	Calls int64 `json:"calls"`
}

func toolEvidence(e *store.EndpointStat) toolEvidenceOut {
	return toolEvidenceOut{SuccessRate: math.Round(e.SuccessRate()*1000) / 1000, P50MS: e.P50MS, LastOK: e.LastOK, Calls: e.Counted()}
}

// evidence is a service tool's stats: its upstream MCP tool, or the
// endpoint its example or guide entry calls. Capabilities have none of
// their own.
func (l *availLookup) evidence(t *mcpTool) *store.EndpointStat {
	if t.capability != "" || t.service == "" {
		return nil
	}
	ref, ok := l.c.svcs[t.service]
	if !ok {
		return nil
	}
	if l.ev == nil {
		l.ev = l.s.Store.Evidence(l.ctx)
	}
	if t.upstreamMCP {
		return l.ev.Endpoint(ref.id, providers.MCPMethod, t.upstreamTool)
	}
	if t.example == nil {
		return nil
	}
	return l.ev.Endpoint(ref.id, strings.ToUpper(orDefault(t.example.Method, "GET")), providers.TemplatePath(t.example.Path))
}

// maxGuideEvidence caps the measured endpoints a guide lists.
const maxGuideEvidence = 100

// guideEvidence adds what this server measured for the service's
// endpoints, where the sample is big enough.
func (s *Server) guideEvidence(ctx context.Context, d *providers.GuideDoc, sv *store.Service) {
	for _, e := range s.Store.Evidence(ctx).Endpoints(sv.ID) {
		if !e.Enough() {
			continue
		}
		d.Evidence = append(d.Evidence, providers.GuideEvidence{Method: e.Method, Path: e.Path, Calls: e.Counted(),
			SuccessRate: math.Round(e.SuccessRate()*1000) / 1000, P50MS: e.P50MS, P95MS: e.P95MS, LastOK: e.LastOK, LastFail: e.LastFail})
		if len(d.Evidence) == maxGuideEvidence {
			break
		}
	}
}

// guideResponses adds the provider's stored sample answers.
func (s *Server) guideResponses(ctx context.Context, d *providers.GuideDoc, provider string) {
	rs, err := s.Store.ExampleResponses(ctx, provider)
	if err != nil {
		return
	}
	for _, r := range rs {
		d.Responses = append(d.Responses, providers.GuideResponse{Example: r.Example, Method: r.Method, Path: r.Path,
			Status: r.Status, ContentType: r.ContentType, Body: r.Body, VerifiedAt: r.VerifiedAt})
	}
}

// routeEvidence is what the reliable strategy knows of one candidate for
// one request: the endpoint's stats when they are enough, else the
// service's over every endpoint.
func (s *Server) routeEvidence(ctx context.Context, sv *store.Service, method, path string) (rate float64, p50 int64, known bool) {
	ev := s.Store.Evidence(ctx)
	if ev.Service(sv.ID) == nil {
		return 0, 0, false
	}
	spec := providers.DocSpec(sv.Provider, sv.PeerProvider)
	if t, _ := spec.EndpointKey(method, "/"+trimSlash(path)); t != "" {
		if e := ev.Endpoint(sv.ID, strings.ToUpper(method), t); e.Enough() {
			return e.SuccessRate(), e.P50MS, true
		}
	}
	if e := ev.Service(sv.ID); e.Enough() {
		return e.SuccessRate(), e.P50MS, true
	}
	return 0, 0, false
}
