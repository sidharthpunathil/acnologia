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
	e := newEnv(t)
	e.api.Cfg.EventRetentionDays = 30
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
	for _, name := range []string{"ACNOLOGIA_MASTER_KEY", "ANTHROPIC_API_KEY"} {
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
}
