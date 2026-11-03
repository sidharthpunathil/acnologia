package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
)

// The access gate: one shared password in front of the whole site, for an
// instance reachable from the internet (docs/deploy-railway.md).
//
// It sits in front of the user accounts, not instead of them. A browser types
// the password once on the lock screen and gets the acnologia_gate cookie;
// only then can it reach setup, sign in and the dashboard. A script sends the
// password in X-Acnologia-Access on every request instead, or a valid admin
// API token (Authorization: Bearer acnadm_...): that is how the acn CLI gets
// through without ever knowing the password (clidevice.go mints its token).
//
// Locked: everything that carries data or lets someone act. That is /api/*
// (setup and sign in included) except the consumer API and the gate's own
// routes, the docs markdown and assets, /llms.txt and /llms-full.txt, and any
// request that is not a GET or HEAD. Open: the SPA shell and its static files
// (they carry no data, and the lock screen is part of them), /healthz,
// /robots.txt, /api/gate*, the CLI sign in's create and poll calls
// (/api/cli/device and /api/cli/device/token), the consumer routes (/proxy/*, /api/v1/*,
// /mcp), which already demand an app token of their own, the server to
// server sharing routes (/api/peer/v1/*, /.well-known/acnologia) and a
// person's claim link routes (/api/claim/v1/*), which need a claim code.
//
// The cookie is "<issued unix>.<mac>", the MAC keyed by the master key and
// the password. Nothing is stored, so a restart signs nobody out; changing
// the password (or the master key) signs every browser out at once. A leaked
// cookie cannot be brute forced back into the password without the master
// key, and the server refuses one older than the gate's ttl whatever the browser
// keeps.
const (
	gateCookie = "acnologia_gate"
	// gateTTL is the default unlock lifetime; ACNOLOGIA_ACCESS_TTL sets the
	// one in use (accessGate.ttl).
	gateTTL = config.DefaultAccessTTL
	// gateClockSkew tolerates an issue time slightly in the future.
	gateClockSkew = 5 * time.Minute
	// accessHeader carries the password for scripts.
	accessHeader = "X-Acnologia-Access"
)

// Wrong unlock attempts are limited globally, not per address: behind
// Railway's proxy the address is whatever X-Forwarded-For claims, so a per
// address limit is one header away from no limit. The cost is that a guesser
// can make the owner wait; scripts that send X-Acnologia-Access are never
// limited. 5 a minute still allows 7,200 a day, so a daily cap bounds it.
const (
	gateFailLimit   = 5
	gateFailWindow  = time.Minute
	gateDailyLimit  = 50
	gateDailyWindow = 24 * time.Hour
)

type accessGate struct {
	// key signs cookies; nil when no password is configured.
	key []byte
	// hash is sha256(password). Comparing digests keeps the comparison
	// constant time whatever the lengths.
	hash [sha256.Size]byte
	// ttl is how long an unlocked browser stays unlocked.
	ttl time.Duration

	mu          sync.Mutex
	failures    int
	since       time.Time
	dayFailures int
	daySince    time.Time
	now         func() time.Time
}

// newAccessGate returns the gate for password; an empty password turns it
// off. The MAC key mixes in the master key when there is one.
func newAccessGate(password string, box *crypto.Box) *accessGate {
	g := &accessGate{now: time.Now, ttl: gateTTL}
	if password == "" {
		return g
	}
	g.hash = sha256.Sum256([]byte(password))
	label := "acnologia/gate/v1\x00" + password
	if box != nil {
		k, _ := hex.DecodeString(box.Fingerprint(label))
		g.key = k
	} else {
		k := sha256.Sum256([]byte(label))
		g.key = k[:]
	}
	return g
}

