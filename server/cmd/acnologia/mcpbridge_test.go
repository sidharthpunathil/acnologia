package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/mcpbridge"
)

func TestMCPOptions(t *testing.T) {
	none := mcpbridge.Filters{}
	cases := []struct {
		name     string
		token    string
		control  bool
		f        mcpbridge.Filters
		endpoint string
		err      string
	}{
		{"app token", "acn_x", false, none, "http://h/mcp", ""},
		{"admin token implies control", "acnadm_x", false, none, "http://h/mcp/control", ""},
		{"explicit control", "acnadm_x", true, mcpbridge.Filters{Groups: "keys"}, "http://h/mcp/control", ""},
		{"app token with control", "acn_x", true, none, "", "--control needs an admin token"},
		{"no token", "", false, none, "", "set an app token"},
		{"control without token", "", true, none, "", "set an admin token"},
		{"filters on the app bridge", "acn_x", false, mcpbridge.Filters{ReadOnly: true}, "", "apply to the control MCP"},
		{"bad group", "acnadm_x", false, mcpbridge.Filters{Groups: "nope"}, "", "unknown group"},
		{"bad confirm", "acnadm_x", false, mcpbridge.Filters{Confirm: "yes"}, "", "on or off"},
	}
	for _, tc := range cases {
		o, err := mcpOptions("http://h/", tc.token, tc.control, tc.f)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || o.Endpoint != tc.endpoint {
			t.Errorf("%s: %q %v", tc.name, o.Endpoint, err)
		}
	}
}

func TestMCPMain(t *testing.T) {
	var got http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(bytes.Replace(b, []byte(`"method":"ping"`), []byte(`"result":{}`), 1))
	}))
	defer srv.Close()
	t.Setenv("ACNOLOGIA_URL", "")
	t.Setenv("ACNOLOGIA_TOKEN", "")
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	pong := `{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"

	// The app bridge is unchanged: /mcp, the app token, no filter headers.
	var out, errb bytes.Buffer
	if code := mcpMain([]string{"--url", srv.URL, "--token", "acn_app"}, strings.NewReader(ping), &out, &errb); code != 0 || out.String() != pong {
		t.Fatalf("code %d out %q err %q", code, out.String(), errb.String())
	}
	if path != "/mcp" || got.Get("Authorization") != "Bearer acn_app" || got.Get("X-Acnologia-MCP-Groups") != "" {
		t.Fatalf("path %s headers %v", path, got)
	}

	out.Reset()
	code := mcpMain([]string{"--url", srv.URL, "--token", "acnadm_t", "--groups", "services", "--read-only", "--confirm", "off"},
		strings.NewReader(ping), &out, &errb)
	if code != 0 || out.String() != pong {
		t.Fatalf("code %d out %q err %q", code, out.String(), errb.String())
	}
	if path != "/mcp/control" || got.Get("X-Acnologia-MCP-Groups") != "services" || got.Get("X-Acnologia-MCP-Read-Only") != "1" ||
		got.Get("X-Acnologia-MCP-Confirm") != "off" || got.Get("Authorization") != "Bearer acnadm_t" {
		t.Fatalf("path %s headers %v", path, got)
	}

	errb.Reset()
	if code := mcpMain([]string{"--token", "acn_x", "--control"}, strings.NewReader(""), &out, &errb); code != 2 || !strings.Contains(errb.String(), "--control needs an admin token") {
		t.Fatalf("code %d err %q", code, errb.String())
	}
}
