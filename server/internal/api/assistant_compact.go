package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Compaction keeps a long Assistant thread inside the model's context
// window. When the prompt grows past a threshold, the older part of the
// conversation is summarized by Claude and the summary takes its place in
// what is sent to the model; the last few messages stay verbatim. Nothing is
// deleted: the stored messages and the transcript the user sees are kept
// whole, and a "compact" item marks where it happened.
//
// The summary is written from the stored history, which already holds vault
// placeholders instead of pasted secrets, and the result goes through the
// same redaction as tool results before it is stored or sent again.

const (
	// chatCompactRatio is the share of the model's context window at which a
	// thread is compacted, unless ACNOLOGIA_CHAT_COMPACT_TOKENS sets a size.
	chatCompactRatio = 0.6
	// chatTrimRatio is the share at which, when the summary call failed, the
	// oldest messages are left out of the request instead.
	chatTrimRatio = 0.9
	// chatCompactKeep is how many of the latest messages stay verbatim.
	chatCompactKeep = 6
	// chatSummaryBlock caps one text block in the summary input, and
	// chatSummaryToolText one tool input or result.
	chatSummaryBlock    = 4000
	chatSummaryToolText = 1500
	// chatSummaryInput caps the whole summary input in characters.
	chatSummaryInput = 400_000
	// chatSummaryMax caps the stored summary in characters.
	chatSummaryMax = 24_000
)

// chatCompact is the latest compaction of a thread.
type chatCompact struct {
	Summary string `json:"summary"`
	// Covers is how many messages, from the start, the summary replaces.
	Covers int       `json:"covers"`
	At     time.Time `json:"at"`
	// TokensSaved estimates the prompt tokens this compaction removed.
	TokensSaved int `json:"tokens_saved"`
	// Count is how many times the thread has been compacted.
	Count int `json:"count"`
}

var errNothingToCompact = errors.New("this chat is too short to compact")

const chatSummaryPrompt = `You compact a conversation between a user and the assistant inside Acnologia, a self-hosted API key pool. Your summary replaces the older messages in the assistant's context, so the assistant must be able to carry on from it alone.

Keep:
- what the user wants: goals, constraints and standing preferences
- decisions made, and actions done, failed, cancelled or waiting for the user's confirmation
- the exact names, slugs and ids of the services, keys, apps, providers, builds and batches touched
- tasks still pending and questions still open

If an earlier summary is given, fold it in: your summary replaces it.

Key placeholders such as [key k3 ...c21e] and batch refs such as b2 stay exactly as written. Never guess, complete or invent a key secret, app token, password or any other credential, and leave out anything that looks like one.

Write plain text in short lines or simple "- " lists, under 600 words, with no preamble. Do not use dashes as punctuation.`

// compactAt is the prompt size, in tokens, that triggers compaction: the
// configured size, or a share of the Assistant's current model's context
// window, so switching models moves it.
func (s *Server) compactAt() int {
	if s.Cfg.ChatCompactTokens > 0 {
		return s.Cfg.ChatCompactTokens
	}
	return compactAtFor(s.Agent.ContextWindowOf(s.Agent.ModelFor(context.Background(), agent.FeatureAssistant).Model))
}

// compactAtFor is the default compaction threshold for a context window.
func compactAtFor(window int) int { return int(float64(window) * chatCompactRatio) }

// trimAt is the prompt size at which a thread whose summary failed sends
// only its latest messages.
func (s *Server) trimAt() int {
	return int(float64(s.compactAt()) / chatCompactRatio * chatTrimRatio)
}

func (s *Server) aiReady() bool {
	return s.chatModel != nil || s.chatSummary != nil || s.Agent.Enabled()
}

// estTokens is the usual rough estimate of four characters per token.
func estTokens(chars int) int { return (chars + 3) / 4 }

func msgChars(ms []chatMsg) int {
	n := 0
	for _, m := range ms {
		for _, b := range m.Blocks {
			n += 16 + len(b.Text) + len(b.Input) + len(b.Content) + len(b.Name)
		}
	}
	return n
}

// compactFrom is the first stored message still sent verbatim.
func (d *chatData) compactFrom() int {
	if d.Compact != nil && d.Compact.Covers > 0 && d.Compact.Covers <= len(d.Messages) {
		return d.Compact.Covers
	}
	return 0
}

