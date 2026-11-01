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

	"github.com/cogito-keys/cogito/internal/api"
	"github.com/cogito-keys/cogito/internal/config"
	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/llmstxt"
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
	cfg := config.Load(version)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap(ctx, cfg, st); err != nil {
		return err
	}

	p := pool.New(st, box)
	if err := p.RestoreLeases(ctx); err != nil {
		return err
	}
	srv := api.New(cfg, st, p, webui.FS(), llmstxt.Content)
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
