package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/go-chi/chi/v5"

	"github.com/cogito-keys/cogito/internal/agent"
	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/store"
)

// The assistant is a chat where Claude manages Cogito through tool calls.
// Every tool is a call to the dashboard API made as the signed in user, so
// roles, validation and the audit log apply exactly as in the UI. Destructive
// tools wait for the user to press Confirm. Conversations are encrypted at
// rest, pasted key secrets are replaced by their hint once the turn ends, and
// app tokens go to the user only, never back to the model.

const (
	chatMaxTurns     = 12
	chatMaxModelText = 12000
	chatMaxMessage   = 20000
)

// chatBlock is one content block of the model conversation, stored in our own
// shape and rebuilt into SDK params each turn.
type chatBlock struct {
	Type      string          `json:"type"` // text, tool_use, tool_result
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type chatMsg struct {
	Role   string      `json:"role"` // user, assistant
	Blocks []chatBlock `json:"blocks"`
}

// chatItem is one entry of the transcript the UI shows.
type chatItem struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"` // user, assistant, tool, confirm, error
	Text    string          `json:"text,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	OK      bool            `json:"ok"`
	Summary string          `json:"summary,omitempty"`
	Detail  string          `json:"detail,omitempty"`
	// Secret is a one time app token. It is sent in the response that created
	// it and never stored.
	Secret  string    `json:"secret,omitempty"`
	Pending *chatPend `json:"pending,omitempty"`
	Link    string    `json:"link,omitempty"`
	TS      time.Time `json:"ts"`
}

type chatPend struct {
	ID     string `json:"id"`
	Status string `json:"status"` // waiting, approved, cancelled, expired
}

type pendingAction struct {
	ID     string          `json:"id"`
	Tool   string          `json:"tool"`
	Input  json.RawMessage `json:"input"`
	Desc   string          `json:"desc"`
	ItemID string          `json:"item_id"`
	Status string          `json:"status"`
}

type chatData struct {
	Messages []chatMsg       `json:"messages"`
	Items    []chatItem      `json:"items"`
	Pending  []pendingAction `json:"pending"`
}

// chatSession is the state of one request against a thread.
type chatSession struct {
	s       *Server
	r       *http.Request
	user    *store.User
	data    *chatData
	secrets map[string]bool // key secrets seen this request, redacted on save
	newIDs  map[string]bool // items created this request
	tokens  map[string]string
}

// ---- persistence ----

func (s *Server) loadChat(ctx context.Context, id, userID int64) (*store.ChatThread, *chatData, error) {
	t, err := s.Store.GetThread(ctx, id, userID)
	if err != nil {
		return nil, nil, err
	}
	plain, err := s.Pool.Box.Decrypt(t.Data)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot decrypt conversation: %w", err)
	}
	var d chatData
	if err := json.Unmarshal([]byte(plain), &d); err != nil {
		return nil, nil, err
	}
	return t, &d, nil
}

func (s *Server) sealChat(d *chatData) ([]byte, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return s.Pool.Box.Encrypt(string(b))
}

// chatLocks serializes requests per thread.
var chatLocks sync.Map

func lockThread(id int64) (func(), bool) {
	m, _ := chatLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// ---- handlers ----

func (s *Server) chatList(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.ListThreads(r.Context(), userFrom(r).ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"threads": ts, "ai": s.Agent.Status()})
}

func (s *Server) chatCreate(w http.ResponseWriter, r *http.Request) {
	blob, err := s.sealChat(&chatData{Messages: []chatMsg{}, Items: []chatItem{}, Pending: []pendingAction{}})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	t, err := s.Store.CreateThread(r.Context(), userFrom(r).ID, "New chat", blob)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"thread": t, "items": []chatItem{}})
}

func (s *Server) chatGet(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	t, d, err := s.loadChat(r.Context(), id, userFrom(r).ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"thread": t, "items": d.Items})
}

func (s *Server) chatDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteThread(r.Context(), id, userFrom(r).ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) chatSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len(text) > chatMaxMessage {
		writeErr(w, 400, "invalid", fmt.Sprintf("send a message of 1 to %d characters", chatMaxMessage))
		return
	}
	s.chatTurn(w, r, func(cs *chatSession) {
		// A new message makes any confirmation still waiting stale.
		for i := range cs.data.Pending {
			if cs.data.Pending[i].Status == "waiting" {
				cs.setPending(i, "expired")
			}
		}
		cs.addItem(chatItem{Kind: "user", Text: text, OK: true})
		cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: []chatBlock{{Type: "text", Text: text}}})
	})
}

func (s *Server) chatConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PendingID string `json:"pending_id"`
		Approve   bool   `json:"approve"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.chatTurn(w, r, func(cs *chatSession) {
		idx := -1
		for i, p := range cs.data.Pending {
			if p.ID == body.PendingID && p.Status == "waiting" {
				idx = i
			}
		}
		if idx < 0 {
			cs.addItem(chatItem{Kind: "error", Text: "That confirmation is no longer waiting. Ask again if you still want it."})
			return
		}
		p := cs.data.Pending[idx]
		if !body.Approve {
			cs.setPending(idx, "cancelled")
			cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: []chatBlock{{Type: "text",
				Text: "[The user pressed Cancel on: " + p.Desc + ". Nothing was done.]"}}})
			return
		}
		cs.setPending(idx, "approved")
		t := chatToolByName(p.Tool)
		var in map[string]any
		_ = json.Unmarshal(p.Input, &in)
		out := cs.runTool(t, in, p.Input)
		cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: []chatBlock{{Type: "text",
			Text: "[The user pressed Confirm on: " + p.Desc + ". Result: " + out.Model + "]"}}})
	})
}

