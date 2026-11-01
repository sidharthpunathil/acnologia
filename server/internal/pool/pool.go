// Package pool picks keys by availability and applies outcomes to them.
package pool

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
)

const maxCooldown = time.Hour

// maxRetryAfter caps any cooldown a single outcome can set.
const maxRetryAfter = 24 * time.Hour

var Strategies = []string{"round_robin", "fill_first", "least_used", "most_quota", "random"}

func ValidStrategy(s string) bool {
	for _, x := range Strategies {
		if x == s {
			return true
		}
	}
	return false
}

// NoKeyError means no key is available right now. Busy is set when usable
// keys exist but every one of them is at its concurrency limit.
type NoKeyError struct {
	RetryAfter time.Duration
	Busy       bool
}

func (e *NoKeyError) Error() string {
	if e.Busy {
		return "all keys are busy"
	}
	return "no key available"
}

type Pool struct {
	Store  *store.Store
	Box    *crypto.Box
	Client *http.Client

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
	slots *slots
}

func New(s *store.Store, box *crypto.Box) *Pool {
	return &Pool{
		Store: s, Box: box, locks: map[int64]*sync.Mutex{}, slots: newSlots(),
		Client: &http.Client{
			Timeout: 20 * time.Second,
			// Never follow a redirect with a key attached: Go forwards custom
			// auth headers (x-api-key and friends) to the new host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (p *Pool) lock(serviceID int64) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.locks[serviceID]
	if !ok {
		l = &sync.Mutex{}
		p.locks[serviceID] = l
	}
	return l
}

// Picked is a selected key with its decrypted secret. It holds one
// concurrency slot on the key until Release is called (or, for a lease,
// until the lease is reported or expires).
type Picked struct {
	Key    *store.Key
	Secret string

	once    sync.Once
	release func()
}

// Release frees the key's concurrency slot. Safe to call more than once.
func (pk *Picked) Release() {
	pk.once.Do(func() {
		if pk.release != nil {
			pk.release()
		}
	})
}

// Pick selects a key for the app, takes a concurrency slot on it, marks it
// used, and returns it decrypted. Callers must Release it or hand it to a lease.
// Hidden keys are considered only when allowHidden is set and a superadmin
// vouched for the service's target, so they never reach a host or an app
// token holder that only an admin controls.
func (p *Pool) Pick(ctx context.Context, svc *store.Service, appID int64, allowHidden bool, exclude map[int64]bool) (*Picked, error) {
	allowHidden = allowHidden && svc.TargetTrusted
	l := p.lock(svc.ID)
	l.Lock()
	defer l.Unlock()

	keys, err := p.Store.CandidateKeys(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	p.slots.sweep(now)
	var primary, backup []*store.Key
	var earliest *time.Time
	busy := false
	for _, k := range keys {
		if exclude[k.ID] || !allowed(k, appID) || (k.Hidden && !allowHidden) {
			continue
		}
		if !k.Available(now) {
			if k.CooldownUntil != nil && k.CooldownUntil.After(now) && (earliest == nil || k.CooldownUntil.Before(*earliest)) {
				earliest = k.CooldownUntil
			}
			continue
		}
		if lim := k.ConcurrencyLimit(svc); lim > 0 && p.slots.count(k.ID) >= lim {
			busy = true
			continue
		}
		if k.Tier == "backup" {
			backup = append(backup, k)
		} else {
			primary = append(primary, k)
		}
	}
	set := primary
	if len(set) == 0 {
		set = backup
	}
	if len(set) == 0 {
		e := &NoKeyError{Busy: busy}
		switch {
		case busy:
			e.RetryAfter = time.Second
		case earliest != nil:
			e.RetryAfter = time.Until(*earliest)
		}
		return nil, e
	}
	k := choose(svc.Strategy, set)
	secret, err := p.Box.Decrypt(k.SecretEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt key, is COGITO_MASTER_KEY correct?")
	}
	if err := p.Store.MarkUsed(ctx, k.ID); err != nil {
		return nil, err
	}
	p.slots.acquire(k.ID)
	keyID, svcID := k.ID, svc.ID
	return &Picked{Key: k, Secret: secret, release: func() { p.slots.release(keyID, svcID) }}, nil
}

func allowed(k *store.Key, appID int64) bool {
	if !k.Restricted {
		return true
	}
	for _, a := range k.AppIDs {
		if a == appID {
			return true
		}
	}
	return false
}

func lastUsed(k *store.Key) int64 {
	if k.LastUsedAt == nil {
		return 0
	}
	return k.LastUsedAt.UnixNano()
}

func choose(strategy string, keys []*store.Key) *store.Key {
	lru := func(i, j int) bool {
		if a, b := lastUsed(keys[i]), lastUsed(keys[j]); a != b {
			return a < b
		}
		return keys[i].ID < keys[j].ID
	}
	switch strategy {
	case "fill_first":
		sort.SliceStable(keys, func(i, j int) bool {
			if keys[i].Priority != keys[j].Priority {
				return keys[i].Priority < keys[j].Priority
			}
			return keys[i].ID < keys[j].ID
		})
	case "least_used":
		sort.SliceStable(keys, func(i, j int) bool {
			if keys[i].UsageToday != keys[j].UsageToday {
				return keys[i].UsageToday < keys[j].UsageToday
			}
			return lru(i, j)
		})
	case "most_quota":
		q := func(k *store.Key) int64 {
			if k.QuotaRemaining == nil {
				return 1 << 62
			}
			return *k.QuotaRemaining
		}
		sort.SliceStable(keys, func(i, j int) bool {
			if a, b := q(keys[i]), q(keys[j]); a != b {
				return a > b
			}
			return lru(i, j)
		})
	case "random":
		total := 0
		for _, k := range keys {
			total += max(k.Weight, 1)
		}
		n := rand.IntN(total)
		for _, k := range keys {
			n -= max(k.Weight, 1)
			if n < 0 {
				return k
			}
		}
	default: // round_robin
		sort.SliceStable(keys, lru)
	}
	return keys[0]
}

// Apply records what happened when a key was used and updates its state.
func (p *Pool) Apply(ctx context.Context, svc *store.Service, k *store.Key, r providers.Result) error {
	now := time.Now()
	e := store.KeyEffect{}
	switch r.Outcome {
	case providers.OK:
		zero := 0
		e.ConsecutiveFails = &zero
		e.MarkOK = true
		if providers.Get(svc.Provider).CountsPerRequest() {
			e.DecrementQuota = true
		}
	case providers.RateLimited:
		fails := k.ConsecutiveFails + 1
		d := r.RetryAfter
		if d <= 0 {
			base := time.Duration(max(svc.CooldownSec, 1)) * time.Second
			d = base << min(fails-1, 10)
		}
		if d > maxCooldown && r.RetryAfter <= 0 {
			d = maxCooldown
		}
		// A Retry-After comes from an upstream or a lease report; neither may
		// park a shared key for longer than a day.
		d = min(d, maxRetryAfter)
		until := now.Add(d)
		e.CooldownUntil, e.ConsecutiveFails, e.LastError = &until, &fails, ptr("rate limited: "+r.Message)
	case providers.QuotaExhausted:
		st := store.StatusExhausted
		reset := r.ResetAt
		if reset == nil && k.QuotaResetAt != nil && k.QuotaResetAt.After(now) {
			reset = k.QuotaResetAt
		}
		if reset == nil {
			t := now.Add(24 * time.Hour)
			reset = &t
		}
		var zero int64
		zp := &zero
		e.Status, e.QuotaResetAt, e.QuotaRemaining, e.LastError = &st, &reset, &zp, ptr("quota exhausted: "+r.Message)
	case providers.Invalid:
		st := store.StatusInvalid
		e.Status, e.LastError = &st, ptr("invalid key: "+r.Message)
	case providers.Error:
		e.LastError = ptr("upstream error: " + r.Message)
	}
	return p.Store.ApplyEffect(ctx, k.ID, e)
}

// Check runs the provider health check and stores the result.
func (p *Pool) Check(ctx context.Context, svc *store.Service, k *store.Key) error {
	secret, err := p.Box.Decrypt(k.SecretEnc)
	if err != nil {
		return err
	}
	spec := providers.Get(svc.Provider)
	if spec.Health == nil {
		return ErrNoHealthCheck
	}
	if k.Hidden && !svc.TargetTrusted {
		return ErrUntrustedTarget
	}
	res, err := spec.Check(ctx, p.Client, svc.BaseURL, secret)
	e := store.KeyEffect{Checked: true}
	if err != nil {
		// Transport errors quote the request URL, which carries query-auth keys.
		err = scrubErr(err, secret)
		e.LastError = ptr("health check failed: " + err.Error())
		_ = p.Store.ApplyEffect(ctx, k.ID, e)
		return err
	}
	res.Message = crypto.Scrub(res.Message, secret)
	if res.Valid {
		e.MarkOK = true
	}
	if !res.Valid {
		st := store.StatusInvalid
		e.Status, e.LastError = &st, ptr("invalid key: "+res.Message)
		return p.Store.ApplyEffect(ctx, k.ID, e)
	}
	if res.QuotaLimit != nil {
		e.QuotaLimit = &res.QuotaLimit
	}
	if res.QuotaRemaining != nil {
		e.QuotaRemaining = &res.QuotaRemaining
	}
	if res.ResetAt != nil {
		e.QuotaResetAt = &res.ResetAt
	}
	if res.CooldownUntil != nil {
		e.CooldownUntil = res.CooldownUntil
	}
	switch {
	case res.QuotaRemaining != nil && *res.QuotaRemaining <= 0:
		st := store.StatusExhausted
		e.Status = &st
		if res.ResetAt == nil {
			t := time.Now().Add(24 * time.Hour)
			tp := &t
			e.QuotaResetAt = &tp
		}
		e.LastError = ptr("quota exhausted")
	case k.Status == store.StatusExhausted || k.Status == store.StatusInvalid:
		// A good check revives keys that were marked bad. Disabled keys stay disabled.
		st := store.StatusActive
		zero := 0
		e.Status, e.ConsecutiveFails, e.LastError = &st, &zero, ptr("")
		if res.CooldownUntil == nil {
			e.ClearCooldown = true
		}
	case res.CooldownUntil != nil:
		e.LastError = ptr(res.Message)
	default:
		e.LastError = ptr("")
	}
	return p.Store.ApplyEffect(ctx, k.ID, e)
}

func ptr[T any](v T) *T { return &v }

// scrubErr returns err with the secret removed from its text.
func scrubErr(err error, secret string) error {
	msg := crypto.Scrub(err.Error(), secret)
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return &scrubbedErr{msg: msg, timeout: true}
	}
	return &scrubbedErr{msg: msg}
}

type scrubbedErr struct {
	msg     string
	timeout bool
}

func (e *scrubbedErr) Error() string { return e.msg }
func (e *scrubbedErr) Timeout() bool { return e.timeout }

// ErrUntrustedTarget means a hidden key would be sent to a base URL that a
// superadmin has not confirmed.
var ErrUntrustedTarget = errors.New("hidden keys are not sent to this service until a superadmin saves its base URL")

// ErrNoHealthCheck means the provider has no free endpoint to test keys, so
// validity is learned from real traffic instead.
var ErrNoHealthCheck = errors.New("this provider has no health check, so key validity is learned from real requests")
