package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func newSyncer(t *testing.T, owner *httptest.Server) (*Syncer, *store.PeerLink) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypto.New(bytes.Repeat([]byte{5}, 32))
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	y := &Syncer{Store: st, Box: box, Catalog: cat, Client: func(bool) *http.Client { return owner.Client() }}
	link, err := st.CreatePeerLink(context.Background(), &store.PeerLink{PeerID: "acn_srv_owner", PeerName: "Owner", PeerURL: owner.URL,
		PeerPublicKey: "x", RemoteShareID: "sh1", Status: LinkActive, Prefix: "own"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return y, link
}

func spec(t *testing.T, withTests bool) json.RawMessage {
	t.Helper()
	s := map[string]any{"base_url": "https://api.widget.example", "auth": map[string]any{"type": "header", "name": "X-Key"}, "use_standard_rules": true,
		"examples": []any{map[string]any{"name": "List", "method": "GET", "path": "/items"}}}
	if withTests {
		s["tests"] = []any{map[string]any{"name": "ok", "status": 200, "body": "{}", "expect": "ok"}}
		s["guide"] = map[string]any{"summary": "Widgets as JSON.", "endpoints": []any{map[string]any{"name": "List", "method": "GET", "path": "/items", "description": "All items."}}}
	}
	b, _ := json.Marshal(s)
	return b
}

// An import that fails still creates the service (it works through the
// owner's proxy), records the owner's provider on it and reports why.
// Applying the same manifest again creates nothing new.
func TestApplyImportFailureStillCreatesService(t *testing.T) {
	owner := httptest.NewServer(http.NotFoundHandler())
	defer owner.Close()
	y, link := newSyncer(t, owner)
	ctx := context.Background()
	m := &Manifest{Status: LinkActive,
		Services:  []ManifestService{{Slug: "widgets", Name: "Widgets", Provider: "widget"}},
		Providers: []ManifestProvider{{Slug: "widget", Name: "Widget", Version: 3, Spec: spec(t, false)}}}
	for i := 0; i < 2; i++ {
		if err := y.apply(ctx, link, "acn_token", m); err != nil {
			t.Fatal(err)
		}
		l, _ := y.Store.GetPeerLink(ctx, link.ID)
		if len(l.Services) != 1 || !strings.Contains(l.LastError, "provider widget (for widgets)") {
			t.Fatalf("pass %d: services %v error %q", i, l.Services, l.LastError)
		}
		sv, err := y.Store.GetService(ctx, l.Services["widgets"])
		if err != nil || sv.Provider != providers.PeerSlug || sv.PeerProvider != "widget" {
			t.Fatalf("service %+v %v", sv, err)
		}
		if _, err := y.Store.GetProvider(ctx, "widget"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("broken spec imported: %v", err)
		}
		link = l
	}
	svcs, _ := y.Store.ListServices(ctx)
	n := 0
	for _, s := range svcs {
		if s.PeerLinkID != nil {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d peer services after two syncs", n)
	}

	// The owner fixes the spec: the next sync imports it as a dependency,
	// once, and the error clears.
	m.Providers[0].Spec = spec(t, true)
	for i := 0; i < 2; i++ {
		if err := y.apply(ctx, link, "acn_token", m); err != nil {
			t.Fatal(err)
		}
		link, _ = y.Store.GetPeerLink(ctx, link.ID)
	}
	if link.LastError != "" {
		t.Fatalf("error after fix %q", link.LastError)
	}
	p, err := y.Store.GetProvider(ctx, "widget")
	if err != nil || p.Source != "peer:acn_srv_owner" || p.Version != 1 {
		t.Fatalf("imported %+v %v", p, err)
	}
	// The owner's usage guide comes along with the spec.
	if ps, _ := providers.DecodeSpec(p.Spec); ps == nil || ps.Guide == nil || ps.Guide.Summary != "Widgets as JSON." || len(ps.Guide.Endpoints) != 1 {
		t.Fatalf("guide not imported: %s", p.Spec)
	}
	row, err := y.Store.GetPeerProvider(ctx, link.ID, "widget")
	if err != nil || row.Kind != store.PeerProviderDependency || row.ImportedVersion != 3 || strings.Join(row.ForServices, ",") != "widgets" {
		t.Fatalf("row %+v %v", row, err)
	}
	if len(link.Providers) != 0 {
		t.Fatalf("dependency listed as shared: %v", link.Providers)
	}

	// A newer owner version is noted, never applied on its own.
	m.Providers[0].Version = 4
	m.Providers[0].Description = "changed"
	if err := y.apply(ctx, link, "acn_token", m); err != nil {
		t.Fatal(err)
	}
	p2, _ := y.Store.GetProvider(ctx, "widget")
	row, _ = y.Store.GetPeerProvider(ctx, link.ID, "widget")
	if p2.Version != p.Version || p2.Description == "changed" || row.RemoteVersion != 4 || row.ImportedVersion != 3 {
		t.Fatalf("auto updated: %+v %+v", p2, row)
	}
}

// A service whose provider the owner did not describe is created and the
// gap is reported.
func TestApplyMissingProviderSpecReported(t *testing.T) {
	owner := httptest.NewServer(http.NotFoundHandler())
	defer owner.Close()
	y, link := newSyncer(t, owner)
	ctx := context.Background()
	m := &Manifest{Status: LinkActive, Services: []ManifestService{{Slug: "w", Name: "W", Provider: "ghost"}}}
	if err := y.apply(ctx, link, "acn_token", m); err != nil {
		t.Fatal(err)
	}
	l, _ := y.Store.GetPeerLink(ctx, link.ID)
	if len(l.Services) != 1 || !strings.Contains(l.LastError, "ghost (for w): the owner sent no spec") {
		t.Fatalf("services %v error %q", l.Services, l.LastError)
	}
}

// A manifest service never takes the slug of a local group (both answer at
// /proxy/{slug}), and a slug that is not a valid slug is refused.
func TestApplyServiceSlugAvoidsGroups(t *testing.T) {
	owner := httptest.NewServer(http.NotFoundHandler())
	defer owner.Close()
	y, link := newSyncer(t, owner)
	ctx := context.Background()
	if _, err := y.Store.CreateGroup(ctx, &store.ServiceGroup{Slug: "own-pool", Name: "Pool", Members: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Status: LinkActive, Services: []ManifestService{
		{Slug: "pool", Name: "Pool", Provider: "ghost"},
		{Slug: "Bad/Slug", Name: "Bad", Provider: "ghost"},
	}}
	if err := y.apply(ctx, link, "acn_token", m); err != nil {
		t.Fatal(err)
	}
	l, _ := y.Store.GetPeerLink(ctx, link.ID)
	sv, err := y.Store.GetService(ctx, l.Services["pool"])
	if err != nil || sv.Slug != "own-pool-2" {
		t.Fatalf("service %+v %v", sv, err)
	}
	if _, ok := l.Services["Bad/Slug"]; ok || !strings.Contains(l.LastError, "invalid service slug") {
		t.Fatalf("services %v error %q", l.Services, l.LastError)
	}
	// The handover path checks the same.
	if _, err := y.handoverService(ctx, l, HandoverService{Slug: "Bad/Slug", Provider: "serpapi", AuthType: "query", AuthName: "api_key", BaseURL: "https://serpapi.com"}); err == nil {
		t.Fatal("handover service with an invalid slug accepted")
	}
}

// The owner's logo is checked: wrong type or mismatched bytes are refused.
func TestFetchProviderLogoValidates(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	var ctype string
	var body []byte
	var gotAuth string
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(body)
	}))
	defer owner.Close()
	ctx := context.Background()
	ctype, body = "image/png", png
	if data, mime, err := FetchProviderLogo(ctx, owner.Client(), owner.URL, "acn_tok", "x"); err != nil || mime != "image/png" || !bytes.Equal(data, png) {
		t.Fatalf("good logo: %v %q", err, mime)
	}
	if gotAuth != "Bearer acn_tok" {
		t.Fatalf("auth %q", gotAuth)
	}
	ctype, body = "text/html", png
	if _, _, err := FetchProviderLogo(ctx, owner.Client(), owner.URL, "acn_tok", "x"); err == nil {
		t.Fatal("html content type accepted")
	}
	ctype, body = "image/png", []byte("<html><script>alert(1)</script></html>")
	if _, _, err := FetchProviderLogo(ctx, owner.Client(), owner.URL, "acn_tok", "x"); err == nil {
		t.Fatal("html bytes accepted as png")
	}
	ctype, body = "image/png", append(append([]byte{}, png...), make([]byte, catalog.MaxLogoBytes)...)
	if _, _, err := FetchProviderLogo(ctx, owner.Client(), owner.URL, "acn_tok", "x"); err == nil {
		t.Fatal("oversized logo accepted")
	}
}
