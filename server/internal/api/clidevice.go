package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The CLI sign in: a device authorization flow (RFC 8628 in spirit, the
// error codes match), so `acn login` gets an admin API token without the
// user ever pasting one, and without the CLI knowing the site password.
//
//  1. The CLI calls POST /api/cli/device and gets a device_code (its secret)
//     and a user_code (shown to the person).
//  2. The person opens /cli/authorize?code=<user_code> in a browser that is
//     past the access gate and signed in, compares the code and approves.
//  3. Approving mints an admin API token for that person, named
//     "acn on <hostname>", 90 days, scope at most their role.
//  4. The CLI polls POST /api/cli/device/token with its device_code and gets
//     the token once.
//
// Create and poll are open in the access gate: they reveal nothing until a
// signed in person approves. The device code is 32 random bytes and only its
// SHA-256 is kept. Requests live in memory for 10 minutes; a restart drops
// the pending ones, and the CLI just starts over.
const (
	deviceTTL      = 10 * time.Minute
	deviceInterval = 5 * time.Second
	// deviceTokenTTL is how long an approved CLI token lasts.
	deviceTokenTTL = 90 * 24 * time.Hour
	// devicePendingCap bounds memory: at most this many live requests.
	devicePendingCap = 200
	// userCodeAlphabet has no vowels (no words) and no look alikes (0/O, 1/I).
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
)

// Global limits, like the access gate's: behind a proxy a per address limit
// is one header away from no limit.
const (
	deviceCreateLimit = 30 // new requests a minute
	deviceCreateDaily = 1000
	devicePollLimit   = 600 // polls a minute, about 50 CLIs waiting at once
	devicePollDaily   = 50000
	// Wrong user codes typed or looked up. 20^8 codes, 10 a minute and 200 a
	// day: guessing a live one is hopeless.
	deviceLookupLimit = 10
	deviceLookupDaily = 200
)

type deviceRequest struct {
	deviceHash string
	userCode   string
	client     string
	hostname   string
	os         string
	created    time.Time
	expires    time.Time
	interval   time.Duration
	lastPoll   time.Time

	// state is pending, approved or denied.
	state string
	// Set on approval, handed to the CLI once and then dropped.
	token    string
	tokenID  int64
	scope    string
	user     *store.User
	tokenExp time.Time
}

// windowLimit is a fixed window counter with a per minute and a daily cap.
// A perDay of 0 means no daily cap.
type windowLimit struct {
	perMin, perDay  int
	n, day          int
	since, daySince time.Time
}

// take counts one hit at now and reports whether it is within the limits.
// The caller holds the device store's lock.
func (l *windowLimit) take(now time.Time) bool {
	if !l.ok(now) {
		return false
	}
	l.n++
	l.day++
	return true
}

// ok reports whether another hit would be allowed, without counting it.
func (l *windowLimit) ok(now time.Time) bool {
	if now.Sub(l.since) >= time.Minute {
		l.n, l.since = 0, now
	}
	if now.Sub(l.daySince) >= 24*time.Hour {
		l.day, l.daySince = 0, now
	}
	return l.n < l.perMin && (l.perDay <= 0 || l.day < l.perDay)
}

// wait is how long until the exhausted window resets, 0 when none is.
// Call it after ok.
func (l *windowLimit) wait(now time.Time) time.Duration {
	if l.perDay > 0 && l.day >= l.perDay {
		return l.daySince.Add(24 * time.Hour).Sub(now)
	}
	if l.n >= l.perMin {
		return l.since.Add(time.Minute).Sub(now)
	}
	return 0
}

// idle reports whether every window has passed, so the counter holds
// nothing worth keeping.
func (l *windowLimit) idle(now time.Time) bool {
	if now.Sub(l.since) < time.Minute {
		return false
	}
	return l.perDay <= 0 || now.Sub(l.daySince) >= 24*time.Hour
}

type deviceStore struct {
	mu       sync.Mutex
	byHash   map[string]*deviceRequest
	byCode   map[string]*deviceRequest
	now      func() time.Time
	interval time.Duration
	ttl      time.Duration
	create   windowLimit
	poll     windowLimit
	lookup   windowLimit
}

func newDeviceStore() *deviceStore {
	return &deviceStore{
		byHash:   map[string]*deviceRequest{},
		byCode:   map[string]*deviceRequest{},
		now:      time.Now,
		interval: deviceInterval,
		ttl:      deviceTTL,
		create:   windowLimit{perMin: deviceCreateLimit, perDay: deviceCreateDaily},
		poll:     windowLimit{perMin: devicePollLimit, perDay: devicePollDaily},
		lookup:   windowLimit{perMin: deviceLookupLimit, perDay: deviceLookupDaily},
	}
}

// sweep drops expired requests. Caller holds mu.
func (d *deviceStore) sweep(now time.Time) {
	for h, r := range d.byHash {
		if !now.Before(r.expires) {
			delete(d.byHash, h)
			delete(d.byCode, r.userCode)
		}
	}
}

func (d *deviceStore) drop(r *deviceRequest) {
	delete(d.byHash, r.deviceHash)
	delete(d.byCode, r.userCode)
}

// newUserCode is 8 letters from userCodeAlphabet, shown as XXXX-XXXX.
func newUserCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		// 256 is not a multiple of 20; the bias (16 of 256) is harmless for
		// a code that lives 10 minutes behind a lookup limit.
		out = append(out, userCodeAlphabet[int(c)%len(userCodeAlphabet)])
	}
	return string(out)
}

