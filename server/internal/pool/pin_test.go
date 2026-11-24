package pool

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// A pinned pick (an Idempotency-Key) lands on the same key every time while
// that key is free, whatever the strategy would choose, and falls back to the
// strategy when the key cannot take the request.
func TestPinnedPickIsDeterministic(t *testing.T) {
	p, st, sv, _ := leastUsedPool(t)
	ctx := context.Background()
	pickOne := func(pin string, exclude map[int64]bool) int64 {
		t.Helper()
		pk, err := p.PickFor(ctx, sv, 0, false, exclude, 0, false, Request{Method: "POST", Path: "/send", Pin: pin})
		if err != nil {
			t.Fatal(err)
		}
		pk.Release()
		return pk.Key.ID
	}
	seen := map[int64]bool{}
	for i := range 8 {
		pin := fmt.Sprint("idem|1|t1|svc|POST|/send?|key-", i)
		first := pickOne(pin, nil)
		seen[first] = true
		for range 5 {
			if got := pickOne(pin, nil); got != first {
				t.Fatalf("pin %q moved from key %d to %d", pin, first, got)
			}
		}
	}
	if len(seen) < 2 {
		t.Fatalf("different pins should spread over the keys, all went to %v", seen)
	}

	// The pinned key excluded (already tried in this request) or cooling
	// down: the pick falls back to another key instead of failing.
	pin := "idem|1|t1|svc|POST|/send?|fallback"
	home := pickOne(pin, nil)
	if got := pickOne(pin, map[int64]bool{home: true}); got == home {
		t.Fatal("an excluded pinned key must not be picked")
	}
	until := time.Now().Add(time.Minute)
	if err := p.EgressFailed(ctx, mustKey(t, st, home), "dial tcp: refused"); err != nil {
		t.Fatal(err)
	}
	if got := pickOne(pin, nil); got == home {
		t.Fatal("a key parked after its egress failed must be skipped")
	}
	k := mustKey(t, st, home)
	if k.CooldownUntil == nil || k.CooldownUntil.After(until) || k.ConsecutiveFails != 0 {
		t.Fatalf("egress failure should park the key briefly without climbing its backoff: %+v", k)
	}
}

func mustKey(t *testing.T, st *store.Store, id int64) *store.Key {
	t.Helper()
	k, err := st.GetKey(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
