package ctl

// Commands for agents and scripts: acn api (any route), acn tool (any
// control MCP tool, so each one is a command without code here), acn apps
// and acn tokens, and the exit codes every command uses.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Exit codes. Scripts branch on them; --json also prints the error.
const (
	exitOther     = 1
	exitUsage     = 2
	exitAuth      = 3
	exitNotFound  = 4
	exitRetryable = 5
)

// usageError is a command used the wrong way.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

// toolError is a control MCP tool's error result: its structuredContent
// (code, message, status, hint, ...) when it has one.
type toolError struct {
	Fields map[string]any
	Text   string
}

func (e *toolError) Error() string { return clean(e.Text) }

func (e *toolError) status() int {
	switch v := e.Fields["status"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func (e *toolError) code() string {
	s, _ := e.Fields["code"].(string)
	return s
}

func isUsage(err error) bool {
	var ue *usageError
	if errors.As(err, &ue) || errors.Is(err, flag.ErrHelp) {
		return true
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "usage:") || strings.HasPrefix(msg, "unknown command") ||
		strings.HasPrefix(msg, "flag provided but not defined") || strings.HasPrefix(msg, "invalid value") ||
		strings.HasPrefix(msg, "invalid boolean") || strings.HasPrefix(msg, "flag needs an argument")
}

func statusExit(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return exitAuth
	case http.StatusNotFound:
		return exitNotFound
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return exitRetryable
	}
	return exitOther
}

func codeExit(code string) int {
	switch code {
	case "unauthenticated", "forbidden", "session_required", "locked":
		return exitAuth
	case "not_found":
		return exitNotFound
	case "rate_limited", "unavailable", "busy":
		return exitRetryable
	case "invalid", "unknown_tool":
		return exitUsage
	}
	return exitOther
}

// exitCode is the process exit code for err.
func exitCode(err error) int {
	var he *httpError
	var te *toolError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &he):
		return statusExit(he.Status)
	case errors.As(err, &te):
		if s := te.status(); s >= 400 {
			return statusExit(s)
		}
		return codeExit(te.code())
	case isUsage(err):
		return exitUsage
	case strings.HasPrefix(err.Error(), "not logged in"):
		return exitAuth
	}
	return exitOther
}

// errorJSON is err as the error object --json prints: the server's fields
// when it sent them, else a code and the message.
func errorJSON(err error) map[string]any {
	out := map[string]any{}
	var he *httpError
	var te *toolError
	switch {
	case errors.As(err, &he):
		for k, v := range he.Fields {
			out[k] = v
		}
		out["status"] = he.Status
		if out["code"] == nil || out["code"] == "" {
			out["code"] = he.Code
		}
		if out["message"] == nil || out["message"] == "" {
			out["message"] = he.Error()
		}
	case errors.As(err, &te):
		for k, v := range te.Fields {
			out[k] = v
		}
		if out["message"] == nil {
			out["message"] = te.Text
		}
	case isUsage(err):
		out["code"], out["message"] = "usage", err.Error()
	case strings.HasPrefix(err.Error(), "not logged in"):
		out["code"], out["message"] = "not_logged_in", err.Error()
	default:
		out["code"], out["message"] = "error", err.Error()
	}
	if out["code"] == nil || out["code"] == "" {
		out["code"] = "error"
	}
	out["exit_code"] = exitCode(err)
	return out
}

// errorHint is the hint a server error carries, if any.
func errorHint(err error) string {
	var he *httpError
	var te *toolError
	switch {
	case errors.As(err, &he):
		h, _ := he.Fields["hint"].(string)
		return h
	case errors.As(err, &te):
		h, _ := te.Fields["hint"].(string)
		return h
	}
	return ""
}

