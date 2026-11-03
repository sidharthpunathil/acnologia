package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// handoverShareView is a share as the dashboard reads it.
type handoverShareView struct {
	store.Share
	Lease    string     `json:"lease"`
	EndsAt   *time.Time `json:"ends_at"`
	Handover *struct {
		Keys []struct {
			KeyID            int64      `json:"key_id"`
			Service          string     `json:"service"`
			Label            string     `json:"label"`
			Live             bool       `json:"live"`
			HandedAt         time.Time  `json:"handed_at"`
			RenewedAt        time.Time  `json:"renewed_at"`
			ValidUntil       time.Time  `json:"valid_until"`
			WipedAt          *time.Time `json:"wiped_at"`
			ReportedRequests *int64     `json:"reported_requests"`
			ReportedStatus   string     `json:"reported_status"`
		} `json:"keys"`
		LastRevokePush *store.RevokePush `json:"last_revoke_push"`
	} `json:"handover"`
	RotateChecklist []struct {
		KeyID    int64  `json:"key_id"`
		Service  string `json:"service"`
		Label    string `json:"label"`
		Provider string `json:"provider"`
	} `json:"rotate_checklist"`
}

type handoverLinkView struct {
	linkResp
	Handover *struct {
		Keys []struct {
			KeyID      int64      `json:"key_id"`
			Service    string     `json:"service"`
			Label      string     `json:"label"`
			ValidUntil *time.Time `json:"valid_until"`
		} `json:"keys"`
	} `json:"handover"`
}

func (e *env) shareView(id string) handoverShareView {
	e.t.Helper()
	var v handoverShareView
	e.do(e.super, "GET", "/api/admin/shares/"+id, nil, 200, &v)
	return v
}

func (e *env) linkView(id int64) handoverLinkView {
	e.t.Helper()
	var out struct {
		Peers []handoverLinkView `json:"peers"`
	}
	e.do(e.super, "GET", "/api/admin/peers", nil, 200, &out)
	for _, l := range out.Peers {
		if l.ID == id {
			return l
		}
	}
	e.t.Fatalf("no link %d", id)
	return handoverLinkView{}
}

func (e *env) syncLink(id int64) handoverLinkView {
	e.t.Helper()
	e.do(e.super, "POST", "/api/admin/peers/"+itoa(int(id))+"/sync", nil, 200, nil)
	return e.linkView(id)
}

