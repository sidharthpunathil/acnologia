package agent

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// fakeAnthropic answers every Messages call with one canned response.
func fakeAnthropic(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newLiveAgent(t *testing.T, baseURL string) (*Agent, *store.User) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	u, err := st.CreateUser(context.Background(), "op@x.io", "Op", "hash", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{APIKey: "test", BaseURL: baseURL}, st, cat, box), u
}

const usageJSON = `"usage":{"input_tokens":1000,"cache_creation_input_tokens":100,"cache_read_input_tokens":5000,"output_tokens":200,"server_tool_use":{"web_search_requests":2}}`

// 1000*$4 + 100*$5 + 5000*$0.20 + 200*$20 per MTok, plus 2 searches at $10 per 1,000.
const wantCost = 0.0095 + 0.02

func TestCostUsesThePriceTable(t *testing.T) {
	got := Cost("claude-opus-5-5", Tokens{Input: 1000, CacheWrite: 100, CacheRead: 5000, Output: 200, WebSearches: 2})
	if math.Abs(got-wantCost) > 1e-12 {
		t.Fatalf("cost %v, want %v", got, wantCost)
	}
	if PriceFor("some-future-model") != defaultPrice || PriceFor("claude-opus-5-5-20261001") != prices["claude-opus-5-5"] {
		t.Fatal("unknown and dated models should price at the default")
	}
	if p := PriceFor("claude-opus-5-5"); p.CacheWrite != p.Input*1.25 {
		t.Fatalf("cache writes should cost 1.25x input: %+v", p)
	}
}

func TestBuilderRecordsEveryCall(t *testing.T) {
	url := fakeAnthropic(t, `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5",
		"content":[{"type":"text","text":"no"}],"stop_reason":"refusal",`+usageJSON+`}`)
	a, u := newLiveAgent(t, url)
	ctx := context.Background()
	run, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", Context: "Example API"})
	if err != nil {
		t.Fatal(err)
	}
	var got *store.AgentRun
	for i := 0; i < 200; i++ {
		got, _ = a.Store.GetRun(ctx, run.ID)
		if got.Status == "failed" && got.Usage.CostUSD > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Status != "failed" {
		t.Fatalf("run status %s", got.Status)
	}
	// Two rows: the builder's call and the run naming call. This fake gives
	// both the same answer, so they look alike.
	var calls []store.AICall
	for i := 0; i < 100; i++ {
		calls, err = a.Store.ListAICalls(ctx, time.Now().Add(-time.Hour))
		if len(calls) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(calls) != 2 {
		t.Fatalf("ledger: %+v %v", calls, err)
	}
	c := calls[0]
	if c.Feature != store.AIFeatureBuilder || c.UserID != u.ID || c.UserEmail != "op@x.io" || c.Ref != runRef(run.ID) ||
		c.Model != "claude-opus-5-5" || c.InputTokens != 1000 || c.CacheWriteTokens != 100 || c.CacheReadTokens != 5000 ||
		c.OutputTokens != 200 || c.WebSearches != 2 || math.Abs(c.CostUSD-wantCost) > 1e-12 {
		t.Fatalf("builder row: %+v", c)
	}
	// The run's own cost agrees with the ledger.
	if math.Abs(got.Usage.CostUSD-c.CostUSD) > 1e-12 {
		t.Fatalf("run cost %v, ledger %v", got.Usage.CostUSD, c.CostUSD)
	}
}

func TestFillRecordsTheCall(t *testing.T) {
	url := fakeAnthropic(t, `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5",
		"content":[{"type":"text","text":"{\"fields\":{\"name\":\"Site\"},\"notes\":\"\"}"}],"stop_reason":"end_turn",`+usageJSON+`}`)
	a, u := newLiveAgent(t, url)
	ctx := context.Background()
	res, err := a.Assist(ctx, u, "app", "a marketing site", nil)
	if err != nil || res.Fields["name"] != "Site" {
		t.Fatalf("assist: %+v %v", res, err)
	}
	calls, _ := a.Store.ListAICalls(ctx, time.Now().Add(-time.Hour))
	if len(calls) != 1 || calls[0].Feature != store.AIFeatureFill || calls[0].UserID != u.ID || calls[0].Ref != "form:app" ||
		math.Abs(calls[0].CostUSD-wantCost) > 1e-12 {
		t.Fatalf("fill row: %+v", calls)
	}
}
