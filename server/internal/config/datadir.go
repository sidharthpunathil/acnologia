package config

import (
	"os"
	"runtime"
	"strings"
)

// DefaultDataDir is where the database, master.key and access-password live
// when ACNOLOGIA_DATA_DIR is not set.
func DefaultDataDir() string { return defaultDataDirFor(runtime.GOOS, os.Getenv) }

// defaultDataDirFor is ./data below the directory the server starts in,
// except on Windows, where it is %LocalAppData%\Acnologia: a service or a
// Scheduled Task starts in C:\Windows\System32, and the user's local AppData
// folder is already private to them (Unix file modes do not apply there).
func defaultDataDirFor(goos string, getenv func(string) string) string {
	if goos == "windows" {
		if d := getenv("LOCALAPPDATA"); d != "" {
			return strings.TrimRight(d, `\/`) + `\Acnologia`
		}
	}
	return "./data"
}
