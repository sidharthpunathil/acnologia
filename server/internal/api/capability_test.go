package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// mapsCapability is "search places" on two providers with different APIs:
// one takes ?query&limit on /search and answers {results:[{name,rating}]},
// the other takes a JSON body on /v1/places and answers {places:[{title,stars}]}.
func mapsCapability(strategy string) map[string]any {
	return map[string]any{
		"slug": "maps-search", "name": "Search places", "strategy": strategy,
		"params": []map[string]any{
			{"name": "query", "type": "string", "required": true},
			{"name": "limit", "type": "integer", "default": 5},
			{"name": "country", "type": "string"},
		},
		"routes": []map[string]any{
			{"service": "omkar", "path": "/search", "query": map[string]string{"query": "{{query}}", "limit": "{{limit}}", "country_code": "{{country}}"},
				"output": map[string]any{"items": "results", "fields": map[string]string{"name": "name", "rating": "rating"}}},
			{"service": "serp", "method": "POST", "path": "/v1/places", "body": map[string]any{"q": "{{query}}", "num": "{{limit}}", "gl": "{{country}}"},
				"output": map[string]any{"items": "places", "fields": map[string]string{"name": "title", "rating": "stars"}}},
		},
	}
}

func TestCapabilityTranslatesAndFallsBack(t *testing.T) {
	e, _ := newFeatureEnv(t)
	omkar, _ := e.upstream("omkar", nil, "o1")
	serp, _ := e.upstream("serp", nil, "s1")
	var omkarQuery, serpBody string
	omkar.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		omkarQuery = r.URL.RawQuery
		return 200, nil, `{"results":[{"name":"Cafe A","rating":4.5,"extra":1}]}`
	}
	serp.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		serpBody = string(b[:n])
		return 200, nil, `{"places":[{"title":"Cafe B","stars":4}]}`
	}
	e.do(e.super, "POST", "/api/admin/capabilities", mapsCapability("priority"), 201, nil)
	_, tok := e.app(map[string]any{"name": "app", "allowed_services": []string{"maps-search"}})

	// The first route answers, mapped to the capability's fields.
	r, body := call(t, e, "GET", "/proxy/maps-search?query=coffee", tok)
	if r.StatusCode != 200 || r.Header.Get("X-Acnologia-Service") != "omkar" {
		t.Fatalf("first route: %d %s %s", r.StatusCode, r.Header.Get("X-Acnologia-Service"), body)
	}
	if omkarQuery != "limit=5&query=coffee" {
		t.Fatalf("the default limit and the query should be filled, and the missing country left out: %q", omkarQuery)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	items, _ := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "Cafe A" || out["capability"] != "maps-search" || out["service"] != "omkar" {
		t.Fatalf("mapped output: %s", body)
	}
	if _, leaked := items[0].(map[string]any)["extra"]; leaked {
		t.Fatal("fields outside the mapping should be dropped")
	}

	// The first provider runs out of quota: the request moves to the second,
	// whose body is filled with typed values.
	omkar.answer = func(string, *http.Request) (int, map[string]string, string) {
		return 402, nil, `{"error":"quota used up"}`
	}
	r, body = call(t, e, "GET", "/proxy/maps-search?query=tea&limit=3&country=us", tok)
	if r.StatusCode != 200 || r.Header.Get("X-Acnologia-Service") != "serp" || !strings.Contains(body, "Cafe B") {
		t.Fatalf("fallback: %d %s", r.StatusCode, body)
	}
	if serpBody != `{"gl":"us","num":3,"q":"tea"}` {
		t.Fatalf("body template: %s", serpBody)
	}
	if !strings.Contains(r.Header.Get("X-Acnologia-Tried"), "omkar=402") {
		t.Fatalf("tried header: %q", r.Header.Get("X-Acnologia-Tried"))
	}

	// _raw returns the provider's own body; a POST body works like a query.
	r, body = call(t, e, "GET", "/proxy/maps-search?query=tea&_raw=1", tok)
	if !strings.Contains(body, `"places"`) {
		t.Fatalf("raw: %s", body)
	}
	// A missing required param is the caller's mistake.
	if r, _ = call(t, e, "GET", "/proxy/maps-search", tok); r.StatusCode != 400 {
		t.Fatalf("missing query: %d", r.StatusCode)
	}
	// Access to the capability does not grant its services directly.
	if r, _ = call(t, e, "GET", "/proxy/omkar/search", tok); r.StatusCode != 403 {
		t.Fatalf("direct route access: %d", r.StatusCode)
	}
	// An answer the provider understood (404) is the answer, not a reason to move on.
	omkar.answer = func(string, *http.Request) (int, map[string]string, string) { return 404, nil, `{"error":"no such place"}` }
	before := serp.total.Load()
	if r, _ = call(t, e, "GET", "/proxy/maps-search?query=x", tok); r.StatusCode != 404 || serp.total.Load() != before {
		t.Fatalf("404 should be relayed from omkar: %d", r.StatusCode)
	}
}

