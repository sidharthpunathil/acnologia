package providers

import (
	"slices"
	"strings"
	"testing"
)

func replicateLike() *Spec {
	return &Spec{BaseURL: "https://api.example.com", Auth: Auth{Type: "bearer"}, Affinity: []AffinityRule{
		{Create: AffinityCreate{Method: "POST", Path: "/v1/predictions$"}, ID: "id", Follow: []AffinityFollow{{Path: "/v1/predictions/{id}"}}},
		{Create: AffinityCreate{Method: "POST", Path: "/v2/acts/*/runs$"}, ID: "data.id", Related: []string{"data.defaultDatasetId"},
			Follow: []AffinityFollow{{Path: "/v2/actor-runs/{id}"}, {Method: "GET", Path: "/v2/datasets/{id}/items$"}}},
		{Create: AffinityCreate{Method: "POST", Path: "/v1/batches$"}, ID: "id", Follow: []AffinityFollow{{Path: "/v1/batches/{id}"}}},
		{Create: AffinityCreate{Method: "POST", Path: "/v1/files$"}, ID: "id", Follow: []AffinityFollow{{Method: "POST", Path: "/v1/batches$", Body: "input_file_id"}}},
	}}
}

func TestMatchAffinity(t *testing.T) {
	s := replicateLike()
	cases := []struct {
		method, path, body string
		follow, create     string
		related            []string
	}{
		{"POST", "/v1/predictions", `{}`, "", "id", nil},
		{"POST", "/v1/predictions/", `{}`, "", "id", nil},
		{"GET", "/v1/predictions/p1", "", "p1", "", nil},
		{"POST", "/v1/predictions/p1/cancel", "", "p1", "", nil},
		{"GET", "/v1/predictions", "", "", "", nil},
		{"POST", "/v2/acts/me~scraper/runs", `{}`, "", "data.id", []string{"data.defaultDatasetId"}},
		{"GET", "/v2/actor-runs/r9", "", "r9", "", []string{"data.defaultDatasetId"}},
		{"GET", "/v2/datasets/d1/items", "", "d1", "", []string{"data.defaultDatasetId"}},
		{"GET", "/v2/datasets/d1/items/extra", "", "", "", nil},
		{"DELETE", "/v2/datasets/d1/items", "", "", "", nil},
		// A request that names one resource and creates another.
		{"POST", "/v1/batches", `{"input_file_id":"file-1"}`, "file-1", "id", nil},
		{"POST", "/v1/batches", `{"input_file_id":42}`, "42", "id", nil},
		{"POST", "/v1/batches", `not json`, "", "id", nil},
		{"POST", "/v1/batches/b1/cancel", ``, "b1", "", nil},
		{"GET", "/v1/predictions/p%2F1", "", "p", "", nil},
	}
	for _, c := range cases {
		h := s.MatchAffinity(c.method, c.path, []byte(c.body))
		if h.FollowID != c.follow || h.CreateID != c.create || !slices.Equal(h.Related, c.related) {
			t.Errorf("%s %s %s: got %+v", c.method, c.path, c.body, h)
		}
	}
	if ids := AffinityIDs([]byte(`{"data":{"id":"run1","defaultDatasetId":"ds1"}}`), "data.id", []string{"data.defaultDatasetId", "data.none"}); !slices.Equal(ids, []string{"run1", "ds1"}) {
		t.Fatalf("ids: %v", ids)
	}
	long := strings.Repeat("x", MaxResourceID+1)
	if ids := AffinityIDs([]byte(`{"id":"`+long+`"}`), "id", nil); len(ids) != 0 {
		t.Fatal("an over long id is not recorded")
	}
	if ids := AffinityIDs([]byte(`data: {"id":"x"}`), "id", nil); len(ids) != 0 {
		t.Fatal("a stream is not read for ids")
	}
}

func TestAffinityValidation(t *testing.T) {
	if errs := replicateLike().Validate(); len(errs) != 0 {
		t.Fatalf("valid rules refused: %v", errs)
	}
	bad := []AffinityRule{
		{Create: AffinityCreate{Method: "FETCH", Path: "v1/x"}, ID: "", Follow: nil},
		{Create: AffinityCreate{Method: "POST", Path: "/v1/x"}, ID: "id", Follow: []AffinityFollow{
			{Path: "/v1/x"},
			{Path: "/v1/x/{id}/{id}"},
			{Path: "/v1/x/pre{id}"},
			{Path: "/v1/x/{id}", Body: "id"},
			{Path: "/v1/x/{id}?a=1"},
		}},
	}
	s := &Spec{BaseURL: "https://api.example.com", Auth: Auth{Type: "bearer"}, Affinity: bad}
	errs := strings.Join(s.Validate(), "\n")
	for _, want := range []string{
		"affinity[0]: create.method", "affinity[0]: create.path", "affinity[0]: id must", "affinity[0]: follow needs",
		"affinity[1].follow[0]: path must hold {id} once", "affinity[1].follow[1]: path must hold {id} once",
		"affinity[1].follow[2]: {id} must be a whole path segment", "affinity[1].follow[3]: a follow with body",
		"affinity[1].follow[4]: path must be a path",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
}

func TestOpenAIAffinity(t *testing.T) {
	var s *Spec
	for _, b := range Builtins() {
		if b.Slug == "openai" {
			s = &b.Spec
		}
	}
	if h := s.MatchAffinity("POST", "/v1/files", nil); h.CreateID != "id" {
		t.Fatalf("file upload: %+v", h)
	}
	if h := s.MatchAffinity("GET", "/v1/files/file-abc/content", nil); h.FollowID != "file-abc" {
		t.Fatalf("file content: %+v", h)
	}
	h := s.MatchAffinity("POST", "/v1/batches", []byte(`{"input_file_id":"file-abc","endpoint":"/v1/responses"}`))
	if h.FollowID != "file-abc" || h.CreateID != "id" {
		t.Fatalf("batch create: %+v", h)
	}
	if h := s.MatchAffinity("GET", "/v1/batches/batch_1", nil); h.FollowID != "batch_1" || !slices.Contains(h.Related, "output_file_id") {
		t.Fatalf("batch poll: %+v", h)
	}
	if h := s.MatchAffinity("POST", "/v1/responses", []byte(`{"model":"x"}`)); h.Grab() || h.FollowID != "" {
		t.Fatalf("a plain call has no affinity: %+v", h)
	}
}
