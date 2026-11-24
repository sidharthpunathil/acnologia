package ctl

// `acn mcp install` writes the `acn mcp` stdio entry into the agents found on
// this machine: Claude Code (through `claude mcp add` when the claude binary
// is on PATH, else in ~/.claude.json), Cursor (~/.cursor/mcp.json) and Codex
// (~/.codex/config.toml). The entry runs this binary, which reads the token
// from the saved login, so no token lands in those files. Other servers and
// settings in each file are kept, the file is backed up before it changes,
// and a second run changes nothing.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/mcpbridge"
)

// installClients are the agents install knows, in the order it sets them up.
var installClients = []string{"claude-code", "cursor", "codex"}

var clientTitle = map[string]string{"claude-code": "Claude Code", "cursor": "Cursor", "codex": "Codex"}

// serverNameRe is what install accepts as the server's name: every client
// takes it as is, and Codex as a bare TOML key.
var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (c *cli) mcpInstall(args []string) error {
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	fs.SetOutput(c.err)
	client := fs.String("client", "all", "claude-code, cursor, codex, or all (the ones found on this machine)")
	name := fs.String("name", "acnologia", "the server's name in the client")
	ctxName := fs.String("context", "", "saved server to pin (default: whichever is current when the agent starts)")
	dry := fs.Bool("dry-run", false, "print what would change and write nothing")
	var f mcpbridge.Filters
	f.Register(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("usage: %s mcp install [--client claude-code|cursor|codex|all] [--name acnologia] [--context name] [--dry-run] [filters]", c.name())
	}
	if !serverNameRe.MatchString(*name) {
		return fmt.Errorf("--name %q: use letters, digits, - and _ only", *name)
	}
	if err := f.Normalize(); err != nil {
		return err
	}
	var targets []string
	switch *client {
	case "all":
		for _, cl := range installClients {
			if c.clientFound(cl) {
				targets = append(targets, cl)
			}
		}
		if len(targets) == 0 {
			return fmt.Errorf("found no Claude Code, Cursor or Codex on this machine; pass --client claude-code, cursor or codex to set one up anyway, or print a snippet with %s mcp config", c.name())
		}
	case "claude-code", "cursor", "codex":
		targets = []string{*client}
	default:
		return fmt.Errorf("unknown client %q; use claude-code, cursor, codex or all", *client)
	}
	srv, err := c.stdioServer(*ctxName, f)
	if err != nil {
		return err
	}

	changed := false
	var errs []string
	for _, cl := range targets {
		var did bool
		var err error
		switch cl {
		case "claude-code":
			did, err = c.installClaudeCode(*name, srv, *dry)
		case "cursor":
			did, err = c.installJSON("Cursor", c.cursorConfigPath(), *name, srv, *dry)
		case "codex":
			did, err = c.installCodex(*name, srv, *dry)
		}
		if err != nil {
			errs = append(errs, clientTitle[cl]+": "+err.Error())
			fmt.Fprintf(c.err, "%s: %v\n", clientTitle[cl], err)
			continue
		}
		changed = changed || did
	}
	if changed && !*dry {
		fmt.Fprintln(c.out, "Restart your agent, or start a new session, to load the Acnologia tools.")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---- where each client keeps its config ----

func (c *cli) homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find your home folder: %w", err)
	}
	return h, nil
}

func (c *cli) lookPathFn(file string) (string, error) {
	if c.lookPath != nil {
		return c.lookPath(file)
	}
	return exec.LookPath(file)
}

func (c *cli) runCommand(name string, args ...string) ([]byte, error) {
	if c.runCmd != nil {
		return c.runCmd(name, args...)
	}
	return exec.Command(name, args...).CombinedOutput()
}

// claudeConfigPath is Claude Code's user config, where user scoped MCP
// servers live: $CLAUDE_CONFIG_DIR/.claude.json, else ~/.claude.json.
func (c *cli) claudeConfigPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	h, _ := c.homeDir()
	return filepath.Join(h, ".claude.json")
}

func (c *cli) cursorConfigPath() string {
	h, _ := c.homeDir()
	return filepath.Join(h, ".cursor", "mcp.json")
}

// codexConfigPath is $CODEX_HOME/config.toml, else ~/.codex/config.toml.
func (c *cli) codexConfigPath() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "config.toml")
	}
	h, _ := c.homeDir()
	return filepath.Join(h, ".codex", "config.toml")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// clientFound says whether an agent looks installed: its binary is on PATH
