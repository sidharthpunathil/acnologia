package agent

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cogito-keys/cogito/internal/catalog"
	"github.com/cogito-keys/cogito/internal/store"
)

// fakeByModel answers Haiku naming calls with naming and every other call
// with other, and records the request bodies.
func fakeByModel(t *testing.T, naming, other string) (string, *[]string) {
	t.Helper()
	bodies := &[]string{}
	ch := make(chan string, 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- string(b)
		var req struct{ Model string }
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Model == namingModel {
			_, _ = w.Write([]byte(naming))
			return
		}
		_, _ = w.Write([]byte(other))
	}))
	t.Cleanup(func() {
		srv.Close()
		close(ch)
		for b := range ch {
			*bodies = append(*bodies, b)
		}
	})
	return srv.URL, bodies
}

const refusal = `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5",
	"content":[{"type":"text","text":"no"}],"stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":1}}`

func namingReply(title, desc string) string {
	j, _ := json.Marshal(map[string]string{"title": title, "description": desc})
	t, _ := json.Marshal(string(j))
	return `{"id":"m2","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":` + string(t) +
		`}],"stop_reason":"end_turn","usage":{"input_tokens":400,"output_tokens":40}}`
}

func waitRun(t *testing.T, a *Agent, id int64, ok func(*store.AgentRun) bool) *store.AgentRun {
	t.Helper()
	var r *store.AgentRun
	for i := 0; i < 300; i++ {
		r, _ = a.Store.GetRun(context.Background(), id)
		if r != nil && ok(r) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run never got there: %+v", r)
	return nil
}

func TestFallbackTitle(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	for _, c := range []struct {
		req  RunRequest
		want string
	}{
		{RunRequest{Kind: "create_provider", Context: "<br /> This API reference describes the unary, streaming, and real-time APIs you&#39;ll use"},
			"This API reference describes the unary, streaming, and"},
		{RunRequest{Kind: "create_provider", Context: "<p>Deepgram</p>", URLs: []string{"https://www.docs.deepgram.com/reference"}}, "docs.deepgram.com"},
		{RunRequest{Kind: "update_provider", ProviderSlug: "serpapi"}, "SerpAPI"},
		{RunRequest{Kind: "create_provider", Context: "Acme \u2014 the best API"}, "Acme, the best API"},
		{RunRequest{Kind: "create_provider"}, "New provider"},
	} {
		got := a.fallbackTitle(ctx, c.req)
		if got != c.want || len(got) > maxTitle || strings.Contains(got, "<") {
			t.Errorf("fallbackTitle(%+v) = %q, want %q", c.req, got, c.want)
		}
	}
}

func TestHaikuNamesTheRunAndIsRecorded(t *testing.T) {
	url, bodies := fakeByModel(t, namingReply("Deepgram <b>provider</b>", "Builds a Deepgram speech to text provider \u2014 with live checks."), refusal)
	a, u := newLiveAgent(t, url)
	ctx := context.Background()
	run, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", Context: "<br/> Deepgram speech API docs", TestKeys: []string{"dg_secret_key_123456"}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Title != "Deepgram speech API docs" {
		t.Fatalf("fallback title %q", run.Title)
	}
	got := waitRun(t, a, run.ID, func(r *store.AgentRun) bool { return r.Title == "Deepgram provider" })
	if got.Description != "Builds a Deepgram speech to text provider, with live checks." {
		t.Fatalf("description %q", got.Description)
	}
	waitRun(t, a, run.ID, func(r *store.AgentRun) bool { return r.Status == "failed" })
	calls, _ := a.Store.ListAICalls(ctx, time.Now().Add(-time.Hour))
	var haiku *store.AICall
	for i := range calls {
		if calls[i].Model == namingModel {
			haiku = &calls[i]
		}
	}
	// 400 input tokens at $1 and 40 output at $5 per MTok.
	if haiku == nil || haiku.Feature != store.AIFeatureBuilder || haiku.Ref != runRef(run.ID) || math.Abs(haiku.CostUSD-0.0006) > 1e-12 {
		t.Fatalf("haiku ledger row: %+v", haiku)
	}
	a.Store.Close() // flush the fake's request log
	for _, b := range *bodies {
		if strings.Contains(b, "dg_secret_key_123456") {
			t.Fatal("a test key reached Claude")
		}
	}
}

