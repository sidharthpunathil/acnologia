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
	"sync/atomic"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
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
	// Paced is set when some usable key is held back by a rate limit its
	// provider publishes in offers, rather than by a concurrency slot.
	Paced bool
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
	// PublicClient is Client restricted to public addresses, for health
	// checks of services from a share whose link is not on a private network.
	PublicClient *http.Client
	// Notify receives key and pool events for webhooks; nil is fine.
	Notify *notify.Notifier
	// Egress routes health checks through a key's or service's proxy; nil connects directly.
	Egress *egress.Manager
	// KeyScope returns the key scope an app has for a service, or nil when
	// the app may use every key. It limits the keys of a share app; nil
	// skips scoping.
	KeyScope func(ctx context.Context, appID int64, svc *store.Service) (*store.KeyScope, error)

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
	slots *slots
	pacer *pacer
	// picked holds the last pick time per key in nanoseconds (key id -> int64).
	picked sync.Map
	// lastPick is the newest pick time handed out. Pick times only move
	// forward, so two picks never tie even where the clock is coarse
	// (Windows), and round robin keeps spreading a burst.
	lastPick atomic.Int64
	// suspects holds verdicts from proxied answers that wait for
	// corroboration (corroborate.go).
	suspects suspects
}

func New(s *store.Store, box *crypto.Box) *Pool {
	return &Pool{
		Store: s, Box: box, locks: map[int64]*sync.Mutex{}, slots: newSlots(), pacer: newPacer(),
		Client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: checkTransport(),
			// Never follow a redirect with a key attached: Go forwards custom
			// auth headers (x-api-key and friends) to the new host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		PublicClient: publicClient(),
	}
}

// checkTransport reaches any address a base URL resolves to but link local
// and cloud metadata ones, like the proxy does. Loopback and private
// addresses stay allowed for model servers on the same network.
func checkTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = catalog.UpstreamDialer().DialContext
	t.Proxy = catalog.GuardProxy(http.ProxyFromEnvironment)
	return egress.Tune(t)
}

