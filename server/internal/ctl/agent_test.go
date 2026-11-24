package ctl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentFake answers the routes acn api, tool, apps and tokens use.
type agentFake struct {
	bodies map[string]string
	rpc    []map[string]any
}

func (f *agentFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer acnadm_good" {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"API token is invalid","hint":"run acn login"}}`)
		return
	}
	b, _ := io.ReadAll(r.Body)
	f.bodies[r.Method+" "+r.URL.Path] = string(b)
	switch {
	case r.URL.Path == "/api/admin/checklist":
		_, _ = io.WriteString(w, `{"steps":[{"id":"service","done":false}],"done":0,"total":4}`)
	case r.URL.Path == "/api/admin/limited":
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"slow down"}}`)
	case r.URL.Path == "/api/admin/apps" && r.Method == "GET":
		_, _ = io.WriteString(w, `{"apps":[{"id":7,"name":"ci-bot","mode":"proxy","active":true,"token_count":1,"allowed_services":["serpapi"]}]}`)
	case r.URL.Path == "/api/admin/apps" && r.Method == "POST":
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"app":{"id":8,"name":"new","mode":"proxy"},"token":"acn_newtoken"}`)
	case r.URL.Path == "/api/admin/apps/7/tokens" && r.Method == "POST":
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"token":"acn_second","app_token":{"name":"ci","read_only":true}}`)
	case r.URL.Path == "/mcp/control":
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		f.rpc = append(f.rpc, req)
		params, _ := req["params"].(map[string]any)
		switch req["method"] {
		case "tools/list":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"acnologia_list_services","title":"List services","annotations":{"readOnlyHint":true}},{"name":"acnologia_delete_key","title":"Delete a key","annotations":{"readOnlyHint":false,"destructiveHint":true}}]}}`)
		case "tools/call":
			switch params["name"] {
			case "get_service":
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"HTTP 404 not_found: not found"}],"structuredContent":{"code":"not_found","message":"not found","status":404}}}`)
			case "nope":
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"unknown tool: nope"}}`)
			default:
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}],"structuredContent":{"ok":true}}}`)
			}
		}
	default:
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"not found"}}`)
	}
}

