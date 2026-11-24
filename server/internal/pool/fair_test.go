package pool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// slotPool makes a service whose keys each take one request at a time.
func slotPool(t *testing.T, keys int) (*Pool, *store.Store, *store.Service, []int64) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	ctx := context.Background()
	sv, err := st.CreateService(ctx, &store.Service{Slug: "s", Name: "S", Provider: "generic", BaseURL: "https://x.example", AuthType: "header",
		AuthName: "Authorization", Strategy: "round_robin", CooldownSec: 1, MaxAttempts: 1, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range keys {
		enc, _ := box.Encrypt(fmt.Sprint("secret-", i))
		k, err := st.CreateKey(ctx, store.NewKey{ServiceID: sv.ID, Label: fmt.Sprint("k", i), SecretEnc: enc, SecretFP: fmt.Sprint("fp", i), Hint: "x", Tier: "primary", Weight: 1, Priority: 100})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, k.ID)
	}
	return New(st, box), st, sv, ids
}

// One app keeps a busy service saturated with many waiting requests. Another
// app's request, sent while they wait, is served after at most a couple of
// the swarm's, not after all of them.
func TestWaitQueueIsFairAcrossApps(t *testing.T) {
	p, _, sv, _ := slotPool(t, 1)
	ctx := context.Background()
	const swarm, hold = 8, 15 * time.Millisecond
	var served atomic.Int64 // swarm requests served so far
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range swarm {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				pk, err := p.PickFor(ctx, sv, 1, false, nil, 10*time.Second, false, Request{})
				if err != nil {
					t.Error(err)
					return
				}
				time.Sleep(hold)
				served.Add(1)
				pk.Release()
			}
		})
	}
	// Let the swarm fill the queue.
	time.Sleep(10 * hold)
	worst := int64(0)
	for range 5 {
		before := served.Load()
		pk, err := p.PickFor(ctx, sv, 2, false, nil, 10*time.Second, false, Request{})
		if err != nil {
			t.Fatal(err)
		}
		worst = max(worst, served.Load()-before)
		time.Sleep(hold)
		pk.Release()
		time.Sleep(2 * hold)
	}
	close(stop)
	wg.Wait()
	// First in, first out would put each request behind all 8 waiters.
	if worst > 3 {
		t.Fatalf("the other app waited behind %d of the swarm's requests", worst)
	}
}

// Within one app, waiters are served in the order they came.
func TestWaitQueueIsFIFOWithinAnApp(t *testing.T) {
	p, _, sv, _ := slotPool(t, 1)
	ctx := context.Background()
	first, err := p.PickFor(ctx, sv, 1, false, nil, time.Second, false, Request{})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			pk, err := p.PickFor(ctx, sv, 1, false, nil, 5*time.Second, false, Request{})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			pk.Release()
		})
		time.Sleep(20 * time.Millisecond) // arrive in order
	}
	first.Release()
	wg.Wait()
	for i, v := range order {
		if v != i {
			t.Fatalf("served out of order: %v", order)
		}
	}
}

// A waiter that gives up passes its wake on, so the next waiter is not
// left asleep with a free slot.
func TestWaiterThatLeavesPassesItsWakeOn(t *testing.T) {
	p, _, sv, _ := slotPool(t, 1)
	held, err := p.PickFor(context.Background(), sv, 1, false, nil, time.Second, false, Request{})
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	gone := make(chan error, 1)
	go func() {
		pk, err := p.PickFor(cctx, sv, 2, false, nil, 5*time.Second, false, Request{})
		if err == nil {
			pk.Release()
		}
		gone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	got := make(chan time.Time, 1)
	go func() {
		pk, err := p.PickFor(context.Background(), sv, 3, false, nil, 5*time.Second, false, Request{})
		if err == nil {
			got <- time.Now()
			pk.Release()
		}
	}()
	time.Sleep(20 * time.Millisecond)
	// App 2 is woken first (it came first), then gives up before it runs.
	p.slots.mu.Lock()
	p.slots.releaseLocked(held.Key.ID, sv.ID, 1)
	held.once.Do(func() {})
	cancel()
	p.slots.mu.Unlock()
	if err := <-gone; !errors.Is(err, context.Canceled) && err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(700 * time.Millisecond):
		t.Fatal("the slot freed for a waiter that left never reached the next one")
	}
}