// normalUserCode upper cases a typed code and puts the dash back, so
// "wdjbmjht" and "WDJB-MJHT" match.
func normalUserCode(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
	if len(s) != 8 {
		return ""
	}
	for _, c := range s {
		if !strings.ContainsRune(userCodeAlphabet, c) {
			return ""
		}
	}
	return s[:4] + "-" + s[4:]
}

// clip trims s to n bytes of printable text.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func (s *Server) baseURL(r *http.Request) string {
	if b := strings.TrimRight(s.Cfg.PublicURL, "/"); b != "" {
		return b
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// cliDeviceCreate starts a sign in. Open in the gate.
func (s *Server) cliDeviceCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		Client   string `json:"client"`
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !decode(w, r, &in) {
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, 500, "internal", "no randomness")
		return
	}
	deviceCode := base64.RawURLEncoding.EncodeToString(raw)

	d := s.devices
	d.mu.Lock()
	now := d.now()
	d.sweep(now)
	if !d.create.take(now) {
		d.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "too_many_requests", "too many sign in requests, wait a minute")
		return
	}
	if len(d.byHash) >= devicePendingCap {
		d.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "too_many_requests", "too many sign ins are waiting, try again in a few minutes")
		return
	}
	code := newUserCode()
	for d.byCode[code] != nil {
		code = newUserCode()
	}
	req := &deviceRequest{
		deviceHash: crypto.SHA256(deviceCode),
		userCode:   code,
		client:     clip(in.Client, 40),
		hostname:   clip(in.Hostname, 80),
		os:         clip(in.OS, 40),
		created:    now,
		expires:    now.Add(d.ttl),
		interval:   d.interval,
		state:      "pending",
	}
	if req.client == "" {
		req.client = "acn"
	}
	if req.hostname == "" {
		req.hostname = "unknown host"
	}
	d.byHash[req.deviceHash] = req
	d.byCode[code] = req
	interval, ttl := d.interval, d.ttl
	d.mu.Unlock()

	verify := s.baseURL(r) + "/cli/authorize"
	writeJSON(w, 200, map[string]any{
		"device_code":               deviceCode,
		"user_code":                 code,
		"verification_uri":          verify,
		"verification_uri_complete": verify + "?code=" + code,
		"expires_in":                int(ttl.Seconds()),
		"interval":                  max(1, int(interval.Seconds())),
	})
}

