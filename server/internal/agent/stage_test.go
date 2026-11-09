package agent

import (
	"encoding/json"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

func TestStagedPartsMergeIntoTheDraft(t *testing.T) {
	st := &staged{}
	mergeStaged(st, &staged{
		Hosts:     []providers.Host{{Name: "a", URL: "https://a.omkar.cloud"}, {Name: "b", URL: "https://b.omkar.cloud"}},
		Endpoints: []providers.GuideEndpoint{{Group: "A", Name: "Search", Method: "GET", Path: "/~a/search"}},
	})
	// A second batch replaces by key and adds the rest.
	mergeStaged(st, &staged{Hosts: []providers.Host{{Name: "b", URL: "https://b.omkar.cloud", Title: "B"}, {Name: "c", URL: "https://c.omkar.cloud"}}})
	if len(st.Hosts) != 3 || st.Hosts[1].Title != "B" {
		t.Fatalf("hosts after two batches: %+v", st.Hosts)
	}
	st.Hosts = dropBy(st.Hosts, []string{"c"}, func(h providers.Host) string { return h.Name })
	if len(st.Hosts) != 2 {
		t.Fatalf("remove failed: %+v", st.Hosts)
	}

	// The draft's own items win over staged ones with the same key.
	draft, _ := json.Marshal(providers.Spec{
		BaseURL: "https://a.omkar.cloud", Auth: providers.Auth{Type: "header", Name: "API-Key"},
		Hosts: []providers.Host{{Name: "a", URL: "https://a.omkar.cloud", Title: "Draft A"}},
		Guide: &providers.Guide{Summary: "x"},
	})
	merged, err := providers.DecodeSpec(withStaged(draft, st))
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Hosts) != 2 || merged.Hosts[0].Title != "Draft A" {
		t.Fatalf("merged hosts: %+v", merged.Hosts)
	}
	if merged.Guide == nil || len(merged.Guide.Endpoints) != 1 || merged.Guide.Endpoints[0].Group != "A" {
		t.Fatalf("merged endpoints: %+v", merged.Guide)
	}
	// Nothing staged leaves the draft byte for byte.
	if string(withStaged(draft, nil)) != string(draft) {
		t.Fatal("an empty stage changed the draft")
	}
}
