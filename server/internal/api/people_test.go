package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type personShareResp struct {
	Share struct {
		store.Share
		RevealedKeys []revealedKeyView `json:"revealed_keys"`
	} `json:"share"`
	ClaimURL string `json:"claim_url"`
	Invite   string `json:"invite"`
}

// newPersonShare creates a share with a person on e and returns it with
// the claim code.
func (e *env) newPersonShare(body map[string]any) (personShareResp, string) {
	e.t.Helper()
	body["recipient"] = "person"
	if _, ok := body["label"]; !ok {
		body["label"] = "Dana"
	}
	var out personShareResp
	e.do(e.super, "POST", "/api/admin/shares", body, 201, &out)
	prefix := e.srv.URL + "/claim/" + store.ClaimPrefix
	if !strings.HasPrefix(out.ClaimURL, prefix) || out.Invite != "" {
		e.t.Fatalf("claim url %q (want prefix %q), invite %q", out.ClaimURL, prefix, out.Invite)
	}
	return out, strings.TrimPrefix(out.ClaimURL, e.srv.URL+"/claim/")
}

// claim posts a code to a public claim route and returns status and body.
func (e *env) claim(route, code string) (int, map[string]any, string) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"code": code})
	resp, err := http.Post(e.srv.URL+"/api/claim/v1/"+route, "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// redeemProxy claims a proxy share and returns the person's token.
func (e *env) redeemProxy(code string) (string, map[string]any) {
	e.t.Helper()
	st, out, raw := e.claim("redeem", code)
	if st != 200 {
		e.t.Fatalf("redeem: %d %s", st, raw)
	}
	tok, _ := out["token"].(string)
	if !store.IsAppToken(tok) {
		e.t.Fatalf("token %q", tok)
	}
	return tok, out
}

func (e *env) me(tok string) map[string]any {
	e.t.Helper()
	var out map[string]any
	e.do(http.DefaultClient, "GET", "/api/v1/me", nil, 200, &out, "Authorization", "Bearer "+tok)
	return out
}

func mcpTools(t *testing.T, e *env, tok string) map[string]bool {
	t.Helper()
	var res map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200, &res, "Authorization", "Bearer "+tok)
	out := map[string]bool{}
	for _, tl := range res["result"].(map[string]any)["tools"].([]any) {
		out[tl.(map[string]any)["name"].(string)] = true
	}
	return out
}

