package api

import (
	"context"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/breaker"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Routing across services: a group (several services with one API shape)
// and a capability (several services doing one job with requests of their
// own shape) both hold a list of candidates and a strategy that picks which
// goes first. The rest follow as fallbacks, so a request still reaches a
// provider that works when the first choice is out of keys or down.

// Strategies, shared by groups and capabilities.
const (
	StrategyPriority   = "priority"    // the listed order
	StrategyRoundRobin = "round_robin" // each request starts at the next one
	StrategyRandom     = "random"      // a shuffle per request
	StrategyWeighted   = "weighted"    // random, in proportion to weights
	StrategyCheapest   = "cheapest"    // lowest known price per request first, unpriced last
	StrategyMostKeys   = "most_keys"   // most keys free right now first
	// StrategyReliable puts the highest measured success rate first (the
	// endpoint's over the last 7 days, else the service's), providers with
	// too few calls to tell last, and breaks ties by median latency.
	StrategyReliable = "reliable"
)

var routingStrategies = map[string]bool{
	StrategyPriority: true, StrategyRoundRobin: true, StrategyRandom: true,
	StrategyWeighted: true, StrategyCheapest: true, StrategyMostKeys: true,
	StrategyReliable: true,
}

// routeCand is one candidate the strategy orders.
type routeCand struct {
	idx    int
	sv     *store.Service
	weight int
	cost   float64
	// unpriced means no price is known (no deciding offer, no cost per
	// request): its cost of 0 is not free, so cheapest puts it last.
	unpriced bool
	keys     int
	// down is set when the service's breaker is open or it has no key free
	// now. Such candidates go last whatever the strategy, so the request
	// tries a working provider first and still has them as a last resort.
	down bool
	// evidence reads the candidate's measured success rate and median
	// latency for this request; known is false with too few calls. Read
	// only by the reliable strategy.
	evidence func() (rate float64, p50 int64, known bool)
}

var rrCounters sync.Map // slug -> *atomic.Uint64

// orderRoutes orders candidates for one request.
func orderRoutes(slug, strategy string, cands []routeCand) []routeCand {
	out := append([]routeCand(nil), cands...)
	n := len(out)
	if n < 2 {
		return out
	}
	switch strategy {
	case StrategyRoundRobin:
		v, _ := rrCounters.LoadOrStore(slug, new(atomic.Uint64))
		start := int((v.(*atomic.Uint64).Add(1) - 1) % uint64(n))
		out = append(out[start:], out[:start]...)
	case StrategyRandom:
		rand.Shuffle(n, func(i, j int) { out[i], out[j] = out[j], out[i] })
	case StrategyWeighted:
		out = weightedOrder(out)
	case StrategyCheapest:
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].unpriced != out[j].unpriced {
				return !out[i].unpriced
			}
			return !out[i].unpriced && out[i].cost < out[j].cost
		})
	case StrategyMostKeys:
		sort.SliceStable(out, func(i, j int) bool { return out[i].keys > out[j].keys })
	case StrategyReliable:
		out = reliableOrder(out)
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].down && out[j].down })
	return out
}

// reliableOrder sorts by measured success rate, highest first, unknown
// last, then by median latency, lowest first (an unknown latency last).
func reliableOrder(in []routeCand) []routeCand {
	type ev struct {
		rate  float64
		p50   int64
		known bool
	}
	evs := make([]ev, len(in))
	for i, c := range in {
		if c.evidence != nil {
			evs[i].rate, evs[i].p50, evs[i].known = c.evidence()
		}
	}
	idx := make([]int, len(in))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		x, y := evs[idx[a]], evs[idx[b]]
		if x.known != y.known {
			return x.known
		}
		if !x.known {
			return false
		}
		if x.rate != y.rate {
			return x.rate > y.rate
		}
		if (x.p50 > 0) != (y.p50 > 0) {
			return x.p50 > 0
		}
		return x.p50 < y.p50
	})
	out := make([]routeCand, len(in))
	for i, j := range idx {
		out[i] = in[j]
	}
	return out
}

// weightedOrder draws candidates without replacement, each in proportion to
// its weight (at least 1).
func weightedOrder(in []routeCand) []routeCand {
	left := append([]routeCand(nil), in...)
	out := make([]routeCand, 0, len(in))
	for len(left) > 0 {
		total := 0
		for _, c := range left {
			total += max(c.weight, 1)
		}
		pick := rand.IntN(total)
		for i, c := range left {
			pick -= max(c.weight, 1)
			if pick < 0 {
				out = append(out, c)
				left = append(left[:i], left[i+1:]...)
				break
			}
		}
	}
	return out
}

// keyCounts caches the store's free key counts for a few seconds: routing
// reads them on every request, and an exact count is not needed to prefer
// a provider that has keys over one that has none.
type keyCounts struct {
	mu   sync.Mutex
	at   time.Time
	data map[int64]int
}

const keyCountsTTL = 3 * time.Second

func (s *Server) freeKeys(ctx context.Context) map[int64]int {
	kc := &s.keyCounts
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.data != nil && time.Since(kc.at) < keyCountsTTL {
		return kc.data
	}
	m, err := s.Store.AvailableKeyCounts(ctx)
	if err != nil {
		if kc.data != nil {
			return kc.data
		}
		return map[int64]int{}
	}
	kc.data, kc.at = m, time.Now()
	return m
}

// candidate describes one service for orderRoutes.
func (s *Server) candidate(ctx context.Context, idx int, sv *store.Service, weight int, method, path string) routeCand {
	prov := providers.Get(sv.Provider)
	free := s.freeKeys(ctx)
	st := s.Breakers.Status(sv.ID, breakerSettings(prov))
	keys := free[sv.ID]
	cost, known := costKnown(sv, prov, method, "/"+trimSlash(path))
	return routeCand{idx: idx, sv: sv, weight: weight, cost: cost, unpriced: !known,
		keys: keys, down: st.State == breaker.Open || keys == 0,
		evidence: func() (float64, int64, bool) { return s.routeEvidence(ctx, sv, method, path) }}
}

func trimSlash(p string) string {
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	return p
}

// orderGroup orders a group's members for one request.
func (s *Server) orderGroup(ctx context.Context, slug, strategy string, svcs []*store.Service, method, path string) []*store.Service {
	if len(svcs) < 2 {
		return svcs
	}
	cands := make([]routeCand, len(svcs))
	for i, sv := range svcs {
		cands[i] = s.candidate(ctx, i, sv, 1, method, path)
	}
	out := make([]*store.Service, 0, len(svcs))
	for _, c := range orderRoutes(slug, strategy, cands) {
		out = append(out, c.sv)
	}
	return out
}
