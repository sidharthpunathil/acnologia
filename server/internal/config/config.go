// Package config loads runtime settings from environment variables.
package config

import (
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
}

func Load(version string) Config {
	return Config{
		Addr:                env("COGITO_ADDR", ":8080"),
		DataDir:             env("COGITO_DATA_DIR", "./data"),
		MasterKey:           os.Getenv("COGITO_MASTER_KEY"),
		PublicURL:           strings.TrimRight(os.Getenv("COGITO_PUBLIC_URL"), "/"),
		SecureCookies:       envBool("COGITO_SECURE_COOKIES", false),
		TrustProxyHeaders:   envBool("COGITO_TRUST_PROXY_HEADERS", false),
		EventRetentionDays:  envInt("COGITO_EVENT_RETENTION_DAYS", 30),
		HealthInterval:      envDuration("COGITO_HEALTH_INTERVAL", 6*time.Hour),
		MaxBodyBytes:        int64(envInt("COGITO_MAX_BODY_MB", 50)) << 20,
		CacheBytes:          int64(envInt("COGITO_CACHE_MB", 64)) << 20,
		WebhookAllowPrivate: envBool("COGITO_WEBHOOK_ALLOW_PRIVATE", false),
		EgressEchoURL:       env("COGITO_EGRESS_ECHO_URL", "https://www.cloudflare.com/cdn-cgi/trace"),
		BootstrapEmail:      os.Getenv("COGITO_BOOTSTRAP_EMAIL"),
		BootstrapPassword:   os.Getenv("COGITO_BOOTSTRAP_PASSWORD"),
		Version:             version,
		AnthropicAPIKey:     os.Getenv("ANTHROPIC_API_KEY"),
		AIModel:             env("COGITO_AI_MODEL", "claude-opus-5-5"),
		GitHubToken:         os.Getenv("GITHUB_TOKEN"),
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
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
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
