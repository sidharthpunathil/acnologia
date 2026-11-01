package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/store"
)

const (
	sessionCookie = "cogito_session"
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

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeErr(w, 401, "unauthenticated", "sign in first")
			return
		}
		h := crypto.SHA256(c.Value)
		u, err := s.Store.SessionUser(r.Context(), h)
		if err != nil {
			writeErr(w, 401, "unauthenticated", "session expired")
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

// appToken reads the app token from the Authorization header, X-Cogito-Token, or ?cogito_token.
func appToken(r *http.Request) string {
	if v := r.Header.Get("X-Cogito-Token"); v != "" {
		return v
	}
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer cog_") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return r.URL.Query().Get("cogito_token")
}

func (s *Server) authApp(r *http.Request) (*store.App, int, string) {
	tok := appToken(r)
	if tok == "" {
		return nil, 401, "missing app token: send Authorization: Bearer cog_..."
	}
	a, err := s.Store.AppByTokenHash(r.Context(), crypto.SHA256(tok))
	if err != nil {
		return nil, 401, "invalid app token"
	}
	if !a.Active {
		return nil, 403, "app is disabled"
	}
	s.Store.TouchApp(r.Context(), a.ID)
	return a, 0, ""
}

func (s *Server) requireApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, code, msg := s.authApp(r)
		if a == nil {
			writeErr(w, code, "unauthenticated", msg)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), appKey, a)))
	})
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var user *store.User
	if c, err := r.Cookie(sessionCookie); err == nil {
		user, _ = s.Store.SessionUser(r.Context(), crypto.SHA256(c.Value))
	}
	writeJSON(w, 200, map[string]any{"setup_required": n == 0, "user": user})
}

type credentials struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func validPassword(p string) bool { return len(p) >= 10 && len(p) <= 128 }

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	c.Email = strings.TrimSpace(strings.ToLower(c.Email))
	if !strings.Contains(c.Email, "@") || !validPassword(c.Password) {
		writeErr(w, 400, "invalid", "a valid email and a password of at least 10 characters are required")
		return
	}
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if n > 0 {
		writeErr(w, 409, "already_setup", "setup is already done")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	u, err := s.Store.CreateUser(r.Context(), c.Email, c.Name, string(hash), store.RoleSuperadmin)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), u, "setup", u.Email, "created first superadmin")
	s.startSession(w, r, u)
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	ip := r.RemoteAddr
	if !s.limiter.allow(ip) {
		writeErr(w, 429, "too_many_attempts", "too many sign in attempts, wait a few minutes")
		return
	}
	u, err := s.Store.GetUserByEmail(r.Context(), strings.TrimSpace(strings.ToLower(c.Email)))
	if err != nil || u.Disabled || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)) != nil {
		s.limiter.fail(ip)
		writeErr(w, 401, "bad_credentials", "wrong email or password")
		return
	}
	s.limiter.reset(ip)
	s.Store.TouchLogin(r.Context(), u.ID)
	s.startSession(w, r, u)
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	tok := crypto.RandomToken("", 32)
	if err := s.Store.CreateSession(r.Context(), crypto.SHA256(tok), u.ID, sessionTTL); err != nil {
		writeStoreErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.Cfg.SecureCookies || r.TLS != nil, MaxAge: int(sessionTTL.Seconds()),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Store.DeleteSession(r.Context(), crypto.SHA256(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
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
			writeErr(w, 400, "weak_password", "password must be at least 10 characters")
			return
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		hs := string(h)
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

// loginLimiter is a tiny per-IP failure counter.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{hits: map[string][]time.Time{}} }

func (l *loginLimiter) recent(ip string) []time.Time {
	cut := time.Now().Add(-10 * time.Minute)
	out := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	l.hits[ip] = out
	return out
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) < 10
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[ip] = append(l.recent(ip), time.Now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, ip)
}