func TestCapabilityStrategies(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("omkar", nil, "o1")
	e.upstream("serp", nil, "s1")
	var c store.Capability
	e.do(e.super, "POST", "/api/admin/capabilities", mapsCapability("round_robin"), 201, &c)
	_, tok := e.app(map[string]any{"name": "app"})
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		r, _ := call(t, e, "GET", "/proxy/maps-search?query=q", tok)
		seen[r.Header.Get("X-Acnologia-Service")]++
	}
	if seen["omkar"] != 2 || seen["serp"] != 2 {
		t.Fatalf("round robin should alternate: %v", seen)
	}
	// _service pins one route.
	if r, _ := call(t, e, "GET", "/proxy/maps-search?query=q&_service=serp", tok); r.Header.Get("X-Acnologia-Service") != "serp" {
		t.Fatal("_service should pick serp")
	}
	// A provider with no key goes last, whatever the strategy.
	e.upstream("empty", nil)
	e.do(e.super, "PATCH", "/api/admin/capabilities/"+itoa(int(c.ID)), map[string]any{"strategy": "priority", "routes": []map[string]any{
		{"service": "empty", "path": "/search"}, {"service": "serp", "path": "/search"},
	}}, 200, nil)
	if r, _ := call(t, e, "GET", "/proxy/maps-search?query=q", tok); r.Header.Get("X-Acnologia-Service") != "serp" {
		t.Fatalf("a keyless route should go last: %s", r.Header.Get("X-Acnologia-Service"))
	}
}

func TestCapabilityValidation(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("omkar", nil, "o1")
	bad := []map[string]any{
		{"slug": "x", "name": "X", "routes": []map[string]any{}},
		{"slug": "x", "name": "X", "routes": []map[string]any{{"service": "nope", "path": "/a"}}},
		{"slug": "x", "name": "X", "routes": []map[string]any{{"service": "omkar", "path": "/a", "query": map[string]string{"q": "{{undeclared}}"}}}},
		{"slug": "x", "name": "X", "strategy": "fastest", "routes": []map[string]any{{"service": "omkar", "path": "/a"}}},
		{"slug": "x", "name": "X", "routes": []map[string]any{{"service": "omkar", "path": "a"}}},
	}
	for i, b := range bad {
		e.do(e.super, "POST", "/api/admin/capabilities", b, 400, nil)
		_ = i
	}
	// Services, groups and capabilities share the /proxy namespace.
	e.do(e.super, "POST", "/api/admin/capabilities", map[string]any{"slug": "omkar", "name": "X", "routes": []map[string]any{{"service": "omkar", "path": "/a"}}}, 409, nil)
	e.do(e.super, "POST", "/api/admin/capabilities", map[string]any{"slug": "cap", "name": "X", "routes": []map[string]any{{"service": "omkar", "path": "/a"}}}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "cap", "base_url": "https://example.com", "auth_type": "header", "auth_name": "K"}, 409, nil)
}

