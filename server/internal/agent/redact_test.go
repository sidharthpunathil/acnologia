package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/redact"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Made up secrets; none is a real key.
const (
	runTestKey  = "tk_live_9Qm2Xr7Lp4Vb8Nc1"
	runOtherKey = "tk_other_5Hs3Jd8Kf2Lg6Pq0"
	pastedAnt   = "sk-ant-api03-Zx9Yw8Vu7Ts6Rq5Po4Nm3Lk2Ji1Hg0Fe"
)

// recordingAnthropic keeps every request body. Builder calls (the ones that
// offer submit_provider) get the scripted replies in order; anything else
// (run naming, Fill with AI) gets fallback.
type recordingAnthropic struct {
	mu       sync.Mutex
	bodies   []string
	replies  []string
	fallback string
}

func (f *recordingAnthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	reply := f.fallback
	if strings.Contains(string(b), "submit_provider") {
		reply = `{"content":[{"type":"text","text":"stop"}],"stop_reason":"refusal"}`
		if len(f.replies) > 0 {
			reply, f.replies = f.replies[0], f.replies[1:]
		}
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":1},` + reply[1:]))
}

func (f *recordingAnthropic) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.bodies, "\n")
}

func TestBuilderNeverSendsOrLogsTheTestKey(t *testing.T) {
	f := &recordingAnthropic{fallback: `{"content":[{"type":"text","text":"{\"title\":\"Echo\",\"description\":\"Echo API\"}"}],"stop_reason":"end_turn"}`}
	spec := `{"base_url":"http://127.0.0.1:9","auth":{"type":"query","name":"key"},"health":{"method":"GET","path":"/health"}}`
	provider := `{"slug":"echo","name":"Echo","category":"test","description":"d","website":"http://127.0.0.1","docs_url":"http://127.0.0.1","color":"#000000","spec":` + spec + `}`
	f.replies = []string{
		// The model runs a live check; the request carries the key in the query.
		`{"content":[{"type":"tool_use","id":"toolu_1","name":"live_check","input":{"provider":` + provider + `}}],"stop_reason":"tool_use"}`,
		// Then it repeats a secret in its own text, as if an upstream had echoed it.
		`{"content":[{"type":"text","text":"The API answered: invalid key ` + runTestKey + ` (also ` + runOtherKey + `)"}],"stop_reason":"end_turn"}`,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a, u := newLiveAgent(t, srv.URL)
	ctx := context.Background()
	run, err := a.Submit(ctx, u, RunRequest{Kind: "create_provider", URLs: []string{"http://127.0.0.1"},
		Context: "Echo API. The key is " + runTestKey + " and my Claude key is " + pastedAnt,
		TestKey: runTestKey, TestKeys: []string{runOtherKey}})
	if err != nil {
		t.Fatal(err)
	}
	var got *store.AgentRun
	for i := 0; i < 500; i++ {
		got, _ = a.Store.GetRun(ctx, run.ID)
		if got.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Status != "failed" {
		t.Fatalf("run status %s", got.Status)
	}
	secrets := []string{runTestKey, runOtherKey, pastedAnt, "9Qm2Xr7Lp4Vb8Nc1", "Zx9Yw8Vu7Ts6Rq5Po4Nm3Lk2Ji1Hg0Fe"}
	sent := f.all()
	if !strings.Contains(sent, "live_check") || !strings.Contains(sent, "[key ...8Nc1]") {
		t.Fatalf("the replayed model text did not reach the boundary redacted: %s", sent)
	}
	steps, _ := a.Store.ListSteps(ctx, run.ID, 0)
	stepText := ""
	for _, s := range steps {
		stepText += s.Title + "\n" + s.Detail + "\n"
	}
	in, _ := json.Marshal(got.Input)
	for _, sec := range secrets {
		for where, text := range map[string]string{"a model request": sent, "agent_steps": stepText,
			"the run error": got.Error, "the run input": string(in), "the run title": got.Title + got.Description} {
			if strings.Contains(text, sec) {
				t.Fatalf("%s holds the secret %s", where, sec)
			}
		}
	}
	if !strings.Contains(stepText, "Live check") {
		t.Fatalf("the live check did not run: %s", stepText)
	}
}

// Fill with AI goes through the same client: a pool key typed into the
// instruction or a form value never reaches the model.
func TestAssistRedactsPoolKeysAtTheBoundary(t *testing.T) {
	f := &recordingAnthropic{fallback: `{"content":[{"type":"text","text":"{\"fields\":{},\"notes\":\"\"}"}],"stop_reason":"end_turn"}`}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a, u := newLiveAgent(t, srv.URL)
	pool := "plainpoolkey77"
	a.Store.SetRedactor(redact.New(func(context.Context) ([]string, error) { return []string{pool}, nil }))
	if _, err := a.Assist(context.Background(), u, "app", "an app for the key "+pool+" and "+pastedAnt,
		map[string]any{"name": pool}); err != nil {
		t.Fatal(err)
	}
	sent := f.all()
	if strings.Contains(sent, pool) || strings.Contains(sent, pastedAnt) || !strings.Contains(sent, "[key ...ey77]") {
		t.Fatalf("model request: %s", sent)
	}
}
