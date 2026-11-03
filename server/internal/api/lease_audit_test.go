package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type reportResp struct {
	Next *leaseDTO `json:"next"`
}

// An app's report on its lease must not move a key an admin disabled back
// into rotation, through the API or MCP.
func TestReportKeepsAdminDisabledKey(t *testing.T) {
	ctx := context.Background()
	for _, outcome := range []string{"quota_exhausted", "invalid", "banned"} {
		t.Run(outcome, func(t *testing.T) {
			e := newEnv(t)
			e.addKeys(map[string]any{"secret": "only0001"})
			appID, tok := e.app(map[string]any{"name": "site", "mode": "lease"})
			e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true}, 200, nil)
			auth := []string{"Authorization", "Bearer " + tok}
			m := &mcpClient{e: e, tok: tok}
			for _, via := range []string{"api", "mcp"} {
				var l leaseDTO
				e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
				e.do(e.super, "PATCH", "/api/admin/keys/"+itoa(int(l.KeyID)), map[string]any{"status": "disabled"}, 200, nil)
				if via == "api" {
					e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": outcome}, 200, nil, auth...)
				} else if out, isErr := m.tool("report_key", map[string]any{"lease_id": l.LeaseID, "outcome": outcome}); isErr {
					t.Fatalf("report_key: %v", out)
				}
				// Even once the reset time a report sets has passed.
				_, _ = e.st.DB.ExecContext(ctx, `UPDATE keys SET quota_reset_at=1 WHERE id=?`, l.KeyID)
				_, _ = e.st.ReactivateExhausted(ctx)
				if k, _ := e.st.GetKey(ctx, l.KeyID); k.Status != store.StatusDisabled {
					t.Fatalf("%s report moved a disabled key to %s", via, k.Status)
				}
				if k, _ := e.st.GetKey(ctx, l.KeyID); k.LastError != "" {
					t.Fatalf("%s report changed a disabled key: %q", via, k.LastError)
				}
				e.do(e.super, "PATCH", "/api/admin/keys/"+itoa(int(l.KeyID)), map[string]any{"status": "active"}, 200, nil)
			}
		})
	}
}

func TestApplyEffectKeepsDisabledAndBanned(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ks := e.addKeys(map[string]any{"secret": "kdis0001"}, map[string]any{"secret": "kban0001"}, map[string]any{"secret": "kact0001"})
	for i, st := range []string{store.StatusDisabled, store.StatusBanned} {
		s := st
		if err := e.st.UpdateKey(ctx, ks[i].ID, store.KeyPatch{Status: &s}); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range ks {
		ex := store.StatusExhausted
		past := time.Now().Add(-time.Hour)
		pp := &past
		if err := e.st.ApplyEffect(ctx, k.ID, store.KeyEffect{Status: &ex, QuotaResetAt: &pp}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = e.st.ReactivateExhausted(ctx)
	want := []string{store.StatusDisabled, store.StatusBanned, store.StatusActive}
	for i, k := range ks {
		got, _ := e.st.GetKey(ctx, k.ID)
		if got.Status != want[i] {
			t.Fatalf("key %d: want %s, got %s", i, want[i], got.Status)
		}
	}
}

// Rotating through report issues a new lease, so it obeys the app's request
// and budget limits like /lease and get_key.
func TestReportRotateObeysAppLimits(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "lim00001"}, map[string]any{"secret": "lim00002"})
	appID, tok := e.app(map[string]any{"name": "site", "mode": "lease"})
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"limits": map[string]any{"daily_requests": 1}}, 200, nil)
	e.do(e.super, "PUT", "/api/admin/apps/"+itoa(int(appID))+"/mcp", map[string]any{"core": true}, 200, nil)
	auth := []string{"Authorization", "Bearer " + tok}
	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 429, nil, auth...)
	var rep reportResp
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "error", "rotate": true}, 200, &rep, auth...)
	if rep.Next != nil {
		t.Fatalf("rotation past the limit issued a lease: %+v", rep.Next)
	}
	// The same through MCP: raise the limit for one lease, then rotate it.
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"limits": map[string]any{"daily_requests": 2}}, 200, nil)
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
	m := &mcpClient{e: e, tok: tok}
	out, isErr := m.tool("report_key", map[string]any{"lease_id": l.LeaseID, "outcome": "error", "rotate": true})
	if isErr || out["next"] != nil {
		t.Fatalf("MCP rotation past the limit: %v", out)
	}
}

// Renewing a lease checks access again: an app that lost lease mode or the
// service, or its grant to hidden keys, cannot keep the key by renewing.
func TestRenewRechecksAccess(t *testing.T) {
	ctx := context.Background()
	revoke := map[string]map[string]any{
		"mode":     {"mode": "proxy"},
		"services": {"allowed_services": []string{"nothing-here"}},
	}
	for name, patch := range revoke {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.addKeys(map[string]any{"secret": "ren00001"})
			appID, tok := e.app(map[string]any{"name": "site", "mode": "both", "allowed_services": []string{"serpapi"}})
			_, tok2 := e.app(map[string]any{"name": "other", "mode": "both"})
			auth := []string{"Authorization", "Bearer " + tok}
			var l leaseDTO
			e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi", "exclusive": true}, 200, &l, auth...)
			e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/renew", map[string]any{"ttl_sec": 600}, 200, nil, auth...)
			e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), patch, 200, nil)
			e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/renew", map[string]any{"ttl_sec": 86400}, 403, nil, auth...)
			if e.api.Pool.ExclusiveHeld(l.KeyID) {
				t.Fatal("the revoked app still holds the key exclusively")
			}
			var l2 leaseDTO
			e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l2, "Authorization", "Bearer "+tok2)
			if ls, _ := e.st.GetLease(ctx, l.LeaseID); ls.Outcome == "" {
				t.Fatal("the revoked lease is still open")
			}
		})
	}
	t.Run("hidden", func(t *testing.T) {
		e := newEnv(t)
		admin := e.adminClient()
		e.addKeys(map[string]any{"secret": "hid00001", "hidden": true})
		appID, tok := e.app(map[string]any{"name": "vip", "mode": "lease", "lease_hidden": true})
		auth := []string{"Authorization", "Bearer " + tok}
		var l leaseDTO
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
		// An admin adds a token: the app loses its hidden key grant.
		e.do(admin, "POST", "/api/admin/apps/"+itoa(int(appID))+"/tokens", map[string]any{"name": "mine"}, 201, nil)
		e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/renew", map[string]any{"ttl_sec": 600}, 403, nil, auth...)
	})
	t.Run("disabled key", func(t *testing.T) {
		e := newEnv(t)
		e.addKeys(map[string]any{"secret": "dis00001"})
		_, tok := e.app(map[string]any{"name": "site", "mode": "lease"})
		auth := []string{"Authorization", "Bearer " + tok}
		var l leaseDTO
		e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, auth...)
		e.do(e.super, "PATCH", "/api/admin/keys/"+itoa(int(l.KeyID)), map[string]any{"status": "disabled"}, 200, nil)
		e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/renew", map[string]any{"ttl_sec": 600}, 403, nil, auth...)
	})
}