// promptTokens estimates the size of the next request: the input tokens
// Claude reported for the last call plus an estimate of what was added
// since, or a character estimate of the whole prompt, whichever is larger
// (the report includes the tool definitions; the character estimate holds
// when there is no report or it is stale).
func (cs *chatSession) promptTokens(system string) int {
	d := cs.data
	from := d.compactFrom()
	n := len(system) + msgChars(d.Messages[from:])
	if from > 0 {
		n += len(d.Compact.Summary)
	}
	est := estTokens(n)
	if d.LastInput > 0 && d.LastInputAt >= from && d.LastInputAt <= len(d.Messages) {
		est = max(est, d.LastInput+estTokens(msgChars(d.Messages[d.LastInputAt:])))
	}
	return est
}

// canStartAt reports whether the history sent to the model may start at
// message k: an assistant message (its tool results follow it), or a user
// message that answers no tool call. A user message holding tool_result
// blocks belongs with the tool_use blocks just before it.
func canStartAt(ms []chatMsg, k int) bool {
	if ms[k].Role == "assistant" {
		return true
	}
	for _, b := range ms[k].Blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return true
}

// compactSplit returns the first message to keep verbatim when compacting
// messages from `from` on, keeping at least `keep` of the latest, or -1 when
// nothing before them can be summarized.
func compactSplit(ms []chatMsg, from, keep int) int {
	for k := len(ms) - keep; k > from; k-- {
		if canStartAt(ms, k) {
			return k
		}
	}
	return -1
}

func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return store.TruncateUTF8(s, n) + " ...(cut)"
}

// summaryInput renders the messages to summarize, with any earlier summary,
// as one plain text document.
func summaryInput(prev string, ms []chatMsg) string {
	var b strings.Builder
	if prev != "" {
		b.WriteString("<earlier_summary>\n" + prev + "\n</earlier_summary>\n\n")
	}
	b.WriteString("<conversation>\n")
	for _, m := range ms {
		who := "User"
		if m.Role == "assistant" {
			who = "Assistant"
		}
		for _, bl := range m.Blocks {
			switch bl.Type {
			case "text":
				if t := strings.TrimSpace(bl.Text); t != "" {
					fmt.Fprintf(&b, "%s: %s\n", who, cutText(t, chatSummaryBlock))
				}
			case "tool_use":
				fmt.Fprintf(&b, "Assistant called %s %s\n", bl.Name, cutText(string(bl.Input), chatSummaryToolText))
			case "tool_result":
				label := "Tool result"
				if bl.IsError {
					label = "Tool error"
				}
				fmt.Fprintf(&b, "%s: %s\n", label, cutText(bl.Content, chatSummaryToolText))
			}
		}
	}
	b.WriteString("</conversation>")
	out := b.String()
	if len(out) > chatSummaryInput {
		// Keep the start and, above all, the latest part.
		head := store.TruncateUTF8(out, chatSummaryInput/4)
		tail := out[len(out)-chatSummaryInput*3/4:]
		out = head + fmt.Sprintf("\n[... %d characters left out ...]\n", len(out)-len(head)-len(tail)) + tail
	}
	return out
}

// dashFix turns dash punctuation in model text into commas.
var dashFix = strings.NewReplacer(" \u2014 ", ", ", "\u2014", ", ", " \u2013 ", ", ")

func (cs *chatSession) summarize(ctx context.Context, input string) (*anthropic.BetaMessage, error) {
	if cs.s.chatSummary != nil {
		return cs.s.chatSummary(ctx, chatSummaryPrompt, input)
	}
	return cs.s.Agent.Summarize(ctx, chatSummaryPrompt, input)
}

// compact summarizes the messages before the kept tail, folding in any
// earlier summary, and records it on the thread. On failure the thread is
// unchanged.
func (cs *chatSession) compact(ctx context.Context) error {
	d := cs.data
	from, prev := d.compactFrom(), ""
	if d.Compact != nil {
		prev = d.Compact.Summary
	}
	split := compactSplit(d.Messages, from, chatCompactKeep)
	if split < 0 {
		return errNothingToCompact
	}
	cs.turn.setStep("Summarizing earlier messages")
	rctx := cs.redactCtx(ctx)
	input := cs.s.Store.Scrub(rctx, cs.redact(summaryInput(prev, d.Messages[from:split])))
	resp, err := cs.summarize(rctx, input)
	if err != nil {
		return err
	}
	cs.s.Agent.Record(ctx, store.AIFeatureAssistant, agent.CallerOf(cs.user), agent.ThreadRef(cs.thread), resp)
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return errors.New("Claude declined to summarize this chat")
	}
	var parts []string
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			parts = append(parts, t.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return errors.New("the summary came back empty")
	}
	text = crypto.Truncate(dashFix.Replace(cs.s.Store.Scrub(rctx, cs.redact(text))), chatSummaryMax)
	saved := estTokens(msgChars(d.Messages[from:split])+len(prev)) - estTokens(len(text))
	if saved < 0 {
		saved = 0
	}
	n := 1
	if d.Compact != nil {
		n = d.Compact.Count + 1
	}
	covered := split - from
	d.Compact = &chatCompact{Summary: text, Covers: split, At: time.Now().UTC(), TokensSaved: saved, Count: n}
	// The last reported size no longer describes the prompt.
	d.LastInput, d.LastInputAt = 0, 0
	cs.compactItem(chatItem{Kind: "compact", Text: text, OK: true,
		Summary: fmt.Sprintf("Summarized %d earlier messages, about %s tokens saved", covered, roundTokens(saved))})
	return nil
}

