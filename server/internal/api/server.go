// Package api exposes the dashboard API, the consumer API, the proxy and MCP.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/cogito-keys/cogito/internal/agent"
	"github.com/cogito-keys/cogito/internal/breaker"
	"github.com/cogito-keys/cogito/internal/catalog"
	"github.com/cogito-keys/cogito/internal/config"
	"github.com/cogito-keys/cogito/internal/egress"
	"github.com/cogito-keys/cogito/internal/llmstxt"
	"github.com/cogito-keys/cogito/internal/notify"
	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/respcache"
	"github.com/cogito-keys/cogito/internal/store"
)

type Server struct {
	Cfg      config.Config
	Store    *store.Store
	Pool     *pool.Pool
	Catalog  *catalog.Catalog
	Agent    *agent.Agent
	UpClient *http.Client
	// Breakers pause traffic to services whose provider keeps failing.
	Breakers *breaker.Set
	// Cache holds cached GET responses and idempotent replays.
	Cache   *respcache.Cache
	WebFS   fs.FS
	LLMsTxt []byte
	limiter *loginLimiter
	// userMu serializes check-then-write user changes (first setup, keeping
	// one superadmin) so concurrent requests cannot both pass the check.
	userMu sync.Mutex
	// routerOnce builds the router used by the assistant's in-process calls.
	routerOnce sync.Once
	routerH    http.Handler
	// chatModel replaces the Claude call in tests.
	chatModel func(ctx context.Context, system string, msgs []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error)
}

