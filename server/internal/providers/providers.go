// Package providers knows how each upstream API signals rate limits, quota
// exhaustion and bad keys, and how to check a key's remaining quota.
package providers

import (
	"context"
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

type Template struct {
	Provider           string `json:"provider"`
	Name               string `json:"name"`
	Category           string `json:"category"`
	Description        string `json:"description"`
	BaseURL            string `json:"base_url"`
	AuthType           string `json:"auth_type"`
	AuthName           string `json:"auth_name"`
	Icon               string `json:"icon"`
	DefaultCooldownSec int    `json:"default_cooldown_sec"`
	DocsURL            string `json:"docs_url"`
	// DefaultMaxConcurrency caps in-flight requests per key; 0 means unlimited.
	DefaultMaxConcurrency  int `json:"default_max_concurrency"`
	DefaultQueueTimeoutSec int `json:"default_queue_timeout_sec"`
}

type Provider interface {
	Template() Template
	// Classify turns an upstream response into an outcome. body may be truncated.
	Classify(status int, h http.Header, body []byte) Result
	// Check asks the upstream about the key's validity and quota without spending it.
	Check(ctx context.Context, c *http.Client, baseURL, secret string) (*CheckResult, error)
	// CountsPerRequest reports whether one successful request spends one quota unit.
	CountsPerRequest() bool
}

var registry = map[string]Provider{}

func register(p Provider) { registry[p.Template().Provider] = p }

func init() {
	register(serpAPI{})
	register(elevenLabs{})
	register(generic{})
}

func Get(name string) Provider {
	if p, ok := registry[name]; ok {
		return p
	}
	return registry["generic"]
}

func Templates() []Template {
	return []Template{serpAPI{}.Template(), elevenLabs{}.Template(), generic{}.Template()}
}

// ParseRetryAfter reads a Retry-After header in seconds or HTTP date form.
func ParseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// generic covers any API key based HTTP service with standard status codes.
type generic struct{}

func (generic) Template() Template {
	return Template{
		Provider: "generic", Name: "Custom API", Category: "Other",
		Description: "Any HTTP API that takes a key in a header, a bearer token, or a query parameter.",
		AuthType:    "header", AuthName: "Authorization", Icon: "plug", DefaultCooldownSec: 60,
	}
}

func (generic) CountsPerRequest() bool { return false }

func (generic) Classify(status int, h http.Header, body []byte) Result {
	return classifyStandard(status, h, body)
}

func classifyStandard(status int, h http.Header, body []byte) Result {
	msg := snippet(body)
	switch {
	case status == http.StatusTooManyRequests:
		return Result{Outcome: RateLimited, RetryAfter: ParseRetryAfter(h), Message: msg}
	case status == http.StatusPaymentRequired:
		return Result{Outcome: QuotaExhausted, Message: msg}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Result{Outcome: Invalid, Message: msg}
	case status >= 500:
		return Result{Outcome: Error, Message: msg}
	}
	return Result{Outcome: OK}
}

func (generic) Check(ctx context.Context, c *http.Client, baseURL, secret string) (*CheckResult, error) {
	return &CheckResult{Valid: true, Message: "no health check for custom APIs"}, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func ptr[T any](v T) *T { return &v }
