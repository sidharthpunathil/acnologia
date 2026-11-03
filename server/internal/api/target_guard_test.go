package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// putProviderHealth publishes the serpapi provider with its health check
// changed by edit, as c.
func (e *env) putProviderHealth(c *http.Client, edit func(h *providers.Health)) {
	e.t.Helper()
	p, err := e.st.GetProvider(context.Background(), "serpapi")
	if err != nil {
		e.t.Fatal(err)
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		e.t.Fatal(err)
	}
	edit(spec.Health)
	raw, _ := json.Marshal(spec)
	in := catalog.Input{Slug: p.Slug, Name: p.Name, Category: p.Category, Description: p.Description, Website: p.Website,
		DocsURL: p.DocsURL, Color: p.Color, Status: p.Status, Spec: raw}
	e.do(c, "PUT", "/api/admin/providers/serpapi", in, 200, nil)
}

func (e *env) serviceTrusted() bool {
	e.t.Helper()
	sv, err := e.st.GetService(context.Background(), 1)
	if err != nil {
		e.t.Fatal(err)
	}
	return sv.TargetTrusted
}

// checkHidden runs the worker's health check on the hidden key.
func (e *env) checkHidden(id int64) error {
	e.t.Helper()
	ctx := context.Background()
	sv, err := e.st.GetService(ctx, 1)
	if err != nil {
		e.t.Fatal(err)
	}
	k, err := e.st.GetKey(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.api.Pool.Check(ctx, sv, k)
}

func TestAdminProviderEditRevokesTargetTrust(t *testing.T) {
	e := newEnv(t)
	ks := e.addKeys(map[string]any{"secret": "hidden01", "hidden": true})
	hidden := ks[0].ID
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	if !e.serviceTrusted() {
		t.Fatal("service created by a superadmin should start trusted")
	}
	if err := e.checkHidden(hidden); err != nil {
		t.Fatalf("trusted health check: %v", err)
	}

	// A superadmin moving the health check keeps the service trusted.
	e.putProviderHealth(e.super, func(h *providers.Health) { h.Query = map[string]string{"v": "2"} })
	if !e.serviceTrusted() {
		t.Fatal("a superadmin's spec edit revoked trust")
	}

	// An admin changing the health path revokes it, and the next health
	// check leaves the hidden key alone.
	hits := e.up.hits["hidden01"]
	e.putProviderHealth(admin, func(h *providers.Health) { h.Path = "/tenant/account.json" })
	if e.serviceTrusted() {
		t.Fatal("an admin's health.path edit kept the service trusted")
	}
	if err := e.checkHidden(hidden); !errors.Is(err, pool.ErrUntrustedTarget) {
		t.Fatalf("health check after admin edit: %v", err)
	}
	if e.up.hits["hidden01"] != hits {
		t.Fatal("the hidden key reached the upstream after the admin's edit")
	}
	audit, _ := e.st.ListAudit(context.Background(), 50)
	found := false
	for _, a := range audit {
		if a.Action == "service.untrust" && a.Target == "serpapi" && strings.Contains(a.Detail, "health.path") && a.UserEmail == "ad@x.io" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no service.untrust audit entry naming health.path: %+v", audit)
	}

	// Re-trusted by a superadmin, then an admin changes auth placement.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"target_trusted": true}, 200, nil)
	p, _ := e.st.GetProvider(context.Background(), "serpapi")
	spec, _ := providers.DecodeSpec(p.Spec)
	spec.Auth = providers.Auth{Type: "header", Name: "X-Leak"}
	raw, _ := json.Marshal(spec)
	e.do(admin, "PUT", "/api/admin/providers/serpapi", catalog.Input{Slug: "serpapi", Name: p.Name, Category: p.Category, Spec: raw}, 200, nil)
	if e.serviceTrusted() {
		t.Fatal("an admin's auth edit kept the service trusted")
	}

	// An admin edit that leaves auth and the health check alone keeps trust.
	e.do(e.super, "PATCH", "/api/admin/services/1", map[string]any{"target_trusted": true}, 200, nil)
	e.putProviderHealth(admin, func(h *providers.Health) {})
	if !e.serviceTrusted() {
		t.Fatal("an unrelated admin edit revoked trust")
	}

	// A rollback by an admin is an edit like any other.
	var vers struct {
		Version int `json:"version"`
	}
	p, _ = e.st.GetProvider(context.Background(), "serpapi")
	vers.Version = 1
	if p.Version < 2 {
		t.Fatalf("expected several versions, got v%d", p.Version)
	}
	e.do(admin, "POST", "/api/admin/providers/serpapi/rollback", vers, 200, nil)
	if e.serviceTrusted() {
		t.Fatal("an admin's rollback to another health check kept the service trusted")
	}
}

func TestProviderEditRefusesDotSegmentHealthPath(t *testing.T) {
	e := newEnv(t)
	p, _ := e.st.GetProvider(context.Background(), "serpapi")
	for _, path := range []string{"/../../tenant", "/%2e%2e/tenant", "/x/./y"} {
		spec, _ := providers.DecodeSpec(p.Spec)
		spec.Health.Path = path
		raw, _ := json.Marshal(spec)
		e.do(e.super, "PUT", "/api/admin/providers/serpapi", catalog.Input{Slug: "serpapi", Name: p.Name, Spec: raw}, 400, nil)
	}
}
