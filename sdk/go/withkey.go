package cogito

import (
	"context"
	"errors"
)

type withKeyConfig struct {
	maxAttempts int
	lease       LeaseOptions
}

// WithKeyOption configures WithKey.
type WithKeyOption func(*withKeyConfig)

// WithMaxAttempts sets how many keys WithKey tries before giving up (default 5).
func WithMaxAttempts(n int) WithKeyOption {
	return func(c *withKeyConfig) {
		if n > 0 {
			c.maxAttempts = n
		}
	}
}

// WithLeaseOptions sets the options used for the first lease (exclusions, TTL).
func WithLeaseOptions(o LeaseOptions) WithKeyOption {
	return func(c *withKeyConfig) { c.lease = o }
}

// WithKey leases a key for service and calls fn with it.
//
//   - fn returns nil: the lease is reported "ok" and WithKey returns the
//     report error, if any.
//   - fn returns an error matching *ErrRateLimited, *ErrQuotaExhausted or
//     *ErrInvalidKey (directly or wrapped): the outcome is reported with
//     rotate=true and fn is called again with the next key, up to the max
//     attempts. When attempts run out, the last such error is returned.
//   - fn returns any other error: the lease is reported "error" and fn's error
//     is returned unchanged.
//
// If the pool has no further key, *ErrNoKeyAvailable is returned.
func (c *Client) WithKey(ctx context.Context, service string, fn func(*Lease) error, opts ...WithKeyOption) error {
	cfg := withKeyConfig{maxAttempts: 5}
	for _, o := range opts {
		o(&cfg)
	}
	tried := append([]int64(nil), cfg.lease.ExcludeKeyIDs...)
	lease, err := c.Lease(ctx, service, &cfg.lease)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= cfg.maxAttempts; attempt++ {
		ferr := fn(lease)
		if ferr == nil {
			_, rerr := lease.Report(ctx, Report{Outcome: OutcomeOK})
			return rerr
		}
		report, rotate := classify(ferr)
		if !rotate {
			_, _ = lease.Report(ctx, report)
			return ferr
		}
		lastErr = ferr
		tried = append(tried, lease.KeyID)
		if attempt == cfg.maxAttempts {
			report.Rotate = false
			_, _ = lease.Report(ctx, report)
			break
		}
		next, rerr := lease.Report(ctx, report)
		if rerr != nil {
			return rerr
		}
		if next == nil {
			next, err = c.Lease(ctx, service, &LeaseOptions{ExcludeKeyIDs: tried, TTL: cfg.lease.TTL})
			if err != nil {
				return err
			}
		}
		lease = next
	}
	return lastErr
}

func classify(err error) (Report, bool) {
	var rl *ErrRateLimited
	var qe *ErrQuotaExhausted
	var ik *ErrInvalidKey
	switch {
	case errors.As(err, &rl):
		return Report{Outcome: OutcomeRateLimited, StatusCode: rl.StatusCode, RetryAfter: rl.RetryAfter, Message: rl.Message, Rotate: true}, true
	case errors.As(err, &qe):
		return Report{Outcome: OutcomeQuotaExhausted, StatusCode: qe.StatusCode, Message: qe.Message, Rotate: true}, true
	case errors.As(err, &ik):
		return Report{Outcome: OutcomeInvalid, StatusCode: ik.StatusCode, Message: ik.Message, Rotate: true}, true
	default:
		return Report{Outcome: OutcomeError, Message: err.Error()}, false
	}
}
