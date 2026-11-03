package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	sessionCookie = "acnologia_session"
	sessionTTL    = 14 * 24 * time.Hour
)

type ctxKey int

const (
	userKey ctxKey = iota
	appKey
	sessionHashKey
)

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func appFrom(r *http.Request) *store.App {
	a, _ := r.Context().Value(appKey).(*store.App)
	return a
}

func isSuper(r *http.Request) bool {
	u := userFrom(r)
	return u != nil && u.Role == store.RoleSuperadmin
}

// assistantCtxKey marks a request the Assistant made in process for a tool
// call. Nothing outside this package can set it.
type assistantCtxKey struct{}

// fromAssistant reports whether the request is an Assistant tool call. Such
// a request acts as the signed in user but carries what the model chose, so
// it must never vouch for anything on the user's behalf.
func fromAssistant(r *http.Request) bool {
	v, _ := r.Context().Value(assistantCtxKey{}).(bool)
	return v
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && store.IsAdminToken(tok) {
			s.tokenUser(w, r, tok, next)
			return
		}
		sess := sessionFrom(r)
		var u *store.User
		var h string
		if sess != "" {
			h = crypto.SHA256(sess)
			u, _ = s.Store.SessionUser(r.Context(), h)
		}
		if u == nil {
			u, h = s.soloSignIn(w, r)
		}
		if u == nil {
			if sess == "" {
				writeErr(w, 401, "unauthenticated", "sign in first")
			} else {
				writeErr(w, 401, "unauthenticated", "session expired")
			}
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, sessionHashKey, h)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireRole(role store.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := userFrom(r); u == nil || !u.Role.AtLeast(role) {
				writeErr(w, 403, "forbidden", "your role cannot do this")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// appToken reads the app token from the Authorization header, X-Acnologia-Token
// or ?acn_token, and from the old X-Cogito-Token and ?cogito_token names.
func appToken(r *http.Request) string {
	if v := reqHeader(r, "Token"); v != "" {
		return v
	}
	if v := r.Header.Get("Authorization"); isBearerAppToken(v) {
		return strings.TrimPrefix(v, "Bearer ")
	}
	q := r.URL.Query()
	if v := q.Get(tokenParam); v != "" {
		return v
	}
	return q.Get(legacyTokenParam)
}

// isBearerAppToken reports whether an Authorization value carries an app
// token, with the new acn_ prefix or the old cog_ one.
func isBearerAppToken(v string) bool {
	tok, ok := strings.CutPrefix(v, "Bearer ")
	return ok && store.IsAppToken(tok)
}

// authFail says why an app token was refused. Share is set when the token
// belongs to a share that is no longer usable (revoked, paused, expired).
type authFail struct {
	Status        int
	Code, Message string
	Share         string
}

func (f *authFail) write(w http.ResponseWriter) {
	if f.Share != "" {
		w.Header().Set("X-Acnologia-Share", f.Share)
	}
	writeErr(w, f.Status, f.Code, f.Message)
}

func (s *Server) authApp(r *http.Request) (*store.App, int, string) {
	a, f := s.authAppFull(r)
	if f != nil {
		return nil, f.Status, f.Message
	}
	return a, 0, ""
}

func (s *Server) authAppFull(r *http.Request) (*store.App, *authFail) {
	unauth := func(msg string) *authFail { return &authFail{Status: 401, Code: "unauthenticated", Message: msg} }
	tok := appToken(r)
	if tok == "" {
		return nil, unauth("missing app token: send Authorization: Bearer acn_...")
	}
	a, t, err := s.Store.AppByToken(r.Context(), crypto.SHA256(tok))
	if err != nil {
		return nil, unauth("invalid app token")
	}
	// A share token says why it stopped working, so the other server can
	// mark its link; this runs before the token checks because revoking a
	// share also revokes its tokens.
	if a.ShareID != nil {
		if f := s.shareGate(r.Context(), *a.ShareID); f != nil {
			return nil, f
		}
	}
	if t.RevokedAt != nil {
		return nil, unauth("app token revoked")
	}
	if !t.Usable(time.Now()) {
		return nil, unauth("app token expired")
	}
	if !a.Active {
		return nil, &authFail{Status: 403, Code: "unauthenticated", Message: "app is disabled"}
	}
	s.Store.TouchAppToken(r.Context(), a.ID, t.ID)
	return a, nil
}

func (s *Server) requireApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, f := s.authAppFull(r)
		if f != nil {
			f.write(w)
			return
		}
		ctx := store.WithAppToken(context.WithValue(r.Context(), appKey, a), a.TokenID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var user *store.User
	if sess := sessionFrom(r); sess != "" {
		user, _ = s.Store.SessionUser(r.Context(), crypto.SHA256(sess))
	}
	if user == nil {
		if u, _ := s.soloSignIn(w, r); u != nil {
			user, n = u, 1
		}
	}
	writeJSON(w, 200, map[string]any{"setup_required": n == 0, "user": user, "password_only": s.soloMode()})
}

type credentials struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// bcrypt reads at most 72 bytes and the library rejects longer input, so cap
// passwords there instead of storing an unusable hash.
func validPassword(p string) bool { return len(p) >= 10 && len(p) <= 72 }

const passwordRule = "password must be 10 to 72 characters"

// hashPassword returns the bcrypt hash or writes a 400 and returns false.
func hashPassword(w http.ResponseWriter, p string) (string, bool) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 400, "weak_password", passwordRule)
		return "", false
	}
	return string(h), true
}

