package providers

import (
	"errors"
	"strings"
	"testing"
)

func hostSpec(hosts ...Host) *Spec {
	return &Spec{
		BaseURL: "https://google-maps-scraper.omkar.cloud",
		Auth:    Auth{Type: "header", Name: "API-Key"},
		Hosts:   hosts,
	}
}

func TestHostSite(t *testing.T) {
	for base, want := range map[string]string{
		"https://google-maps-scraper.omkar.cloud": "omkar.cloud",
		"https://api.example.co.uk/v1":            "example.co.uk",
		"https://example.com":                     "example.com",
		"https://a.b.example.com":                 "b.example.com",
		"http://localhost:8080":                   "localhost",
	} {
		if got := HostSite(base); got != want {
			t.Errorf("HostSite(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestValidateHosts(t *testing.T) {
	ok := hostSpec(
		Host{Name: "tiktok-scraper", URL: "https://tiktok-scraper.omkar.cloud", Title: "TikTok Scraper", Group: "Social & Video"},
		Host{Name: "amazon", URL: "https://amazon-scraper.omkar.cloud/"},
	)
	var errs []string
	ok.validateHosts(func(f string, a ...any) { errs = append(errs, f) })
	if len(errs) > 0 {
		t.Fatalf("valid hosts rejected: %v", errs)
	}

	cases := map[string]Host{
		"other site":  {Name: "evil", URL: "https://evil.example.com"},
		"look alike":  {Name: "evil", URL: "https://evilomkar.cloud"},
		"ip address":  {Name: "ip", URL: "https://10.0.0.1"},
		"bad name":    {Name: "Tik Tok", URL: "https://tiktok-scraper.omkar.cloud"},
		"credentials": {Name: "u", URL: "https://user:pw@x.omkar.cloud"},
		"query":       {Name: "q", URL: "https://x.omkar.cloud/?a=1"},
		"scheme":      {Name: "f", URL: "ftp://x.omkar.cloud"},
	}
	for name, h := range cases {
		var errs []string
		hostSpec(h).validateHosts(func(f string, a ...any) { errs = append(errs, f) })
		if len(errs) == 0 {
			t.Errorf("%s: %+v accepted", name, h)
		}
	}

	var dup []string
	hostSpec(Host{Name: "a", URL: "https://a.omkar.cloud"}, Host{Name: "a", URL: "https://b.omkar.cloud"}).
		validateHosts(func(f string, a ...any) { dup = append(dup, f) })
	if len(dup) == 0 {
		t.Error("duplicate host names accepted")
	}
}

func TestHostsRaiseGuideAndOfferCaps(t *testing.T) {
	s := hostSpec()
	if maxGuideEndpoints(s) != 40 || maxOffers(s) != 50 {
		t.Fatal("caps changed for a spec without hosts")
	}
	s.Hosts = []Host{{Name: "a", URL: "https://a.omkar.cloud"}}
	if maxGuideEndpoints(s) <= 40 || maxOffers(s) <= 50 {
		t.Fatal("a spec with hosts keeps the small caps")
	}
}

func TestResolveHost(t *testing.T) {
	s := hostSpec(Host{Name: "tiktok-scraper", URL: "https://tiktok-scraper.omkar.cloud"})
	h, rest, ok, err := s.ResolveHost("~tiktok-scraper/videos/details")
	if err != nil || !ok || h.Name != "tiktok-scraper" || rest != "videos/details" {
		t.Fatalf("got %v %q %v %v", h, rest, ok, err)
	}
	_, rest, ok, err = s.ResolveHost("/~tiktok-scraper/videos/details")
	if err != nil || !ok || rest != "/videos/details" {
		t.Fatalf("leading slash: %q %v %v", rest, ok, err)
	}
	_, rest, ok, err = s.ResolveHost("~tiktok-scraper")
	if err != nil || !ok || rest != "" {
		t.Fatalf("bare host: %q %v %v", rest, ok, err)
	}
	if _, rest, ok, err = s.ResolveHost("search"); err != nil || ok || rest != "search" {
		t.Fatalf("plain path changed: %q %v %v", rest, ok, err)
	}
	if _, _, _, err = s.ResolveHost("~nope/x"); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("unknown host: %v", err)
	}
	// A spec without hosts leaves "~" paths alone, as before hosts existed.
	if _, rest, ok, err = hostSpec().ResolveHost("~user/x"); err != nil || ok || rest != "~user/x" {
		t.Fatalf("no hosts: %q %v %v", rest, ok, err)
	}
}

func TestHostSetChanged(t *testing.T) {
	a := hostSpec(Host{Name: "a", URL: "https://a.omkar.cloud", Title: "A"})
	b := hostSpec(Host{Name: "a", URL: "https://a.omkar.cloud", Title: "Renamed"})
	if HostSetChanged(a, b) {
		t.Error("a title change counted as a target change")
	}
	c := hostSpec(Host{Name: "a", URL: "https://other.omkar.cloud"})
	if !HostSetChanged(a, c) {
		t.Error("a URL change was missed")
	}
	if !HostSetChanged(a, hostSpec()) {
		t.Error("removing hosts was missed")
	}
}

func TestGuideRendersHostsAndGroups(t *testing.T) {
	s := hostSpec(
		Host{Name: "tiktok-scraper", URL: "https://tiktok-scraper.omkar.cloud", Title: "TikTok Scraper", Group: "Social & Video", Description: "Videos and users"},
		Host{Name: "amazon-scraper", URL: "https://amazon-scraper.omkar.cloud", Title: "Amazon Scraper", Group: "E-commerce"},
	)
	s.Guide = &Guide{Summary: "Scrapers.", Endpoints: []GuideEndpoint{
		{Group: "TikTok Scraper", Name: "Video details", Method: "GET", Path: "/~tiktok-scraper/videos/details"},
		{Group: "Amazon Scraper", Name: "Search", Method: "GET", Path: "/~amazon-scraper/search"},
		{Group: "TikTok Scraper", Name: "User profile", Method: "GET", Path: "/~tiktok-scraper/users/profile"},
	}}
	md := RenderGuide(GuideInput{Meta: GuideMeta{Slug: "omkar", Name: "Omkar Cloud"}, Spec: s, Ctx: GuideContext{Service: "omkar", PublicURL: "https://k.example"}})
	for _, want := range []string{"## APIs", "### Social & Video", "`/~tiktok-scraper`", "### E-commerce", "### TikTok Scraper", "#### User profile"} {
		if !strings.Contains(md, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
	// Endpoints of one group stay together.
	if strings.Index(md, "#### User profile") > strings.Index(md, "### Amazon Scraper") {
		t.Error("grouped endpoints were not kept together")
	}
	doc := BuildGuide(GuideInput{Meta: GuideMeta{Slug: "omkar"}, Spec: s})
	if len(doc.Hosts) != 2 || doc.Hosts[0].Prefix != "/~tiktok-scraper" {
		t.Errorf("guide doc hosts: %+v", doc.Hosts)
	}
}
