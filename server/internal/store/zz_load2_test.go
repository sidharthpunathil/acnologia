package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
)

// requestOps runs the store calls one successful proxied request makes, in
// order: the reads that admit and route it, then its four writes.
func requestOps(ctx context.Context, s *Store, app int) error {
	a, _, err := s.AppByToken(ctx, crypto.SHA256(fmt.Sprintf("tok%d", app)))
	if err != nil {
		return err
	}
	sv, err := s.GetServiceBySlug(ctx, fmt.Sprintf("svc%d", rand.IntN(nServices)+1))
	if err != nil {
		return err
	}
	l, err := s.GetAppLimits(ctx, a.ID)
	if err != nil {
		return err
	}
	if l.Any() {
		if _, err := s.GetAppUsage(ctx, a.ID, time.Now()); err != nil {
			return err
		}
	}
	if _, err := s.AppKeyScope(ctx, a.ID, sv.Slug); err != nil {
		return err
	}
	keys, err := s.CandidateKeys(ctx, sv.ID)
	if err != nil {
		return err
	}
	k := keys[rand.IntN(len(keys))]
	if err := s.MarkUsed(ctx, k.ID); err != nil {
		return err
	}
	s.AddEvent(ctx, NewEvent{Kind: "proxy", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: 200, LatencyMS: 100, Detail: "POST /v1/chat/completions"})
	zero := 0
	if err := s.ApplyEffect(ctx, k.ID, KeyEffect{ConsecutiveFails: &zero, MarkOK: true}); err != nil {
		return err
	}
	return s.RecordAppUsage(ctx, a.ID, 0.001, false)
}

// dashboardOps is what one person on the overview and Usage pages asks for.
func dashboardOps(ctx context.Context, s *Store) {
	svcs, _ := s.ListServices(ctx)
	_ = s.FillStats(ctx, svcs, false, nil)
	_, _ = s.ListEvents(ctx, EventFilter{Limit: 100})
	_, _ = s.UsageReport(ctx, time.Now(), 30, false)
}

type latStats struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latStats) add(d time.Duration) { l.mu.Lock(); l.d = append(l.d, d); l.mu.Unlock() }

func (l *latStats) pct(p float64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.d) == 0 {
		return 0
	}
	sort.Slice(l.d, func(i, j int) bool { return l.d[i] < l.d[j] })
	return l.d[int(float64(len(l.d)-1)*p)]
}

func loadDur() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("LOAD_SEC")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 4 * time.Second
}

// runLoad drives workers goroutines through requestOps for dur, with one
// dashboard reader alongside when dash is set, and prints throughput and
// latency.
func runLoad(t *testing.T, name string, s *Store, workers int, dur time.Duration, dash bool) {
	ctx := context.Background()
	var n, errs atomic.Int64
	lat := &latStats{}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := time.Now()
				if err := requestOps(ctx, s, i%nApps+1); err != nil {
					errs.Add(1)
				}
				lat.add(time.Since(st))
				n.Add(1)
			}
		}(i)
	}
	var dashN atomic.Int64
	dashLat := &latStats{}
	if dash {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := time.Now()
				dashboardOps(ctx, s)
				dashLat.add(time.Since(st))
				dashN.Add(1)
			}
		}()
	}
	time.Sleep(dur)
	close(stop)
	wg.Wait()
	msg := fmt.Sprintf("%-14s dash=%-5v workers=%-3d req/s=%8.0f p50=%-9v p99=%-9v max=%-9v errors=%d",
		name, dash, workers, float64(n.Load())/dur.Seconds(), lat.pct(0.5).Round(time.Microsecond),
		lat.pct(0.99).Round(time.Microsecond), lat.pct(1).Round(time.Microsecond), errs.Load())
	if dash {
		msg += fmt.Sprintf(" dash/s=%.2f dashP50=%v", float64(dashN.Load())/dur.Seconds(), dashLat.pct(0.5).Round(time.Millisecond))
	}
	fmt.Println(msg)
	if errs.Load() > 0 {
		t.Errorf("%s: %d requests failed", name, errs.Load())
	}
}

// TestZZLoad runs the full per request store sequence at 1, 16 and 64
// concurrent requests.
func TestZZLoad(t *testing.T) {
	needLoad(t)
	for _, workers := range []int{1, 16, 64} {
		runLoad(t, "store", openAt(t, freshDir(t)), workers, loadDur(), false)
	}
}

// TestZZLoad2 runs the same requests while one person keeps reloading the
// overview and Usage pages.
func TestZZLoad2(t *testing.T) {
	needLoad(t)
	for _, workers := range []int{1, 16, 64} {
		runLoad(t, "store", openAt(t, freshDir(t)), workers, loadDur(), true)
	}
}
