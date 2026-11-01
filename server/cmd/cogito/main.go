// Command cogito runs the Cogito API key pool server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/cogito-keys/cogito/internal/agent"
	"github.com/cogito-keys/cogito/internal/api"
	"github.com/cogito-keys/cogito/internal/catalog"
	"github.com/cogito-keys/cogito/internal/config"
	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/egress"
	"github.com/cogito-keys/cogito/internal/llmstxt"
	"github.com/cogito-keys/cogito/internal/notify"
	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/store"
	"github.com/cogito-keys/cogito/internal/webui"
	"github.com/cogito-keys/cogito/internal/worker"
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
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
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

func run() error {
	config.LoadDotEnv(".env")
	cfg := config.Load(version)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	// MkdirAll keeps the mode of an existing dir; the data dir holds the
	// database and possibly master.key, so keep it private.
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		slog.Warn("could not restrict data dir permissions", "path", cfg.DataDir, "err", err)
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
	if err := p.RestoreLeases(ctx); err != nil {
		return err
	}
	// Webhook URLs are chosen in the dashboard, so by default they may not
	// reach private networks (cloud metadata, internal admin panels).
	hookClient := catalog.SafeClient(15 * time.Second)
	if cfg.WebhookAllowPrivate {
		hookClient = &http.Client{Timeout: 15 * time.Second}
	}
	p.Notify = notify.New(st, box, hookClient)
	go p.Notify.Run(ctx)
	p.Egress = egress.New(st, box, egress.NewTransport(nil))
	p.Egress.EchoURL = cfg.EgressEchoURL
	srv := api.New(cfg, st, p, cat, ag, webui.FS(), llmstxt.Content)
	w := &worker.Worker{Store: st, Pool: p, HealthInterval: cfg.HealthInterval,
		Retention: time.Duration(cfg.EventRetentionDays) * 24 * time.Hour}
	go w.Run(ctx)

	hs := &http.Server{Addr: cfg.Addr, Handler: srv.Router(), ReadHeaderTimeout: 15 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	slog.Info("cogito listening", "addr", cfg.Addr, "data", cfg.DataDir, "version", version)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
				return fmt.Errorf("the master key cannot decrypt stored keys; set COGITO_MASTER_KEY to the key this database was created with: %w", err)
			}
		}
		return st.SetMeta(ctx, "master_key_check", want)
	}
	if got != want {
		return errors.New("the master key does not match this database; set COGITO_MASTER_KEY (or restore master.key) to the key it was created with")
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
