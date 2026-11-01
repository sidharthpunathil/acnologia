package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cogito-keys/cogito/internal/store"
)

// A made up provider whose quota errors are a 400 with a JSON code, which the
// standard rules would get wrong. Publishing a spec must change proxy
// behavior immediately, with no restart.
func TestCustomProviderHotReload(t *testing.T) {
	e := newEnv(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Acme-Key") == "acme_broke" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"CREDITS_EXHAUSTED","message":"No credits left"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"key":"` + r.Header.Get("X-Acme-Key") + `"}`))
	}))
	defer up.Close()

	spec := func(withRule bool) map[string]any {
		rules := []any{}
		if withRule {
			rules = append(rules, map[string]any{"name": "credits", "match": map[string]any{"status": []int{400}, "json_path": "error.code", "json_equals": "CREDITS_EXHAUSTED"}, "outcome": "quota_exhausted"})
		}
		return map[string]any{
			"base_url": up.URL, "auth": map[string]any{"type": "header", "name": "X-Acme-Key"},
			"use_standard_rules": true, "rules": rules,
			"tests": []any{map[string]any{"name": "ok", "status": 200, "body": "{}", "expect": "ok"}},
		}
	}
	var created struct {
		Provider struct {
			Slug    string `json:"slug"`
			Version int    `json:"version"`
		} `json:"provider"`
	}
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "acme", "name": "Acme", "category": "Data", "color": "#ff0000", "spec": spec(false)}, 201, &created)
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "acme"}, 201, &sv)
	if sv.Slug != "acme" || sv.AuthName != "X-Acme-Key" || sv.BaseURL != up.URL {
		t.Fatalf("service not built from provider spec: %+v", sv)
	}
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys", map[string]any{"keys": []map[string]any{
		{"secret": "acme_broke", "priority": 1}, {"secret": "acme_good", "priority": 2}}}, 201, nil)
	e.do(e.super, "PATCH", "/api/admin/services/"+itoa(int(sv.ID)), map[string]any{"strategy": "fill_first"}, 200, nil)
	_, tok := e.app(map[string]any{"name": "site"})

	// v1: a 400 is the caller's fault, so the broken key's error is relayed.
	e.do(http.DefaultClient, "GET", "/proxy/acme/data", nil, 400, nil, "Authorization", "Bearer "+tok)

	// v2 adds a rule. No restart: the next request rotates past the broken key.
	bad := spec(true)
	bad["tests"] = []any{map[string]any{"name": "credits", "status": 400, "body": `{"error":{"code":"CREDITS_EXHAUSTED"}}`, "expect": "rate_limited"}}
	var fail struct {
		Report struct{ Failed int } `json:"report"`
	}
	e.do(e.super, "PUT", "/api/admin/providers/acme", map[string]any{"name": "Acme", "spec": bad}, 400, &fail)
	if fail.Report.Failed != 1 {
		t.Fatal("a failing fixture must block publishing")
	}
	good := spec(true)
	good["tests"] = []any{map[string]any{"name": "credits", "status": 400, "body": `{"error":{"code":"CREDITS_EXHAUSTED"}}`, "expect": "quota_exhausted"}}
	e.do(e.super, "PUT", "/api/admin/providers/acme", map[string]any{"name": "Acme", "spec": good, "note": "add credits rule"}, 200, &created)
	if created.Provider.Version != 2 {
		t.Fatalf("version = %d", created.Provider.Version)
	}
	e.do(e.super, "PATCH", "/api/admin/keys/1", map[string]any{"status": "active"}, 200, nil)
	var body map[string]any
	e.do(http.DefaultClient, "GET", "/proxy/acme/data", nil, 200, &body, "Authorization", "Bearer "+tok)
	if body["key"] != "acme_good" {
		t.Fatalf("expected rotation to the good key, got %v", body)
	}

	// Versions and rollback.
	var full struct {
		Versions []store.ProviderVersion `json:"versions"`
	}
	e.do(e.super, "GET", "/api/admin/providers/acme", nil, 200, &full)
	if len(full.Versions) != 2 || full.Versions[0].Note != "add credits rule" {
		t.Fatalf("versions: %+v", full.Versions)
	}
	e.do(e.super, "POST", "/api/admin/providers/acme/rollback", map[string]any{"version": 1}, 200, &created.Provider)
	if created.Provider.Version != 3 {
		t.Fatalf("rollback should create v3, got %d", created.Provider.Version)
	}
	// Providers in use cannot be deleted; builtins never.
	e.do(e.super, "DELETE", "/api/admin/providers/acme", nil, 409, nil)
	e.do(e.super, "DELETE", "/api/admin/providers/serpapi", nil, 409, nil)
}

