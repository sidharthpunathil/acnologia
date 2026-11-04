package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// checkCooldown asserts the key sits out a 429 that asked for about two minutes.
func checkCooldown(t *testing.T, st *store.Store, keyID int64, retry time.Duration) {
	t.Helper()
	k, err := st.GetKey(context.Background(), keyID)
	if err != nil {
		t.Fatal(err)
	}
	if k.CooldownUntil == nil {
		t.Fatalf("429 with Retry-After seen, but the key has no cooldown (available now: %v)", k.Available(time.Now()))
	}
	if left := time.Until(*k.CooldownUntil); left < retry/2 {
		t.Fatalf("cooldown ends in %v, want about %v", left, retry)
	}
	if k.Available(time.Now()) {
		t.Fatal("key in cooldown is still available")
	}
}

// The upstream answers 429 with Retry-After: 120 and sends the body slowly.
// A client that hangs up while the proxy is still reading it must not cost
// the key its cooldown, or the pool keeps sending traffic into a limit.
func TestProxyVerdictSurvivesClientHangup(t *testing.T) {
	for _, hang := range []bool{false, true} {
		name := "client_waits"
		if hang {
			name = "client_hangs_up"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/account.json" {
					_, _ = w.Write([]byte(`{"searches_per_month":100,"total_searches_left":42}`))
					return
				}
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"error":"Too many`))
				w.(http.Flusher).Flush()
				time.Sleep(400 * time.Millisecond)
				_, _ = w.Write([]byte(` requests"}`))
			}))
			defer up.Close()
			var svc store.Service
			e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "serp2", "base_url": up.URL}, 201, &svc)
			var out struct{ Keys []store.Key }
			e.do(e.super, "POST", "/api/admin/services/"+strconv.FormatInt(svc.ID, 10)+"/keys", map[string]any{"keys": []map[string]any{{"secret": "k_aaaaaaaaaaaa"}}}, 201, &out)
			_, tok := e.app(map[string]any{"name": "a"})
			// Let the key's add time health check settle first.
			time.Sleep(300 * time.Millisecond)

			ctx := context.Background()
			if hang {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
				defer cancel()
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/proxy/serp2/search.json?q=x", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			} else if !hang {
				t.Fatal(err)
			}
			// The handler goes on after a hang-up; wait for its event.
			deadline := time.Now().Add(3 * time.Second)
			for found := false; !found && time.Now().Before(deadline); {
				evs, _ := e.st.ListEvents(context.Background(), store.EventFilter{ServiceID: svc.ID, Limit: 50})
				for _, ev := range evs {
					found = found || ev.Kind == "rate_limited"
				}
				if !found {
					time.Sleep(20 * time.Millisecond)
				}
			}
			checkCooldown(t, e.st, out.Keys[0].ID, 120*time.Second)
		})
	}
}

// A lease report claims the lease before it applies the verdict, so a
// caller that hangs up after the claim can never report again (409). The
// verdict must still land. The test holds the single writer connection to
// stop report right after its claim, then cancels the caller's context.
func TestLeaseReportVerdictSurvivesHangup(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "aaaa1111"})
	appID, tok := e.app(map[string]any{"name": "site", "mode": "lease"})
	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, "Authorization", "Bearer "+tok)
	app, err := e.st.GetApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := e.st.GetLease(context.Background(), l.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	// Let the add time health check and other writes drain.
	time.Sleep(300 * time.Millisecond)

	bg := context.Background()
	conn, err := e.st.DB.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		_, code, err := e.api.report(ctx, app, l.LeaseID, reportInput{Outcome: "rate_limited", RetryAfterSec: 120})
		done <- result{code, err}
	}()
	// Let queued writer uses through, queueing for the writer again right
	// behind them each time, until report's claim has committed. The test
	// then holds the writer before report can apply the verdict, which only
	// queues after the claim.
	for deadline := time.Now().Add(5 * time.Second); ; {
		conn.Close()
		if conn, err = e.st.DB.Conn(bg); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.st.GetLease(bg, l.LeaseID); got != nil && got.Outcome != "" {
			break
		}
		select {
		case r := <-done:
			conn.Close()
			t.Fatalf("report returned before the test held the writer: %d %v", r.code, r.err)
		default:
		}
		if time.Now().After(deadline) {
			conn.Close()
			t.Fatal("report never claimed the lease")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	conn.Close()
	r := <-done
	if r.err != nil || r.code != 200 {
		t.Fatalf("report after hang-up: %d %v", r.code, r.err)
	}
	checkCooldown(t, e.st, lease.KeyID, 120*time.Second)
}