// readData reads a --data or --args value: inline JSON, @file, or @- for
// stdin. The result must be JSON.
func (c *cli) readData(v string) (json.RawMessage, error) {
	var b []byte
	switch {
	case v == "@-" || v == "-":
		var err error
		if b, err = io.ReadAll(io.LimitReader(c.in, 16<<20)); err != nil {
			return nil, err
		}
	case strings.HasPrefix(v, "@"):
		var err error
		if b, err = os.ReadFile(v[1:]); err != nil {
			return nil, err
		}
	default:
		b = []byte(v)
	}
	b = bytes.TrimSpace(b)
	if !json.Valid(b) {
		return nil, usageErrorf("the data is not valid JSON")
	}
	return b, nil
}

// ---- acn api ----

func (c *cli) apiCmd(args []string) error {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	fs.SetOutput(c.err)
	data := fs.String("data", "", "JSON body: inline, @file, or @- for stdin")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("usage: %s api <METHOD> <path> [--data json|@file|@-]", c.name())
	}
	method, path := strings.ToUpper(pos[0]), pos[1]
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		return usageErrorf("method must be GET, POST, PUT, PATCH, DELETE or HEAD")
	}
	if !strings.HasPrefix(path, "/") {
		return usageErrorf("the path starts with /, such as /api/admin/services")
	}
	var body any
	if *data != "" {
		raw, err := c.readData(*data)
		if err != nil {
			return err
		}
		body = raw
	}
	var raw json.RawMessage
	if err := c.call(method, path, body, &raw); err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if json.Valid(raw) {
		return c.printJSON(raw)
	}
	_, err = fmt.Fprintln(c.out, clean(string(raw)))
	return err
}

// ---- acn tool ----

// controlRPC sends one JSON-RPC request to the control MCP, with every
// group on so any tool the role allows can run.
func (c *cli) controlRPC(method string, params any) (map[string]any, error) {
	ctx, err := c.current()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	rpc := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	if err := c.callWith(ctx, http.MethodPost, "/mcp/control?groups=all", rpc, &raw); err != nil {
		return nil, err
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("the control MCP's answer is not JSON-RPC: %w", err)
	}
	if resp.Error != nil {
		code := "rpc_error"
		if strings.HasPrefix(resp.Error.Message, "unknown tool") {
			code = "unknown_tool"
		}
		return nil, &toolError{Fields: map[string]any{"code": code, "message": resp.Error.Message, "rpc_code": resp.Error.Code}, Text: resp.Error.Message}
	}
	return resp.Result, nil
}

// parseToolArgs turns key=value pairs into tool arguments. A value that
// parses as JSON is used as JSON (numbers, true, arrays, objects); any
// other value is text. Quote a value to keep it text: name='"123"'.
func parseToolArgs(pairs []string, into map[string]any) error {
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return usageErrorf("arguments are key=value, got %q", p)
		}
		var j any
		if json.Unmarshal([]byte(v), &j) == nil {
			into[k] = j
		} else {
			into[k] = v
		}
	}
	return nil
}

func (c *cli) toolCmd(args []string) error {
	fs := flag.NewFlagSet("tool", flag.ContinueOnError)
	fs.SetOutput(c.err)
	list := fs.Bool("list", false, "list the tools")
	argsJSON := fs.String("args", "", "arguments as a JSON object: inline, @file, or @- for stdin")
	yes := fs.Bool("yes", false, "confirm a destructive tool (confirm: true)")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if *list {
		return c.toolList()
	}
	if len(pos) == 0 {
		return usageErrorf("usage: %s tool <name> [key=value ...] [--args json|@file] [--yes], or %s tool --list", c.name(), c.name())
	}
	in := map[string]any{}
	if *argsJSON != "" {
		raw, err := c.readData(*argsJSON)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &in); err != nil || in == nil {
			return usageErrorf("--args must be a JSON object")
		}
	}
	if err := parseToolArgs(pos[1:], in); err != nil {
		return err
	}
	if *yes {
		in["confirm"] = true
	}
	res, err := c.controlRPC("tools/call", map[string]any{"name": pos[0], "arguments": in})
	if err != nil {
		return err
	}
	text := ""
	if content, ok := res["content"].([]any); ok && len(content) > 0 {
		if m, ok := content[0].(map[string]any); ok {
			text, _ = m["text"].(string)
		}
	}
	sc, _ := res["structuredContent"].(map[string]any)
	if res["isError"] == true {
		if sc == nil {
			sc = errorFromText(text)
		}
		return &toolError{Fields: sc, Text: text}
	}
	if c.json {
		var v any = sc
		if sc == nil {
			v = map[string]any{"text": text}
		}
		b, _ := json.Marshal(v)
		return c.printJSON(b)
	}
	if sc != nil {
		b, _ := json.Marshal(sc)
		return c.printJSON(b)
	}
	_, err = fmt.Fprintln(c.out, clean(text))
	return err
}

