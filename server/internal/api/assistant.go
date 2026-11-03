package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The assistant is a chat where Claude manages Acnologia through tool calls.
// Every tool is a call to the dashboard API made as the signed in user, so
// roles, validation and the audit log apply exactly as in the UI. Destructive
// tools wait for the user to press Confirm. Conversations are encrypted at
// rest, pasted key secrets go to a per thread vault and reach Claude only as
// placeholders (see assistant_vault.go), and app tokens go to the user only,
// never back to the model.
//
// A turn runs as a server side job: the request that starts it waits a short
// while and then returns with turn.active set, the job keeps writing items to
// the thread as they happen, and the client polls GET thread until the turn
// ends. So a turn can wait for a provider build without hitting HTTP timeouts.

const (
	chatMaxTurns     = 24
	chatMaxModelText = 12000
	// chatMaxMessage fits a 1 MB file of keys dropped into the composer.
	chatMaxMessage = 1 << 20
	// chatTurnTimeout bounds one turn, including waits for provider builds.
	chatTurnTimeout = 15 * time.Minute
	// chatWaitBudget caps the time one turn spends in wait_for_build.
	chatWaitBudget = 10 * time.Minute
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
	// Keys counts the keys found in a user message; their secrets are in the
	// vault and Text shows only their hints.
	Keys int `json:"keys,omitempty"`
	// KeyList is those keys as label and hint ("...c21e"), in paste order.
	// Text is then the prose alone, without the keys.
	KeyList []chatKeyRef `json:"key_list,omitempty"`
	// Build is a provider build the turn follows (kind build).
	Build *chatBuild `json:"build,omitempty"`
	// Ask is a structured question for the user (kind ask).
	Ask *chatAsk `json:"ask,omitempty"`
	// Card is a rich result of a tool call: keys added, a service, a provider.
	Card *chatCard `json:"card,omitempty"`
	// Entities are the services, keys, apps and providers a tool result
	// names, with ids, so the UI can link them.
	Entities []chatEntity `json:"entities,omitempty"`
	// Suggestions are short follow ups the model proposes at the end of a turn.
	Suggestions []string  `json:"suggestions,omitempty"`
	TS          time.Time `json:"ts"`
}

type chatEntity struct {
	Type string `json:"type"` // service, key, app, provider
	ID   int64  `json:"id,omitempty"`
	Slug string `json:"slug,omitempty"`
	Name string `json:"name"`
}

