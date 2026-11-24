package ctl

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// installEnv gives each test its own home folder and a signed in server, and
// returns a runner. claude says whether the claude binary is "on PATH"; the
// commands it would run are recorded in ran.
type installEnv struct {
	home   string
	ran    [][]string
	claude bool
}

func newInstallEnv(t *testing.T) *installEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	mcpEnv(t, "https://key.example.com")
	return &installEnv{home: home}
}

func (e *installEnv) run(args ...string) (error, string, string) {
	var out, errb bytes.Buffer
	c := &cli{prog: "acn", in: strings.NewReader(""), out: &out, err: &errb,
		executable: func() (string, error) { return "/opt/bin/acn", nil },
		lookPath: func(f string) (string, error) {
			if f == "claude" && e.claude {
				return "/usr/local/bin/claude", nil
			}
			return "", errors.New("not found")
		},
		runCmd: func(name string, args ...string) ([]byte, error) {
			e.ran = append(e.ran, append([]string{name}, args...))
			return nil, nil
		}}
	err := c.run(append([]string{"mcp", "install"}, args...))
	return err, out.String(), errb.String()
}

func (e *installEnv) path(p ...string) string {
	return filepath.Join(append([]string{e.home}, p...)...)
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func backups(t *testing.T, p string) []string {
	t.Helper()
	m, _ := filepath.Glob(p + ".bak-*")
	return m
}

func TestMCPInstallNothingFound(t *testing.T) {
	e := newInstallEnv(t)
	err, _, _ := e.run()
	if err == nil || !strings.Contains(err.Error(), "found no Claude Code, Cursor or Codex") {
		t.Fatalf("err %v", err)
	}
	for _, args := range [][]string{{"--client", "vim"}, {"--name", "a b"}, {"extra"}} {
		if err, _, _ := e.run(args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestMCPInstallFreshFiles(t *testing.T) {
	e := newInstallEnv(t)
	for _, d := range []string{".claude", ".cursor", ".codex"} {
		if err := os.MkdirAll(e.path(d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	err, out, _ := e.run("--read-only")
	if err != nil {
		t.Fatalf("err %v out %s", err, out)
	}
	wantJSON := func(typ string) string {
		t := ""
		if typ != "" {
			t = "\n      \"type\": \"stdio\","
		}
		return `{
  "mcpServers": {
    "acnologia": {` + t + `
      "command": "/opt/bin/acn",
      "args": [
        "mcp",
        "--read-only"
      ]
    }
  }
}
`
	}
	if got := read(t, e.path(".claude.json")); got != wantJSON("stdio") {
		t.Errorf("claude:\n%s", got)
	}
	if got := read(t, e.path(".cursor", "mcp.json")); got != wantJSON("") {
		t.Errorf("cursor:\n%s", got)
	}
	if got := read(t, e.path(".codex", "config.toml")); got != "[mcp_servers.acnologia]\ncommand = \"/opt/bin/acn\"\nargs = [\"mcp\", \"--read-only\"]\n" {
		t.Errorf("codex:\n%s", got)
	}
	for _, s := range []string{"Claude Code: added", "Cursor: added", "Codex: added", "Restart your agent"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	// No token in any file.
	for _, p := range []string{".claude.json", ".cursor/mcp.json", ".codex/config.toml"} {
		if strings.Contains(read(t, e.path(p)), "acnadm_") {
			t.Errorf("%s holds the token", p)
		}
	}

	// A second run changes nothing and backs nothing up.
	err, out, _ = e.run("--read-only")
	if err != nil || strings.Count(out, "already set up") != 3 || strings.Contains(out, "Restart") {
		t.Fatalf("rerun: err %v out %s", err, out)
	}
	if b := backups(t, e.path(".claude.json")); len(b) != 0 {
		t.Fatalf("backups %v", b)
	}
}

func TestMCPInstallMergesJSON(t *testing.T) {
	e := newInstallEnv(t)
	p := e.path(".cursor", "mcp.json")
	orig := `{"zeta": 1, "mcpServers": {"other": {"command": "x", "args": ["<&>"]}, "acnologia": {"command": "/old/acn", "args": ["mcp"], "env": {"A": "1"}}}, "alpha": [true]}`
	write(t, p, orig)

	// Dry run shows the entry and writes nothing.
	err, out, _ := e.run("--client", "cursor", "--dry-run")
	if err != nil || !strings.Contains(out, "would set mcpServers.acnologia") || !strings.Contains(out, `"/opt/bin/acn"`) {
		t.Fatalf("dry: err %v out %s", err, out)
	}
	if read(t, p) != orig || len(backups(t, p)) != 0 {
		t.Fatal("dry run wrote")
	}

	err, out, _ = e.run("--client", "cursor")
	if err != nil || !strings.Contains(out, "Cursor: updated acnologia") {
		t.Fatalf("err %v out %s", err, out)
	}
	want := `{
  "zeta": 1,
  "mcpServers": {
    "other": {
      "command": "x",
      "args": [
        "<&>"
      ]
    },
    "acnologia": {
      "command": "/opt/bin/acn",
      "args": [
        "mcp"
      ],
      "env": {
        "A": "1"
      }
    }
  },
  "alpha": [
    true
  ]
}
`
	if got := read(t, p); got != want {
		t.Fatalf("got:\n%s", got)
	}
	b := backups(t, p)
	if len(b) != 1 || read(t, b[0]) != orig {
		t.Fatalf("backup %v", b)
	}

	// A file that is not JSON is left alone.
	write(t, e.path(".claude.json"), "{nope")
	err, _, _ = e.run("--client", "claude-code")
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") || read(t, e.path(".claude.json")) != "{nope" {
		t.Fatalf("bad json: %v", err)
	}
}

func TestMCPInstallClaudeBinary(t *testing.T) {
	e := newInstallEnv(t)
	e.claude = true
	err, out, _ := e.run("--client", "claude-code", "--context", "staging")
	want := []string{"/usr/local/bin/claude", "mcp", "add", "--scope", "user", "acnologia", "--", "/opt/bin/acn", "mcp", "--context", "staging"}
	if err != nil || len(e.ran) != 1 || !reflect.DeepEqual(e.ran[0], want) {
		t.Fatalf("err %v ran %v out %s", err, e.ran, out)
	}

	// An entry that differs is removed first; a matching one is left.
	e.ran = nil
	write(t, e.path(".claude.json"), `{"mcpServers":{"acnologia":{"type":"stdio","command":"/old/acn","args":["mcp"]}}}`)
	if err, _, _ := e.run("--client", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if len(e.ran) != 2 || e.ran[0][2] != "remove" || e.ran[1][2] != "add" || len(backups(t, e.path(".claude.json"))) != 1 {
		t.Fatalf("ran %v", e.ran)
	}
	e.ran = nil
	write(t, e.path(".claude.json"), `{"mcpServers":{"acnologia":{"type":"stdio","command":"/opt/bin/acn","args":["mcp"],"env":{}}}}`)
	if err, out, _ := e.run("--client", "claude-code"); err != nil || len(e.ran) != 0 || !strings.Contains(out, "already set up") {
		t.Fatalf("err %v ran %v out %s", err, e.ran, out)
	}

	// Dry run prints the command.
	e.ran = nil
	write(t, e.path(".claude.json"), `{}`)
	err, out, _ = e.run("--client", "claude-code", "--dry-run")
	if err != nil || len(e.ran) != 0 || !strings.Contains(out, "would run claude mcp add --scope user acnologia -- /opt/bin/acn mcp") {
		t.Fatalf("err %v ran %v out %s", err, e.ran, out)
	}
}

func TestMCPInstallCodexMerge(t *testing.T) {
	e := newInstallEnv(t)
	p := e.path(".codex", "config.toml")
	orig := `model = "o3"

[mcp_servers.other]
command = "npx"
args = ["-y", "thing"]

[mcp_servers.acnologia]
command = "/old/acn"
args = [
  "mcp", # the bridge
]
startup_timeout_sec = 20

[mcp_servers.acnologia.env]
X = "1"

[[profiles]]
name = "p"
`
	write(t, p, orig)
	err, out, _ := e.run("--client", "codex")
	if err != nil || !strings.Contains(out, "Codex: updated acnologia") {
		t.Fatalf("err %v out %s", err, out)
	}
	want := `model = "o3"

[mcp_servers.other]
command = "npx"
args = ["-y", "thing"]

[mcp_servers.acnologia]
command = "/opt/bin/acn"
args = ["mcp"]
startup_timeout_sec = 20

[mcp_servers.acnologia.env]
X = "1"

[[profiles]]
name = "p"
`
	if got := read(t, p); got != want {
		t.Fatalf("got:\n%s", got)
	}
	if b := backups(t, p); len(b) != 1 || read(t, b[0]) != orig {
		t.Fatalf("backup %v", b)
	}
	if err, out, _ := e.run("--client", "codex"); err != nil || !strings.Contains(out, "already set up") {
		t.Fatalf("rerun: %v %s", err, out)
	}

	// A hand written multi line array that says the same is already set up.
	write(t, p, "[mcp_servers.acnologia]\ncommand = \"/opt/bin/acn\"\nargs = [\n  \"mcp\",\n]\n")
	if err, out, _ := e.run("--client", "codex"); err != nil || !strings.Contains(out, "already set up") {
		t.Fatalf("multiline: %v %s", err, out)
	}

	// The server set as a dotted key is left for a person.
	write(t, p, "mcp_servers.acnologia.command = \"x\"\n")
	if err, _, _ := e.run("--client", "codex"); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("dotted: %v", err)
	}
	write(t, p, "[mcp_servers]\nacnologia = { command = \"x\" }\n")
	if err, _, _ := e.run("--client", "codex"); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("inline: %v", err)
	}

	// CODEX_HOME moves the file.
	alt := t.TempDir()
	t.Setenv("CODEX_HOME", alt)
	if err, _, _ := e.run("--client", "codex"); err != nil || !strings.Contains(read(t, filepath.Join(alt, "config.toml")), "[mcp_servers.acnologia]") {
		t.Fatalf("CODEX_HOME: %v", err)
	}
}

func TestMCPConfigCodex(t *testing.T) {
	run := mcpEnv(t, "https://key.example.com")
	err, out, _ := run("", "mcp", "config", "--client", "codex", "--groups", "services")
	if err != nil || out != "[mcp_servers.acnologia]\ncommand = \"/opt/bin/acn\"\nargs = [\"mcp\", \"--groups\", \"services\"]\n" {
		t.Fatalf("err %v out %q", err, out)
	}
	if err, _, _ := run("", "mcp", "config", "--client", "codex", "--http"); err == nil {
		t.Fatal("codex --http accepted")
	}
	if got := tomlString(`C:\Users\me\acn.exe`); got != `"C:\\Users\\me\\acn.exe"` {
		t.Fatalf("toml %s", got)
	}
	var v any
	if json.Unmarshal(mustJSON("<&>"), &v); v != "<&>" {
		t.Fatal(v)
	}
}
