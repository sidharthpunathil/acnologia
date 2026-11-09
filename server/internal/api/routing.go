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
	StrategyCheapest   = "cheapest"    // lowest price per request first, from offers
	StrategyMostKeys   = "most_keys"   // most keys free right now first
)

var routingStrategies = map[string]bool{
	StrategyPriority: true, StrategyRoundRobin: true, StrategyRandom: true,
	StrategyWeighted: true, StrategyCheapest: true, StrategyMostKeys: true,
}

// routeCand is one candidate the strategy orders.
type routeCand struct {
	idx    int
	sv     *store.Service
	weight int
	cost   float64
	keys   int
	// down is set when the service's breaker is open or it has no key free
	// now. Such candidates go last whatever the strategy, so the request
	// tries a working provider first and still has them as a last resort.
	down bool
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
		sort.SliceStable(out, func(i, j int) bool { return out[i].cost < out[j].cost })
	case StrategyMostKeys:
		sort.SliceStable(out, func(i, j int) bool { return out[i].keys > out[j].keys })
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].down && out[j].down })
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
	return routeCand{idx: idx, sv: sv, weight: weight, cost: costPerRequest(sv, prov, method, "/"+trimSlash(path)),
		keys: keys, down: st.State == breaker.Open || keys == 0}
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
