package pool

import (
	"context"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
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
	inflight map[int64]int        // key id -> requests in flight
	leases   map[string]leaseSlot // lease id -> held slot
	excl     map[int64]string     // key id -> lease id holding it exclusively
	// pending counts picks whose usage write has not committed yet, by key
	// id. Pick writes usage outside the service lock, so least_used adds
	// these to the stored usage to keep spreading a burst evenly.
	pending map[int64]int64
	// appFlight counts the slots each app holds on each service, which the
	// wait queue uses to share freed slots fairly (fair.go).
	appFlight map[svcApp]int
	// queues are the requests waiting for a slot, per service (fair.go).
	queues map[int64]*svcQueue
}

type svcApp struct{ svc, app int64 }

type leaseSlot struct {
	keyID     int64
	serviceID int64
	appID     int64
	expires   time.Time
}

func newSlots() *slots {
	return &slots{inflight: map[int64]int{}, leases: map[string]leaseSlot{}, excl: map[int64]string{}, pending: map[int64]int64{},
		appFlight: map[svcApp]int{}, queues: map[int64]*svcQueue{}}
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

func (s *slots) acquire(keyID, serviceID, appID int64) {
	s.mu.Lock()
	s.inflight[keyID]++
	s.appFlight[svcApp{serviceID, appID}]++
	s.mu.Unlock()
}

// addPending moves the key's count of uncommitted usage writes by d.
func (s *slots) addPending(keyID, d int64) {
	s.mu.Lock()
	if n := s.pending[keyID] + d; n > 0 {
		s.pending[keyID] = n
	} else {
		delete(s.pending, keyID)
	}
	s.mu.Unlock()
}

// pendingSnapshot copies the uncommitted usage writes per key, or returns
// nil when there are none.
func (s *slots) pendingSnapshot() map[int64]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := make(map[int64]int64, len(s.pending))
	for k, v := range s.pending {
		out[k] = v
	}
	return out
}

// release frees one slot on the key and hands it to the next waiter for
// the service.
func (s *slots) release(keyID, serviceID, appID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked(keyID, serviceID, appID)
}

func (s *slots) releaseLocked(keyID, serviceID, appID int64) {
	if s.inflight[keyID] > 1 {
		s.inflight[keyID]--
	} else {
		delete(s.inflight, keyID)
	}
	sa := svcApp{serviceID, appID}
	if s.appFlight[sa] > 1 {
		s.appFlight[sa]--
	} else {
		delete(s.appFlight, sa)
	}
	if q := s.queues[serviceID]; q != nil {
		q.gen++
		s.wakeLocked(serviceID, q, q.gen)
	}
}

func (s *slots) holdLease(id string, keyID, serviceID, appID int64, expires time.Time) {
	s.mu.Lock()
	s.leases[id] = leaseSlot{keyID: keyID, serviceID: serviceID, appID: appID, expires: expires}
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
		s.releaseLocked(l.keyID, l.serviceID, l.appID)
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
			s.releaseLocked(l.keyID, l.serviceID, l.appID)
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
	p.slots.holdLease(leaseID, pk.Key.ID, pk.svcID, pk.appID, expires)
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
func (p *Pool) SweepLeases() int {
	now := time.Now()
	p.pacer.prune(now)
	return p.slots.sweep(now)
}

// RestoreLeases re-acquires slots for open leases after a restart.
func (p *Pool) RestoreLeases(ctx context.Context) error {
	ls, err := p.Store.OpenLeases(ctx)
	if err != nil {
		return err
	}
	excl, _ := p.Store.ExclusiveLeaseIDs(ctx)
	for _, l := range ls {
		p.slots.acquire(l.KeyID, l.ServiceID, l.AppID)
		p.slots.holdLease(l.ID, l.KeyID, l.ServiceID, l.AppID, l.ExpiresAt)
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
	return p.PickFor(ctx, svc, appID, allowHidden, exclude, wait, exclusive, Request{})
}
