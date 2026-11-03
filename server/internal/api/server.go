// Package api exposes the dashboard API, the consumer API, the proxy and MCP.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/breaker"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/llmstxt"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/refresh"
	"github.com/sidharthpunathil/acnologia/server/internal/respcache"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
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
	// admitted counts each app's admitted requests not yet in its usage
	// (proxy_extras.go), keyed by app id.
	admitted sync.Map
	// devices holds pending CLI sign ins (clidevice.go).
	devices *deviceStore
	// gate is the site-wide access password lock (gate.go); off when
	// ACNOLOGIA_ACCESS_PASSWORD is unset.
	gate *accessGate
	// userMu serializes check-then-write user changes (first setup, keeping
	// one superadmin) so concurrent requests cannot both pass the check.
	userMu sync.Mutex
	// routerOnce builds the router used by the assistant's in-process calls.
	routerOnce sync.Once
	routerH    http.Handler
	// chatModel replaces the Claude call in tests.
	chatModel func(ctx context.Context, system string, msgs []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error)
	// chatSummary replaces the compaction summary call in tests.
	chatSummary func(ctx context.Context, system, transcript string) (*anthropic.BetaMessage, error)
	// chatWaitFor overrides how long a chat request waits for its turn (tests).
	chatWaitFor time.Duration
	// buildPoll overrides how often wait_for_build polls a run (tests).
	buildPoll time.Duration
	// peers is the receiver side of sharing between servers (peer.Syncer).
	peers *peer.Syncer
	// peerLimit bounds invite preview and redeem calls (peer_public.go).
	peerLimit *secretLimit
	// revokeLimit bounds revoke pushes from other servers (handover.go).
	revokeLimit *secretLimit
	// claimLimit bounds claim link preview and redeem calls (people.go).
	claimLimit *secretLimit
	// Refresh keeps providers' offers current (refresh.go).
	Refresh *refresh.Refresher
	// ctlAppMu makes the control MCP create its app once (mcp_control.go).
	ctlAppMu sync.Mutex
}

func New(cfg config.Config, st *store.Store, p *pool.Pool, cat *catalog.Catalog, ag *agent.Agent, web fs.FS, llms []byte) *Server {
	s := &Server{
		Cfg: cfg, Store: st, Pool: p, Catalog: cat, Agent: ag, WebFS: web, LLMsTxt: llms,
		UpClient: newUpstreamClient(),
		Breakers: breaker.New(),
		Cache:    respcache.New(cfg.CacheBytes),
		limiter:  newLoginLimiter(),
		devices:  newDeviceStore(),
	}
	s.peerLimit = newSecretLimit()
	s.revokeLimit = newSecretLimit()
	s.claimLimit = newSecretLimit()
	s.peers = &peer.Syncer{Store: st, Box: p.Box, Catalog: cat, FetchSiteLogo: peer.SiteLogo}
	s.peers.OnHandover = s.onHandover
	s.Refresh = &refresh.Refresher{Store: st, Catalog: cat, AI: serverAI{s}}
	s.Breakers.OnChange = func(id int64, down bool, reason string) { go s.onBreakerChange(id, down, reason) }
	// main wires these up with their workers; tests and embedders get working defaults.
	if p.Egress == nil {
		p.Egress = egress.New(st, p.Box, egress.NewTransport(nil))
	}
	if p.Notify == nil {
		p.Notify = notify.New(st, p.Box, catalog.SafeClient(15*time.Second))
	}
	s.Refresh.Notify = p.Notify
	if st.Redactor() == nil {
		st.SetRedactor(NewRedactor(st, p.Box))
	}
	s.setAccessPassword(cfg.AccessPassword)
	return s
}

