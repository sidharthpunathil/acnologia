package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// A provider with many products (Omkar Cloud lists about 185 scraper APIs)
// does not fit in one tool call: the model's output per turn is capped. So
// the builder can stage hosts, guide endpoints, offers and examples in
// batches with stage_parts, and the harness merges them into every draft it
// validates or submits.

// Caps on what one run may stage. They match the spec's own caps.
const (
	maxStagedHosts     = providers.MaxHosts
	maxStagedEndpoints = 400
	maxStagedOffers    = 400
	maxStagedExamples  = 400
)

type staged struct {
	Hosts     []providers.Host          `json:"hosts,omitempty"`
	Endpoints []providers.GuideEndpoint `json:"endpoints,omitempty"`
	Offers    []providers.Offer         `json:"offers,omitempty"`
	Examples  []providers.Example       `json:"examples,omitempty"`
}

func (st *staged) empty() bool {
	return st == nil || len(st.Hosts)+len(st.Endpoints)+len(st.Offers)+len(st.Examples) == 0
}

func endpointKey(e providers.GuideEndpoint) string {
	return strings.ToUpper(strings.TrimSpace(e.Method)) + " " + strings.TrimSpace(e.Path)
}

var stageTool = anthropic.BetaToolParam{
	Name: "stage_parts",
	Description: anthropic.String("Stage parts of the draft in batches, for a provider with many products or hosts that would not fit in one submit. " +
		"Staged hosts, guide endpoints, offers and examples are merged into every validate_spec, live_check and submit_provider call, " +
		"after the lists in the draft itself (an item already in the draft wins). Items replace staged ones with the same key: " +
		"host name, endpoint method and path, offer name, example name. Call it as often as you need, about 40 to 80 items per call."),
	InputSchema: schema(map[string]any{
		"hosts":     map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "spec.hosts items: {name, url, title, group, description, docs_url}"},
		"endpoints": map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "spec.guide.endpoints items, with group set to the product name"},
		"offers":    map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "spec.offers items"},
		"examples":  map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "spec.examples items"},
		"remove": map[string]any{"type": "object", "description": "staged items to drop: {hosts: [names], endpoints: [\"GET /path\"], offers: [names], examples: [names]}",
			"properties": map[string]any{
				"hosts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "endpoints": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"offers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "examples": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}},
		"from_current": map[string]any{"type": "boolean", "description": "update runs only: first stage the current version's hosts, guide endpoints, offers and examples, so you can submit a slim spec and send only what changes"},
	}),
}

type stageInput struct {
	staged
	Remove struct {
		Hosts     []string `json:"hosts"`
		Endpoints []string `json:"endpoints"`
		Offers    []string `json:"offers"`
		Examples  []string `json:"examples"`
	} `json:"remove"`
	FromCurrent bool `json:"from_current"`
}

// stage runs the stage_parts tool.
func (a *Agent) stage(ctx context.Context, s *runState, raw []byte) (string, bool) {
	var in stageInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "INVALID_JSON: " + err.Error(), true
	}
	if s.staged == nil {
		s.staged = &staged{}
	}
	st := s.staged
	if in.FromCurrent {
		if s.fixedSlug == "" {
			return "from_current works only when updating an existing provider", true
		}
		p, err := a.Store.GetProvider(ctx, s.fixedSlug)
		if err != nil {
			return err.Error(), true
		}
		cur, err := providers.DecodeSpec(p.Spec)
		if err != nil {
			return "the current spec is unreadable: " + err.Error(), true
		}
		cs := &staged{Hosts: cur.Hosts, Offers: cur.Offers, Examples: cur.Examples}
		if cur.Guide != nil {
			cs.Endpoints = cur.Guide.Endpoints
		}
		mergeStaged(st, cs)
	}
	mergeStaged(st, &in.staged)
	st.Hosts = dropBy(st.Hosts, in.Remove.Hosts, func(h providers.Host) string { return h.Name })
	st.Endpoints = dropBy(st.Endpoints, in.Remove.Endpoints, endpointKey)
	st.Offers = dropBy(st.Offers, in.Remove.Offers, func(o providers.Offer) string { return o.Name })
	st.Examples = dropBy(st.Examples, in.Remove.Examples, func(e providers.Example) string { return e.Name })
	if len(st.Hosts) > maxStagedHosts || len(st.Endpoints) > maxStagedEndpoints || len(st.Offers) > maxStagedOffers || len(st.Examples) > maxStagedExamples {
		return fmt.Sprintf("too many staged items: at most %d hosts, %d endpoints, %d offers and %d examples", maxStagedHosts, maxStagedEndpoints, maxStagedOffers, maxStagedExamples), true
	}
	msg := fmt.Sprintf("Staged %d hosts, %d guide endpoints, %d offers, %d examples.", len(st.Hosts), len(st.Endpoints), len(st.Offers), len(st.Examples))
	a.logf(ctx, s.run.ID, "tool", msg, "")
	return msg + " They merge into your next validate_spec or submit_provider.", false
}

// mergeStaged adds or replaces items of add in st, keyed as the tool says.
func mergeStaged(st, add *staged) {
	st.Hosts = upsert(st.Hosts, add.Hosts, func(h providers.Host) string { return h.Name })
	st.Endpoints = upsert(st.Endpoints, add.Endpoints, endpointKey)
	st.Offers = upsert(st.Offers, add.Offers, func(o providers.Offer) string { return o.Name })
	st.Examples = upsert(st.Examples, add.Examples, func(e providers.Example) string { return e.Name })
}

func upsert[T any](list, add []T, key func(T) string) []T {
	idx := map[string]int{}
	for i, x := range list {
		idx[key(x)] = i
	}
	for _, x := range add {
		if i, ok := idx[key(x)]; ok {
			list[i] = x
			continue
		}
		idx[key(x)] = len(list)
		list = append(list, x)
	}
	return list
}

func dropBy[T any](list []T, drop []string, key func(T) string) []T {
	if len(drop) == 0 {
		return list
	}
	gone := map[string]bool{}
	for _, d := range drop {
		gone[strings.TrimSpace(d)] = true
	}
	out := list[:0]
	for _, x := range list {
		if !gone[key(x)] {
			out = append(out, x)
		}
	}
	return out
}

// withStaged merges the staged parts into a draft spec. Items already in the
// draft win. A spec that does not parse is returned as is, for the
// validator to report.
func withStaged(spec json.RawMessage, st *staged) json.RawMessage {
	if st.empty() || len(spec) == 0 {
		return spec
	}
	sp, err := providers.DecodeSpec(spec)
	if err != nil {
		return spec
	}
	sp.Hosts = appendMissing(sp.Hosts, st.Hosts, func(h providers.Host) string { return h.Name })
	sp.Offers = appendMissing(sp.Offers, st.Offers, func(o providers.Offer) string { return o.Name })
	sp.Examples = appendMissing(sp.Examples, st.Examples, func(e providers.Example) string { return e.Name })
	if len(st.Endpoints) > 0 {
		if sp.Guide == nil {
			sp.Guide = &providers.Guide{}
		}
		sp.Guide.Endpoints = appendMissing(sp.Guide.Endpoints, st.Endpoints, endpointKey)
	}
	out, err := json.Marshal(sp)
	if err != nil {
		return spec
	}
	return out
}

func appendMissing[T any](list, extra []T, key func(T) string) []T {
	have := map[string]bool{}
	for _, x := range list {
		have[key(x)] = true
	}
	for _, x := range extra {
		if !have[key(x)] {
			have[key(x)] = true
			list = append(list, x)
		}
	}
	return list
}