// TestPersonShareProxy: a person claims a proxy share once and uses the
// token on /proxy, /api/v1 and /mcp, only for the shared service and the
// scoped key, under the limits; pause and revoke refuse the next call.
func TestPersonShareProxy(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "personkey1"}, map[string]any{"secret": "personkey2"})
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "other", "base_url": e.upURL}, 201, nil)
	sh, code := e.newPersonShare(map[string]any{"name": "For Dana", "email": "dana@example.com", "services": []string{"serpapi"},
		"limits":     map[string]any{"daily_requests": 3},
		"key_scopes": map[string]any{"serpapi": map[string]any{"mode": "pick", "key_ids": []int64{keys[1].ID}}}})
	s := sh.Share
	if s.Recipient != "person" || s.Label != "Dana" || s.Email != "dana@example.com" || s.Mode != "proxy" || s.Status != store.SharePending ||
		s.ClaimExpires == nil || s.ClaimedAt != nil || s.RevealedKeys == nil {
		t.Fatalf("share %+v", sh.Share)
	}
	if time.Until(*s.ClaimExpires) < 23*time.Hour || time.Until(*s.ClaimExpires) > 25*time.Hour {
		t.Fatalf("claim validity %v", s.ClaimExpires)
	}

	// Preview: what and by whom, no secret, and it does not burn the link.
	for i := 0; i < 2; i++ {
		st, pv, raw := e.claim("preview", code)
		if st != 200 {
			t.Fatalf("preview: %d %s", st, raw)
		}
		ps := pv["share"].(map[string]any)
		if ps["name"] != "For Dana" || ps["label"] != "Dana" || ps["mode"] != "proxy" || pv["owner"].(map[string]any)["name"] == "" || pv["claim_expires_at"] == nil {
			t.Fatalf("preview: %s", raw)
		}
		svcs := ps["services"].([]any)
		if len(svcs) != 1 || svcs[0].(map[string]any)["slug"] != "serpapi" || svcs[0].(map[string]any)["provider"] != "serpapi" {
			t.Fatalf("preview services %s", raw)
		}
		if strings.Contains(raw, "personkey") || pv["token"] != nil || strings.Contains(raw, store.ClaimPrefix) {
			t.Fatalf("secret in preview: %s", raw)
		}
	}
	// The whole link works as the code too.
	if st, _, _ := e.claim("preview", sh.ClaimURL); st != 200 {
		t.Fatalf("preview by link: %d", st)
	}

	tok, red := e.redeemProxy(code)
	if red["base_url"] != e.srv.URL+"/proxy" || red["mcp_url"] != e.srv.URL+"/mcp" {
		t.Fatalf("redeem %v", red)
	}
	rs := red["services"].([]any)[0].(map[string]any)
	if rs["slug"] != "serpapi" || rs["proxy_url"] != e.srv.URL+"/proxy/serpapi" || rs["auth"].(map[string]any)["type"] == "" || len(rs["examples"].([]any)) == 0 {
		t.Fatalf("redeem service %v", rs)
	}
	if red["limits"].(map[string]any)["daily_requests"].(float64) != 3 {
		t.Fatalf("redeem limits %v", red["limits"])
	}
	// Once: a second redeem and a later preview are refused.
	if st, out, _ := e.claim("redeem", code); st != 409 || errCode(out) != "already_claimed" {
		t.Fatalf("second redeem: %d %v", st, out)
	}
	if st, out, _ := e.claim("preview", code); st != 409 || errCode(out) != "already_claimed" {
		t.Fatalf("preview after claim: %d %v", st, out)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+s.ID+"/claim-link", map[string]any{"valid": "1h"}, 409, nil)

	// The token works for the shared service and only the scoped key.
	for i := 0; i < 2; i++ {
		code, body, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json?q=tea", tok)
		if code != 200 || !strings.Contains(body, `"key":"personkey2"`) {
			t.Fatalf("proxy: %d %s", code, body)
		}
	}
	if code, body, _ := peerCall(t, "GET", e.srv.URL+"/proxy/other/search.json?q=tea", tok); code != 403 || !strings.Contains(body, "service_not_allowed") {
		t.Fatalf("other service: %d %s", code, body)
	}
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 403, nil, "Authorization", "Bearer "+tok)

	// MCP: core and service tools, never insights, manage or leasing, even
	// if the settings row says otherwise.
	appID := *s.AppID
	st := store.DefaultMCPSettings()
	st.Insights, st.Manage = true, true
	if err := e.st.SetMCPSettings(context.Background(), appID, st); err != nil {
		t.Fatal(err)
	}
	tools := mcpTools(t, e, tok)
	if !tools["proxy_request"] || !tools["list_services"] {
		t.Fatalf("tools %v", tools)
	}
	for _, n := range []string{"list_keys", "key_details", "service_stats", "check_keys", "set_key_status", "add_keys", "get_key", "report_key"} {
		if tools[n] {
			t.Fatalf("person token sees %s: %v", n, tools)
		}
	}
	var res map[string]any
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "add_keys", "arguments": map[string]any{"service": "serpapi", "keys": []any{map[string]any{"secret": "planted"}}}}},
		200, &res, "Authorization", "Bearer "+tok)
	if res["error"] == nil {
		t.Fatalf("manage tool ran: %v", res)
	}
	e.do(http.DefaultClient, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "proxy_request", "arguments": map[string]any{"service": "serpapi", "path": "/search.json"}}},
		200, &res, "Authorization", "Bearer "+tok)
	if sc, _ := res["result"].(map[string]any)["structuredContent"].(map[string]any); sc == nil || sc["status"].(float64) != 200 {
		t.Fatalf("mcp proxy: %v", res)
	}
	// Admin and server to server routes are not for this token.
	for _, p := range []string{"/api/admin/me", "/api/admin/shares", "/api/admin/keys"} {
		e.do(http.DefaultClient, "GET", p, nil, 401, nil, "Authorization", "Bearer "+tok)
	}
	e.do(http.DefaultClient, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []any{map[string]any{"secret": "x"}}}, 401, nil, "Authorization", "Bearer "+tok)
	e.do(http.DefaultClient, "GET", "/api/peer/v1/manifest", nil, 403, nil, "Authorization", "Bearer "+tok)
	e.do(http.DefaultClient, "POST", "/api/peer/v1/leave", nil, 403, nil, "Authorization", "Bearer "+tok)

	// Limits: three requests a day (two proxy calls and one MCP call so far).
	if code, body, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json?q=more", tok); code != 429 || !strings.Contains(body, "app_limit_reached") {
		t.Fatalf("limit: %d %s", code, body)
	}

	// /api/v1/me shows the share, its limits and use.
	me := e.me(tok)
	share := me["share"].(map[string]any)
	if share["name"] != "For Dana" || share["status"] != "active" || share["mode"] != "proxy" || share["owner"].(map[string]any)["name"] == "" || share["label"] != "Dana" ||
		me["usage"].(map[string]any)["last_used_at"] == nil {
		t.Fatalf("me share %v", me)
	}
	if me["usage"].(map[string]any)["today_requests"].(float64) != 3 || me["limits"].(map[string]any)["daily_requests"].(float64) != 3 {
		t.Fatalf("me usage %v", me)
	}
	ms := me["services"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["slug"] != "serpapi" || ms[0].(map[string]any)["available"].(float64) != 1 {
		t.Fatalf("me services %v", ms)
	}

	// Lift the limit; pause and revoke refuse the very next call.
	e.do(e.super, "PATCH", "/api/admin/shares/"+s.ID, map[string]any{"limits": map[string]any{}}, 200, nil)
	if code, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 200 {
		t.Fatalf("after lifting: %d", code)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+s.ID+"/pause", nil, 200, nil)
	if code, body, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 403 || !strings.Contains(body, "share_paused") {
		t.Fatalf("paused: %d %s", code, body)
	}
	if me := e.me(tok); me["share"].(map[string]any)["status"] != "paused" || len(me["services"].([]any)) != 0 {
		t.Fatalf("me paused %v", me)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+s.ID+"/resume", nil, 200, nil)
	if code, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 200 {
		t.Fatalf("resumed: %d", code)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+s.ID+"/revoke", nil, 200, nil)
	if code, body, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 403 || !strings.Contains(body, "share_revoked") {
		t.Fatalf("revoked: %d %s", code, body)
	}
	if code, _, _ := peerCall(t, "POST", e.srv.URL+"/mcp", tok); code != 403 {
		t.Fatalf("mcp after revoke: %d", code)
	}
	if me := e.me(tok); me["share"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("me revoked %v", me)
	}

	// The list shows the person fields.
	var list struct {
		Shares []map[string]any `json:"shares"`
	}
	e.do(e.super, "GET", "/api/admin/shares", nil, 200, &list)
	got := list.Shares[0]
	for _, f := range []string{"recipient", "label", "email", "claimed_at", "claim_expires_at", "revealed_keys"} {
		if _, ok := got[f]; !ok {
			t.Fatalf("list lacks %s: %v", f, got)
		}
	}
	if got["claimed_at"] == nil || got["recipient"] != "person" {
		t.Fatalf("list %v", got)
	}
	noSecretsIn(t, e, "personkey1", "personkey2", tok, code, store.ClaimPrefix)
}