// dummyHash is compared against when the user does not exist, so a login
// takes the same time whether or not the email is registered.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("acnologia-timing-equalizer"), bcrypt.DefaultCost)

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	c.Email = strings.TrimSpace(strings.ToLower(c.Email))
	if !strings.Contains(c.Email, "@") || !validPassword(c.Password) {
		writeErr(w, 400, "invalid", "a valid email and a "+passwordRule+" are required")
		return
	}
	hash, ok := hashPassword(w, c.Password)
	if !ok {
		return
	}
	// Count and insert under one lock so concurrent setup calls cannot each
	// create a superadmin.
	s.userMu.Lock()
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.userMu.Unlock()
		writeStoreErr(w, err)
		return
	}
	if n > 0 {
		s.userMu.Unlock()
		writeErr(w, 409, "already_setup", "setup is already done")
		return
	}
	u, err := s.Store.CreateUser(r.Context(), c.Email, c.Name, hash, store.RoleSuperadmin)
	s.userMu.Unlock()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), u, "setup", u.Email, "created first superadmin")
	if !s.startSession(w, r, u) {
		return
	}
	writeJSON(w, 200, map[string]any{"user": u})
}

// clientIP is the connection's address without the port, so reconnecting
// does not reset the login limiter. With ACNOLOGIA_TRUST_PROXY_HEADERS the
// RealIP middleware has already replaced RemoteAddr.
var proxyHeaderWarn sync.Once

func clientIP(r *http.Request) string {
	if r.Header.Get("X-Forwarded-For") != "" {
		proxyHeaderWarn.Do(func() {
			slog.Warn("requests carry X-Forwarded-For but ACNOLOGIA_TRUST_PROXY_HEADERS is off; behind a reverse proxy every client shares one login rate limit, so set it to true there")
		})
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(c.Email))
	ip := clientKey(r)
	tooMany := func() {
		writeErr(w, 429, "too_many_attempts", "too many sign in attempts, wait a few minutes")
	}
	// Limit per address and per account: rotating addresses must not give
	// unlimited guesses against one account.
	if !s.limiter.allow(ip) || !s.limiter.allow("email:"+email) {
		tooMany()
		return
	}
	u, err := s.Store.GetUserByEmail(r.Context(), email)
	hash := dummyHash
	keys := []string{ip}
	if err == nil {
		hash = []byte(u.PasswordHash)
		// Only real accounts get a per-account counter, which keeps its
		// size bounded by the user count.
		keys = append(keys, "email:"+email)
	}
	// Count the attempt before the password check, in the same step as the
	// limit check, so a parallel burst cannot all pass the check while every
	// bcrypt compare is still running. A success clears it again below.
	if !s.limiter.reserve(keys...) {
		tooMany()
		return
	}
	pwOK := bcrypt.CompareHashAndPassword(hash, []byte(c.Password)) == nil
	if err != nil || u.Disabled || !pwOK {
		writeErr(w, 401, "bad_credentials", "That email and password do not match. Check both and try again.")
		return
	}
	s.limiter.reset(ip)
	s.limiter.reset("email:" + email)
	s.Store.TouchLogin(r.Context(), u.ID)
	if !s.startSession(w, r, u) {
		return
	}
	s.Store.AddAudit(r.Context(), u, "login", u.Email, clientIP(r))
	writeJSON(w, 200, map[string]any{"user": u})
}

// startSession issues a session cookie. On failure it writes the error
// response and returns false.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User) bool {
	tok := crypto.RandomToken("", 32)
	if err := s.Store.CreateSession(r.Context(), crypto.SHA256(tok), u.ID, sessionTTL); err != nil {
		writeStoreErr(w, err)
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.secureCookie(r), MaxAge: int(sessionTTL.Seconds()),
	})
	return true
}