type chatCard struct {
	Type     string         `json:"type"` // keys, service, provider
	Service  *chatEntity    `json:"service,omitempty"`
	Keys     []chatCardKey  `json:"keys,omitempty"`
	KeyTotal int            `json:"key_total,omitempty"`
	Skipped  int            `json:"skipped,omitempty"`
	Provider *chatCardProv  `json:"provider,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}

type chatCardKey struct {
	ID     int64  `json:"id"`
	Label  string `json:"label"`
	Hint   string `json:"hint"`
	Status string `json:"status"`
}

type chatCardProv struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Passed  int    `json:"tests_passed"`
	Failed  int    `json:"tests_failed"`
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
	// Vault holds pasted key secrets, each encrypted on its own, until a tool
	// uses them. The model sees only their refs.
	Vault    []vaultEntry `json:"vault,omitempty"`
	VaultSeq int          `json:"vault_seq,omitempty"`
	BatchSeq int          `json:"batch_seq,omitempty"`
	// Compact is the summary that replaces the older messages in what the
	// model is sent (assistant_compact.go). Messages and Items stay whole.
	Compact *chatCompact `json:"compact,omitempty"`
	// LastInput is the prompt size Claude reported for the last call, made
	// with the first LastInputAt messages.
	LastInput   int `json:"last_input,omitempty"`
	LastInputAt int `json:"last_input_at,omitempty"`
}

// chatSession is the state of one turn against a thread.
type chatSession struct {
	s *Server
	r *http.Request
	// ctx outlives the request that started the turn.
	ctx    context.Context
	cookie string
	remote string
	user   *store.User
	thread int64
	title  string
	data   *chatData
	turn   *turnState
	// halt ends the turn after the current tool results (ask_user).
	halt     bool
	waitUsed time.Duration
	secrets  map[string]bool // key secrets seen this turn, redacted on save
	newIDs   map[string]bool // items created this turn
	// compactFailed stops retrying a failed summary within the turn.
	compactFailed bool
}

// turnState is what GET thread reports while a turn runs.
type turnState struct {
	mu      sync.Mutex
	step    string
	started time.Time
	done    chan struct{}
	cancel  context.CancelFunc
}

func (t *turnState) setStep(s string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.step = s
	t.mu.Unlock()
}

func (t *turnState) view() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{"active": true, "step": t.step, "started_at": t.started}
}

// chatTurns maps a thread id to its running turn.
var chatTurns sync.Map

// chatTokens holds one time app tokens by thread until the owner's next
// request picks them up. They are never stored.
var chatTokens sync.Map // thread id -> *tokenBox

type tokenBox struct {
	mu sync.Mutex
	m  map[string]string
}

func putToken(thread int64, itemID, tok string) {
	v, _ := chatTokens.LoadOrStore(thread, &tokenBox{m: map[string]string{}})
	b := v.(*tokenBox)
	b.mu.Lock()
	b.m[itemID] = tok
	b.mu.Unlock()
}

// takeTokens hands out the waiting tokens of a thread once.
func takeTokens(thread int64) map[string]string {
	v, ok := chatTokens.LoadAndDelete(thread)
	if !ok {
		return nil
	}
	b := v.(*tokenBox)
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.m
}

func withTokens(items []chatItem, toks map[string]string) []chatItem {
	if len(toks) == 0 {
		return items
	}
	out := make([]chatItem, len(items))
	copy(out, items)
	for i := range out {
		if t, ok := toks[out[i].ID]; ok {
			out[i].Secret = t
		}
	}
	return out
}

func turnView(thread int64) map[string]any {
	if v, ok := chatTurns.Load(thread); ok {
		return v.(*turnState).view()
	}
	return map[string]any{"active": false}
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
	writeJSON(w, 200, map[string]any{"thread": t, "items": withTokens(d.Items, takeTokens(id)), "turn": turnView(id)})
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
	// The vault lived in the thread row and is gone with it; stop a running
	// turn so it cannot write the thread back.
	if v, ok := chatTurns.Load(id); ok {
		v.(*turnState).cancel()
	}
	takeTokens(id)
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
	s.chatTurn(w, r, func(cs *chatSession) error {
		// A new message makes any confirmation or question still waiting stale.
		for i := range cs.data.Pending {
			if cs.data.Pending[i].Status == "waiting" {
				cs.setPending(i, "expired")
			}
		}
		cs.expireAsks()
		model, shown, list, n, err := cs.vaultMessage(text)
		if err != nil {
			return err
		}
		// Anything key shaped that is left (a known pool key in prose, say)
		// is scrubbed from both copies as a last line.
		ctx := cs.ctx
		model, shown = cs.s.Store.Scrub(ctx, model), cs.s.Store.Scrub(ctx, shown)
		cs.addItem(chatItem{Kind: "user", Text: shown, Keys: n, KeyList: list, OK: true})
		cs.appendUser(chatBlock{Type: "text", Text: model})
		return nil
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
	s.chatTurn(w, r, func(cs *chatSession) error {
		idx := -1
		for i, p := range cs.data.Pending {
			if p.ID == body.PendingID && p.Status == "waiting" {
				idx = i
			}
		}
		if idx < 0 {
			cs.addItem(chatItem{Kind: "error", Text: "That confirmation is no longer waiting. Ask again if you still want it."})
			return nil
		}
		p := cs.data.Pending[idx]
		if !body.Approve {
			cs.setPending(idx, "cancelled")
			cs.appendUser(chatBlock{Type: "text", Text: "[The user pressed Cancel on: " + p.Desc + ". Nothing was done.]"})
			return nil
		}
		cs.setPending(idx, "approved")
		t := chatToolByName(p.Tool)
		var in map[string]any
		_ = json.Unmarshal(p.Input, &in)
		out := cs.runTool(t, in, p.Input)
		cs.appendUser(chatBlock{Type: "text", Text: "[The user pressed Confirm on: " + p.Desc + ". Result: " + out.Model + "]"})
		return nil
	})
}

// chatWait is how long the request that starts a turn waits for it before
// returning with the turn still active.
func (s *Server) chatWait() time.Duration {
	if s.chatWaitFor > 0 {
		return s.chatWaitFor
	}
	return 20 * time.Second
}

// chatTurn loads the thread, applies the user's action, then runs the model
// as a job that saves the thread as it goes. The response carries the items
// so far and turn.active while the job still runs.
func (s *Server) chatTurn(w http.ResponseWriter, r *http.Request, apply func(cs *chatSession) error) {
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
	u := userFrom(r)
	t, d, err := s.loadChat(r.Context(), id, u.ID)
	if err != nil {
		unlock()
		writeStoreErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), chatTurnTimeout)
	ts := &turnState{started: time.Now().UTC(), step: "Thinking", done: make(chan struct{}), cancel: cancel}
	cs := &chatSession{s: s, r: r, ctx: ctx, cookie: r.Header.Get("Cookie"), remote: r.RemoteAddr, user: u, thread: id,
		title: t.Title, data: d, turn: ts, secrets: map[string]bool{}, newIDs: map[string]bool{}}
	if err := apply(cs); err != nil {
		cancel()
		unlock()
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	cs.nameThread()
	if err := cs.save(); err != nil {
		cancel()
		unlock()
		writeStoreErr(w, err)
		return
	}
	run := len(d.Messages) > 0 && d.Messages[len(d.Messages)-1].Role == "user" && !cs.halt
	if !run {
		cancel()
		unlock()
		s.turnReply(w, cs, t, true)
		return
	}
	chatTurns.Store(id, ts)
	go func() {
		defer func() {
			chatTurns.CompareAndDelete(id, ts)
			cancel()
			unlock()
			close(ts.done)
		}()
		if err := cs.loop(ctx); err != nil {
			msg := cs.s.Store.Scrub(cs.redactCtx(ctx), err.Error())
			if ctx.Err() != nil {
				msg = "The assistant stopped: this turn ran out of time or the chat was deleted."
			}
			cs.addItem(chatItem{Kind: "error", Text: msg})
		}
		if err := cs.save(); err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("saving chat failed", "thread", id, "err", err)
		}
	}()
	select {
	case <-ts.done:
		s.turnReply(w, cs, t, true)
	case <-time.After(s.chatWait()):
		s.turnReply(w, cs, t, false)
	case <-r.Context().Done():
	}
}

// turnReply answers the request that started a turn. When the turn is done
// it returns the items it created; otherwise the saved transcript so far.
func (s *Server) turnReply(w http.ResponseWriter, cs *chatSession, t *store.ChatThread, done bool) {
	t.Title = cs.title
	t.UpdatedAt = time.Now().UTC()
	if !done {
		_, d, err := s.loadChat(context.Background(), cs.thread, cs.user.ID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		all := withTokens(d.Items, takeTokens(cs.thread))
		writeJSON(w, 200, map[string]any{"thread": t, "items": all, "all": all, "turn": turnView(cs.thread)})
		return
	}
	toks := takeTokens(cs.thread)
	fresh := []chatItem{}
	for _, it := range cs.data.Items {
		if cs.newIDs[it.ID] {
			fresh = append(fresh, it)
		}
	}
	writeJSON(w, 200, map[string]any{"thread": t, "items": withTokens(fresh, toks), "all": withTokens(cs.data.Items, toks),
		"turn": map[string]any{"active": false}})
}

// nameThread titles a new chat after its first message (keys already masked).
func (cs *chatSession) nameThread() {
	if cs.title != "New chat" {
		return
	}
	for _, it := range cs.data.Items {
		if it.Kind == "user" {
			cs.title = crypto.Truncate(firstLineOf(cs.redact(it.Text)), 60)
			return
		}
	}
}

// save scrubs secrets seen so far and writes the thread, so a client polling
// GET thread sees the turn as it happens.
func (cs *chatSession) save() error {
	cs.redactAll()
	blob, err := cs.s.sealChat(cs.data)
	if err != nil {
		return err
	}
	return cs.s.Store.SaveThread(context.Background(), cs.thread, cs.user.ID, cs.title, blob)
}

// appendUser adds blocks to the conversation as the user, merged into the
// last message when that is the user's too (after ask_user ends a turn on
// tool results).
func (cs *chatSession) appendUser(blocks ...chatBlock) {
	n := len(cs.data.Messages)
	if n > 0 && cs.data.Messages[n-1].Role == "user" {
		cs.data.Messages[n-1].Blocks = append(cs.data.Messages[n-1].Blocks, blocks...)
		return
	}
	cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: blocks})
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

// redactCtx carries the secrets this turn handled, so the model boundary
// and Scrub replace them even before they are pool keys.
func (cs *chatSession) redactCtx(ctx context.Context) context.Context {
	list := make([]string, 0, len(cs.secrets))
	for s := range cs.secrets {
		list = append(list, s)
	}
	return redact.WithSecrets(ctx, list...)
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

func toParams(msgs []chatMsg) []anthropic.BetaMessageParam {
	out := make([]anthropic.BetaMessageParam, 0, len(msgs))
	for _, m := range msgs {
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
		cs.maybeCompact(ctx, system)
		cs.turn.setStep("Thinking")
		history, trimmed := cs.history(system)
		sent := len(cs.data.Messages)
		resp, err := converse(cs.redactCtx(ctx), system, toParams(history), tools)
		if err != nil {
			return fmt.Errorf("the assistant could not answer: %v", err)
		}
		cs.s.Agent.Record(ctx, store.AIFeatureAssistant, agent.CallerOf(cs.user), agent.ThreadRef(cs.thread), resp)
		if !trimmed {
			u := resp.Usage
			cs.data.LastInput = int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
			cs.data.LastInputAt = sent
		}
		msg := chatMsg{Role: "assistant"}
		results := []chatBlock{}
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaTextBlock:
				msg.Blocks = append(msg.Blocks, chatBlock{Type: "text", Text: b.Text})
				if t, sugg := splitSuggestions(b.Text); t != "" || len(sugg) > 0 {
					cs.addItem(chatItem{Kind: "assistant", Text: t, Suggestions: sugg, OK: true})
				}
			case anthropic.BetaToolUseBlock:
				raw := json.RawMessage(b.JSON.Input.Raw())
				msg.Blocks = append(msg.Blocks, chatBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: raw})
				out := cs.call(b.Name, raw)
				results = append(results, chatBlock{Type: "tool_result", ToolUseID: b.ID, Content: out.Model, IsError: !out.OK})
			}
		}
		cs.data.Messages = append(cs.data.Messages, msg)
		if cs.halt {
			// ask_user: the answer comes back as the user's next message.
			cs.data.Messages = append(cs.data.Messages, chatMsg{Role: "user", Blocks: results})
			return nil
		}
		_ = cs.save()
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
	fmt.Fprintf(&b, `You are the assistant inside Acnologia, a self-hosted API key pool. Acnologia stores many API keys per provider (SerpAPI, ElevenLabs, any API key based HTTP API), hands working keys to apps through a proxy or leases, and cools keys down on rate limits.

You are talking to %s (%s), role %s. You act only through tools, which call Acnologia's dashboard API as this user, so the same permissions apply. Never claim something was done unless a tool result says so. If a tool fails, say what failed in plain words.

Concepts: a provider describes an API, including its offers: per product or endpoint, what is free, the price and the rate limits from its pricing page (Acnologia paces keys under those limits). For questions about pricing, free tiers or limits, call provider_offers (or get_provider) and cite the source page and its date. A service is a provider plus settings and keys; apps reach it at /proxy/<service slug>. Keys belong to a service. A key can record who it belongs to: account_name, account_email, account_phone, and account_ref (any other identifier, such as a username or account id).

Pasted keys: you never see key secrets. Before a message reaches you, Acnologia moves every key in it to a vault and puts a placeholder in its place, such as [key k3 ...c21e]: k3 is the key's ref and c21e its last four characters. Labels next to a key stay in the text (acct-1 [key k1 ...a1b2] means k1 is labeled acct-1). A big paste arrives as one line, such as [Pasted 300 keys, stored as batch b2 (refs k5 to k304). Labels: ...]. To store pasted keys call add_keys with batch "latest" (every key from the last paste, one call however many there are), or key_refs, or keys with a ref each when you add account details. Never ask for, repeat or invent a secret, and never put a placeholder in a secret field. Refer to keys by label or by their last four characters.

Adding keys: when the user names a provider and gives keys, find where they go. Call list_services: if exactly one service uses that provider, add the keys there; if several do, ask which. If none does, call search_providers with the name and any URL the user gave (it matches name, slug, website and docs URL). If a provider fits, prefer it (built in ones above all): create_service from it, then add_keys. Only when nothing fits, call start_provider_build (pass test_key_ref with one pasted key so it can run a live check), then call wait_for_build right away and keep calling it until the build ends. When it is published, create the service if the build did not, add the pasted keys with add_keys, and tell the user what is set up. If it needs review or fails, say so plainly with the link and stop. Put any email, phone, name or other identifier the user mentions for a key into its account fields. Use the labels from the paste as they are; when keys have no labels, label them usefully (for example the account email), and use ask_user with kind names only when the user clearly wants to name them.

Finish the job inside this turn. Never tell the user to come back later, to check on something, or to ask you again: wait for builds with wait_for_build and complete every step yourself. Ask only when a decision is genuinely the user's, and then use ask_user (kind choice for a short list of options, such as which of two Google APIs they meant), which shows a form and ends your turn.

Deleting keys, services or apps, rotating app tokens and revoking an app token need the user's confirmation: call the tool, then tell the user what will happen and that a Confirm button is shown. Do not call it again.

You cannot reveal stored key secrets, manage users, or read the audit log. An app can have several named tokens (list_app_tokens, create_app_token, revoke_app_token); rotate_app_token replaces all of them with one new token. App tokens from create_app, create_app_token or rotate_app_token are shown to the user directly; you never see them.

Be brief: short sentences, plain text, no headings. Use simple lists only when listing several items.

When your final reply of a turn leaves obvious next steps, end it with one last line "Suggestions: first | second | third": two or three short follow ups the user might send next, in their voice, under 40 characters each (for example "Suggestions: Check both Gemini keys | Create a proxy app for Gemini"). The line is shown as buttons, not as text. Leave it out when nothing obvious follows.`,
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
	cs.turn.setStep(stepText(t.Name))
	out := t.Run(cs, in)
	// Tool results go to Claude and into the transcript: scrub pool keys,
	// keys this turn handled, and anything key shaped (an upstream error
	// that echoes a key, a URL with ?key=).
	rctx := cs.redactCtx(cs.ctx)
	out.Model, out.Summary = cs.s.Store.Scrub(rctx, out.Model), cs.s.Store.Scrub(rctx, out.Summary)
	if len(out.Model) > chatMaxModelText {
		out.Model = crypto.Truncate(out.Model, chatMaxModelText) + "\n...(truncated)"
	}
	if out.Silent {
		return out
	}
	itemID := cs.addItem(chatItem{Kind: "tool", Tool: t.Name, Input: cs.redactRaw(raw), OK: out.OK,
		Summary: out.Summary, Detail: crypto.Truncate(out.Model, 4000), Link: out.Link, Card: out.Card, Entities: out.Entities})
	if out.Secret != "" {
		putToken(cs.thread, itemID, out.Secret)
	}
	_ = cs.save()
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
	ctx := context.WithValue(cs.ctx, chi.RouteCtxKey, nil)
	req := httptest.NewRequest(method, "http://acnologia.internal"+path, rdr).WithContext(ctx)
	req.Header.Set("Cookie", cs.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "acnologia-assistant")
	req.RemoteAddr = cs.remote
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