func TestPersonShareTimerAndMe(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "timerkey1"})
	sh, code := e.newPersonShare(map[string]any{"name": "Short", "services": []string{"serpapi"}, "ends_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	tok, red := e.redeemProxy(code)
	if red["ends_at"] == nil {
		t.Fatalf("redeem ends_at %v", red)
	}
	if code, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 200 {
		t.Fatalf("before end: %d", code)
	}
	if _, err := e.st.DB.Exec(`UPDATE shares SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).Unix(), sh.Share.ID); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 403 || !strings.Contains(body, "share_expired") {
		t.Fatalf("after end: %d %s", code, body)
	}
	if me := e.me(tok); me["share"].(map[string]any)["status"] != "expired" {
		t.Fatalf("me %v", me)
	}
	// Extend brings it back.
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/extend", map[string]any{"ends_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)}, 200, nil)
	if code, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json", tok); code != 200 {
		t.Fatalf("after extend: %d", code)
	}

	// /api/v1/me works for an ordinary app too, without share fields and
	// without pool sizes.
	e.addKeys(map[string]any{"secret": "timerkey2"}, map[string]any{"secret": "timerkey3"})
	_, atok := e.app(map[string]any{"name": "plain", "limits": map[string]any{"monthly_requests": 100}})
	me := e.me(atok)
	if me["share"] != nil || me["app"].(map[string]any)["name"] != "plain" {
		t.Fatalf("plain me %v", me)
	}
	svcs := me["services"].([]any)
	if len(svcs) != 1 || svcs[0].(map[string]any)["available"].(float64) != 1 {
		t.Fatalf("plain services %v", svcs)
	}
	e.do(http.DefaultClient, "GET", "/api/v1/me", nil, 401, nil)
	e.do(http.DefaultClient, "GET", "/api/v1/me", nil, 401, nil, "Authorization", "Bearer acn_notarealtoken0000000000000000")
}

func TestPersonShareClaimErrors(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "errkey1"})
	if st, out, _ := e.claim("preview", store.ClaimPrefix+"doesnotexist0000000000000000"); st != 404 || errCode(out) != "claim_invalid" {
		t.Fatalf("unknown: %d %v", st, out)
	}
	if st, out, _ := e.claim("redeem", "garbage"); st != 404 || errCode(out) != "claim_invalid" {
		t.Fatalf("garbage: %d %v", st, out)
	}

	// Expired link: 410, and a new link replaces it (the old one is dead).
	sh, code := e.newPersonShare(map[string]any{"name": "Late", "services": []string{"serpapi"}, "claim_valid": "1h"})
	if time.Until(*sh.Share.ClaimExpires) > 61*time.Minute {
		t.Fatalf("claim_valid ignored: %v", sh.Share.ClaimExpires)
	}
	if _, err := e.st.DB.Exec(`UPDATE shares SET claim_expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Unix(), sh.Share.ID); err != nil {
		t.Fatal(err)
	}
	if st, out, _ := e.claim("redeem", code); st != 410 || errCode(out) != "claim_expired" {
		t.Fatalf("expired: %d %v", st, out)
	}
	var nl personShareResp
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/claim-link", map[string]any{"valid": "7d"}, 200, &nl)
	var nlRaw map[string]any
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/claim-link", map[string]any{"valid": "7d"}, 200, &nlRaw)
	if nlRaw["claim_expires_at"] == nil {
		t.Fatalf("claim-link %v", nlRaw)
	}
	nl.ClaimURL = nlRaw["claim_url"].(string)
	if !strings.HasPrefix(nl.ClaimURL, e.srv.URL+"/claim/"+store.ClaimPrefix) || time.Until(*nl.Share.ClaimExpires) < 6*24*time.Hour {
		t.Fatalf("new link %+v", nl)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/claim-link", map[string]any{"valid": "2d"}, 400, nil)
	if st, _, _ := e.claim("preview", code); st != 404 {
		t.Fatalf("old link after new: %d", st)
	}
	fresh := strings.TrimPrefix(nl.ClaimURL, e.srv.URL+"/claim/")
	if st, _, raw := e.claim("preview", fresh); st != 200 {
		t.Fatalf("new link: %d %s", st, raw)
	}
	// Server shares have invites, person shares claim links.
	srv := e.newShare(map[string]any{"name": "Server", "services": []string{"serpapi"}})
	e.do(e.super, "POST", "/api/admin/shares/"+srv.Share.ID+"/claim-link", nil, 409, nil)
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/invite", nil, 409, nil)

	// A share revoked or timed out before anyone claimed it: 410.
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	if st, out, _ := e.claim("redeem", fresh); st != 410 || errCode(out) != "claim_expired" {
		t.Fatalf("revoked: %d %v", st, out)
	}
	ended, endCode := e.newPersonShare(map[string]any{"name": "Ends", "services": []string{"serpapi"}})
	if _, err := e.st.DB.Exec(`UPDATE shares SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).Unix(), ended.Share.ID); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := e.claim("redeem", endCode); st != 410 {
		t.Fatalf("ended before claim: %d", st)
	}

	// Malformed codes cost no lookup and are not counted.
	for i := 0; i < peerInviteLimit+2; i++ {
		if st, _, _ := e.claim("preview", "garbage"); st != 404 {
			t.Fatalf("malformed code %d: %d", i, st)
		}
	}
	// Unknown ones are, like the invite routes.
	limited := false
	for i := 0; i < peerInviteLimit+2; i++ {
		if st, out, _ := e.claim("preview", store.ClaimPrefix+"doesnotexist0000000000000000"); st == 429 {
			limited = errCode(out) == "rate_limited"
			break
		}
	}
	if !limited {
		t.Fatal("claim routes are not rate limited")
	}
}

// TestClaimLimitSpareRealLinks: junk from other addresses spending the
// whole global budget, for the minute and the day, does not stop a real link.
func TestClaimLimitSpareRealLinks(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "limkey1"})
	_, code := e.newPersonShare(map[string]any{"name": "Real", "services": []string{"serpapi"}})
	spendGlobal(e.api.claimLimit)
	if st, out, _ := e.claim("preview", store.ClaimPrefix+"doesnotexist0000000000000000"); st != 429 || errCode(out) != "rate_limited" {
		t.Fatalf("junk after the global budget: %d %v", st, out)
	}
	if st, out, _ := e.claim("preview", code); st != 200 {
		t.Fatalf("real link refused: %d %v", st, out)
	}
	if st, out, _ := e.claim("redeem", code); st != 200 {
		t.Fatalf("real link redeem refused: %d %v", st, out)
	}
}

func TestPersonShareValidation(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "valkey1", "hidden": true})
	bad := []map[string]any{
		{"name": "x", "services": []string{"serpapi"}, "recipient": "person", "mode": "handover", "label": "D"},
		{"name": "x", "services": []string{"serpapi"}, "mode": "reveal"},
		{"name": "x", "services": []string{"serpapi"}, "recipient": "person"},
		{"name": "x", "services": []string{"serpapi"}, "recipient": "person", "label": "D", "email": "not an email"},
		{"name": "x", "services": []string{"serpapi"}, "recipient": "robot"},
		{"name": "x", "services": []string{"serpapi"}, "label": "server shares have no label"},
		{"name": "x", "services": []string{"serpapi"}, "recipient": "person", "label": "D", "claim_valid": "3d"},
		{"name": "x", "services": []string{"serpapi"}, "recipient": "person", "label": "D", "mode": "reveal", "limits": map[string]any{"daily_requests": 5}},
	}
	for _, b := range bad {
		e.do(e.super, "POST", "/api/admin/shares", b, 400, nil)
	}
	// Only a hidden key: nothing to reveal, even with include_hidden.
	var out struct {
		Error apiError `json:"error"`
	}
	e.do(e.super, "POST", "/api/admin/shares", map[string]any{"name": "x", "services": []string{"serpapi"}, "recipient": "person", "label": "D",
		"mode": "reveal", "include_hidden": true}, 400, &out)
	if out.Error.Code != "no_revealable_keys" {
		t.Fatalf("code %q", out.Error.Code)
	}
	e.do(e.super, "POST", "/api/admin/shares", map[string]any{"name": "x", "services": []string{"serpapi"}, "recipient": "person", "label": "D",
		"mode": "reveal", "include_hidden": true, "key_scopes": map[string]any{"serpapi": map[string]any{"mode": "pick", "key_ids": []int64{1}}}}, 400, nil)
	// The recipient cannot change.
	sh, _ := e.newPersonShare(map[string]any{"name": "P", "services": []string{"serpapi"}})
	e.do(e.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"recipient": "server"}, 400, nil)
	// Label and email can change; the email is only a label, accepted loosely.
	e.do(e.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"email": "d@x.co", "label": "Dana B"}, 200, nil)
	got := e.shareView(sh.Share.ID)
	if got.Email != "d@x.co" || got.Label != "Dana B" {
		t.Fatalf("patched %+v", got.Share)
	}
}

// TestRevealShare: the claim shows the scoped, non hidden keys once,
// records each reveal, and an ended share lists them to rotate.
func TestRevealShare(t *testing.T) {
	e := newEnv(t)
	got := make(chan string, 64)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- string(body)
	}))
	t.Cleanup(hook.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.api.Pool.Notify = notify.New(e.st, e.api.Pool.Box, hook.Client())
	go e.api.Pool.Notify.Run(ctx)
	e.do(e.super, "POST", "/api/admin/webhooks", map[string]any{"name": "h", "url": hook.URL, "format": "json",
		"events": []string{notify.ShareClaimed, notify.ShareRevealed, notify.ShareClaimLink, notify.ShareCreated, notify.ShareRevoked}}, 201, nil)

	keys := e.addKeys(map[string]any{"secret": "revealsecret1", "label": "main"}, map[string]any{"secret": "revealsecret2", "hidden": true},
		map[string]any{"secret": "revealsecret3", "label": "spare"})
	sh, code := e.newPersonShare(map[string]any{"name": "Raw keys", "services": []string{"serpapi"}, "mode": "reveal", "include_hidden": true,
		"key_scopes": map[string]any{"serpapi": map[string]any{"mode": "count", "count": 2}}})
	if sh.Share.Mode != "reveal" {
		t.Fatalf("mode %q", sh.Share.Mode)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/claim-link", nil, 200, nil) // claim_link webhook
	var nl personShareResp
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/claim-link", map[string]any{"valid": "24h"}, 200, &nl)
	code = strings.TrimPrefix(nl.ClaimURL, e.srv.URL+"/claim/")

	st, pv, raw := e.claim("preview", code)
	if st != 200 || pv["share"].(map[string]any)["mode"] != "reveal" || pv["share"].(map[string]any)["key_count"].(float64) != 2 || strings.Contains(raw, "revealsecret") {
		t.Fatalf("preview %d %s", st, raw)
	}
	st, red, raw := e.claim("redeem", code)
	if st != 200 {
		t.Fatalf("redeem %d %s", st, raw)
	}
	rk := red["keys"].([]any)
	secrets := map[string]bool{}
	for _, k := range rk {
		m := k.(map[string]any)
		secrets[m["secret"].(string)] = true
		if m["service"] != "serpapi" || m["provider"] != "serpapi" || m["base_url"] != e.upURL || m["auth"].(map[string]any)["type"] != "query" {
			t.Fatalf("revealed key %v", m)
		}
	}
	if len(rk) != 2 || !secrets["revealsecret1"] || !secrets["revealsecret3"] || secrets["revealsecret2"] {
		t.Fatalf("revealed %v", secrets)
	}
	if _, ok := red["token"]; ok {
		t.Fatalf("reveal made a token: %v", red)
	}
	// Once: nothing can return them again.
	if st, out, raw := e.claim("redeem", code); st != 409 || errCode(out) != "already_claimed" || strings.Contains(raw, "revealsecret") {
		t.Fatalf("second redeem %d %s", st, raw)
	}

	// Exposure: on the share, and on each key's list entry.
	v := e.shareView(sh.Share.ID)
	var full map[string]any
	e.do(e.super, "GET", "/api/admin/shares/"+sh.Share.ID, nil, 200, &full)
	rv := full["revealed_keys"].([]any)
	if len(rv) != 2 || v.Status != store.ShareActive || v.RotateChecklist != nil {
		t.Fatalf("share after reveal %v", full)
	}
	var kl struct {
		Keys []store.Key `json:"keys"`
	}
	e.do(e.super, "GET", "/api/admin/services/1/keys", nil, 200, &kl)
	for _, k := range kl.Keys {
		want := k.ID == keys[0].ID || k.ID == keys[2].ID
		if want != (len(k.RevealedTo) == 1) {
			t.Fatalf("key %d revealed_to %+v", k.ID, k.RevealedTo)
		}
		if want && (k.RevealedTo[0].ShareID != sh.Share.ID || k.RevealedTo[0].Label != "Dana" || k.RevealedTo[0].ShareName != "Raw keys") {
			t.Fatalf("revealed_to %+v", k.RevealedTo)
		}
	}

	// Revoke: the checklist names the keys to rotate, and the audit says so.
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	v = e.shareView(sh.Share.ID)
	if len(v.RotateChecklist) != 2 || v.RotateChecklist[0].Service != "serpapi" {
		t.Fatalf("checklist %+v", v.RotateChecklist)
	}
	for _, act := range []string{"share.claimed", "share.revealed", "share.claim_link", "share.rotate_keys"} {
		if !hasAudit(t, e, act) {
			t.Fatalf("no %s audit", act)
		}
	}
	noSecretsIn(t, e, "revealsecret1", "revealsecret2", "revealsecret3", code, store.ClaimPrefix)

	seen := map[string]int{}
	deadline := time.After(10 * time.Second)
	for seen[notify.ShareRevealed] < 2 || seen[notify.ShareClaimed] < 1 || seen[notify.ShareClaimLink] < 2 || seen[notify.ShareRevoked] < 1 {
		select {
		case body := <-got:
			if strings.Contains(body, "revealsecret") || strings.Contains(body, store.ClaimPrefix) {
				t.Fatalf("secret in webhook: %s", body)
			}
			var ev notify.Event
			_ = json.Unmarshal([]byte(body), &ev)
			seen[ev.Type]++
		case <-deadline:
			t.Fatalf("webhooks seen %v", seen)
		}
	}
}

// TestRevealShareTimerChecklist: a reveal share that expires on its timer
// shows the rotate checklist too.
func TestRevealShareTimerChecklist(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "timedreveal1"})
	sh, code := e.newPersonShare(map[string]any{"name": "Timed", "services": []string{"serpapi"}, "mode": "reveal",
		"ends_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	if st, _, raw := e.claim("redeem", code); st != 200 {
		t.Fatalf("redeem %d %s", st, raw)
	}
	e.api.sharingTick(context.Background(), time.Now().Add(2*time.Hour))
	v := e.shareView(sh.Share.ID)
	if v.Status != store.ShareExpired || len(v.RotateChecklist) != 1 || v.RotateChecklist[0].Service != "serpapi" {
		t.Fatalf("expired reveal %+v", v)
	}
	if !hasAudit(t, e, "share.rotate_keys") {
		t.Fatal("no rotate audit")
	}
}

// TestClaimRedeemOnceConcurrently: many parallel redeems, one token.
func TestClaimRedeemOnceConcurrently(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "racekey1"})
	rsh, code := e.newPersonShare(map[string]any{"name": "Race", "services": []string{"serpapi"}})
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _, _ := e.claim("redeem", code)
			mu.Lock()
			codes[st]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[200] != 1 || codes[409] != 7 {
		t.Fatalf("redeem results %v", codes)
	}
	toks, _ := e.st.ListAppTokens(context.Background(), *rsh.Share.AppID)
	live := 0
	for _, tk := range toks {
		if tk.RevokedAt == nil {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("%d live tokens", live)
	}
}

// TestGatePersonRoutes: on a deploy locked by the access password, the claim
// page, /me, the claim routes and the person's own calls work, and nothing
// else opens.
func TestGatePersonRoutes(t *testing.T) {
	e := newGateEnv(t)
	e.unlock(e.super)
	e.addKeys(map[string]any{"secret": "gatekey1"})
	_, code := e.newPersonShare(map[string]any{"name": "Locked", "services": []string{"serpapi"}})

	// The pages are the SPA shell, with no cookie.
	for _, p := range []string{"/claim/" + code, "/me"} {
		c, body, _ := peerCall(t, "GET", e.srv.URL+p, "")
		if c != 200 || body != "ui" {
			t.Fatalf("%s: %d %q", p, c, body)
		}
	}
	if st, _, raw := e.claim("preview", code); st != 200 {
		t.Fatalf("locked preview %d %s", st, raw)
	}
	tok, _ := e.redeemProxy(code)
	if c, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json?q=x", tok); c != 200 {
		t.Fatalf("locked proxy %d", c)
	}
	e.me(tok)
	if tools := mcpTools(t, e, tok); !tools["proxy_request"] {
		t.Fatalf("locked mcp %v", tools)
	}
	// Nothing else opens: not with the person's token, not near the open paths.
	for _, p := range []string{"/api/admin/me", "/api/admin/shares", "/api/claim", "/api/claim/v2/preview", "/api/claimx/v1/preview", "/api/auth/state"} {
		var out struct {
			Error apiError `json:"error"`
		}
		e.do(http.DefaultClient, "GET", p, nil, 401, &out, "Authorization", "Bearer "+tok)
		if out.Error.Code != "locked" {
			t.Fatalf("%s: %q", p, out.Error.Code)
		}
	}
	e.do(http.DefaultClient, "POST", "/claim/"+code, map[string]any{}, 401, nil)
	e.do(http.DefaultClient, "POST", "/me", map[string]any{}, 401, nil)
	for _, p := range []string{"/api/claim/v1/../../admin/me", "/api/claim/v1/%2e%2e/%2e%2e/admin/me", "/api/claim/v1%2F..%2F..%2Fadmin/me"} {
		req, _ := http.NewRequest("GET", e.srv.URL+"/x", nil)
		req.URL.Opaque = p
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && strings.Contains(string(b), "root@x.io") {
			t.Fatalf("%s leaked: %s", p, b)
		}
	}
}

// TestClaimCodeRedacted: a claim code never reaches a log line, and the
// redactor knows its prefix.
func TestClaimCodeRedacted(t *testing.T) {
	code := store.ClaimPrefix + "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	if out := redact.Patterns("open https://x.io/claim/" + code + " now"); strings.Contains(out, code) {
		t.Fatalf("not redacted: %s", out)
	}
	e := newEnv(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	peerCall(t, "GET", e.srv.URL+"/claim/"+code, "")
	if strings.Contains(buf.String(), code) || !strings.Contains(buf.String(), "/claim/[code]") {
		t.Fatalf("log: %s", buf.String())
	}
}