func TestValidateAndLogos(t *testing.T) {
	e := newEnv(t)
	var v struct {
		Errors []string
		Report struct{ Passed, Failed int }
	}
	e.do(e.super, "POST", "/api/admin/providers/validate", map[string]any{"name": "x", "spec": map[string]any{"base_url": "nope", "auth": map[string]any{"type": "query"}}}, 200, &v)
	if len(v.Errors) < 2 {
		t.Fatalf("expected errors, got %v", v.Errors)
	}
	// Builtin logos are seeded and served; services inherit them.
	var list struct {
		Providers []struct {
			Slug    string  `json:"slug"`
			LogoURL *string `json:"logo_url"`
		}
	}
	e.do(e.super, "GET", "/api/admin/providers", nil, 200, &list)
	got := map[string]bool{}
	for _, p := range list.Providers {
		got[p.Slug] = p.LogoURL != nil
	}
	if !got["serpapi"] || !got["elevenlabs"] || !got["generic"] {
		t.Fatalf("logo presence: %v", got)
	}
	var sv store.Service
	e.do(e.super, "GET", "/api/admin/services/1", nil, 200, &sv)
	if sv.LogoURL == nil || !strings.Contains(*sv.LogoURL, "/providers/serpapi") {
		t.Fatalf("service should inherit provider logo: %v", sv.LogoURL)
	}
	resp := e.do(e.super, "GET", *sv.LogoURL, nil, 200, nil)
	if resp.Header.Get("Content-Type") != "image/png" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("logo headers: %v", resp.Header)
	}
	// Upload a service logo; it overrides the provider's.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`
	e.do(e.super, "PUT", "/api/admin/services/1/logo", map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte(svg))}, 200, &sv)
	if sv.LogoURL == nil || !strings.Contains(*sv.LogoURL, "/services/1") {
		t.Fatalf("service logo not set: %v", sv.LogoURL)
	}
	e.do(e.super, "PUT", "/api/admin/services/1/logo", map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte("not an image at all"))}, 400, nil)
	// Logos need a session.
	e.do(http.DefaultClient, "GET", *sv.LogoURL, nil, 401, nil)
	// AI is disabled without a key.
	var st struct{ Enabled bool }
	e.do(e.super, "GET", "/api/admin/ai/status", nil, 200, &st)
	if st.Enabled {
		t.Fatal("AI should be disabled in tests")
	}
	e.do(e.super, "POST", "/api/admin/ai/runs", map[string]any{"kind": "create_provider", "context": "x"}, 503, nil)
	e.do(e.super, "PUT", "/api/admin/ai/rules", map[string]any{"rules": "Always set max_concurrency for voice APIs."}, 200, nil)
	var rules struct {
		Rules     string `json:"rules"`
		UpdatedBy string `json:"updated_by"`
	}
	e.do(e.super, "GET", "/api/admin/ai/rules", nil, 200, &rules)
	if rules.UpdatedBy != "root@x.io" || !strings.Contains(rules.Rules, "voice") {
		t.Fatalf("rules: %+v", rules)
	}
}

func TestBuiltinLogosAndKeyCounts(t *testing.T) {
	e := newEnv(t)
	var list struct {
		Providers []struct {
			Slug      string  `json:"slug"`
			LogoURL   *string `json:"logo_url"`
			KeysCount int     `json:"keys_count"`
		}
	}
	e.addKeys(map[string]any{"secret": "countme1"}, map[string]any{"secret": "hiddenx1", "hidden": true})
	e.do(e.super, "GET", "/api/admin/providers", nil, 200, &list)
	for _, p := range list.Providers {
		if p.LogoURL == nil {
			t.Errorf("built-in provider %s has no logo", p.Slug)
		}
		want := 0
		if p.Slug == "serpapi" {
			want = 1 // hidden keys are not counted
		}
		if p.KeysCount != want {
			t.Errorf("%s keys_count = %d, want %d", p.Slug, p.KeysCount, want)
		}
	}
}