// handedKeys lists the keys handed over through a link on the receiver.
func handedKeys(t *testing.T, e *env, linkID int64) []*store.Key {
	t.Helper()
	ks, err := e.st.HandoverKeys(context.Background(), linkID)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// noSecretsIn fails when any of the secrets shows in the audit log or the
// events of e.
func noSecretsIn(t *testing.T, e *env, secrets ...string) {
	t.Helper()
	var au struct {
		Audit []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit?limit=500", nil, 200, &au)
	var ev struct {
		Events []store.Event `json:"events"`
	}
	e.do(e.super, "GET", "/api/admin/events?limit=500", nil, 200, &ev)
	dump, _ := json.Marshal([]any{au, ev})
	for _, s := range append(secrets, peer.SealPrefix) {
		if strings.Contains(string(dump), s) {
			t.Fatalf("%q in audit or events: %s", s, dump)
		}
	}
}

func hasAudit(t *testing.T, e *env, action string) bool {
	t.Helper()
	var au struct {
		Audit []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit?limit=500", nil, 200, &au)
	for _, a := range au.Audit {
		if a.Action == action {
			return true
		}
	}
	return false
}

// TestHandoverEndToEndRevokePush: A hands a key to B, a local app on B
// calls the fake provider directly with it, and A's revoke push makes B
// delete it at once.
func TestHandoverEndToEndRevokePush(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "Keys for B", "services": []string{"serpapi"}, "mode": "handover",
		"lease": "6h", "private_network": true,
		"limits": map[string]any{"daily_requests": nil, "monthly_requests": nil, "daily_budget": nil, "monthly_budget": nil}})
	if sh.Share.Mode != store.ShareModeHandover || sh.Share.LeaseSec != 6*3600 {
		t.Fatalf("share %+v", sh.Share)
	}

	link := b.connect(sh.Invite, 201)
	lv := b.linkView(link.ID)
	if lv.Mode != store.ShareModeHandover || lv.Handover == nil || len(lv.Handover.Keys) != 1 {
		t.Fatalf("link %+v", lv)
	}
	if len(lv.LocalServices) != 1 || lv.LocalServices[0].Provider != "serpapi" || lv.LocalServices[0].BaseURL != a.upURL {
		t.Fatalf("local service %+v", lv.LocalServices)
	}
	hk := lv.Handover.Keys[0]
	if hk.ValidUntil == nil || hk.ValidUntil.Sub(time.Now()) > 6*time.Hour+time.Minute || hk.ValidUntil.Before(time.Now().Add(5*time.Hour)) {
		t.Fatalf("valid until %v", hk.ValidUntil)
	}
	// The key is stored hidden, unrestricted, with its lease and the owner's id.
	keys := handedKeys(t, b, link.ID)
	if len(keys) != 1 || !keys[0].Hidden || keys[0].Restricted || keys[0].PeerRemoteKeyID == nil || keys[0].PeerValidUntil == nil {
		t.Fatalf("stored key %+v", keys)
	}

	// A local app on B calls the provider directly: A's proxy sees nothing.
	_, btok := b.app(map[string]any{"name": "b-app"})
	code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=coffee", btok)
	if code != 200 || !strings.Contains(body, `"key":"ownerkey1"`) {
		t.Fatalf("direct call from B: %d %s", code, body)
	}
	if v := a.shareView(sh.Share.ID); v.Handover == nil || len(v.Handover.Keys) != 1 || !v.Handover.Keys[0].Live {
		t.Fatalf("owner view %+v", v.Handover)
	} else if u := a.shareViewUsage(sh.Share.ID); u != 0 {
		t.Fatalf("owner proxied %d requests", u)
	}

	// B never reveals, leases, re shares or edits the secret of the key.
	b.do(&http.Client{}, "POST", "/api/v1/lease", map[string]any{"service": "a-serpapi"}, 403, nil, "Authorization", "Bearer "+btok)
	_, leaseTok := b.app(map[string]any{"name": "leaser", "mode": "both", "lease_hidden": true})
	b.do(&http.Client{}, "POST", "/api/v1/lease", map[string]any{"service": "a-serpapi"}, 403, nil, "Authorization", "Bearer "+leaseTok)
	b.do(b.super, "POST", "/api/admin/keys/"+itoa(int(keys[0].ID))+"/reveal", nil, 403, nil)
	b.do(b.super, "PATCH", "/api/admin/keys/"+itoa(int(keys[0].ID)), map[string]any{"secret": "other"}, 403, nil)
	b.do(b.super, "PATCH", "/api/admin/keys/"+itoa(int(keys[0].ID)), map[string]any{"hidden": false}, 403, nil)
	b.do(b.super, "PATCH", "/api/admin/services/"+itoa(int(lv.LocalServices[0].ID)), map[string]any{"base_url": "https://evil.example"}, 409, nil)
	// Switching the provider in the same PATCH does not lift the lock.
	b.do(b.super, "PATCH", "/api/admin/services/"+itoa(int(lv.LocalServices[0].ID)), map[string]any{"provider": "acnologia-peer", "base_url": "https://evil.example", "auth_type": "header", "auth_name": "X-Key"}, 409, nil)
	if sv, _ := b.st.GetServiceBySlug(context.Background(), "a-serpapi"); sv.Provider != "serpapi" || sv.BaseURL != a.upURL {
		t.Fatalf("service after provider switch %+v", sv)
	}
	b.do(b.super, "POST", "/api/admin/shares", map[string]any{"name": "onward", "services": []string{"a-serpapi"}}, 400, nil)
	b.do(b.super, "POST", "/api/admin/shares", map[string]any{"name": "onward", "services": []string{"a-serpapi"}, "mode": "handover"}, 400, nil)

	// A sync renews the key and reports B's use to A.
	b.syncLink(link.ID)
	v := a.shareView(sh.Share.ID)
	if r := v.Handover.Keys[0].ReportedRequests; r == nil || *r != 1 || v.Handover.Keys[0].ReportedStatus != "active" {
		t.Fatalf("reported %+v", v.Handover.Keys[0])
	}
	// The owner's key list says where the key went.
	var ak struct {
		Keys []json.RawMessage `json:"keys"`
	}
	a.do(a.super, "GET", "/api/admin/services/1/keys", nil, 200, &ak)
	if !strings.Contains(string(ak.Keys[0]), `"handed_over":[{`) || !strings.Contains(string(ak.Keys[0]), `"live":true`) {
		t.Fatalf("owner key %s", ak.Keys[0])
	}

	// What the assistant and MCP tools read on B (key lists, peer links)
	// never holds the secret or the sealed blob.
	for _, path := range []string{"/api/admin/keys", "/api/admin/services/" + itoa(int(lv.LocalServices[0].ID)) + "/keys", "/api/admin/peers"} {
		var raw json.RawMessage
		b.do(b.super, "GET", path, nil, 200, &raw)
		if strings.Contains(string(raw), "ownerkey1") || strings.Contains(string(raw), peer.SealPrefix) {
			t.Fatalf("%s leaks: %s", path, raw)
		}
	}

	// Revoke: the push makes B delete the key now.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	if ks := handedKeys(t, b, link.ID); len(ks) != 0 {
		t.Fatalf("B kept %d handed over keys after the revoke push", len(ks))
	}
	if l, _ := b.st.GetPeerLink(context.Background(), link.ID); l.Status != peer.LinkRevoked || len(l.TokenEnc) != 0 {
		t.Fatalf("link after push %+v", l)
	}
	code, _, _ = peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=again", btok)
	if code == 200 {
		t.Fatal("B still calls with a revoked key")
	}
	v = a.shareView(sh.Share.ID)
	if v.Handover.LastRevokePush == nil || !v.Handover.LastRevokePush.OK || !strings.Contains(v.Handover.LastRevokePush.Detail, "wiped 1") {
		t.Fatalf("push %+v", v.Handover.LastRevokePush)
	}
	if v.Handover.Keys[0].Live || v.Handover.Keys[0].WipedAt == nil {
		t.Fatalf("exposure %+v", v.Handover.Keys[0])
	}
	if len(v.RotateChecklist) != 1 || v.RotateChecklist[0].Service != "serpapi" || v.RotateChecklist[0].Provider != "serpapi" {
		t.Fatalf("checklist %+v", v.RotateChecklist)
	}
	for _, act := range []string{"share.handover", "share.revoke_push", "share.handover_wiped"} {
		if !hasAudit(t, a, act) {
			t.Fatalf("owner audit lacks %s", act)
		}
	}
	for _, act := range []string{"peer.handover_received", "peer.handover_wiped", "peer.revoked"} {
		if !hasAudit(t, b, act) {
			t.Fatalf("receiver audit lacks %s", act)
		}
	}
	noSecretsIn(t, a, "ownerkey1")
	noSecretsIn(t, b, "ownerkey1")
}

