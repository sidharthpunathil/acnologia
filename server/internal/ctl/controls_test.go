package ctl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// routeFake is a fake server that answers each "METHOD path" with a canned
// body and remembers every request it got.
type routeFake struct {
	mu       sync.Mutex
	answers  map[string]string
	reqs     []string
	bodies   map[string]string
	agents   []string
	streamed int
}

func (f *routeFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.reqs = append(f.reqs, key+"?"+r.URL.RawQuery)
	f.bodies[key] = string(b)
	f.agents = append(f.agents, r.Header.Get("User-Agent")+" "+r.Header.Get("X-Requested-With"))
	f.mu.Unlock()
	if key == "GET /api/admin/events/stream" {
		f.mu.Lock()
		f.streamed++
		n := f.streamed
		f.mu.Unlock()
		if n > 1 {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"token revoked"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "retry: 3000\n: hello\n\nid: 41\ndata: {\"id\":41,\"kind\":\"proxy\",\"service_slug\":\"serpapi\"}\n\n")
		return
	}
	if a, ok := f.answers[key]; ok {
		_, _ = io.WriteString(w, a)
		return
	}
	w.WriteHeader(404)
	_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"not found"}}`)
}

func (f *routeFake) saw(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if strings.HasPrefix(r, prefix) {
			return r
		}
	}
	return ""
}

func (f *routeFake) body(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal([]byte(f.bodies[key]), &m)
	return m
}

func newRecorder(t *testing.T) (*routeFake, func(args ...string) (int, string, string)) {
	f := &routeFake{bodies: map[string]string{}, answers: map[string]string{
		"GET /api/admin/apps":                   `{"apps":[{"id":3,"name":"billing"}]}`,
		"GET /api/admin/services":               `{"services":[{"id":7,"slug":"serpapi"}]}`,
		"GET /api/admin/apps/3/tokens":          `{"tokens":[{"id":9,"name":"ci"},{"id":10,"name":"old","revoked_at":"2026-01-01T00:00:00Z"}]}`,
		"GET /api/admin/apps/3/controls":        `{"token_limits":{"daily_requests":5},"tag_budgets":[{"key":"team","daily_budget":1}],"deny_rules":[{"id":1}]}`,
		"PUT /api/admin/apps/3/token-limits":    `{"token_limits":{}}`,
		"PATCH /api/admin/apps/3/tokens/9":      `{"id":9,"name":"ci","limits":{"monthly_budget":4},"pinned_tags":{"customer":"a"}}`,
		"PUT /api/admin/apps/3/tag-budgets":     `{"tag_budgets":[]}`,
		"GET /api/admin/apps/3/usage/tags":      `{"days":30,"tags":[{"key":"customer","value":"a","requests":4,"cost":0.5}],"tag_budgets":[]}`,
		"GET /api/admin/apps/3/deny-rules":      `{"rules":[{"id":1,"method":"POST","service":"serpapi","path_prefix":"/v1/files"}]}`,
		"POST /api/admin/apps/3/deny-rules":     `{"id":2,"method":"POST","host":"api.example.com","path_prefix":"/v1"}`,
		"DELETE /api/admin/apps/3/deny-rules/2": ``,
		"GET /api/admin/settings/spend-ceiling": `{"ceiling_usd":0,"source":"env","spent_today_usd":1.5}`,
		"PUT /api/admin/settings/spend-ceiling": `{"ceiling_usd":20,"source":"saved","spent_today_usd":1.5}`,
		"POST /api/admin/keys/check-all":        `{"job_id":12,"status":"queued","status_url":"/api/admin/jobs/12"}`,
		"POST /api/admin/services/7/check":      `{"message":"Checked 2 keys"}`,
		"GET /api/admin/jobs/12":                `{"job":{"id":12,"kind":"keys.check_all","status":"done","done":2,"total":2,"result":{"message":"Checked 2 keys"}}}`,
		"GET /api/admin/jobs":                   `{"jobs":[{"id":12,"kind":"keys.check_all","status":"done","done":2,"total":2}]}`,
		"GET /api/admin/events":                 `{"events":[{"id":40,"kind":"proxy"}],"next_cursor":40}`,
		"GET /api/admin/audit":                  `{"audit":[{"id":5,"action":"key.create","via":"cli","client":"acn/1.2.3"}],"next_cursor":5}`,
		"GET /api/admin/demo":                   `{"loaded":true,"service_slug":"demo","app_id":4}`,
		"POST /api/admin/demo":                  `{"service":{"slug":"demo"},"app":{"id":4,"name":"Demo app"},"token":"acn_demo","try":{"curl":"curl x"}}`,
		"DELETE /api/admin/demo":                `{"ok":true}`,
		"POST /api/admin/services/7/try":        `{"status":200,"latency_ms":12,"body":"{\"ok\":true}","app":{"name":"Try it"}}`,
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("ACNOLOGIA_CTL_CONFIG", filepath.Join(t.TempDir(), "ctl.json"))
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_good")
	return f, func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := RunAs("acn", args, strings.NewReader(`{"q":"tea"}`), &out, &errb)
		return code, out.String(), errb.String()
	}
}

func TestAccessControlCommands(t *testing.T) {
	f, run := newRecorder(t)
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })

	if code, out, errs := run("limits", "billing"); code != 0 || !strings.Contains(out, "5 requests a day") || !strings.Contains(out, "each team: $1.00 a day") {
		t.Fatalf("limits show: %d %s %s", code, out, errs)
	}
	if code, _, errs := run("limits", "billing", "--daily-budget", "2"); code != 0 {
		t.Fatalf("limits set: %s", errs)
	}
	if l := f.body("PUT /api/admin/apps/3/token-limits")["limits"].(map[string]any); l["daily_budget"] != 2.0 || len(l) != 1 {
		t.Fatalf("token-limits body: %v", l)
	}
	if code, _, _ := run("limits", "billing", "--clear"); code != 0 || len(f.body("PUT /api/admin/apps/3/token-limits")["limits"].(map[string]any)) != 0 {
		t.Fatal("limits --clear sends {}")
	}
	code, out, errs := run("limits", "billing", "--token", "ci", "--monthly-budget", "4", "--pin", "customer=a")
	if code != 0 || !strings.Contains(out, "customer=a") {
		t.Fatalf("token limits: %d %s %s", code, out, errs)
	}
	if b := f.body("PATCH /api/admin/apps/3/tokens/9"); b["pinned_tags"].(map[string]any)["customer"] != "a" || b["limits"].(map[string]any)["monthly_budget"] != 4.0 {
		t.Fatalf("token patch: %v", b)
	}
	if code, _, errs := run("limits", "billing", "--token", "old"); code != 4 {
		t.Fatalf("a revoked token is not found: %d %s", code, errs)
	}
	if code, _, _ := run("limits", "billing", "--pin", "a=b"); code != 2 {
		t.Fatal("--pin without --token is a usage error")
	}

	if code, out, _ := run("tags", "billing"); code != 0 || !strings.Contains(out, "customer=a") {
		t.Fatalf("tags: %s", out)
	}
	if code, _, errs := run("tags", "billing", "--budget", "customer", "--daily-budget", "2"); code != 0 {
		t.Fatalf("tag budget: %s", errs)
	}
	budgets := f.body("PUT /api/admin/apps/3/tag-budgets")["budgets"].([]any)
	if len(budgets) != 2 || budgets[1].(map[string]any)["key"] != "customer" || budgets[0].(map[string]any)["key"] != "team" {
		t.Fatalf("tag budgets keep the others: %v", budgets)
	}

	if code, out, _ := run("deny", "list", "billing"); code != 0 || !strings.Contains(out, "/v1/files") {
		t.Fatalf("deny list: %s", out)
	}
	if code, out, _ := run("deny", "add", "billing", "--method", "POST", "--host", "api.example.com", "--path", "/v1"); code != 0 || !strings.Contains(out, "Added deny rule 2") {
		t.Fatalf("deny add: %s", out)
	}
	if b := f.body("POST /api/admin/apps/3/deny-rules"); b["host"] != "api.example.com" || b["path_prefix"] != "/v1" {
		t.Fatalf("deny add body: %v", b)
	}
	if code, _, errs := run("deny", "rm", "billing", "2"); code != 0 {
		t.Fatalf("deny rm: %s", errs)
	}

	if code, out, _ := run("ceiling"); code != 0 || !strings.Contains(out, "No daily spend ceiling") {
		t.Fatalf("ceiling: %s", out)
	}
	for args, want := range map[string]any{"set 20": 20.0, "off": 0.0, "reset": nil} {
		if code, _, errs := run(append([]string{"ceiling"}, strings.Fields(args)...)...); code != 0 {
			t.Fatalf("ceiling %s: %s", args, errs)
		}
		if b := f.body("PUT /api/admin/settings/spend-ceiling"); b["usd"] != want {
			t.Fatalf("ceiling %s sent %v", args, b)
		}
	}

	// Every request names the CLI and its version, and keeps its marker.
	for _, a := range f.agents {
		if a != "acn/1.2.3 acnologia-ctl" {
			t.Fatalf("request headers: %q", a)
		}
	}
}

func TestObserveCommands(t *testing.T) {
	f, run := newRecorder(t)
	if code, out, _ := run("keys", "check", "--all", "--async"); code != 0 || !strings.Contains(out, "Started job 12") || f.saw("POST /api/admin/keys/check-all?async=1") == "" {
		t.Fatalf("check --all --async: %s %v", out, f.reqs)
	}
	if code, out, _ := run("keys", "check", "serpapi"); code != 0 || !strings.Contains(out, "Checked 2 keys") {
		t.Fatalf("keys check: %s", out)
	}
	if code, out, _ := run("jobs", "12", "--wait"); code != 0 || !strings.Contains(out, "done (2/2)") || !strings.Contains(out, "Checked 2 keys") {
		t.Fatalf("jobs 12: %s", out)
	}
	if code, out, _ := run("jobs", "--mine"); code != 0 || !strings.Contains(out, "keys.check_all") || f.saw("GET /api/admin/jobs?") == "" {
		t.Fatalf("jobs: %s", out)
	}
	code, _, errs := run("events", "--app", "billing", "--since", "1h", "--after", "30", "--kind", "proxy")
	if r := f.saw("GET /api/admin/events?"); code != 0 || !strings.Contains(r, "app_id=3") || !strings.Contains(r, "after_id=30") || !strings.Contains(r, "since=1h") {
		t.Fatalf("events query: %s", r)
	}
	if !strings.Contains(errs, "acn events --after 40") {
		t.Fatalf("events cursor hint: %s", errs)
	}
	code, out, errs := run("events", "--follow", "--json", "--after", "40")
	if code != 3 || !strings.Contains(out, `"id":41`) {
		t.Fatalf("follow: %d %s %s", code, out, errs)
	}
	// The reconnect asked for what came after the last event it printed.
	f.mu.Lock()
	var streams []string
	for _, r := range f.reqs {
		if strings.HasPrefix(r, "GET /api/admin/events/stream") {
			streams = append(streams, r)
		}
	}
	f.mu.Unlock()
	if len(streams) != 2 || !strings.Contains(streams[0], "after_id=40") || !strings.Contains(streams[1], "after_id=41") {
		t.Fatalf("stream requests: %v", streams)
	}
	if code, out, _ := run("audit", "--via", "cli", "--client", "acn/"); code != 0 || !strings.Contains(out, "acn/1.2.3") || !strings.Contains(f.saw("GET /api/admin/audit?"), "via=cli") {
		t.Fatalf("audit: %s", out)
	}

	if code, out, _ := run("demo"); code != 0 || !strings.Contains(out, "service demo") {
		t.Fatalf("demo status: %s", out)
	}
	if code, out, _ := run("demo", "load", "--force"); code != 0 || !strings.Contains(out, "acn_demo") || f.body("POST /api/admin/demo")["force"] != true {
		t.Fatalf("demo load: %s", out)
	}
	if code, _, _ := run("demo", "remove"); code != 0 {
		t.Fatal("demo remove")
	}
	code, out, errs = run("try", "serpapi", "/search.json?q=x", "--data", "@-")
	if code != 0 || !strings.Contains(errs, "200 OK in 12 ms as Try it") || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("try: %d %s %s", code, out, errs)
	}
	if b := f.body("POST /api/admin/services/7/try"); b["method"] != "POST" || b["body"] != `{"q":"tea"}` || b["path"] != "/search.json?q=x" {
		t.Fatalf("try body: %v", b)
	}
}