func (s *Server) secureCookie(r *http.Request) bool { return s.Cfg.SecureCookies || r.TLS != nil }

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFrom(r); sess != "" {
		_ = s.Store.DeleteSession(r.Context(), crypto.SHA256(sess))
	}
	names := []string{sessionCookie, legacySessionCookie}
	if s.soloMode() {
		// The access password is the login, so signing out locks the browser.
		names = append(names, gateCookie)
	}
	for _, name := range names {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: s.secureCookie(r)})
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, userFrom(r))
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name            *string `json:"name"`
		CurrentPassword string  `json:"current_password"`
		Password        *string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	u := userFrom(r)
	p := store.UserPatch{Name: body.Name}
	if body.Password != nil {
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.CurrentPassword)) != nil {
			writeErr(w, 400, "bad_password", "current password is wrong")
			return
		}
		if !validPassword(*body.Password) {
			writeErr(w, 400, "weak_password", passwordRule)
			return
		}
		hs, ok := hashPassword(w, *body.Password)
		if !ok {
			return
		}
		p.PasswordHash = &hs
	}
	if err := s.Store.UpdateUser(r.Context(), u.ID, p); err != nil {
		writeStoreErr(w, err)
		return
	}
	if body.Password != nil {
		sh, _ := r.Context().Value(sessionHashKey).(string)
		_ = s.Store.DeleteUserSessions(r.Context(), u.ID, sh)
		s.Store.AddAudit(r.Context(), u, "password.change", u.Email, "")
	}
	nu, _ := s.Store.GetUser(r.Context(), u.ID)
	writeJSON(w, 200, nu)
}

// loginLimiter is a tiny attempt counter keyed by client address or account
// ("email:" plus the address). An attempt is counted when it starts
// (reserve) and cleared on success (reset).
//
// Account counters live apart from address ones and are never capped: they
// exist only for real accounts, so the user count bounds them, and a flood
// of addresses must never stop an account's lockout from counting. Address
// counters are capped; when the cap is full of live entries the address seen
// longest ago is forgotten, which only frees that address.
type loginLimiter struct {
	mu        sync.Mutex
	hits      map[string][]time.Time // client addresses
	accounts  map[string][]time.Time // "email:" keys
	lastSweep time.Time
	now       func() time.Time
}

const (
	loginWindow   = 10 * time.Minute
	loginMaxFails = 10
	// limiterCap bounds memory if attempts arrive from very many addresses.
	limiterCap = 100_000
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{hits: map[string][]time.Time{}, accounts: map[string][]time.Time{}, now: time.Now}
}

func (l *loginLimiter) table(key string) map[string][]time.Time {
	if strings.HasPrefix(key, "email:") {
		return l.accounts
	}
	return l.hits
}

// recent drops expired attempts for key. Keys with none left are removed so
// lookups for unknown keys never grow the map.
func (l *loginLimiter) recent(m map[string][]time.Time, key string, now time.Time) []time.Time {
	ts, ok := m[key]
	if !ok {
		return nil
	}
	cut := now.Add(-loginWindow)
	out := ts[:0]
	for _, t := range ts {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		delete(m, key)
		return nil
	}
	m[key] = out
	return out
}

// allow reports whether key may try again, without counting. It is a cheap
// early refusal; reserve is the check that holds.
func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(l.table(key), key, l.now())) < loginMaxFails
}

// reserve counts one attempt against every key if each is still under the
// limit, in one step, and reports whether it did. Nothing is counted when
// any key is limited.
func (l *loginLimiter) reserve(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) > time.Minute {
		l.sweep(now)
	}
	for _, k := range keys {
		if len(l.recent(l.table(k), k, now)) >= loginMaxFails {
			return false
		}
	}
	for _, k := range keys {
		m := l.table(k)
		if _, ok := m[k]; !ok && len(m) >= limiterCap && !strings.HasPrefix(k, "email:") {
			l.makeRoom(now)
		}
		m[k] = append(m[k], now)
	}
	return true
}

// sweep drops expired entries from both tables. Caller holds mu.
func (l *loginLimiter) sweep(now time.Time) {
	for _, m := range []map[string][]time.Time{l.hits, l.accounts} {
		for k := range m {
			l.recent(m, k, now)
		}
	}
	l.lastSweep = now
}

// makeRoom frees one address slot: expired entries go first, and when every
// entry is live, the address whose last attempt is oldest. Caller holds mu.
func (l *loginLimiter) makeRoom(now time.Time) {
	var oldest string
	var oldestAt time.Time
	for k := range l.hits {
		ts := l.recent(l.hits, k, now)
		if len(ts) == 0 {
			continue
		}
		if last := ts[len(ts)-1]; oldest == "" || last.Before(oldestAt) {
			oldest, oldestAt = k, last
		}
	}
	if len(l.hits) >= limiterCap && oldest != "" {
		delete(l.hits, oldest)
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.table(key), key)
}
