package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cogito-keys/cogito/internal/agent"
	"github.com/cogito-keys/cogito/internal/store"
)

// fakeAnthropicHTTP is a Messages endpoint that replays scripted replies and
// keeps every request body, so tests can prove what reached "Claude".
type fakeAnthropicHTTP struct {
	mu      sync.Mutex
	replies []string
	bodies  []string
	delay   time.Duration
}

func (f *fakeAnthropicHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	reply := text("ok")
	if len(f.replies) > 0 {
		reply, f.replies = f.replies[0], f.replies[1:]
	}
	delay := f.delay
	f.mu.Unlock()
	time.Sleep(delay)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":1},` + reply[1:]))
}

func (f *fakeAnthropicHTTP) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.bodies, "\n")
}

// useFakeAnthropic points the env's agent at a fake Anthropic server.
func (e *env) useFakeAnthropic(f *fakeAnthropicHTTP) {
	srv := httptest.NewServer(f)
	e.t.Cleanup(srv.Close)
	e.api.Agent = agent.New(agent.Config{APIKey: "test", BaseURL: srv.URL}, e.st, e.api.Catalog, e.api.Pool.Box)
	e.api.chatModel = nil
}

func (e *env) say(id int64, msg string) chatResp {
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": msg}, 200, &out)
	return out
}

