package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type shareResp struct {
	Share  store.Share `json:"share"`
	Invite string      `json:"invite"`
}

type linkResp struct {
	store.PeerLink
	Fingerprint   string          `json:"fingerprint"`
	LocalServices []store.Service `json:"local_services"`
}

// newShare creates a share on e as the superadmin.
func (e *env) newShare(body map[string]any) shareResp {
	e.t.Helper()
	var out shareResp
	e.do(e.super, "POST", "/api/admin/shares", body, 201, &out)
	if !strings.HasPrefix(out.Invite, peer.InvitePrefix) {
		e.t.Fatalf("invite %q", out.Invite)
	}
	return out
}

// connect pairs e (the receiver) with an invite from another test server.
func (e *env) connect(invite string, want int) linkResp {
	e.t.Helper()
	var out linkResp
	e.do(e.super, "POST", "/api/admin/peers", map[string]any{"invite": invite, "prefix": "a", "trusted": true}, want, &out)
	return out
}

// call sends a request with an app token and returns status, body and headers.
func peerCall(t *testing.T, method, url, token string, hdr ...string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// redeemDirect redeems an invite against e with this server's identity (or a
// tampered request), returning the status and the token.
func (e *env) redeemDirect(other *env, invite string, tamper func(*peer.RedeemRequest)) (int, string) {
	e.t.Helper()
	inv, err := peer.ParseInvite(invite)
	if err != nil {
		e.t.Fatal(err)
	}
	self, priv, err := peer.Self(context.Background(), other.st, other.api.Pool.Box, "test", other.srv.URL)
	if err != nil {
		e.t.Fatal(err)
	}
	nonce := "nonce-0123456789abcdef"
	req := peer.RedeemRequest{Invite: inv.Secret, ShareID: inv.ShareID, ID: self.ID, Name: self.Name, URL: self.URL,
		PublicKey: self.PublicKey, Nonce: nonce, Signature: peer.SignRedeem(priv, inv.ShareID, peer.InviteHash(inv.Secret), nonce, self.ID)}
	if tamper != nil {
		tamper(&req)
	}
	b, _ := json.Marshal(req)
	resp, err := http.Post(e.srv.URL+"/api/peer/v1/redeem", "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out peer.RedeemResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Token
}

// TestShareEndToEnd runs two servers in process: B proxies through A to a
// fake upstream, then A revokes and B's very next call is refused and B
// marks the link revoked.
func TestShareEndToEnd(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "For B", "services": []string{"serpapi"}, "providers": []string{"tavily"}})
	if sh.Share.Status != store.SharePending || sh.Share.AppID == nil {
		t.Fatalf("share %+v", sh.Share)
	}

	// Preview shows the owner and what is shared, without burning the invite.
	var pv map[string]any
	b.do(b.super, "POST", "/api/admin/peers/preview", map[string]any{"invite": sh.Invite, "trusted": true}, 200, &pv)
	owner := pv["owner"].(map[string]any)
	if !strings.HasPrefix(owner["id"].(string), peer.IDPrefix) || pv["fingerprint"] == "" {
		t.Fatalf("preview %v", pv)
	}

	link := b.connect(sh.Invite, 201)
	if link.Status != peer.LinkActive || len(link.LocalServices) != 1 || link.PeerID != owner["id"] {
		t.Fatalf("link %+v", link)
	}
	local := link.LocalServices[0]
	if local.Slug != "a-serpapi" || local.Provider != providers.PeerSlug || local.PeerName == "" {
		t.Fatalf("local service %+v", local)
	}
	// The shared provider spec was imported with a peer source.
	var prov store.Provider
	b.do(b.super, "GET", "/api/admin/providers/tavily", nil, 200, &prov)
	if len(link.Providers) != 0 {
		// tavily is built in on B too, so the import is skipped as a conflict.
		t.Fatalf("providers %v", link.Providers)
	}
	if !strings.Contains(link.LastError, "tavily") {
		t.Fatalf("conflict not reported: %q", link.LastError)
	}

	// A local app on B uses the shared service like any other.
	_, btok := b.app(map[string]any{"name": "b-app"})
	code, body, hdr := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=coffee", btok)
	if code != 200 || !strings.Contains(body, `"key":"ownerkey1"`) {
		t.Fatalf("proxy through A: %d %s", code, body)
	}
	if hdr.Get("X-Acnologia-Service") != "a-serpapi" {
		t.Fatalf("headers %v", hdr)
	}
	// Leasing a shared service would hand out the token: refused.
	b.do(&http.Client{}, "POST", "/api/v1/lease", map[string]any{"service": "a-serpapi"}, 403, nil, "Authorization", "Bearer "+btok)
	// The share token is never revealed, even to a superadmin.
	keys := mustKeys(t, b, local.ID)
	b.do(b.super, "POST", "/api/admin/keys/"+itoa(int(keys[0].ID))+"/reveal", nil, 403, nil)

	// A sees the use.
	var view struct {
		store.Share
		Usage shareUsage `json:"usage"`
	}
	a.do(a.super, "GET", "/api/admin/shares/"+sh.Share.ID, nil, 200, &view)
	if view.Status != store.ShareActive || view.Usage.TodayRequests != 1 || view.Usage.PerService["serpapi"] != 1 || view.PeerID == "" {
		t.Fatalf("owner view %+v", view)
	}

	// Revoke: B's very next call is refused, and B marks the link revoked.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	code, body, hdr = peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json?q=again", btok)
	if code != 403 || !strings.Contains(body, "share_revoked") || hdr.Get("X-Acnologia-Share") != "revoked" {
		t.Fatalf("after revoke: %d %s %v", code, body, hdr)
	}
	var got linkResp
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &got)
	if got.Status != peer.LinkRevoked {
		t.Fatalf("link status %q", got.Status)
	}
	if ks := mustKeys(t, b, local.ID); len(ks) != 0 {
		t.Fatalf("share token key kept after revoke: %d", len(ks))
	}
	l, _ := b.st.GetPeerLink(context.Background(), link.ID)
	if len(l.TokenEnc) != 0 {
		t.Fatal("token kept after revoke")
	}
	// The token stays dead: revoke cannot be undone.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/resume", nil, 409, nil)

	// Nothing secret reached audit, events or logs of either side.
	for _, e := range []*env{a, b} {
		var au struct {
			Audit []store.AuditEntry `json:"audit"`
		}
		e.do(e.super, "GET", "/api/admin/audit?limit=200", nil, 200, &au)
		var ev struct {
			Events []store.Event `json:"events"`
		}
		e.do(e.super, "GET", "/api/admin/events?limit=500", nil, 200, &ev)
		dump, _ := json.Marshal([]any{au, ev})
		inv, _ := peer.ParseInvite(sh.Invite)
		if strings.Contains(string(dump), inv.Secret) || strings.Contains(string(dump), "acninv_") || strings.Contains(string(dump), "Bearer acn_") {
			t.Fatalf("secret in audit or events: %s", dump)
		}
	}
}

