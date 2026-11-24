package ctl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// acn scan [paths...] [--import [--yes] [--only 1,3]]
//
// Finds provider API keys in .env files, shell start up files and the config
// files of agents and tools (Claude, Cursor, Codex and friends), and prints a
// preview: file, line, variable, provider, the key masked to its first and
// last four characters, and whether the server likely holds it already. It
// never prints a whole key and never changes a file. --import uploads the
// chosen keys to a service of their provider after a confirm, making the
// service from the catalog when the server has none.

// scanHome and scanCwd stand in for the user's home and working directory
// in tests.
var (
	scanHome = os.UserHomeDir
	scanCwd  = os.Getwd
)

type finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Var      string `json:"var,omitempty"`
	Provider string `json:"provider"`
	Masked   string `json:"key"`
	// OnServer is "likely" when a key of the provider on the server ends in
	// the same four characters, "no" when none does, "" when unknown.
	OnServer string `json:"on_server,omitempty"`
	secret   string
}

// maxScanFile skips files too large to be config.
const maxScanFile = 4 << 20

// homeScanFiles are the files under the home directory that often hold keys:
// shell start up files, and the config of agents and AI tools.
var homeScanFiles = []string{
	".env", ".bashrc", ".bash_profile", ".profile", ".zshrc", ".zshenv", ".zprofile",
	".config/fish/config.fish",
	".claude.json", ".claude/settings.json", ".claude/settings.local.json",
	".cursor/mcp.json", ".codeium/windsurf/mcp_config.json",
	".codex/config.toml", ".codex/auth.json",
	".gemini/settings.json", ".continue/config.json", ".continue/config.yaml",
	".config/goose/config.yaml", ".aider.conf.yml",
	".config/Claude/claude_desktop_config.json",
	"Library/Application Support/Claude/claude_desktop_config.json",
	"AppData/Roaming/Claude/claude_desktop_config.json",
	"Documents/PowerShell/Microsoft.PowerShell_profile.ps1",
	"Documents/WindowsPowerShell/Microsoft.PowerShell_profile.ps1",
}

// projectScanFiles are looked for in the working directory, besides .env*.
var projectScanFiles = []string{
	".mcp.json", ".cursor/mcp.json", ".vscode/mcp.json",
	".claude/settings.json", ".claude/settings.local.json",
}

// skipDirs are never walked into.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, "dist": true, "build": true, "target": true, ".next": true,
}

// candidate reports whether a file found while walking a directory is worth
// reading: .env files and MCP or agent config.
func candidate(name string) bool {
	n := strings.ToLower(name)
	switch {
	case n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasSuffix(n, ".env"):
		return true
	case n == ".claude.json" || n == "claude_desktop_config.json" || n == "auth.json":
		return true
	case strings.Contains(n, "mcp") && (strings.HasSuffix(n, ".json") || strings.HasSuffix(n, ".toml")):
		return true
	}
	return false
}

// scanTargets lists the files to read: the given paths (a directory is
// walked a few levels deep for candidates), or the default places.
func scanTargets(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if seen[p] {
			return
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Size() <= maxScanFile {
			seen[p] = true
			out = append(out, p)
		}
	}
	walk := func(root string, depth int) {
		root = filepath.Clean(root)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && (skipDirs[d.Name()] || strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)) > depth) {
					return filepath.SkipDir
				}
				return nil
			}
			if candidate(d.Name()) {
				add(p)
			}
			return nil
		})
	}
	if len(paths) > 0 {
		for _, p := range paths {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				walk(p, 3)
			} else {
				add(p)
			}
		}
		return out
	}
	if cwd, err := scanCwd(); err == nil {
		entries, _ := os.ReadDir(cwd)
		for _, e := range entries {
			if !e.IsDir() && candidate(e.Name()) {
				add(filepath.Join(cwd, e.Name()))
			}
		}
		for _, f := range projectScanFiles {
			add(filepath.Join(cwd, filepath.FromSlash(f)))
		}
	}
	if home, err := scanHome(); err == nil {
		for _, f := range homeScanFiles {
			add(filepath.Join(home, filepath.FromSlash(f)))
		}
	}
	return out
}

// assignRe finds NAME=value, NAME: value, "NAME": "value", NAME = "value"
// (TOML), export NAME=value, set -x NAME value (fish) and $env:NAME = value
// (PowerShell).
var assignRe = regexp.MustCompile(`(?:^|[\s{,;(])(?:export\s+|set\s+-[gUux]+\s+|\$env:)?["']?([A-Za-z_][A-Za-z0-9_]{1,63})["']?\s*(?:=|:|\s)\s*["'` + "`" + `]?([A-Za-z0-9_\-.:+/=]{8,512})`)

