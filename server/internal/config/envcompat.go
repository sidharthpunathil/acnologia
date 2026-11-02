package config

import (
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
)

// The product used to be called Cogito and read COGITO_* variables. Getenv
// keeps those working: it reads the ACNOLOGIA_* name first and falls back to
// the old one, remembering which old names were used so startup can warn once.
const (
	envPrefix       = "ACNOLOGIA_"
	legacyEnvPrefix = "COGITO_"
)

var (
	legacyMu   sync.Mutex
	legacyUsed = map[string]bool{}
	legacyOnce sync.Once
)

// Getenv returns the value of name, an ACNOLOGIA_* variable. When it is unset
// or empty, the deprecated COGITO_* variable with the same suffix is used.
// Names without the ACNOLOGIA_ prefix are read as is.
func Getenv(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	suffix, ok := strings.CutPrefix(name, envPrefix)
	if !ok {
		return ""
	}
	old := legacyEnvPrefix + suffix
	v := os.Getenv(old)
	if v != "" {
		legacyMu.Lock()
		legacyUsed[old] = true
		legacyMu.Unlock()
	}
	return v
}

// DeprecatedEnvUsed lists the COGITO_* names Getenv fell back to, sorted.
func DeprecatedEnvUsed() []string {
	legacyMu.Lock()
	defer legacyMu.Unlock()
	out := make([]string, 0, len(legacyUsed))
	for k := range legacyUsed {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LogDeprecatedEnv warns once about deprecated variable names. It logs the
// names only, never their values.
func LogDeprecatedEnv() {
	legacyOnce.Do(func() {
		if names := DeprecatedEnvUsed(); len(names) > 0 {
			slog.Warn("deprecated COGITO_* environment variables are in use; rename them to ACNOLOGIA_*",
				"names", strings.Join(names, ","))
		}
	})
}
