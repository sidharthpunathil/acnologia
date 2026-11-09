package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// fakeSummary stands in for the compaction call: it records each input and
// answers with reply, or fails with err.
type fakeSummary struct {
	mu     sync.Mutex
	inputs []string
	reply  string
	err    error
}

func (f *fakeSummary) call(_ context.Context, _ string, in string) (*anthropic.BetaMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	b, _ := json.Marshal(f.reply)
	var m anthropic.BetaMessage
	full := `{"id":"msg","type":"message","role":"assistant","model":"fake","usage":{"input_tokens":5,"output_tokens":5},` +
		`"content":[{"type":"text","text":` + string(b) + `}],"stop_reason":"end_turn"}`
	if err := json.Unmarshal([]byte(full), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func txt(role, s string) chatMsg {
	return chatMsg{Role: role, Blocks: []chatBlock{{Type: "text", Text: s}}}
}

func TestCompactSplitKeepsToolPairsTogether(t *testing.T) {
	use := chatMsg{Role: "assistant", Blocks: []chatBlock{{Type: "tool_use", ID: "tu", Name: "list_keys"}}}
	res := chatMsg{Role: "user", Blocks: []chatBlock{{Type: "tool_result", ToolUseID: "tu", Content: "[]"}}}
	ms := []chatMsg{
		txt("user", "hi"), txt("assistant", "hello"), // 0, 1
		txt("user", "list keys"), use, res, use, res, // 2..6
		txt("assistant", "done"), txt("user", "thanks"), // 7, 8
	}
	// keep 4: the natural cut is 5 (an assistant tool_use), kept with its result.
	if k := compactSplit(ms, 0, 4); k != 5 {
		t.Fatalf("split = %d, want 5", k)
	}
	// keep 5: index 4 is a tool_result, so the cut moves back to its tool_use.
	if k := compactSplit(ms, 0, 5); k != 3 {
		t.Fatalf("split = %d, want 3", k)
	}
	for keep := 1; keep < len(ms); keep++ {
		k := compactSplit(ms, 0, keep)
		if k < 0 {
			continue
		}
		if !canStartAt(ms, k) || len(ms)-k < keep {
			t.Fatalf("keep %d: bad split %d", keep, k)
		}
	}
	// Nothing older than the kept tail, or only what an earlier summary covers.
	if k := compactSplit(ms, 0, len(ms)); k != -1 {
		t.Fatalf("split with everything kept = %d", k)
	}
	if k := compactSplit(ms, 3, 6); k != -1 {
		t.Fatalf("split inside the covered part = %d", k)
	}

	// The history sent after a split never starts with an orphan tool_result.
	cs := &chatSession{s: &Server{}, data: &chatData{Messages: ms, Compact: &chatCompact{Summary: "S", Covers: 3}}}
	h, _ := cs.history("")
	if h[0].Role != "user" || h[0].Blocks[0].Type != "text" || !strings.Contains(h[0].Blocks[0].Text, "S") {
		t.Fatalf("history does not lead with the summary: %+v", h[0])
	}
	if h[1].Role != "assistant" || h[1].Blocks[0].Type != "tool_use" {
		t.Fatalf("kept part does not start at the tool_use: %+v", h[1])
	}
	if len(ms[3].Blocks) != 1 || cs.data.Messages[0].Blocks[0].Text != "hi" {
		t.Fatal("history changed the stored messages")
	}
}

// sendLong sends n messages of about 2000 characters of plain prose each.
func (e *env) sendLong(id int64, n int, tag string) {
	for i := 0; i < n; i++ {
		e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages",
			map[string]any{"text": tag + " " + itoa(i) + ": " + strings.Repeat("please remember this detail about the setup. ", 45)}, 200, nil)
	}
}

func TestAssistantCompactsPastThresholdAndAgain(t *testing.T) {
	e := newEnv(t)
	e.api.Cfg.ChatCompactTokens = 2600
	f := &fakeClaude{}
	sum := &fakeSummary{reply: "User is setting up serpapi. FIRST SUMMARY."}
	e.api.chatModel, e.api.chatSummary = f.converse, sum.call
	id := e.newChat(e.super)

	// Short messages stay under the threshold: no summary call.
	for i := 0; i < 4; i++ {
		e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "short " + itoa(i)}, 200, nil)
	}
	if len(sum.inputs) != 0 {
		t.Fatalf("compacted below the threshold: %d calls", len(sum.inputs))
	}

	// Long ones push it over.
	e.sendLong(id, 3, "long")
	if len(sum.inputs) == 0 {
		t.Fatal("never compacted past the threshold")
	}
	if !strings.Contains(sum.inputs[0], "User: short 0") || strings.Contains(sum.inputs[0], "<earlier_summary>") {
		t.Fatalf("first summary input: %.300s", sum.inputs[0])
	}
	last := f.sent[len(f.sent)-1]
	if !strings.Contains(last, "FIRST SUMMARY") || strings.Contains(last, "short 0") {
		t.Fatalf("model was not sent the summary in place of old messages: %.400s", last)
	}
	_, d, err := e.api.loadChat(context.Background(), id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Compact == nil || d.Compact.Covers == 0 || d.Compact.TokensSaved <= 0 {
		t.Fatalf("compaction not stored: %+v", d.Compact)
	}
	firstCovers, firstCount := d.Compact.Covers, d.Compact.Count
	// The whole transcript is still there, with a divider.
	users, dividers := 0, 0
	for _, it := range d.Items {
		switch it.Kind {
		case "user":
			users++
		case "compact":
			dividers++
			if it.Text == "" || !strings.Contains(it.Summary, "Summarized") {
				t.Fatalf("divider item: %+v", it)
			}
		}
	}
	if users != 7 || dividers != firstCount {
		t.Fatalf("transcript has %d user items and %d dividers", users, dividers)
	}
	// The kept tail never starts on a tool_result, and old messages are kept.
	if len(d.Messages) != 14 {
		t.Fatalf("stored messages were dropped: %d", len(d.Messages))
	}

	// Repeated compaction folds the earlier summary in.
	sum.reply = "SECOND SUMMARY."
	e.sendLong(id, 3, "more")
	_, d, _ = e.api.loadChat(context.Background(), id, 1)
	if d.Compact.Count <= firstCount || d.Compact.Covers <= firstCovers || d.Compact.Summary != "SECOND SUMMARY." {
		t.Fatalf("second compaction: %+v", d.Compact)
	}
	in := sum.inputs[len(sum.inputs)-1]
	if !strings.Contains(in, "<earlier_summary>") || !strings.Contains(in, "SUMMARY") {
		t.Fatalf("second summary input lacks the earlier summary: %.300s", in)
	}
	if strings.Contains(in, "short 0") {
		t.Fatal("second summary was sent messages the first one already covers")
	}
	last = f.sent[len(f.sent)-1]
	if !strings.Contains(last, "SECOND SUMMARY") || strings.Contains(last, "FIRST SUMMARY") {
		t.Fatalf("model got the wrong summary: %.300s", last)
	}
}