func TestDraftNameOverridesAndHaikuCannotUndoIt(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	run, _ := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "running", Title: "docs.example.com"})
	s := &runState{run: run}
	a.nameFromDraft(ctx, s, &draft{Input: catalog.Input{Name: "Example", Description: "Example <i>search</i> API."}})
	// A late naming call matches on the fallback title and changes nothing.
	if ok, _ := a.Store.RenameRun(ctx, run.ID, "Late name", "late", "docs.example.com"); ok {
		t.Fatal("late naming overwrote the draft name")
	}
	got, _ := a.Store.GetRun(ctx, run.ID)
	if got.Title != "Example provider" || got.Description != "Example search API." {
		t.Fatalf("run named %q / %q", got.Title, got.Description)
	}
}

func TestPublishAddsAllTestKeysThenWipes(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	keys := []string{"first_key_0000000001", "second_key_000000002", "third_key_0000000003"}
	all, _ := json.Marshal(keys)
	enc, _ := a.Box.Encrypt(string(all))
	run, err := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "running", Title: "x", TestKeysEnc: enc,
		Input: store.AgentInput{CreateService: true, HasTestKey: true, TestKeyCount: 3}})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := a.Store.GetProvider(ctx, "serpapi")
	d := &draft{Input: catalog.Input{Slug: "serp-two", Name: "Serp Two", Category: base.Category, Spec: base.Spec}}
	if err := a.publish(ctx, run, d, "test", []byte("x"), "image/png", true); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Store.GetRun(ctx, run.ID)
	if got.Status != "published" || got.ServiceID == nil || got.TestKeysEnc != nil {
		t.Fatalf("run: status %s service %v keys kept %v", got.Status, got.ServiceID, got.TestKeysEnc != nil)
	}
	ks, _ := a.Store.ListKeys(ctx, *got.ServiceID, true)
	if len(ks) != 3 || ks[0].Hint != "...0001" || ks[0].Label != "Serp Two key 0001" {
		t.Fatalf("keys: %+v", ks)
	}
	steps, _ := a.Store.ListSteps(ctx, run.ID, 0)
	found := false
	for _, st := range steps {
		if st.Title == "Added 3 keys to Serp Two" {
			found = true
		}
		for _, k := range keys {
			if strings.Contains(st.Title+st.Detail, k) {
				t.Fatal("a step holds a secret")
			}
		}
	}
	if !found {
		t.Fatalf("no added step: %+v", steps)
	}
}

func TestFailedAndCancelledRunsWipeTestKeys(t *testing.T) {
	url, _ := fakeByModel(t, refusal, refusal)
	a, u := newLiveAgent(t, url)
	ctx := context.Background()
	run, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", Context: "Example", TestKey: "k_live_0000000000a",
		TestKeys: []string{"k_live_0000000000b", "k_live_0000000000a", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Input.TestKeyCount != 2 || run.TestKeysEnc == nil || run.TestKeyEnc == nil {
		t.Fatalf("run input: %+v", run.Input)
	}
	got := waitRun(t, a, run.ID, func(r *store.AgentRun) bool { return r.Status == "failed" && r.TestKeysEnc == nil })
	if got.TestKeyEnc != nil {
		t.Fatal("live check key kept after failure")
	}
	// Cancel wipes them too.
	run2, _ := a.Store.CreateRun(ctx, &store.AgentRun{Kind: "create_provider", Status: "queued", Title: "y", TestKeyEnc: []byte("a"), TestKeysEnc: []byte("b")})
	if err := a.Cancel(ctx, run2.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = a.Store.GetRun(ctx, run2.ID)
	if got.TestKeyEnc != nil || got.TestKeysEnc != nil {
		t.Fatal("cancel kept test keys")
	}
	many := make([]string, MaxTestKeys+1)
	for i := range many {
		many[i] = "key" + strings.Repeat("x", 10) + string(rune('a'+i%26)) + json.Number(rune(i)).String()
	}
	if _, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", Context: "x", TestKeys: many}); err == nil {
		t.Fatal("over the cap was accepted")
	}
}
