package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// logs captures slog output for the test.
func logs(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestBootstrapToken(t *testing.T) {
	ctx := context.Background()
	tok := "acnadm_" + strings.Repeat("c1", 20)
	cfg := config.Config{BootstrapEmail: "ci@x.io", BootstrapPassword: "supersecret1", BootstrapToken: tok}

	// A fresh server: the first superadmin, and the token is theirs with admin scope.
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	buf := logs(t)
	if err := bootstrap(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	at, u, err := st.TokenByHash(ctx, crypto.SHA256(tok))
	if err != nil || u.Email != "ci@x.io" || u.Role != store.RoleSuperadmin || at.Scope != store.ScopeAdmin {
		t.Fatalf("bootstrap token: %v %v %v", at, u, err)
	}
	if strings.Contains(buf.String(), tok) || strings.Contains(buf.String(), tok[12:]) {
		t.Fatal("the bootstrap token was logged")
	}

	// A server with users ignores it, with a notice.
	other := "acnadm_" + strings.Repeat("d2", 20)
	cfg.BootstrapToken = other
	buf.Reset()
	if err := bootstrap(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.TokenByHash(ctx, crypto.SHA256(other)); err == nil {
		t.Fatal("a second token was stored on a server with users")
	}
	if !strings.Contains(buf.String(), "already has users") || strings.Contains(buf.String(), other) {
		t.Fatalf("notice: %s", buf.String())
	}

	// A weak or malformed token stops the start, without echoing it.
	for _, bad := range []string{"acnadm_short", "nope_" + strings.Repeat("x", 40), "acnadm_" + strings.Repeat("x", 30) + " yy"} {
		cfg.BootstrapToken = bad
		err := bootstrap(ctx, cfg, st)
		if err == nil || strings.Contains(err.Error(), bad) {
			t.Fatalf("%q: %v", bad, err)
		}
	}

	// Without email and password it does nothing but warn.
	st2, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	buf.Reset()
	if err := bootstrap(ctx, config.Config{BootstrapToken: tok}, st2); err != nil {
		t.Fatal(err)
	}
	if n, _ := st2.CountUsers(ctx); n != 0 || !strings.Contains(buf.String(), "is ignored") {
		t.Fatalf("token alone: %d users, %s", n, buf.String())
	}
}