func TestAssistantCompactionSummaryIsRedacted(t *testing.T) {
	e := newEnv(t)
	pool := "SKPOOLSECRET9f8e7d6c5b4a"
	e.addKeys(map[string]any{"secret": pool})
	e.api.Cfg.ChatCompactTokens = 2600
	f := &fakeClaude{}
	leak := "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	sum := &fakeSummary{reply: "Keys: " + pool + " and " + leak + " and [key k1 ...c21e] \u2014 done."}
	e.api.chatModel, e.api.chatSummary = f.converse, sum.call
	id := e.newChat(e.super)
	pasted := "sk-proj-Zz9Yy8Xx7Ww6Vv5Uu4Tt3Ss2Rr1Qq0Pp"
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "add this openai key " + pasted}, 200, nil)
	e.sendLong(id, 4, "long")
	if len(sum.inputs) == 0 {
		t.Fatal("never compacted")
	}
	for _, in := range sum.inputs {
		if strings.Contains(in, pasted) {
			t.Fatal("the summary call saw a pasted secret")
		}
	}
	_, d, _ := e.api.loadChat(context.Background(), id, 1)
	if d.Compact == nil {
		t.Fatal("no compaction stored")
	}
	for _, s := range []string{pool, leak} {
		if strings.Contains(d.Compact.Summary, s) {
			t.Fatalf("stored summary holds a secret: %s", d.Compact.Summary)
		}
		for _, sent := range f.sent {
			if strings.Contains(sent, s) {
				t.Fatal("the model was sent a secret from the summary")
			}
		}
	}
	if !strings.Contains(d.Compact.Summary, "[key k1 ...c21e]") || strings.Contains(d.Compact.Summary, "\u2014") {
		t.Fatalf("placeholder lost or dash kept: %s", d.Compact.Summary)
	}
}