// errorFromText reads a tool error that came as text only (tools with an
// output schema keep error fields out of structuredContent): it starts
// "HTTP <status> <code>: <message>".
func errorFromText(text string) map[string]any {
	out := map[string]any{"code": "tool_error", "message": text}
	head, msg, ok := strings.Cut(strings.SplitN(text, "\n", 2)[0], ": ")
	f := strings.Fields(head)
	if !ok || len(f) != 3 || f[0] != "HTTP" {
		return out
	}
	if n, err := strconv.Atoi(f[1]); err == nil {
		out["status"], out["code"], out["message"] = n, f[2], msg
	}
	return out
}

func (c *cli) toolList() error {
	res, err := c.controlRPC("tools/list", map[string]any{})
	if err != nil {
		return err
	}
	tools, _ := res["tools"].([]any)
	if c.json {
		b, _ := json.Marshal(map[string]any{"tools": tools})
		return c.printJSON(b)
	}
	type row struct{ name, kind, title string }
	rows := []row{}
	for _, x := range tools {
		t, _ := x.(map[string]any)
		name, _ := t["name"].(string)
		title, _ := t["title"].(string)
		kind := "write"
		if ann, ok := t["annotations"].(map[string]any); ok {
			if ann["readOnlyHint"] == true {
				kind = "read"
			} else if ann["destructiveHint"] == true {
				kind = "destructive"
			}
		}
		rows = append(rows, row{strings.TrimPrefix(name, "acnologia_"), kind, title})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tKIND\tWHAT")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", clean(r.name), r.kind, clean(r.title))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "\n%d tools. Run one with: %s tool <name> key=value ... (destructive ones need --yes)\n", len(rows), c.name())
	return nil
}

// ---- acn apps and acn tokens ----

type appRow struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Mode            string   `json:"mode"`
	Active          bool     `json:"active"`
	AllowedServices []string `json:"allowed_services"`
	TokenCount      int      `json:"token_count"`
}

