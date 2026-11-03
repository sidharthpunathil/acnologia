package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type peerProvidersResp struct {
	Providers []peerProviderView `json:"providers"`
}

// acmeSpec is a provider only server A has. The fake upstream reads the key
// from the api_key query parameter and echoes it.
func acmeSpec(base string) map[string]any {
	return map[string]any{
		"base_url": base, "auth": map[string]any{"type": "query", "name": "api_key"},
		"use_standard_rules": true,
		"examples":           []any{map[string]any{"name": "Search", "method": "GET", "path": "/search.json", "query": map[string]string{"q": "coffee"}}},
		"tests":              []any{map[string]any{"name": "ok", "status": 200, "body": "{}", "expect": "ok"}},
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withAcme gives server a the acme provider (with a logo) and an acme
// service with one key, and shares only the service with a new invite.
func withAcme(t *testing.T, a *env) (shareResp, []byte) {
	t.Helper()
	a.do(a.super, "POST", "/api/admin/providers", map[string]any{"slug": "acme", "name": "Acme Search", "category": "Search",
		"color": "#ff5500", "docs_url": "https://docs.acme.example/api", "spec": acmeSpec(a.upURL)}, 201, nil)
	logo := testPNG(t)
	a.do(a.super, "PUT", "/api/admin/providers/acme/logo", map[string]any{"data_base64": base64.StdEncoding.EncodeToString(logo)}, 200, nil)
	var sv store.Service
	a.do(a.super, "POST", "/api/admin/services", map[string]any{"provider": "acme"}, 201, &sv)
	a.do(a.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "acmekey"}}}, 201, nil)
	return a.newShare(map[string]any{"name": "Acme for B", "services": []string{"acme"}}), logo
}