func TestAssistantCompactionFailureKeepsHistory(t *testing.T) {
	e := newEnv(t)
	e.api.Cfg.ChatCompactTokens = 3000
	f := &fakeClaude{}
	sum := &fakeSummary{err: errors.New("overloaded")}
	e.api.chatModel, e.api.chatSummary = f.converse, sum.call
	id := e.newChat(e.super)
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "first goal"}, 200, nil)
	e.sendLong(id, 4, "long")
	if len(sum.inputs) == 0 {
		t.Fatal("never tried to compact")
	}
	// Below the trim point the turn goes on with the full history.
	if !strings.Contains(f.sent[len(f.sent)-1], "first goal") {
		t.Fatal("history was not sent in full after a failed summary")
	}
	var got struct{ Items []chatItem }
	e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, &got)
	for _, it := range got.Items {
		if it.Kind == "error" || it.Kind == "compact" {
			t.Fatalf("failed summary showed up in the transcript: %+v", it)
		}
	}
	// Far past the threshold, the oldest messages are left out with a note.
	e.sendLong(id, 4, "more")
	last := f.sent[len(f.sent)-1]
	if strings.Contains(last, "first goal") || !strings.Contains(last, "Earlier messages were left out") {
		t.Fatalf("history was not trimmed near the window: %.300s", last)
	}
	_, d, _ := e.api.loadChat(context.Background(), id, 1)
	if d.Compact != nil || len(d.Messages) != 18 {
		t.Fatalf("a failed summary changed the thread: %+v, %d messages", d.Compact, len(d.Messages))
	}
}

func TestAssistantCompactNowRoute(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{}
	sum := &fakeSummary{reply: "MANUAL SUMMARY."}
	e.api.chatModel, e.api.chatSummary = f.converse, sum.call
	id := e.newChat(e.super)
	path := "/api/admin/chat/threads/" + itoa(int(id)) + "/compact"
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "hello"}, 200, nil)
	e.do(e.super, "POST", path, map[string]any{}, 400, nil)
	for i := 0; i < 4; i++ {
		e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "step " + itoa(i)}, 200, nil)
	}
	var out chatResp
	e.do(e.super, "POST", path, map[string]any{}, 200, &out)
	if len(out.Items) != 1 || out.Items[0].Kind != "compact" || out.Items[0].Text != "MANUAL SUMMARY." {
		t.Fatalf("compact reply: %+v", out.Items)
	}
	// Below the threshold, the next turn still uses the summary.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "and now"}, 200, nil)
	if last := f.sent[len(f.sent)-1]; !strings.Contains(last, "MANUAL SUMMARY") || strings.Contains(last, `"hello"`) {
		t.Fatalf("next turn ignored the summary: %.300s", last)
	}
	// Same auth as the other chat routes: signed out is a 401, and another
	// user's thread is a 404.
	e.do(client(), "POST", path, map[string]any{}, 401, nil)
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	e.do(m, "POST", path, map[string]any{}, 404, nil)
	// A failing summary leaves the thread as it was.
	sum.err = errors.New("boom")
	e.do(e.super, "POST", path, map[string]any{}, 502, nil)
	_, d, _ := e.api.loadChat(context.Background(), id, 1)
	if d.Compact == nil || d.Compact.Summary != "MANUAL SUMMARY." {
		t.Fatalf("failed compact changed the thread: %+v", d.Compact)
	}
}
