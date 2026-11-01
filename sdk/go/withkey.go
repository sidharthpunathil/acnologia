package cogito

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// reportTimeout bounds a report sent after the caller's context is done.
const reportTimeout = 10 * time.Second

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

// WithLeaseOptions sets the options used for the first lease (exclusions,
// TTL, Wait). TTL and Wait also apply to every later lease.
func WithLeaseOptions(o LeaseOptions) WithKeyOption {
	return func(c *withKeyConfig) { c.lease = o }
}

// WithWait sets LeaseOptions.Wait for every lease WithKey takes: how long the
// server may wait for a free slot when every usable key is busy (at most
// MaxWait).
func WithWait(d time.Duration) WithKeyOption {
	return func(c *withKeyConfig) { c.lease.Wait = d }
}

// detached returns a context for reporting that survives cancellation of ctx,
// so a lease is still released when the caller's deadline has passed.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
}

func reportDetached(ctx context.Context, l *Lease, r Report) (*Lease, error) {
	rctx, cancel := detached(ctx)
	defer cancel()
	return l.Report(rctx, r)
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
// If the pool has no further key, *ErrNoKeyAvailable is returned (Busy is set
// when every usable key is at its concurrency limit).
//
// Every lease is reported, even when fn panics (the panic is re-raised after
// the report), calls runtime.Goexit, or ctx is cancelled while fn runs (the report is sent on a
// detached context). This matters on services with a concurrency limit, where
// an unreported lease keeps one of the key's slots busy until its TTL expires.
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
		ferr := callReporting(ctx, lease, fn)
		if ferr == nil {
			_, rerr := reportDetached(ctx, lease, Report{Outcome: OutcomeOK})
			return rerr
		}
		report, rotate := classify(ferr, lease.Secret)
		if !rotate {
			_, _ = reportDetached(ctx, lease, report)
			return ferr
		}
		lastErr = ferr
		tried = append(tried, lease.KeyID)
		if attempt == cfg.maxAttempts || ctx.Err() != nil {
			report.Rotate = false
			_, _ = reportDetached(ctx, lease, report)
			if attempt < cfg.maxAttempts {
				return ctx.Err()
			}
			break
		}
		next, rerr := lease.Report(ctx, report)
		if rerr != nil {
			return rerr
		}
		if next == nil {
			next, err = c.Lease(ctx, service, &LeaseOptions{ExcludeKeyIDs: tried, TTL: cfg.lease.TTL, Wait: cfg.lease.Wait})
			if err != nil {
				return err
			}
		}
		lease = next
	}
	return lastErr
}

// callReporting runs fn and, if it does not return normally (a panic or
// runtime.Goexit), reports the lease as an error so the key's concurrency slot
// is released. A panic is re-raised after the report.
func callReporting(ctx context.Context, lease *Lease, fn func(*Lease) error) error {
	returned := false
	defer func() {
		if returned {
			return
		}
		r := recover()
		msg := "fn did not return"
		if r != nil {
			msg = fmt.Sprint("panic: ", r)
		}
		_, _ = reportDetached(ctx, lease, Report{Outcome: OutcomeError, Message: scrubMessage(msg, lease.Secret)})
		if r != nil {
			panic(r)
		}
	}()
	err := fn(lease)
	returned = true
	return err
}

// maxReportMessage caps the message sent with a report built from an error.
const maxReportMessage = 500

// scrubMessage removes the lease secret (raw and query escaped) from msg and
// truncates it. Errors from query auth services often carry the full request
// URL, key included, and the report must not copy it to the server.
func scrubMessage(msg, secret string) string {
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[key]")
		msg = strings.ReplaceAll(msg, url.QueryEscape(secret), "[key]")
	}
	if len(msg) > maxReportMessage {
		msg = strings.ToValidUTF8(msg[:maxReportMessage], "")
	}
	return msg
}

func classify(err error, secret string) (Report, bool) {
	var rl *ErrRateLimited
	var qe *ErrQuotaExhausted
	var ik *ErrInvalidKey
	switch {
	case errors.As(err, &rl):
		return Report{Outcome: OutcomeRateLimited, StatusCode: rl.StatusCode, RetryAfter: rl.RetryAfter, Message: scrubMessage(rl.Message, secret), Rotate: true}, true
	case errors.As(err, &qe):
		return Report{Outcome: OutcomeQuotaExhausted, StatusCode: qe.StatusCode, Message: scrubMessage(qe.Message, secret), Rotate: true}, true
	case errors.As(err, &ik):
		return Report{Outcome: OutcomeInvalid, StatusCode: ik.StatusCode, Message: scrubMessage(ik.Message, secret), Rotate: true}, true
	default:
		return Report{Outcome: OutcomeError, Message: scrubMessage(err.Error(), secret)}, false
	}
}
