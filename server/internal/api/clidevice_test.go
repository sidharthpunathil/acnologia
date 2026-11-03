package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type deviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceGrant struct {
	Token   string `json:"token"`
	TokenID int64  `json:"token_id"`
	Scope   string `json:"scope"`
	User    struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
	Error     apiError  `json:"error"`
}

// fakeClock lets a test move the device store's time by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }
func useFakeClock(e *env) *fakeClock {
	c := &fakeClock{t: time.Now()}
	e.api.devices.now = c.now
	return c
}

func (e *env) deviceStart() deviceStart {
	e.t.Helper()
	var out deviceStart
	e.do(http.DefaultClient, "POST", "/api/cli/device", map[string]any{"client": "acn", "hostname": "laptop", "os": "darwin/arm64"}, 200, &out)
	return out
}

func (e *env) devicePoll(code string, want int) deviceGrant {
	e.t.Helper()
	var out deviceGrant
	e.do(http.DefaultClient, "POST", "/api/cli/device/token", map[string]any{"device_code": code}, want, &out)
	return out
}

var userCodeRe = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`)

func TestCLIDeviceFlowEndToEnd(t *testing.T) {
	e := newGateEnv(t)
	clock := useFakeClock(e)

	// Create needs neither the gate nor an account.
	st := e.deviceStart()
	if !userCodeRe.MatchString(st.UserCode) || len(st.DeviceCode) < 40 {
		t.Fatalf("codes: %+v", st)
	}
	if st.ExpiresIn != 600 || st.Interval != 5 {
		t.Fatalf("timing: %+v", st)
	}
	if st.VerificationURI != e.srv.URL+"/cli/authorize" || st.VerificationURIComplete != st.VerificationURI+"?code="+st.UserCode {
		t.Fatalf("urls: %+v", st)
	}
	// Only the hash of the device code is kept.
	e.api.devices.mu.Lock()
	for h, r := range e.api.devices.byHash {
		if h == st.DeviceCode || r.deviceHash != crypto.SHA256(st.DeviceCode) {
			t.Fatal("device code is not stored as its SHA-256")
		}
	}
	e.api.devices.mu.Unlock()

	if g := e.devicePoll(st.DeviceCode, 400); g.Error.Code != "authorization_pending" {
		t.Fatalf("first poll: %+v", g)
	}

	// The browser side is behind the gate and a session.
	e.do(client(), "GET", "/api/cli/device/lookup?user_code="+st.UserCode, nil, 401, nil)
	e.unlock(e.super)
	var look struct {
		Request map[string]any `json:"request"`
	}
	// A lower case code without the dash still matches.
	typed := strings.ToLower(strings.ReplaceAll(st.UserCode, "-", ""))
	e.do(e.super, "GET", "/api/cli/device/lookup?user_code="+typed, nil, 200, &look)
	if look.Request["hostname"] != "laptop" || look.Request["os"] != "darwin/arm64" || look.Request["state"] != "pending" {
		t.Fatalf("lookup: %v", look.Request)
	}
	e.do(e.super, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "nope"}, 400, nil)
	e.do(e.super, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "write"}, 200, nil)
	// Only once.
	e.do(e.super, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "admin"}, 409, nil)

	clock.add(6 * time.Second)
	g := e.devicePoll(st.DeviceCode, 200)
	if !strings.HasPrefix(g.Token, "acnadm_") || g.Scope != "write" || g.User.Email != "root@x.io" || g.TokenID == 0 {
		t.Fatalf("grant: %+v", g)
	}
	if d := time.Until(g.ExpiresAt); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("expires in %v, want 90 days", d)
	}
	// Handed out once: the request is gone.
	clock.add(6 * time.Second)
	if g2 := e.devicePoll(st.DeviceCode, 400); g2.Error.Code != "expired_token" {
		t.Fatalf("second grant: %+v", g2)
	}

	// The token is a real admin token with the chosen scope and name, it
	// passes the gate without the password, and it acts as an admin, not a
	// superadmin.
	tok, err := e.st.ListTokens(t.Context(), 0)
	if err != nil || len(tok) != 1 || tok[0].Name != "acn on laptop" || tok[0].Scope != store.ScopeWrite {
		t.Fatalf("tokens: %+v %v", tok, err)
	}
	var me struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 200, &me, "Authorization", "Bearer "+g.Token)
	if me.Role != "admin" {
		t.Fatalf("token acts as %q", me.Role)
	}
	e.do(http.DefaultClient, "GET", "/api/admin/users", nil, 403, nil, "Authorization", "Bearer "+g.Token)
	var audit struct {
		Entries []store.AuditEntry `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	found := false
	for _, a := range audit.Entries {
		if a.Action == "token.create" && a.Target == "acn on laptop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no audit entry: %+v", audit.Entries)
	}

	// The token can revoke itself (acn logout), and nothing else.
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "ci", "scope": "read"}, 201, nil)
	e.do(http.DefaultClient, "DELETE", "/api/admin/tokens/999", nil, 403, nil, "Authorization", "Bearer "+g.Token)
	e.do(http.DefaultClient, "DELETE", "/api/admin/tokens/"+strconv.FormatInt(g.TokenID, 10), nil, 200, nil, "Authorization", "Bearer "+g.Token)
	e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 401, nil, "Authorization", "Bearer "+g.Token)
}

