package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/docsmd"
)

// codePatterns find the error codes the package writes: writeErr calls,
// apiError and authFail literals, the invite helpers' fail, and map
// literals.
var codePatterns = []*regexp.Regexp{
	regexp.MustCompile(`writeErr\(\s*\w+,\s*[^,]+,\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`Code:\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`fail\(\d+,\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`errCode\s*(?::=|=\s*\d+,)\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`"code":\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`toolErrf\(\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`toolErr\{code:\s*"([a-z_0-9]+)"`),
	regexp.MustCompile(`code\s*(?::=|=)\s*"([a-z_0-9]+)"`),
}

// indirectCodes are written through a helper the patterns cannot follow.
var indirectCodes = []string{"no_revealable_keys", "all_keys_busy", "invalid_arguments", "unknown_tool", "tool_error"}

// oauthCodes follow the OAuth spec's own error shape, not the envelope.
var oauthCodes = map[string]bool{
	"invalid_request": true, "invalid_target": true, "temporarily_unavailable": true, "unsupported_response_type": true,
	"invalid_client_metadata": true, "invalid_redirect_uri": true, "invalid_grant": true, "invalid_client": true,
	"unsupported_grant_type": true, "invalid_scope": true, "access_denied": false,
}

// sourceCodes are the codes the package's source writes.
func sourceCodes(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "oauth") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range codePatterns {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				if _, ok := found[m[1]]; !ok {
					found[m[1]] = f
				}
			}
		}
	}
	for _, c := range indirectCodes {
		found[c] = "indirect"
	}
	return found
}

func TestErrorRegistryCoversEveryCode(t *testing.T) {
	codes := sourceCodes(t)
	if len(codes) < 100 {
		t.Fatalf("found only %d codes: the patterns no longer match the source", len(codes))
	}
	for code, file := range codes {
		if oauthCodes[code] {
			continue
		}
		if _, ok := errRegistry[code]; !ok {
			t.Errorf("%s writes error code %q, which has no entry in errRegistry (errors.go)", file, code)
		}
	}
	areas := map[string]bool{}
	for _, a := range errAreas {
		areas[a] = true
	}
	for code, h := range errRegistry {
		if h.Hint == "" || h.Meaning == "" || !areas[h.Area] {
			t.Errorf("%s: needs a meaning, a hint and a known area", code)
		}
		for _, s := range []string{h.Hint, h.Meaning, h.Fix.Tool, h.Fix.Command, h.Fix.URL} {
			if strings.ContainsRune(s, '—') {
				t.Errorf("%s: no em dashes", code)
			}
		}
		if h.Fix.Tool != "" && !strings.HasPrefix(h.Fix.Tool, toolPrefix) {
			t.Errorf("%s: fix.tool %q should be the public tool name", code, h.Fix.Tool)
		}
	}
}