// keyLike is keyparse's rule for a key-like token: 16 to 512 characters of
// [A-Za-z0-9_\-.:+/=], no "://", with a digit or at least 24 long.
func keyLike(v string) bool {
	if len(v) < 16 || len(v) > 512 || strings.Contains(v, "://") {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.:+/=", r)) {
			return false
		}
	}
	return len(v) >= 24 || strings.ContainsAny(v, "0123456789")
}

// placeholder reports values that only stand in for a key.
func placeholder(v string) bool {
	l := strings.ToLower(v)
	if v == placeholderKey || strings.HasPrefix(l, "acn_") || strings.HasPrefix(l, "acnadm_") {
		return true
	}
	for _, w := range []string{"your", "xxxx", "example", "placeholder", "changeme", "replace", "dummy", "redacted", "<", "${"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// scanText finds keys in one file's text.
func scanText(file string, text []byte, known map[string]bool) []finding {
	var out []finding
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), maxScanFile)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		add := func(name, value, prov string) {
			if seen[value] || placeholder(value) {
				return
			}
			seen[value] = true
			out = append(out, finding{File: file, Line: n, Var: name, Provider: prov, Masked: maskKey(value), secret: value})
		}
		// A variable assignment: a strong key format names the provider,
		// else the variable's name does.
		for _, m := range assignRe.FindAllStringSubmatch(line, -1) {
			name, value := m[1], strings.TrimRight(m[2], ".,;:")
			if prov := providerByFormat(value); prov != "" {
				add(name, value, prov)
			} else if prov := providerByEnvName(name, known); prov != "" && keyLike(value) {
				add(name, value, prov)
			}
		}
		// Keys with a strong format anywhere else on the line, such as an
		// MCP server's --api-key argument.
		for _, f := range keyFormats {
			for _, v := range f.re.FindAllString(line, -1) {
				add("", v, f.provider)
			}
		}
	}
	return out
}

// maskKey shows the first and last four characters of a key, never more.
func maskKey(s string) string {
	if len(s) < 16 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// shortPath shows a path under the home directory as ~/...
func shortPath(p string) string {
	if home, err := scanHome(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~" + string(filepath.Separator) + rel
		}
	}
	return p
}

func (c *cli) scan(args []string) error {
	var paths []string
	doImport, yes, only := false, false, ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--import":
			doImport = true
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--only" && i+1 < len(args):
			only = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("usage: %s scan [paths...] [--import [--yes] [--only 1,3]]", c.name())
		default:
			paths = append(paths, a)
		}
	}
	// Signed in, the server's providers widen the names matched and its keys
	// tell which findings it likely holds. Signed out, the scan still runs.
	_, loginErr := c.current()
	signedIn := loginErr == nil
	known := map[string]bool{}
	if signedIn {
		var ps struct {
			Providers []struct {
				Slug string `json:"slug"`
			} `json:"providers"`
		}
		if err := c.call("GET", "/api/admin/providers", nil, &ps); err == nil {
			for _, p := range ps.Providers {
				known[p.Slug] = true
			}
		}
	}
	var found []finding
	for _, f := range scanTargets(paths) {
		b, err := os.ReadFile(f)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			continue
		}
		found = append(found, scanText(shortPath(f), b, known)...)
	}
	// One row per key: the same key in two files shows once, where first found.
	dedup := found[:0]
	seen := map[string]bool{}
	for _, f := range found {
		if !seen[f.secret] {
			seen[f.secret] = true
			dedup = append(dedup, f)
		}
	}
	found = dedup
	var svcs []service
	if signedIn {
		svcs, _ = c.listServices()
		c.markOnServer(found, svcs)
	}
	if c.json {
		if found == nil {
			found = []finding{}
		}
		return c.printJSON(mustJSON(map[string]any{"findings": found}))
	}
	if len(found) == 0 {
		fmt.Fprintln(c.out, "No provider keys found.")
		return nil
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tFILE\tLINE\tVARIABLE\tPROVIDER\tKEY\tON SERVER")
	for i, f := range found {
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\t%s\n", i+1, clean(f.File), f.Line, orDash(f.Var), f.Provider, f.Masked, orDash(f.OnServer))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !doImport {
		fmt.Fprintf(c.out, "\nNothing was changed. To add these keys to the server: %s scan --import\n", c.name())
		return nil
	}
	if !signedIn {
		return loginErr
	}
	pick, err := c.pickFindings(found, only, yes)
	if err != nil || len(pick) == 0 {
		return err
	}
	return c.importFindings(pick, svcs)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// markOnServer sets OnServer from the keys' hints (their last four
// characters). The server keeps only a keyed fingerprint of each secret, so
// this is a likely match, not proof; an import skips true duplicates.
func (c *cli) markOnServer(found []finding, svcs []service) {
	var out struct {
		Keys []struct {
			Hint        string `json:"hint"`
			ServiceSlug string `json:"service_slug"`
		} `json:"keys"`
	}
	if err := c.call("GET", "/api/admin/keys", nil, &out); err != nil {
		return
	}
	provOf := map[string]string{}
	for _, s := range svcs {
		provOf[s.Slug] = s.Provider
	}
	have := map[string]bool{}
	for _, k := range out.Keys {
		have[provOf[k.ServiceSlug]+"|"+k.Hint] = true
	}
	for i := range found {
		f := &found[i]
		f.OnServer = "no"
		if have[f.Provider+"|..."+f.secret[len(f.secret)-4:]] {
			f.OnServer = "likely"
		}
	}
}

// pickFindings asks which findings to import: all of them with --yes, the
// --only numbers, or what the person types.
func (c *cli) pickFindings(found []finding, only string, yes bool) ([]finding, error) {
	in := bufio.NewReader(c.in)
	sel := only
	if sel == "" && yes {
		sel = "all"
	}
	if sel == "" {
		fmt.Fprint(c.err, "\nImport which keys? all, none, or numbers like 1,3: ")
		line, _ := in.ReadString('\n')
		sel = strings.TrimSpace(line)
	}
	var pick []finding
	switch strings.ToLower(sel) {
	case "", "none", "n", "no":
		fmt.Fprintln(c.out, "Nothing imported.")
		return nil, nil
	case "all", "a", "y", "yes":
		pick = found
	default:
		for _, part := range strings.FieldsFunc(sel, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 || n > len(found) {
				return nil, fmt.Errorf("%q is not a number from the table (1 to %d)", part, len(found))
			}
			pick = append(pick, found[n-1])
		}
	}
	if !yes {
		fmt.Fprintf(c.err, "Upload %d key(s) to %s? Your files stay as they are. [y/N]: ", len(pick), c.serverName())
		line, _ := in.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(c.out, "Nothing imported.")
			return nil, nil
		}
	}
	return pick, nil
}