func TestCLIDeviceDeny(t *testing.T) {
	e := newEnv(t)
	st := e.deviceStart()
	e.do(e.super, "POST", "/api/cli/device/deny", map[string]any{"user_code": st.UserCode}, 200, nil)
	if g := e.devicePoll(st.DeviceCode, 400); g.Error.Code != "access_denied" {
		t.Fatalf("poll after deny: %+v", g)
	}
	e.do(e.super, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "read"}, 404, nil)
}

func TestCLIDeviceExpiry(t *testing.T) {
	e := newEnv(t)
	clock := useFakeClock(e)
	st := e.deviceStart()
	clock.add(deviceTTL + time.Second)
	e.do(e.super, "GET", "/api/cli/device/lookup?user_code="+st.UserCode, nil, 404, nil)
	if g := e.devicePoll(st.DeviceCode, 400); g.Error.Code != "expired_token" {
		t.Fatalf("poll after expiry: %+v", g)
	}
}

func TestCLIDeviceSlowDown(t *testing.T) {
	e := newEnv(t)
	clock := useFakeClock(e)
	st := e.deviceStart()
	e.devicePoll(st.DeviceCode, 400)
	clock.add(time.Second)
	var out struct {
		Error    apiError `json:"error"`
		Interval int      `json:"interval"`
	}
	e.do(http.DefaultClient, "POST", "/api/cli/device/token", map[string]any{"device_code": st.DeviceCode}, 400, &out)
	if out.Error.Code != "slow_down" || out.Interval != 10 {
		t.Fatalf("fast poll: %+v", out)
	}
	// The new interval holds: 6s later is still too soon, 10s is fine.
	clock.add(6 * time.Second)
	if g := e.devicePoll(st.DeviceCode, 400); g.Error.Code != "slow_down" {
		t.Fatalf("poll at 6s: %+v", g)
	}
	clock.add(15 * time.Second)
	if g := e.devicePoll(st.DeviceCode, 400); g.Error.Code != "authorization_pending" {
		t.Fatalf("poll at 15s: %+v", g)
	}
}

func TestCLIDeviceScopeCappedByRole(t *testing.T) {
	e := newEnv(t)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	st := e.deviceStart()
	e.do(m, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "write"}, 403, nil)
	e.do(m, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "read", "label": "work laptop"}, 200, nil)
	g := e.devicePoll(st.DeviceCode, 200)
	if g.Scope != "read" || g.User.Email != "m@x.io" {
		t.Fatalf("grant: %+v", g)
	}
	toks, _ := e.st.ListTokens(t.Context(), 0)
	if len(toks) != 1 || toks[0].Name != "work laptop" {
		t.Fatalf("tokens: %+v", toks)
	}
}

func TestCLIDeviceNeedsSession(t *testing.T) {
	e := newEnv(t)
	var tok struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "ci", "scope": "admin"}, 201, &tok)
	st := e.deviceStart()
	// An API token cannot approve a sign in, so one token cannot mint more.
	e.do(http.DefaultClient, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "read"}, 403, nil, "Authorization", "Bearer "+tok.Token)
	e.do(client(), "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "read"}, 401, nil)
}

func TestCLIDeviceRateLimits(t *testing.T) {
	e := newEnv(t)
	useFakeClock(e)
	// Wrong user codes: 10 a minute, then refused unseen, even a right one.
	st := e.deviceStart()
	for i := 0; i < deviceLookupLimit; i++ {
		e.do(e.super, "GET", "/api/cli/device/lookup?user_code=BBBB-BBBB", nil, 404, nil)
	}
	e.do(e.super, "GET", "/api/cli/device/lookup?user_code="+st.UserCode, nil, 429, nil)
	e.do(e.super, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "read"}, 429, nil)
	e.api.devices.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	e.do(e.super, "GET", "/api/cli/device/lookup?user_code="+st.UserCode, nil, 200, nil)

	// Creating requests is limited too, per address.
	e2 := newEnv(t)
	for i := 0; i < deviceCreateClientLimit; i++ {
		e2.deviceStart()
	}
	e2.do(http.DefaultClient, "POST", "/api/cli/device", map[string]any{"hostname": "x"}, 429, nil)

	// And polling with unknown codes.
	e3 := newEnv(t)
	e3.api.devices.poll = newClientLimit(3, 100, 100, 0)
	for i := 0; i < 3; i++ {
		e3.devicePoll("nope", 400)
	}
	e3.devicePoll("nope", 429)
}