func New(cfg config.Config, st *store.Store, p *pool.Pool, cat *catalog.Catalog, ag *agent.Agent, web fs.FS, llms []byte) *Server {
	s := &Server{
		Cfg: cfg, Store: st, Pool: p, Catalog: cat, Agent: ag, WebFS: web, LLMsTxt: llms,
		UpClient: newUpstreamClient(),
		Breakers: breaker.New(),
		Cache:    respcache.New(cfg.CacheBytes),
		limiter:  newLoginLimiter(),
	}
	s.Breakers.OnChange = func(id int64, down bool, reason string) { go s.onBreakerChange(id, down, reason) }
	// main wires these up with their workers; tests and embedders get working defaults.
	if p.Egress == nil {
		p.Egress = egress.New(st, p.Box, egress.NewTransport(nil))
	}
	if p.Notify == nil {
		p.Notify = notify.New(st, p.Box, catalog.SafeClient(15*time.Second))
	}
	return s
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	// Client IP headers are attacker controlled unless a proxy sets them, and
	// the login limiter keys on the client IP.
	if s.Cfg.TrustProxyHeaders {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.Recoverer, requestLog, s.privateSite)

	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": s.Cfg.Version})
	})
	r.Get("/llms.txt", s.serveLLMs)
	r.Get("/llms-full.txt", s.serveLLMsFull)
	s.mountDocs(r)

	r.Route("/api/auth", func(r chi.Router) {
		r.Use(jsonOnly)
		r.Get("/state", s.authState)
		r.Post("/setup", s.setup)
		r.Post("/login", s.login)
		r.Post("/logout", s.logout)
	})

	r.Route("/api/admin", func(r chi.Router) {
		r.Use(jsonOnly, s.requireUser)
		r.Get("/me", s.me)
		// Account and token management need a real sign-in, not an API token.
		r.With(s.sessionOnly).Patch("/me", s.updateMe)
		r.With(s.sessionOnly).Get("/tokens", s.listTokens)
		r.With(s.sessionOnly).Post("/tokens", s.createToken)
		r.With(s.sessionOnly).Delete("/tokens/{id}", s.deleteToken)
		r.Get("/webhooks", s.listWebhooks)
		r.Get("/egress", s.listEgress)
		r.Get("/groups", s.listGroups)
		r.Get("/apps/{id}/usage", s.appUsage)
		r.Get("/services/{id}/breaker", s.serviceBreaker)
		r.Get("/cache", s.cacheStats)
		r.Get("/overview", s.overview)
		r.Get("/usage", s.usageReport)
		r.Get("/providers", s.listProviders)
		r.Get("/services", s.listServices)
		r.Get("/services/{id}", s.getService)
		r.Get("/services/{id}/keys", s.listKeys)
		r.Get("/keys", s.listAllKeys)
		r.Get("/keys/{id}/insights", s.keyInsights)
		r.Get("/events", s.listEvents)
		r.Get("/apps", s.listApps)
		r.Get("/apps/{id}/mcp", s.getAppMCP)
		r.Get("/providers/{slug}", s.getProvider)
		r.Post("/providers/validate", s.validateProvider)
		r.Get("/ai/status", s.aiStatus)
		r.Get("/ai/rules", s.aiGetRules)
		r.Get("/ai/runs", s.aiListRuns)
		r.Get("/ai/runs/{id}", s.aiGetRun)
		// Assistant chat: every user has their own threads. Tools inside run
		// with the user's own role.
		r.Get("/chat/threads", s.chatList)
		r.Post("/chat/threads", s.chatCreate)
		r.Get("/chat/threads/{id}", s.chatGet)
		r.Delete("/chat/threads/{id}", s.chatDelete)
		r.Post("/chat/threads/{id}/messages", s.chatSend)
		r.Post("/chat/threads/{id}/confirm", s.chatConfirm)

		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(store.RoleAdmin))
			r.Post("/services", s.createService)
			r.Patch("/services/{id}", s.updateService)
			r.Delete("/services/{id}", s.deleteService)
			r.Post("/services/{id}/keys", s.createKeys)
			r.Patch("/keys/{id}", s.updateKey)
			r.Delete("/keys/{id}", s.deleteKey)
			r.Post("/keys/{id}/check", s.checkKey)
			r.Post("/keys/{id}/reset", s.resetKey)
			r.Post("/keys/check-all", s.checkAllKeys)
			r.Post("/services/{id}/check", s.checkServiceKeys)
			r.Post("/apps", s.createApp)
			r.Patch("/apps/{id}", s.updateApp)
			r.Delete("/apps/{id}", s.deleteApp)
			r.Post("/apps/{id}/rotate-token", s.rotateAppToken)
			r.Put("/apps/{id}/mcp", s.putAppMCP)
			r.Put("/services/{id}/logo", s.putServiceLogo)
			r.Delete("/services/{id}/logo", s.deleteServiceLogo)
			r.Post("/providers", s.createProvider)
			r.Put("/providers/{slug}", s.updateProvider)
			r.Delete("/providers/{slug}", s.deleteProvider)
			r.Post("/providers/{slug}/rollback", s.rollbackProvider)
			r.Post("/providers/{slug}/live-check", s.liveCheckProvider)
			r.Put("/providers/{slug}/logo", s.putProviderLogo)
			r.Delete("/providers/{slug}/logo", s.deleteProviderLogo)
			r.Post("/providers/{slug}/logo/fetch", s.fetchProviderLogo)
			r.Post("/ai/assist", s.aiAssist)
			r.Post("/ai/runs", s.aiCreateRun)
			r.Post("/ai/runs/{id}/cancel", s.aiCancelRun)
			r.Post("/ai/runs/{id}/publish", s.aiPublishRun)
			r.Post("/webhooks", s.createWebhook)
			r.Patch("/webhooks/{id}", s.updateWebhook)
			r.Delete("/webhooks/{id}", s.deleteWebhook)
			r.Post("/webhooks/{id}/test", s.testWebhook)
			r.Post("/egress", s.createEgress)
			r.Patch("/egress/{id}", s.updateEgress)
			r.Delete("/egress/{id}", s.deleteEgress)
			r.Post("/egress/{id}/test", s.testEgress)
			r.Post("/groups", s.createGroup)
			r.Patch("/groups/{id}", s.updateGroup)
			r.Delete("/groups/{id}", s.deleteGroup)
			r.Post("/services/{id}/breaker/reset", s.resetServiceBreaker)
			r.Post("/services/{id}/cache/purge", s.purgeServiceCache)
		})
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(store.RoleSuperadmin))
			r.Post("/keys/{id}/reveal", s.revealKey)
			r.Get("/users", s.listUsers)
			r.Post("/users", s.createUser)
			r.Patch("/users/{id}", s.updateUser)
			r.Delete("/users/{id}", s.deleteUser)
			r.Get("/audit", s.listAudit)
			r.Put("/ai/rules", s.aiSetRules)
		})
	})

	r.Route("/api/logos", func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/providers/{slug}", s.providerLogo)
		r.Get("/services/{id}", s.serviceLogo)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.requireApp)
		r.Get("/services", s.v1Services)
		r.Post("/lease", s.v1Lease)
		r.Post("/leases/{id}/report", s.v1Report)
		r.Post("/leases/{id}/renew", s.v1Renew)
	})

	r.With(s.requireApp).HandleFunc("/proxy/{service}/*", s.proxy)
	r.With(s.requireApp).HandleFunc("/proxy/{service}", s.proxy)
	r.HandleFunc("/mcp", s.mcp)

	r.NotFound(s.serveWeb)
	return r
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		// Never log query strings: they may carry provider keys or app tokens.
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "ms", time.Since(start).Milliseconds())
	})
}

