package worker

import (
	"context"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestWorkerFoldsEndpointStats: proxied calls land on the catalog's
// templates, and paths no entry covers get a generic one.
func TestWorkerFoldsEndpointStats(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	providers.Set("evidence-test", &providers.Spec{BaseURL: "https://api.example.com",
		Guide: &providers.Guide{Endpoints: []providers.GuideEndpoint{{Name: "User", Method: "GET", Path: "/v1/users/{id}"}}}})
	defer providers.Remove("evidence-test")
	sv, err := st.CreateService(ctx, &store.Service{Slug: "ev", Name: "Ev", Provider: "evidence-test", BaseURL: "https://api.example.com",
		AuthType: "bearer", Strategy: "round_robin", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: sv.ID, StatusCode: 200, LatencyMS: 120, Detail: "GET /v1/users/42"})
	}
	st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: sv.ID, StatusCode: 502, LatencyMS: 30, Detail: "GET /v1/users/7"})
	st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: sv.ID, StatusCode: 200, LatencyMS: 30, Detail: "GET /v2/orders/123456"})
	w := &Worker{Store: st}
	w.FoldEndpointStats(ctx)
	ev := st.Evidence(ctx)
	got := ev.Endpoint(sv.ID, "GET", "/v1/users/{id}")
	if got == nil || got.OK != 25 || got.Fail != 1 || !got.Enough() {
		t.Fatalf("template stats %+v", got)
	}
	if ev.Endpoint(sv.ID, "GET", "/v2/orders/{id}") == nil {
		t.Fatal("the uncovered path has no generic row")
	}
}