// or its config folder exists.
func (c *cli) clientFound(cl string) bool {
	h, _ := c.homeDir()
	switch cl {
	case "claude-code":
		_, err := c.lookPathFn("claude")
		return err == nil || exists(c.claudeConfigPath()) || (h != "" && exists(filepath.Join(h, ".claude")))
	case "cursor":
		_, err := c.lookPathFn("cursor")
		return err == nil || exists(filepath.Dir(c.cursorConfigPath()))
	case "codex":
		_, err := c.lookPathFn("codex")
		return err == nil || exists(filepath.Dir(c.codexConfigPath()))
	}
	return false
}

// ---- Claude Code ----

// installClaudeCode adds the server at user scope, so every project sees it.
// With the claude binary on PATH it runs `claude mcp add`, which owns its
// config file; without it, it edits that file the same way.
func (c *cli) installClaudeCode(name string, srv mcpServer, dry bool) (bool, error) {
	path := c.claudeConfigPath()
	want := srv
	want.Type = "stdio"
	cur, found, err := jsonServer(path, name)
	if err != nil {
		return false, err
	}
	if found && sameServer(cur, want) {
		fmt.Fprintf(c.out, "Claude Code: %s is already set up in %s\n", name, path)
		return false, nil
	}
	claude, lerr := c.lookPathFn("claude")
	if lerr != nil {
		return c.installJSON("Claude Code", path, name, want, dry)
	}
	add := append([]string{"mcp", "add", "--scope", "user", name, "--", srv.Command}, srv.Args...)
	var cmds [][]string
	if found {
		cmds = append(cmds, []string{"mcp", "remove", "--scope", "user", name})
	}
	cmds = append(cmds, add)
	if dry {
		for _, a := range cmds {
			fmt.Fprintf(c.out, "Claude Code: would run %s\n", shellLine("claude", a))
		}
		return true, nil
	}
	if exists(path) {
		bak, err := backup(path)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(c.out, "Claude Code: backed up %s to %s\n", path, bak)
	}
	for _, a := range cmds {
		if out, err := c.runCommand(claude, a...); err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return false, fmt.Errorf("%s failed: %s", shellLine("claude", a), clean(msg))
		}
	}
	verb := "added"
	if found {
		verb = "updated"
	}
	fmt.Fprintf(c.out, "Claude Code: %s %s with %s\n", verb, name, shellLine("claude", add))
	return true, nil
}

func shellLine(prog string, args []string) string {
	w := []string{shellQuote(prog)}
	for _, a := range args {
		w = append(w, shellQuote(a))
	}
	return strings.Join(w, " ")
}

func sameServer(a, b mcpServer) bool {
	at, bt := a.Type, b.Type
	if at == "" {
		at = "stdio"
	}
	if bt == "" {
		bt = "stdio"
	}
	if at != bt || a.Command != b.Command || a.URL != b.URL || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	return true
}

// ---- JSON configs (Claude Code without its binary, Cursor) ----

// jsonServer reads mcpServers[name] from a JSON config, if the file and
// entry exist.
func jsonServer(path, name string) (mcpServer, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(b)) == 0) {
		return mcpServer{}, false, nil
	}
	if err != nil {
		return mcpServer{}, false, err
	}
	top, err := parseObject(b)
	if err != nil {
		return mcpServer{}, false, fmt.Errorf("%s is not valid JSON (%v); fix it or add the server by hand", path, err)
	}
	raw, ok := top.get("mcpServers")
	if !ok {
		return mcpServer{}, false, nil
	}
	servers, err := parseObject(raw)
	if err != nil {
		return mcpServer{}, false, fmt.Errorf("%s: mcpServers is not an object", path)
	}
	raw, ok = servers.get(name)
	if !ok {
		return mcpServer{}, false, nil
	}
	var s mcpServer
	_ = json.Unmarshal(raw, &s)
	return s, true, nil
}

