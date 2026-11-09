// Command acnologia runs the Acnologia API key pool server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/api"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/ctl"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/llmstxt"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/peer"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
	"github.com/sidharthpunathil/acnologia/server/internal/webui"
	"github.com/sidharthpunathil/acnologia/server/internal/worker"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "gen-master-key" {
		fmt.Println(genKey())
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(runMCPBridge(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "ctl" {
		os.Exit(ctl.Run(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	// Every log line passes the redactor: patterns from the start, the pool's
	// known secrets once the store is open.
	logHandler = redact.NewHandler(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(slog.New(logHandler))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func genKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// logHandler is the redacting default log handler.
var logHandler *redact.Handler

func run() error {
	config.LoadDotEnv(".env")
	cfg := config.Load(version)
	config.LogDeprecatedEnv()
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	// MkdirAll keeps the mode of an existing dir; the data dir holds the
	// database and possibly master.key, so keep it private.
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		slog.Warn("could not restrict data dir permissions", "path", cfg.DataDir, "err", err)
	}
	// Required but not given: use (or generate) the password stored on the
	// data volume, so the site comes up locked rather than not at all.
	if cfg.RequireAccessPassword && cfg.AccessPassword == "" {
		p, created, err := config.LoadOrCreateAccessPassword(cfg.DataDir)
		if err != nil {
			return err
		}
		cfg.AccessPassword = p
		path := filepath.Join(cfg.DataDir, config.AccessPasswordFile)
		if created {
			slog.Warn("ACNOLOGIA_ACCESS_PASSWORD is not set, so a random access password was generated; read it from the data volume or set the variable to replace it", "file", path)
		} else {
			slog.Info("access password read from the data volume", "file", path)
		}
	}
	if err := cfg.CheckAccess(); err != nil {
		return err
	}
	if cfg.AccessPassword != "" {
		slog.Info("access gate on: the dashboard needs the access password")
	}
	key, err := crypto.LoadOrCreateKey(cfg.MasterKey, cfg.DataDir)
	if err != nil {
		return err
	}
	box, err := crypto.New(key)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := checkMasterKey(st, box); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap(ctx, cfg, st); err != nil {
		return err
	}
	// Make the server identity now, before any request can ask for it, so it
	// is never created lazily under concurrent first requests.
	if _, err := peer.Keys(ctx, st, box); err != nil {
		return err
	}
	if _, err := peer.BoxKeys(ctx, st, box); err != nil {
		return err
	}

	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(ctx); err != nil {
		return fmt.Errorf("load providers: %w", err)
	}
	ag := agent.New(agent.Config{APIKey: cfg.AnthropicAPIKey, Model: cfg.AIModel, GitHubToken: cfg.GitHubToken}, st, cat, box)
	if err := ag.Boot(ctx); err != nil {
		return err
	}
	if s := ag.Status(); s.Enabled {
		slog.Info("AI provider builder enabled", "model", s.Model)
	} else {
		slog.Info("AI provider builder disabled", "reason", s.Reason)
	}
	p := pool.New(st, box)
	p.KeyScope = pool.StoreKeyScope(st)
	if err := p.RestoreLeases(ctx); err != nil {
		return err
	}
	// Listen before any background loop starts, so a busy port fails fast
	// with nothing to wind down.
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	// Webhook URLs are chosen in the dashboard, so by default they may not
	// reach private networks (cloud metadata, internal admin panels).
	hookClient := catalog.SafeClient(15 * time.Second)
	if cfg.WebhookAllowPrivate {
		hookClient = &http.Client{Timeout: 15 * time.Second}
	}
	p.Notify = notify.New(st, box, hookClient)
	// Background loops write to the store, so shutdown waits for them before
	// the deferred st.Close.
	var bg sync.WaitGroup
	bg.Go(func() { p.Notify.Run(ctx) })
	p.Egress = egress.New(st, box, egress.NewTransport(nil))
	p.Egress.EchoURL = cfg.EgressEchoURL
	srv := api.New(cfg, st, p, cat, ag, webui.FS(), llmstxt.Content)
	if err := st.Redactor().Refresh(ctx); err != nil {
		slog.Warn("could not load secrets for redaction; patterns still apply", "err", err)
	}
	if logHandler != nil {
		logHandler.Use(st.Redactor())
	}
	w := &worker.Worker{Store: st, Pool: p, HealthInterval: cfg.HealthInterval,
		Retention: time.Duration(cfg.EventRetentionDays) * 24 * time.Hour,
		Peers:     srv.PeerSyncer(), PeerInterval: peer.SyncInterval, Refresh: srv.ProviderRefresher(),
		Sharing: srv.SharingTicker()}
	bg.Go(func() { w.Run(ctx) })
	if cfg.ForwardProxyAddr != "" {
		fln, err := net.Listen("tcp", cfg.ForwardProxyAddr)
		if err != nil {
			return err
		}
		slog.Info("forward proxy listening", "addr", cfg.ForwardProxyAddr)
		go func() {
			if err := srv.ServeForwardProxy(ctx, fln); err != nil {
				slog.Error("forward proxy stopped", "err", err)
			}
		}()
	}
	slog.Info("acnologia listening", "addr", cfg.Addr, "data", cfg.DataDir, "version", version)
	return serve(ctx, stop, newHTTPServer(cfg.Addr, srv.PublicHandler()), ln, shutdownGrace, func() {
		bg.Wait()
		srv.ProviderRefresher().Wait()
	})
}

// shutdownGrace bounds how long a shutdown waits for in-flight requests and
// background work. Docker and systemd allow 10 seconds by default before
// they kill the process; raise their grace period to use all of it.
const shutdownGrace = 15 * time.Second

// serve runs hs on ln until ctx ends, then drains it. It returns only once
// the in-flight requests have finished (or grace ran out and the rest were
// closed) and background has returned within what is left of grace, so the
// caller can close the store without cutting a request's writes. stop is the
// signal context's stop: calling it right away means a second signal ends
// the process at once, and calling it when Serve fails starts the same
// shutdown.
func serve(ctx context.Context, stop context.CancelFunc, hs *http.Server, ln net.Listener, grace time.Duration, background func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		stop()
		slog.Info("shutting down: finishing requests in flight", "grace", grace)
		sctx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := hs.Shutdown(sctx); err != nil {
			slog.Warn("requests still open when the shutdown grace ran out; closing them", "err", err)
			_ = hs.Close()
		}
		if background == nil {
			return
		}
		bgDone := make(chan struct{})
		go func() {
			defer close(bgDone)
			background()
		}()
		select {
		case <-bgDone:
		case <-sctx.Done():
			slog.Warn("background work still running when the shutdown grace ran out")
		}
	}()
	err := hs.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	stop()
	<-done
	return err
}

// newHTTPServer is the listening server. Headers must arrive within 15
// seconds and an idle keep-alive connection closes after 2 minutes, so slow
// or idle clients cannot pile up connections. There is deliberately no
// ReadTimeout or WriteTimeout: either would cut long streaming responses
// (chat, MCP, proxied streams), since Go cancels a request's context when a
// ReadTimeout passes. JSON bodies get their own read deadline instead (the
// api package's decode).
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// checkMasterKey refuses to start with a master key that differs from the
// one the database was written with. Starting anyway would make every stored
// secret unreadable and mix two keys in one table.
func checkMasterKey(st *store.Store, box *crypto.Box) error {
	ctx := context.Background()
	want := box.CheckValue()
	got := st.GetMeta(ctx, "master_key_check")
	if got == "" {
		n, err := st.CountKeys(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			// A database from before the check existed: verify one secret decrypts.
			if err := st.VerifyAnySecret(ctx, box.Decrypt); err != nil {
				return fmt.Errorf("the master key cannot decrypt stored keys; set ACNOLOGIA_MASTER_KEY to the key this database was created with: %w", err)
			}
		}
		return st.SetMeta(ctx, "master_key_check", want)
	}
	if got != want {
		return errors.New("the master key does not match this database; set ACNOLOGIA_MASTER_KEY (or restore master.key) to the key it was created with")
	}
	return nil
}

// bootstrap creates the first superadmin from env vars for headless installs.
func bootstrap(ctx context.Context, cfg config.Config, st *store.Store) error {
	if cfg.BootstrapEmail == "" || cfg.BootstrapPassword == "" {
		return nil
	}
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(cfg.BootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u, err := st.CreateUser(ctx, strings.ToLower(cfg.BootstrapEmail), "Admin", string(h), store.RoleSuperadmin)
	if err != nil {
		return err
	}
	st.AddAudit(ctx, u, "bootstrap", u.Email, "created superadmin from environment")
	slog.Info("created bootstrap superadmin", "email", u.Email)
	return nil
}
