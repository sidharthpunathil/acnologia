package api

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Stop ends a running turn: the model call is cancelled, the transcript says
// so, and the next turn's history tells the model the work was cut short.
func TestAssistantStopEndsTheTurn(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{}
	var calls atomic.Int32
	started := make(chan struct{})
	e.api.chatModel = func(ctx context.Context, system string, msgs []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done() // a long answer, until Stop
			return nil, ctx.Err()
		}
		return f.converse(ctx, system, msgs, tools)
	}
	e.api.chatWaitFor = 20 * time.Millisecond
	id := e.newChat(e.super)
	var res chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "fix the catalog for all"}, 200, &res)
	<-started

	var got struct {
		Items []chatItem     `json:"items"`
		Turn  map[string]any `json:"turn"`
	}
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/stop", map[string]any{}, 200, &got)
	if got.Turn["active"] != false {
		t.Fatalf("still active after stop: %v", got.Turn)
	}
	last := got.Items[len(got.Items)-1]
	if last.Kind != "stopped" || !strings.HasPrefix(last.Text, "Stopped") {
		t.Fatalf("last item: %+v", last)
	}
	for _, it := range got.Items {
		if it.Kind == "error" {
			t.Fatalf("a stop is not an error: %+v", it)
		}
	}
	// Stopping again, with nothing running, is harmless.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/stop", map[string]any{}, 200, nil)

	// The next message runs, and the model learns the last turn was stopped.
	e.api.chatWaitFor = 0
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "just list services"}, 200, nil)
	if len(f.sent) != 1 || !strings.Contains(f.sent[0], "The user pressed Stop") || !strings.Contains(f.sent[0], "just list services") {
		t.Fatalf("next turn history: %v", f.sent)
	}
}
