package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func meterOf(t *testing.T, body string) (int64, int64) {
	t.Helper()
	m := &tokenMeter{rc: io.NopCloser(strings.NewReader(body))}
	got, err := io.ReadAll(m)
	if err != nil || string(got) != body {
		t.Fatalf("meter changed the body: %v", err)
	}
	return m.counts()
}

// TestTokenMeterShapes: the counts of each provider's answer shape, plain
// and streamed, with a long middle between the head and the tail.
func TestTokenMeterShapes(t *testing.T) {
	filler := strings.Repeat(`data: {"type":"content_block_delta","delta":{"text":"lorem ipsum "}}`+"\n\n", 4000)
	cases := []struct {
		name    string
		body    string
		in, out int64
	}{
		{"openai chat", `{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`, 12, 34},
		{"anthropic", `{"content":[],"usage":{"input_tokens":7,"cache_read_input_tokens":900,"output_tokens":5}}`, 7, 5},
		{"gemini", `{"candidates":[],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":9,"totalTokenCount":12}}`, 3, 9},
		{"anthropic stream", `data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}` + "\n\n" + filler +
			`data: {"type":"message_delta","usage":{"output_tokens":512}}` + "\n\n", 25, 512},
		{"openai stream", filler + `data: {"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":80}}` + "\n\ndata: [DONE]\n\n", 40, 80},
		{"none", `{"results":[1,2,3]}`, 0, 0},
	}
	for _, c := range cases {
		in, out := meterOf(t, c.body)
		if in != c.in || out != c.out {
			t.Errorf("%s: got %d/%d, want %d/%d", c.name, in, out, c.in, c.out)
		}
	}
}

// TestShareAnalyticsMeasuresProxyShare: a person's calls through a proxy
// share show up in the analytics with requests, cost, tokens, the service,
// the claimant and how long access has been live.
func TestShareAnalyticsMeasuresProxyShare(t *testing.T) {
	e := newEnv(t)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33}}`)
	}))
	t.Cleanup(llm.Close)
	var svc store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "openai", "slug": "gpt", "base_url": llm.URL, "cost_per_request": 0.5}, 201, &svc)
	if !strings.EqualFold(svc.Category, "llm") {
		t.Fatalf("category %q", svc.Category)
	}
	e.do(e.super, "POST", "/api/admin/services/"+itoa(int(svc.ID))+"/keys", map[string]any{"keys": []map[string]any{{"secret": "sk-test-analytics-1"}}}, 201, nil)
	_, code := e.newPersonShare(map[string]any{"name": "For Ana", "label": "Ana", "services": []string{"gpt"}})
	tok, _ := e.redeemProxy(code)
	for i := 0; i < 3; i++ {
		e.do(http.DefaultClient, "POST", "/proxy/gpt/v1/chat/completions", map[string]any{"model": "m", "messages": []any{}}, 200, nil, "Authorization", "Bearer "+tok)
	}

	var rep analyticsReport
	deadline := time.Now().Add(3 * time.Second)
	for {
		e.do(e.super, "GET", "/api/admin/shares/analytics?days=7", nil, 200, &rep)
		if len(rep.Shares) == 1 && rep.Shares[0].Requests == 3 && rep.Shares[0].InputTokens == 33 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rep.Shares) != 1 {
		t.Fatalf("shares %+v", rep.Shares)
	}
	a := rep.Shares[0]
	if a.Recipient != "person" || a.Who != "Ana" || a.Tracking != "measured" || a.StartedAt == nil || a.LastUsedAt == nil || a.ActiveDays != 1 {
		t.Fatalf("share %+v", a)
	}
	if a.Requests != 3 || a.Cost < 1.49 || a.Cost > 1.51 || a.InputTokens != 33 || a.OutputTokens != 66 || a.TokenCalls != 3 {
		t.Fatalf("usage %+v", a)
	}
	if len(a.Services) != 1 || a.Services[0].Slug != "gpt" || a.Services[0].OutputTokens != 66 {
		t.Fatalf("services %+v", a.Services)
	}
	if len(a.Claimants) != 1 || a.Claimants[0].Requests != 3 {
		t.Fatalf("claimants %+v", a.Claimants)
	}
	if rep.Totals.Requests != 3 || rep.Totals.Recipients != 1 || rep.Totals.LiveShares != 1 || len(rep.Series) != 7 {
		t.Fatalf("totals %+v, %d days", rep.Totals, len(rep.Series))
	}
	e.do(e.super, "GET", "/api/admin/shares/analytics?days=500", nil, 400, nil)
}