func (c *cli) apps(args []string) error {
	if len(args) > 0 && args[0] == "create" {
		return c.appsCreate(args[1:])
	}
	if len(args) > 0 && args[0] != "list" {
		return usageErrorf("usage: %s apps [list], or %s apps create <name> [--mode proxy] [--services a,b]", c.name(), c.name())
	}
	var out struct {
		Apps []appRow `json:"apps"`
	}
	if done, err := c.callJSON(http.MethodGet, "/api/admin/apps", &out); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tMODE\tACTIVE\tTOKENS\tSERVICES")
	for _, a := range out.Apps {
		svcs := "all"
		if len(a.AllowedServices) > 0 {
			svcs = strings.Join(a.AllowedServices, ",")
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%v\t%d\t%s\n", a.ID, clean(a.Name), clean(a.Mode), a.Active, a.TokenCount, clean(svcs))
	}
	return tw.Flush()
}

func splitList(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func (c *cli) appsCreate(args []string) error {
	fs := flag.NewFlagSet("apps create", flag.ContinueOnError)
	fs.SetOutput(c.err)
	mode := fs.String("mode", "proxy", "proxy, lease or both")
	services := fs.String("services", "", "service slugs the app may use, comma separated; empty for all")
	desc := fs.String("description", "", "what the app is")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: %s apps create <name> [--mode proxy|lease|both] [--services a,b] [--description d]", c.name())
	}
	body := map[string]any{"name": pos[0], "mode": *mode, "allowed_services": splitList(*services)}
	if *desc != "" {
		body["description"] = *desc
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPost, "/api/admin/apps", body, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out struct {
		App   appRow `json:"app"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Created app %s (id %d, mode %s).\nToken (shown once): %s\n", clean(out.App.Name), out.App.ID, clean(out.App.Mode), clean(out.Token))
	return nil
}

// appID finds an app by id or exact name.
func (c *cli) appID(ref string) (int64, error) {
	if ref == "" {
		return 0, usageErrorf("say which app with --app <id|name>")
	}
	var out struct {
		Apps []appRow `json:"apps"`
	}
	if err := c.call(http.MethodGet, "/api/admin/apps", nil, &out); err != nil {
		return 0, err
	}
	id, numErr := strconv.ParseInt(ref, 10, 64)
	for _, a := range out.Apps {
		if (numErr == nil && a.ID == id) || strings.EqualFold(a.Name, ref) {
			return a.ID, nil
		}
	}
	return 0, &httpError{Status: http.StatusNotFound, Code: "not_found", Message: "no app " + ref + "; " + c.name() + " apps lists them"}
}

func (c *cli) tokens(args []string) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "list") {
		return usageErrorf("usage: %s tokens list --app <id|name>, or %s tokens create --app <id|name> [--name n] [--read-only] [--expires days]", c.name(), c.name())
	}
	fs := flag.NewFlagSet("tokens "+args[0], flag.ContinueOnError)
	fs.SetOutput(c.err)
	app := fs.String("app", "", "the app: its id or name")
	name := fs.String("name", "cli", "what the token is for, such as ci or staging")
	readOnly := fs.Bool("read-only", false, "the token can only make GET calls and use read only MCP tools")
	expires := fs.String("expires", "", "days until it expires, such as 30 or 30d; empty never expires")
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageErrorf("unexpected %q; pass the app with --app", pos[0])
	}
	id, err := c.appID(*app)
	if err != nil {
		return err
	}
	if args[0] == "list" {
		return c.tokensList(id)
	}
	body := map[string]any{"name": *name, "read_only": *readOnly}
	if *expires != "" {
		days, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(*expires), "d"))
		if err != nil || days < 1 {
			return usageErrorf("--expires takes days, such as 30 or 30d")
		}
		body["expires_in_days"] = days
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPost, fmt.Sprintf("/api/admin/apps/%d/tokens", id), body, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out struct {
		Token    string `json:"token"`
		AppToken struct {
			Name      string     `json:"name"`
			ReadOnly  bool       `json:"read_only"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"app_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	extra := ""
	if out.AppToken.ReadOnly {
		extra += ", read only"
	}
	if out.AppToken.ExpiresAt != nil {
		extra += ", expires " + out.AppToken.ExpiresAt.Local().Format(time.DateOnly)
	}
	fmt.Fprintf(c.out, "Created token %s for app %d%s.\nToken (shown once): %s\n", clean(out.AppToken.Name), id, extra, clean(out.Token))
	return nil
}

func (c *cli) tokensList(id int64) error {
	var out struct {
		Tokens []struct {
			ID         int64      `json:"id"`
			Name       string     `json:"name"`
			Prefix     string     `json:"prefix"`
			ReadOnly   bool       `json:"read_only"`
			LastUsedAt *time.Time `json:"last_used_at"`
			ExpiresAt  *time.Time `json:"expires_at"`
			RevokedAt  *time.Time `json:"revoked_at"`
		} `json:"tokens"`
	}
	if done, err := c.callJSON(http.MethodGet, fmt.Sprintf("/api/admin/apps/%d/tokens", id), &out); done || err != nil {
		return err
	}
	when := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.Local().Format(time.DateOnly)
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPREFIX\tREAD ONLY\tLAST USED\tEXPIRES\tREVOKED")
	for _, t := range out.Tokens {
		fmt.Fprintf(tw, "%d\t%s\t%s...\t%v\t%s\t%s\t%s\n", t.ID, clean(t.Name), clean(t.Prefix), t.ReadOnly, when(t.LastUsedAt), when(t.ExpiresAt), when(t.RevokedAt))
	}
	return tw.Flush()
}
