package apierr

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestRegistryEntries: every entry has a meaning, a hint and a known area,
// and no em dashes.
func TestRegistryEntries(t *testing.T) {
	known := map[string]bool{}
	for _, a := range areas {
		known[a] = true
	}
	for code, h := range registry {
		if h.Hint == "" || h.Meaning == "" || !known[h.Area] {
			t.Errorf("%s: needs a meaning, a hint and a known area", code)
		}
		for _, s := range []string{h.Hint, h.Meaning, h.Fix.Tool, h.Fix.Command, h.Fix.URL} {
			if strings.ContainsRune(s, '\u2014') {
				t.Errorf("%s: no em dashes", code)
			}
		}
		if h.Fix.Tool != "" && !strings.HasPrefix(h.Fix.Tool, toolPrefix) {
			t.Errorf("%s: fix.tool %q should be the public tool name", code, h.Fix.Tool)
		}
	}
	if len(Codes()) != len(registry) {
		t.Fatal("Codes does not list every code")
	}
}

// TestUnknownCode: an unknown code has no help but still gets a doc link,
// and an envelope gets the response's request id.
func TestUnknownCode(t *testing.T) {
	h, ok := Lookup("not_a_real_code")
	if ok || h.Hint != "" {
		t.Fatal("made up code found")
	}
	b, _ := json.Marshal(Error{Code: "not_a_real_code", Message: "x"})
	if !strings.Contains(string(b), `"doc_url":"/docs/errors.md"`) {
		t.Fatalf("unknown code: %s", b)
	}
	w := httptest.NewRecorder()
	w.Header().Set("Retry-After", "7")
	e := Envelope(w, "not_a_real_code", "x")
	if e.RequestID == "" || e.RequestID != w.Header().Get("X-Request-Id") || e.RetryAfterSec == nil || *e.RetryAfterSec != 7 {
		t.Fatalf("envelope %+v, header %q", e, w.Header().Get("X-Request-Id"))
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
	for code, h := range registry {
		byArea[h.Area] = append(byArea[h.Area], code)
	}
	for _, area := range areas {
		codes := byArea[area]
		sort.Strings(codes)
		b.WriteString("## " + area + "\n\n")
		for _, code := range codes {
			h := registry[code]
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
			t.Errorf("%s is out of date with the error registry: run ACNOLOGIA_UPDATE_ERRORS_DOC=1 go test ./internal/apierr -run TestErrorsDocMatchesRegistry", p)
		}
	}
	if strings.ContainsRune(want, '\u2014') {
		t.Error("errors.md: no em dashes")
	}
	// Every code's doc_url anchor exists on the page.
	for code, h := range registry {
		if h.Doc == "" && !strings.Contains(want, "### `"+code+"`") {
			t.Errorf("%s: no heading for its anchor", code)
		}
	}
}
