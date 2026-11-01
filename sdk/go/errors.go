package cogito

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors. Every typed error below reports true for its sentinel via
// errors.Is, so callers can choose between errors.Is(err, cogito.RateLimited)
// and errors.As(err, &rl) when they also need fields such as RetryAfter.
var (
	RateLimited    = errors.New("cogito: rate limited")
	QuotaExhausted = errors.New("cogito: quota exhausted")
	InvalidKey     = errors.New("cogito: invalid key")
	NoKeyAvailable = errors.New("cogito: no key available")
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
		return fmt.Sprintf("cogito: http %d: %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("cogito: http %d: %s", e.StatusCode, e.Message)
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
	msg := "cogito: rate limited"
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
		return "cogito: quota exhausted: " + e.Message
	}
	return "cogito: quota exhausted"
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
		return "cogito: invalid key: " + e.Message
	}
	return "cogito: invalid key"
}
func (e *ErrInvalidKey) Is(target error) bool { return target == InvalidKey }
func (e *ErrInvalidKey) Unwrap() error        { return e.Err }

// ErrNoKeyAvailable is returned when the pool has no usable key (HTTP 503).
// RetryAfter is the time until the earliest cooldown ends, or zero if unknown.
type ErrNoKeyAvailable struct {
	RetryAfter time.Duration
	Message    string
}

func (e *ErrNoKeyAvailable) Error() string {
	msg := "cogito: no key available"
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}
func (e *ErrNoKeyAvailable) Is(target error) bool { return target == NoKeyAvailable }
