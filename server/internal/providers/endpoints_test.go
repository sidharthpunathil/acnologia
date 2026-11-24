package providers

import "testing"

func TestMatchEndpoint(t *testing.T) {
	s := &Spec{
		Examples: []Example{
			{Name: "List models", Method: "GET", Path: "/v1/models?limit=5"},
			{Name: "One model", Method: "GET", Path: "/v1/models/gpt-6"},
		},
		Guide: &Guide{Endpoints: []GuideEndpoint{
			{Name: "Model", Method: "GET", Path: "/v1/models/{model}"},
			{Name: "Generate", Method: "POST", Path: "/v1beta/models/{model}:generateContent"},
			{Name: "Archive", Method: "GET", Path: "/searches/{id}.json"},
		}},
	}
	cases := []struct{ method, path, want string }{
		{"GET", "/v1/models", "/v1/models"},
		{"GET", "/v1/models/", "/v1/models"},
		{"GET", "/v1/models/gpt-6", "/v1/models/gpt-6"},
		{"GET", "/v1/models/claude-x", "/v1/models/{model}"},
		{"POST", "/v1beta/models/gemini-2.5-flash:generateContent", "/v1beta/models/{model}:generateContent"},
		{"POST", "/v1beta/models/gemini-2.5-flash%3AgenerateContent", "/v1beta/models/{model}:generateContent"},
		{"GET", "/searches/abc.json", "/searches/{id}.json"},
		{"MCP", "search_web", "search_web"},
	}
	for _, c := range cases {
		got, ok := s.MatchEndpoint(c.method, c.path)
		if !ok || got != c.want {
			t.Errorf("%s %s: got %q %v, want %q", c.method, c.path, got, ok, c.want)
		}
	}
	if _, ok := s.MatchEndpoint("POST", "/v1/models"); ok {
		t.Error("a method the catalog does not list matched")
	}
	if got, ok := s.EndpointKey("GET", "/v2/users/12345/posts/3f2a1c9e-0b1d-4c55-9a7e-1d2c3b4a5f60"); ok || got != "/v2/users/{id}/posts/{id}" {
		t.Errorf("generic template %q %v", got, ok)
	}
	if !MatchesTemplate("/v1/models/{model}", "/v1/models/x") || MatchesTemplate("/v1/models/{model}", "/v1/models/x/y") {
		t.Error("MatchesTemplate")
	}
}
