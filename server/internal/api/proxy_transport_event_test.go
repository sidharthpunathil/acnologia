package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestProxyTransportFailureNamesTheCall: a call the provider never answered
// logs its method and path, so the evidence fold can count it against that
// endpoint. The key stays scrubbed.
func TestProxyTransportFailureNamesTheCall(t *testing.T) {
	e := newEnv(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close() // nothing listens there any more
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "gone", "base_url": url}, 201, &sv)
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "serp-transport-key-1"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "t", "mode": "proxy"})
	req, _ := http.NewRequest("GET", e.srv.URL+"/proxy/gone/search.json?q=secret-query", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	var detail string
	deadline := time.Now().Add(3 * time.Second)
	for detail == "" && time.Now().Before(deadline) {
		evs, _ := e.st.ListEvents(context.Background(), store.EventFilter{ServiceID: sv.ID, Kind: "error"})
		for _, ev := range evs {
			if strings.HasPrefix(ev.Detail, "upstream: ") {
				detail = ev.Detail
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.HasPrefix(detail, "upstream: GET /search.json: ") || strings.Contains(detail, "serp-transport-key-1") {
		t.Fatalf("transport error detail %q", detail)
	}
	if m, p, ok := store.ParseTransportFailure(detail); !ok || m != "GET" || p != "/search.json" {
		t.Fatalf("parsed as %q %q %v", m, p, ok)
	}
}
