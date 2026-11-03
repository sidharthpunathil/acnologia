package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type scopedView struct {
	store.Share
	Usage       shareUsage                 `json:"usage"`
	ServiceKeys map[string]serviceKeyCount `json:"service_keys"`
}

// usedKeys sends n proxied requests with a share token and returns how often
// each upstream key answered.
func usedKeys(t *testing.T, a *env, tok string, n int) map[string]int {
	t.Helper()
	out := map[string]int{}
	for i := 0; i < n; i++ {
		code, body, _ := peerCall(t, "GET", a.srv.URL+"/proxy/serpapi/search.json?q="+itoa(i), tok)
		if code != 200 {
			t.Fatalf("request %d: %d %s", i, code, body)
		}
		var got struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal([]byte(body), &got)
		out[got.Key]++
	}
	return out
}

func onlyKeys(t *testing.T, used map[string]int, want ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, k := range want {
		ok[k] = true
		if used[k] == 0 {
			t.Fatalf("key %s never used: %v", k, used)
		}
	}
	for k := range used {
		if !ok[k] {
			t.Fatalf("key %s used outside the scope: %v", k, used)
		}
	}
}

func manifestAvailable(t *testing.T, a *env, tok string) int {
	t.Helper()
	code, body, _ := peerCall(t, "GET", a.srv.URL+"/api/peer/v1/manifest", tok)
	if code != 200 {
		t.Fatalf("manifest: %d %s", code, body)
	}
	var m peer.Manifest
	_ = json.Unmarshal([]byte(body), &m)
	if len(m.Services) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	return m.Services[0].Available
}

func TestShareKeyScopeCount(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	keys := a.addKeys(map[string]any{"secret": "k1", "priority": 1}, map[string]any{"secret": "k2", "priority": 2},
		map[string]any{"secret": "k3", "priority": 3}, map[string]any{"secret": "k4", "priority": 4})
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"},
		"key_scopes": map[string]any{"serpapi": map[string]any{"mode": "count", "count": 2}}})
	if sc := sh.Share.KeyScopes["serpapi"]; sc.Mode != store.ScopeCount || sc.Count != 2 {
		t.Fatalf("scopes %+v", sh.Share.KeyScopes)
	}
	_, tok := a.redeemDirect(b, sh.Invite, nil)

	var v scopedView
	a.do(a.super, "GET", "/api/admin/shares/"+sh.Share.ID, nil, 200, &v)
	if got := v.ServiceKeys["serpapi"]; got.KeysShared != 2 || got.KeysTotal != 4 {
		t.Fatalf("service keys %+v", v.ServiceKeys)
	}
	onlyKeys(t, usedKeys(t, a, tok, 20), "k1", "k2")
	if n := manifestAvailable(t, a, tok); n != 2 {
		t.Fatalf("manifest available %d", n)
	}

	// A key leaving rotation is replaced by the next one.
	if _, err := a.st.DB.Exec(`UPDATE keys SET status=? WHERE id=?`, store.StatusInvalid, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	onlyKeys(t, usedKeys(t, a, tok, 20), "k2", "k3")

	// A key cooling down still counts, so the share does not drift onto k4.
	if _, err := a.st.DB.Exec(`UPDATE keys SET cooldown_until=? WHERE id=?`, time.Now().Add(time.Hour).Unix(), keys[1].ID); err != nil {
		t.Fatal(err)
	}
	onlyKeys(t, usedKeys(t, a, tok, 10), "k3")
	if n := manifestAvailable(t, a, tok); n != 1 {
		t.Fatalf("manifest available with a cooling key %d", n)
	}

	// A local app is not scoped.
	_, local := a.app(map[string]any{"name": "local"})
	onlyKeys(t, usedKeys(t, a, local, 20), "k3", "k4")
}

