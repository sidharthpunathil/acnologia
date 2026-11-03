package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// proxyTo creates a service at baseURL with one key and proxies a request
// through it.
func (e *env) proxyTo(slug, baseURL string) (int, string) {
	e.t.Helper()
	var sv store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": slug, "base_url": baseURL}, 201, &sv)
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(sv.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "dummy-" + slug}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "app-" + slug})
	req, _ := http.NewRequest("GET", e.srv.URL+"/proxy/"+slug+"/latest/meta-data/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestProxyRefusesMetadataAndLinkLocal(t *testing.T) {
	e := newEnv(t)
	for slug, base := range map[string]string{
		"aws-meta":   "http://169.254.169.254",
		"link-local": "http://[fe80::1]:8080",
		"aws-v6":     "http://[fd00:ec2::254]",
		"alibaba":    "http://100.100.100.200",
	} {
		code, body := e.proxyTo(slug, base)
		if code != 502 || !strings.Contains(body, catalog.ErrMetadataAddr) {
			t.Errorf("%s: want 502 with the refusal, got %d %s", base, code, body)
		}
		// The health check refuses the same address.
		ctx := context.Background()
		sv, err := e.st.GetServiceBySlug(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		keys, _ := e.st.ListKeys(ctx, sv.ID, true)
		if len(keys) != 1 {
			t.Fatalf("%s: %d keys", slug, len(keys))
		}
		if err := e.api.Pool.Check(ctx, sv, keys[0]); err == nil || !strings.Contains(err.Error(), catalog.ErrMetadataAddr) {
			t.Errorf("%s: health check: %v", base, err)
		}
	}
	// A loopback upstream (a model server on the same machine) still works.
	_, tok := e.app(map[string]any{"name": "local"})
	e.addKeys(map[string]any{"secret": "localkey"})
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json?q=a", nil, 200, nil, "Authorization", "Bearer "+tok)
	ks, _ := e.st.ListKeys(context.Background(), 1, true)
	sv, _ := e.st.GetService(context.Background(), 1)
	if err := e.api.Pool.Check(context.Background(), sv, ks[0]); err != nil {
		t.Fatalf("loopback health check: %v", err)
	}
}
