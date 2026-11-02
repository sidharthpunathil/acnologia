package pool

import (
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Request says what a pick is for, so the provider's offers for that
// endpoint apply. The zero value (a lease, where Acnologia never sees the
// request) gets the provider wide offers only.
type Request struct {
	Method string
	Path   string
}

// pacer keeps each key under the limits its provider publishes in offers
// (30 searches a minute, 1,000 fetches a day) and under an endpoint offer's
// concurrency. It is in memory, like the slots: Acnologia runs as one
// instance, and after a restart the windows simply start empty.
//
// Each window is a ring of sub buckets, so memory is fixed per key and
// window however high the limit is. Counting the whole oldest bucket makes
// it conservative: it never lets more through than the rolling limit, and
// may hold a request back for up to a tenth of the window longer than needed.
type pacer struct {
	mu       sync.Mutex
	wins     map[winKey]*window
	inflight map[flightKey]int
}

type winKey struct {
	keyID int64
	offer string
	per   time.Duration
}

type flightKey struct {
	keyID int64
	offer string
}

// subBuckets is how many buckets one window is split into.
const subBuckets = 10

type window struct {
	bucket time.Duration // per / subBuckets
	ring   [subBuckets + 1]int64
	head   int64 // bucket number of the newest bucket
}

func newPacer() *pacer {
	return &pacer{wins: map[winKey]*window{}, inflight: map[flightKey]int{}}
}

func newWindow(per time.Duration) *window {
	return &window{bucket: max(per/subBuckets, time.Millisecond)}
}

// advance moves the ring to now, clearing buckets that fell out.
func (w *window) advance(now time.Time) {
	b := now.UnixNano() / int64(w.bucket)
	if b <= w.head {
		return
	}
	if b-w.head > int64(len(w.ring)) {
		w.ring = [subBuckets + 1]int64{}
	} else {
		for i := w.head + 1; i <= b; i++ {
			w.ring[i%int64(len(w.ring))] = 0
		}
	}
	w.head = b
}

func (w *window) count() int64 {
	var n int64
	for _, c := range w.ring {
		n += c
	}
	return n
}

// wait is how long until one more unit fits under limit, after advance.
func (w *window) wait(now time.Time, limit int64) time.Duration {
	over := w.count() - limit + 1
	if over <= 0 {
		return 0
	}
	n := int64(len(w.ring))
	var dropped int64
	for b := w.head - n + 1; b <= w.head; b++ {
		dropped += w.ring[((b%n)+n)%n]
		if dropped >= over {
			// Bucket b stops counting once the head moves n buckets past it.
			at := time.Unix(0, (b+n)*int64(w.bucket))
			return max(at.Sub(now), time.Millisecond)
		}
	}
	return time.Duration(n) * w.bucket
}

// limitsOf returns an offer's limits with one entry per window length, the
// smallest count winning, since every limit counts the same requests.
func limitsOf(o *providers.Offer) map[time.Duration]int64 {
	out := map[time.Duration]int64{}
	for _, l := range o.Limits {
		per, ok := providers.LimitWindows[l.Per]
		if !ok || l.Count <= 0 {
			continue
		}
		if c, seen := out[per]; !seen || l.Count < c {
			out[per] = l.Count
		}
	}
	return out
}

func offerID(o *providers.Offer) string { return strings.ToLower(o.Name) }

// check reports whether the key may take one more request under the applied
// offers, and if not, how long until a window frees up (0 when it waits on a
// concurrency slot instead).
func (p *pacer) check(keyID int64, a providers.Applied, now time.Time) (bool, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ok, wait := true, time.Duration(0)
	for _, o := range a.All() {
		for per, limit := range limitsOf(o) {
			w := p.wins[winKey{keyID, offerID(o), per}]
			if w == nil {
				continue
			}
			w.advance(now)
			if d := w.wait(now, limit); d > 0 {
				ok = false
				wait = max(wait, d)
			}
		}
	}
	if s := a.Specific; s != nil && s.Concurrency > 0 && p.inflight[flightKey{keyID, offerID(s)}] >= s.Concurrency {
		ok = false
	}
	return ok, wait
}

// take records one request on the key under the applied offers. The
// returned func frees the endpoint concurrency slot, if one was taken.
func (p *pacer) take(keyID int64, a providers.Applied, now time.Time) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, o := range a.All() {
		for per := range limitsOf(o) {
			k := winKey{keyID, offerID(o), per}
			w := p.wins[k]
			if w == nil {
				w = newWindow(per)
				p.wins[k] = w
			}
			w.advance(now)
			w.ring[w.head%int64(len(w.ring))]++
		}
	}
	s := a.Specific
	if s == nil || s.Concurrency <= 0 {
		return nil
	}
	fk := flightKey{keyID, offerID(s)}
	p.inflight[fk]++
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.inflight[fk] > 1 {
			p.inflight[fk]--
		} else {
			delete(p.inflight, fk)
		}
	}
}

// prune drops windows that have emptied, so deleted keys and edited offers
// do not keep memory.
func (p *pacer) prune(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, w := range p.wins {
		w.advance(now)
		if w.count() == 0 {
			delete(p.wins, k)
		}
	}
}

// ConcurrencyLimit is the key's in-flight cap: the key's override, else the
// service's, else the smallest concurrency a provider wide offer publishes.
// 0 means unlimited.
func ConcurrencyLimit(k *store.Key, svc *store.Service, spec *providers.Spec) int {
	if lim := k.ConcurrencyLimit(svc); lim > 0 || k.MaxConcurrency != nil {
		return lim
	}
	return spec.WideConcurrency()
}
