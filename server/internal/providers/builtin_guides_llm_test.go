package providers

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// llmGuideSlugs are the LLM providers whose usage guides live in
// builtin_labs.go, builtin_llm.go and builtin_hosts.go.
var llmGuideSlugs = []string{
	"openai", "anthropic", "xai", "mistral", "deepseek", "cohere", "perplexity",
	"gemini",
	"together", "groq", "fireworks", "cerebras", "openrouter",
}

// Every LLM provider ships a guide that validates, whose paths are relative
// to its base URL without repeating the base path, and whose example bodies
// marshal to JSON.
func TestLLMBuiltinGuides(t *testing.T) {
	for _, slug := range llmGuideSlugs {
		_, s := builtinSpec(t, slug)
		g := s.Guide
		if g == nil {
			t.Errorf("%s: no guide", slug)
			continue
		}
		if errs := s.Validate(); len(errs) > 0 {
			t.Errorf("%s: spec does not validate: %v", slug, errs)
		}
		if n := len(g.Endpoints); n < 3 || n > 8 {
			t.Errorf("%s: %d guide endpoints, want 3 to 8", slug, n)
		}
		if len(g.Notes) == 0 {
			t.Errorf("%s: guide has no notes", slug)
		}
		base, err := url.Parse(s.BaseURL)
		if err != nil {
			t.Fatalf("%s: base url: %v", slug, err)
		}
		var baseSegs []string
		for _, sg := range strings.Split(strings.Trim(base.Path, "/"), "/") {
			if sg != "" {
				baseSegs = append(baseSegs, sg)
			}
		}
		for _, e := range g.Endpoints {
			if !strings.HasPrefix(e.Path, "/") {
				t.Errorf("%s/%s: path %q must start with /", slug, e.Name, e.Path)
			}
			first := strings.SplitN(strings.TrimPrefix(e.Path, "/"), "/", 2)[0]
			for _, sg := range baseSegs {
				if first == sg {
					t.Errorf("%s/%s: path %q repeats %q from base url %s", slug, e.Name, e.Path, sg, s.BaseURL)
				}
			}
			if strings.ContainsAny(e.Path, "?#") {
				t.Errorf("%s/%s: path %q carries a query; use a query param", slug, e.Name, e.Path)
			}
			if e.Description == "" || e.Response == "" {
				t.Errorf("%s/%s: needs a description and a response", slug, e.Name)
			}
			if e.Body != nil {
				if _, err := json.Marshal(e.Body); err != nil {
					t.Errorf("%s/%s: body does not marshal: %v", slug, e.Name, err)
				}
			}
		}
	}
}