// chatTurn loads the thread, applies the user's action, runs the model until
// it answers, saves, and returns the items created by this request.
func (s *Server) chatTurn(w http.ResponseWriter, r *http.Request, apply func(cs *chatSession)) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if s.chatModel == nil && !s.Agent.Enabled() {
		writeErr(w, 503, "ai_disabled", agent.ErrDisabled.Error())
		return
	}
	unlock, ok := lockThread(id)
	if !ok {
		writeErr(w, 409, "busy", "this chat is still answering the previous message")
		return
	}
	defer unlock()
	u := userFrom(r)
	t, d, err := s.loadChat(r.Context(), id, u.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	cs := &chatSession{s: s, r: r, user: u, data: d, secrets: map[string]bool{}, newIDs: map[string]bool{}, tokens: map[string]string{}}
	apply(cs)
	if len(d.Messages) > 0 && d.Messages[len(d.Messages)-1].Role == "user" {
		if err := cs.loop(r.Context()); err != nil {
			cs.addItem(chatItem{Kind: "error", Text: err.Error()})
		}
	}
	if t.Title == "New chat" {
		for _, it := range d.Items {
			if it.Kind == "user" {
				t.Title = crypto.Truncate(firstLineOf(cs.redact(it.Text)), 60)
				break
			}
		}
	}
	cs.redactAll()
	fresh := []chatItem{}
	for _, it := range d.Items {
		if cs.newIDs[it.ID] {
			it.Secret = cs.tokens[it.ID]
			fresh = append(fresh, it)
		}
	}
	blob, err := s.sealChat(d)
	if err == nil {
		err = s.Store.SaveThread(context.Background(), id, u.ID, t.Title, blob)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	t.UpdatedAt = time.Now().UTC()
	writeJSON(w, 200, map[string]any{"thread": t, "items": fresh, "all": d.Items})
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ---- session helpers ----

func (cs *chatSession) addItem(it chatItem) string {
	if it.ID == "" {
		it.ID = crypto.RandomToken("itm_", 9)
	}
	it.TS = time.Now().UTC()
	cs.data.Items = append(cs.data.Items, it)
	cs.newIDs[it.ID] = true
	return it.ID
}

func (cs *chatSession) setPending(i int, status string) {
	cs.data.Pending[i].Status = status
	for j := range cs.data.Items {
		if cs.data.Items[j].ID == cs.data.Pending[i].ItemID && cs.data.Items[j].Pending != nil {
			cs.data.Items[j].Pending.Status = status
			cs.newIDs[cs.data.Items[j].ID] = true
		}
	}
}

// redact replaces key secrets seen this request with their hint.
func (cs *chatSession) redact(s string) string {
	for sec := range cs.secrets {
		if len(sec) >= 6 {
			s = strings.ReplaceAll(s, sec, "[key "+crypto.Hint(sec)+"]")
		}
	}
	return s
}

func (cs *chatSession) redactRaw(b json.RawMessage) json.RawMessage {
	if len(b) == 0 || len(cs.secrets) == 0 {
		return b
	}
	out := string(b)
	for sec := range cs.secrets {
		if len(sec) < 6 {
			continue
		}
		enc, _ := json.Marshal(sec)
		hint, _ := json.Marshal("[key " + crypto.Hint(sec) + "]")
		out = strings.ReplaceAll(out, string(enc), string(hint))
		out = strings.ReplaceAll(out, sec, "[key "+crypto.Hint(sec)+"]")
	}
	return json.RawMessage(out)
}

// redactAll scrubs every stored message and item of the secrets seen.
func (cs *chatSession) redactAll() {
	if len(cs.secrets) == 0 {
		return
	}
	for i := range cs.data.Messages {
		for j := range cs.data.Messages[i].Blocks {
			b := &cs.data.Messages[i].Blocks[j]
			b.Text, b.Content = cs.redact(b.Text), cs.redact(b.Content)
			b.Input = cs.redactRaw(b.Input)
		}
	}
	for i := range cs.data.Items {
		it := &cs.data.Items[i]
		it.Text, it.Summary, it.Detail = cs.redact(it.Text), cs.redact(it.Summary), cs.redact(it.Detail)
		it.Input = cs.redactRaw(it.Input)
	}
	for i := range cs.data.Pending {
		cs.data.Pending[i].Input = cs.redactRaw(cs.data.Pending[i].Input)
	}
}

// ---- model loop ----

func (cs *chatSession) params() []anthropic.BetaMessageParam {
	out := make([]anthropic.BetaMessageParam, 0, len(cs.data.Messages))
	for _, m := range cs.data.Messages {
		blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					blocks = append(blocks, anthropic.NewBetaTextBlock(b.Text))
				}
			case "tool_use":
				in := b.Input
				if len(in) == 0 {
					in = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropic.NewBetaToolUseBlock(b.ID, in, b.Name))
			case "tool_result":
				blocks = append(blocks, anthropic.NewBetaToolResultBlock(b.ToolUseID, b.Content, b.IsError))
			}
		}
		if len(blocks) == 0 {
			blocks = append(blocks, anthropic.NewBetaTextBlock("(empty)"))
		}
		if m.Role == "assistant" {
			out = append(out, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{Content: blocks}})
		} else {
			out = append(out, anthropic.NewBetaUserMessage(blocks...))
		}
	}
	return out
}

