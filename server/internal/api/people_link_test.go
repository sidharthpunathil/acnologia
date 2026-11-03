package api

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// newLinkShare creates an "anyone with the link" share and returns it with
// the claim code.
func (e *env) newLinkShare(body map[string]any) (personShareResp, string) {
	e.t.Helper()
	body["reusable"] = true
	if _, ok := body["label"]; !ok {
		body["label"] = ""
	}
	return e.newPersonShare(body)
}

func TestLinkShareClaimedByMany(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "linkkey1"})
	sh, code := e.newLinkShare(map[string]any{"name": "Team", "services": []string{"serpapi"}, "max_claims": 3})
	if !sh.Share.Reusable || sh.Share.MaxClaims != 3 || sh.Share.Label != "Anyone with the link" || sh.Share.Status != "pending" {
		t.Fatalf("created %+v", sh.Share.Share)
	}
	if st, out, raw := e.claim("preview", code); st != 200 || !strings.Contains(raw, `"reusable":true`) {
		t.Fatalf("preview %d %v", st, out)
	}

	// Three people claim the same link, each with a token of their own.
	toks := map[string]bool{}
	for i := 0; i < 3; i++ {
		tok, _ := e.redeemProxy(code)
		toks[tok] = true
	}
	if len(toks) != 3 {
		t.Fatalf("tokens not distinct: %d", len(toks))
	}
	for tok := range toks {
		if c, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json?q=x", tok); c != 200 {
			t.Fatalf("claimed token: %d", c)
		}
	}
	// The cap holds.
	if st, out, _ := e.claim("redeem", code); st != 410 || errCode(out) != "claim_full" {
		t.Fatalf("fourth claim: %d %v", st, out)
	}
	if st, out, _ := e.claim("preview", code); st != 410 || errCode(out) != "claim_full" {
		t.Fatalf("preview when full: %d %v", st, out)
	}
	got, _ := e.st.GetShare(context.Background(), sh.Share.ID)
	if got.Status != "active" || got.ClaimCount != 3 || got.ClaimedAt == nil {
		t.Fatalf("after claims %+v", got)
	}

	// Raising the cap opens the link again.
	e.do(e.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"max_claims": 0}, 200, nil)
	e.redeemProxy(code)

	// A new link: the old one stops, claimed tokens keep working.
	var nl struct {
		ClaimURL string `json:"claim_url"`
	}
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/claim-link", map[string]any{"valid": "30d"}, 200, &nl)
	if st, out, _ := e.claim("redeem", code); st != 404 || errCode(out) != "claim_invalid" {
		t.Fatalf("old link after replace: %d %v", st, out)
	}
	newCode := nl.ClaimURL[strings.LastIndex(nl.ClaimURL, "/claim/")+len("/claim/"):]
	e.redeemProxy(newCode)
	for tok := range toks {
		if c, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json?q=x", tok); c != 200 {
			t.Fatalf("token after new link: %d", c)
		}
	}

	// Pause stops new claims; revoke cuts off everyone at once.
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/pause", nil, 200, nil)
	if st, out, _ := e.claim("redeem", newCode); st != 410 || errCode(out) != "claim_expired" {
		t.Fatalf("claim while paused: %d %v", st, out)
	}
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/resume", nil, 200, nil)
	e.do(e.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/revoke", nil, 200, nil)
	for tok := range toks {
		if c, _, _ := peerCall(t, "GET", e.srv.URL+"/proxy/serpapi/search.json?q=x", tok); c == 200 {
			t.Fatal("a token worked after revoke")
		}
	}
	if st, _, _ := e.claim("redeem", newCode); st == 200 {
		t.Fatal("claimed after revoke")
	}
}

func TestLinkShareCapHoldsConcurrently(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "linkkey2"})
	sh, code := e.newLinkShare(map[string]any{"name": "Race", "services": []string{"serpapi"}, "max_claims": 3})
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
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
	if codes[200] != 3 || codes[410] != 7 {
		t.Fatalf("redeem results %v", codes)
	}
	toks, _ := e.st.ListAppTokens(context.Background(), *sh.Share.AppID)
	if len(toks) != 3 {
		t.Fatalf("%d tokens", len(toks))
	}
}

func TestLinkShareRefusals(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "linkkey3"})
	for name, body := range map[string]map[string]any{
		"reveal":            {"name": "R", "services": []string{"serpapi"}, "reusable": true, "recipient": "person", "label": "x", "mode": "reveal"},
		"email":             {"name": "E", "services": []string{"serpapi"}, "reusable": true, "recipient": "person", "label": "x", "email": "a@b.co"},
		"server":            {"name": "S", "services": []string{"serpapi"}, "reusable": true, "recipient": "server"},
		"cap on a one time": {"name": "C", "services": []string{"serpapi"}, "recipient": "person", "label": "x", "max_claims": 2},
		"negative cap":      {"name": "N", "services": []string{"serpapi"}, "reusable": true, "recipient": "person", "max_claims": -1},
		"bad validity":      {"name": "V", "services": []string{"serpapi"}, "reusable": true, "recipient": "person", "claim_valid": "90d"},
	} {
		var out struct {
			Error apiError `json:"error"`
		}
		e.do(e.super, "POST", "/api/admin/shares", body, 400, &out)
		if out.Error.Message == "" {
			t.Fatalf("%s: no message", name)
		}
	}
	// Recipient person is implied, and 30 days is allowed for a link.
	var out personShareResp
	e.do(e.super, "POST", "/api/admin/shares", map[string]any{"name": "Implied", "services": []string{"serpapi"}, "reusable": true, "claim_valid": "30d"}, 201, &out)
	if out.Share.Recipient != "person" || !out.Share.Reusable {
		t.Fatalf("implied recipient %+v", out.Share.Share)
	}
	// Reusable is fixed at creation.
	e.do(e.super, "PATCH", "/api/admin/shares/"+out.Share.ID, map[string]any{"reusable": false}, 400, nil)
}
