// Package config loads runtime settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr          string
	DataDir       string
	MasterKey     string
	PublicURL     string
	SecureCookies bool
	// TrustProxyHeaders takes the client IP from X-Forwarded-For and friends.
	// Enable it only behind a reverse proxy that overwrites those headers.
	TrustProxyHeaders  bool
	EventRetentionDays int
	HealthInterval     time.Duration
	MaxBodyBytes       int64
	// CacheBytes caps the in-memory response cache; 0 turns caching off.
	CacheBytes int64
	// EgressEchoURL answers with the caller's IP, used to test egress proxies.
	EgressEchoURL string
	// WebhookAllowPrivate lets webhooks reach private and loopback addresses.
	WebhookAllowPrivate bool
	BootstrapEmail      string
	BootstrapPassword   string
	Version             string
	AnthropicAPIKey     string
	AIModel             string
	GitHubToken         string
	// AccessPassword locks the whole site behind one shared password (the
	// access gate) when set. RequireAccessPassword makes a missing or short
	// one a startup error, so a public deploy can never come up unlocked.
	AccessPassword        string
	RequireAccessPassword bool
}

// MinAccessPasswordLen is the shortest ACNOLOGIA_ACCESS_PASSWORD accepted.
const MinAccessPasswordLen = 12

// CheckAccess reports a configuration the server must refuse to start with:
// an access password shorter than MinAccessPasswordLen, or none at all while
// ACNOLOGIA_REQUIRE_ACCESS_PASSWORD is true. It never includes the password.
func (c Config) CheckAccess() error {
	if c.AccessPassword != "" && len(c.AccessPassword) < MinAccessPasswordLen {
		return fmt.Errorf("ACNOLOGIA_ACCESS_PASSWORD is too short: use at least %d characters", MinAccessPasswordLen)
	}
	if c.RequireAccessPassword && c.AccessPassword == "" {
		return errors.New("ACNOLOGIA_REQUIRE_ACCESS_PASSWORD is true but ACNOLOGIA_ACCESS_PASSWORD is not set: set a password of at least 12 characters, or the site would be open to anyone")
	}
	return nil
}

// listenAddr is ACNOLOGIA_ADDR, else 0.0.0.0:$PORT when a host such as
// Railway injects PORT, else :8080.
func listenAddr() string {
	if v := Getenv("ACNOLOGIA_ADDR"); v != "" {
		return v
	}
	if p := strings.TrimSpace(os.Getenv("PORT")); p != "" {
		return "0.0.0.0:" + p
	}
	return ":8080"
}

func Load(version string) Config {
	return Config{
		Addr:                  listenAddr(),
		DataDir:               env("ACNOLOGIA_DATA_DIR", "./data"),
		MasterKey:             Getenv("ACNOLOGIA_MASTER_KEY"),
		PublicURL:             strings.TrimRight(Getenv("ACNOLOGIA_PUBLIC_URL"), "/"),
		SecureCookies:         envBool("ACNOLOGIA_SECURE_COOKIES", false),
		TrustProxyHeaders:     envBool("ACNOLOGIA_TRUST_PROXY_HEADERS", false),
		EventRetentionDays:    envInt("ACNOLOGIA_EVENT_RETENTION_DAYS", 30),
		HealthInterval:        envDuration("ACNOLOGIA_HEALTH_INTERVAL", 6*time.Hour),
		MaxBodyBytes:          int64(envInt("ACNOLOGIA_MAX_BODY_MB", 50)) << 20,
		CacheBytes:            int64(envInt("ACNOLOGIA_CACHE_MB", 64)) << 20,
		WebhookAllowPrivate:   envBool("ACNOLOGIA_WEBHOOK_ALLOW_PRIVATE", false),
		EgressEchoURL:         env("ACNOLOGIA_EGRESS_ECHO_URL", "https://www.cloudflare.com/cdn-cgi/trace"),
		BootstrapEmail:        Getenv("ACNOLOGIA_BOOTSTRAP_EMAIL"),
		BootstrapPassword:     Getenv("ACNOLOGIA_BOOTSTRAP_PASSWORD"),
		Version:               version,
		AnthropicAPIKey:       os.Getenv("ANTHROPIC_API_KEY"),
		AIModel:               env("ACNOLOGIA_AI_MODEL", "claude-opus-5-5"),
		GitHubToken:           os.Getenv("GITHUB_TOKEN"),
		AccessPassword:        Getenv("ACNOLOGIA_ACCESS_PASSWORD"),
		RequireAccessPassword: envBool("ACNOLOGIA_REQUIRE_ACCESS_PASSWORD", false),
	}
}

// LoadDotEnv reads KEY=VALUE lines from a .env file into the environment.
// Variables already set in the environment win. A missing file is fine.
func LoadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set && k != "" {
			os.Setenv(k, v)
		}
	}
}

func env(k, def string) string {
	if v := Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(Getenv(k)); err == nil {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(Getenv(k)); err == nil {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	v := Getenv(k)
	if v == "" {
		return def
	}
	if v == "0" || v == "off" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}