// renderErrorsDoc is docs/errors.md, built from the registry.
func renderErrorsDoc() string {
	var b strings.Builder
	b.WriteString(`# Error codes

Every error code the Acnologia API answers with: what it means, whether trying again can help, and what to do next. The hint in an error is the "What to do" line of its code here.

## The error envelope

Every JSON error the API returns has this shape, with a matching HTTP status:

` + "```json" + `
{
  "error": {
    "code": "no_key_available",
    "message": "every key for this service is cooling down, exhausted or disabled",
    "hint": "Wait retry_after_sec, then retry. ...",
    "fix": {"tool": "acnologia_pool_status"},
    "doc_url": "/docs/errors.md#no_key_available",
    "request_id": "req_3kTq9wZx1bQf",
    "retry_after_sec": 30
  }
}
` + "```" + `

- ` + "`code`" + ` and ` + "`message`" + ` are as they always were. Branch on ` + "`code`" + `; the message is for people and may change.
- ` + "`hint`" + ` says what to do next.
- ` + "`fix`" + ` names the next step when there is one: an MCP ` + "`tool`" + ` to call, a CLI ` + "`command`" + ` to run, or a ` + "`url`" + ` (a path on the server) to call or read. Each part is optional, and ` + "`fix`" + ` is left out when there is none.
- ` + "`doc_url`" + ` points at the code's entry on this page, or at the doc that covers it.
- ` + "`request_id`" + ` is also sent as the ` + "`X-Request-Id`" + ` header. Quote it to the operator: server errors log it.
- ` + "`retry_after_sec`" + ` is there when the response carries a ` + "`Retry-After`" + ` header. Some answers (` + "`no_key_available`" + `, ` + "`all_keys_busy`" + `, ` + "`service_unavailable`" + `, ` + "`too_many_attempts`" + `) also repeat it at the top level, next to ` + "`error`" + `, as they did before.

Fields may be added later; ignore the ones you do not know.

## MCP tool errors

A tool call on ` + "`/mcp`" + ` that fails returns a result with ` + "`isError: true`" + `. Its text is the message followed by the hint, for clients that read only text. The same facts come as an object, ` + "`{code, message, hint, fix, doc_url, retry_after_sec}`" + `, under ` + "`_meta[\"acnologia/error\"]`" + `. When the tool declares no output schema with required fields (` + "`acnologia_call_tool`" + `, ` + "`acnologia_search_tools`" + ` and upstream MCP tools), the object is also the result's ` + "`structuredContent`" + `. Tools with a stricter schema leave it out there, because clients check ` + "`structuredContent`" + ` against the schema even on an error. The codes are the ones below; ` + "`invalid_arguments`" + `, ` + "`unknown_tool`" + ` and ` + "`tool_error`" + ` occur only on MCP.

When a call is refused and the reason is unclear, call ` + "`acnologia_whoami`" + ` (or ` + "`GET /api/v1/me`" + `): it shows the token, the app's mode, tool sets, limits with what is left, and the services it may use.

`)
	byArea := map[string][]string{}
	for code, h := range errRegistry {
		byArea[h.Area] = append(byArea[h.Area], code)
	}
	for _, area := range errAreas {
		codes := byArea[area]
		sort.Strings(codes)
		b.WriteString("## " + area + "\n\n")
		for _, code := range codes {
			h := errRegistry[code]
			b.WriteString("### `" + code + "`\n\n")
			status := "MCP tool error"
			if h.Status != 0 {
				status = "HTTP " + strconv.Itoa(h.Status)
			}
			retry := "Retry: no, not without a change."
			if h.Retry {
				retry = "Retry: yes, after `retry_after_sec` when the error gives one."
			}
			b.WriteString(status + ". " + retry + "\n\n")
			b.WriteString(h.Meaning + "\n\n")
			b.WriteString("What to do: " + h.Hint + "\n")
			var next []string
			if h.Fix.Tool != "" {
				next = append(next, "tool `"+h.Fix.Tool+"`")
			}
			if h.Fix.Command != "" {
				next = append(next, "command `"+h.Fix.Command+"`")
			}
			if h.Fix.URL != "" {
				next = append(next, "`"+h.Fix.URL+"`")
			}
			if len(next) > 0 {
				b.WriteString("\nNext step: " + strings.Join(next, ", ") + ".\n")
			}
			if h.Doc != "" {
				b.WriteString("\nMore in [" + h.Doc + "](" + h.Doc + ").\n")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// TestErrorsDocMatchesRegistry keeps docs/errors.md and its embedded copy
// in step with the registry. ACNOLOGIA_UPDATE_ERRORS_DOC=1 rewrites both.
func TestErrorsDocMatchesRegistry(t *testing.T) {
	want := renderErrorsDoc()
	paths := []string{filepath.Join("..", "..", "..", "docs", "errors.md"), filepath.Join("..", "docsmd", "files", "errors.md")}
	if os.Getenv("ACNOLOGIA_UPDATE_ERRORS_DOC") == "1" {
		for _, p := range paths {
			if err := os.WriteFile(p, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range paths {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s is out of date with the error registry: run ACNOLOGIA_UPDATE_ERRORS_DOC=1 go test ./internal/api -run TestErrorsDocMatchesRegistry", p)
		}
	}
	if strings.ContainsRune(want, '—') {
		t.Error("errors.md: no em dashes")
	}
	// Every code's doc_url anchor exists on the page.
	for code, h := range errRegistry {
		if h.Doc == "" && !strings.Contains(want, "### `"+code+"`") {
			t.Errorf("%s: no heading for its anchor", code)
		}
	}
}

type envelope struct {
	Error struct {
		Code          string            `json:"code"`
		Message       string            `json:"message"`
		Hint          string            `json:"hint"`
		Fix           map[string]string `json:"fix"`
		DocURL        string            `json:"doc_url"`
		RequestID     string            `json:"request_id"`
		RetryAfterSec *int              `json:"retry_after_sec"`
	} `json:"error"`
	RetryAfterSec int `json:"retry_after_sec"`
}

func getEnvelope(t *testing.T, e *env, method, path string, body any, want int, hdr ...string) (envelope, *http.Response) {
	t.Helper()
	var out envelope
	resp := e.do(http.DefaultClient, method, path, body, want, &out, hdr...)
	return out, resp
}

func TestErrorEnvelope(t *testing.T) {
	e := newEnv(t)
	// No token: the original code and message, plus the new fields.
	out, resp := getEnvelope(t, e, "GET", "/api/v1/services", nil, 401)
	if out.Error.Code != "unauthenticated" || !strings.Contains(out.Error.Message, "missing app token") {
		t.Fatalf("code and message changed: %+v", out.Error)
	}
	if out.Error.Hint == "" || out.Error.DocURL != "/docs/errors.md#unauthenticated" || out.Error.Fix["url"] != "/api/v1/me" {
		t.Fatalf("no help: %+v", out.Error)
	}
	if !strings.HasPrefix(out.Error.RequestID, "req_") || resp.Header.Get("X-Request-Id") != out.Error.RequestID {
		t.Fatalf("request id %q, header %q", out.Error.RequestID, resp.Header.Get("X-Request-Id"))
	}
	if out.Error.RetryAfterSec != nil {
		t.Fatalf("retry_after_sec without a Retry-After header: %d", *out.Error.RetryAfterSec)
	}

	// A Retry-After header becomes retry_after_sec, and the top level copy stays.
	e.up.bad["k1"] = "Your searches for the past hour have exceeded the limit."
	e.addKeys(map[string]any{"secret": "k1"})
	appID, tok := e.app(map[string]any{"name": "site", "allowed_services": []string{"serpapi"}})
	auth := []string{"Authorization", "Bearer " + tok}
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json", nil, 429, nil, auth...)
	out, resp = getEnvelope(t, e, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 503, auth...)
	if out.Error.Code != "no_key_available" || out.Error.RetryAfterSec == nil || *out.Error.RetryAfterSec <= 0 ||
		strconv.Itoa(*out.Error.RetryAfterSec) != resp.Header.Get("Retry-After") || out.RetryAfterSec != *out.Error.RetryAfterSec {
		t.Fatalf("retry: %+v top %d header %q", out.Error, out.RetryAfterSec, resp.Header.Get("Retry-After"))
	}
	if out.Error.Fix["tool"] != "acnologia_pool_status" {
		t.Fatalf("fix: %+v", out.Error.Fix)
	}

	// unknown_service suggests close slugs the app may use; service_not_allowed
	// lists them.
	out, _ = getEnvelope(t, e, "GET", "/api/v1/services/serpap/guide", nil, 403, auth...)
	if out.Error.Code != "service_not_allowed" || !strings.Contains(out.Error.Message, "it may use: serpapi") {
		t.Fatalf("not allowed: %+v", out.Error)
	}
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"allowed_services": []string{}}, 200, nil)
	out, _ = getEnvelope(t, e, "GET", "/api/v1/services/serpap/guide", nil, 404, auth...)
	if out.Error.Code != "unknown_service" || !strings.Contains(out.Error.Message, "did you mean serpapi?") {
		t.Fatalf("unknown service: %+v", out.Error)
	}

	// forbidden names the role needed and the caller's.
	var tk struct {
		Token string `json:"token"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "ci", "scope": "read"}, 201, &tk)
	out, _ = getEnvelope(t, e, "POST", "/api/admin/services", map[string]any{"provider": "generic", "slug": "x", "base_url": "https://x.example"}, 403, "Authorization", "Bearer "+tk.Token)
	if out.Error.Code != "forbidden" || !strings.Contains(out.Error.Message, "needs the admin role") || !strings.Contains(out.Error.Message, "scope read") {
		t.Fatalf("forbidden: %+v", out.Error)
	}

	// An unknown code still gets a request id and a doc link.
	h, ok := errHelpFor("not_a_real_code")
	if ok || h.Hint != "" {
		t.Fatal("made up code found")
	}
	b, _ := json.Marshal(apiError{Code: "not_a_real_code", Message: "x"})
	if !strings.Contains(string(b), `"doc_url":"/docs/errors.md"`) {
		t.Fatalf("unknown code: %s", b)
	}
}

func TestCloseSlugs(t *testing.T) {
	slugs := []string{"serpapi", "brave-search", "elevenlabs", "openai"}
	for _, c := range []struct {
		in   string
		want string
	}{{"serpap", "serpapi"}, {"brave", "brave-search"}, {"elevenlab", "elevenlabs"}, {"open-ai", "openai"}, {"zzzzzz", ""}} {
		got := strings.Join(closeSlugs(c.in, slugs, 3), ",")
		if got != c.want {
			t.Errorf("closeSlugs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMCPToolErrorsAreStructured(t *testing.T) {
	e := newEnv(t)
	_, tok := e.app(map[string]any{"name": "agent", "mode": "both"})
	m := &mcpClient{e: e, tok: tok}
	raw := func(name string, args map[string]any) map[string]any {
		res := m.call("tools/call", map[string]any{"name": name, "arguments": args})
		r, _ := res["result"].(map[string]any)
		if r == nil || r["isError"] != true {
			t.Fatalf("%s: want an isError result, got %v", name, res)
		}
		return r
	}
	// proxy_request declares a schema with required fields: the error is in
	// _meta and the text, not in structuredContent.
	r := raw("acnologia_proxy_request", map[string]any{"service": "serpap", "path": "/x"})
	meta := r["_meta"].(map[string]any)["acnologia/error"].(map[string]any)
	if meta["code"] != "unknown_service" || meta["hint"] == "" || meta["doc_url"] != "/docs/errors.md#unknown_service" {
		t.Fatalf("meta %v", meta)
	}
	if !strings.Contains(meta["message"].(string), "did you mean serpapi?") {
		t.Fatalf("message %v", meta["message"])
	}
	if _, ok := r["structuredContent"]; ok {
		t.Fatal("structuredContent would fail proxy_request's output schema")
	}
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "no service named serpap") || !strings.Contains(text, "Hint (unknown_service): ") {
		t.Fatalf("text %q", text)
	}
	// call_tool declares no schema, so the error object is its structuredContent.
	r = raw("acnologia_call_tool", map[string]any{"name": "nope_nothing_here", "arguments": map[string]any{}})
	sc := r["structuredContent"].(map[string]any)
	if sc["code"] != "unknown_tool" || sc["message"] == "" {
		t.Fatalf("structuredContent %v", sc)
	}
	// A busy pool carries retry_after_sec.
	e.up.bad["k1"] = "Your searches for the past hour have exceeded the limit."
	e.addKeys(map[string]any{"secret": "k1"})
	e.do(http.DefaultClient, "GET", "/proxy/serpapi/search.json", nil, 429, nil, "Authorization", "Bearer "+tok)
	r = raw("acnologia_get_key", map[string]any{"service": "serpapi"})
	meta = r["_meta"].(map[string]any)["acnologia/error"].(map[string]any)
	if meta["code"] != "no_key_available" || meta["retry_after_sec"] == nil {
		t.Fatalf("no key: %v", meta)
	}
}

func TestMeAndWhoami(t *testing.T) {
	e := newEnv(t)
	appID, tok := e.app(map[string]any{"name": "agent", "mode": "both"})
	e.do(e.super, "PATCH", "/api/admin/apps/"+itoa(int(appID)), map[string]any{"limits": map[string]any{"daily_requests": 10}}, 200, nil)
	var me map[string]any
	e.do(http.DefaultClient, "GET", "/api/v1/me", nil, 200, &me, "Authorization", "Bearer "+tok)
	tk := me["token"].(map[string]any)
	if tk["kind"] != "app" || tk["id"] == nil || tk["name"] == "" || tk["read_only"] != false {
		t.Fatalf("token %v", tk)
	}
	if _, ok := tk["expires_at"]; !ok {
		t.Fatalf("token has no expires_at: %v", tk)
	}
	if me["mode"] != "both" || me["app"].(map[string]any)["mode"] != "both" {
		t.Fatalf("mode %v", me)
	}
	mcp := me["mcp"].(map[string]any)
	if !strings.Contains(strings.Join(toStrings(mcp["tool_sets"]), ","), "core") {
		t.Fatalf("mcp %v", mcp)
	}
	left := me["remaining"].(map[string]any)
	if left["daily_requests"] != float64(10) || left["monthly_budget"] != nil {
		t.Fatalf("remaining %v", left)
	}
	for _, s := range me["services"].([]any) {
		if s.(map[string]any)["logo_url"] != nil {
			t.Fatal("logo inlined without logos=1")
		}
	}

	m := &mcpClient{e: e, tok: tok}
	if !has(m.toolNames(), "whoami") {
		t.Fatal("whoami is not listed")
	}
	who, isErr := m.tool("acnologia_whoami", nil)
	if isErr {
		t.Fatalf("whoami: %v", who)
	}
	if who["token"].(map[string]any)["kind"] != "app" || who["mode"] != "both" || who["remaining"].(map[string]any)["daily_requests"] != float64(10) {
		t.Fatalf("whoami %v", who)
	}
}

func TestAgentIndex(t *testing.T) {
	e := newEnv(t)
	var idx map[string]any
	e.do(http.DefaultClient, "GET", "/api/v1", nil, 200, &idx)
	eps := idx["endpoints"].(map[string]any)
	for _, k := range []string{"mcp", "mcp_control", "oauth_metadata", "llms", "llms_full", "docs", "errors_doc", "openapi", "device_login"} {
		if s, _ := eps[k].(string); !strings.HasPrefix(s, e.srv.URL+"/") {
			t.Errorf("endpoint %s = %v", k, eps[k])
		}
	}
	if idx["gated"] != false || idx["setup_required"] != false || idx["version"] != "test" || len(idx["start_here"].([]any)) == 0 {
		t.Fatalf("index %v", idx)
	}
	var wk map[string]any
	e.do(http.DefaultClient, "GET", "/.well-known/acnologia", nil, 200, &wk)
	if wk["id"] == nil || wk["public_key"] == nil || wk["agent"].(map[string]any)["endpoints"] == nil {
		t.Fatalf("well-known %v", wk)
	}

	// Behind the gate the index stays open, without saying whether the
	// server is set up.
	g := newGateEnv(t)
	idx = nil
	g.do(http.DefaultClient, "GET", "/api/v1", nil, 200, &idx)
	if idx["gated"] != true || idx["setup_required"] != nil || idx["auth"].(map[string]any)["access_header"] != accessHeader {
		t.Fatalf("gated index %v", idx)
	}
	g.do(http.DefaultClient, "GET", "/.well-known/acnologia", nil, 200, nil)
}

func TestDocsThroughTheGate(t *testing.T) {
	docs := []string{"/llms.txt", "/llms-full.txt", "/docs/api.md", "/docs/errors.md", "/api/docs"}
	// Open server: the docs need nothing.
	e := newEnv(t)
	for _, p := range docs {
		e.do(http.DefaultClient, "GET", p, nil, 200, nil)
	}

	// Tokens are made first, then the gate goes on (as in newGateEnv).
	g := newEnv(t)
	appID, tok := g.app(map[string]any{"name": "agent"})
	var tk struct {
		Token string `json:"token"`
	}
	g.do(g.super, "POST", "/api/admin/tokens", map[string]any{"name": "ci", "scope": "read"}, 201, &tk)
	g.api.setAccessPassword(testAccessPassword)
	srv := httptest.NewServer(g.api.Router())
	t.Cleanup(srv.Close)
	g.srv = srv
	for _, p := range docs {
		out, _ := getEnvelope(t, g, "GET", p, nil, 401)
		if out.Error.Code != "locked" || !strings.Contains(out.Error.Message, accessHeader) || !strings.Contains(out.Error.Message, "app token") {
			t.Fatalf("%s: %+v", p, out.Error)
		}
		g.do(http.DefaultClient, "GET", p, nil, 200, nil, "Authorization", "Bearer "+tok)
		g.do(http.DefaultClient, "GET", p, nil, 200, nil, "X-Acnologia-Token", tok)
		g.do(http.DefaultClient, "GET", p, nil, 200, nil, "Authorization", "Bearer "+tk.Token)
		g.do(http.DefaultClient, "GET", p, nil, 401, nil, "Authorization", "Bearer acn_made_up_and_wrong_0000000000")
		g.do(http.DefaultClient, "GET", p, nil, 401, nil, "Authorization", "Bearer acno_made_up_and_wrong")
	}
	// An app token opens the docs only: the dashboard stays locked.
	g.do(http.DefaultClient, "GET", "/api/admin/me", nil, 401, nil, "Authorization", "Bearer "+tok)
	g.do(http.DefaultClient, "GET", "/api/auth/state", nil, 401, nil, "Authorization", "Bearer "+tok)
	// A revoked token no longer opens them.
	var toks struct {
		Tokens []struct {
			ID int64 `json:"id"`
		} `json:"tokens"`
	}
	g.do(g.super, "GET", "/api/admin/apps/"+itoa(int(appID))+"/tokens", nil, 200, &toks, accessHeader, testAccessPassword)
	g.do(g.super, "DELETE", "/api/admin/apps/"+itoa(int(appID))+"/tokens/"+itoa(int(toks.Tokens[0].ID)), nil, 200, nil, accessHeader, testAccessPassword)
	g.do(http.DefaultClient, "GET", "/llms.txt", nil, 401, nil, "Authorization", "Bearer "+tok)
}

// The errors doc is served like every other doc.
func TestErrorsDocServed(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.srv.URL + "/docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	d, ok := docsmd.Get("errors")
	if resp.StatusCode != 200 || !ok || string(b) != string(d.Body) || !strings.Contains(string(b), "### `no_key_available`") {
		t.Fatalf("errors.md: %d", resp.StatusCode)
	}
}
