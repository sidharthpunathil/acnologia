package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func guideSpec() *Spec {
	return &Spec{
		BaseURL:  "https://api.acme.test",
		Auth:     Auth{Type: "bearer"},
		Defaults: Defaults{MaxAttempts: 4, CacheTTLSec: 60},
		Quota:    QuotaInfo{Unit: "credits", FreeTier: "1,000 credits a month"},
		Offers: []Offer{{Name: "Search", Match: &OfferMatch{Method: "POST", Path: "/v1/search"},
			Price: &Price{Amount: 0.005, Unit: "search"}, Limits: []Limit{{Count: 60, Per: "minute"}}, Notes: "Basic depth only."}},
		Rules:            []Rule{{Name: "Out of credits", Match: Match{BodyContains: []string{"credits"}}, Outcome: QuotaExhausted}},
		UseStandardRules: true,
		Examples: []Example{
			{Name: "Search", Method: "POST", Path: "/v1/search", Body: map[string]any{"query": "it's coffee"}},
			{Name: "Item", Method: "GET", Path: "/v1/items/{id}", Query: map[string]string{"lang": "en", "fields": "a,b"}},
		},
	}
}

func guideMeta() GuideMeta {
	return GuideMeta{Slug: "acme", Name: "Acme", Description: "Search the Acme index.", DocsURL: "https://docs.acme.test", Website: "javascript:alert(1)"}
}

func TestRenderGuideFallback(t *testing.T) {
	in := GuideInput{Meta: guideMeta(), Spec: guideSpec(), Ctx: GuideContext{Service: "acme-prod", PublicURL: "https://acn.test/"}}
	d := BuildGuide(in)
	if !d.Generated || d.Summary != "Search the Acme index." || d.ProxyBase != "https://acn.test/proxy/acme-prod" {
		t.Fatalf("doc = %+v", d)
	}
	if len(d.Endpoints) != 2 || d.Endpoints[1].Params[0].Name != "id" || d.Endpoints[1].Params[0].In != "path" {
		t.Fatalf("endpoints = %+v", d.Endpoints)
	}
	if d.Website != "" {
		t.Fatal("a non http website must be dropped")
	}
	md := RenderGuide(in)
	for _, want := range []string{
		"# Acme usage guide\n\nSearch the Acme index.",
		"generated from the provider's examples",
		"- Base URL: `https://acn.test/proxy/acme-prod`",
		"`Authorization: Bearer acn_...`",
		"Do not send one",
		"`proxy_request` with `service=acme-prod`",
		"`call_service`",
		"### Item\n\n`GET https://acn.test/proxy/acme-prod/v1/items/{id}`",
		"| `lang` | query | string | no | Example value: en |",
		"curl -X POST 'https://acn.test/proxy/acme-prod/v1/search' \\\n  -H \"Authorization: Bearer $ACN_TOKEN\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"query\":\"it'\\''s coffee\"}'",
		"curl 'https://acn.test/proxy/acme-prod/v1/items/{id}?fields=a%2Cb&lang=en'",
		"- Free tier: 1,000 credits a month",
		"- Search (POST /v1/search): $0.005 per search, 60 requests/minute. Basic depth only.",
		"cached for 60 seconds",
		"Out of quota (Out of credits; 402 Payment Required)",
		"up to 4 attempts",
		"503 `no_key_available`",
		"- API reference: https://docs.acme.test",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("guide is missing %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "To get a token") || strings.Contains(md, "Website:") {
		t.Error("app guide must not show admin steps or an unsafe website")
	}
	for _, r := range md {
		if r == 0x2014 {
			t.Fatal("guide contains an em dash")
		}
	}
}

func TestRenderGuideWritten(t *testing.T) {
	s := guideSpec()
	s.Guide = &Guide{
		Summary: "Acme finds documents.",
		Endpoints: []GuideEndpoint{{Name: "Search", Method: "post", Path: "/v1/search", Description: "Full text search.",
			Params:   []GuideParam{{Name: "query", In: "body", Type: "string", Required: true, Description: "What | to find"}},
			Body:     map[string]any{"query": "coffee"},
			Response: "`results[]` with `title` and `url`."}},
		Notes:    []string{"Pages start at 1."},
		Markdown: "## Extra\n\nMore text.",
	}
	in := GuideInput{Meta: guideMeta(), Spec: s, Ctx: GuideContext{PublicURL: "https://acn.test", Admin: true}}
	d := BuildGuide(in)
	if d.Generated || d.Summary != "Acme finds documents." || len(d.Endpoints) != 1 || d.Service != "" {
		t.Fatalf("doc = %+v", d)
	}
	md := RenderGuide(in)
	for _, want := range []string{
		"# Acme usage guide\n\nAcme finds documents.\n\n## How to call",
		"https://acn.test/proxy/{service}",
		"Replace `{service}`",
		"To get a token",
		"`POST https://acn.test/proxy/{service}/v1/search`",
		"| `query` | body | string | yes | What \\| to find |",
		"```json\n{\n  \"query\": \"coffee\"\n}\n```",
		"Response: `results[]` with `title` and `url`.",
		"## Notes\n\n- Pages start at 1.",
		"## Extra\n\nMore text.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("guide is missing %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "generated from") {
		t.Error("a written guide is not generated")
	}
	// The JSON form round trips.
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var back GuideDoc
	if err := json.Unmarshal(b, &back); err != nil || back.Endpoints[0].Params[0].Name != "query" || len(back.Examples) != 2 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

func TestRenderGuideEmptySpec(t *testing.T) {
	md := RenderGuide(GuideInput{Meta: GuideMeta{Slug: "bare", Name: "Bare"}, Ctx: GuideContext{Service: "bare"}})
	for _, want := range []string{"# Bare usage guide", "Bare through Acnologia.", "## Errors and retries"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
}

func TestBuiltinGuidesRender(t *testing.T) {
	for _, b := range Builtins() {
		s := b.Spec
		md := RenderGuide(GuideInput{Meta: GuideMeta{Slug: b.Slug, Name: b.Name, Description: b.Description, DocsURL: b.DocsURL}, Spec: &s, Ctx: GuideContext{Service: b.Slug, PublicURL: "https://acn.test"}})
		if !strings.HasPrefix(md, "# "+b.Name+" usage guide") || !strings.Contains(md, "/proxy/"+b.Slug) {
			t.Errorf("%s: bad guide", b.Slug)
		}
	}
}