// installJSON puts srv at mcpServers[name] in a JSON config, keeping every
// other key and server in their order. Fields of an existing entry that
// install does not set (env, cwd, ...) are kept too.
func (c *cli) installJSON(title, path, name string, srv mcpServer, dry bool) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	existed := err == nil
	top := &jsonObject{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(b)) > 0 {
		if top, err = parseObject(b); err != nil {
			return false, fmt.Errorf("%s is not valid JSON (%v); fix it or add the server by hand", path, err)
		}
	}
	servers := &jsonObject{vals: map[string]json.RawMessage{}}
	if raw, ok := top.get("mcpServers"); ok && string(bytes.TrimSpace(raw)) != "null" {
		if servers, err = parseObject(raw); err != nil {
			return false, fmt.Errorf("%s: mcpServers is not an object; fix it or add the server by hand", path)
		}
	}
	entry := &jsonObject{vals: map[string]json.RawMessage{}}
	found := false
	if raw, ok := servers.get(name); ok {
		found = true
		var cur mcpServer
		_ = json.Unmarshal(raw, &cur)
		if sameServer(cur, srv) {
			fmt.Fprintf(c.out, "%s: %s is already set up in %s\n", title, name, path)
			return false, nil
		}
		if e, err := parseObject(raw); err == nil {
			entry = e
		}
		// An entry that pointed at a URL loses the URL fields.
		for _, k := range []string{"url", "headers", "type"} {
			entry.del(k)
		}
	}
	if srv.Type != "" {
		entry.set("type", mustJSON(srv.Type))
	}
	entry.set("command", mustJSON(srv.Command))
	entry.set("args", mustJSON(srv.Args))
	servers.set(name, entry.encode())
	top.set("mcpServers", servers.encode())

	var out bytes.Buffer
	if err := json.Indent(&out, top.encode(), "", "  "); err != nil {
		return false, err
	}
	out.WriteByte('\n')

	if dry {
		var e bytes.Buffer
		_ = json.Indent(&e, entry.encode(), "  ", "  ")
		fmt.Fprintf(c.out, "%s: would set mcpServers.%s in %s to\n  %s\n", title, name, path, e.String())
		return true, nil
	}
	perm := os.FileMode(0o600)
	if existed {
		if st, err := os.Stat(path); err == nil {
			perm = st.Mode().Perm()
		}
		bak, err := backup(path)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(c.out, "%s: backed up %s to %s\n", title, path, bak)
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := writeFileAtomic(path, out.Bytes(), perm); err != nil {
		return false, err
	}
	verb := "added"
	if found {
		verb = "updated"
	}
	fmt.Fprintf(c.out, "%s: %s %s in %s\n", title, verb, name, path)
	return true, nil
}

func mustJSON(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// jsonObject is a JSON object that keeps its keys in file order, with the
// values left as they were written.
type jsonObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(b []byte) (*jsonObject, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	o := &jsonObject{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := tok.(string)
		if !ok {
			return nil, errors.New("bad object key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the closing brace")
	}
	return o, nil
}

func (o *jsonObject) get(k string) (json.RawMessage, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *jsonObject) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsonObject) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *jsonObject) encode() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(mustJSON(k))
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// ---- Codex ----

// codexBlock is the [mcp_servers.<name>] table for srv.
func codexBlock(name string, srv mcpServer) string {
	args := make([]string, len(srv.Args))
	for i, a := range srv.Args {
		args[i] = tomlString(a)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n", name, tomlString(srv.Command), strings.Join(args, ", "))
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlHeader matches a table or array of tables header line and captures
// its name.
var tomlHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\[\]]+?)\s*\]\]?\s*(#.*)?$`)

// setsKey says whether a TOML line assigns key, or a dotted key under it,
// with or without quotes and spaces around the dots.
func setsKey(line, key string) bool {
	k, _, ok := strings.Cut(line, "=")
	if !ok {
		return false
	}
	k = strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(k)
	return k == key || strings.HasPrefix(k, key+".")
}

// tomlKeyLine matches `key = ...` and captures the key.
var tomlKeyLine = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)