// compactItem puts the divider in the transcript: before the message that
// started this turn when compaction ran inside a turn, else at the end.
func (cs *chatSession) compactItem(it chatItem) {
	cs.addItem(it)
	items := cs.data.Items
	at := -1
	for i := len(items) - 2; i >= 0; i-- {
		if items[i].Kind == "user" && cs.newIDs[items[i].ID] {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	last := items[len(items)-1]
	copy(items[at+1:], items[at:len(items)-1])
	items[at] = last
}

func roundTokens(n int) string {
	switch {
	case n >= 10_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// maybeCompact compacts before a model call once the prompt passes the
// threshold. A failed summary is logged and the turn goes on with the
// history as before (trimmed if it is close to the window); it is not
// retried in the same turn.
func (cs *chatSession) maybeCompact(ctx context.Context, system string) {
	if cs.compactFailed || cs.promptTokens(system) < cs.s.compactAt() {
		return
	}
	err := cs.compact(ctx)
	if err == nil {
		_ = cs.save()
		return
	}
	cs.compactFailed = true
	if !errors.Is(err, errNothingToCompact) {
		slog.Warn("compacting chat failed, sending the full history", "thread", cs.thread,
			"err", cs.s.Store.Scrub(cs.redactCtx(ctx), err.Error()))
	}
}

const chatSummaryLead = "[Summary of the earlier part of this conversation, written by Acnologia to save space. " +
	"The messages it covers are no longer shown to you; the user still sees them. Key placeholders are as they were.]\n\n"

const chatTrimNote = "[Earlier messages were left out of this request because the conversation is too long. " +
	"The user still sees them; ask the user if you need something from them.]"

// history is what the model is sent: the messages after the summary, led by
// the summary, or, when a failed summary left the prompt near the window,
// only the latest messages led by a note.
func (cs *chatSession) history(system string) (msgs []chatMsg, trimmed bool) {
	d := cs.data
	from := d.compactFrom()
	lead := ""
	if from > 0 {
		lead = chatSummaryLead + d.Compact.Summary
	}
	if cs.compactFailed && cs.promptTokens(system) >= cs.s.trimAt() {
		if k := compactSplit(d.Messages, from, chatCompactKeep); k > 0 {
			from, trimmed = k, true
			if lead != "" {
				lead += "\n\n" + chatTrimNote
			} else {
				lead = chatTrimNote
			}
		}
	}
	msgs = d.Messages[from:]
	if lead == "" {
		return msgs, false
	}
	block := chatBlock{Type: "text", Text: lead}
	if len(msgs) > 0 && msgs[0].Role == "user" {
		first := chatMsg{Role: "user", Blocks: append([]chatBlock{block}, msgs[0].Blocks...)}
		return append([]chatMsg{first}, msgs[1:]...), trimmed
	}
	return append([]chatMsg{{Role: "user", Blocks: []chatBlock{block}}}, msgs...), trimmed
}

// chatCompactNow compacts a thread on request, whatever its size.
func (s *Server) chatCompactNow(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if !s.aiReady() {
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancel()
	cs := &chatSession{s: s, r: r, ctx: ctx, cookie: r.Header.Get("Cookie"), remote: r.RemoteAddr, user: u, thread: id,
		title: t.Title, data: d, secrets: map[string]bool{}, newIDs: map[string]bool{}}
	if err := cs.compact(ctx); err != nil {
		if errors.Is(err, errNothingToCompact) {
			writeErr(w, 400, "nothing_to_compact", "This chat is too short to compact.")
			return
		}
		writeErr(w, 502, "compact_failed", "Could not summarize this chat, so nothing was changed: "+
			s.Store.Scrub(cs.redactCtx(ctx), err.Error()))
		return
	}
	if err := cs.save(); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.turnReply(w, cs, t, true)
}