// TestCLIDeviceLimitsArePerSource: one source that spends the create, poll
// or lookup budget cannot stop anyone else's sign in.
func TestCLIDeviceLimitsArePerSource(t *testing.T) {
	e := newEnv(t)
	clock := useFakeClock(e)
	d := e.api.devices

	// Another member burns their lookups (and the global miss ceiling) on
	// wrong codes; the superadmin still finds and approves a live one.
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	d.lookup = newClientLimit(deviceLookupLimit, deviceLookupDaily, deviceLookupLimit, deviceLookupLimit)
	st := e.deviceStart()
	for i := 0; i < deviceLookupLimit; i++ {
		e.do(m, "GET", "/api/cli/device/lookup?user_code=BBBB-BBBB", nil, 404, nil)
	}
	e.do(m, "GET", "/api/cli/device/lookup?user_code="+st.UserCode, nil, 429, nil)
	e.do(e.super, "GET", "/api/cli/device/lookup?user_code="+st.UserCode, nil, 200, nil)
	e.do(e.super, "POST", "/api/cli/device/approve", map[string]any{"user_code": st.UserCode, "scope": "read"}, 200, nil)

	// A live request's polls never spend the poll budget, even once unknown
	// codes have spent all of it.
	d.poll = newClientLimit(2, 2, 2, 2)
	st2 := e.deviceStart()
	e.devicePoll("nope", 400)
	e.devicePoll("nope", 400)
	e.devicePoll("nope", 429)
	clock.add(deviceInterval)
	e.devicePoll(st2.DeviceCode, 400) // authorization_pending, not 429

	// Creates: a source that spent its daily budget waits alone.
	d.mu.Lock()
	for i := 0; i < deviceCreateClientDaily; i++ {
		if i%deviceCreateClientLimit == 0 {
			clock.add(time.Minute)
		}
		if !d.create.take("203.0.113.9", clock.now()) {
			d.mu.Unlock()
			t.Fatalf("create %d refused early", i)
		}
	}
	clock.add(time.Minute)
	spent := d.create.take("203.0.113.9", clock.now())
	d.mu.Unlock()
	if spent {
		t.Fatal("a source past its daily budget could still create")
	}
	e.deviceStart() // 127.0.0.1 is unaffected

	// One source cannot hold every pending slot.
	e2 := newEnv(t)
	useFakeClock(e2)
	e2.api.devices.create = newClientLimit(1000, 1000, 1000, 0)
	for i := 0; i < devicePendingPerClient; i++ {
		e2.deviceStart()
	}
	e2.do(http.DefaultClient, "POST", "/api/cli/device", map[string]any{"hostname": "x"}, 429, nil)
	e2.api.devices.mu.Lock()
	n := len(e2.api.devices.byHash)
	e2.api.devices.mu.Unlock()
	if n != devicePendingPerClient {
		t.Fatalf("pending = %d, want %d", n, devicePendingPerClient)
	}
}

func TestNormalUserCode(t *testing.T) {
	for in, want := range map[string]string{
		"wdjb-mjht": "WDJB-MJHT", " WDJBMJHT ": "WDJB-MJHT", "WDJB MJHT": "WDJB-MJHT",
		"WDJB-MJH": "", "AEIO-UUUU": "", "": "",
	} {
		if got := normalUserCode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	for range 50 {
		if c := newUserCode(); !userCodeRe.MatchString(c) {
			t.Fatalf("bad code %q", c)
		}
	}
}

func TestGateAdminTokenPasses(t *testing.T) {
	e := newGateEnv(t)
	e.unlock(e.super)
	var tok struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "ci", "scope": "read"}, 201, &tok)
	_, appTok := e.app(map[string]any{"name": "site"})

	// A valid admin token passes the lock with no password and no cookie.
	e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 200, nil, "Authorization", "Bearer "+tok.Token)
	var st gateStateBody
	e.do(http.DefaultClient, "GET", "/api/gate", nil, 200, &st, "Authorization", "Bearer "+tok.Token)
	if st.Locked {
		t.Fatalf("gate state with a token: %v", st)
	}

	refused := func(auth string) {
		t.Helper()
		var out struct {
			Error apiError `json:"error"`
		}
		e.do(http.DefaultClient, "GET", "/api/admin/me", nil, 401, &out, "Authorization", auth)
		if out.Error.Code != "locked" {
			t.Fatalf("%q: code %q, want locked", auth, out.Error.Code)
		}
	}
	refused("Bearer acnadm_made_up_and_wrong")
	refused("Bearer " + tok.Token + "x")
	refused("Bearer " + appTok)
	refused(tok.Token) // no Bearer
	// The app token still works where it belongs.
	e.do(http.DefaultClient, "GET", "/api/v1/services", nil, 200, nil, "Authorization", "Bearer "+appTok)

	// A revoked token stops passing at once.
	var list struct {
		Tokens []store.AdminToken `json:"tokens"`
	}
	e.do(e.super, "GET", "/api/admin/tokens", nil, 200, &list)
	e.do(e.super, "DELETE", "/api/admin/tokens/"+strconv.FormatInt(list.Tokens[0].ID, 10), nil, 200, nil)
	refused("Bearer " + tok.Token)
}
