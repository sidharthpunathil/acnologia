package api

import (
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// emptyEnv is newEnv without the serpapi service it adds.
func emptyEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.do(e.super, "DELETE", "/api/admin/services/1", nil, 200, nil)
	return e
}

type demoLoaded struct {
	Service struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	} `json:"service"`
	App struct {
		ID int64 `json:"id"`
	} `json:"app"`
	Token string `json:"token"`
	Try   struct {
		Path string `json:"path"`
		Curl string `json:"curl"`
	} `json:"try"`
}

func TestDemoDataFirstCallAndRemove(t *testing.T) {
	e := emptyEnv(t)
	var st map[string]any
	e.do(e.super, "GET", "/api/admin/demo", nil, 200, &st)
	if st["loaded"] != false {
		t.Fatalf("state before: %v", st)
	}
	var d demoLoaded
	e.do(e.super, "POST", "/api/admin/demo", nil, 201, &d)
	if d.Service.Slug != "demo" || !strings.HasPrefix(d.Token, store.AppTokenPrefix) || !strings.Contains(d.Try.Curl, "/proxy/demo/v1/hello") {
		t.Fatalf("loaded: %+v", d)
	}
	// Loading twice is refused.
	e.do(e.super, "POST", "/api/admin/demo", map[string]any{"force": true}, 409, nil)

	// The first call: the app sends only its token, the proxy adds a demo key.
	var hello map[string]any
	e.do(client(), "GET", "/proxy/demo/v1/hello", nil, 200, &hello, "X-Acnologia-Token", d.Token)
	if !strings.Contains(hello["message"].(string), "Hello") || !strings.HasPrefix(hello["key"].(string), "Demo key") {
		t.Fatalf("hello: %v", hello)
	}
	var echo map[string]any
	e.do(client(), "POST", "/proxy/demo/v1/echo", map[string]any{"a": 1}, 200, &echo, "X-Acnologia-Token", d.Token)
	if echo["you_sent"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("echo: %v", echo)
	}
	// The fake upstream answers no one without a demo key.
	e.do(client(), "GET", "/api/v1/demo/upstream/v1/hello", nil, 401, nil)
	e.do(client(), "GET", "/api/v1/demo/upstream/v1/hello", nil, 401, nil, "X-Demo-Key", "demo_guess")

	// Members and admins cannot load or remove it.
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "a@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "a@x.io", "password": "adminpass12"}, 200, nil)
	e.do(admin, "DELETE", "/api/admin/demo", nil, 403, nil)

	// DELETE removes the service, its keys and the app.
	e.do(e.super, "DELETE", "/api/admin/demo", nil, 200, nil)
	if _, err := e.st.GetService(t.Context(), d.Service.ID); err == nil {
		t.Fatal("demo service left behind")
	}
	if _, err := e.st.GetApp(t.Context(), d.App.ID); err == nil {
		t.Fatal("demo app left behind")
	}
	e.do(client(), "GET", "/api/v1/demo/upstream/v1/hello", nil, 404, nil)
	e.do(e.super, "DELETE", "/api/admin/demo", nil, 404, nil)
}

func TestDemoDataNeedsAnEmptyServerOrForce(t *testing.T) {
	e := newEnv(t) // has the serpapi service
	e.do(e.super, "POST", "/api/admin/demo", nil, 409, nil)
	var d demoLoaded
	e.do(e.super, "POST", "/api/admin/demo", map[string]any{"force": true}, 201, &d)
	if d.Service.Slug != "demo" {
		t.Fatalf("forced: %+v", d)
	}
	// The other service stays when the demo goes.
	e.do(e.super, "DELETE", "/api/admin/demo", nil, 200, nil)
	if _, err := e.st.GetService(t.Context(), 1); err != nil {
		t.Fatal("removing the demo took another service with it")
	}
}