func TestGroupRoundRobin(t *testing.T) {
	e, _ := newFeatureEnv(t)
	e.upstream("a", nil, "a1")
	e.upstream("b", nil, "b1")
	e.do(e.super, "POST", "/api/admin/groups", map[string]any{"slug": "both", "name": "Both", "members": []string{"a", "b"}, "strategy": "round_robin"}, 201, nil)
	e.do(e.super, "POST", "/api/admin/groups", map[string]any{"slug": "bad", "name": "Bad", "members": []string{"a", "b"}, "strategy": "weighted"}, 400, nil)
	_, tok := e.app(map[string]any{"name": "app"})
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		r, _ := call(t, e, "GET", "/proxy/both/x", tok)
		seen[r.Header.Get("X-Acnologia-Service")]++
	}
	if seen["a"] != 2 || seen["b"] != 2 {
		t.Fatalf("group round robin: %v", seen)
	}
}

func TestOrderRoutesWeightedAndCheapest(t *testing.T) {
	cands := []routeCand{{idx: 0, cost: 0.01, weight: 1}, {idx: 1, cost: 0.001, weight: 1000}, {idx: 2, cost: 0.005, down: true}}
	if o := orderRoutes("t", StrategyCheapest, cands); o[0].idx != 1 || o[2].idx != 2 {
		t.Fatalf("cheapest: %+v", o)
	}
	first := 0
	for i := 0; i < 200; i++ {
		if orderRoutes("t", StrategyWeighted, cands[:2])[0].idx == 1 {
			first++
		}
	}
	if first < 190 {
		t.Fatalf("weight 1000 should lead almost always, led %d of 200", first)
	}
}

// Each capability is an MCP tool whose arguments are its params.
func TestCapabilityMCPTool(t *testing.T) {
	e, _ := newFeatureEnv(t)
	omkar, _ := e.upstream("omkar", nil, "o1")
	e.upstream("serp", nil, "s1")
	omkar.answer = func(_ string, r *http.Request) (int, map[string]string, string) {
		return 200, nil, `{"results":[{"name":"` + r.URL.Query().Get("query") + `","rating":5}]}`
	}
	e.do(e.super, "POST", "/api/admin/capabilities", mapsCapability("priority"), 201, nil)
	_, tok := e.app(map[string]any{"name": "agent", "allowed_services": []string{"maps-search"}})
	auth := []string{"Authorization", "Bearer " + tok}

	var res map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200, &res, auth...)
	var tool map[string]any
	for _, x := range res["result"].(map[string]any)["tools"].([]any) {
		if m := x.(map[string]any); m["name"] == "maps_search" {
			tool = m
		}
	}
	if tool == nil {
		t.Fatal("no maps_search tool")
	}
	req := tool["inputSchema"].(map[string]any)["required"].([]any)
	if len(req) != 1 || req[0] != "query" {
		t.Fatalf("required params: %v", req)
	}
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "maps_search", "arguments": map[string]any{"query": "bakery", "limit": 2}}}, 200, &res, auth...)
	sc := res["result"].(map[string]any)["structuredContent"].(map[string]any)
	body := sc["body"].(map[string]any)
	if sc["status"].(float64) != 200 || body["service"] != "omkar" || body["items"].([]any)[0].(map[string]any)["name"] != "bakery" {
		t.Fatalf("capability via mcp: %v", sc)
	}
	// The app lists it with its params.
	var list struct {
		Capabilities []map[string]any `json:"capabilities"`
	}
	e.do(http.DefaultClient, "GET", "/api/v1/capabilities", nil, 200, &list, auth...)
	if len(list.Capabilities) != 1 || list.Capabilities[0]["slug"] != "maps-search" {
		t.Fatalf("v1 capabilities: %v", list)
	}
}