func (e *env) chatData(id int64) *chatData {
	_, d, err := e.api.loadChat(context.Background(), id, 1)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func TestAssistantVaultKeeps300KeysAwayFromClaude(t *testing.T) {
	e := newEnv(t)
	f := &fakeAnthropicHTTP{replies: []string{
		toolUse("tu_1", "add_keys", `{"service":"serpapi","batch":"latest"}`),
		text("Added 300 keys to serpapi."),
	}}
	e.useFakeAnthropic(f)
	var b strings.Builder
	b.WriteString("add these serpapi keys please\n")
	seps := []string{"\n", ", ", "; ", " | ", "\t", " "}
	secrets := []string{}
	for i := 0; i < 300; i++ {
		sec := fmt.Sprintf("serp_%04d_%s", i, strings.Repeat("q", 12+i%7))
		secrets = append(secrets, sec)
		if i%5 == 0 {
			fmt.Fprintf(&b, "acct-%d: %s\n", i, sec)
			continue
		}
		b.WriteString(sec + seps[i%len(seps)])
	}
	id := e.newChat(e.super)
	out := e.say(id, b.String())

	keys, _ := e.st.ListKeys(context.Background(), 1, true)
	if len(keys) != 300 {
		t.Fatalf("stored %d keys, want 300", len(keys))
	}
	labeled := 0
	for _, k := range keys {
		if strings.HasPrefix(k.Label, "acct-") {
			labeled++
		}
	}
	if labeled != 60 {
		t.Fatalf("%d keys kept their pasted label, want 60", labeled)
	}
	// One add_keys call did it.
	calls := 0
	for _, it := range out.Items {
		if it.Kind == "tool" && it.Tool == "add_keys" {
			calls++
			if it.Card == nil || it.Card.KeyTotal != 300 {
				t.Fatalf("add_keys card: %+v", it.Card)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("%d add_keys calls", calls)
	}
	// Claude never saw a secret, only the condensed batch line.
	sent := f.all()
	for _, s := range secrets {
		if strings.Contains(sent, s) {
			t.Fatalf("a secret reached the Anthropic API: %s", s)
		}
	}
	if !strings.Contains(sent, "Pasted 300 keys, stored as batch b1") {
		t.Fatal("the model did not get the batch line")
	}
	// The transcript shows hints only, and the vault is empty once used.
	var got struct{ Items []chatItem }
	e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, &got)
	tb, _ := json.Marshal(got.Items)
	for _, s := range secrets {
		if strings.Contains(string(tb), s) {
			t.Fatal("the transcript holds a secret")
		}
	}
	if got.Items[0].Keys != 300 || !strings.Contains(got.Items[0].Text, "300 keys: …") {
		t.Fatalf("user item: %d %q", got.Items[0].Keys, got.Items[0].Text)
	}
	if d := e.chatData(id); len(d.Vault) != 0 {
		t.Fatalf("vault kept %d used entries", len(d.Vault))
	}
}

func TestAssistantSmallPasteGetsInlinePlaceholders(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{text("Which service?")}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	e.say(id, "acct-1, sk_live_abc123def456ghi789 and acct-2 sk_live_zyx987wvu654tsr321")
	if strings.Contains(f.sent[0], "sk_live_") || !strings.Contains(f.sent[0], "acct-1, [key k1 ...i789]") ||
		!strings.Contains(f.sent[0], "acct-2 [key k2 ...r321]") {
		t.Fatalf("model text: %s", f.sent[0])
	}
	d := e.chatData(id)
	if len(d.Vault) != 2 || d.Vault[0].Label != "acct-1" || string(d.Vault[0].Enc) == "sk_live_abc123def456ghi789" {
		t.Fatalf("vault: %+v", d.Vault)
	}
	if d.Items[0].Text != "acct-1, …i789 and acct-2 …r321" {
		t.Fatalf("shown: %q", d.Items[0].Text)
	}
	// Deleting the thread takes the vault with it.
	e.do(e.super, "DELETE", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, nil)
	if _, err := e.st.GetThread(context.Background(), id, 1); err == nil {
		t.Fatal("thread survived delete")
	}
}

// A provider build is followed to the end inside the turn, then the pasted
// keys go to the new service with their pasted names.
func TestAssistantFollowsBuildAndAddsKeys(t *testing.T) {
	e := newEnv(t)
	e.api.buildPoll = 5 * time.Millisecond
	ctx := context.Background()
	run, err := e.st.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "running", Title: "Gemini provider", CreatedBy: "root@x.io"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		// The fake builder logs a few steps, then publishes after several polls.
		e.st.AddStep(ctx, run.ID, "fetch", "Reading https://ai.google.dev/api", "")
		time.Sleep(40 * time.Millisecond)
		e.st.AddStep(ctx, run.ID, "tool", "Submitted Gemini (attempt 1 of 3)", "")
		time.Sleep(20 * time.Millisecond)
		st, slug, svc := "published", "serpapi", int64(1)
		_ = e.st.UpdateRun(ctx, run.ID, store.RunPatch{Status: &st, ProviderSlug: &slug, ServiceID: &svc, Report: map[string]int{"passed": 7, "failed": 0}})
	}()
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "wait_for_build", fmt.Sprintf(`{"run_id":%d}`, run.ID)),
		toolUse("tu_2", "add_keys", `{"service":"serpapi","batch":"latest"}`),
		text("Gemini is set up with both keys.\nSuggestions: Check both Gemini keys | Create a proxy app for Gemini"),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	k1, k2 := "AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUv", "AIzaSyZ9y8X7w6V5u4T3s2R1q0PoNmLkJiHgFe"
	out := e.say(id, "add these two gemini api keys (sachu: "+k1+", navaneeth1: "+k2+") https://ai.google.dev/api and add google as provider")

	var build, last *chatItem
	for i := range out.Items {
		if out.Items[i].Kind == "build" {
			build = &out.Items[i]
		}
		if out.Items[i].Kind == "assistant" {
			last = &out.Items[i]
		}
	}
	if build == nil || build.Build.Status != "published" || build.Build.Phase != 4 || build.Card == nil || build.Card.Provider.Passed != 7 {
		t.Fatalf("build card: %+v", build)
	}
	if last == nil || strings.Contains(last.Text, "Suggestions") || len(last.Suggestions) != 2 || last.Suggestions[0] != "Check both Gemini keys" {
		t.Fatalf("final reply: %+v", last)
	}
	keys, _ := e.st.ListKeys(ctx, 1, true)
	labels := map[string]bool{}
	for _, k := range keys {
		labels[k.Label] = true
	}
	if len(keys) != 2 || !labels["sachu"] || !labels["navaneeth1"] {
		t.Fatalf("keys: %+v", labels)
	}
	for _, s := range f.sent {
		if strings.Contains(s, k1) || strings.Contains(s, k2) {
			t.Fatal("a secret reached the model")
		}
	}
	if !strings.Contains(f.sent[1], `\"status\":\"published\"`) && !strings.Contains(f.sent[1], `"status":"published"`) {
		t.Fatalf("wait_for_build result: %s", f.sent[1])
	}
}