// setAccessPassword turns the gate on for password (off when empty). Call it
// before Router. The password joins the redactor's known secrets, so it is
// scrubbed from logs, events and prompts if it ever turns up there.
func (s *Server) setAccessPassword(password string) {
	s.Cfg.AccessPassword = password
	s.gate = newAccessGate(password, s.Pool.Box)
	if s.Cfg.AccessTTL > 0 {
		s.gate.ttl = s.Cfg.AccessTTL
	}
	if password != "" {
		s.Store.Redactor().AlsoKnow(password)
	}
}

// on reports whether a password is configured.
func (g *accessGate) on() bool { return g != nil && g.key != nil }

func (g *accessGate) mac(issued string) string {
	m := hmac.New(sha256.New, g.key)
	m.Write([]byte("acnologia/gate-cookie/v1\x00"))
	m.Write([]byte(issued))
	return hex.EncodeToString(m.Sum(nil))
}

// issue is the cookie value for a browser unlocked at t.
func (g *accessGate) issue(t time.Time) string {
	issued := strconv.FormatInt(t.Unix(), 10)
	return issued + "." + g.mac(issued)
}

// valid reports whether v is a cookie this gate issued within its ttl. The
// MAC is compared, in constant time, before the time is trusted.
func (g *accessGate) valid(v string) bool {
	issued, mac, ok := strings.Cut(v, ".")
	if !ok || issued == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(mac), []byte(g.mac(issued))) != 1 {
		return false
	}
	secs, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return false
	}
	age := g.now().Sub(time.Unix(secs, 0))
	return age >= -gateClockSkew && age <= g.ttl
}

// matches reports whether secret is the password, in constant time.
func (g *accessGate) matches(secret string) bool {
	h := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(h[:], g.hash[:]) == 1
}

// authorised reports whether r may pass: no password is set, it carries a
// valid gate cookie, or it carries the password in X-Acnologia-Access.
func (g *accessGate) authorised(r *http.Request) bool {
	if !g.on() {
		return true
	}
	if c, err := r.Cookie(gateCookie); err == nil && g.valid(c.Value) {
		return true
	}
	if v := r.Header.Get(accessHeader); v != "" {
		return g.matches(v)
	}
	return false
}

// gatePasses is authorised plus a valid admin API token. Only the admin
// token prefix is looked up, so an app token (acn_) never opens a dashboard
// route, and a made up acnadm_ value costs one hash lookup and fails.
func (s *Server) gatePasses(r *http.Request) bool {
	return s.gate.authorised(r) || (s.gate.on() && s.validAdminToken(r))
}

// tryUnlock checks one lock screen guess against the limits. Checking and
// counting happen under one lock, so a burst of parallel guesses cannot all
// slip past the check before any is counted. limited means the guess was
// refused unseen, so a locked out guesser learns nothing even when it is
// right; wait is how long until another guess is taken.
func (g *accessGate) tryUnlock(secret string) (ok, limited bool, wait time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if now.Sub(g.since) >= gateFailWindow {
		g.failures, g.since = 0, now
	}
	if now.Sub(g.daySince) >= gateDailyWindow {
		g.dayFailures, g.daySince = 0, now
	}
	if g.dayFailures >= gateDailyLimit {
		return false, true, g.daySince.Add(gateDailyWindow).Sub(now)
	}
	if g.failures >= gateFailLimit {
		return false, true, g.since.Add(gateFailWindow).Sub(now)
	}
	if g.matches(secret) {
		return true, false, 0
	}
	g.failures++
	g.dayFailures++
	return false, false, 0
}

// gateOpenPrefixes pass the gate untouched. The consumer routes check their
// own app token.
// /api/peer/v1/ is the server to server side of sharing (peer_public.go);
// /api/claim/v1/ is a person's claim link page (people.go), which needs the
// claim code. The pages /claim/<code> and /me are SPA navigations: a locked
// GET of them already gets the shell, and the shell skips the lock screen
// there and calls only these open routes.
var gateOpenPrefixes = []string{"/proxy/", "/api/v1/", "/api/gate/", "/api/peer/v1/", "/api/claim/v1/"}

