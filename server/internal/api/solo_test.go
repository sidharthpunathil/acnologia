package api

import (
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func newSoloEnv(t *testing.T) *env {
	t.Helper()
	e := newGateEnv(t)
	e.api.Cfg.AccessSignsIn = true
	return e
}

func TestPasswordOnlySignsInAsOwner(t *testing.T) {
	e := newSoloEnv(t)
	c := client()
	e.unlock(c)
	var st struct {
		SetupRequired bool        `json:"setup_required"`
		User          *store.User `json:"user"`
		PasswordOnly  bool        `json:"password_only"`
	}
	e.do(c, "GET", "/api/auth/state", nil, 200, &st)
	if st.User == nil || st.User.Role != store.RoleSuperadmin || st.SetupRequired || !st.PasswordOnly {
		t.Fatalf("state after unlock: %+v", st)
	}
	// The session cookie now works on its own, as for a normal sign in.
	var me store.User
	e.do(c, "GET", "/api/admin/me", nil, 200, &me)
	if me.ID != st.User.ID {
		t.Fatalf("me = %d, want %d", me.ID, st.User.ID)
	}
	// Signing out locks the browser too.
	e.do(c, "POST", "/api/auth/logout", map[string]any{}, 200, nil)
	e.do(c, "GET", "/api/admin/me", nil, 401, nil)
}

func TestPasswordOnlyNeedsTheGateCookie(t *testing.T) {
	e := newSoloEnv(t)
	// Locked: nothing.
	e.do(client(), "GET", "/api/admin/me", nil, 401, nil)
	// The access header passes the gate but does not make a session.
	var out struct {
		Error apiError `json:"error"`
	}
	e.do(client(), "GET", "/api/admin/me", nil, 401, &out, accessHeader, testAccessPassword)
	if out.Error.Code != "unauthenticated" {
		t.Fatalf("code = %q", out.Error.Code)
	}
}

func TestPasswordOnlyOffKeepsAccounts(t *testing.T) {
	e := newGateEnv(t) // AccessSignsIn false
	c := client()
	e.unlock(c)
	var st struct {
		User *store.User `json:"user"`
	}
	e.do(c, "GET", "/api/auth/state", nil, 200, &st)
	if st.User != nil {
		t.Fatal("signed in without an account while password only is off")
	}
}

func TestPasswordOnlyCreatesOwnerOnEmptyDB(t *testing.T) {
	e := newSoloEnv(t)
	if _, err := e.api.Store.DB.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.api.Store.DB.Exec(`DELETE FROM users`); err != nil {
		t.Skip("users cannot be cleared here:", err)
	}
	c := client()
	e.unlock(c)
	var me store.User
	e.do(c, "GET", "/api/admin/me", nil, 200, &me)
	if me.Role != store.RoleSuperadmin {
		t.Fatalf("owner role = %s", me.Role)
	}
}
