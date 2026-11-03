package ctl

// `acn mcp` runs the control MCP over stdio for Claude Code, Claude Desktop
// and Cursor, as the signed in user; `acn mcp config` prints the snippet
// that adds it to a client.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
	"github.com/sidharthpunathil/acnologia/server/internal/mcpbridge"
)

// tokenPlaceholder stands in for the token in --http snippets unless
// --show-token is given, so a snippet pasted into a chat or a ticket leaks
// nothing.
const tokenPlaceholder = "<your acnadm_ token>"

func (c *cli) mcp(args []string) error {
	if len(args) > 0 && args[0] == "config" {
		return c.mcpConfig(args[1:])
	}
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(c.err)
	name := fs.String("context", "", "saved server to use (default: the current one, see contexts)")
	var f mcpbridge.Filters
	f.Register(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("usage: %s mcp [--context name] [--groups g1,g2] [--exclude t1,t2] [--read-only] [--confirm on|off], or %s mcp config", c.name(), c.name())
	}
	if err := f.Normalize(); err != nil {
		return err
	}
	ctx, err := c.mcpContext(*name)
	if err != nil {
		return err
	}
	if mcpbridge.IsAppToken(ctx.Token) {
		return errors.New("ACNOLOGIA_TOKEN holds an app token (acn_...); the control MCP needs an admin token (acnadm_...). For the app MCP, run: acnologia mcp --token acn_...")
	}
	h := f.Header()
	if pw := config.Getenv("ACNOLOGIA_ACCESS_PASSWORD"); pw != "" {
		h.Set("X-Acnologia-Access", pw)
	}
	return mcpbridge.Run(mcpbridge.Options{
		Endpoint: ctx.URL + "/mcp/control",
		Token:    ctx.Token,
		Header:   h,
		Unauthorized: fmt.Sprintf("the saved token may have expired or been revoked: run %s login again, or set ACNOLOGIA_TOKEN to a valid admin token (acnadm_...)",
			c.name()),
		Client: c.http,
	}, c.in, c.out, c.err)
}

// mcpContext is the named saved server, or the current one.
func (c *cli) mcpContext(name string) (context, error) {
	if name != "" {
		cf := c.load()
		ctx, ok := cf.Contexts[name]
		if !ok {
			return context{}, fmt.Errorf("no saved server named %q; %s contexts lists them", name, c.name())
		}
		return ctx, nil
	}
	ctx, err := c.current()
	if err != nil {
		return context{}, fmt.Errorf("not logged in; run %s login first (or set ACNOLOGIA_URL and ACNOLOGIA_TOKEN), then start your MCP client again", c.name())
	}
	return ctx, nil
}

// mcpServer is one entry under mcpServers in a client's JSON config.
type mcpServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (c *cli) mcpConfig(args []string) error {
	fs := flag.NewFlagSet("mcp config", flag.ContinueOnError)
	fs.SetOutput(c.err)
	client := fs.String("client", "claude-code", "claude-code, claude-desktop, cursor or json")
	httpMode := fs.Bool("http", false, "connect to the server's URL directly instead of running "+c.name()+" mcp")
	name := fs.String("name", "acnologia", "the server's name in the client")
	showToken := fs.Bool("show-token", false, "with --http, print the saved token instead of a placeholder")
	ctxName := fs.String("context", "", "saved server to use (default: the current one)")
	var f mcpbridge.Filters
	f.Register(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("usage: %s mcp config [--client claude-code|claude-desktop|cursor|json] [--http] [--name acnologia] [filters]", c.name())
	}
	switch *client {
	case "claude-code", "claude-desktop", "cursor", "json":
	default:
		return fmt.Errorf("unknown client %q; use claude-code, claude-desktop, cursor or json", *client)
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("--name is empty")
	}
	if err := f.Normalize(); err != nil {
		return err
	}

	var srv mcpServer
	if *httpMode {
		ctx, err := c.mcpContext(*ctxName)
		if err != nil {
			return err
		}
		u := ctx.URL + "/mcp/control"
		if q := f.Query(); q != "" {
			u += "?" + q
		}
		tok := tokenPlaceholder
		if *showToken {
			tok = ctx.Token
		} else {
			fmt.Fprintf(c.err, "Replace %s with an admin token, or run again with --show-token to print the saved one.\n", tokenPlaceholder)
		}
		srv = mcpServer{Type: "http", URL: u, Headers: map[string]string{"Authorization": "Bearer " + tok}}
		if *client == "claude-desktop" {
			fmt.Fprintf(c.err, "Claude Desktop may only run local commands from its config file; if it skips this entry, drop --http to use %s mcp.\n", c.name())
		}
	} else {
		exe, err := c.executablePath()
		if err != nil {
			return fmt.Errorf("cannot find the path of %s: %w", c.name(), err)
		}
		a := append(c.progArgs(), "mcp")
		if *ctxName != "" {
			if _, ok := c.load().Contexts[*ctxName]; !ok {
				return fmt.Errorf("no saved server named %q; %s contexts lists them", *ctxName, c.name())
			}
			a = append(a, "--context", *ctxName)
		} else if _, err := c.current(); err != nil {
			fmt.Fprintf(c.err, "Not logged in yet: run %s login before starting your MCP client.\n", c.name())
		}
		srv = mcpServer{Command: exe, Args: append(a, f.Args()...)}
	}

	switch *client {
	case "claude-code":
		if srv.Type == "http" {
			fmt.Fprintf(c.out, "claude mcp add --transport http %s %s --header \"Authorization: %s\"\n",
				shellQuote(*name), shellQuote(srv.URL), srv.Headers["Authorization"])
			return nil
		}
		words := []string{"claude", "mcp", "add", shellQuote(*name), "--", shellQuote(srv.Command)}
		for _, a := range srv.Args {
			words = append(words, shellQuote(a))
		}
		fmt.Fprintln(c.out, strings.Join(words, " "))
		return nil
	case "json":
		return writeJSON(c, srv)
	default:
		return writeJSON(c, map[string]any{"mcpServers": map[string]mcpServer{*name: srv}})
	}
}

// writeJSON prints v indented, without escaping <, > and & (the placeholder
// and query strings read as typed).
func writeJSON(c *cli, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := c.out.Write(b.Bytes())
	return err
}

func (c *cli) executablePath() (string, error) {
	if c.executable != nil {
		return c.executable()
	}
	return os.Executable()
}

// progArgs are the words after the binary that reach this client: none for
// acn, "ctl" for acnologia ctl.
func (c *cli) progArgs() []string {
	w := strings.Fields(c.name())
	return append([]string(nil), w[1:]...)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