// cliDeviceToken is the CLI's poll. Open in the gate.
func (s *Server) cliDeviceToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		DeviceCode string `json:"device_code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !decode(w, r, &in) {
		return
	}
	d := s.devices
	d.mu.Lock()
	now := d.now()
	if !d.poll.take(now) {
		d.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "slow_down", "too many polls, slow down")
		return
	}
	d.sweep(now)
	req := d.byHash[crypto.SHA256(in.DeviceCode)]
	if req == nil || in.DeviceCode == "" {
		d.mu.Unlock()
		writeErr(w, 400, "expired_token", "this sign in request expired or does not exist; run acn login again")
		return
	}
	// A poll sooner than the interval allows (a fifth of it as slack for
	// network jitter) is refused and the interval grows, as in RFC 8628.
	if !req.lastPoll.IsZero() && now.Sub(req.lastPoll) < req.interval-req.interval/5 {
		req.lastPoll = now
		req.interval += d.interval
		secs := max(1, int(req.interval.Seconds()))
		d.mu.Unlock()
		writeJSON(w, 400, map[string]any{
			"error":    apiError{Code: "slow_down", Message: "polling too fast"},
			"interval": secs,
		})
		return
	}
	req.lastPoll = now
	switch req.state {
	case "denied":
		d.drop(req)
		d.mu.Unlock()
		writeErr(w, 400, "access_denied", "the sign in was denied in the browser")
		return
	case "approved":
		d.drop(req)
		d.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"token":      req.token,
			"token_id":   req.tokenID,
			"user":       map[string]any{"email": req.user.Email, "name": req.user.Name, "role": req.user.Role},
			"scope":      req.scope,
			"expires_at": req.tokenExp,
		})
		return
	}
	d.mu.Unlock()
	writeErr(w, 400, "authorization_pending", "waiting for approval in the browser")
}

// findUserCode returns the live request for a typed code, counting misses
// against the lookup limit. limited means the lookup was refused unseen.
func (d *deviceStore) findUserCode(code string) (req *deviceRequest, limited bool) {
	now := d.now()
	if !d.lookup.ok(now) {
		return nil, true
	}
	d.sweep(now)
	req = d.byCode[normalUserCode(code)]
	if req == nil {
		d.lookup.take(now)
	}
	return req, false
}

func writeDeviceLookupErr(w http.ResponseWriter, limited bool) {
	if limited {
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "too_many_attempts", "Too many wrong codes. Wait a minute and try again.")
		return
	}
	writeErr(w, 404, "not_found", "No sign in is waiting for that code. It may have expired: run acn login again.")
}

func deviceView(req *deviceRequest) map[string]any {
	return map[string]any{
		"user_code":  req.userCode,
		"client":     req.client,
		"hostname":   req.hostname,
		"os":         req.os,
		"created_at": req.created.UTC(),
		"expires_at": req.expires.UTC(),
		"state":      req.state,
	}
}

// cliDeviceLookup shows the authorize page what is asking. Signed in only.
func (s *Server) cliDeviceLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	d := s.devices
	d.mu.Lock()
	req, limited := d.findUserCode(r.URL.Query().Get("user_code"))
	if req == nil {
		d.mu.Unlock()
		writeDeviceLookupErr(w, limited)
		return
	}
	view := deviceView(req)
	d.mu.Unlock()
	writeJSON(w, 200, map[string]any{"request": view})
}

// cliDeviceApprove mints the CLI's token for the signed in user.
func (s *Server) cliDeviceApprove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		UserCode string `json:"user_code"`
		Scope    string `json:"scope"`
		Label    string `json:"label"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := userFrom(r)
	role, ok := store.ScopeRole(in.Scope)
	if !ok {
		writeErr(w, 400, "invalid", "scope must be read, write or admin")
		return
	}
	if !u.Role.AtLeast(role) {
		writeErr(w, 403, "forbidden", "a token cannot have more access than you")
		return
	}
	d := s.devices
	d.mu.Lock()
	req, limited := d.findUserCode(in.UserCode)
	if req == nil {
		d.mu.Unlock()
		writeDeviceLookupErr(w, limited)
		return
	}
	if req.state != "pending" {
		d.mu.Unlock()
		writeErr(w, 409, "already_decided", "this sign in was already "+req.state)
		return
	}
	// Claim it before the store call, so a second approve cannot mint a
	// second token.
	req.state = "approving"
	host := req.hostname
	d.mu.Unlock()

	name := clip(in.Label, 100)
	if name == "" {
		name = clip("acn on "+host, 100)
	}
	tok := crypto.RandomToken(store.AdminTokenPrefix, 40)
	exp := time.Now().Add(deviceTokenTTL).UTC()
	created, err := s.Store.CreateToken(r.Context(), &store.AdminToken{
		Name: name, Scope: in.Scope, UserID: u.ID, TokenPrefix: tok[:12], ExpiresAt: &exp,
	}, crypto.SHA256(tok))

	d.mu.Lock()
	if err != nil {
		req.state = "pending"
		d.mu.Unlock()
		writeStoreErr(w, err)
		return
	}
	req.state = "approved"
	req.token, req.tokenID, req.scope, req.user, req.tokenExp = tok, created.ID, created.Scope, u, exp
	view := deviceView(req)
	d.mu.Unlock()

	s.Store.AddAudit(r.Context(), u, "token.create", created.Name, "scope="+created.Scope+" via cli sign in")
	writeJSON(w, 200, map[string]any{"request": view, "token": created})
}

// cliDeviceDeny refuses a sign in; the CLI's next poll gets access_denied.
func (s *Server) cliDeviceDeny(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		UserCode string `json:"user_code"`
	}
	if !decode(w, r, &in) {
		return
	}
	d := s.devices
	d.mu.Lock()
	req, limited := d.findUserCode(in.UserCode)
	if req == nil {
		d.mu.Unlock()
		writeDeviceLookupErr(w, limited)
		return
	}
	if req.state != "pending" {
		d.mu.Unlock()
		writeErr(w, 409, "already_decided", "this sign in was already "+req.state)
		return
	}
	req.state = "denied"
	view := deviceView(req)
	d.mu.Unlock()
	s.Store.AddAudit(r.Context(), userFrom(r), "cli.deny", req.hostname, "")
	writeJSON(w, 200, map[string]any{"request": view})
}