func (c *cli) serverName() string {
	if ctx, err := c.current(); err == nil {
		return hostOf(ctx.URL)
	}
	return "the server"
}

// importFindings uploads keys to the first service of their provider,
// creating one from the catalog when there is none.
func (c *cli) importFindings(pick []finding, svcs []service) error {
	byProv := map[string][]finding{}
	var order []string
	for _, f := range pick {
		if byProv[f.Provider] == nil {
			order = append(order, f.Provider)
		}
		byProv[f.Provider] = append(byProv[f.Provider], f)
	}
	sort.Strings(order)
	failed := 0
	for _, prov := range order {
		sv, created, err := c.serviceFor(prov, svcs)
		if err != nil {
			fmt.Fprintf(c.out, "%s: skipped, %v\n", prov, err)
			failed++
			continue
		}
		if created {
			svcs = append(svcs, sv)
		}
		keys := []map[string]any{}
		for _, f := range byProv[prov] {
			label := f.Var
			if label == "" {
				label = prov
			}
			keys = append(keys, map[string]any{"secret": f.secret, "label": label + " (acn scan)"})
		}
		var out struct {
			Keys    []any    `json:"keys"`
			Skipped []string `json:"skipped"`
		}
		if err := c.call("POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"keys": keys}, &out); err != nil {
			fmt.Fprintf(c.out, "%s: upload failed, %v\n", prov, err)
			failed++
			continue
		}
		note := ""
		if created {
			note = " (new service from the catalog)"
		}
		fmt.Fprintf(c.out, "%s: added %d, already there %d, to service %s%s\n", prov, len(out.Keys), len(out.Skipped), clean(sv.Slug), note)
	}
	fmt.Fprintln(c.out, "\nNo file was changed. Once your apps call through Acnologia (acn run, or the forward proxy), remove the keys from those files yourself.")
	if failed > 0 {
		return errors.New("some keys were not imported")
	}
	return nil
}

// serviceFor finds the provider's service, or makes one from the catalog.
func (c *cli) serviceFor(prov string, svcs []service) (service, bool, error) {
	for _, s := range svcs {
		if s.Provider == prov {
			return s, false, nil
		}
	}
	var sv service
	if err := c.call("POST", "/api/admin/services", map[string]any{"provider": prov}, &sv); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.Code == "unknown_provider" {
			return service{}, false, fmt.Errorf("the server's catalog has no provider %q", prov)
		}
		return service{}, false, err
	}
	return sv, true, nil
}