// shareViewUsage is today's proxied requests of a share on its owner.
func (e *env) shareViewUsage(id string) int64 {
	e.t.Helper()
	var v struct {
		Usage shareUsage `json:"usage"`
	}
	e.do(e.super, "GET", "/api/admin/shares/"+id, nil, 200, &v)
	return v.Usage.TodayRequests
}

// TestHandoverPushFailsLeaseWipes: the revoke push cannot reach B (the
// share is not marked private network, so the push refuses 127.0.0.1), and
// B, cut off from A, deletes the key when its lease ends.
func TestHandoverPushFailsLeaseWipes(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "Keys for B", "services": []string{"serpapi"}, "mode": "handover", "lease": "1h"})
	link := b.connect(sh.Invite, 201)
	if ks := handedKeys(t, b, link.ID); len(ks) != 1 {
		t.Fatalf("handed %d", len(ks))
	}
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	v := a.shareView(sh.Share.ID)
	if v.Handover.LastRevokePush == nil || v.Handover.LastRevokePush.OK || !strings.Contains(v.Handover.LastRevokePush.Detail, "not reached") {
		t.Fatalf("push %+v", v.Handover.LastRevokePush)
	}
	if !v.Handover.Keys[0].Live {
		t.Fatal("owner marked the key wiped without confirmation")
	}
	// B did not hear: it still holds the key.
	if ks := handedKeys(t, b, link.ID); len(ks) != 1 {
		t.Fatalf("B lost the key without the push: %d", len(ks))
	}
	// Cut B off from A entirely, then let the lease run out.
	a.srv.Close()
	b.api.sharingTick(context.Background(), time.Now().Add(30*time.Minute))
	if ks := handedKeys(t, b, link.ID); len(ks) != 1 {
		t.Fatal("key deleted before its lease ended")
	}
	b.api.sharingTick(context.Background(), time.Now().Add(61*time.Minute))
	if ks := handedKeys(t, b, link.ID); len(ks) != 0 {
		t.Fatalf("lease ended but B kept %d keys", len(ks))
	}
	var n int
	_ = b.st.DB.QueryRow(`SELECT COUNT(*) FROM keys WHERE peer_link_id IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("handed over key row remains")
	}
	if !hasAudit(t, b, "peer.handover_wiped") {
		t.Fatal("no wipe audit on B")
	}
	// The owner, on its own clock, notes the lease ended too.
	a.api.sharingTick(context.Background(), time.Now().Add(61*time.Minute))
	rows, _ := a.st.ListHandovers(context.Background(), sh.Share.ID)
	if len(rows) != 1 || rows[0].Live() || !strings.Contains(rows[0].WipeReason, "lease") {
		t.Fatalf("owner exposure after lease %+v", rows)
	}
	noSecretsIn(t, b, "ownerkey1")
}

// TestHandoverPauseScopeAndDisconnectWipe covers the other wipes and that
// resume hands the keys over again.
func TestHandoverPauseScopeAndDisconnectWipe(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	ks := a.addKeys(map[string]any{"secret": "ownerkey1"}, map[string]any{"secret": "ownerkey2"})
	sh := a.newShare(map[string]any{"name": "Keys", "services": []string{"serpapi"}, "mode": "handover"})
	link := b.connect(sh.Invite, 201)
	if n := len(handedKeys(t, b, link.ID)); n != 2 {
		t.Fatalf("handed %d", n)
	}
	// Scope change: only the second key stays.
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"key_scopes": map[string]any{
		"serpapi": map[string]any{"mode": "pick", "key_ids": []int64{ks[1].ID}}}}, 200, nil)
	b.syncLink(link.ID)
	held := handedKeys(t, b, link.ID)
	if len(held) != 1 || *held[0].PeerRemoteKeyID != ks[1].ID {
		t.Fatalf("after scope change %+v", held)
	}
	// Pause wipes, resume hands over again.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/pause", nil, 200, nil)
	b.syncLink(link.ID)
	if n := len(handedKeys(t, b, link.ID)); n != 0 {
		t.Fatalf("paused but B holds %d", n)
	}
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/resume", nil, 200, nil)
	if l := b.syncLink(link.ID); l.Status != peer.LinkActive || l.Handover == nil || len(l.Handover.Keys) != 1 {
		t.Fatalf("after resume %+v", l)
	}
	// Disconnect wipes and the owner records it.
	b.do(b.super, "DELETE", "/api/admin/peers/"+itoa(int(link.ID)), nil, 200, nil)
	if n := len(handedKeys(t, b, link.ID)); n != 0 {
		t.Fatalf("disconnected but B holds %d", n)
	}
	v := a.shareView(sh.Share.ID)
	for _, k := range v.Handover.Keys {
		if k.Live {
			t.Fatalf("owner still shows %+v live", k)
		}
	}
}

// TestHandoverDeliveryOnlyForActiveHandover: the keys route refuses a proxy
// share and an inactive handover share.
func TestHandoverDeliveryOnlyForActiveHandover(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	proxy := a.newShare(map[string]any{"name": "Proxy", "services": []string{"serpapi"}})
	code, tok := a.redeemDirect(b, proxy.Invite, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if c, body, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/keys", tok); c != 409 || strings.Contains(body, "ownerkey1") {
		t.Fatalf("proxy share keys: %d %s", c, body)
	}
	ho := a.newShare(map[string]any{"name": "Handover", "services": []string{"serpapi"}, "mode": "handover"})
	// A receiver that cannot take keys (no box key) cannot redeem it.
	if c, _ := a.redeemDirect(b, ho.Invite, nil); c != 400 {
		t.Fatalf("old receiver redeemed a handover share: %d", c)
	}
	link := b.connect(ho.Invite, 201)
	l, _ := b.st.GetPeerLink(context.Background(), link.ID)
	htok, _ := b.api.Pool.Box.Decrypt(l.TokenEnc)
	c, body, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/keys", htok)
	if c != 200 || strings.Contains(body, "ownerkey1") || !strings.Contains(body, peer.SealPrefix) {
		t.Fatalf("handover keys: %d %s", c, body)
	}
	a.do(a.super, "POST", "/api/admin/shares/"+ho.Share.ID+"/pause", nil, 200, nil)
	if c, body, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/keys", htok); c != 403 || strings.Contains(body, peer.SealPrefix) {
		t.Fatalf("paused share keys: %d %s", c, body)
	}
	// The mode is fixed once redeemed.
	a.do(a.super, "PATCH", "/api/admin/shares/"+ho.Share.ID, map[string]any{"mode": "proxy"}, 400, nil)
}

// TestHandoverLimitsRefused: request and budget limits cannot apply to a
// handover share; timers and leases can.
func TestHandoverLimitsRefused(t *testing.T) {
	a := newEnv(t)
	var e map[string]any
	a.do(a.super, "POST", "/api/admin/shares", map[string]any{"name": "x", "services": []string{"serpapi"}, "mode": "handover",
		"limits": map[string]any{"daily_requests": 5}}, 400, &e)
	if !strings.Contains(e["error"].(map[string]any)["message"].(string), "handover") {
		t.Fatalf("message %v", e)
	}
	a.do(a.super, "POST", "/api/admin/shares", map[string]any{"name": "x", "services": []string{"serpapi"}, "mode": "handover", "lease": "2h"}, 400, nil)
	a.do(a.super, "POST", "/api/admin/shares", map[string]any{"name": "x", "services": []string{"serpapi"}, "mode": "relay"}, 400, nil)
	a.do(a.super, "POST", "/api/admin/shares", map[string]any{"name": "x", "services": []string{"serpapi"}, "end_action": "explode"}, 400, nil)
	ends := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}, "mode": "handover", "lease": "7d",
		"ends_at": ends, "expires_at": ends, "end_action": "revoke"})
	v := a.shareView(sh.Share.ID)
	if v.Lease != "7d" || v.EndsAt == nil || v.EndsAt.Format(time.RFC3339) != ends || v.EndAction != store.EndRevoke || v.ExpiresAt == nil {
		t.Fatalf("view %+v", v)
	}
	// A pending proxy share with limits cannot become handover.
	p := a.newShare(map[string]any{"name": "p", "services": []string{"serpapi"}, "limits": map[string]any{"daily_requests": 5}})
	a.do(a.super, "PATCH", "/api/admin/shares/"+p.Share.ID, map[string]any{"mode": "handover"}, 400, nil)
}

// TestShareTimers: the timer expires or revokes a share, and extend brings
// an expired share back.
func TestShareTimers(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	ends := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	sh := a.newShare(map[string]any{"name": "Timed", "services": []string{"serpapi"}, "ends_at": ends})
	link := b.connect(sh.Invite, 201)
	_, btok := b.app(map[string]any{"name": "b-app"})
	if c, _, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=1", btok); c != 200 {
		t.Fatal(c)
	}
	a.api.sharingTick(context.Background(), time.Now().Add(2*time.Hour))
	if v := a.shareView(sh.Share.ID); v.Status != store.ShareExpired {
		t.Fatalf("status %q", v.Status)
	}
	if !hasAudit(t, a, "share.expired") {
		t.Fatal("no expiry audit")
	}
	// Extend: only later, and the share is active again.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/extend", map[string]any{"ends_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, 400, nil)
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/extend", map[string]any{"ends_at": "soon"}, 400, nil)
	later := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	var v handoverShareView
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/extend", map[string]any{"ends_at": later}, 200, &v)
	if v.Status != store.ShareActive || v.EndsAt == nil || v.EndsAt.Format(time.RFC3339) != later {
		t.Fatalf("extended %+v", v)
	}
	b.syncLink(link.ID)
	if c, _, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=2", btok); c != 200 {
		t.Fatalf("after extend %d", c)
	}
	// End action revoke: the timer revokes for good.
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"end_action": "revoke"}, 200, nil)
	a.api.sharingTick(context.Background(), time.Now().Add(4*time.Hour))
	if v := a.shareView(sh.Share.ID); v.Status != store.ShareRevoked {
		t.Fatalf("status %q", v.Status)
	}
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/extend", map[string]any{"ends_at": time.Now().Add(9 * time.Hour).UTC().Format(time.RFC3339)}, 409, nil)
	if c, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=3", btok); c != 403 || !strings.Contains(body, "share_revoked") {
		t.Fatalf("after timer revoke %d %s", c, body)
	}
}

// TestRevokePushChecks: only a fresh push signed by the pinned owner key
// wipes; bad signatures, stale timestamps and replays are refused.
func TestRevokePushChecks(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "Keys", "services": []string{"serpapi"}, "mode": "handover"})
	link := b.connect(sh.Invite, 201)
	ctx := context.Background()
	ownerPriv, err := peer.Keys(ctx, a.st, a.api.Pool.Box)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _ := peer.Self(ctx, a.st, a.api.Pool.Box, "test", a.srv.URL)
	recv, recvPriv, _ := peer.Self(ctx, b.st, b.api.Pool.Box, "test", b.srv.URL)
	post := func(req peer.RevokeRequest) int {
		body, _ := json.Marshal(req)
		resp, err := http.Post(b.srv.URL+"/api/peer/v1/revoke", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// Signed by another key (the receiver's own), claiming to be the owner.
	if c := post(peer.SignRevoke(recvPriv, owner.ID, sh.Share.ID, recv.ID, time.Now())); c != 401 {
		t.Fatalf("bad signature: %d", c)
	}
	// Stale, both ways.
	if c := post(peer.SignRevoke(ownerPriv, owner.ID, sh.Share.ID, recv.ID, time.Now().Add(-6*time.Minute))); c != 400 {
		t.Fatalf("stale: %d", c)
	}
	if c := post(peer.SignRevoke(ownerPriv, owner.ID, sh.Share.ID, recv.ID, time.Now().Add(6*time.Minute))); c != 400 {
		t.Fatalf("future: %d", c)
	}
	// For another receiver.
	if c := post(peer.SignRevoke(ownerPriv, owner.ID, sh.Share.ID, "acn_srv_someoneelse0000", time.Now())); c != 401 {
		t.Fatalf("other receiver: %d", c)
	}
	if n := len(handedKeys(t, b, link.ID)); n != 1 {
		t.Fatalf("a refused push wiped keys: %d left", n)
	}
	good := peer.SignRevoke(ownerPriv, owner.ID, sh.Share.ID, recv.ID, time.Now())
	if c := post(good); c != 200 {
		t.Fatalf("valid push: %d", c)
	}
	if n := len(handedKeys(t, b, link.ID)); n != 0 {
		t.Fatalf("valid push left %d keys", n)
	}
	if c := post(good); c != 409 {
		t.Fatalf("replay: %d", c)
	}
}

// TestHandoverWebhooksCarryNoSecret checks the new event types exist and
// the handover events name keys by label only.
func TestHandoverWebhooksCarryNoSecret(t *testing.T) {
	for _, typ := range []string{notify.ShareExpired, notify.ShareHandover, notify.ShareHandoverWiped, notify.ShareRevokePush,
		notify.PeerHandoverReceived, notify.PeerHandoverWiped} {
		if !notify.ValidType(typ) {
			t.Fatalf("%s is not a webhook event", typ)
		}
	}
	a, b := newEnv(t), newEnv(t)
	got := make(chan string, 64)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- string(body)
	}))
	t.Cleanup(hook.Close)
	// Deliver to the local test hook (the default client refuses loopback).
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.api.Pool.Notify = notify.New(a.st, a.api.Pool.Box, hook.Client())
	go a.api.Pool.Notify.Run(ctx)
	a.do(a.super, "POST", "/api/admin/webhooks", map[string]any{"name": "h", "url": hook.URL, "format": "json",
		"events": []string{notify.ShareHandover, notify.ShareRevokePush, notify.ShareHandoverWiped}}, 201, nil)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "Keys", "services": []string{"serpapi"}, "mode": "handover", "private_network": true})
	b.connect(sh.Invite, 201)
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(seen) < 3 {
		select {
		case body := <-got:
			if strings.Contains(body, "ownerkey1") || strings.Contains(body, peer.SealPrefix) {
				t.Fatalf("secret in webhook: %s", body)
			}
			var ev notify.Event
			_ = json.Unmarshal([]byte(body), &ev)
			seen[ev.Type] = true
		case <-deadline:
			t.Fatalf("webhooks seen %v", seen)
		}
	}
}

// TestHandoverUntrustedLinkDialsPublicOnly: on a link that is not on a
// private network the owner's base URL passed the public address check at
// sync, but its name now points at a private address (DNS rebinding). The
// proxy and the health check refuse to connect there.
func TestHandoverUntrustedLinkDialsPublicOnly(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "Keys for B", "services": []string{"serpapi"}, "mode": "handover", "lease": "6h",
		"limits": map[string]any{"daily_requests": nil, "monthly_requests": nil, "daily_budget": nil, "monthly_budget": nil}})
	a.up.mu.Lock()
	before := a.up.hits["ownerkey1"]
	a.up.mu.Unlock()
	// The owner's server and base URL looked public when B synced.
	b.api.peers.Client = func(bool) *http.Client { return &http.Client{Timeout: 20 * time.Second} }
	b.api.peers.CheckHost = func(context.Context, string) error { return nil }
	var link linkResp
	b.do(b.super, "POST", "/api/admin/peers", map[string]any{"invite": sh.Invite, "prefix": "a", "trusted": false}, 201, &link)
	keys := handedKeys(t, b, link.ID)
	if len(keys) != 1 {
		t.Fatalf("handed keys %+v", keys)
	}
	_, btok := b.app(map[string]any{"name": "b-app"})
	if code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=coffee", btok); code == 200 {
		t.Fatalf("proxy reached a private address: %s", body)
	}
	sv, err := b.st.GetServiceBySlug(context.Background(), "a-serpapi")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.api.Pool.Check(context.Background(), sv, keys[0]); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("health check: %v", err)
	}
	a.up.mu.Lock()
	hits := a.up.hits["ownerkey1"]
	a.up.mu.Unlock()
	if hits != before {
		t.Fatalf("private upstream got %d requests", hits-before)
	}
}