func (cs *chatSession) loop(ctx context.Context) error {
	tools := chatToolParams(cs.user.Role)
	system := cs.systemPrompt(ctx)
	for turn := 0; turn < chatMaxTurns; turn++ {
		converse := cs.s.Agent.Converse
		if cs.s.chatModel != nil {
			converse = cs.s.chatModel
		}
		resp, err := converse(ctx, system, cs.params(), tools)
		if err != nil {
			return fmt.Errorf("the assistant could not answer: %v", err)
		}
		msg := chatMsg{Role: "assistant"}
		results := []chatBlock{}
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaTextBlock:
				msg.Blocks = append(msg.Blocks, chatBlock{Type: "text", Text: b.Text})
				if t := strings.TrimSpace(b.Text); t != "" {
					cs.addItem(chatItem{Kind: "assistant", Text: t, OK: true})
				}
			case anthropic.BetaToolUseBlock:
				raw := json.RawMessage(b.JSON.Input.Raw())
				msg.Blocks = append(msg.Blocks, chatBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: raw})
				out := cs.call(b.Name, raw)
				results = append(results, chatBlock{Type: "tool_result", ToolUseID: b.ID, Content: out.Model, IsError: !out.OK})
			}
		}
		cs.data.Messages = append(cs.data.Messages, msg)
		switch resp.StopReason {
		case anthropic.BetaStopReasonToolUse:
			cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: results})
			continue
		case anthropic.BetaStopReasonMaxTokens:
			results = append(results, chatBlock{Type: "text", Text: "Your reply hit the length limit. Continue briefly."})
			cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: results})
			continue
		case anthropic.BetaStopReasonRefusal:
			return errors.New("Claude declined this request")
		}
		if len(results) > 0 {
			// Tool calls without a tool_use stop: still answer them so the
			// history stays valid, then let the model wrap up.
			cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: results})
			continue
		}
		return nil
	}
	return fmt.Errorf("stopped after %d steps; ask again to continue", chatMaxTurns)
}