// A key cooling down for less than the queue timeout is waited for; one
// cooling down for longer fails at once.
func TestPickWaitsForACooldownEndingWithinTheQueueTimeout(t *testing.T) {
	p, st, sv, ids := slotPool(t, 1)
	ctx := context.Background()
	soon := time.Now().Add(time.Second)
	if err := st.ApplyEffect(ctx, ids[0], store.KeyEffect{CooldownUntil: &soon}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	pk, err := p.PickFor(ctx, sv, 1, false, nil, 3*time.Second, false, Request{})
	if err != nil {
		t.Fatalf("a key back within the wait should be waited for: %v", err)
	}
	pk.Release()
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("waited %s", time.Since(start))
	}

	later := time.Now().Add(5 * time.Second)
	if err := st.ApplyEffect(ctx, ids[0], store.KeyEffect{CooldownUntil: &later}); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	_, err = p.PickFor(ctx, sv, 1, false, nil, time.Second, false, Request{})
	var nk *NoKeyError
	if !errors.As(err, &nk) || nk.Busy || nk.RetryAfter <= time.Second {
		t.Fatalf("a cooldown past the wait fails with its retry time: %v %+v", err, nk)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a key that is not back in time is not waited for: %s", time.Since(start))
	}
}

// Request.Key binds the pick to one key: it waits for that key's slot and
// never moves to another key; when the key cannot serve, the error says so.
func TestRequiredKeyNeverMoves(t *testing.T) {
	p, st, sv, ids := slotPool(t, 3)
	ctx := context.Background()
	want := ids[1]
	for range 4 {
		pk, err := p.PickFor(ctx, sv, 1, false, nil, 0, false, Request{Key: want})
		if err != nil {
			t.Fatal(err)
		}
		if pk.Key.ID != want {
			t.Fatalf("picked %d, want %d", pk.Key.ID, want)
		}
		pk.Release()
	}
	// Busy: wait for its slot, not another key.
	held, _ := p.PickFor(ctx, sv, 1, false, nil, 0, false, Request{Key: want})
	go func() { time.Sleep(50 * time.Millisecond); held.Release() }()
	pk, err := p.PickFor(ctx, sv, 1, false, nil, 2*time.Second, false, Request{Key: want})
	if err != nil || pk.Key.ID != want {
		t.Fatalf("waited pick: %v", err)
	}
	pk.Release()
	// Excluded (already tried) or disabled: a pinned error, not another key.
	_, err = p.PickFor(ctx, sv, 1, false, map[int64]bool{want: true}, 0, false, Request{Key: want})
	var nk *NoKeyError
	if !errors.As(err, &nk) || !nk.Pinned {
		t.Fatalf("excluded pinned key: %v", err)
	}
	dis := store.StatusDisabled
	if err := st.ApplyEffect(ctx, want, store.KeyEffect{Status: &dis}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.PickFor(ctx, sv, 1, false, nil, 0, false, Request{Key: want}); !errors.As(err, &nk) || !nk.Pinned || nk.RetryAfter != 0 {
		t.Fatalf("disabled pinned key: %v", err)
	}
}

// The wait queue keeps no state for apps that stopped waiting.
func TestWaitQueueForgetsIdleApps(t *testing.T) {
	p, _, sv, _ := slotPool(t, 1)
	ctx := context.Background()
	held, _ := p.PickFor(ctx, sv, 1, false, nil, 0, false, Request{})
	var wg sync.WaitGroup
	for app := range int64(5) {
		wg.Go(func() {
			if pk, err := p.PickFor(ctx, sv, 10+app, false, nil, 2*time.Second, false, Request{}); err == nil {
				time.Sleep(time.Millisecond)
				pk.Release()
			}
		})
	}
	time.Sleep(30 * time.Millisecond)
	held.Release()
	wg.Wait()
	p.slots.mu.Lock()
	defer p.slots.mu.Unlock()
	if q := p.slots.queues[sv.ID]; q == nil || len(q.apps) != 0 || q.woken != 0 {
		t.Fatalf("queue left state behind: %+v", q)
	}
	if len(p.slots.appFlight) != 0 || len(p.slots.inflight) != 0 {
		t.Fatalf("slots left behind: %v %v", p.slots.appFlight, p.slots.inflight)
	}
}