func TestAssistantAskUserNamesThenContinues(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "ask_user", `{"kind":"names","question":"What should I call these keys?","batch":"latest","suggestions":{"k1":"work"}}`),
		toolUse("tu_2", "add_keys", `{"service":"serpapi","batch":"latest"}`),
		text("Added both."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	out := e.say(id, "add 9f8e7d6c5b4a39281706f5e4 and 1a2b3c4d5e6f7a8b9c0d1e2f to serpapi")
	var ask *chatAsk
	for _, it := range out.Items {
		if it.Kind == "ask" {
			ask = it.Ask
		}
		if it.Kind == "assistant" {
			t.Fatal("the turn went on after ask_user")
		}
	}
	if ask == nil || len(ask.Rows) != 2 || ask.Rows[0].Suggestion != "work" || ask.Rows[1].Hint != "...1e2f" {
		t.Fatalf("ask: %+v", ask)
	}
	if len(f.sent) != 1 {
		t.Fatalf("model called %d times before the answer", len(f.sent))
	}
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/answer",
		map[string]any{"ask_id": ask.ID, "names": map[string]string{"k1": "work", "k2": "personal"}}, 200, &out)
	keys, _ := e.st.ListKeys(context.Background(), 1, true)
	if len(keys) != 2 || keys[0].Label != "work" || keys[1].Label != "personal" {
		t.Fatalf("keys: %+v", keys)
	}
	if !strings.Contains(f.sent[1], "k1 = work, k2 = personal") {
		t.Fatalf("answer not sent as a user message: %s", f.sent[1])
	}
	// A second answer to the same form is refused.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/answer",
		map[string]any{"ask_id": ask.ID, "choice": "x"}, 200, &out)
	if len(out.Items) == 0 || out.Items[len(out.Items)-1].Kind != "error" {
		t.Fatalf("replayed answer: %+v", out.Items)
	}
}

func TestAssistantLongTurnRunsInBackground(t *testing.T) {
	e := newEnv(t)
	e.api.chatWaitFor = 20 * time.Millisecond
	f := &fakeAnthropicHTTP{delay: 300 * time.Millisecond, replies: []string{text("Slow answer.")}}
	e.useFakeAnthropic(f)
	id := e.newChat(e.super)
	var out struct {
		Items []chatItem
		Turn  struct {
			Active bool
			Step   string
		}
	}
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "hello"}, 200, &out)
	if !out.Turn.Active || out.Turn.Step == "" || len(out.Items) != 1 {
		t.Fatalf("first reply: %+v", out)
	}
	// A second message while the turn runs is refused.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "again"}, 409, nil)
	for i := 0; i < 200 && out.Turn.Active; i++ {
		time.Sleep(10 * time.Millisecond)
		e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, &out)
	}
	if out.Turn.Active || len(out.Items) != 2 || out.Items[1].Text != "Slow answer." {
		t.Fatalf("after polling: %+v", out)
	}
}

func TestCreateKeysFromText(t *testing.T) {
	e := newEnv(t)
	var out struct {
		Keys    []store.Key
		Skipped []string
		Ignored int
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{
		"text": "# keys\nalpha, sk_live_abc123def456ghi789\nsk_live_zyx987wvu654tsr321\nnot a key",
	}, 201, &out)
	if len(out.Keys) != 2 || out.Keys[0].Label != "alpha" || out.Keys[1].Label != "SerpAPI key r321" || out.Ignored != 3 {
		t.Fatalf("keys: %+v ignored %d", out.Keys, out.Ignored)
	}
	// keys and text together; the duplicate is skipped, not an error.
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{
		"keys": []map[string]any{{"secret": "explicit-key", "label": "x"}}, "text": "sk_live_abc123def456ghi789",
	}, 201, &out)
	if len(out.Keys) != 1 || len(out.Skipped) != 1 {
		t.Fatalf("second call: %+v", out)
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"text": "nothing here"}, 400, nil)
	var b strings.Builder
	for i := 0; i < 5001; i++ {
		fmt.Fprintf(&b, "key_%016d\n", i)
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"text": b.String()}, 400, nil)
	if ks, _ := e.st.ListKeys(context.Background(), 1, true); len(ks) != 3 {
		t.Fatalf("over the cap stored keys: %d", len(ks))
	}
}

func TestCreateKeys5000InOneRequest(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	e := newEnv(t)
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "key_%016d\n", i)
	}
	var out struct{ Keys []store.Key }
	start := time.Now()
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"text": b.String()}, 201, &out)
	if len(out.Keys) != 5000 {
		t.Fatalf("created %d", len(out.Keys))
	}
	t.Logf("5000 keys in %s", time.Since(start))
}
