package ctl

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpEnv saves one signed in server at url and returns a runner whose
// binary lives at /opt/bin/acn.
func mcpEnv(t *testing.T, url string) func(stdin string, args ...string) (error, string, string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "ctl.json")
	t.Setenv("ACNOLOGIA_CTL_CONFIG", cfg)
	t.Setenv("ACNOLOGIA_URL", "")
	t.Setenv("ACNOLOGIA_TOKEN", "")
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "")
	if url != "" {
		saved := `{"current":"key.example.com","contexts":{"key.example.com":{"url":"` + url + `","token":"acnadm_saved"},` +
			`"staging":{"url":"https://staging.example.com","token":"acnadm_staging"}}}`
		if err := os.WriteFile(cfg, []byte(saved), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return func(stdin string, args ...string) (error, string, string) {
		var out, errb bytes.Buffer
		c := &cli{prog: "acn", in: strings.NewReader(stdin), out: &out, err: &errb,
			executable: func() (string, error) { return "/opt/bin/acn", nil }}
		err := c.run(args)
		return err, out.String(), errb.String()
	}
}

func TestMCPBridgesToControl(t *testing.T) {
	var got http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.Path
		if r.Header.Get("Authorization") != "Bearer acnadm_saved" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"API token is invalid"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()
	run := mcpEnv(t, srv.URL)
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"

	err, out, _ := run(ping, "mcp", "--groups", "services,keys", "--read-only", "--confirm", "off", "--exclude", "delete_service")
	if err != nil || out != `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n" {
		t.Fatalf("err %v out %q", err, out)
	}
	if path != "/mcp/control" || got.Get("Authorization") != "Bearer acnadm_saved" ||
		got.Get("X-Acnologia-MCP-Groups") != "services,keys" || got.Get("X-Acnologia-MCP-Read-Only") != "1" ||
		got.Get("X-Acnologia-MCP-Confirm") != "off" || got.Get("X-Acnologia-MCP-Exclude") != "delete_service" {
		t.Fatalf("path %s headers %v", path, got)
	}

	// A token the server refuses tells the client to sign in again.
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_revoked")
	t.Setenv("ACNOLOGIA_ACCESS_PASSWORD", "pw")
	err, out, errs := run(ping, "mcp")
	if err != nil || !strings.Contains(out, "run acn login again") || !strings.Contains(errs, "401") {
		t.Fatalf("err %v out %q stderr %q", err, out, errs)
	}
	if got.Get("X-Acnologia-Access") != "pw" {
		t.Fatalf("access password not sent: %v", got)
	}

	t.Setenv("ACNOLOGIA_TOKEN", "acn_app")
	if err, _, _ := run(ping, "mcp"); err == nil || !strings.Contains(err.Error(), "app token") {
		t.Fatalf("app token: %v", err)
	}
}

func TestMCPFlagErrors(t *testing.T) {
	run := mcpEnv(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"mcp"}, "not logged in; run acn login first"},
		{[]string{"mcp", "--groups", "nope"}, `unknown group "nope"`},
		{[]string{"mcp", "--confirm", "maybe"}, "on or off"},
		{[]string{"mcp", "extra"}, "usage: acn mcp"},
		{[]string{"mcp", "--context", "prod"}, `no saved server named "prod"`},
		{[]string{"mcp", "config", "--client", "vim"}, `unknown client "vim"`},
		{[]string{"mcp", "config", "--http"}, "not logged in"},
		{[]string{"mcp", "config", "--name", " "}, "--name is empty"},
	} {
		err, _, _ := run("", tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
	// Stdio snippets work before signing in, with a nudge.
	err, out, errs := run("", "mcp", "config")
	if err != nil || out != "claude mcp add acnologia -- /opt/bin/acn mcp\n" || !strings.Contains(errs, "Not logged in yet: run acn login") {
		t.Fatalf("err %v out %q stderr %q", err, out, errs)
	}
}

func TestMCPConfigSnippets(t *testing.T) {
	run := mcpEnv(t, "https://key.example.com")
	filters := []string{"--groups", "services,keys", "--read-only"}
	cases := []struct {
		args []string
		want string
	}{
		{nil, "claude mcp add acnologia -- /opt/bin/acn mcp\n"},
		{filters, "claude mcp add acnologia -- /opt/bin/acn mcp --groups services,keys --read-only\n"},
		{[]string{"--context", "staging", "--name", "acn staging"}, "claude mcp add 'acn staging' -- /opt/bin/acn mcp --context staging\n"},
		{append([]string{"--client", "claude-desktop"}, filters...), `{
  "mcpServers": {
    "acnologia": {
      "command": "/opt/bin/acn",
      "args": [
        "mcp",
        "--groups",
        "services,keys",
        "--read-only"
      ]
    }
  }
}
`},
		{[]string{"--client", "cursor", "--confirm", "off"}, `{
  "mcpServers": {
    "acnologia": {
      "command": "/opt/bin/acn",
      "args": [
        "mcp",
        "--confirm",
        "off"
      ]
    }
  }
}
`},
		{[]string{"--client", "json"}, `{
  "command": "/opt/bin/acn",
  "args": [
    "mcp"
  ]
}
`},
		{[]string{"--http"}, `claude mcp add --transport http acnologia https://key.example.com/mcp/control --header "Authorization: Bearer <your acnadm_ token>"` + "\n"},
		{append([]string{"--http", "--show-token"}, filters...),
			`claude mcp add --transport http acnologia 'https://key.example.com/mcp/control?groups=services,keys&read_only=1' --header "Authorization: Bearer acnadm_saved"` + "\n"},
		{append([]string{"--http", "--client", "claude-desktop"}, filters...), `{
  "mcpServers": {
    "acnologia": {
      "type": "http",
      "url": "https://key.example.com/mcp/control?groups=services,keys&read_only=1",
      "headers": {
        "Authorization": "Bearer <your acnadm_ token>"
      }
    }
  }
}
`},
		{[]string{"--http", "--client", "cursor", "--context", "staging", "--show-token"}, `{
  "mcpServers": {
    "acnologia": {
      "type": "http",
      "url": "https://staging.example.com/mcp/control",
      "headers": {
        "Authorization": "Bearer acnadm_staging"
      }
    }
  }
}
`},
		{[]string{"--http", "--client", "json", "--exclude", "delete_service,api_request"}, `{
  "type": "http",
  "url": "https://key.example.com/mcp/control?exclude=delete_service,api_request",
  "headers": {
    "Authorization": "Bearer <your acnadm_ token>"
  }
}
`},
	}
	for _, tc := range cases {
		err, out, errs := run("", append([]string{"mcp", "config"}, tc.args...)...)
		if err != nil || out != tc.want {
			t.Errorf("%v: err %v\ngot:\n%s\nwant:\n%s", tc.args, err, out, tc.want)
		}
		hidden := strings.Contains(tc.want, "<your acnadm_ token>")
		if hidden != strings.Contains(errs, "--show-token") {
			t.Errorf("%v: stderr %q", tc.args, errs)
		}
	}
}

func TestMCPConfigAsServerCtl(t *testing.T) {
	mcpEnv(t, "https://key.example.com")
	var out bytes.Buffer
	c := &cli{prog: "acnologia ctl", out: &out, err: io.Discard,
		executable: func() (string, error) { return "/Applications/Acnologia Server/acnologia", nil }}
	if err := c.run([]string{"mcp", "config"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "claude mcp add acnologia -- '/Applications/Acnologia Server/acnologia' ctl mcp\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUsageListsMCP(t *testing.T) {
	var out bytes.Buffer
	RunAs("acn", []string{"help"}, strings.NewReader(""), &out, io.Discard)
	if !strings.Contains(out.String(), "mcp config [--client claude-code|claude-desktop|cursor|json]") {
		t.Fatalf("usage: %s", out.String())
	}
}
