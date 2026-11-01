package pool

import (
	"context"
	"sync"
	"time"

	"github.com/cogito-keys/cogito/internal/store"
)

// slots tracks in-flight requests per key so providers with a concurrency
// cap (ElevenLabs) never see more parallel requests on one key than allowed.
// A slot is held by a proxied request until its response finishes, or by a
// lease until it is reported or expires.
//
// State is in memory: Acnologia runs as a single instance. Open leases are
// restored from the database on start.
type slots struct {
	mu       sync.Mutex
	inflight map[int64]int           // key id -> requests in flight
	leases   map[string]leaseSlot    // lease id -> held slot
	wake     map[int64]chan struct{} // service id -> closed when a slot frees up
	excl     map[int64]string        // key id -> lease id holding it exclusively
}

type leaseSlot struct {
	keyID     int64
	serviceID int64
	expires   time.Time
}

func newSlots() *slots {
	return &slots{inflight: map[int64]int{}, leases: map[string]leaseSlot{}, wake: map[int64]chan struct{}{}, excl: map[int64]string{}}
}

func (s *slots) count(keyID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight[keyID]
}

// exclusiveHeld reports whether a lease holds the key for itself alone.
func (s *slots) exclusiveHeld(keyID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.excl[keyID]
	return ok
}

func (s *slots) acquire(keyID int64) {
	s.mu.Lock()
	s.inflight[keyID]++
	s.mu.Unlock()
}

// release frees one slot on the key and wakes waiters for the service.
func (s *slots) release(keyID, serviceID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked(keyID, serviceID)
}

func (s *slots) releaseLocked(keyID, serviceID int64) {
	if s.inflight[keyID] > 1 {
		s.inflight[keyID]--
	} else {
		delete(s.inflight, keyID)
	}
	if ch, ok := s.wake[serviceID]; ok {
		close(ch)
		delete(s.wake, serviceID)
	}
}

// waitCh returns a channel closed on the next release in the service.
func (s *slots) waitCh(serviceID int64) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.wake[serviceID]
	if !ok {
		ch = make(chan struct{})
		s.wake[serviceID] = ch
	}
	return ch
}

func (s *slots) holdLease(id string, keyID, serviceID int64, expires time.Time) {
	s.mu.Lock()
	s.leases[id] = leaseSlot{keyID: keyID, serviceID: serviceID, expires: expires}
	s.mu.Unlock()
}

// dropLease releases the slot a lease holds. It is safe to call twice.
func (s *slots) dropLease(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.leases[id]; ok {
		delete(s.leases, id)
		if s.excl[l.keyID] == id {
			delete(s.excl, l.keyID)
		}
		s.releaseLocked(l.keyID, l.serviceID)
	}
}

// renew moves a held lease's expiry.
func (s *slots) renew(id string, expires time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[id]
	if ok {
		l.expires = expires
		s.leases[id] = l
	}
	return ok
}

// sweep releases slots held by expired leases.
func (s *slots) sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, l := range s.leases {
		if !l.expires.After(now) {
			delete(s.leases, id)
			if s.excl[l.keyID] == id {
				delete(s.excl, l.keyID)
			}
			s.releaseLocked(l.keyID, l.serviceID)
			n++
		}
	}
	return n
}

func (s *slots) snapshot() map[int64]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]int, len(s.inflight))
	for k, v := range s.inflight {
		out[k] = v
	}
	return out
}

// InFlight returns the number of requests in flight per key id.
func (p *Pool) InFlight() map[int64]int { return p.slots.snapshot() }

// HoldLease ties the picked key's slot to a lease instead of a request.
func (p *Pool) HoldLease(pk *Picked, leaseID string, expires time.Time) {
	pk.once.Do(func() {}) // the lease owns the slot now; Release becomes a no-op
	p.slots.holdLease(leaseID, pk.Key.ID, pk.Key.ServiceID, expires)
	if pk.exclusive {
		p.slots.mu.Lock()
		p.slots.excl[pk.Key.ID] = leaseID
		p.slots.mu.Unlock()
	}
}

// RenewLease extends a held lease. It reports false when the lease already ended.
func (p *Pool) RenewLease(leaseID string, expires time.Time) bool {
	return p.slots.renew(leaseID, expires)
}

// ExclusiveHeld reports whether a key is reserved by an exclusive lease.
func (p *Pool) ExclusiveHeld(keyID int64) bool { return p.slots.exclusiveHeld(keyID) }

// ReleaseLease frees the slot a lease holds.
func (p *Pool) ReleaseLease(leaseID string) { p.slots.dropLease(leaseID) }

// SweepLeases frees slots held by expired leases.
func (p *Pool) SweepLeases() int { return p.slots.sweep(time.Now()) }

// RestoreLeases re-acquires slots for open leases after a restart.
func (p *Pool) RestoreLeases(ctx context.Context) error {
	ls, err := p.Store.OpenLeases(ctx)
	if err != nil {
		return err
	}
	excl, _ := p.Store.ExclusiveLeaseIDs(ctx)
	for _, l := range ls {
		p.slots.acquire(l.KeyID)
		p.slots.holdLease(l.ID, l.KeyID, l.ServiceID, l.ExpiresAt)
		if excl[l.ID] {
			p.slots.mu.Lock()
			p.slots.excl[l.KeyID] = l.ID
			p.slots.mu.Unlock()
		}
	}
	return nil
}

// PickWait is Pick that waits up to wait for a slot when every usable key is busy.
func (p *Pool) PickWait(ctx context.Context, svc *store.Service, appID int64, allowHidden bool, exclude map[int64]bool, wait time.Duration) (*Picked, error) {
	return p.PickWaitOpts(ctx, svc, appID, allowHidden, exclude, wait, false)
}

// PickWaitOpts is PickWait that can ask for an exclusive key: one with
// nothing in flight, which no one else is handed until the lease ends.
func (p *Pool) PickWaitOpts(ctx context.Context, svc *store.Service, appID int64, allowHidden bool, exclude map[int64]bool, wait time.Duration, exclusive bool) (*Picked, error) {
	deadline := time.Now().Add(wait)
	for {
		// Grab the wake channel before trying, so a release between the
		// attempt and the wait is not missed.
		ch := p.slots.waitCh(svc.ID)
		pk, err := p.pick(ctx, svc, appID, allowHidden, exclude, exclusive)
		nk, isNoKey := err.(*NoKeyError)
		if err == nil || !isNoKey || !nk.Busy {
			return pk, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, err
		}
		// Expired leases free slots too, so wake up at least once a second.
		t := time.NewTimer(min(left, time.Second))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-ch:
		case <-t.C:
			p.slots.sweep(time.Now())
		}
		t.Stop()
	}
}
