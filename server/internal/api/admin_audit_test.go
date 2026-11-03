package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// adminClient signs in a new admin (not superadmin) and returns its client.
func (e *env) adminClient() *http.Client {
	e.t.Helper()
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	c := client()
	e.do(c, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)
	return c
}

// cookieOf is the session cookie header a client sends to the test server.
func (e *env) cookieOf(c *http.Client) string {
	u, _ := url.Parse(e.srv.URL)
	h := ""
	for _, ck := range c.Jar.Cookies(u) {
		if h != "" {
			h += "; "
		}
		h += ck.Name + "=" + ck.Value
	}
	return h
}

// A slow app edit must not write back a lease_hidden it read before a token
// rotation cleared it: the rotating admin would then hold a token that
// leases hidden keys.
func TestUpdateAppDoesNotRestoreLeaseHidden(t *testing.T) {
	for _, route := range []string{"rotate-token", "tokens"} {
		t.Run(route, func(t *testing.T) {
			e := newEnv(t)
			admin := e.adminClient()
			appID, _ := e.app(map[string]any{"name": "vip", "mode": "both"})
			path := "/api/admin/apps/" + itoa(int(appID))
			e.do(e.super, "PATCH", path, map[string]any{"lease_hidden": true}, 200, nil)

			// The admin's edit arrives with its body held back.
			pr, pw := io.Pipe()
			req := httptest.NewRequest("PATCH", path, pr)
			req.Header.Set("Cookie", e.cookieOf(admin))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Requested-With", "test")
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				e.api.handler().ServeHTTP(rec, req)
				close(done)
			}()
			// The handler has read this byte, so it is past any read of the row.
			if _, err := pw.Write([]byte(`{`)); err != nil {
				t.Fatal(err)
			}
			var body any
			if route == "tokens" {
				body = map[string]any{"name": "mine"}
			}
			want := 200
			if route == "tokens" {
				want = 201
			}
			e.do(admin, "POST", path+"/"+route, body, want, nil)
			if a, _ := e.st.GetApp(context.Background(), appID); a.LeaseHidden {
				t.Fatal("a token made by an admin did not clear lease_hidden")
			}
			_, _ = pw.Write([]byte(`"mode":"both"}`))
			_ = pw.Close()
			<-done
			if rec.Code != 200 {
				t.Fatalf("edit: %d %s", rec.Code, rec.Body)
			}
			if a, _ := e.st.GetApp(context.Background(), appID); a.LeaseHidden {
				t.Fatal("a stale edit restored lease_hidden")
			}
		})
	}
}

// An edit leaves fields it did not send alone.
func TestUpdateAppWritesOnlySentFields(t *testing.T) {
	e := newEnv(t)
	appID, _ := e.app(map[string]any{"name": "a1", "mode": "lease", "allowed_services": []string{"serpapi"}})
	var out store.App
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"description": "d"}, 200, &out)
	if out.Name != "a1" || out.Mode != "lease" || len(out.AllowedServices) != 1 || out.Description != "d" || !out.Active {
		t.Fatalf("app after edit: %+v", out)
	}
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"name": "  "}, 400, nil)
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"mode": "nope"}, 400, nil)
	e.do(e.super, "PATCH", "/api/admin/apps/999", map[string]any{"name": "x"}, 404, nil)
}