// TestSharedServiceBringsItsProvider: A has provider acme and service acme,
// B has no acme. Sharing only the service imports acme on B as a dependency,
// the local service shows acme, its logo and examples, and a proxied call
// reaches A's upstream.
func TestSharedServiceBringsItsProvider(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	sh, logo := withAcme(t, a)
	ctx := context.Background()

	link := b.connect(sh.Invite, 201)
	if link.LastError != "" {
		t.Fatalf("last error %q", link.LastError)
	}
	// The owner's manifest describes acme although only the service is shared.
	var m peer.Manifest
	if err := json.Unmarshal(link.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Providers) != 1 || m.Providers[0].Slug != "acme" || m.Providers[0].Shared {
		t.Fatalf("manifest providers %+v", m.Providers)
	}
	// B imported acme as a dependency, from the peer.
	var prov store.Provider
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, &prov)
	if prov.Source != "peer:"+link.PeerID || prov.Name != "Acme Search" || prov.Color != "#ff5500" {
		t.Fatalf("imported provider %+v", prov)
	}
	if len(link.Providers) != 0 {
		t.Fatalf("a dependency is not an explicitly shared provider: %v", link.Providers)
	}
	var deps peerProvidersResp
	b.do(b.super, "GET", "/api/admin/peers/"+itoa(int(link.ID))+"/providers", nil, 200, &deps)
	if len(deps.Providers) != 1 || deps.Providers[0].Kind != store.PeerProviderDependency ||
		strings.Join(deps.Providers[0].ForServices, ",") != "acme" || deps.Providers[0].Services["acme"] == 0 || deps.Providers[0].UpdateAvailable {
		t.Fatalf("deps %+v", deps.Providers)
	}

	// The local service keeps acnologia-peer and names acme for display.
	var sv store.Service
	b.do(b.super, "GET", "/api/admin/services/"+itoa(int(link.Services["acme"])), nil, 200, &sv)
	if sv.Provider != providers.PeerSlug || sv.PeerProvider != "acme" || sv.DocProvider() != "acme" {
		t.Fatalf("local service %+v", sv)
	}
	if sv.LogoURL == nil || !strings.HasPrefix(*sv.LogoURL, "/api/logos/providers/acme") {
		t.Fatalf("service logo %v", sv.LogoURL)
	}
	// The owner's logo bytes came over and are served from B.
	got := fetchBytes(t, b, "/api/logos/providers/acme")
	if !bytes.Equal(got, logo) {
		t.Fatal("logo bytes differ from the owner's")
	}
	if got := fetchBytes(t, b, "/api/logos/services/"+itoa(int(sv.ID))); !bytes.Equal(got, logo) {
		t.Fatal("service logo does not fall back to the owner's provider logo")
	}

	// MCP service tools for the peer service come from acme's examples.
	full, err := b.st.GetServiceBySlug(ctx, sv.Slug)
	if err != nil {
		t.Fatal(err)
	}
	tools := b.api.serviceTools(ctx, &store.App{})
	found := false
	for _, tl := range tools {
		if tl.service == full.Slug && tl.example != nil && tl.example.Path == "/search.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tool from acme's examples for %s: %d tools", full.Slug, len(tools))
	}

	// A local app on B calls it through A, which adds A's key.
	_, btok := b.app(map[string]any{"name": "b-app"})
	code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/"+sv.Slug+"/search.json?q=coffee", btok)
	if code != 200 || !strings.Contains(body, `"who":"ACMEKEY"`) || strings.Contains(body, "acmekey") {
		t.Fatalf("proxy through A: %d %s", code, body)
	}

	// Re-sync changes nothing.
	var again linkResp
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &again)
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &again)
	var prov2 store.Provider
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, &prov2)
	if again.LastError != "" || len(again.LocalServices) != 1 || prov2.Version != prov.Version {
		t.Fatalf("re-sync: err %q services %d version %d->%d", again.LastError, len(again.LocalServices), prov.Version, prov2.Version)
	}

	// A edits acme: B reports an update but does not take it on its own.
	a.do(a.super, "PUT", "/api/admin/providers/acme", map[string]any{"name": "Acme Search", "description": "v2", "spec": acmeSpec(a.upURL)}, 200, nil)
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &again)
	b.do(b.super, "GET", "/api/admin/peers/"+itoa(int(link.ID))+"/providers", nil, 200, &deps)
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, &prov2)
	if !deps.Providers[0].UpdateAvailable || prov2.Description == "v2" {
		t.Fatalf("update: %+v desc %q", deps.Providers[0], prov2.Description)
	}
	// A superadmin pulls it.
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/providers/acme/pull", nil, 200, &deps)
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, &prov2)
	if deps.Providers[0].UpdateAvailable || prov2.Description != "v2" {
		t.Fatalf("after pull: %+v desc %q", deps.Providers[0], prov2.Description)
	}
	// A moves the base URL: pulling needs an explicit yes.
	a.do(a.super, "PUT", "/api/admin/providers/acme", map[string]any{"name": "Acme Search", "spec": acmeSpec("https://elsewhere.example")}, 200, nil)
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &again)
	var moved map[string]any
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/providers/acme/pull", nil, 409, &moved)
	if moved["to"] != "https://elsewhere.example" {
		t.Fatalf("base url change %v", moved)
	}
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/providers/acme/pull", map[string]any{"allow_base_url_change": true}, 200, &deps)
	// Admins may look; only a superadmin pulls.
	b.do(b.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	b.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	b.do(admin, "GET", "/api/admin/peers/"+itoa(int(link.ID))+"/providers", nil, 200, nil)
	b.do(admin, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/providers/acme/pull", nil, 403, nil)

	// Disconnect leaves the imported provider in place.
	b.do(b.super, "DELETE", "/api/admin/peers/"+itoa(int(link.ID)), nil, 200, nil)
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, nil)
}