// NewRedactor returns a Redactor that knows every secret the store holds:
// pool keys, webhook URLs and secrets, egress proxy URLs. It decrypts them
// once and again only after the store reports a change.
func NewRedactor(st *store.Store, box *crypto.Box) *redact.Redactor {
	return redact.New(func(ctx context.Context) ([]string, error) {
		blobs, err := st.SecretBlobs(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(blobs))
		for _, b := range blobs {
			if sec, err := box.Decrypt(b); err == nil {
				out = append(out, sec)
			}
		}
		return out, nil
	})
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	// Client IP headers are attacker controlled unless a proxy sets them, and
	// the login limiter keys on the client IP.
	if s.Cfg.TrustProxyHeaders {
		r.Use(middleware.RealIP)
	}
	// privateSite runs before the gate so a locked answer is no-index too.
	r.Use(middleware.Recoverer, requestLog, s.privateSite, s.gateRequests)

	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": s.Cfg.Version})
	})
	r.Get("/llms.txt", s.serveLLMs)
	// Sharing between servers (peer_public.go): who this server is, and the
	// server to server routes. Open in the access gate; preview and redeem
	// need an invite, manifest and leave a share token.
	r.Get("/.well-known/acnologia", s.wellKnown)
	r.Route("/api/peer/v1", func(r chi.Router) {
		r.Post("/preview", s.peerPreview)
		r.Post("/redeem", s.peerRedeem)
		r.With(s.requireApp).Get("/manifest", s.peerManifest)
		r.With(s.requireApp).Post("/leave", s.peerLeave)
		r.With(s.requireApp).Get("/providers/{slug}/logo", s.peerProviderLogo)
		// Key handover: sealed keys and the receiver's report need the
		// share token; a revoke push is signed by the owner.
		r.With(s.requireApp).Get("/keys", s.peerKeys)
		r.With(s.requireApp).Post("/report", s.peerReport)
		r.Post("/revoke", s.peerRevoke)
	})
	// Sharing with people (people.go): a claim link's page calls these.
	// Open in the access gate; they need the claim code.
	r.Route("/api/claim/v1", func(r chi.Router) {
		r.Post("/preview", s.claimPreview)
		r.Post("/redeem", s.claimRedeem)
	})
	r.Get("/llms-full.txt", s.serveLLMsFull)
	s.mountDocs(r)

	// The access gate's own routes: the lock screen must reach them before
	// it holds the cookie (gateRequests lets them through).
	r.Route("/api/gate", func(r chi.Router) {
		r.Use(jsonOnly)
		r.Get("/", s.gateState)
		r.Post("/unlock", s.gateUnlock)
		r.Post("/lock", s.gateLock)
	})

	// CLI sign in (clidevice.go). Create and poll are open in the gate and
	// need no account; looking up, approving and denying a code need a
	// dashboard session, not an API token.
	r.Route("/api/cli/device", func(r chi.Router) {
		r.Use(jsonOnly)
		r.Post("/", s.cliDeviceCreate)
		r.Post("/token", s.cliDeviceToken)
		r.Group(func(r chi.Router) {
			r.Use(s.requireUser, s.sessionOnly)
			r.Get("/lookup", s.cliDeviceLookup)
			r.Post("/approve", s.cliDeviceApprove)
			r.Post("/deny", s.cliDeviceDeny)
		})
	})

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
		// A token may revoke itself (acn logout), and nothing else.
		r.With(s.sessionOrSelfToken).Delete("/tokens/{id}", s.deleteToken)
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
		r.Get("/apps/{id}/tokens", s.listAppTokens)
		r.Get("/providers/{slug}", s.getProvider)
		r.Get("/providers/{slug}/guide", s.providerGuide)
		r.Get("/services/{id}/guide", s.adminServiceGuide)
		r.Post("/providers/validate", s.validateProvider)
		r.Get("/ai/status", s.aiStatus)
		r.Get("/ai/rules", s.aiGetRules)
		r.Get("/ai/runs", s.aiListRuns)
		r.Get("/ai/runs/{id}", s.aiGetRun)
		// The control MCP's endpoint and tools, for the Connect Claude card.
		r.Get("/mcp/control", s.mcpControlInfo)
		// Assistant chat: every user has their own threads. Tools inside run
		// with the user's own role.
		r.Get("/chat/threads", s.chatList)
		r.Post("/chat/threads", s.chatCreate)
		r.Get("/chat/threads/{id}", s.chatGet)
		r.Delete("/chat/threads/{id}", s.chatDelete)
		r.Post("/chat/threads/{id}/messages", s.chatSend)
		r.Post("/chat/threads/{id}/confirm", s.chatConfirm)
		r.Post("/chat/threads/{id}/answer", s.chatAnswer)
		r.Post("/chat/threads/{id}/compact", s.chatCompactNow)

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
			// Apps backing a share are managed on the Sharing page.
			r.With(s.notShareApp).Patch("/apps/{id}", s.updateApp)
			r.With(s.notShareApp).Delete("/apps/{id}", s.deleteApp)
			r.With(s.notShareApp).Post("/apps/{id}/rotate-token", s.rotateAppToken)
			r.With(s.notShareApp).Post("/apps/{id}/tokens", s.createAppToken)
			r.With(s.notShareApp).Patch("/apps/{id}/tokens/{tokenId}", s.renameAppToken)
			r.With(s.notShareApp).Delete("/apps/{id}/tokens/{tokenId}", s.revokeAppToken)
			r.With(s.notShareApp).Put("/apps/{id}/mcp", s.putAppMCP)
			// Settings: the read only configuration the environment sets.
			r.Get("/settings", s.settingsSummary)
			r.Get("/ai/models", s.aiModels)
			// Provider offers refresh (refresh.go): admins read.
			r.Get("/providers/refresh/settings", s.getRefreshSettings)
			r.Get("/providers/refresh/runs", s.listRefreshRuns)
			r.Get("/providers/refresh/runs/{id}", s.getRefreshRun)
			r.Get("/providers/refresh/reviews", s.listRefreshReviews)
			// Sharing: admins may look.
			r.Get("/identity", s.getIdentity)
			r.Get("/shares", s.listShares)
			r.Get("/shares/{id}", s.getShare)
			r.Get("/peers", s.listPeers)
			r.Get("/peers/{id}/providers", s.listPeerProviders)
			r.Put("/services/{id}/logo", s.putServiceLogo)
			r.Delete("/services/{id}/logo", s.deleteServiceLogo)
			r.Post("/providers", s.createProvider)
			r.Put("/providers/{slug}", s.updateProvider)
			r.Delete("/providers/{slug}", s.deleteProvider)
			r.Post("/providers/{slug}/rollback", s.rollbackProvider)
			r.Post("/providers/{slug}/guide/generate", s.generateProviderGuide)
			r.Post("/providers/{slug}/live-check", s.liveCheckProvider)
			r.Put("/providers/{slug}/logo", s.putProviderLogo)
			r.Delete("/providers/{slug}/logo", s.deleteProviderLogo)
			r.Post("/providers/{slug}/logo/fetch", s.fetchProviderLogo)
			r.Post("/ai/assist", s.aiAssist)
			r.Post("/ai/runs", s.aiCreateRun)
			r.Post("/ai/runs/{id}/cancel", s.aiCancelRun)
			r.Post("/ai/runs/{id}/publish", s.aiPublishRun)
			r.Delete("/ai/runs", s.aiClearRuns)
			r.Delete("/ai/runs/{id}", s.aiDeleteRun)
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
			r.Put("/ai/models", s.aiSetModels)
			r.Put("/ai/prices/{model}", s.aiSetPrice)
			r.Delete("/ai/prices/{model}", s.aiResetPrice)
			// Provider offers refresh: only a superadmin changes anything.
			r.Put("/providers/refresh/settings", s.putRefreshSettings)
			r.Post("/providers/refresh/run", s.runRefresh)
			r.Post("/providers/refresh/reviews/{id}/apply", s.applyRefreshReview)
			r.Post("/providers/refresh/reviews/{id}/dismiss", s.dismissRefreshReview)
			r.Post("/providers/{slug}/refresh", s.checkProviderOffers)
			// Sharing: only a superadmin changes anything.
			r.Patch("/identity", s.patchIdentity)
			r.Post("/shares", s.createShare)
			r.Patch("/shares/{id}", s.updateShare)
			r.Post("/shares/{id}/pause", s.shareStatusHandler(store.SharePaused, notify.SharePaused, "paused"))
			r.Post("/shares/{id}/resume", s.shareStatusHandler(store.ShareActive, notify.ShareResumed, "resumed"))
			r.Post("/shares/{id}/revoke", s.shareStatusHandler(store.ShareRevoked, notify.ShareRevoked, "revoked"))
			r.Post("/shares/{id}/invite", s.newShareInvite)
			r.Post("/shares/{id}/claim-link", s.shareClaimLink)
			r.Post("/shares/{id}/extend", s.extendShare)
			r.Post("/peers/preview", s.previewPeer)
			r.Post("/peers", s.connectPeer)
			r.Post("/peers/{id}/sync", s.syncPeer)
			r.Post("/peers/{id}/providers/{slug}/pull", s.pullPeerProvider)
			r.Delete("/peers/{id}", s.disconnectPeer)
		})
	})

	r.Route("/api/logos", func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/providers/{slug}", s.providerLogo)
		r.Get("/services/{id}", s.serviceLogo)
	})

	r.Route("/api/v1", func(r chi.Router) {
		// me checks the token itself, so a share token whose share stopped
		// still learns why.
		r.Get("/me", s.v1Me)
		r.Group(func(r chi.Router) {
			r.Use(s.requireApp)
			r.Get("/services", s.v1Services)
			r.Get("/services/{slug}/guide", s.v1ServiceGuide)
			r.Post("/lease", s.v1Lease)
			r.Post("/leases/{id}/report", s.v1Report)
			r.Post("/leases/{id}/renew", s.v1Renew)
		})
	})

	r.With(s.requireApp).HandleFunc("/proxy/{service}/*", s.proxy)
	r.With(s.requireApp).HandleFunc("/proxy/{service}", s.proxy)
	r.HandleFunc("/mcp", s.mcp)
	// The control MCP (mcp_control.go): the dashboard's features for an
	// admin API token. Behind the access gate like /api/admin.
	r.HandleFunc("/mcp/control", s.mcpControl)

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
		// A claim page's path carries its code: log the route, not the code.
		path := r.URL.Path
		if strings.HasPrefix(path, "/claim/") {
			path = "/claim/[code]"
		}
		slog.Info("http", "method", r.Method, "path", path, "status", ww.Status(), "ms", time.Since(start).Milliseconds())
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
	body = strings.ReplaceAll(body, "{{BASE_URL}}", s.baseURL(r))
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

// bodyReadTimeout bounds how long decode waits for a JSON body, so a client
// that sends its body a byte at a time cannot hold a connection open. The
// server has no ReadTimeout because that would cancel long streaming
// responses (chat, MCP, proxied streams) when it passes.
var bodyReadTimeout = 30 * time.Second

// decode reads one JSON value from the body under a read deadline. It reads
// whatever trails the value under the same deadline, so the server's own
// discard of an unread body cannot wait on a slow client either, then lifts
// the deadline so the response may take as long as it needs. On an error
// the deadline stays, and the connection closes once it passes.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout)) // not supported in-process: no socket to bound
	body := http.MaxBytesReader(w, r.Body, 4<<20)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		writeErr(w, 400, "bad_json", err.Error())
		return false
	}
	if _, err := io.Copy(io.Discard, body); err != nil {
		writeErr(w, 400, "bad_json", err.Error())
		return false
	}
	_ = rc.SetReadDeadline(time.Time{})
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