func publicClient() *http.Client {
	c := catalog.SafeClient(20 * time.Second)
	if t, ok := c.Transport.(*http.Transport); ok {
		egress.Tune(t)
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
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

	once      sync.Once
	release   func()
	exclusive bool
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
	return p.pick(ctx, svc, appID, allowHidden, exclude, false, Request{})
}

func (p *Pool) pick(ctx context.Context, svc *store.Service, appID int64, allowHidden bool, exclude map[int64]bool, exclusive bool, req Request) (*Picked, error) {
	allowHidden = allowHidden && svc.TargetTrusted
	scoped, err := p.scopedKeys(ctx, svc, appID, allowHidden)
	if err != nil {
		return nil, err
	}
	l := p.lock(svc.ID)
	l.Lock()
	locked := true
	defer func() {
		if locked {
			l.Unlock()
		}
	}()

	// Usage writes run after the unlock, so least_used counts the ones still
	// in flight. Taking the snapshot before reading the keys means a write
	// that commits in between is counted twice (harmless), never missed:
	// new pending writes on this service only start under this lock.
	var pending map[int64]int64
	if svc.Strategy == "least_used" {
		pending = p.slots.pendingSnapshot()
	}
	keys, err := p.Store.CandidateKeys(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	p.slots.sweep(now)
	spec := providers.Get(svc.Provider)
	applied := spec.OffersFor(req.Method, req.Path)
	var primary, backup []*store.Key
	var earliest *time.Time
	busy, slotBusy := false, false
	var paceWait time.Duration
	for _, k := range keys {
		if exclude[k.ID] || !k.AllowsApp(appID) || (k.Hidden && !allowHidden) || (scoped != nil && !scoped[k.ID]) {
			continue
		}
		if !k.Available(now) {
			if k.CooldownUntil != nil && k.CooldownUntil.After(now) && (earliest == nil || k.CooldownUntil.Before(*earliest)) {
				earliest = k.CooldownUntil
			}
			continue
		}
		if lim := ConcurrencyLimit(k, svc, spec); lim > 0 && p.slots.count(k.ID) >= lim {
			busy, slotBusy = true, true
			continue
		}
		// Exclusive leases own a key outright; exclusive requests need an idle key.
		if p.slots.exclusiveHeld(k.ID) || (exclusive && p.slots.count(k.ID) > 0) {
			busy, slotBusy = true, true
			continue
		}
		// Stay under the limits the provider publishes for this endpoint.
		if ok, wait := p.pacer.check(k.ID, applied, now); !ok {
			busy = true
			if wait <= 0 {
				slotBusy = true
			} else if paceWait == 0 || wait < paceWait {
				paceWait = wait
			}
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
		e := &NoKeyError{Busy: busy, Paced: paceWait > 0}
		switch {
		case busy && paceWait > 0 && (!slotBusy || paceWait < time.Second):
			e.RetryAfter = paceWait
		case busy:
			e.RetryAfter = time.Second
		case earliest != nil:
			e.RetryAfter = time.Until(*earliest)
		}
		return nil, e
	}
	// The database stores last use in whole seconds, so a burst would tie
	// and always pick the lowest id. Use the precise in-memory pick time.
	for _, c := range set {
		c.UsageToday += pending[c.ID]
		if v, ok := p.picked.Load(c.ID); ok {
			t := time.Unix(0, v.(int64))
			if c.LastUsedAt == nil || t.After(*c.LastUsedAt) {
				c.LastUsedAt = &t
			}
		}
	}
	k := choose(svc.Strategy, set)
	// Hand the key out with its stored usage, not the ordering adjustment.
	k.UsageToday -= pending[k.ID]
	p.picked.Store(k.ID, p.nextPick(time.Now().UnixNano()))
	secret, err := p.Box.Decrypt(k.SecretEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt key, is ACNOLOGIA_MASTER_KEY correct?")
	}
	p.slots.acquire(k.ID)
	freeOffer := p.pacer.take(k.ID, applied, now)
	keyID, svcID := k.ID, svc.ID
	release := func() {
		if freeOffer != nil {
			freeOffer()
		}
		p.slots.release(keyID, svcID)
	}
	// The slot and the pace are taken, so the choice is final. The usage
	// write waits on the single writer, so it runs after the unlock: other
	// picks on the service need not queue behind a group commit. The pick
	// still returns only once the write has committed.
	p.slots.addPending(keyID, 1)
	l.Unlock()
	locked = false
	err = p.Store.MarkUsed(ctx, keyID)
	p.slots.addPending(keyID, -1)
	if err != nil {
		// Free the slot and any endpoint concurrency. The request already
		// counted in the pacer's rate window stays counted, so a failed
		// write can only make the key's pacing stricter, never looser.
		release()
		return nil, err
	}
	return &Picked{Key: k, Secret: secret, exclusive: exclusive, release: release}, nil
}

// scopedKeys returns the only keys the app may use on the service, or nil
// when its scope allows every key.
func (p *Pool) scopedKeys(ctx context.Context, svc *store.Service, appID int64, allowHidden bool) (map[int64]bool, error) {
	if p.KeyScope == nil || appID == 0 {
		return nil, nil
	}
	sc, err := p.KeyScope(ctx, appID, svc)
	if err != nil || sc.IsAll() {
		return nil, err
	}
	if sc.Mode == store.ScopePick {
		ids := make(map[int64]bool, len(sc.KeyIDs))
		for _, id := range sc.KeyIDs {
			ids[id] = true
		}
		return ids, nil
	}
	res, err := p.Store.ScopeKeys(ctx, svc.ID, appID, allowHidden, sc)
	if err != nil {
		return nil, err
	}
	return res.IDs, nil
}

// MayUse reports whether the app may still be handed the key on the
// service, by the same rules Pick applies: the key's service, its app
// restriction, hidden keys, and the app's share scope.
func (p *Pool) MayUse(ctx context.Context, svc *store.Service, appID int64, allowHidden bool, k *store.Key) bool {
	allowHidden = allowHidden && svc.TargetTrusted
	if k.ServiceID != svc.ID || !k.AllowsApp(appID) || (k.Hidden && !allowHidden) || k.IsHandedOver() {
		return false
	}
	scoped, err := p.scopedKeys(ctx, svc, appID, allowHidden)
	if err != nil {
		return false
	}
	return scoped == nil || scoped[k.ID]
}

// StoreKeyScope is the KeyScope hook backed by the store's share scopes.
func StoreKeyScope(st *store.Store) func(context.Context, int64, *store.Service) (*store.KeyScope, error) {
	return func(ctx context.Context, appID int64, svc *store.Service) (*store.KeyScope, error) {
		return st.AppKeyScope(ctx, appID, svc.Slug)
	}
}

// nextPick returns now, or one nanosecond after the last pick when the clock
// has not moved past it.
func (p *Pool) nextPick(now int64) int64 {
	for {
		last := p.lastPick.Load()
		n := now
		if n <= last {
			n = last + 1
		}
		if p.lastPick.CompareAndSwap(last, n) {
			return n
		}
	}
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
		// A quota header beats counting: it is the provider's own number.
		if providers.Get(svc.Provider).CountsPerRequest() && r.Quota.Remaining == nil {
			e.DecrementQuota = true
		}
	case providers.Busy:
		// The account is healthy, just at its concurrency cap: step aside
		// briefly without climbing the backoff ladder.
		d := r.RetryAfter
		if d <= 0 {
			d = time.Second
		}
		until := now.Add(min(d, maxBusyCooldown))
		e.CooldownUntil, e.LastError = &until, ptr("busy: "+r.Message)
	case providers.Banned:
		st := store.StatusBanned
		e.Status, e.LastError = &st, ptr("account suspended: "+r.Message)
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
		if reset == nil {
			reset = r.Quota.ResetAt
		}
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
	applyQuotaReading(&e, r, now)
	err := p.Store.ApplyEffect(ctx, k.ID, e)
	p.notifyEffect(svc, k, e, r.Message)
	return err
}

// Check runs the provider health check, stores the result and reports any
// change of the key's state to webhooks.
func (p *Pool) Check(ctx context.Context, svc *store.Service, k *store.Key) error {
	err := p.check(ctx, svc, k)
	p.notifyAfterCheck(ctx, svc, k)
	return err
}

func (p *Pool) check(ctx context.Context, svc *store.Service, k *store.Key) error {
	// A check sends the secret to the service's base URL, so a key is only
	// ever checked against its own service.
	if k.ServiceID != svc.ID {
		return ErrWrongService
	}
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
	if k.Status == store.StatusBanned {
		return ErrBanned
	}
	client, err := p.checkClient(ctx, svc, k)
	if err != nil {
		return err
	}
	res, err := spec.Check(ctx, client, svc.BaseURL, secret)
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
			// The provider gave no reset time: keep an earlier estimate rather
			// than pushing it forward on every check.
			t := time.Now().Add(24 * time.Hour)
			if k.QuotaResetAt != nil && k.QuotaResetAt.After(time.Now()) {
				t = *k.QuotaResetAt
			}
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
var ErrUntrustedTarget = errors.New("hidden keys are not sent to this service until a superadmin confirms its target")

// ErrWrongService refuses to check a key against a service it does not belong to.
var ErrWrongService = errors.New("this key belongs to another service")

// ErrNoHealthCheck means the provider has no free endpoint to test keys, so
// validity is learned from real traffic instead.
var ErrNoHealthCheck = errors.New("This provider has no health check, so key validity is learned from real requests.")
