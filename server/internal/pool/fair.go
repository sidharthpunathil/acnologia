package pool

import (
	"context"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The wait queue: how requests share a busy service.
//
// When every usable key of a service is busy, a request with a queue timeout
// waits in the service's queue. A freed slot wakes one waiter, not all of
// them. The waiter is chosen by app: the app with the fewest requests in
// flight on the service (a wake it has not taken up yet counts as one), then
// the app that was woken longest ago, then the app whose waiter arrived
// first. Within one app, waiters go first in, first out. So one app's swarm
// of requests cannot starve another app that sends one now and then.
//
// While anyone waits on a service, a new request with a queue timeout joins
// the queue too instead of taking a slot first: otherwise the app whose
// request just finished would send its next one and take the freed slot
// before the waiter it was meant for. It goes at once when it is next in
// line and no wake is outstanding.
//
// A woken waiter may still not get a key: it is pinned to another key, or a
// request without a queue timeout took the slot first. It then passes the
// wake on to the next waiter in the same order. Each waiter tries at most
// once per release, so a slot that nobody waiting can use ends the chain
// instead of spinning.
//
// Waiters also wake on a timer: at a known time when a key frees up then (a
// cooldown or a published rate limit ends), else once a second to sweep
// expired leases. A timer wake of a request that waits for a slot retries
// only when that waiter is the one the queue would wake next, so timers do
// not let anyone jump the queue either.

// waiter is one request waiting for a slot.
type waiter struct {
	app int64
	seq uint64
	ch  chan uint64 // receives the generation of the release that woke it
	// woke is the generation of a wake that was sent and not taken yet, and
	// tried the newest generation this waiter tried after. Both are guarded
	// by slots.mu.
	woke, tried uint64
	// cur is a wake the waiter took and has not settled: it gets a key (the
	// wake is used) or passes the wake on. Only its own goroutine uses it.
	cur uint64
}

// svcQueue is one service's wait queue.
type svcQueue struct {
	gen   uint64 // bumped on every release in the service
	seq   uint64 // arrival order
	grant uint64 // wake order
	woken int    // wakes sent and not taken yet, all apps
	apps  map[int64]*appWait
}

// appWait is one app's waiters on a service.
type appWait struct {
	ws    []*waiter // first in, first out
	woken int       // wakes sent to these waiters and not taken yet
	last  uint64    // when the app was last woken (svcQueue.grant)
}

func (s *slots) queueLocked(serviceID int64) *svcQueue {
	q := s.queues[serviceID]
	if q == nil {
		q = &svcQueue{apps: map[int64]*appWait{}}
		s.queues[serviceID] = q
	}
	return q
}

// observe returns the service's release count, read before a pick so a
// release between that pick and joining the queue is not missed, and
// whether requests wait there.
func (s *slots) observe(serviceID int64) (gen uint64, waiting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queueLocked(serviceID)
	return q.gen, len(q.apps) > 0
}

// join queues a waiter for the app. When tried is set, the request just
// tried after release gen, and raced reports that a slot was released
// since, so it should try again before it waits.
func (s *slots) join(serviceID, appID int64, gen uint64, tried bool) (w *waiter, raced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queueLocked(serviceID)
	q.seq++
	w = &waiter{app: appID, seq: q.seq, ch: make(chan uint64, 1), tried: gen}
	aw := q.apps[appID]
	if aw == nil {
		aw = &appWait{}
		q.apps[appID] = aw
	}
	aw.ws = append(aw.ws, w)
	return w, tried && q.gen != gen
}

// took records that the waiter received the wake for release g.
func (s *slots) took(serviceID int64, w *waiter, g uint64) {
	s.mu.Lock()
	if q := s.queues[serviceID]; q != nil && w.woke != 0 {
		w.woke = 0
		q.woken--
		if aw := q.apps[w.app]; aw != nil {
			aw.woken--
		}
	}
	s.mu.Unlock()
	w.cur = g
}

// failed records that the waiter tried after release gen and got no key. A
// wake it took goes on to the next waiter.
func (s *slots) failed(serviceID int64, w *waiter, gen uint64) {
	g := w.cur
	w.cur = 0
	s.mu.Lock()
	defer s.mu.Unlock()
	w.tried = max(w.tried, gen, g)
	if q := s.queues[serviceID]; q != nil && g != 0 {
		s.wakeLocked(serviceID, q, g)
	}
}

// leave takes the waiter out of the queue. A wake it holds, taken or not,
// goes on to the next waiter.
func (s *slots) leave(serviceID int64, w *waiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[serviceID]
	if q == nil {
		return
	}
	aw := q.apps[w.app]
	if aw != nil {
		for i, x := range aw.ws {
			if x == w {
				aw.ws = append(aw.ws[:i], aw.ws[i+1:]...)
				break
			}
		}
	}
	pending := w.woke
	if pending != 0 {
		w.woke = 0
		q.woken--
		if aw != nil {
			aw.woken--
		}
	}
	taken := w.cur
	w.cur = 0
	if aw != nil && len(aw.ws) == 0 {
		delete(q.apps, w.app)
	}
	for _, g := range [2]uint64{taken, pending} {
		if g != 0 {
			s.wakeLocked(serviceID, q, g)
		}
	}
}

// nextLocked is the waiter the queue would wake next among those that have
// not tried since release gen (all of them when gen is 0).
func (s *slots) nextLocked(serviceID int64, q *svcQueue, gen uint64) (*waiter, *appWait) {
	var best *waiter
	var bestAW *appWait
	bestFlight := 0
	for app, aw := range q.apps {
		var c *waiter
		for _, w := range aw.ws {
			if w.woke == 0 && (gen == 0 || w.tried < gen) {
				c = w
				break
			}
		}
		if c == nil {
			continue
		}
		f := s.appFlight[svcApp{serviceID, app}] + aw.woken
		if best == nil || f < bestFlight || (f == bestFlight && (aw.last < bestAW.last || (aw.last == bestAW.last && c.seq < best.seq))) {
			best, bestAW, bestFlight = c, aw, f
		}
	}
	return best, bestAW
}

// wakeLocked wakes the next waiter that has not tried since release gen.
func (s *slots) wakeLocked(serviceID int64, q *svcQueue, gen uint64) {
	w, aw := s.nextLocked(serviceID, q, gen)
	if w == nil {
		return
	}
	q.grant++
	aw.last = q.grant
	aw.woken++
	q.woken++
	w.woke = gen
	w.ch <- gen // never blocks: one wake at a time, and the last one was taken
}

// isNext reports whether the waiter may try now: it holds a wake, or no
// wake is outstanding and it is the waiter the queue would wake next among
// those that have not tried since the last release.
func (s *slots) isNext(serviceID int64, w *waiter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[serviceID]
	if q == nil || w.woke != 0 {
		return true
	}
	if q.woken > 0 {
		return false
	}
	next, _ := s.nextLocked(serviceID, q, q.gen)
	return next == nil || next == w
}

// PickFor is PickWaitOpts for a known request, so the provider's offer for
// that endpoint paces the key. A key at an offer's rate limit counts as busy:
// the pick tries another key, then waits up to wait for a window to free.
// A key whose cooldown ends within the wait counts as soon free, and the
// pick waits for it rather than failing. Waiting requests share freed slots
// fairly between apps (see the top of this file).
func (p *Pool) PickFor(ctx context.Context, svc *store.Service, appID int64, allowHidden bool, exclude map[int64]bool, wait time.Duration, exclusive bool, req Request) (*Picked, error) {
	deadline := time.Now().Add(wait)
	var w *waiter
	defer func() {
		if w != nil {
			p.slots.leave(svc.ID, w)
		}
	}()
	for {
		var gen uint64
		if wait > 0 {
			var waiting bool
			gen, waiting = p.slots.observe(svc.ID)
			if w == nil && waiting {
				// Others wait here already: queue behind them rather than
				// take the slot a wake is on its way to.
				w, _ = p.slots.join(svc.ID, appID, 0, false)
				if !p.slots.isNext(svc.ID, w) && !p.awaitTurn(ctx, svc.ID, w, deadline, false, 0) {
					return nil, ctx.Err()
				}
				continue
			}
		}
		pk, err := p.pick(ctx, svc, appID, allowHidden, exclude, exclusive, req)
		nk, isNoKey := err.(*NoKeyError)
		if err == nil || !isNoKey {
			if err == nil && w != nil {
				w.cur = 0 // the wake it took was used
			}
			return pk, err
		}
		left := time.Until(deadline)
		// No key now, but one comes back from its cooldown before the wait
		// is over: wait for it instead of failing.
		soon := !nk.Busy && nk.RetryAfter > 0 && nk.RetryAfter < left
		if left <= 0 || (!nk.Busy && !soon) {
			return nil, err
		}
		if w == nil {
			var raced bool
			if w, raced = p.slots.join(svc.ID, appID, gen, true); raced {
				continue
			}
		} else {
			p.slots.failed(svc.ID, w, gen)
		}
		// A key that frees up at a known time is retried then; a wait for a
		// slot is woken by the release that frees it.
		timed := soon || nk.Paced
		if !p.awaitTurn(ctx, svc.ID, w, deadline, timed, nk.RetryAfter) {
			return nil, ctx.Err()
		}
	}
}

// awaitTurn waits until the waiter should try again: it was woken, its
// known wait is over, the deadline passed, or a timer found it next in
// line. It returns false when ctx ended.
func (p *Pool) awaitTurn(ctx context.Context, serviceID int64, w *waiter, deadline time.Time, timed bool, retryAfter time.Duration) bool {
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return true
		}
		next := min(left, time.Second)
		if timed && retryAfter > 0 {
			next = min(next, retryAfter)
		}
		t := time.NewTimer(next)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case g := <-w.ch:
			t.Stop()
			p.slots.took(serviceID, w, g)
			return true
		case <-t.C:
			// Expired leases free slots too; the sweep wakes waiters for them.
			p.slots.sweep(time.Now())
		}
		if timed || p.slots.isNext(serviceID, w) {
			return true
		}
	}
}