func (cs *chatSession) systemPrompt(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are the assistant inside Cogito, a self-hosted API key pool. Cogito stores many API keys per provider (SerpAPI, ElevenLabs, any API key based HTTP API), hands working keys to apps through a proxy or leases, and cools keys down on rate limits.

You are talking to %s (%s), role %s. You act only through tools, which call Cogito's dashboard API as this user, so the same permissions apply. Never claim something was done unless a tool result says so. If a tool fails, say what failed in plain words.

Concepts: a provider describes an API. A service is a provider plus settings and keys; apps reach it at /proxy/<service slug>. Keys belong to a service. A key can record who it belongs to: account_name, account_email, account_phone, and account_ref (any other identifier, such as a username or account id).

Adding keys: when the user names a provider and gives keys, call list_services. If exactly one service uses that provider, add the keys there. If several do, ask which. If none does, call list_providers: if the provider exists, create_service from it, then add_keys. If no provider fits, offer start_provider_build (the AI provider builder) or a generic service with a base_url the user gives. Put any email, phone, name or other identifier the user mentions for a key into its account fields. Label keys usefully (for example the account email) when the user gives no label.

Deleting keys, services or apps and rotating app tokens need the user's confirmation: call the tool, then tell the user what will happen and that a Confirm button is shown. Do not call it again.

You cannot reveal stored key secrets, manage users, or read the audit log. App tokens from create_app or rotate_app_token are shown to the user directly; you never see them.

Be brief: short sentences, plain text, no headings. Use simple lists only when listing several items.`,
		cs.user.Name, cs.user.Email, cs.user.Role)
	if svcs, err := cs.s.Store.ListServices(ctx); err == nil && len(svcs) > 0 {
		b.WriteString("\n\nServices right now (slug: name, provider):")
		for i, sv := range svcs {
			if i == 40 {
				b.WriteString("\n...more, use list_services")
				break
			}
			fmt.Fprintf(&b, "\n- %s: %s, %s", sv.Slug, sv.Name, sv.Provider)
		}
	}
	return b.String()
}

// call runs one tool call from the model and records it in the transcript.
func (cs *chatSession) call(name string, raw json.RawMessage) toolOut {
	t := chatToolByName(name)
	if t == nil || !cs.user.Role.AtLeast(t.Role) {
		out := toolOut{Model: "unknown or not allowed tool " + name}
		cs.addItem(chatItem{Kind: "tool", Tool: name, Input: raw, Summary: out.Model})
		return out
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil || in == nil {
		in = map[string]any{}
	}
	if t.Confirm {
		return cs.ask(t, in, raw)
	}
	return cs.runTool(t, in, raw)
}

func (cs *chatSession) runTool(t *chatTool, in map[string]any, raw json.RawMessage) toolOut {
	for _, sec := range t.secretsIn(in) {
		cs.secrets[sec] = true
	}
	out := t.Run(cs, in)
	if len(out.Model) > chatMaxModelText {
		out.Model = crypto.Truncate(out.Model, chatMaxModelText) + "\n...(truncated)"
	}
	itemID := cs.addItem(chatItem{Kind: "tool", Tool: t.Name, Input: cs.redactRaw(raw), OK: out.OK,
		Summary: out.Summary, Detail: crypto.Truncate(out.Model, 4000), Link: out.Link})
	if out.Secret != "" {
		cs.tokens[itemID] = out.Secret
	}
	return out
}

// ask records a destructive action for the user to confirm.
func (cs *chatSession) ask(t *chatTool, in map[string]any, raw json.RawMessage) toolOut {
	desc, err := t.Describe(cs, in)
	if err != nil {
		out := toolOut{Model: err.Error(), Summary: err.Error()}
		cs.addItem(chatItem{Kind: "tool", Tool: t.Name, Input: raw, Summary: out.Summary})
		return out
	}
	p := pendingAction{ID: crypto.RandomToken("act_", 9), Tool: t.Name, Input: raw, Desc: desc, Status: "waiting"}
	p.ItemID = cs.addItem(chatItem{Kind: "confirm", Tool: t.Name, Input: raw, Text: desc, OK: true,
		Pending: &chatPend{ID: p.ID, Status: "waiting"}})
	cs.data.Pending = append(cs.data.Pending, p)
	return toolOut{OK: true, Model: "Not done yet. The user now sees a Confirm button for: " + desc +
		". Tell them briefly what will happen. Do not call this tool again; you will get a note when they decide."}
}

// ---- calling the dashboard API as the user ----

// handler is the server's router, built once, for in-process calls.
func (s *Server) handler() http.Handler {
	s.routerOnce.Do(func() { s.routerH = s.Router() })
	return s.routerH
}

// api calls a dashboard route as the signed in user and returns the status
// and decoded JSON body.
func (cs *chatSession) api(method, path string, body any) (int, map[string]any, string) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	// Drop the outer request's chi route context, or the router would treat
	// this call as a sub route of the chat endpoint.
	ctx := context.WithValue(cs.r.Context(), chi.RouteCtxKey, nil)
	req := httptest.NewRequest(method, "http://cogito.internal"+path, rdr).WithContext(ctx)
	req.Header.Set("Cookie", cs.r.Header.Get("Cookie"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "cogito-assistant")
	req.RemoteAddr = cs.r.RemoteAddr
	rec := httptest.NewRecorder()
	cs.s.handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	msg := ""
	if rec.Code >= 400 {
		msg = fmt.Sprintf("HTTP %d", rec.Code)
		if e, ok := out["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok && m != "" {
				msg = m
			}
		}
	}
	return rec.Code, out, msg
}
