package pool

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// leastUsedPool makes a least_used service whose 4 keys start at usage 0, 8,
// 16 and 24, so 64 more picks level them at 28 each.
func leastUsedPool(t *testing.T) (*Pool, *store.Store, *store.Service, []int64) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypto.New(bytes.Repeat([]byte{7}, 32))
	ctx := context.Background()
	sv, err := st.CreateService(ctx, &store.Service{Slug: "s", Name: "S", Provider: "generic", BaseURL: "https://x.example", AuthType: "header",
		AuthName: "Authorization", Strategy: "least_used", CooldownSec: 1, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 4 {
		enc, _ := box.Encrypt(fmt.Sprint("secret-", i))
		k, err := st.CreateKey(ctx, store.NewKey{ServiceID: sv.ID, Label: fmt.Sprint("k", i), SecretEnc: enc, SecretFP: fmt.Sprint("fp", i), Hint: "x", Tier: "primary", Weight: 1, Priority: 100})
		if err != nil {
			t.Fatal(err)
		}
		for range 8 * i {
			if err := st.MarkUsed(ctx, k.ID); err != nil {
				t.Fatal(err)
			}
		}
		ids = append(ids, k.ID)
	}
	return New(st, box), st, sv, ids
}

// pickConcurrently runs n picks at once and returns them once all have returned.
func pickConcurrently(t *testing.T, p *Pool, sv *store.Service, n int) []*Picked {
	t.Helper()
	var mu sync.Mutex
	var held []*Picked
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			pk, err := p.Pick(context.Background(), sv, 0, false, nil)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			held = append(held, pk)
			mu.Unlock()
		})
	}
	wg.Wait()
	return held
}

// checkLevelled asserts that every pick's usage committed and that the keys'
// usage differs by at most one. A usage write that commits just before its
// pending count drops can be counted twice by a concurrent pick, so the
// spread may be off by one, never more.
func checkLevelled(t *testing.T, p *Pool, st *store.Store, ids []int64, held []*Picked) {
	t.Helper()
	usage := map[int64]int64{}
	var total, lo, hi int64 = 0, 1 << 62, 0
	for _, id := range ids {
		k, err := st.GetKey(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		usage[id] = k.UsageToday
		total += k.UsageToday
		lo, hi = min(lo, k.UsageToday), max(hi, k.UsageToday)
	}
	if want := int64(len(held) + 48); total != want {
		t.Errorf("usage_today sums to %d, want %d", total, want)
	}
	if hi-lo > 1 {
		t.Errorf("usage %v: max minus min is %d, want at most 1", usage, hi-lo)
	}
	if n := len(p.slots.pendingSnapshot()); n != 0 {
		t.Errorf("%d keys still have pending usage writes", n)
	}
	for _, pk := range held {
		pk.Release()
	}
	if n := len(p.InFlight()); n != 0 {
		t.Errorf("%d keys still hold slots after release", n)
	}
}

// Pick writes usage after it lets go of the service lock. least_used must
// still count the writes in flight, or a burst all goes to one key.
func TestLeastUsedCountsPendingUsage(t *testing.T) {
	t.Run("writes_held", func(t *testing.T) {
		// Hold the only writer connection so no usage write can commit until
		// every pick has chosen its key: only the pending counts can spread them.
		p, st, sv, ids := leastUsedPool(t)
		conn, err := st.DB.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan []*Picked)
		go func() { done <- pickConcurrently(t, p, sv, 64) }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var n int64
			for _, v := range p.slots.pendingSnapshot() {
				n += v
			}
			if n == 64 {
				break
			}
			if time.Now().After(deadline) {
				conn.Close()
				t.Fatalf("only %d of 64 picks reached their usage write", n)
			}
			time.Sleep(time.Millisecond)
		}
		conn.Close()
		checkLevelled(t, p, st, ids, <-done)
	})
	t.Run("free_running", func(t *testing.T) {
		for range 10 {
			p, st, sv, ids := leastUsedPool(t)
			checkLevelled(t, p, st, ids, pickConcurrently(t, p, sv, 64))
		}
	})
}
