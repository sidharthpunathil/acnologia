package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func keyStatuses(t *testing.T, e *env, serviceID string) []string {
	t.Helper()
	var keys struct{ Keys []store.Key }
	e.do(e.super, "GET", "/api/admin/services/"+serviceID+"/keys", nil, 200, &keys)
	out := []string{}
	for _, k := range keys.Keys {
		out = append(out, k.Status)
	}
	return out
}

// TestCallerShapedRejectionNeedsCorroboration: one app that makes the
// upstream answer 401 (a path it may not call, its own credentials) cannot
// take working keys every app shares out of rotation. The same verdict from
// a second app confirms it.
func TestCallerShapedRejectionNeedsCorroboration(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, sv := e.upstream("gen", nil, "k1", "k2")
	up.answer = func(k string, r *http.Request) (int, map[string]string, string) {
		if strings.HasSuffix(r.URL.Path, "/admin-only") {
			return 401, nil, `{"error":"not allowed"}`
		}
		return 200, nil, `{"ok":true}`
	}
	id := itoa(int(sv.ID))
	_, atok := e.app(map[string]any{"name": "a"})
	_, btok := e.app(map[string]any{"name": "b"})
	for i := 0; i < 3; i++ {
		if resp, body := call(t, e, "GET", "/proxy/gen/admin-only", atok); resp.StatusCode != 401 {
			t.Fatalf("call %d: %d %s", i, resp.StatusCode, body)
		}
	}
	if got := keyStatuses(t, e, id); strings.Join(got, ",") != "active,active" {
		t.Fatalf("one app's 401s changed the keys: %v", got)
	}
	if resp, body := call(t, e, "GET", "/proxy/gen/ok", btok); resp.StatusCode != 200 {
		t.Fatalf("other app: %d %s", resp.StatusCode, body)
	}
	// A success clears what was suspected, so a second app's 401s now only
	// start a new suspicion on the key that answered it.
	call(t, e, "GET", "/proxy/gen/admin-only", atok)
	call(t, e, "GET", "/proxy/gen/admin-only", btok)
	invalid := 0
	for _, s := range keyStatuses(t, e, id) {
		if s == store.StatusInvalid {
			invalid++
		}
	}
	if invalid == 0 {
		t.Fatalf("two apps agreeing should confirm the verdict: %v", keyStatuses(t, e, id))
	}
}

// TestReflectedBodyDoesNotBanKey: a rule that reads the error body (here
// ElevenLabs' suspended account text) matches text the caller made the
// upstream echo. The health check, which the caller cannot shape, finds the
// key fine, so it stays in rotation.
func TestReflectedBodyDoesNotBanKey(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up := &scripted{hits: map[string]int{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Key", r.Header.Get("xi-api-key"))
		up.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	up.answer = func(k string, r *http.Request) (int, map[string]string, string) {
		if r.URL.Path == "/v1/user/subscription" {
			return 200, nil, `{"character_count":0,"character_limit":100}`
		}
		if v := r.URL.Query().Get("voice"); v != "" {
			return 400, nil, `{"detail":{"message":"unknown voice ` + v + `"}}`
		}
		return 200, nil, `ok`
	}
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "elevenlabs", "slug": "el", "base_url": ts.URL}, 201, nil)
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "good"}}}, 201, nil)
	_, tok := e.app(map[string]any{"name": "x"})
	if resp, _ := call(t, e, "GET", "/proxy/el/v1/tts?voice=detected_unusual_activity", tok); resp.StatusCode != 400 {
		t.Fatalf("want the relayed 400, got %d", resp.StatusCode)
	}
	if got := keyStatuses(t, e, "1"); got[0] != store.StatusActive {
		t.Fatalf("reflected text banned the key: %v", got)
	}
	if resp, body := call(t, e, "GET", "/proxy/el/v1/tts", tok); resp.StatusCode != 200 {
		t.Fatalf("key out of rotation: %d %s", resp.StatusCode, body)
	}
}
