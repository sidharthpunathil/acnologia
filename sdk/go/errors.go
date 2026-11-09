package acnologia

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors. Every typed error below reports true for its sentinel via
// errors.Is, so callers can choose between errors.Is(err, acnologia.RateLimited)
// and errors.As(err, &rl) when they also need fields such as RetryAfter.
var (
	RateLimited    = errors.New("acnologia: rate limited")
	QuotaExhausted = errors.New("acnologia: quota exhausted")
	InvalidKey     = errors.New("acnologia: invalid key")
	NoKeyAvailable = errors.New("acnologia: no key available")
	// AllKeysBusy matches an *ErrNoKeyAvailable whose Busy field is true:
	// every usable key is at its concurrency limit (HTTP 503 all_keys_busy).
	// Such an error also matches NoKeyAvailable.
	AllKeysBusy = errors.New("acnologia: all keys busy")
	// ServiceUnavailable matches an *ErrServiceUnavailable: the provider keeps
	// failing and its circuit breaker is open (HTTP 503 service_unavailable).
	ServiceUnavailable = errors.New("acnologia: service unavailable")
	// AppLimitReached matches an *ErrAppLimitReached: the app used up one of
	// its request or budget limits (HTTP 429 app_limit_reached).
	AppLimitReached = errors.New("acnologia: app limit reached")
	// ShareUnavailable matches an *ErrShareUnavailable: the share behind this
	// token was revoked, paused or expired (HTTP 403 share_*).
	ShareUnavailable = errors.New("acnologia: share unavailable")
)

// APIError is returned for any non-2xx response from the consumer API that is
// not a more specific error type.
//
// Codes worth checking on it include empty_group, bad_params, no_route,
// no_route_worked, proxy_not_allowed, lease_not_allowed and loop_detected.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	// RetryAfter is retry_after_sec from the body, or the Retry-After
	// header, when the server gave one.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("acnologia: http %d: %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("acnologia: http %d: %s", e.StatusCode, e.Message)
}

// ErrRateLimited signals that the upstream provider rate limited the key.
// Return it (or wrap it) from a WithKey callback to rotate to the next key.
type ErrRateLimited struct {
	RetryAfter time.Duration
	StatusCode int
	Message    string
	Err        error // optional underlying error
}

func (e *ErrRateLimited) Error() string {
	msg := "acnologia: rate limited"
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}
func (e *ErrRateLimited) Is(target error) bool { return target == RateLimited }
func (e *ErrRateLimited) Unwrap() error        { return e.Err }

// ErrQuotaExhausted signals that the key has used up its quota.
type ErrQuotaExhausted struct {
	StatusCode int
	Message    string
	Err        error
}

func (e *ErrQuotaExhausted) Error() string {
	if e.Message != "" {
		return "acnologia: quota exhausted: " + e.Message
	}
	return "acnologia: quota exhausted"
}
func (e *ErrQuotaExhausted) Is(target error) bool { return target == QuotaExhausted }
func (e *ErrQuotaExhausted) Unwrap() error        { return e.Err }

// ErrInvalidKey signals that the provider rejected the key.
type ErrInvalidKey struct {
	StatusCode int
	Message    string
	Err        error
}

func (e *ErrInvalidKey) Error() string {
	if e.Message != "" {
		return "acnologia: invalid key: " + e.Message
	}
	return "acnologia: invalid key"
}
func (e *ErrInvalidKey) Is(target error) bool { return target == InvalidKey }
func (e *ErrInvalidKey) Unwrap() error        { return e.Err }

// ErrNoKeyAvailable is returned when no key can be leased right now (HTTP 503).
// RetryAfter is how long to wait before retrying, or zero if unknown.
//
// Code is "no_key_available" when every key is cooling down, exhausted or
// excluded, and "all_keys_busy" when usable keys exist but every one is at its
// concurrency limit. Busy is true in the second case; it clears as soon as
// another caller reports a lease, so retry after RetryAfter (usually 1s) or
// lease with LeaseOptions.Wait. errors.Is(err, AllKeysBusy) also tests it.
type ErrNoKeyAvailable struct {
	RetryAfter time.Duration
	Message    string
	Code       string
	Busy       bool
}

func (e *ErrNoKeyAvailable) Error() string {
	msg := "acnologia: no key available"
	if e.Busy {
		msg = "acnologia: all keys busy"
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}
func (e *ErrNoKeyAvailable) Is(target error) bool {
	return target == NoKeyAvailable || (target == AllKeysBusy && e.Busy)
}

// ErrServiceUnavailable is returned for HTTP 503 service_unavailable: the
// provider keeps failing and Acnologia paused traffic to it (its circuit
// breaker is open). Wait RetryAfter, or zero if unknown, before retrying. It
// is not an *ErrNoKeyAvailable. errors.Is(err, ServiceUnavailable) tests it,
// and errors.As(err, &apiErr) with an **APIError also matches.
type ErrServiceUnavailable struct {
	APIError
	RetryAfter time.Duration
}

func (e *ErrServiceUnavailable) Error() string {
	msg := "acnologia: service unavailable"
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}
func (e *ErrServiceUnavailable) Is(target error) bool { return target == ServiceUnavailable }

// As lets errors.As(err, &apiErr) with apiErr *APIError see the embedded APIError.
func (e *ErrServiceUnavailable) As(target any) bool {
	if p, ok := target.(**APIError); ok {
		*p = &e.APIError
		return true
	}
	return false
}

// apiErrorAs lets errors.As(err, &apiErr) with apiErr *APIError see an
// embedded APIError.
func apiErrorAs(e *APIError, target any) bool {
	if p, ok := target.(**APIError); ok {
		*p = e
		return true
	}
	return false
}

// ErrAppLimitReached is returned for HTTP 429 app_limit_reached: the app
// used up a daily or monthly request or budget limit. Limit names it
// (daily_requests, monthly_requests, daily_budget or monthly_budget) and
// ResetAt says when it resets; RetryAfter (on the embedded APIError) is how
// long to wait. errors.Is(err, AppLimitReached) tests it, and errors.As with
// an **APIError also matches.
type ErrAppLimitReached struct {
	APIError
	Limit   string
	ResetAt time.Time
}

func (e *ErrAppLimitReached) Error() string {
	msg := "acnologia: app limit reached"
	if e.Limit != "" {
		msg += " (" + e.Limit + ")"
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}
func (e *ErrAppLimitReached) Is(target error) bool { return target == AppLimitReached }
func (e *ErrAppLimitReached) As(target any) bool   { return apiErrorAs(&e.APIError, target) }

// ErrShareUnavailable is returned for HTTP 403 share_revoked, share_paused
// or share_expired: the token belongs to a share its owner revoked, paused
// or let expire. Status is "revoked", "paused" or "expired" (also sent as
// X-Acnologia-Share). errors.Is(err, ShareUnavailable) tests it, and
// errors.As with an **APIError also matches.
type ErrShareUnavailable struct {
	APIError
	Status string
}

func (e *ErrShareUnavailable) Error() string {
	msg := "acnologia: share " + e.Status
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}
func (e *ErrShareUnavailable) Is(target error) bool { return target == ShareUnavailable }
func (e *ErrShareUnavailable) As(target any) bool   { return apiErrorAs(&e.APIError, target) }
