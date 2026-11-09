package pool

import (
	"context"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// A proxied answer is shaped by the caller: its path, query, headers and
// body all reach the upstream, and many upstreams echo them back. So one
// app could make a working key look rejected (a 401 from an endpoint it
// may not call, its own Authorization header, text a rule looks for) and
// take a key every other app shares out of rotation. A lasting verdict
// (invalid, banned, exhausted) from a proxied answer therefore only lands
// when something the caller does not control agrees:
//
//   - the provider's health check, a request the server shapes itself,
//     finds the key invalid or out of quota, or
//   - another app got the same verdict for the key within suspectWindow.
//
// Until then the request still moves on to the next key, and the key keeps
// serving everyone else. A success on the key clears what was suspected.

// suspectWindow is how long a suspected verdict waits for a second app.
const suspectWindow = 10 * time.Minute

// checkSpacing bounds the health checks proxied answers can trigger per key.
const checkSpacing = time.Minute

type suspicion struct {
	apps map[int64]time.Time
}

type suspects struct {
	mu      sync.Mutex
	byKey   map[int64]map[providers.Outcome]*suspicion
	checked map[int64]time.Time
}

func (s *suspects) init() {
	if s.byKey == nil {
		s.byKey = map[int64]map[providers.Outcome]*suspicion{}
		s.checked = map[int64]time.Time{}
	}
}

// note records that appID saw outcome o on key and reports whether another
// app saw it too within the window.
func (s *suspects) note(key, appID int64, o providers.Outcome, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	m := s.byKey[key]
	if m == nil {
		m = map[providers.Outcome]*suspicion{}
		s.byKey[key] = m
	}
	sp := m[o]
	if sp == nil {
		sp = &suspicion{apps: map[int64]time.Time{}}
		m[o] = sp
	}
	other := false
	for id, at := range sp.apps {
		if now.Sub(at) > suspectWindow {
			delete(sp.apps, id)
			continue
		}
		if id != appID {
			other = true
		}
	}
	sp.apps[appID] = now
	return other
}

func (s *suspects) clear(key int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey, key)
}

// mayCheck reports whether a proxied answer may trigger a health check on
// key now, and if so takes the turn.
func (s *suspects) mayCheck(key int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if at, ok := s.checked[key]; ok && now.Sub(at) < checkSpacing {
		return false
	}
	s.checked[key] = now
	return true
}

// lasting reports whether an outcome would keep a key out of rotation past
// a short cooldown.
func lasting(o providers.Outcome) bool {
	return o == providers.Invalid || o == providers.Banned || o == providers.QuotaExhausted
}

// ApplyProxied is Apply for the answer to a proxied request made by appID.
// A lasting verdict lands only when corroborated (see above); otherwise the
// key keeps its state and the answer is noted on it.
func (p *Pool) ApplyProxied(ctx context.Context, svc *store.Service, k *store.Key, appID int64, r providers.Result) error {
	if r.Outcome == providers.OK {
		p.suspects.clear(k.ID)
	}
	if !lasting(r.Outcome) || p.corroborated(ctx, svc, k, appID, r.Outcome) {
		return p.Apply(ctx, svc, k, r)
	}
	now := time.Now()
	e := store.KeyEffect{LastError: ptr("unconfirmed " + string(r.Outcome) + ": " + r.Message)}
	applyQuotaReading(&e, r, now)
	return p.Store.ApplyEffect(ctx, k.ID, e)
}

func (p *Pool) corroborated(ctx context.Context, svc *store.Service, k *store.Key, appID int64, o providers.Outcome) bool {
	now := time.Now()
	if p.suspects.note(k.ID, appID, o, now) {
		return true
	}
	// A key borrowed from its key source is checked against its own service
	// by the worker, never against the service that borrowed it.
	if k.ServiceID != svc.ID || providers.Get(svc.Provider).Health == nil || !p.suspects.mayCheck(k.ID, now) {
		return false
	}
	if err := p.Check(context.WithoutCancel(ctx), svc, k); err != nil {
		return false
	}
	fresh, err := p.Store.GetKey(ctx, k.ID)
	if err != nil {
		return false
	}
	switch o {
	case providers.QuotaExhausted:
		return fresh.Status == store.StatusExhausted || fresh.Status == store.StatusInvalid
	default:
		return fresh.Status == store.StatusInvalid || fresh.Status == store.StatusBanned
	}
}