// installCodex writes the [mcp_servers.<name>] table into Codex's
// config.toml. Codex keeps its settings in TOML, and acn keeps to the
// standard library, so the file is edited as text: the table's command and
// args lines are replaced, its other lines (env, timeouts, comments) and the
// rest of the file stay as they are.
func (c *cli) installCodex(name string, srv mcpServer, dry bool) (bool, error) {
	path := c.codexConfigPath()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	existed := err == nil
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	header := "mcp_servers." + name
	start, end := -1, len(lines)
	table := ""
	for i, l := range lines {
		m := tomlHeader.FindStringSubmatch(l)
		if m == nil {
			// The same server written another way (a dotted key or an
			// inline table) is left for a person to merge.
			if (table == "" && setsKey(l, header)) || (table == "mcp_servers" && setsKey(l, name)) {
				return false, fmt.Errorf("%s sets %s in a form this command does not edit; change it by hand to:\n%s", path, header, codexBlock(name, srv))
			}
			continue
		}
		table = strings.ReplaceAll(strings.ReplaceAll(m[1], " ", ""), `"`, "")
		if start >= 0 && end == len(lines) {
			end = i
		}
		if table == header && start < 0 {
			start = i
		}
	}
	block := strings.Split(strings.TrimSuffix(codexBlock(name, srv), "\n"), "\n")

	var next []string
	found := start >= 0
	if found {
		body := lines[start+1 : end]
		var keep []string
		var cmdLine, argsLine string
		skipArgs := 0
		for _, l := range body {
			if skipArgs > 0 {
				argsLine += "\n" + l
				skipArgs += bracketDepth(l)
				continue
			}
			m := tomlKeyLine.FindStringSubmatch(l)
			switch {
			case m != nil && m[1] == "command":
				cmdLine = l
			case m != nil && m[1] == "args":
				argsLine = l
				skipArgs = bracketDepth(l)
			default:
				keep = append(keep, l)
			}
		}
		if strings.TrimSpace(cmdLine) == block[1] && normalizeArgs(argsLine) == normalizeArgs(block[2]) {
			fmt.Fprintf(c.out, "Codex: %s is already set up in %s\n", name, path)
			return false, nil
		}
		next = append(next, lines[:start]...)
		next = append(next, block...)
		next = append(next, keep...)
		next = append(next, lines[end:]...)
	} else {
		next = append(next, lines...)
		if len(next) > 0 && strings.TrimSpace(next[len(next)-1]) != "" {
			next = append(next, "")
		}
		next = append(next, block...)
	}
	out := strings.Join(next, "\n") + "\n"
	if strings.Contains(string(b), "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}

	if dry {
		fmt.Fprintf(c.out, "Codex: would write to %s\n  %s\n", path, strings.Join(block, "\n  "))
		return true, nil
	}
	perm := os.FileMode(0o600)
	if existed {
		if st, err := os.Stat(path); err == nil {
			perm = st.Mode().Perm()
		}
		bak, err := backup(path)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(c.out, "Codex: backed up %s to %s\n", path, bak)
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := writeFileAtomic(path, []byte(out), perm); err != nil {
		return false, err
	}
	verb := "added"
	if found {
		verb = "updated"
	}
	fmt.Fprintf(c.out, "Codex: %s %s in %s\n", verb, name, path)
	return true, nil
}

// bracketDepth is the change in [ ] nesting over a line, ignoring brackets
// inside strings and comments.
func bracketDepth(l string) int {
	d := 0
	var quote rune
	esc := false
	for _, r := range l {
		switch {
		case esc:
			esc = false
		case quote != 0:
			if r == '\\' && quote == '"' {
				esc = true
			} else if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return d
		case r == '[':
			d++
		case r == ']':
			d--
		}
	}
	return d
}

// normalizeArgs drops whitespace outside strings so a reformatted args
// array still compares equal.
func normalizeArgs(s string) string {
	var b strings.Builder
	var quote rune
	esc, comment := false, false
	for _, r := range s {
		switch {
		case comment:
			comment = r != '\n'
		case esc:
			esc = false
			b.WriteRune(r)
		case quote != 0:
			if r == '\\' && quote == '"' {
				esc = true
			} else if r == quote {
				quote = 0
			}
			b.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			b.WriteRune(r)
		case r == '#':
			comment = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if strings.HasSuffix(out, ",]") {
		out = strings.TrimSuffix(out, ",]") + "]"
	}
	return out
}

// backup copies path next to itself with a time stamp and returns the copy's
// name. The copy keeps the file's permissions.
func backup(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return "", err
	}
	dst := path + ".bak-" + time.Now().Format("20060102-150405")
	for i := 2; exists(dst); i++ {
		dst = fmt.Sprintf("%s.bak-%s-%d", path, time.Now().Format("20060102-150405"), i)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return "", err
	}
	return dst, out.Close()
}
