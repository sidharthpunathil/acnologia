package api

import (
	"net/http"
	"testing"
	"time"
)

// TestAppLimitHoldsUnderConcurrency: with one request left, a burst of
// concurrent requests reaches the upstream once; the rest get a 429 while
// the first is still in flight, and the limit still holds after it ends.
func TestAppLimitHoldsUnderConcurrency(t *testing.T) {
	e, _ := newFeatureEnv(t)
	up, _ := e.upstream("slow", nil, "key-a")
	block := make(chan struct{})
	up.answer = func(string, *http.Request) (int, map[string]string, string) {
		<-block
		return 200, nil, `{"ok":true}`
	}
	_, tok := e.app(map[string]any{"name": "x", "limits": map[string]any{"daily_requests": 1}})

	const n = 6
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			resp, _ := call(t, e, "GET", "/proxy/slow/q", tok)
			codes <- resp.StatusCode
		}()
	}
	got := map[int]int{}
	for i := 0; i < n-1; i++ {
		select {
		case c := <-codes:
			got[c]++
		case <-time.After(5 * time.Second):
			close(block)
			t.Fatalf("requests over the limit are waiting on the upstream: %v so far", got)
		}
	}
	close(block)
	got[<-codes]++
	if got[200] != 1 || got[429] != n-1 || up.total.Load() != 1 {
		t.Fatalf("codes %v, upstream calls %d", got, up.total.Load())
	}
	if resp, _ := call(t, e, "GET", "/proxy/slow/q", tok); resp.StatusCode != 429 {
		t.Fatalf("after the burst: %d", resp.StatusCode)
	}
}
