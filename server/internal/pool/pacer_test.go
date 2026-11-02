package pool

import (
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

func searchSpec() *providers.Spec {
	return &providers.Spec{Offers: []providers.Offer{
		{Name: "Search", Match: &providers.OfferMatch{Method: "GET", Path: "/search"}, Free: true,
			Limits: []providers.Limit{{Count: 3, Per: "minute"}, {Count: 5, Per: "hour"}}},
		{Name: "Agent", Match: &providers.OfferMatch{Path: "/run"}, Concurrency: 1},
		{Name: "Account", Limits: []providers.Limit{{Count: 100, Per: "day"}}},
	}}
}

func TestPacerHoldsAKeyAtItsMinuteLimit(t *testing.T) {
	p := newPacer()
	a := searchSpec().OffersFor("GET", "/search")
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if ok, _ := p.check(1, a, now); !ok {
			t.Fatalf("request %d should fit", i+1)
		}
		p.take(1, a, now)
	}
	ok, wait := p.check(1, a, now)
	if ok || wait <= 0 || wait > time.Minute+6*time.Second {
		t.Fatalf("fourth request: ok=%v wait=%v", ok, wait)
	}
	// Another key has its own windows.
	if ok, _ := p.check(2, a, now); !ok {
		t.Fatal("key 2 should be free")
	}
	// Other endpoints are not held by the search limit.
	if ok, _ := p.check(1, searchSpec().OffersFor("POST", "/fetch"), now); !ok {
		t.Fatal("an unrelated endpoint should not be paced by the search offer")
	}
	// Once the window has passed (plus one sub bucket), the key fits again.
	later := now.Add(wait)
	if ok, _ := p.check(1, a, later); !ok {
		t.Fatalf("after %v the key should fit again", wait)
	}
	// The hour limit (5) still binds after two more.
	p.take(1, a, later)
	p.take(1, a, later)
	if ok, w := p.check(1, a, later.Add(2*time.Minute)); ok || w < 50*time.Minute {
		t.Fatalf("hour limit: ok=%v wait=%v", ok, w)
	}
}

func TestPacerNeverExceedsTheRollingWindow(t *testing.T) {
	p := newPacer()
	spec := &providers.Spec{Offers: []providers.Offer{{Name: "x", Limits: []providers.Limit{{Count: 10, Per: "second"}}}}}
	a := spec.OffersFor("", "")
	start := time.Unix(1_800_000_000, 0)
	var taken []time.Time
	for ms := 0; ms < 5000; ms += 7 {
		now := start.Add(time.Duration(ms) * time.Millisecond)
		if ok, _ := p.check(9, a, now); ok {
			p.take(9, a, now)
			taken = append(taken, now)
		}
	}
	if len(taken) < 30 {
		t.Fatalf("too conservative: only %d requests in 5s at 10/s", len(taken))
	}
	for i := range taken {
		n := 0
		for j := i; j < len(taken) && taken[j].Sub(taken[i]) < time.Second; j++ {
			n++
		}
		if n > 10 {
			t.Fatalf("%d requests within one second starting at %v", n, taken[i].Sub(start))
		}
	}
}

func TestPacerEndpointConcurrency(t *testing.T) {
	p := newPacer()
	a := searchSpec().OffersFor("POST", "/run/abc")
	if a.Specific == nil || a.Specific.Name != "Agent" {
		t.Fatalf("specific = %+v", a.Specific)
	}
	now := time.Now()
	release := p.take(1, a, now)
	if ok, wait := p.check(1, a, now); ok || wait != 0 {
		t.Fatalf("second run on the key: ok=%v wait=%v", ok, wait)
	}
	release()
	if ok, _ := p.check(1, a, now); !ok {
		t.Fatal("released slot should free the key")
	}
}

func TestPacerPrunesEmptyWindows(t *testing.T) {
	p := newPacer()
	a := searchSpec().OffersFor("GET", "/search")
	now := time.Now()
	p.take(1, a, now)
	if len(p.wins) != 3 {
		t.Fatalf("windows = %d", len(p.wins))
	}
	p.prune(now.Add(30 * time.Hour))
	if len(p.wins) != 0 {
		t.Fatalf("windows after prune = %d", len(p.wins))
	}
}
