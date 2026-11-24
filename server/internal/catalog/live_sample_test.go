package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// echoKey answers with JSON that quotes the key in several spellings.
type echoKey struct{ secret string }

func (e echoKey) RoundTrip(req *http.Request) (*http.Response, error) {
	js, _ := json.Marshal(e.secret)
	body := `{"data":[{"id":"m1","owner":"acme"},{"id":"m2"},{"id":"m3"}],"auth":` + string(js) +
		`,"next":"https://api.example.com/v1/items?key=` + url.QueryEscape(e.secret) + `"}`
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// TestLiveCheckStoresScrubbedSample: the safe example's answer is stored as
// the provider's sample, keyed by example, with no form of the key in it.
func TestLiveCheckStoresScrubbedSample(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := "sk-live+abc/def123456789"
	spec := providers.Spec{BaseURL: "https://api.example.com/v1", Auth: providers.Auth{Type: "bearer"},
		Examples: []providers.Example{{Name: "List items", Method: "GET", Path: "/items?limit=3", Safe: true}}}
	rep := liveCheck(context.Background(), &http.Client{Transport: echoKey{secret}}, &spec, secret)
	if !rep.Example.OK || rep.Example.Name != "List items" {
		t.Fatalf("report %+v", rep.Example)
	}
	if b, _ := json.Marshal(rep); strings.Contains(string(b), "abc/def") || strings.Contains(string(b), `"sample"`) {
		t.Fatalf("the report carries the sample or the key: %s", b)
	}
	rep.SaveSample(context.Background(), st, "acme", secret)
	got, err := st.ExampleResponses(context.Background(), "acme")
	if err != nil || len(got) != 1 {
		t.Fatalf("samples %v %v", got, err)
	}
	s := got[0]
	if s.Example != "List items" || s.Method != "GET" || s.Status != 200 || s.VerifiedAt.IsZero() {
		t.Fatalf("sample %+v", s)
	}
	for _, form := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), "abc/def", `abc\/def`} {
		if strings.Contains(s.Body, form) {
			t.Fatalf("sample keeps %q: %s", form, s.Body)
		}
	}
	if !strings.Contains(s.Body, `"owner": "acme"`) || !strings.Contains(s.Body, "1 more") {
		t.Fatalf("sample shape: %s", s.Body)
	}
	// A failing example stores nothing.
	rep.Example.OK = false
	rep.Example.Name = "Other"
	rep.SaveSample(context.Background(), st, "acme", secret)
	if got, _ := st.ExampleResponses(context.Background(), "acme"); len(got) != 1 {
		t.Fatal("a failed example was stored")
	}
}
