package api

import (
	"net/http"
	"sync"
	"testing"
)

// With usage written by group commit, a daily limit of 10 under 50 parallel
// requests (many commits sharing a batch) never serves more than 10, and
// serves exactly 10 in all.
func TestDailyLimitExactUnderGroupCommit(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("fast", nil, "key-a")
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		return 200, nil, `{"ok":true}`
	}
	_, tok := e.app(map[string]any{"name": "x", "limits": map[string]any{"daily_requests": 10}})

	var mu sync.Mutex
	got := map[int]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, _ := call(t, e, "GET", "/proxy/fast/q", tok)
			mu.Lock()
			got[resp.StatusCode]++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	if got[200] > 10 || got[200]+got[429] != 50 {
		t.Fatalf("burst over the limit: codes %v", got)
	}
	// A request admitted while another's usage is committed but its
	// reservation not yet released can be refused, so the burst may serve
	// fewer than 10. Requests after it take up exactly the rest.
	for i := 0; i < 20; i++ {
		resp, _ := call(t, e, "GET", "/proxy/fast/q", tok)
		got[resp.StatusCode]++
	}
	if got[200] != 10 || up.total.Load() != 10 {
		t.Fatalf("served %d (upstream %d), want exactly 10: %v", got[200], up.total.Load(), got)
	}
}
