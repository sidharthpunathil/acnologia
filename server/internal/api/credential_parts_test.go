package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// A provider that signs in with a login and a password (DataForSEO uses
// Basic auth): the key is added as two parts, stored as one, and the proxy
// sends it as Authorization: Basic, replacing the app's own token.
func TestBasicAuthServiceTakesALoginAndPassword(t *testing.T) {
	e, _ := newFeatureEnv(t)
	var mu sync.Mutex
	seen := []string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(ts.Close)

	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "seo", "name": "SEO",
		"base_url": ts.URL, "auth_type": "basic"}, 201, &sv)
	if sv.AuthName != "Authorization" {
		t.Fatalf("auth name %q", sv.AuthName)
	}
	var got store.Service
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/services/%d", sv.ID), nil, 200, &got)
	if len(got.CredentialParts) != 2 || got.CredentialParts[0].Key != "login" || got.CredentialParts[1].Key != "password" {
		t.Fatalf("credential parts %+v", got.CredentialParts)
	}

	// A part missing is refused with what is needed.
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID),
		map[string]any{"keys": []map[string]any{{"label": "a", "parts": map[string]string{"login": "me@x.io"}}}}, 400, nil)

	var created struct {
		Keys    []store.Key `json:"keys"`
		Skipped []string    `json:"skipped"`
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID),
		map[string]any{"keys": []map[string]any{{"label": "acct", "parts": map[string]string{"login": "me@x.io", "password": "pw-12345678"}}}}, 201, &created)
	if len(created.Keys) != 1 || created.Keys[0].Hint != "...5678" {
		t.Fatalf("created %+v", created.Keys)
	}
	// The same credential pasted as login:password is a duplicate.
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"text": "me@x.io:pw-12345678"}, 201, &created)
	if len(created.Keys) != 0 || len(created.Skipped) != 1 {
		t.Fatalf("paste of the same credential: %+v", created)
	}

	_, tok := e.app(map[string]any{"name": "app"})
	r, body := call(t, e, "GET", "/proxy/seo/v3/serp", tok)
	if r.StatusCode != 200 {
		t.Fatalf("proxy %d %s", r.StatusCode, body)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("me@x.io:pw-12345678"))
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[len(seen)-1] != want {
		t.Fatalf("upstream saw %v, want %q", seen, want)
	}
}
