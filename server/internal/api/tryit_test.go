package api

import (
	"strings"
	"testing"
)

type tryOut struct {
	Status    int               `json:"status"`
	LatencyMS int64             `json:"latency_ms"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	App       struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
	} `json:"app"`
}

func TestTryItRunsThroughTheProxy(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "try-secret-key-0001"})

	var out tryOut
	e.do(e.super, "POST", "/api/admin/services/1/try", map[string]any{"method": "GET", "path": "/search.json?q=coffee"}, 200, &out)
	if out.Status != 200 || out.App.Name != "Try it" || out.Headers["X-Acnologia-Service"] != "serpapi" {
		t.Fatalf("try: %+v", out)
	}
	// The fake upstream echoes the key it got: the proxy scrubs it.
	if strings.Contains(out.Body, "try-secret-key-0001") || !strings.Contains(out.Body, `"q":"coffee"`) {
		t.Fatalf("body: %s", out.Body)
	}
	// The try is an event of its own, from the Try it app.
	var evs struct {
		Events []struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
			AppID  *int64 `json:"app_id"`
		} `json:"events"`
	}
	e.do(e.super, "GET", "/api/admin/events?kind=try", nil, 200, &evs)
	found := false
	for _, ev := range evs.Events {
		if ev.Kind == "try" && strings.Contains(ev.Detail, "GET /search.json (Try it)") && ev.AppID != nil && *ev.AppID == out.App.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no try event: %+v", evs)
	}

	// Read-only: a write is refused before it reaches the provider.
	e.do(e.super, "POST", "/api/admin/services/1/try", map[string]any{"method": "POST", "path": "/search.json", "body": `{"a":1}`, "read_only": true}, 200, &out)
	if out.Status != 403 || !strings.Contains(out.Body, "read_only_token") {
		t.Fatalf("read only: %+v", out)
	}
	// An app that may not use the service gets its refusal.
	other, _ := e.app(map[string]any{"name": "other", "allowed_services": []string{"nope"}, "mode": "proxy"})
	e.do(e.super, "POST", "/api/admin/services/1/try", map[string]any{"path": "/search.json", "app_id": other}, 200, &out)
	if out.Status != 403 || !strings.Contains(out.Body, "service_not_allowed") || out.App.Name != "other" {
		t.Fatalf("deny: %+v", out)
	}
	// A body must be JSON.
	e.do(e.super, "POST", "/api/admin/services/1/try", map[string]any{"method": "POST", "path": "/x", "body": "not json"}, 400, nil)
}

func TestTryItNeedsAnAdmin(t *testing.T) {
	e := newEnv(t)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass12", "role": "member"}, 201, nil)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass12"}, 200, nil)
	e.do(m, "POST", "/api/admin/services/1/try", map[string]any{"path": "/search.json"}, 403, nil)
}