func TestAgentCommands(t *testing.T) {
	f := &agentFake{bodies: map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	t.Setenv("ACNOLOGIA_CTL_CONFIG", filepath.Join(t.TempDir(), "ctl.json"))
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_good")
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := RunAs("acn", args, strings.NewReader(stdin), &out, &errb)
		return code, out.String(), errb.String()
	}

	// acn api: any route, with a body inline, from a file or from stdin.
	if code, out, _ := run("", "api", "GET", "/api/admin/checklist"); code != 0 || !strings.Contains(out, `"total": 4`) {
		t.Fatalf("api GET: %d %s", code, out)
	}
	if code, _, errs := run("", "api", "POST", "/api/admin/apps", "--data", `{"name":"x"}`); code != 0 || f.bodies["POST /api/admin/apps"] != `{"name":"x"}` {
		t.Fatalf("api POST inline: %d %s %q", code, errs, f.bodies["POST /api/admin/apps"])
	}
	file := filepath.Join(t.TempDir(), "body.json")
	_ = os.WriteFile(file, []byte(`{"name":"fromfile"}`), 0o600)
	if code, _, _ := run("", "api", "POST", "/api/admin/apps", "--data", "@"+file); code != 0 || !strings.Contains(f.bodies["POST /api/admin/apps"], "fromfile") {
		t.Fatalf("api POST @file: %d", code)
	}
	if code, _, _ := run(`{"name":"stdin"}`, "api", "POST", "/api/admin/apps", "--data", "@-"); code != 0 || !strings.Contains(f.bodies["POST /api/admin/apps"], "stdin") {
		t.Fatalf("api POST @-: %d", code)
	}

	// Exit codes: usage, not found, retryable, auth.
	if code, _, _ := run("", "api", "GET"); code != exitUsage {
		t.Fatalf("usage exit: %d", code)
	}
	if code, _, _ := run("", "api", "POST", "/x", "--data", "{not json"); code != exitUsage {
		t.Fatalf("bad data exit: %d", code)
	}
	if code, _, _ := run("", "nonsense"); code != exitUsage {
		t.Fatalf("unknown command exit: %d", code)
	}
	if code, _, _ := run("", "api", "GET", "/api/admin/nothing"); code != exitNotFound {
		t.Fatalf("not found exit: %d", code)
	}
	code, out, _ := run("", "api", "GET", "/api/admin/limited", "--json")
	var e struct {
		Error map[string]any `json:"error"`
	}
	if code != exitRetryable || json.Unmarshal([]byte(out), &e) != nil || e.Error["code"] != "rate_limited" || e.Error["status"] != float64(429) {
		t.Fatalf("retryable with --json: %d %s", code, out)
	}
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_bad")
	code, out, errs := run("", "apps", "--json")
	if code != exitAuth || json.Unmarshal([]byte(out), &e) != nil || e.Error["hint"] != "run acn login" || errs != "" {
		t.Fatalf("auth error with --json goes to stdout with the server's fields: %d %q %q", code, out, errs)
	}
	if code, _, errs = run("", "apps"); code != exitAuth || !strings.Contains(errs, "hint: run acn login") {
		t.Fatalf("auth error as text: %d %q", code, errs)
	}
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_good")

	// acn apps and acn tokens.
	if code, out, _ := run("", "apps"); code != 0 || !strings.Contains(out, "ci-bot") || !strings.Contains(out, "serpapi") {
		t.Fatalf("apps: %d %s", code, out)
	}
	if code, out, _ := run("", "apps", "create", "new", "--services", "serpapi,tavily"); code != 0 || !strings.Contains(out, "acn_newtoken") ||
		!strings.Contains(f.bodies["POST /api/admin/apps"], `"allowed_services":["serpapi","tavily"]`) {
		t.Fatalf("apps create: %d %s %s", code, out, f.bodies["POST /api/admin/apps"])
	}
	if code, out, _ := run("", "tokens", "create", "--app", "ci-bot", "--name", "ci", "--read-only", "--expires", "30d"); code != 0 || !strings.Contains(out, "acn_second") {
		t.Fatalf("tokens create: %d %s", code, out)
	}
	var tb map[string]any
	_ = json.Unmarshal([]byte(f.bodies["POST /api/admin/apps/7/tokens"]), &tb)
	if tb["read_only"] != true || tb["expires_in_days"] != float64(30) || tb["name"] != "ci" {
		t.Fatalf("token body: %v", tb)
	}
	if code, _, _ := run("", "tokens", "create", "--app", "ghost"); code != exitNotFound {
		t.Fatalf("unknown app: %d", code)
	}

	// acn tool: key=value as JSON when it parses, --yes confirms, every group on.
	if code, out, _ := run("", "tool", "add_keys", "service=serpapi", "keys=[\"a\",\"b\"]", "count=3", "label=hello world", "--yes"); code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("tool call: %d %s", code, out)
	}
	last := f.rpc[len(f.rpc)-1]["params"].(map[string]any)
	args := last["arguments"].(map[string]any)
	if args["service"] != "serpapi" || len(args["keys"].([]any)) != 2 || args["count"] != float64(3) || args["label"] != "hello world" || args["confirm"] != true {
		t.Fatalf("tool arguments: %v", args)
	}
	if code, out, _ := run("", "tool", "--list"); code != 0 || !strings.Contains(out, "list_services") || !strings.Contains(out, "destructive") {
		t.Fatalf("tool --list: %d %s", code, out)
	}
	code, out, _ = run("", "tool", "get_service", "service=x", "--json")
	if code != exitNotFound || json.Unmarshal([]byte(out), &e) != nil || e.Error["code"] != "not_found" {
		t.Fatalf("tool error: %d %s", code, out)
	}
	if code, _, _ := run("", "tool", "nope"); code != exitUsage {
		t.Fatalf("unknown tool: %d", code)
	}
}

func TestExitCodes(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{&httpError{Status: 401}, exitAuth},
		{&httpError{Status: 403}, exitAuth},
		{&httpError{Status: 404}, exitNotFound},
		{&httpError{Status: 503}, exitRetryable},
		{&httpError{Status: 500}, exitOther},
		{&toolError{Fields: map[string]any{"code": "rate_limited"}}, exitRetryable},
		{&toolError{Fields: map[string]any{"code": "invalid"}}, exitUsage},
		{usageErrorf("usage: x"), exitUsage},
		{io.EOF, exitOther},
	} {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%v: %d, want %d", c.err, got, c.want)
		}
	}
	if exitCode(errorsNew("not logged in; run: acn login")) != exitAuth {
		t.Error("not logged in")
	}
	if f := errorFromText("HTTP 403 forbidden: no\n{}"); f["status"] != 403 || f["code"] != "forbidden" || f["message"] != "no" {
		t.Errorf("error from text: %v", f)
	}
	if f := errorFromText("service is required"); f["code"] != "tool_error" {
		t.Errorf("plain text error: %v", f)
	}
}

type strErr string

func (e strErr) Error() string { return string(e) }

func errorsNew(s string) error { return strErr(s) }
