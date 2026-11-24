package ctl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// acn oauth connect opens the sign in link and polls until it is used.
func TestOAuthConnectWaits(t *testing.T) {
	var polls atomic.Int32
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/admin/services":
			_, _ = w.Write([]byte(`{"services":[{"id":7,"slug":"gsc","name":"GSC","provider":"google"}]}`))
		case r.Method == "POST" && r.URL.Path == "/api/admin/services/7/oauth/connect":
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"connect_id":"oc_1","status":"pending","url":"https://accounts.example/auth?x=1","status_url":"/api/admin/oauth/connects/oc_1"}`))
		case r.URL.Path == "/api/admin/oauth/connects/oc_1":
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"connect_id":"oc_1","status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"connect_id":"oc_1","status":"done","key_id":12,"account":"ana@example.com"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c, out, errb, opened := newTestCLI(t, "")
	c.sleep = func(time.Duration) {}
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_test")
	if code := c.exec([]string{"oauth", "connect", "gsc", "--field", "developer_token=dt"}); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errb)
	}
	if len(*opened) != 1 || !strings.HasPrefix((*opened)[0], "https://accounts.example/auth") {
		t.Fatalf("opened %v", *opened)
	}
	if f, _ := got["fields"].(map[string]any); f["developer_token"] != "dt" {
		t.Fatalf("body %v", got)
	}
	if !strings.Contains(out.String(), "Connected ana@example.com as key 12") || polls.Load() != 3 {
		t.Fatalf("out %q after %d polls", out, polls.Load())
	}
}