func mustKeys(t *testing.T, e *env, serviceID int64) []store.Key {
	t.Helper()
	var out struct {
		Keys []store.Key `json:"keys"`
	}
	e.do(e.super, "GET", "/api/admin/services/"+itoa(int(serviceID))+"/keys", nil, 200, &out)
	return out.Keys
}

func TestShareRedeemChecks(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}})

	// A wrong signature is refused and does not burn the invite.
	code, _ := a.redeemDirect(b, sh.Invite, func(r *peer.RedeemRequest) { r.Nonce = "another-nonce-123456" })
	if code != 401 {
		t.Fatalf("wrong signature: %d", code)
	}
	// An id that does not match the key is refused.
	code, _ = a.redeemDirect(b, sh.Invite, func(r *peer.RedeemRequest) { r.ID = peer.IDPrefix + "aaaaaaaaaaaaaaaaaaaa" })
	if code != 400 {
		t.Fatalf("wrong id: %d", code)
	}
	// A wrong invite secret is refused.
	code, _ = a.redeemDirect(b, sh.Invite, func(r *peer.RedeemRequest) { r.Invite = peer.NewInviteSecret() })
	if code != 404 {
		t.Fatalf("wrong secret: %d", code)
	}
	// A redeem B signed for another owner, passed on to A, is refused.
	inv, _ := peer.ParseInvite(sh.Invite)
	bSelf, bPriv, _ := peer.Self(context.Background(), b.st, b.api.Pool.Box, "test", b.srv.URL)
	aSelf, _, _ := peer.Self(context.Background(), a.st, a.api.Pool.Box, "test", a.srv.URL)
	signFor := func(owner string) func(*peer.RedeemRequest) {
		return func(r *peer.RedeemRequest) {
			r.OwnerID, r.BoxPublicKey = owner, bSelf.BoxPublicKey
			r.Signature = peer.SignRedeemV3(bPriv, owner, inv.ShareID, peer.InviteHash(inv.Secret), r.Nonce, bSelf.ID, bSelf.BoxPublicKey)
		}
	}
	if code, _ = a.redeemDirect(b, sh.Invite, signFor(peer.IDPrefix+"mmmmmmmmmmmmmmmmmmmm")); code != 401 {
		t.Fatalf("redeem signed for another owner: %d", code)
	}
	// Claiming A as the owner does not help: the signature covers the id.
	if code, _ = a.redeemDirect(b, sh.Invite, func(r *peer.RedeemRequest) {
		signFor(peer.IDPrefix + "mmmmmmmmmmmmmmmmmmmm")(r)
		r.OwnerID = aSelf.ID
	}); code != 401 {
		t.Fatalf("relabelled redeem: %d", code)
	}
	if aSelf.Redeem != peer.RedeemVersion {
		t.Fatalf("identity redeem version %d", aSelf.Redeem)
	}
	// The real one works once.
	code, tok := a.redeemDirect(b, sh.Invite, nil)
	if code != 200 || !store.IsAppToken(tok) {
		t.Fatalf("redeem: %d %q", code, tok)
	}
	code, _ = a.redeemDirect(b, sh.Invite, nil)
	if code != 409 {
		t.Fatalf("reused invite: %d", code)
	}
	// A new invite is only for a pending share.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/invite", nil, 409, nil)

	// An expired invite is refused.
	sh2 := a.newShare(map[string]any{"name": "y", "services": []string{"serpapi"}, "invite_valid": "1h"})
	if _, err := a.st.DB.Exec(`UPDATE shares SET invite_expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Unix(), sh2.Share.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := a.redeemDirect(b, sh2.Invite, nil); code != 410 {
		t.Fatalf("expired invite: %d", code)
	}
	// A fresh invite replaces it.
	var fresh shareResp
	a.do(a.super, "POST", "/api/admin/shares/"+sh2.Share.ID+"/invite", map[string]any{"invite_valid": "7d"}, 200, &fresh)
	if code, _ := a.redeemDirect(b, sh2.Invite, nil); code != 404 {
		t.Fatalf("old invite after a new one: %d", code)
	}
	if code, _ := a.redeemDirect(b, fresh.Invite, nil); code != 200 {
		t.Fatalf("fresh invite: %d", code)
	}
	// A server cannot redeem its own invite.
	sh3 := a.newShare(map[string]any{"name": "z", "services": []string{"serpapi"}})
	if code, _ := a.redeemDirect(a, sh3.Invite, nil); code != 400 {
		t.Fatalf("self redeem: %d", code)
	}
}

func TestSharePauseResumeLimitsAndExpiry(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}, "limits": map[string]any{"daily_requests": 2}})
	_, tok := a.redeemDirect(b, sh.Invite, nil)
	u := a.srv.URL + "/proxy/serpapi/search.json?q=1"
	if code, body, _ := peerCall(t, "GET", u, tok); code != 200 {
		t.Fatalf("first: %d %s", code, body)
	}

	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/pause", nil, 200, nil)
	code, body, hdr := peerCall(t, "GET", u, tok)
	if code != 403 || !strings.Contains(body, "share_paused") || hdr.Get("X-Acnologia-Share") != "paused" {
		t.Fatalf("paused: %d %s", code, body)
	}
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/manifest", tok); code != 403 {
		t.Fatalf("manifest while paused: %d", code)
	}
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/resume", nil, 200, nil)
	if code, body, _ := peerCall(t, "GET", u, tok); code != 200 {
		t.Fatalf("resumed: %d %s", code, body)
	}
	// The daily limit of 2 is reached.
	code, body, _ = peerCall(t, "GET", u, tok)
	if code != 429 || !strings.Contains(body, "app_limit_reached") {
		t.Fatalf("limit: %d %s", code, body)
	}

	// Expiry is checked on every request.
	past := time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"expires_at": past, "limits": map[string]any{}}, 200, nil)
	code, body, hdr = peerCall(t, "GET", u, tok)
	if code != 403 || !strings.Contains(body, "share_expired") || hdr.Get("X-Acnologia-Share") != "expired" {
		t.Fatalf("expired: %d %s", code, body)
	}
	// Moving the expiry forward brings it back.
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"expires_at": ""}, 200, nil)
	if code, body, _ := peerCall(t, "GET", u, tok); code != 200 {
		t.Fatalf("unexpired: %d %s", code, body)
	}
}

func TestShareAppScopeAndHiddenKeys(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "hiddenkey1", "hidden": true})
	// A provider only share grants no service on the proxy.
	sh := a.newShare(map[string]any{"name": "specs", "providers": []string{"serpapi"}})
	_, tok := a.redeemDirect(b, sh.Invite, nil)
	if code, body, _ := peerCall(t, "GET", a.srv.URL+"/proxy/serpapi/search.json", tok); code != 403 || !strings.Contains(body, "service_not_allowed") {
		t.Fatalf("provider only share used a service: %d %s", code, body)
	}
	app, _ := a.st.GetApp(context.Background(), *sh.Share.AppID)
	if len(app.AllowedServices) == 0 || app.CanUse("serpapi") || app.Mode != "proxy" {
		t.Fatalf("share app %+v", app)
	}
	// Hidden keys only with include_hidden.
	sh2 := a.newShare(map[string]any{"name": "svc", "services": []string{"serpapi"}})
	_, tok2 := a.redeemDirect(b, sh2.Invite, nil)
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/proxy/serpapi/search.json", tok2); code != 503 {
		t.Fatalf("hidden key used without include_hidden: %d", code)
	}
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh2.Share.ID, map[string]any{"include_hidden": true}, 200, nil)
	if code, body, _ := peerCall(t, "GET", a.srv.URL+"/proxy/serpapi/search.json", tok2); code != 200 {
		t.Fatalf("hidden key with include_hidden: %d %s", code, body)
	}
	// Removing every service never leaves an empty (allow all) list.
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh2.Share.ID, map[string]any{"services": []string{}, "providers": []string{"serpapi"}}, 200, nil)
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/proxy/serpapi/search.json", tok2); code != 403 {
		t.Fatalf("emptied share still proxies: %d", code)
	}
	// Share apps stay off the Apps page and cannot be edited there.
	var apps struct {
		Apps []store.App `json:"apps"`
	}
	a.do(a.super, "GET", "/api/admin/apps", nil, 200, &apps)
	if len(apps.Apps) != 0 {
		t.Fatalf("share apps listed: %+v", apps.Apps)
	}
	a.do(a.super, "PATCH", "/api/admin/apps/"+itoa(int(*sh2.Share.AppID)), map[string]any{"allowed_services": []string{}}, 409, nil)
	a.do(a.super, "POST", "/api/admin/apps/"+itoa(int(*sh2.Share.AppID))+"/tokens", map[string]any{"name": "x"}, 409, nil)
	// A share needs something in it.
	a.do(a.super, "POST", "/api/admin/shares", map[string]any{"name": "empty"}, 400, nil)
}

func TestShareManifestHasNoSecrets(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "sk-verysecretownerkey-1234567890"})
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}, "providers": []string{"tavily"}})
	_, tok := a.redeemDirect(b, sh.Invite, nil)
	code, body, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/manifest", tok)
	if code != 200 {
		t.Fatalf("manifest: %d %s", code, body)
	}
	if strings.Contains(body, "sk-verysecretownerkey") || strings.Contains(body, "secret_enc") || strings.Contains(body, tok) {
		t.Fatalf("manifest leaks: %s", body)
	}
	var m peer.Manifest
	_ = json.Unmarshal([]byte(body), &m)
	if len(m.Services) != 1 || m.Services[0].Available != 1 || len(m.Providers) != 2 {
		t.Fatalf("manifest %+v", m)
	}
	for _, p := range m.Providers {
		if p.Shared != (p.Slug == "tavily") || len(p.Spec) == 0 {
			t.Fatalf("provider %+v", p)
		}
	}
}

func TestShareRoles(t *testing.T) {
	a := newEnv(t)
	a.do(a.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	a.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}})
	a.do(admin, "GET", "/api/admin/shares", nil, 200, nil)
	a.do(admin, "GET", "/api/admin/peers", nil, 200, nil)
	a.do(admin, "GET", "/api/admin/identity", nil, 200, nil)
	a.do(admin, "POST", "/api/admin/shares", map[string]any{"name": "y", "services": []string{"serpapi"}}, 403, nil)
	a.do(admin, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"name": "z"}, 403, nil)
	a.do(admin, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 403, nil)
	a.do(admin, "POST", "/api/admin/peers", map[string]any{"invite": sh.Invite}, 403, nil)
	a.do(admin, "PATCH", "/api/admin/identity", map[string]any{"name": "evil"}, 403, nil)
}

func TestShareHopGuardAndNoReshare(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}})
	link := b.connect(sh.Invite, 201)
	_, btok := b.app(map[string]any{"name": "b-app"})
	// Past three hops is a loop.
	if code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json", btok, "X-Acnologia-Hop", "3"); code != 508 || !strings.Contains(body, "loop_detected") {
		t.Fatalf("hop guard on a peer service: %d %s", code, body)
	}
	if code, _, _ := peerCall(t, "GET", b.srv.URL+"/proxy/serpapi/search.json", btok, "X-Acnologia-Hop", "4"); code != 508 {
		t.Fatalf("hop guard: %d", code)
	}
	// The hop header reaches A, and A refuses a request that loops back.
	if code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json", btok, "X-Acnologia-Hop", "1"); code != 200 {
		t.Fatalf("two hops: %d %s", code, body)
	}
	// A service that came from a peer cannot be shared onward.
	b.do(b.super, "POST", "/api/admin/shares", map[string]any{"name": "onward", "services": []string{link.LocalServices[0].Slug}}, 400, nil)
	b.do(b.super, "POST", "/api/admin/shares", map[string]any{"name": "onward", "providers": []string{providers.PeerSlug}}, 400, nil)
}

func TestShareReceiverSyncAddsAndDisables(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	a.addKeys(map[string]any{"secret": "ownerkey1"})
	var tav store.Service
	a.do(a.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serp2", "base_url": a.upURL}, 201, &tav)
	a.do(a.super, "POST", "/api/admin/services/"+itoa(int(tav.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "ownerkey2"}}}, 201, nil)
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}})
	link := b.connect(sh.Invite, 201)
	_, btok := b.app(map[string]any{"name": "b-app"})

	// A swaps serpapi for serp2; B's sync adds one and disables the other.
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"services": []string{"serp2"}}, 200, nil)
	var got linkResp
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &got)
	if len(got.LocalServices) != 2 || got.Services["serp2"] == 0 {
		t.Fatalf("after sync %+v", got)
	}
	if code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serp2/search.json", btok); code != 200 || !strings.Contains(body, "ownerkey2") {
		t.Fatalf("new service: %d %s", code, body)
	}
	if code, _, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json", btok); code != 503 {
		t.Fatalf("removed service still works: %d", code)
	}
	// Disabled, not deleted: the service and its key stay.
	if ks := mustKeys(t, b, got.Services["serpapi"]); len(ks) != 1 || ks[0].Status != store.StatusDisabled {
		t.Fatalf("removed service keys %+v", ks)
	}
	// Adding it back turns it on again.
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"services": []string{"serpapi", "serp2"}}, 200, nil)
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &got)
	if code, _, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serpapi/search.json", btok); code != 200 {
		t.Fatalf("re-added service: %d", code)
	}

	// Pause shows on B's next sync, and resume brings the services back.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/pause", nil, 200, nil)
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &got)
	if got.Status != peer.LinkPaused {
		t.Fatalf("paused link %q", got.Status)
	}
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/resume", nil, 200, nil)
	b.do(b.super, "POST", "/api/admin/peers/"+itoa(int(link.ID))+"/sync", nil, 200, &got)
	if got.Status != peer.LinkActive {
		t.Fatalf("resumed link %q", got.Status)
	}
	if code, body, _ := peerCall(t, "GET", b.srv.URL+"/proxy/a-serp2/search.json", btok); code != 200 {
		t.Fatalf("after resume: %d %s", code, body)
	}

	// Disconnect tells A, which sees the share left by the peer.
	b.do(b.super, "DELETE", "/api/admin/peers/"+itoa(int(link.ID)), nil, 200, &got)
	if got.Status != peer.LinkDisconnected {
		t.Fatalf("disconnected %q", got.Status)
	}
	var view store.Share
	a.do(a.super, "GET", "/api/admin/shares/"+sh.Share.ID, nil, 200, &view)
	if view.Status != store.ShareRevoked || view.LeftAt == nil {
		t.Fatalf("owner after leave %+v", view)
	}
	// A second delete removes the row.
	b.do(b.super, "DELETE", "/api/admin/peers/"+itoa(int(link.ID)), nil, 200, nil)
}

func TestWellKnownIdentityOpenInGate(t *testing.T) {
	a := newEnv(t)
	a.api.setAccessPassword("a-long-access-password")
	code, body, _ := peerCall(t, "GET", a.srv.URL+"/.well-known/acnologia", "")
	if code != 200 {
		t.Fatalf("well-known: %d %s", code, body)
	}
	var id peer.Identity
	_ = json.Unmarshal([]byte(body), &id)
	if err := peer.CheckIdentity(id); err != nil || id.URL != a.srv.URL {
		t.Fatalf("identity %+v %v", id, err)
	}
	// The admin side stays locked.
	if code, _, _ := peerCall(t, "GET", a.srv.URL+"/api/admin/shares", ""); code != 401 {
		t.Fatalf("admin open in gate: %d", code)
	}
}
