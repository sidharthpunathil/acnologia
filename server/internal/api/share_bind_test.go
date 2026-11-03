package api

import (
	"fmt"
	"testing"
)

// TestShareFollowsTheSharedService: a share names services by slug, so when
// an admin renames the shared service and gives its slug to another service
// or a group, the share must not reach what now holds the slug.
func TestShareFollowsTheSharedService(t *testing.T) {
	e, _ := newFeatureEnv(t)
	_, sv := e.upstream("alpha", nil, "shared-key")
	_, code := e.newPersonShare(map[string]any{"name": "For Dana", "services": []string{"alpha"}})
	stok, _ := e.redeemProxy(code)
	if resp, body := call(t, e, "GET", "/proxy/alpha/x", stok); resp.StatusCode != 200 {
		t.Fatalf("before rename: %d %s", resp.StatusCode, body)
	}

	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", sv.ID), map[string]any{"slug": "alpha-old"}, 200, nil)
	other, _ := e.upstream("alpha", nil, "other-key")
	if resp, body := call(t, e, "GET", "/proxy/alpha/x", stok); resp.StatusCode != 403 || other.total.Load() != 0 {
		t.Fatalf("new service under the shared slug: %d %s", resp.StatusCode, body)
	}

	// A group under the freed slug is never what was shared either.
	_, bsv := e.upstream("beta", nil, "beta-key")
	_, code = e.newPersonShare(map[string]any{"name": "For Eli", "services": []string{"beta"}})
	btok, _ := e.redeemProxy(code)
	e.do(e.super, "PATCH", fmt.Sprintf("/api/admin/services/%d", bsv.ID), map[string]any{"slug": "beta-old"}, 200, nil)
	gamma, _ := e.upstream("gamma", nil, "gamma-key")
	e.do(e.super, "POST", "/api/admin/groups", map[string]any{"slug": "beta", "name": "Beta", "members": []string{"gamma", "alpha"}}, 201, nil)
	if resp, body := call(t, e, "GET", "/proxy/beta/x", btok); resp.StatusCode != 403 || gamma.total.Load() != 0 || other.total.Load() != 0 {
		t.Fatalf("group under the shared slug: %d %s", resp.StatusCode, body)
	}
}
