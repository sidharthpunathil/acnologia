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
)

// APIError is returned for any non-2xx response from the consumer API that is
// not a more specific error type.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
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