var gateOpenExact = map[string]bool{
	"/healthz": true, "/robots.txt": true, "/mcp": true, "/api/gate": true, "/api/v1": true,
	// The CLI sign in: a code is useless until a signed in person, past
	// the gate, approves it (clidevice.go).
	"/api/cli/device": true, "/api/cli/device/token": true,
	// A server's public identity, for pairing with another server.
	"/.well-known/acnologia": true,
}

// gateOpen reports whether a path is reachable while locked. Both the decoded
// path and the escaped one chi routes on must agree, and a dot segment never
// counts as open, so an encoded or relative path cannot borrow an open
// prefix to reach a locked route.
func gateOpen(r *http.Request) bool {
	p := r.URL.Path
	raw := r.URL.RawPath
	if raw == "" {
		raw = p
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	if gateOpenExact[p] && raw == p {
		return true
	}
	for _, pre := range gateOpenPrefixes {
		if strings.HasPrefix(p, pre) && strings.HasPrefix(raw, pre) {
			return true
		}
	}
	return false
}

// gateData reports whether a locked GET would read data rather than the SPA
// shell, and so must be refused instead of answered with the lock screen.
func gateData(p string) bool {
	switch {
	case strings.HasPrefix(p, "/api/"), p == "/api":
		return true
	case strings.HasPrefix(p, "/docs/") && (strings.HasSuffix(p, ".md") || strings.HasPrefix(p, "/docs/assets/")):
		return true
	case p == "/llms.txt" || p == "/llms-full.txt":
		return true
	}
	return false
}

// gateRequests applies the gate to every request. A locked data request gets
// 401 {"error":{"code":"locked"}}; a locked page navigation gets the SPA shell
// (or the static file it names), which shows the lock screen. Nothing else is
// routed, so a route added later is locked unless it is listed as open.
func (s *Server) gateRequests(next http.Handler) http.Handler {
	if !s.gate.on() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gateOpen(r) || s.gatePasses(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || gateData(r.URL.Path) {
			writeErr(w, http.StatusUnauthorized, "locked", "this Acnologia instance is private: unlock it with its access password")
			return
		}
		s.serveWeb(w, r)
	})
}

// gateState answers the lock screen: is there a lock, and is this browser
// past it, and how long an unlock lasts. It says nothing about the password.
func (s *Server) gateState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"required":       s.gate.on(),
		"locked":         !s.gatePasses(r),
		"password_only":  s.soloMode(),
		"unlock_ttl_sec": int(s.gate.ttl.Seconds()),
	})
}

// gateUnlock exchanges the password for the gate cookie.
func (s *Server) gateUnlock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !decode(w, r, &body) {
		return
	}
	if !s.gate.on() {
		writeJSON(w, http.StatusOK, map[string]any{"required": false, "locked": false})
		return
	}
	ok, limited, wait := s.gate.tryUnlock(body.Password)
	if limited {
		secs := max(1, int(wait.Round(time.Second).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		msg := "Too many wrong passwords. Wait a minute and try again."
		if wait > gateFailWindow {
			msg = "Too many wrong passwords today. Try again tomorrow, or restart the server."
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":           apiError{Code: "too_many_attempts", Message: msg},
			"retry_after_sec": secs,
		})
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "wrong_password", "That password is not correct.")
		return
	}
	now := s.gate.now()
	http.SetCookie(w, &http.Cookie{
		Name:  gateCookie,
		Value: s.gate.issue(now),
		Path:  "/",
		// No script needs it, so an injected one cannot read it.
		HttpOnly: true,
		// Lax, not Strict: Strict drops the cookie when the dashboard is
		// opened from a link in a chat or an email.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookie(r),
		Expires:  now.Add(s.gate.ttl),
		MaxAge:   int(s.gate.ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"required": true, "locked": false})
}

// gateLock drops the gate cookie from this browser. Other browsers stay
// unlocked until their cookie ages out or the password changes.
func (s *Server) gateLock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, &http.Cookie{
		Name: gateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secureCookie(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"required": s.gate.on(), "locked": s.gate.on()})
}
