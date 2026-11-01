package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/cogito-keys/cogito/internal/store"
)

// fakeClaude replays scripted assistant messages and records what it was sent.
type fakeClaude struct {
	mu      sync.Mutex
	replies []string
	sent    []string // JSON of the messages of each call
	tools   [][]string
}

func (f *fakeClaude) converse(_ context.Context, _ string, msgs []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := json.Marshal(msgs)
	f.sent = append(f.sent, string(b))
	names := []string{}
	for _, t := range tools {
		if t.OfTool != nil {
			names = append(names, t.OfTool.Name)
		}
	}
	f.tools = append(f.tools, names)
	reply := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
	if len(f.replies) > 0 {
		reply, f.replies = f.replies[0], f.replies[1:]
	}
	var m anthropic.BetaMessage
	full := `{"id":"msg","type":"message","role":"assistant","model":"fake","usage":{"input_tokens":1,"output_tokens":1},` + reply[1:]
	if err := json.Unmarshal([]byte(full), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func toolUse(id, name, input string) string {
	return `{"content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}],"stop_reason":"tool_use"}`
}

func text(t string) string {
	b, _ := json.Marshal(t)
	return `{"content":[{"type":"text","text":` + string(b) + `}],"stop_reason":"end_turn"}`
}

type chatResp struct {
	Thread struct{ ID int64 } `json:"thread"`
	Items  []chatItem         `json:"items"`
}

func (e *env) newChat(c *http.Client) int64 {
	var out chatResp
	e.do(c, "POST", "/api/admin/chat/threads", map[string]any{}, 201, &out)
	return out.Thread.ID
}

func TestAssistantAddsKeysWithAccountsAndForgetsSecrets(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "add_keys", `{"service":"serpapi","keys":[{"secret":"SK_ALPHA_111","account_email":"alice@x.io","label":"alice"},{"secret":"SK_BRAVO_222","account_phone":"+15550001"}]}`),
		text("Added two keys to serpapi."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages",
		map[string]any{"text": "add serpapi keys SK_ALPHA_111 (alice@x.io) and SK_BRAVO_222 phone +15550001"}, 200, &out)

	var tool *chatItem
	for i := range out.Items {
		if out.Items[i].Kind == "tool" {
			tool = &out.Items[i]
		}
	}
	if tool == nil || !tool.OK || !strings.Contains(tool.Summary, "Added 2 key(s)") {
		t.Fatalf("tool item: %+v", out.Items)
	}
	keys, _ := e.st.ListKeys(context.Background(), 1, true)
	if len(keys) != 2 || keys[0].AccountEmail != "alice@x.io" || keys[1].AccountPhone != "+15550001" {
		t.Fatalf("keys not stored with accounts: %+v", keys)
	}

	// The stored conversation and the transcript no longer hold the secrets.
	var got struct{ Items []chatItem }
	e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, &got)
	b, _ := json.Marshal(got.Items)
	if strings.Contains(string(b), "SK_ALPHA_111") || strings.Contains(string(b), "SK_BRAVO_222") {
		t.Fatalf("transcript still holds a secret: %s", b)
	}
	th, _ := e.st.GetThread(context.Background(), id, 1)
	if strings.Contains(string(th.Data), "SK_ALPHA") || strings.Contains(string(th.Data), "alice@x.io") {
		t.Fatal("thread data is not encrypted at rest")
	}
	// The next model call does not see them either.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "thanks"}, 200, nil)
	last := f.sent[len(f.sent)-1]
	if strings.Contains(last, "SK_ALPHA_111") || !strings.Contains(last, "_111]") {
		t.Fatalf("history sent to the model still holds the secret or lost the hint: %s", last)
	}
}

func TestAssistantDestructiveActionsNeedConfirm(t *testing.T) {
	e := newEnv(t)
	ks := e.addKeys(map[string]any{"secret": "k1"}, map[string]any{"secret": "k2"})
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "delete_key", `{"key_id":`+itoa(int(ks[0].ID))+`}`),
		text("Press Confirm to delete it."),
		text("Deleted."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "delete key 1"}, 200, &out)
	var pend string
	for _, it := range out.Items {
		if it.Kind == "confirm" && it.Pending != nil && it.Pending.Status == "waiting" {
			pend = it.Pending.ID
		}
	}
	if pend == "" {
		t.Fatalf("no confirmation card: %+v", out.Items)
	}
	if _, err := e.st.GetKey(context.Background(), ks[0].ID); err != nil {
		t.Fatal("key deleted before confirmation")
	}
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/confirm", map[string]any{"pending_id": pend, "approve": true}, 200, &out)
	if _, err := e.st.GetKey(context.Background(), ks[0].ID); err == nil {
		t.Fatal("key not deleted after confirmation")
	}
	// A second confirm of the same action does nothing.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/confirm", map[string]any{"pending_id": pend, "approve": true}, 200, &out)
	if len(out.Items) == 0 || out.Items[0].Kind != "error" {
		t.Fatalf("replayed confirm was not refused: %+v", out.Items)
	}
}

func TestAssistantRespectsRolesAndOwnership(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1", "account_email": "owner@x.io"})
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "memberpass1", "role": "member"}, 201, nil)
	m := client()
	e.do(m, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "memberpass1"}, 200, nil)
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "add_keys", `{"service":"serpapi","keys":[{"secret":"sneaky1"}]}`),
		text("I cannot."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(m)
	e.do(m, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "add a key"}, 200, nil)
	for _, n := range f.tools[0] {
		if n == "add_keys" || n == "delete_key" {
			t.Fatalf("member was offered %s", n)
		}
	}
	if keys, _ := e.st.ListKeys(context.Background(), 1, true); len(keys) != 1 {
		t.Fatal("member added a key through the assistant")
	}
	// Members do not see who keys belong to.
	var ks struct{ Keys []store.Key }
	e.do(m, "GET", "/api/admin/services/1/keys", nil, 200, &ks)
	if ks.Keys[0].AccountEmail != "" {
		t.Fatal("member sees account email")
	}
	// Threads are private to their owner.
	e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 404, nil)
}

func TestAssistantAppTokenGoesToUserOnly(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "create_app", `{"name":"site","mode":"proxy"}`),
		text("Created."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "make an app"}, 200, &out)
	var tok string
	for _, it := range out.Items {
		if it.Secret != "" {
			tok = it.Secret
		}
	}
	if !strings.HasPrefix(tok, "cog_") {
		t.Fatalf("token not shown to the user: %+v", out.Items)
	}
	for _, sent := range f.sent {
		if strings.Contains(sent, tok) {
			t.Fatal("the model saw the app token")
		}
	}
	var got struct{ Items []chatItem }
	e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, &got)
	b, _ := json.Marshal(got.Items)
	if strings.Contains(string(b), tok) {
		t.Fatal("the app token was persisted")
	}
}
