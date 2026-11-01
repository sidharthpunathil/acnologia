// Package breaker stops traffic to a service whose provider is failing.
//
// A key that is rate limited or out of quota is the key's problem and is
// handled by cooldowns. A provider that answers 5xx or cannot be reached is
// everyone's problem: trying the next key only burns attempts. After enough
// such failures inside a window the breaker opens and requests fail fast.
// When the reset time passes one probe request is let through (half open);
// its result closes the breaker or opens it again.
package breaker

import (
	"sync"
	"time"
)

type State string

const (
	Closed   State = "closed"
	Open     State = "open"
	HalfOpen State = "half_open"
)

// Settings for one service. Threshold <= 0 disables the breaker.
type Settings struct {
	Threshold int
	Window    time.Duration
	Reset     time.Duration
}

type entry struct {
	state    State
	failures []time.Time
	openedAt time.Time
	probing  bool
	reason   string
}

// Status is a snapshot for the dashboard.
type Status struct {
	State      State      `json:"state"`
	Failures   int        `json:"failures"`
	OpenedAt   *time.Time `json:"opened_at"`
	RetryAfter float64    `json:"retry_after_sec"`
	Reason     string     `json:"reason"`
}

// Set holds one breaker per service id. The zero value is not usable; use New.
type Set struct {
	mu  sync.Mutex
	m   map[int64]*entry
	now func() time.Time

	// OnChange is called outside the lock when a breaker opens (down=true)
	// or closes again (down=false).
	OnChange func(serviceID int64, down bool, reason string)
}

func New() *Set { return &Set{m: map[int64]*entry{}, now: time.Now} }

func (s *Set) get(id int64) *entry {
	e, ok := s.m[id]
	if !ok {
		e = &entry{state: Closed}
		s.m[id] = e
	}
	return e
}

// Allow reports whether a request may go to the service. When it returns
// false, retryAfter says how long until the next probe. A true answer while
// the breaker is half open makes the caller the probe: it must report
// Success, Failure or Abort.
func (s *Set) Allow(id int64, cfg Settings) (ok bool, retryAfter time.Duration) {
	if cfg.Threshold <= 0 {
		return true, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.get(id)
	now := s.now()
	switch e.state {
	case Open:
		left := e.openedAt.Add(cfg.Reset).Sub(now)
		if left > 0 {
			return false, left
		}
		e.state, e.probing = HalfOpen, true
		return true, 0
	case HalfOpen:
		if e.probing {
			return false, time.Second
		}
		e.probing = true
		return true, 0
	}
	return true, 0
}

// Success records that the provider answered without a provider-side error.
func (s *Set) Success(id int64) {
	s.mu.Lock()
	e, ok := s.m[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	was := e.state
	e.state, e.failures, e.probing, e.reason = Closed, nil, false, ""
	s.mu.Unlock()
	if was != Closed && s.OnChange != nil {
		s.OnChange(id, false, "")
	}
}

// Failure records a provider-side error (5xx, timeout, connection refused).
func (s *Set) Failure(id int64, cfg Settings, reason string) {
	if cfg.Threshold <= 0 {
		return
	}
	s.mu.Lock()
	e := s.get(id)
	now := s.now()
	opened := false
	switch e.state {
	case HalfOpen:
		e.state, e.openedAt, e.probing, e.reason = Open, now, false, reason
	case Open:
		// A request that started before the breaker opened. Nothing to change.
	default:
		cut := now.Add(-cfg.Window)
		kept := e.failures[:0]
		for _, t := range e.failures {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		e.failures = append(kept, now)
		if len(e.failures) >= cfg.Threshold {
			e.state, e.openedAt, e.reason = Open, now, reason
			opened = true
		}
	}
	s.mu.Unlock()
	if opened && s.OnChange != nil {
		s.OnChange(id, true, reason)
	}
}

// Abort gives up the probe slot without a verdict, for example when no key
// was available to make the probe request with.
func (s *Set) Abort(id int64) {
	s.mu.Lock()
	if e, ok := s.m[id]; ok && e.state == HalfOpen {
		e.probing = false
	}
	s.mu.Unlock()
}

// Reset closes the breaker by hand.
func (s *Set) Reset(id int64) { s.Success(id) }

func (s *Set) Status(id int64, cfg Settings) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok {
		return Status{State: Closed}
	}
	st := Status{State: e.state, Failures: len(e.failures), Reason: e.reason}
	if e.state != Closed {
		t := e.openedAt
		st.OpenedAt = &t
		if e.state == Open {
			if left := e.openedAt.Add(cfg.Reset).Sub(s.now()); left > 0 {
				st.RetryAfter = left.Seconds()
			}
		}
	}
	return st
}
