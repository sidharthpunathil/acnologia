package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
)

// A draft as the builder would submit it, with offers read from a pricing page.
const offerDraft = `{
  "slug": "fishsearch", "name": "Fish Search", "category": "Search", "description": "Search.",
  "website": "https://fish.example", "docs_url": "https://fish.example/docs", "color": "#d74b28",
  "spec": {
    "base_url": "https://api.fish.example",
    "auth": {"type": "header", "name": "X-API-Key"},
    "use_standard_rules": true,
    "examples": [{"name": "Search", "method": "GET", "path": "/", "query": {"query": "coffee"}}],
    "offers": [
      {"name": "Search", "match": {"examples": ["Search"]}, "free": true,
       "price": {"amount": 0, "unit": "request"},
       "limits": [{"count": 30, "per": "minute", "unit": "requests"}, {"count": 500, "per": "hour"}],
       "source_url": "https://fish.example/pricing", "checked_at": "2026-11-02"},
      {"name": "Agent", "match": {"method": "POST", "path": "/v1/automation/run"},
       "price": {"amount": 0.016, "currency": "USD", "unit": "step"}, "concurrency": 2,
       "source_url": "https://fish.example/pricing", "checked_at": "2026-11-02"}
    ],
    "tests": [{"name": "ok", "status": 200, "body": "{}", "expect": "ok"}, {"name": "429", "status": 429, "expect": "rate_limited"}]
  }
}`

func TestBuilderDraftOffersAreValidated(t *testing.T) {
	var in catalog.Input
	if err := json.Unmarshal([]byte(offerDraft), &in); err != nil {
		t.Fatal(err)
	}
	spec, errs, rep := catalog.Check(&in)
	if len(errs) > 0 || rep.Failed > 0 {
		t.Fatalf("good draft: %v %+v", errs, rep)
	}
	if len(spec.Offers) != 2 || spec.CostFor("GET", "/") != 0 {
		t.Fatalf("offers not decoded: %+v", spec.Offers)
	}
	bad := strings.Replace(offerDraft, `"per": "minute"`, `"per": "fortnight"`, 1)
	bad = strings.Replace(bad, `"checked_at": "2026-11-02"}`, `"checked_at": "last week"}`, 1)
	bad = strings.Replace(bad, `"examples": ["Search"]`, `"examples": ["Missing"]`, 1)
	if err := json.Unmarshal([]byte(bad), &in); err != nil {
		t.Fatal(err)
	}
	_, errs, _ = catalog.Check(&in)
	got := strings.Join(errs, "\n")
	for _, want := range []string{"per must be second", "checked_at must be a date", `"Missing", which is not an example`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestBuilderPromptAsksForOffers(t *testing.T) {
	for _, want := range []string{"offers:", "pricing page", "source_url", "checked_at", "Never guess a price"} {
		if !strings.Contains(specGuide, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	if !strings.Contains(examplesBlock(), `"offers"`) || !strings.Contains(examplesBlock(), "TinySearch") {
		t.Error("worked examples should show offers")
	}
}
