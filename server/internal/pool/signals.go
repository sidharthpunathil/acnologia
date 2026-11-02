package pool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// maxBusyCooldown caps how long a busy key steps aside.
const maxBusyCooldown = 5 * time.Minute

// ErrBanned means the key's account is suspended; it is not health checked.
var ErrBanned = errors.New("this key's account is suspended by the provider, reset it after the provider restores it")

// applyQuotaReading copies quota read from response headers into the effect.
// A reading of zero remaining parks the key until the reset the provider
// announced, or a day when it announced none.
func applyQuotaReading(e *store.KeyEffect, r providers.Result, now time.Time) {
	q := r.Quota
	if q.Empty() {
		return
	}
	switch r.Outcome {
	case providers.QuotaExhausted, providers.Invalid, providers.Banned:
		return // the outcome already decided the key's state
	}
	if q.Limit != nil {
		e.QuotaLimit = &q.Limit
	}
	if q.ResetAt != nil {
		e.QuotaResetAt = &q.ResetAt
	}
	if q.Remaining != nil {
		e.QuotaRemaining = &q.Remaining
		e.DecrementQuota = false
		if *q.Remaining <= 0 {
			st := store.StatusExhausted
			reset := q.ResetAt
			if reset == nil {
				t := now.Add(24 * time.Hour)
				reset = &t
			}
			e.Status, e.QuotaResetAt = &st, &reset
			e.LastError = ptr("quota exhausted: the provider reports 0 left")
		}
	}
}

// checkClient is the health check client, through the key's or the
// service's egress proxy when one is set.
func (p *Pool) checkClient(ctx context.Context, svc *store.Service, k *store.Key) (*http.Client, error) {
	id := egress.Pick(k.EgressID, svc.EgressID)
	if id == nil || p.Egress == nil {
		return p.Client, nil
	}
	rt, err := p.Egress.Transport(ctx, id)
	if err != nil {
		return nil, err
	}
	return egress.Client(rt, 20*time.Second), nil
}

func (p *Pool) emitKey(typ string, svc *store.Service, k *store.Key, msg string, data map[string]any) {
	// Admins must not learn about hidden keys, and webhooks are set up by admins.
	if p.Notify == nil || k.Hidden {
		return
	}
	p.Notify.Emit(notify.Event{Type: typ, ServiceID: svc.ID, Service: svc.Slug, KeyID: k.ID, Key: k.Label, Message: msg, Data: data})
}

// notifyEffect reports a key's state change caused by an outcome.
func (p *Pool) notifyEffect(svc *store.Service, k *store.Key, e store.KeyEffect, msg string) {
	if p.Notify == nil {
		return
	}
	if e.Status != nil && *e.Status != k.Status {
		p.emitStatus(svc, k, *e.Status, msg)
	}
	limit, remaining := k.QuotaLimit, k.QuotaRemaining
	if e.QuotaLimit != nil {
		limit = *e.QuotaLimit
	}
	if e.QuotaRemaining != nil {
		remaining = *e.QuotaRemaining
	}
	p.maybeQuotaLow(svc, k, limit, remaining)
}

func (p *Pool) emitStatus(svc *store.Service, k *store.Key, status, msg string) {
	name := fmt.Sprintf("%s key %q", svc.Name, k.Label)
	switch status {
	case store.StatusInvalid:
		p.emitKey(notify.KeyInvalid, svc, k, name+" was rejected by the provider: "+msg, nil)
	case store.StatusBanned:
		p.emitKey(notify.KeyBanned, svc, k, name+" belongs to a suspended account: "+msg, nil)
	case store.StatusExhausted:
		p.emitKey(notify.KeyExhausted, svc, k, name+" is out of quota", nil)
	case store.StatusActive:
		p.emitKey(notify.KeyRecovered, svc, k, name+" works again", nil)
	}
}

func (p *Pool) maybeQuotaLow(svc *store.Service, k *store.Key, limit, remaining *int64) {
	if limit == nil || remaining == nil || *limit <= 0 || *remaining <= 0 {
		return
	}
	if *remaining*10 >= *limit {
		return
	}
	unit := providers.Get(svc.Provider).Quota.Unit
	if unit == "" {
		unit = "units"
	}
	p.emitKey(notify.QuotaLow, svc, k, fmt.Sprintf("%s key %q has %d of %d %s left", svc.Name, k.Label, *remaining, *limit, unit),
		map[string]any{"remaining": *remaining, "limit": *limit})
}

// notifyAfterCheck compares a key before and after a health check.
func (p *Pool) notifyAfterCheck(ctx context.Context, svc *store.Service, before *store.Key) {
	if p.Notify == nil || before.Hidden {
		return
	}
	after, err := p.Store.GetKey(ctx, before.ID)
	if err != nil {
		return
	}
	if after.Status != before.Status {
		p.emitStatus(svc, after, after.Status, after.LastError)
	}
	p.maybeQuotaLow(svc, after, after.QuotaLimit, after.QuotaRemaining)
}

// EmitPoolEmpty reports that a request found no usable key.
func (p *Pool) EmitPoolEmpty(svc *store.Service, err error) {
	if p.Notify == nil {
		return
	}
	p.Notify.Emit(notify.Event{Type: notify.PoolEmpty, ServiceID: svc.ID, Service: svc.Slug,
		Message: fmt.Sprintf("%s has no usable key: %v", svc.Name, err)})
}
