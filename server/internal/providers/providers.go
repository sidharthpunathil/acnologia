// Package providers knows how each upstream API signals rate limits, quota
// exhaustion and bad keys, and how to check a key's remaining quota.
package providers

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Outcome string

const (
	OK             Outcome = "ok"
	RateLimited    Outcome = "rate_limited"
	QuotaExhausted Outcome = "quota_exhausted"
	Invalid        Outcome = "invalid"
	Error          Outcome = "error"
)

func ParseOutcome(s string) (Outcome, bool) {
	switch o := Outcome(s); o {
	case OK, RateLimited, QuotaExhausted, Invalid, Error:
		return o, true
	}
	return "", false
}

// Rotates reports whether the outcome is the key's fault, so another key may succeed.
func (o Outcome) Rotates() bool { return o == RateLimited || o == QuotaExhausted || o == Invalid }

type Result struct {
	Outcome    Outcome
	RetryAfter time.Duration
	ResetAt    *time.Time
	Message    string
}

type CheckResult struct {
	Valid          bool
	QuotaLimit     *int64
	QuotaRemaining *int64
	ResetAt        *time.Time
	CooldownUntil  *time.Time
	Message        string
}

// MaxRetryAfter caps any wait an upstream asks for. A larger value is
// almost certainly an error, and an uncapped one could park a key for years.
const MaxRetryAfter = 24 * time.Hour

// ClampSeconds turns a seconds value into a duration in [0, MaxRetryAfter]
// without overflowing.
func ClampSeconds(f float64) time.Duration {
	if !(f > 0) { // also catches NaN
		return 0
	}
	if f >= MaxRetryAfter.Seconds() {
		return MaxRetryAfter
	}
	return time.Duration(f * float64(time.Second))
}

// ParseRetryAfter reads a Retry-After header in seconds or HTTP date form.
func ParseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return ClampSeconds(float64(n))
	}
	if t, err := http.ParseTime(v); err == nil {
		return min(max(time.Until(t), 0), MaxRetryAfter)
	}
	return 0
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func ptr[T any](v T) *T { return &v }
