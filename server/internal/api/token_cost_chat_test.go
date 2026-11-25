package api

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// TestChatCompletionsTokenCost: OpenAI's model offers match the Responses
// API, and a Chat Completions call of the same model is priced by its
// tokens at the same rates, so the app's spend counts it.
func TestChatCompletionsTokenCost(t *testing.T) {
	e := newEnv(t)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","model":"gpt-6-astra","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500}}`)
	}))
	t.Cleanup(llm.Close)
	var svc store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "openai", "slug": "gpt", "base_url": llm.URL}, 201, &svc)
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(svc.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "sk-test-chat-cost-1"}}}, 201, nil)
	appID, tok := e.app(map[string]any{"name": "chat", "mode": "proxy"})
	e.do(http.DefaultClient, "POST", "/proxy/gpt/v1/chat/completions", map[string]any{"model": "gpt-6-astra", "messages": []any{}}, 200, nil, "Authorization", "Bearer "+tok)

	// GPT-6 Astra is $10 input and $50 output per million tokens.
	want := (1000*10.0 + 500*50.0) / 1e6
	var u store.AppUsage
	deadline := time.Now().Add(3 * time.Second)
	for {
		u, _ = e.st.GetAppUsage(context.Background(), appID, time.Now())
		if u.TodayCost > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if math.Abs(u.TodayCost-want) > 1e-9 {
		t.Fatalf("chat completions spend %v, want %v", u.TodayCost, want)
	}
}
