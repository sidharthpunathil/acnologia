package config

import (
	"strings"
	"testing"
	"time"
)

func TestCheckAccess(t *testing.T) {
	cases := []struct {
		name     string
		password string
		require  bool
		wantErr  string
	}{
		{"off", "", false, ""},
		{"set", "fake-long-password", false, ""},
		{"required and set", "fake-long-password", true, ""},
		{"required and missing", "", true, "ACNOLOGIA_REQUIRE_ACCESS_PASSWORD"},
		{"too short", "short-fake", false, "too short"},
		{"required and too short", "short-fake", true, "too short"},
	}
	for _, c := range cases {
		err := Config{AccessPassword: c.password, RequireAccessPassword: c.require}.CheckAccess()
		if c.wantErr == "" && err != nil {
			t.Fatalf("%s: unexpected %v", c.name, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Fatalf("%s: want an error containing %q, got %v", c.name, c.wantErr, err)
		}
		if err != nil && c.password != "" && strings.Contains(err.Error(), c.password) {
			t.Fatalf("%s: the error repeats the password", c.name)
		}
	}
}

func TestLoadAccessVariables(t *testing.T) {
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "fake-long-password")
	t.Setenv("ACNOLOGIA_REQUIRE_ACCESS_PASSWORD", "true")
	cfg := Load("test")
	if cfg.AccessPassword != "fake-long-password" || !cfg.RequireAccessPassword {
		t.Fatalf("got %q %v", cfg.AccessPassword, cfg.RequireAccessPassword)
	}
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "")
	if err := Load("test").CheckAccess(); err == nil {
		t.Fatal("REQUIRE without a password must refuse to start")
	}
}

func TestListenAddrHonoursPort(t *testing.T) {
	t.Setenv("COGITO_ADDR", "")
	t.Setenv("ACNOLOGIA_ADDR", "")
	t.Setenv("PORT", "")
	if got := Load("test").Addr; got != ":8080" {
		t.Fatalf("default addr = %q", got)
	}
	t.Setenv("PORT", "4321")
	if got := Load("test").Addr; got != "0.0.0.0:4321" {
		t.Fatalf("PORT addr = %q", got)
	}
	t.Setenv("ACNOLOGIA_ADDR", "127.0.0.1:9999")
	if got := Load("test").Addr; got != "127.0.0.1:9999" {
		t.Fatalf("ACNOLOGIA_ADDR must win, got %q", got)
	}
}

func TestAccessTTL(t *testing.T) {
	day := 24 * time.Hour
	cases := map[string]time.Duration{
		"":        DefaultAccessTTL,
		"7":       7 * day,
		"7d":      7 * day,
		"0.5d":    12 * time.Hour,
		"12h":     12 * time.Hour,
		"0":       DefaultAccessTTL,
		"-3":      DefaultAccessTTL,
		"-1h":     DefaultAccessTTL,
		"forever": DefaultAccessTTL,
	}
	for v, want := range cases {
		t.Setenv("ACNOLOGIA_ACCESS_TTL", v)
		if got := Load("test").AccessTTL; got != want {
			t.Errorf("ACNOLOGIA_ACCESS_TTL=%q: got %v, want %v", v, got, want)
		}
	}
}
