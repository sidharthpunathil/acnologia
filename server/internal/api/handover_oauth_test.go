package api

import (
	"context"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestHandoverRefusesOAuthServices: a handover share would send an OAuth
// account's refresh token to the other server, so a share that includes an
// OAuth service is refused with a typed error, while a proxy share of it
// works. Delivery skips such a service even if a share somehow has it.
func TestHandoverRefusesOAuthServices(t *testing.T) {
	o := newOAuthEnv(t)
	o.setClient()
	o.connect(t, o.path("/oauth/connect"), nil)

	var res struct {
		Error struct {
			Code, Message, Hint string
		} `json:"error"`
	}
	o.do(o.super, "POST", "/api/admin/shares", map[string]any{"name": "ho", "services": []string{"fakeo"}, "mode": "handover"}, 400, &res)
	if res.Error.Code != "oauth_handover_refused" || res.Error.Hint == "" {
		t.Fatalf("handover of an OAuth service: %+v", res.Error)
	}
	var created struct {
		Share struct {
			ID string `json:"id"`
		} `json:"share"`
	}
	o.do(o.super, "POST", "/api/admin/shares", map[string]any{"name": "px", "services": []string{"fakeo"}, "mode": "proxy"}, 201, &created)
	// Turning the pending proxy share into a handover share is refused too.
	o.do(o.super, "PATCH", "/api/admin/shares/"+created.Share.ID, map[string]any{"mode": "handover"}, 400, &res)
	if res.Error.Code != "oauth_handover_refused" {
		t.Fatalf("patch to handover: %+v", res.Error)
	}

	out, err := o.api.handoverKeys(context.Background(), &store.Share{ID: "shr_test", Mode: store.ShareModeHandover, Services: []string{"fakeo"},
		PeerID: "peer", PeerBoxPublicKey: "unused", LeaseSec: 3600})
	if err != nil || len(out.Keys) != 0 || len(out.Services) != 0 {
		t.Fatalf("delivery of an OAuth service: %+v, %v", out, err)
	}
}
