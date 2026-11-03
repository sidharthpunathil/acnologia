package mcpbridge

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Groups are the control MCP tool groups, in the order the server lists
// them. "all" also selects admin, secrets and raw, which are off by default.
var Groups = []string{"overview", "call", "services", "keys", "apps", "providers", "ai", "routing", "sharing", "refresh", "admin", "secrets", "raw"}

// Filters choose which control MCP tools a client sees. The zero value is
// the server's default set.
type Filters struct {
	Groups   string // comma separated group names, or "all"
	Exclude  string // comma separated tool names
	ReadOnly bool
	Confirm  string // "on", "off" or empty for the server default (on)
}

// Register adds --groups, --exclude, --read-only and --confirm to fs.
func (f *Filters) Register(fs *flag.FlagSet) {
	fs.StringVar(&f.Groups, "groups", "", "only these tool groups, comma separated ("+strings.Join(Groups, ", ")+", or all)")
	fs.StringVar(&f.Exclude, "exclude", "", "drop these tools by name, comma separated")
	fs.BoolVar(&f.ReadOnly, "read-only", false, "list only read tools and refuse the rest")
	fs.StringVar(&f.Confirm, "confirm", "", "on (default) or off: whether destructive tools ask for confirm: true first")
}

// Set reports whether any filter is given.
func (f Filters) Set() bool {
	return f.Groups != "" || f.Exclude != "" || f.ReadOnly || f.Confirm != ""
}

// Normalize trims the lists and checks the group names and --confirm.
func (f *Filters) Normalize() error {
	f.Groups = cleanList(f.Groups)
	f.Exclude = cleanList(f.Exclude)
	for _, g := range strings.Split(f.Groups, ",") {
		if g == "" || g == "all" || known(g) {
			continue
		}
		return fmt.Errorf("unknown group %q; groups are %s, or all", g, strings.Join(Groups, ", "))
	}
	f.Confirm = strings.ToLower(strings.TrimSpace(f.Confirm))
	if f.Confirm != "" && f.Confirm != "on" && f.Confirm != "off" {
		return fmt.Errorf("--confirm takes on or off, not %q", f.Confirm)
	}
	return nil
}

// Header is the filters as the X-Acnologia-MCP-* headers the bridge sends.
func (f Filters) Header() http.Header {
	h := http.Header{}
	if f.Groups != "" {
		h.Set("X-Acnologia-MCP-Groups", f.Groups)
	}
	if f.Exclude != "" {
		h.Set("X-Acnologia-MCP-Exclude", f.Exclude)
	}
	if f.ReadOnly {
		h.Set("X-Acnologia-MCP-Read-Only", "1")
	}
	if f.Confirm != "" {
		h.Set("X-Acnologia-MCP-Confirm", f.Confirm)
	}
	return h
}

// Query is the filters as the endpoint's query string (without the "?"),
// for clients that connect over HTTP. Commas stay readable.
func (f Filters) Query() string {
	var parts []string
	add := func(k, v string) {
		parts = append(parts, k+"="+strings.ReplaceAll(url.QueryEscape(v), "%2C", ","))
	}
	if f.Groups != "" {
		add("groups", f.Groups)
	}
	if f.Exclude != "" {
		add("exclude", f.Exclude)
	}
	if f.ReadOnly {
		add("read_only", "1")
	}
	if f.Confirm != "" {
		add("confirm", f.Confirm)
	}
	return strings.Join(parts, "&")
}

// Args is the filters as command line flags, for a client config that runs
// the bridge.
func (f Filters) Args() []string {
	var a []string
	if f.Groups != "" {
		a = append(a, "--groups", f.Groups)
	}
	if f.Exclude != "" {
		a = append(a, "--exclude", f.Exclude)
	}
	if f.ReadOnly {
		a = append(a, "--read-only")
	}
	if f.Confirm != "" {
		a = append(a, "--confirm", f.Confirm)
	}
	return a
}

func cleanList(s string) string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

func known(g string) bool {
	for _, k := range Groups {
		if g == k {
			return true
		}
	}
	return false
}

// IsAppToken reports whether tok looks like an app token (acn_, or the
// older cog_). It mirrors store.IsAppToken without importing the store.
func IsAppToken(tok string) bool {
	return strings.HasPrefix(tok, "acn_") || strings.HasPrefix(tok, "cog_")
}

// IsAdminToken reports whether tok looks like an admin API token (acnadm_,
// or the older cogadm_).
func IsAdminToken(tok string) bool {
	return strings.HasPrefix(tok, "acnadm_") || strings.HasPrefix(tok, "cogadm_")
}
