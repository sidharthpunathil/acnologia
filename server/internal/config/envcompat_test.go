package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func resetLegacy() {
	legacyMu.Lock()
	legacyUsed = map[string]bool{}
	legacyMu.Unlock()
}

func TestGetenvPrefersNewName(t *testing.T) {
	resetLegacy()
	t.Setenv("ACNOLOGIA_ADDR", ":9001")
	t.Setenv("COGITO_ADDR", ":9002")
	if got := Getenv("ACNOLOGIA_ADDR"); got != ":9001" {
		t.Fatalf("got %q", got)
	}
	if used := DeprecatedEnvUsed(); len(used) != 0 {
		t.Fatalf("no fallback expected, got %v", used)
	}
}

func TestGetenvFallsBackToLegacyName(t *testing.T) {
	resetLegacy()
	t.Setenv("ACNOLOGIA_DATA_DIR", "")
	t.Setenv("COGITO_DATA_DIR", "/srv/old")
	t.Setenv("ACNOLOGIA_MASTER_KEY", "")
	t.Setenv("COGITO_MASTER_KEY", "s3cret-master-value")
	t.Setenv("ACNOLOGIA_CACHE_MB", "")
	t.Setenv("COGITO_CACHE_MB", "8")
	cfg := Load("test")
	if cfg.DataDir != "/srv/old" || cfg.MasterKey != "s3cret-master-value" || cfg.CacheBytes != 8<<20 {
		t.Fatalf("legacy values not used: %+v", cfg)
	}
	used := strings.Join(DeprecatedEnvUsed(), ",")
	for _, n := range []string{"COGITO_DATA_DIR", "COGITO_MASTER_KEY", "COGITO_CACHE_MB"} {
		if !strings.Contains(used, n) {
			t.Fatalf("%s missing from %q", n, used)
		}
	}
}

func TestGetenvIgnoresOtherNames(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("COGITO_GITHUB_TOKEN", "x")
	if Getenv("GITHUB_TOKEN") != "" {
		t.Fatal("only ACNOLOGIA_ names fall back")
	}
}

func TestLogDeprecatedEnvNamesOnly(t *testing.T) {
	resetLegacy()
	t.Setenv("ACNOLOGIA_TOKEN", "")
	t.Setenv("COGITO_TOKEN", "acn_value_that_must_not_be_logged")
	_ = Getenv("ACNOLOGIA_TOKEN")
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	LogDeprecatedEnv()
	LogDeprecatedEnv()
	out := buf.String()
	if strings.Count(out, "deprecated") != 1 || !strings.Contains(out, "COGITO_TOKEN") {
		t.Fatalf("want one warning naming COGITO_TOKEN, got %q", out)
	}
	if strings.Contains(out, "acn_value_that_must_not_be_logged") {
		t.Fatal("the value leaked into the log")
	}
}
