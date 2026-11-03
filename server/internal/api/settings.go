package api

import (
	"cmp"
	"net/http"
	"os"
	"strconv"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/config"
)

// envSetting is one environment variable as the Settings page shows it.
// Value is the effective value the server runs with (the default when the
// variable is unset). Secrets never carry a value: only whether they are set.
type envSetting struct {
	Name   string `json:"name"`
	Set    bool   `json:"set"`
	Secret bool   `json:"secret,omitempty"`
	Value  string `json:"value,omitempty"`
}

// settingsSummary answers the Settings page with the configuration that only
// the environment can change, so it can be shown read only. It is a read of
// s.Cfg and never includes a secret's value.
func (s *Server) settingsSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c := s.Cfg
	isSet := func(name string) bool { return config.Getenv(name) != "" }
	plain := func(name, value string) envSetting { return envSetting{Name: name, Set: isSet(name), Value: value} }
	secret := func(name string, set bool) envSetting { return envSetting{Name: name, Set: set, Secret: true} }
	b := strconv.FormatBool
	itoa := strconv.Itoa

	env := []envSetting{
		plain("ACNOLOGIA_PUBLIC_URL", c.PublicURL),
		secret("ACNOLOGIA_ACCESS_PASSWORD", isSet("ACNOLOGIA_ACCESS_PASSWORD")),
		plain("ACNOLOGIA_REQUIRE_ACCESS_PASSWORD", b(c.RequireAccessPassword)),
		plain("ACNOLOGIA_ACCESS_SIGNS_IN", b(c.AccessSignsIn)),
		// In seconds; the page words it ("30 days").
		plain("ACNOLOGIA_ACCESS_TTL", itoa(int(s.gate.ttl.Seconds()))),
		secret("ACNOLOGIA_MASTER_KEY", isSet("ACNOLOGIA_MASTER_KEY")),
		plain("ACNOLOGIA_SECURE_COOKIES", b(c.SecureCookies)),
		plain("ACNOLOGIA_TRUST_PROXY_HEADERS", b(c.TrustProxyHeaders)),
		plain("ACNOLOGIA_WEBHOOK_ALLOW_PRIVATE", b(c.WebhookAllowPrivate)),
		plain("ACNOLOGIA_EGRESS_ECHO_URL", c.EgressEchoURL),
		plain("ACNOLOGIA_EVENT_RETENTION_DAYS", itoa(c.EventRetentionDays)),
		plain("ACNOLOGIA_HEALTH_INTERVAL", c.HealthInterval.String()),
		plain("ACNOLOGIA_MAX_BODY_MB", itoa(int(c.MaxBodyBytes>>20))),
		plain("ACNOLOGIA_CACHE_MB", itoa(int(c.CacheBytes>>20))),
		secret("ANTHROPIC_API_KEY", os.Getenv("ANTHROPIC_API_KEY") != ""),
		// The fallback model: the model saved on this page wins over it.
		plain("ACNOLOGIA_AI_MODEL", cmp.Or(c.AIModel, agent.BuiltinModel)),
		// Unset means 60% of the Assistant's model's context window; the
		// value is the threshold in effect either way.
		plain("ACNOLOGIA_CHAT_COMPACT_TOKENS", itoa(s.compactAt())),
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        c.Version,
		"access_gate_on": s.gate.on(),
		"env":            env,
	})
}
