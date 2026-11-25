package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestSettingsSummaryIsAdminOnlyAndHidesSecrets(t *testing.T) {
	t.Setenv("ACNOLOGIA_MASTER_KEY", "a-master-key-value-that-must-not-leak")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-not-leak")
	t.Setenv("ACNOLOGIA_EVENT_RETENTION_DAYS", "")
	t.Setenv("ACNOLOGIA_BOOTSTRAP_TOKEN", "acnadm_bootstrap-should-not-leak")
	t.Setenv("ACNOLOGIA_CLIENT_IP_HEADER", "Fly-Client-IP")
	e := newEnv(t)
	e.api.Cfg.EventRetentionDays = 30
	e.api.Cfg.TrustedProxyHops = 2
	e.api.Cfg.ClientIPHeader = "Fly-Client-IP"
	e.api.Cfg.MigrationBackup = true
	e.api.Cfg.DailySpendCeilingUSD = 25
	e.api.Cfg.ChatCompactTokens = 4321

	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	member := client()
	e.do(member, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	e.do(member, "GET", "/api/admin/settings", nil, http.StatusForbidden, nil)

	var out struct {
		Version      string       `json:"version"`
		AccessGateOn bool         `json:"access_gate_on"`
		Env          []envSetting `json:"env"`
	}
	resp := e.do(e.super, "GET", "/api/admin/settings", nil, 200, &out)
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control %q", resp.Header.Get("Cache-Control"))
	}
	if out.Version != "test" || out.AccessGateOn {
		t.Fatalf("version %q gate %v", out.Version, out.AccessGateOn)
	}
	byName := map[string]envSetting{}
	for _, s := range out.Env {
		byName[s.Name] = s
		if strings.Contains(s.Value, "leak") {
			t.Fatalf("%s leaked a secret: %q", s.Name, s.Value)
		}
	}
	for _, name := range []string{"ACNOLOGIA_MASTER_KEY", "ANTHROPIC_API_KEY", "ACNOLOGIA_BOOTSTRAP_TOKEN"} {
		if s := byName[name]; !s.Secret || !s.Set || s.Value != "" {
			t.Fatalf("%s: %+v", name, s)
		}
	}
	if s := byName["ACNOLOGIA_EVENT_RETENTION_DAYS"]; s.Set || s.Value != "30" {
		t.Fatalf("retention: %+v", s)
	}
	if s := byName["ACNOLOGIA_CHAT_COMPACT_TOKENS"]; s.Value != "4321" {
		t.Fatalf("compact: %+v", s)
	}
	for name, want := range map[string]string{
		"ACNOLOGIA_TRUSTED_PROXY_HOPS":      "2",
		"ACNOLOGIA_CLIENT_IP_HEADER":        "Fly-Client-IP",
		"ACNOLOGIA_MIGRATION_BACKUP":        "true",
		"ACNOLOGIA_DAILY_SPEND_CEILING_USD": "25",
	} {
		if s, ok := byName[name]; !ok || s.Value != want {
			t.Fatalf("%s: %+v, want %q", name, s, want)
		}
	}
	if s := byName["ACNOLOGIA_BOOTSTRAP_PASSWORD"]; !s.Secret || s.Value != "" {
		t.Fatalf("bootstrap password: %+v", s)
	}
}
