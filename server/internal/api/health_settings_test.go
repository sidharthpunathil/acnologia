package api

import (
	"context"
	"testing"
	"time"
)

type healthSettingsResp struct {
	Interval    string   `json:"interval"`
	IntervalSec int      `json:"interval_sec"`
	Source      string   `json:"source"`
	Saved       string   `json:"saved"`
	Env         string   `json:"env"`
	Choices     []string `json:"choices"`
}

func TestHealthIntervalSetting(t *testing.T) {
	e := newEnv(t)
	e.api.Cfg.HealthInterval = 6 * time.Hour
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "ad@x.io", "password": "adminpass12", "role": "admin"}, 201, nil)
	admin := client()
	e.do(admin, "POST", "/api/auth/login", map[string]any{"email": "ad@x.io", "password": "adminpass12"}, 200, nil)

	var got healthSettingsResp
	e.do(admin, "GET", "/api/admin/health/settings", nil, 200, &got)
	if got.Interval != "6h" || got.Source != "env" || got.Saved != "" || got.Env != "6h" || len(got.Choices) == 0 {
		t.Fatalf("default view: %+v", got)
	}
	// Only a superadmin changes it.
	e.do(admin, "PUT", "/api/admin/health/settings", map[string]any{"interval": "1h"}, 403, nil)

	e.do(e.super, "PUT", "/api/admin/health/settings", map[string]any{"interval": "1h"}, 200, &got)
	if got.Interval != "1h" || got.IntervalSec != 3600 || got.Source != "saved" || got.Saved != "1h" {
		t.Fatalf("saved 1h: %+v", got)
	}
	ctx := context.Background()
	if d := e.st.HealthInterval(ctx, 6*time.Hour); d != time.Hour {
		t.Fatalf("worker would use %v", d)
	}
	e.do(e.super, "PUT", "/api/admin/health/settings", map[string]any{"interval": "off"}, 200, &got)
	if got.Interval != "off" || got.IntervalSec != 0 || got.Source != "saved" {
		t.Fatalf("off: %+v", got)
	}
	if d := e.st.HealthInterval(ctx, 6*time.Hour); d != 0 {
		t.Fatalf("off reads as %v", d)
	}
	for _, bad := range []map[string]any{{"interval": "5s"}, {"interval": "forever"}, {}} {
		e.do(e.super, "PUT", "/api/admin/health/settings", bad, 400, nil)
	}
	// Back to the environment.
	e.do(e.super, "PUT", "/api/admin/health/settings", map[string]any{"interval": ""}, 200, &got)
	if got.Interval != "6h" || got.Source != "env" || got.Saved != "" {
		t.Fatalf("cleared: %+v", got)
	}
	var audit struct {
		Entries []struct {
			Action string `json:"action"`
			Detail string `json:"detail"`
		} `json:"audit"`
	}
	e.do(e.super, "GET", "/api/admin/audit", nil, 200, &audit)
	n := 0
	for _, a := range audit.Entries {
		if a.Action == "settings.health_interval" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("%d audit entries: %+v", n, audit.Entries)
	}
}