// privateSite keeps every page out of search engines and AI crawlers, and
// sets baseline security headers. Acnologia is a private tool.
func (s *Server) privateSite(next http.Handler) http.Handler {
	csp := dashboardCSP(inlineScriptHashes(s.WebFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex, noai, noimageai")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if !strings.HasPrefix(r.URL.Path, "/proxy/") {
			// The dashboard loads only its own scripts and styles plus Google
			// Fonts. Proxied responses get their own sandbox policy in relay.
			h.Set("Content-Security-Policy", csp)
		}
		next.ServeHTTP(w, r)
	})
}

// dashboardCSP allows the bundle from 'self' plus the exact inline scripts
// shipped in index.html (the pre-paint theme script), by hash.
func dashboardCSP(hashes []string) string {
	script := "script-src 'self'"
	for _, h := range hashes {
		script += " 'sha256-" + h + "'"
	}
	return "default-src 'self'; " + script + "; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com data:; img-src 'self' data: blob:; connect-src 'self'; " +
		"object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
}

var inlineScriptRe = regexp.MustCompile(`(?is)<script(\s[^>]*)?>(.*?)</script>`)

// inlineScriptHashes returns base64 SHA-256 hashes of index.html's inline
// scripts, computed from the file actually served so they always match.
func inlineScriptHashes(web fs.FS) []string {
	if web == nil {
		return nil
	}
	b, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range inlineScriptRe.FindAllSubmatch(b, -1) {
		if bytes.Contains(bytes.ToLower(m[1]), []byte("src=")) || len(bytes.TrimSpace(m[2])) == 0 {
			continue
		}
		sum := sha256.Sum256(m[2])
		out = append(out, base64.StdEncoding.EncodeToString(sum[:]))
	}
	return out
}

func jsonOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// Mutating dashboard calls must carry a header a cross-site form or
		// simple request cannot set: X-Requested-With or a JSON content type.
		// Either forces a CORS preflight, which blocks CSRF with the session
		// cookie. This holds for bodyless calls too (rotate, reveal, publish).
		json := strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
		if r.ContentLength != 0 && !json {
			writeErr(w, http.StatusUnsupportedMediaType, "bad_content_type", "use application/json")
			return
		}
		if !json && r.Header.Get("X-Requested-With") == "" {
			writeErr(w, http.StatusBadRequest, "csrf", "missing X-Requested-With header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveLLMsFull(w http.ResponseWriter, r *http.Request) {
	s.writeLLMs(w, r, llmstxt.Full)
}

func (s *Server) serveLLMs(w http.ResponseWriter, r *http.Request) {
	s.writeLLMs(w, r, s.LLMsTxt)
}

func (s *Server) writeLLMs(w http.ResponseWriter, r *http.Request, text []byte) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	body := string(text)
	base := s.Cfg.PublicURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	body = strings.ReplaceAll(body, "{{BASE_URL}}", base)
	_, _ = w.Write([]byte(body))
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, 404, "not_found", "no such endpoint")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path != "" {
		if f, err := s.WebFS.Open(path); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.FileServer(http.FS(s.WebFS)).ServeHTTP(w, r)
				return
			}
		}
	}
	// SPA fallback
	b, err := fs.ReadFile(s.WebFS, "index.html")
	if err != nil {
		http.Error(w, "UI not built", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// helpers

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: msg}})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, "not_found", "not found")
	case errors.Is(err, store.ErrDuplicate):
		writeErr(w, 409, "duplicate", "that key already exists in this service")
	case err != nil && strings.Contains(err.Error(), "UNIQUE"):
		writeErr(w, 409, "duplicate", "already exists")
	default:
		slog.Error("internal error", "err", err)
		writeErr(w, 500, "internal", "internal error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		writeErr(w, 400, "bad_json", err.Error())
		return false
	}
	return true
}

func idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad_id", "id must be a number")
		return 0, false
	}
	return id, true
}