// TestSharedServiceKeepsReceiversOwnProvider: when B already has acme, its
// own spec stays untouched and the service still shows acme.
func TestSharedServiceKeepsReceiversOwnProvider(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	sh, _ := withAcme(t, a)
	b.do(b.super, "POST", "/api/admin/providers", map[string]any{"slug": "acme", "name": "Acme (mine)", "category": "Search",
		"spec": acmeSpec("https://mine.example")}, 201, nil)
	var before store.Provider
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, &before)

	link := b.connect(sh.Invite, 201)
	if link.LastError != "" {
		t.Fatalf("last error %q", link.LastError)
	}
	var after store.Provider
	b.do(b.super, "GET", "/api/admin/providers/acme", nil, 200, &after)
	if after.Version != before.Version || after.Name != "Acme (mine)" || after.Source == "peer:"+link.PeerID || !bytes.Equal(after.Spec, before.Spec) {
		t.Fatalf("own provider changed: %+v", after)
	}
	var deps peerProvidersResp
	b.do(b.super, "GET", "/api/admin/peers/"+itoa(int(link.ID))+"/providers", nil, 200, &deps)
	if len(deps.Providers) != 0 {
		t.Fatalf("own provider recorded as imported: %+v", deps.Providers)
	}
	sv, err := b.st.GetService(context.Background(), link.Services["acme"])
	if err != nil || sv.PeerProvider != "acme" {
		t.Fatalf("service %+v %v", sv, err)
	}
}

// TestPeerProviderLogoOnlyForManifestProviders: the owner serves logos of
// providers the share describes, to the share token only.
func TestPeerProviderLogoOnlyForManifestProviders(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	sh, logo := withAcme(t, a)
	link := b.connect(sh.Invite, 201)
	l, _ := b.st.GetPeerLink(context.Background(), link.ID)
	token, err := b.api.Pool.Box.Decrypt(l.TokenEnc)
	if err != nil {
		t.Fatal(err)
	}
	code, body, hdr := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/providers/acme/logo", token)
	if code != 200 || body != string(logo) || hdr.Get("Content-Type") != "image/png" || !strings.Contains(hdr.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("logo: %d %v", code, hdr)
	}
	// serpapi has a logo on A but is not in this share.
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/providers/serpapi/logo", token); code != 404 {
		t.Fatalf("logo outside the share: %d", code)
	}
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/providers/acme/logo", ""); code != 401 {
		t.Fatalf("logo without a token: %d", code)
	}
	// An ordinary app token is not a share token.
	_, atok := a.app(map[string]any{"name": "plain"})
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/providers/acme/logo", atok); code != 403 {
		t.Fatalf("logo with an app token: %d", code)
	}
	// Revoked: refused.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/providers/acme/logo", token); code != 401 && code != 403 {
		t.Fatalf("logo after revoke: %d", code)
	}
}

// TestPeerServiceToolsUseOwnersProviderExamples: MCP tools for a peer
// service come from its peer provider's examples; with no such provider
// here, the peer spec (no examples) leaves it without generated tools.
func TestPeerServiceToolsUseOwnersProviderExamples(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.do(e.super, "POST", "/api/admin/providers", map[string]any{"slug": "zeta", "name": "Zeta", "spec": acmeSpec(e.upURL)}, 201, nil)
	mk := func(slug, peerProvider string) {
		sv, err := e.st.CreateService(ctx, &store.Service{Slug: slug, Name: slug, Provider: providers.PeerSlug, BaseURL: "https://owner.example/proxy/x",
			AuthType: "bearer", AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 30, MaxAttempts: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.SetServicePeerProvider(ctx, sv.ID, peerProvider); err != nil {
			t.Fatal(err)
		}
	}
	mk("own-zeta", "zeta")
	mk("own-nope", "nope-not-here")
	names := map[string]string{}
	for _, tl := range e.api.serviceTools(ctx, &store.App{}) {
		names[tl.Name] = tl.service
	}
	if names[toolName("own-zeta", "Search")] != "own-zeta" {
		t.Fatalf("no tool from zeta's examples: %v", names)
	}
	for n, svc := range names {
		if svc == "own-nope" {
			t.Fatalf("unexpected tool %s for a service whose provider is missing", n)
		}
	}
}

func fetchBytes(t *testing.T, e *env, path string) []byte {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	resp, err := e.super.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d %s", path, resp.StatusCode, b)
	}
	return b
}
