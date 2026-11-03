package main

// `acnologia mcp` bridges a stdio MCP client (for example Claude Desktop) to an
// Acnologia server's streamable HTTP MCP endpoint. With an app token it talks
// to /mcp; with --control (or an admin token) it talks to /mcp/control, the
// MCP that runs the server itself.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/mcpbridge"
)

const mcpUsage = `usage: acnologia mcp [--url <base>] [--token acn_...]
       acnologia mcp --control [--url <base>] [--token acnadm_...]
                     [--groups g1,g2] [--exclude tool1,tool2] [--read-only] [--confirm on|off]

Bridges a stdio MCP client to Acnologia. An app token (acn_...) talks to /mcp;
--control, or an admin token (acnadm_...), talks to /mcp/control and runs the
whole server as the token's owner. ACNOLOGIA_URL and ACNOLOGIA_TOKEN set the
defaults.

Flags:
`

func runMCPBridge(args []string) int {
	return mcpMain(args, os.Stdin, os.Stdout, os.Stderr)
}

func mcpMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, mcpUsage)
		fs.PrintDefaults()
	}
	url := fs.String("url", envOr("ACNOLOGIA_URL", "http://localhost:8080"), "Acnologia base URL (or ACNOLOGIA_URL)")
	token := fs.String("token", config.Getenv("ACNOLOGIA_TOKEN"), "app token acn_..., or admin token acnadm_... with --control (or ACNOLOGIA_TOKEN)")
	control := fs.Bool("control", false, "bridge to /mcp/control (implied by an acnadm_ token)")
	var f mcpbridge.Filters
	f.Register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	o, err := mcpOptions(*url, *token, *control, f)
	if err != nil {
		fmt.Fprintln(stderr, "acnologia mcp:", err)
		return 2
	}
	if err := mcpbridge.Run(o, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "acnologia mcp:", err)
		return 1
	}
	return 0
}

// mcpOptions picks the endpoint from the token and flags.
func mcpOptions(base, token string, control bool, f mcpbridge.Filters) (mcpbridge.Options, error) {
	base = strings.TrimRight(base, "/")
	if mcpbridge.IsAdminToken(token) {
		control = true
	}
	if !control {
		if !mcpbridge.IsAppToken(token) {
			return mcpbridge.Options{}, fmt.Errorf("set an app token with --token acn_... or ACNOLOGIA_TOKEN")
		}
		if f.Set() {
			return mcpbridge.Options{}, fmt.Errorf("--groups, --exclude, --read-only and --confirm apply to the control MCP; add --control and use an admin token (acnadm_...)")
		}
		return mcpbridge.Options{
			Endpoint:     base + "/mcp",
			Token:        token,
			Unauthorized: "pass a valid app token with --token acn_... or ACNOLOGIA_TOKEN",
		}, nil
	}
	if mcpbridge.IsAppToken(token) {
		return mcpbridge.Options{}, fmt.Errorf("--control needs an admin token (acnadm_...), not an app token; app tokens use /mcp (drop --control)")
	}
	if !mcpbridge.IsAdminToken(token) {
		return mcpbridge.Options{}, fmt.Errorf("set an admin token with --token acnadm_... or ACNOLOGIA_TOKEN (create one under Account, API tokens, or run acn login)")
	}
	if err := f.Normalize(); err != nil {
		return mcpbridge.Options{}, err
	}
	return mcpbridge.Options{
		Endpoint:     base + "/mcp/control",
		Token:        token,
		Header:       f.Header(),
		Unauthorized: "pass a valid admin token with --token acnadm_... or ACNOLOGIA_TOKEN (it may have expired or been revoked)",
	}, nil
}

func envOr(k, d string) string {
	if v := config.Getenv(k); v != "" {
		return v
	}
	return d
}
