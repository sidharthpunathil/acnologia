package providers

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// guidedSearchSlugs are the built-ins whose guides live in builtin.go,
// builtin_search.go, builtin_tinyfish.go and builtin_peer.go.
var guidedSearchSlugs = []string{
	"serper", "brave-search", "tavily", "exa", "firecrawl", "jina", "searchapi", "linkup", "youcom",
	"serpapi", "elevenlabs", "generic", "tinyfish-search", "tinyfish-fetch", PeerSlug,
}

// sgNoEndpoints are guides that describe a pass through and list no endpoints.
var sgNoEndpoints = map[string]bool{"generic": true, PeerSlug: true}

func TestSearchAndVoiceGuides(t *testing.T) {
	bySlug := map[string]Builtin{}
	for _, b := range Builtins() {
		bySlug[b.Slug] = b
	}
	for _, slug := range guidedSearchSlugs {
		b, ok := bySlug[slug]
		if !ok {
			t.Errorf("%s: not a built-in provider", slug)
			continue
		}
		s := b.Spec
		s.Normalize()
		if s.Guide == nil {
			t.Errorf("%s: no guide", slug)
			continue
		}
		if errs := s.Validate(); len(errs) > 0 {
			t.Errorf("%s: %v", slug, errs)
		}
		g := s.Guide
		if n := len(g.Endpoints); !sgNoEndpoints[slug] && (n < 1 || n > 8) {
			t.Errorf("%s: %d endpoints, want 1 to 8", slug, n)
		}
		if len(g.Notes) == 0 {
			t.Errorf("%s: no notes", slug)
		}
		mentionsKey := false
		for _, n := range g.Notes {
			if strings.Contains(n, "Never send a provider API key") {
				mentionsKey = true
			}
		}
		if !mentionsKey {
			t.Errorf("%s: no note saying the caller never sends a provider key", slug)
		}
		u, err := url.Parse(s.BaseURL)
		if err != nil {
			t.Fatalf("%s: base url: %v", slug, err)
		}
		baseSegs := map[string]bool{}
		for _, sg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
			if sg != "" {
				baseSegs[sg] = true
			}
		}
		for _, e := range g.Endpoints {
			label := slug + " " + e.Name
			if !strings.HasPrefix(e.Path, "/") {
				t.Errorf("%s: path %q must start with /", label, e.Path)
			}
			if strings.ContainsAny(e.Path, "?#") {
				t.Errorf("%s: path %q must not carry a query", label, e.Path)
			}
			first := strings.SplitN(strings.TrimPrefix(e.Path, "/"), "/", 2)[0]
			if first != "" && baseSegs[first] {
				t.Errorf("%s: path %q repeats %q from the base URL %s", label, e.Path, first, s.BaseURL)
			}
			if e.Description == "" || e.Response == "" {
				t.Errorf("%s: needs a description and a response", label)
			}
			if e.Body != nil {
				if _, err := json.Marshal(e.Body); err != nil {
					t.Errorf("%s: body does not marshal: %v", label, err)
				}
			}
			for _, p := range e.Params {
				if p.In == "path" && !strings.Contains(e.Path, "{"+p.Name+"}") {
					t.Errorf("%s: path param %q is not in %q", label, p.Name, e.Path)
				}
			}
		}
		// The guide survives the JSON round trip the database stores.
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		back, err := DecodeSpec(raw)
		if err != nil || back.Guide == nil || len(back.Guide.Endpoints) != len(g.Endpoints) {
			t.Errorf("%s: guide lost in the JSON round trip: %v", slug, err)
		}
	}
}

func TestGuidesHaveNoDashes(t *testing.T) {
	// U+2014 and the other dashes the house style bans; the en dash (U+2013)
	// is allowed only in numeric ranges, and these guides do not use it.
	banned := []rune{0x2014, 0x2013, 0x2012, 0x2015, 0x2212, 0x2E3A, 0x2E3B, 0xFE58, 0xFE63, 0xFF0D}
	for _, b := range Builtins() {
		if b.Spec.Guide == nil || !slices.Contains(guidedSearchSlugs, b.Slug) {
			continue
		}
		raw, _ := json.Marshal(b.Spec.Guide)
		for _, r := range banned {
			if strings.ContainsRune(string(raw), r) {
				t.Errorf("%s: guide contains U+%04X", b.Slug, r)
			}
		}
		if strings.Contains(string(raw), "--") {
			t.Errorf("%s: guide contains a doubled hyphen", b.Slug)
		}
	}
}
