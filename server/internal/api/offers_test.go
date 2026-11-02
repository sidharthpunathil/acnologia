package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// newOfferEnv sets up a provider with a free, rate limited search offer, a
// priced endpoint and a per second burst limit, plus one service with keys.
func newOfferEnv(t *testing.T, queueSec int, keys ...string) (*env, *scripted, string) {
	t.Helper()
	e, _ := newFeatureEnv(t)
	up := &scripted{hits: map[string]int{}}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	spec := map[string]any{
		"base_url": ts.URL, "auth": map[string]any{"type": "header", "name": "X-Key"}, "use_standard_rules": true,
		"quota": map[string]any{"unit": "requests", "cost_per_request": 0.002},
		"offers": []map[string]any{
			{"name": "Search", "match": map[string]any{"method": "GET", "path": "/search"}, "free": true,
				"price": map[string]any{"amount": 0, "unit": "request"}, "limits": []map[string]any{{"count": 2, "per": "minute"}},
				"source_url": "https://fish.example/pricing", "checked_at": "2026-11-02"},
			{"name": "Burst", "match": map[string]any{"path": "/burst"}, "limits": []map[string]any{{"count": 1, "per": "second"}}},
			{"name": "Paid", "match": map[string]any{"method": "POST", "path": "/paid"}, "price": map[string]any{"amount": 0.03, "unit": "request"}},
		},
		"tests": []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}},
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "fish", "name": "Fish", "category": "Search", "spec": spec}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "fish", "slug": "fish", "base_url": ts.URL, "queue_timeout_sec": queueSec}, 201, nil)
	ks := []map[string]any{}
	for _, k := range keys {
		ks = append(ks, map[string]any{"secret": k})
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": ks}, 201, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	return e, up, tok
}

func TestOfferLimitsPaceKeys(t *testing.T) {
	e, up, tok := newOfferEnv(t, 0, "key-a", "key-b")
	for i := 0; i < 4; i++ {
		if resp, body := call(t, e, "GET", "/proxy/fish/search?q=x", tok); resp.StatusCode != 200 {
			t.Fatalf("request %d: %d %s", i+1, resp.StatusCode, body)
		}
	}
	if up.hits["key-a"] != 2 || up.hits["key-b"] != 2 {
		t.Fatalf("each key should take its 2 a minute: %v", up.hits)
	}
	resp, body := call(t, e, "GET", "/proxy/fish/search?q=x", tok)
	var out struct {
		Error         struct{ Code, Message string }
		RetryAfterSec int `json:"retry_after_sec"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	if resp.StatusCode != 503 || out.Error.Code != "all_keys_busy" || out.RetryAfterSec < 1 || out.RetryAfterSec > 66 ||
		resp.Header.Get("Retry-After") == "" || !strings.Contains(out.Error.Message, "rate limit") {
		t.Fatalf("fifth search: %d %s", resp.StatusCode, body)
	}
	if up.total.Load() != 4 {
		t.Fatalf("the paced request must not reach upstream: %d calls", up.total.Load())
	}
	// Other endpoints are not held back by the search offer.
	if resp, body := call(t, e, "GET", "/proxy/fish/other", tok); resp.StatusCode != 200 {
		t.Fatalf("unrelated endpoint: %d %s", resp.StatusCode, body)
	}
}

func TestOfferLimitWaitsForTheQueue(t *testing.T) {
	e, up, tok := newOfferEnv(t, 3, "only")
	call(t, e, "GET", "/proxy/fish/burst", tok)
	start := time.Now()
	resp, body := call(t, e, "GET", "/proxy/fish/burst", tok)
	if resp.StatusCode != 200 || time.Since(start) < 500*time.Millisecond || up.total.Load() != 2 {
		t.Fatalf("second burst request should wait about a second then pass: %d %s after %v", resp.StatusCode, body, time.Since(start))
	}
}

func TestOfferPricesRequests(t *testing.T) {
	e, _, tok := newOfferEnv(t, 0, "k1")
	call(t, e, "GET", "/proxy/fish/search", tok)    // free offer: 0
	call(t, e, "POST", "/proxy/fish/paid", tok)     // offer price: 0.03
	call(t, e, "GET", "/proxy/fish/elsewhere", tok) // no offer: cost_per_request 0.002
	var u usageResp
	e.do(e.super, "GET", "/api/admin/usage?days=1", nil, 200, &u)
	if got := u.Totals["today"].ProviderCostUSD; math.Abs(got-0.032) > 1e-9 {
		t.Fatalf("provider cost today = %v, want 0.032", got)
	}
	// A service override still wins over offers.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"cost_per_request": 0.5}, 200, nil)
	call(t, e, "GET", "/proxy/fish/search", tok)
	e.do(e.super, "GET", "/api/admin/usage?days=1", nil, 200, &u)
	if got := u.Totals["today"].ProviderCostUSD; math.Abs(got-0.532) > 1e-9 {
		t.Fatalf("override: cost today = %v", got)
	}
}

func TestOfferConcurrencyFeedsSlots(t *testing.T) {
	e, _ := newFeatureEnv(t)
	spec := map[string]any{"base_url": "https://x.example", "auth": map[string]any{"type": "bearer"}, "use_standard_rules": true,
		"offers": []map[string]any{{"name": "Plan", "concurrency": 1}},
		"tests":  []map[string]any{{"name": "429", "status": 429, "body": "", "expect": "rate_limited"}}}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "one", "name": "One", "spec": spec}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "one", "slug": "one", "base_url": "https://x.example"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "s1"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	lease := func(want int) {
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "one"}, want, nil, "Authorization", "Bearer "+tok)
	}
	lease(200)
	lease(503) // the offer's concurrency of 1 holds the only key
}

func TestAssistantProviderOffersTool(t *testing.T) {
	e, s := newFeatureEnv(t)
	cs := &chatSession{s: s, ctx: context.Background(), user: &store.User{ID: 1, Role: store.RoleSuperadmin}}
	out := providerOffersTool(cs, map[string]any{"category": "Search", "free_only": true})
	if !out.OK || !strings.Contains(out.Model, "tinyfish-search") || !strings.Contains(out.Model, "30 requests/minute") || strings.Contains(out.Model, `"slug":"serper"`) {
		t.Fatalf("free search providers: %s", out.Model)
	}
	out = providerOffersTool(cs, map[string]any{"provider": "tinyfish"})
	if !strings.Contains(out.Model, "TinyFetch") || !strings.Contains(out.Model, "1000 urls/day") || !strings.Contains(out.Model, "https://www.tinyfish.ai/pricing") {
		t.Fatalf("tinyfish limits: %s", out.Model)
	}
	if chatToolByName("provider_offers") == nil {
		t.Fatal("provider_offers is not registered")
	}
	_ = e
}

func TestMCPToolDescriptionsMentionOffers(t *testing.T) {
	e, s := newFeatureEnv(t)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "tinyfish-search", "slug": "tfs"}, 201, nil)
	found := false
	for _, tl := range s.serviceTools(context.Background(), &store.App{}) {
		if strings.Contains(tl.Description, "TinySearch is free") && strings.Contains(tl.Description, "30 requests/minute") {
			found = true
		}
	}
	if !found {
		t.Fatal("the TinySearch tool should say it is free and name its limits")
	}
}