func TestShareKeyScopePick(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	keys := a.addKeys(map[string]any{"secret": "k1"}, map[string]any{"secret": "k2"}, map[string]any{"secret": "k3"},
		map[string]any{"secret": "k4"}, map[string]any{"secret": "khidden", "hidden": true})
	pick := func(ids ...int64) map[string]any {
		return map[string]any{"serpapi": map[string]any{"mode": "pick", "key_ids": ids}}
	}
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}, "key_scopes": pick(keys[1].ID, keys[3].ID)})
	_, tok := a.redeemDirect(b, sh.Invite, nil)
	onlyKeys(t, usedKeys(t, a, tok, 40), "k2", "k4")
	if n := manifestAvailable(t, a, tok); n != 2 {
		t.Fatalf("manifest available %d", n)
	}

	// Scope changes apply on the very next request (the cache is cleared).
	var v scopedView
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"key_scopes": pick(keys[0].ID)}, 200, &v)
	if got := v.ServiceKeys["serpapi"]; got.KeysShared != 1 || got.KeysTotal != 4 {
		t.Fatalf("service keys after update %+v", v.ServiceKeys)
	}
	onlyKeys(t, usedKeys(t, a, tok, 10), "k1")
	// Pause and resume keep the scope.
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/pause", nil, 200, nil)
	a.do(a.super, "POST", "/api/admin/shares/"+sh.Share.ID+"/resume", nil, 200, nil)
	onlyKeys(t, usedKeys(t, a, tok, 5), "k1")
	// A picked key that stops working leaves no key: the normal no key answer.
	if _, err := a.st.DB.Exec(`UPDATE keys SET status=? WHERE id=?`, store.StatusInvalid, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := peerCall(t, "GET", a.srv.URL+"/proxy/serpapi/search.json", tok); code != 503 {
		t.Fatalf("no key in scope: %d %s", code, body)
	}
	// Back to every key.
	v = scopedView{}
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"key_scopes": map[string]any{"serpapi": map[string]any{"mode": "all"}}}, 200, &v)
	if len(v.KeyScopes) != 0 || v.ServiceKeys["serpapi"].KeysShared != 4 {
		t.Fatalf("after all %+v %+v", v.KeyScopes, v.ServiceKeys)
	}
	onlyKeys(t, usedKeys(t, a, tok, 20), "k2", "k3", "k4")

	// The audit records the modes, never key data.
	var au struct {
		Audit []store.AuditEntry `json:"audit"`
	}
	a.do(a.super, "GET", "/api/admin/audit?limit=50", nil, 200, &au)
	dump, _ := json.Marshal(au)
	if !strings.Contains(string(dump), "serpapi 1 picked") || strings.Contains(string(dump), "khidden") {
		t.Fatalf("audit %s", dump)
	}
}

func TestShareKeyScopeValidation(t *testing.T) {
	a := newEnv(t)
	keys := a.addKeys(map[string]any{"secret": "k1"}, map[string]any{"secret": "khidden", "hidden": true})
	var other store.Service
	a.do(a.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serp2", "base_url": a.upURL}, 201, &other)
	var foreign struct {
		Keys []store.Key `json:"keys"`
	}
	a.do(a.super, "POST", "/api/admin/services/"+itoa(int(other.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "k9"}}}, 201, &foreign)

	body := func(scope map[string]any, extra ...any) map[string]any {
		m := map[string]any{"name": "x", "services": []string{"serpapi"}, "key_scopes": map[string]any{"serpapi": scope}}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i].(string)] = extra[i+1]
		}
		return m
	}
	bad := []map[string]any{
		body(map[string]any{"mode": "pick", "key_ids": []int64{foreign.Keys[0].ID}}),
		body(map[string]any{"mode": "pick", "key_ids": []int64{999999}}),
		body(map[string]any{"mode": "pick", "key_ids": []int64{keys[1].ID}}),
		body(map[string]any{"mode": "pick", "key_ids": []int64{}}),
		body(map[string]any{"mode": "count", "count": 0}),
		body(map[string]any{"mode": "some"}),
		{"name": "x", "services": []string{"serpapi"}, "key_scopes": map[string]any{"serp2": map[string]any{"mode": "count", "count": 1}}},
	}
	for _, in := range bad {
		a.do(a.super, "POST", "/api/admin/shares", in, 400, nil)
	}
	// A hidden key may be picked once include_hidden is on.
	sh := a.newShare(body(map[string]any{"mode": "pick", "key_ids": []int64{keys[0].ID, keys[1].ID}}, "include_hidden", true))
	if got := sh.Share.KeyScopes["serpapi"].KeyIDs; len(got) != 2 {
		t.Fatalf("picked %v", got)
	}
	// Unsharing a service drops its scope.
	var v scopedView
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"services": []string{"serp2"}}, 200, &v)
	if len(v.KeyScopes) != 0 {
		t.Fatalf("scope kept for an unshared service: %+v", v.KeyScopes)
	}
}

func TestAppKeyScopeCache(t *testing.T) {
	a := newEnv(t)
	ctx := context.Background()
	appID, _ := a.app(map[string]any{"name": "plain"})
	if sc, err := a.st.AppKeyScope(ctx, appID, "serpapi"); err != nil || sc != nil {
		t.Fatalf("plain app scope %v %v", sc, err)
	}
	sh := a.newShare(map[string]any{"name": "x", "services": []string{"serpapi"}})
	if sc, _ := a.st.AppKeyScope(ctx, *sh.Share.AppID, "serpapi"); sc != nil {
		t.Fatalf("unscoped share %v", sc)
	}
	a.do(a.super, "PATCH", "/api/admin/shares/"+sh.Share.ID, map[string]any{"key_scopes": map[string]any{"serpapi": map[string]any{"mode": "count", "count": 3}}}, 200, nil)
	if sc, _ := a.st.AppKeyScope(ctx, *sh.Share.AppID, "serpapi"); sc == nil || sc.Count != 3 {
		t.Fatalf("scope after update %v", sc)
	}
}
