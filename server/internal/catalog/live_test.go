package catalog

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// hostRecorder answers every request with 200 {} and records where it went.
type hostRecorder struct {
	mu   sync.Mutex
	urls []string
}

func (r *hostRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
}

func TestLiveCheckKeepsTheKeyOnBaseHost(t *testing.T) {
	auth := providers.Auth{Type: "bearer"}
	cases := map[string]providers.Spec{
		"dot path":      {BaseURL: "https://api.example.com", Auth: auth, Health: &providers.Health{Method: "GET", Path: ".attacker.test/c"}},
		"userinfo path": {BaseURL: "https://api.example.com", Auth: auth, Health: &providers.Health{Method: "GET", Path: "@attacker.test/c"}},
		"origin path":   {BaseURL: "https://api.example.com/v1", Auth: auth, Health: &providers.Health{Method: "GET", Path: "@attacker.test/c", FromOrigin: true}},
		"delete health": {BaseURL: "https://api.example.com", Auth: auth, Health: &providers.Health{Method: "DELETE", Path: "/account"}},
		"example path": {BaseURL: "https://api.example.com", Auth: auth,
			Examples: []providers.Example{{Name: "x", Method: "GET", Path: "@attacker.test/c", Safe: true}}},
		"userinfo base": {BaseURL: "https://api.example.com@attacker.test", Auth: auth, Health: &providers.Health{Method: "GET", Path: "/x"}},
	}
	for name, spec := range cases {
		rec := &hostRecorder{}
		rep := liveCheck(context.Background(), &http.Client{Transport: rec}, &spec, "sk-secret")
		if len(rec.urls) != 0 {
			t.Errorf("%s: sent %v", name, rec.urls)
		}
		if rep.Example.Ran && rep.Example.OK {
			t.Errorf("%s: example reported ok", name)
		}
	}
	// A well formed spec still runs, on its own host.
	rec := &hostRecorder{}
	spec := providers.Spec{BaseURL: "https://api.example.com/v1", Auth: auth, Health: &providers.Health{Method: "GET", Path: "/me"},
		Examples: []providers.Example{{Name: "x", Method: "GET", Path: "/items", Safe: true}}}
	rep := liveCheck(context.Background(), &http.Client{Transport: rec}, &spec, "sk-secret")
	if !rep.OK || len(rec.urls) != 2 || !strings.HasPrefix(rec.urls[0], "https://api.example.com/v1/me") {
		t.Fatalf("good spec: %+v %v", rep, rec.urls)
	}
}

func TestPinnedTransportRefusesOtherHosts(t *testing.T) {
	rec := &hostRecorder{}
	tr := pinnedTransport{next: rec, scheme: "https", host: "api.example.com"}
	for _, u := range []string{"https://attacker.test/x", "http://api.example.com/x", "https://u@api.example.com/x", "https://api.example.com.attacker.test/"} {
		req, _ := http.NewRequest("GET", u, nil)
		if _, err := tr.RoundTrip(req); err == nil {
			t.Errorf("%s was sent", u)
		}
	}
	if len(rec.urls) != 0 {
		t.Fatalf("sent %v", rec.urls)
	}
}
