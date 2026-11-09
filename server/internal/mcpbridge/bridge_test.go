package mcpbridge

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMCP answers like /mcp/control: 401 for a wrong token, 202 for
// notifications, and an echo of the request otherwise.
type fakeMCP struct {
	mu      sync.Mutex
	headers []http.Header
	bodies  []string
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// No notification stream here, as on /mcp/control.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.headers = append(f.headers, r.Header.Clone())
	f.bodies = append(f.bodies, string(b))
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer acnadm_good" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="acnologia"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"API token is invalid"}}`)
		return
	}
	if bytes.HasPrefix(b, []byte("[")) {
		var msgs []map[string]any
		_ = json.Unmarshal(b, &msgs)
		var out []map[string]any
		for _, m := range msgs {
			if id, ok := m["id"]; ok {
				out = append(out, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"method": m["method"]}})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	id, ok := m["id"]
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if m["method"] == "boom" {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "kaput\n")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"method": m["method"]}})
}

func run(t *testing.T, o Options, in string) (out, errs string) {
	t.Helper()
	var ob, eb bytes.Buffer
	if err := Run(o, strings.NewReader(in), &ob, &eb); err != nil {
		t.Fatal(err)
	}
	return ob.String(), eb.String()
}

func sortedLines(s string) []string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	sort.Strings(l)
	return l
}

func TestBridgeForwardsMessagesAndHeaders(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	filters := Filters{Groups: "services, keys,", Exclude: "delete_service", ReadOnly: true, Confirm: "OFF"}
	if err := filters.Normalize(); err != nil {
		t.Fatal(err)
	}
	h := filters.Header()
	h.Set("X-Acnologia-Access", "pw")
	o := Options{Endpoint: srv.URL + "/mcp/control", Token: "acnadm_good", Header: h}
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":"two","method":"tools/list"}` + "\n"
	out, errs := run(t, o, in)
	if errs != "" {
		t.Fatalf("stderr: %q", errs)
	}
	want := []string{
		`{"id":"two","jsonrpc":"2.0","result":{"method":"tools/list"}}`,
		`{"id":1,"jsonrpc":"2.0","result":{"method":"initialize"}}`,
	}
	if got := sortedLines(out); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stdout:\n%s", out)
	}
	if len(f.headers) != 3 {
		t.Fatalf("requests: %d", len(f.headers))
	}
	for _, hd := range f.headers {
		for k, v := range map[string]string{
			"Authorization":             "Bearer acnadm_good",
			"Content-Type":              "application/json",
			"Accept":                    "application/json",
			"X-Acnologia-Mcp-Groups":    "services,keys",
			"X-Acnologia-Mcp-Exclude":   "delete_service",
			"X-Acnologia-Mcp-Read-Only": "1",
			"X-Acnologia-Mcp-Confirm":   "off",
			"X-Acnologia-Access":        "pw",
		} {
			if hd.Get(k) != v {
				t.Errorf("%s = %q, want %q", k, hd.Get(k), v)
			}
		}
	}
}

func TestBridgeBatchPassesThrough(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	batch := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	out, _ := run(t, Options{Endpoint: srv.URL, Token: "acnadm_good"}, batch+"\n")
	if f.bodies[0] != batch {
		t.Fatalf("sent %q", f.bodies[0])
	}
	if strings.TrimSpace(out) != `[{"id":1,"jsonrpc":"2.0","result":{"method":"ping"}},{"id":2,"jsonrpc":"2.0","result":{"method":"tools/list"}}]` {
		t.Fatalf("stdout %q", out)
	}
}

func TestBridgeUnauthorized(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{})
	defer srv.Close()
	o := Options{Endpoint: srv.URL, Token: "acnadm_bad", Unauthorized: "run acn login"}
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`[{"jsonrpc":"2.0","id":7,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/x"}]` + "\n"
	out, errs := run(t, o, in)
	msg := "Acnologia refused the token (401): API token is invalid; run acn login"
	want := []string{
		`[{"error":{"code":-32603,"message":"` + msg + `"},"id":7,"jsonrpc":"2.0"}]`,
		`{"error":{"code":-32603,"message":"` + msg + `"},"id":1,"jsonrpc":"2.0"}`,
	}
	if got := sortedLines(out); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stdout:\n%s", out)
	}
	if errs != msg+"\n" {
		t.Fatalf("stderr should carry the hint once: %q", errs)
	}
}

func TestBridgeServerErrorsAndUnreachable(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{})
	out, _ := run(t, Options{Endpoint: srv.URL, Token: "acnadm_good"}, `{"jsonrpc":"2.0","id":3,"method":"boom"}`+"\n")
	if strings.TrimSpace(out) != `{"error":{"code":-32603,"message":"Acnologia returned 500: kaput"},"id":3,"jsonrpc":"2.0"}` {
		t.Fatalf("stdout %q", out)
	}
	srv.Close()
	out, _ = run(t, Options{Endpoint: srv.URL, Token: "acnadm_good"}, `{"jsonrpc":"2.0","id":4,"method":"ping"}`+"\n"+`{"jsonrpc":"2.0","method":"notifications/x"}`+"\n")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":4`) || !strings.Contains(lines[0], "cannot reach Acnologia at "+srv.URL) {
		t.Fatalf("stdout %q", out)
	}
}

func TestFilters(t *testing.T) {
	var f Filters
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	f.Register(fs)
	if err := fs.Parse([]string{"--groups", "services,keys", "--exclude", "delete_service, api_request", "--read-only", "--confirm", "off"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Normalize(); err != nil {
		t.Fatal(err)
	}
	if q := f.Query(); q != "groups=services,keys&exclude=delete_service,api_request&read_only=1&confirm=off" {
		t.Fatalf("query %q", q)
	}
	if a := strings.Join(f.Args(), " "); a != "--groups services,keys --exclude delete_service,api_request --read-only --confirm off" {
		t.Fatalf("args %q", a)
	}
	if (Filters{}).Set() || len((Filters{}).Header()) != 0 || (Filters{}).Query() != "" {
		t.Fatal("zero filters should add nothing")
	}
	if err := (&Filters{Groups: "all"}).Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := (&Filters{Groups: "services,nope"}).Normalize(); err == nil || !strings.Contains(err.Error(), `unknown group "nope"`) {
		t.Fatalf("got %v", err)
	}
	if err := (&Filters{Confirm: "maybe"}).Normalize(); err == nil || !strings.Contains(err.Error(), "on or off") {
		t.Fatalf("got %v", err)
	}
}

func TestTokenKinds(t *testing.T) {
	for tok, want := range map[string][2]bool{
		"acn_x": {true, false}, "cog_x": {true, false},
		"acnadm_x": {false, true}, "cogadm_x": {false, true}, "": {false, false},
	} {
		if IsAppToken(tok) != want[0] || IsAdminToken(tok) != want[1] {
			t.Errorf("%q", tok)
		}
	}
}

// The bridge relays the server's notification stream to stdout while stdin
// stays open.
func TestBridgeRelaysNotifications(t *testing.T) {
	sent := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Bearer acn_x" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": hello\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
		w.(http.Flusher).Flush()
		close(sent)
		<-r.Context().Done()
	}))
	defer srv.Close()
	pr, pw := io.Pipe()
	var out syncBuf
	done := make(chan error)
	go func() { done <- Run(Options{Endpoint: srv.URL, Token: "acn_x"}, pr, &out, io.Discard) }()
	<-sent
	for i := 0; i < 100 && !strings.Contains(out.String(), "list_changed"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` {
		t.Fatalf("relayed: %q", got)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
