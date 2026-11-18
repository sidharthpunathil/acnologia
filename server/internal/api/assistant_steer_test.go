package api

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Send now hands a message to the running turn: the model reads it on its
// next call in the same turn, and the transcript shows it as the user's.
func TestAssistantSteerReachesTheRunningTurn(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{
		toolUse("t1", "list_services", `{}`),
		text("done, and I saw your note"),
	}}
	var calls atomic.Int32
	first := make(chan struct{})
	release := make(chan struct{})
	e.api.chatModel = func(ctx context.Context, system string, msgs []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
		if calls.Add(1) == 1 {
			close(first)
			<-release // the first step runs until the test has steered
		}
		return f.converse(ctx, system, msgs, tools)
	}
	e.api.chatWaitFor = 20 * time.Millisecond
	id := e.newChat(e.super)
	path := "/api/admin/chat/threads/" + itoa(int(id))

	// Nothing running yet: Send now is refused so the client sends normally.
	e.do(e.super, "POST", path+"/steer", map[string]any{"text": "early"}, 409, nil)

	e.do(e.super, "POST", path+"/messages", map[string]any{"text": "look around"}, 200, nil)
	<-first
	var st struct {
		Turn map[string]any `json:"turn"`
	}
	e.do(e.super, "POST", path+"/steer", map[string]any{"text": "also check the serpapi keys"}, 202, &st)
	if st.Turn["steered"] != float64(1) {
		t.Fatalf("turn after steer: %v", st.Turn)
	}
	close(release)

	var got struct {
		Items []chatItem     `json:"items"`
		Turn  map[string]any `json:"turn"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.do(e.super, "GET", path, nil, 200, &got)
		if got.Turn["active"] == false || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Turn["active"] != false {
		t.Fatal("turn never ended")
	}
	f.mu.Lock()
	sent := append([]string(nil), f.sent...)
	f.mu.Unlock()
	if len(sent) != 2 || strings.Contains(sent[0], "serpapi keys") || !strings.Contains(sent[1], "also check the serpapi keys") ||
		!strings.Contains(sent[1], "while you were working") {
		t.Fatalf("model calls: %d\n%v", len(sent), sent)
	}
	users := []string{}
	for _, it := range got.Items {
		if it.Kind == "user" {
			users = append(users, it.Text)
		}
	}
	if len(users) != 2 || users[1] != "also check the serpapi keys" {
		t.Fatalf("user items %v", users)
	}

	// After the turn, Send now is refused again.
	e.do(e.super, "POST", path+"/steer", map[string]any{"text": "late"}, 409, nil)
}

// A message sent now as the model writes its final reply still gets an
// answer in the same turn.
func TestAssistantSteerAfterTheLastStepContinues(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{text("all set"), text("noted the extra ask")}}
	var calls atomic.Int32
	first := make(chan struct{})
	release := make(chan struct{})
	e.api.chatModel = func(ctx context.Context, system string, msgs []anthropic.BetaMessageParam, tools []anthropic.BetaToolUnionParam) (*anthropic.BetaMessage, error) {
		if calls.Add(1) == 1 {
			close(first)
			<-release
		}
		return f.converse(ctx, system, msgs, tools)
	}
	e.api.chatWaitFor = 20 * time.Millisecond
	id := e.newChat(e.super)
	path := "/api/admin/chat/threads/" + itoa(int(id))
	e.do(e.super, "POST", path+"/messages", map[string]any{"text": "hi"}, 200, nil)
	<-first
	e.do(e.super, "POST", path+"/steer", map[string]any{"text": "one more thing"}, 202, nil)
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("model calls %d, want 2", calls.Load())
	}
}
