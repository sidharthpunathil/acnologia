// Package acnologia is a Go client for Acnologia, a self-hosted API key pool.
//
// Acnologia can be consumed in two ways:
//
//   - Lease mode: Lease a key, call the provider yourself, then Report the
//     outcome. WithKey wraps this loop and rotates keys automatically.
//   - Proxy mode: send requests through Proxy; Acnologia injects a key and
//     rotates on rate limits without ever exposing the secret.
package acnologia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version is the SDK version.
const Version = "0.2.0"

// DefaultTimeout bounds API calls when New is given no WithHTTPClient. It is
// longer than MaxWait so a lease that waits for a free slot can still succeed.
const DefaultTimeout = 75 * time.Second

// Client talks to an Acnologia server using an app token (acn_...).
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	userAgent  string
	// accessPassword, when set, goes out as X-Acnologia-Access.
	accessPassword string
	// defaultHTTP is true when httpClient was created by New, so proxy calls
	// can drop its timeout (streamed bodies may outlive it).
	defaultHTTP bool
}

// String redacts the app token so a logged Client does not leak it.
func (c Client) String() string {
	return fmt.Sprintf("acnologia.Client{baseURL:%q, token:\"[redacted]\"}", c.baseURL)
}

// GoString redacts the app token for %#v.
func (c Client) GoString() string { return c.String() }

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc; c.defaultHTTP = false }
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithAccessPassword sends X-Acnologia-Access on every request, for a
// deploy behind the access password.
func WithAccessPassword(pw string) Option { return func(c *Client) { c.accessPassword = pw } }

// New creates a client. baseURL is the Acnologia server, e.g. http://localhost:8080.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		token:       token,
		httpClient:  &http.Client{Timeout: DefaultTimeout},
		userAgent:   "acnologia-go/" + Version,
		defaultHTTP: true,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Auth describes where a provider expects the key.
// Type is "query", "header" or "bearer"; Name is the param or header name.
// Prefix, when set, goes in front of the secret for every type (for bearer it
// replaces the default "Bearer " prefix).
type Auth struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Prefix string `json:"prefix,omitempty"`
}

// Service is an entry from GET /api/v1/services.
type Service struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Category  string `json:"category"`
	Available bool   `json:"available"`
	BaseURL   string `json:"base_url"`
	Auth      Auth   `json:"auth"`
	// GuideURL is where the service's usage guide is (see Guide).
	GuideURL string `json:"guide_url"`
}

// Lease is a key handed out by the pool. Report its outcome when done.
type Lease struct {
	LeaseID   string    `json:"lease_id"`
	KeyID     int64     `json:"key_id"`
	Service   string    `json:"service"`
	Secret    string    `json:"secret"`
	Auth      Auth      `json:"auth"`
	BaseURL   string    `json:"base_url"`
	ExpiresAt time.Time `json:"-"`
	// Exclusive is true when the key is reserved for this lease only.
	Exclusive bool `json:"exclusive"`

	client *Client
}

// String redacts the secret so a logged Lease does not leak the key.
func (l Lease) String() string {
	return fmt.Sprintf("acnologia.Lease{LeaseID:%q, KeyID:%d, Service:%q, Secret:\"[redacted]\", Auth:%+v, BaseURL:%q, ExpiresAt:%s, Exclusive:%t}",
		l.LeaseID, l.KeyID, l.Service, l.Auth, l.BaseURL, l.ExpiresAt.Format(time.RFC3339), l.Exclusive)
}

// GoString redacts the secret for %#v.
func (l Lease) GoString() string { return l.String() }

type leaseWire struct {
	LeaseID   string `json:"lease_id"`
	KeyID     int64  `json:"key_id"`
	Service   string `json:"service"`
	Secret    string `json:"secret"`
	Auth      Auth   `json:"auth"`
	BaseURL   string `json:"base_url"`
	ExpiresAt string `json:"expires_at"`
	Exclusive bool   `json:"exclusive"`
}

func (c *Client) leaseFromWire(w *leaseWire) *Lease {
	if w == nil {
		return nil
	}
	l := &Lease{LeaseID: w.LeaseID, KeyID: w.KeyID, Service: w.Service, Secret: w.Secret,
		Auth: w.Auth, BaseURL: w.BaseURL, Exclusive: w.Exclusive, client: c}
	if w.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, w.ExpiresAt); err == nil {
			l.ExpiresAt = t
		}
	}
	return l
}

// LeaseOptions are optional parameters for Lease.
type LeaseOptions struct {
	ExcludeKeyIDs []int64       // keys that must not be returned
	TTL           time.Duration // lease lifetime; zero uses the server default (120s on services with a concurrency limit, 300s otherwise)
	// Wait lets the server wait up to this long (at most MaxWait) for a free
	// slot when every usable key is at its concurrency limit. Zero fails fast
	// with a busy *ErrNoKeyAvailable. Sent as wait_sec, rounded up to whole
	// seconds. If your http.Client has a Timeout, make it longer than Wait.
	Wait time.Duration
	// Exclusive reserves the key for this lease only: no other lease or proxy
	// call gets it until the lease is reported or expires.
	Exclusive bool
}

// MaxWait is the longest LeaseOptions.Wait the server accepts.
const MaxWait = 60 * time.Second

// Outcome is the result of using a leased key.
type Outcome string

const (
	OutcomeOK             Outcome = "ok"
	OutcomeRateLimited    Outcome = "rate_limited"
	OutcomeQuotaExhausted Outcome = "quota_exhausted"
	OutcomeInvalid        Outcome = "invalid"
	OutcomeError          Outcome = "error"
	// OutcomeBusy: the account is at its concurrency cap; the key steps aside briefly.
	OutcomeBusy Outcome = "busy"
	// OutcomeBanned: the provider suspended the account; the key leaves rotation.
	OutcomeBanned Outcome = "banned"
)

// Report describes how a leased key behaved. Set Rotate to receive the next
// key in the same round trip.
type Report struct {
	Outcome    Outcome
	StatusCode int
	RetryAfter time.Duration
	Message    string
	Rotate     bool
}

// Services lists the services this app token may use.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var out struct {
		Services []Service `json:"services"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/services", nil, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

// Lease asks the pool for a key. It returns *ErrNoKeyAvailable when the pool
// is empty or every key is cooling down, with Busy set when every usable key
// is at its concurrency limit and opts.Wait ran out.
//
// Always Report the lease: on services with a concurrency limit it holds one
// of the key's slots until it is reported or its TTL expires.
func (c *Client) Lease(ctx context.Context, service string, opts *LeaseOptions) (*Lease, error) {
	body := map[string]any{"service": service}
	if opts != nil {
		if len(opts.ExcludeKeyIDs) > 0 {
			body["exclude_key_ids"] = opts.ExcludeKeyIDs
		}
		if opts.TTL > 0 {
			body["ttl_sec"] = ceilSeconds(opts.TTL)
		}
		if opts.Wait < 0 || opts.Wait > MaxWait {
			return nil, fmt.Errorf("acnologia: lease wait must be between 0 and %s", MaxWait)
		}
		if opts.Wait > 0 {
			body["wait_sec"] = ceilSeconds(opts.Wait)
		}
		if opts.Exclusive {
			body["exclusive"] = true
		}
	}
	var w leaseWire
	if err := c.do(ctx, http.MethodPost, "/api/v1/lease", body, &w); err != nil {
		return nil, err
	}
	return c.leaseFromWire(&w), nil
}

// Report sends the outcome of a lease. When r.Rotate is true and another key
// is available, the next lease is returned; otherwise next is nil.
func (l *Lease) Report(ctx context.Context, r Report) (*Lease, error) {
	if l.client == nil {
		return nil, fmt.Errorf("acnologia: lease %q has no client", l.LeaseID)
	}
	outcome := r.Outcome
	if outcome == "" {
		outcome = OutcomeOK
	}
	body := map[string]any{"outcome": outcome}
	if r.StatusCode != 0 {
		body["status_code"] = r.StatusCode
	}
	if r.RetryAfter > 0 {
		body["retry_after_sec"] = ceilSeconds(r.RetryAfter)
	}
	if r.Message != "" {
		body["message"] = r.Message
	}
	if r.Rotate {
		body["rotate"] = true
	}
	var out struct {
		OK   bool       `json:"ok"`
		Next *leaseWire `json:"next"`
	}
	path := "/api/v1/leases/" + url.PathEscape(l.LeaseID) + "/report"
	if err := l.client.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return l.client.leaseFromWire(out.Next), nil
}

// Renew extends an open lease so a long job keeps its key and updates
// l.ExpiresAt. A zero ttl uses the server default (120 seconds); the server
// caps it at 24 hours. When the lease is no longer open it returns an *APIError
// with Code "lease_not_found" (HTTP 404).
func (l *Lease) Renew(ctx context.Context, ttl time.Duration) (time.Time, error) {
	if l.client == nil {
		return time.Time{}, fmt.Errorf("acnologia: lease %q has no client", l.LeaseID)
	}
	return l.client.Renew(ctx, l, ttl)
}

// Renew extends an open lease and updates l.ExpiresAt. It is the same as
// l.Renew(ctx, ttl).
func (c *Client) Renew(ctx context.Context, l *Lease, ttl time.Duration) (time.Time, error) {
	body := map[string]any{}
	if ttl > 0 {
		body["ttl_sec"] = ceilSeconds(ttl)
	}
	var out struct {
		OK        bool   `json:"ok"`
		ExpiresAt string `json:"expires_at"`
	}
	path := "/api/v1/leases/" + url.PathEscape(l.LeaseID) + "/renew"
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, out.ExpiresAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("acnologia: renew response has no valid expires_at: %w", err)
	}
	l.ExpiresAt = t
	return t, nil
}

// Apply puts the secret on req where the provider expects it: a query
// parameter, a header, or an "Authorization: Bearer" style header. A non empty
// Auth.Prefix is put in front of the secret for every type; for bearer it
// replaces the default "Bearer " prefix. Header names default to
// "Authorization" when Auth.Name is empty.
func (l *Lease) Apply(req *http.Request) {
	value := l.Auth.Prefix + l.Secret
	switch l.Auth.Type {
	case "query":
		q := req.URL.Query()
		q.Set(l.Auth.Name, value)
		req.URL.RawQuery = q.Encode()
	case "bearer":
		name := l.Auth.Name
		if name == "" {
			name = "Authorization"
		}
		if l.Auth.Prefix == "" {
			value = "Bearer " + l.Secret
		}
		req.Header.Set(name, value)
	default: // "header"
		name := l.Auth.Name
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, value)
	}
}

// URL joins the lease's provider base URL with path and query. The secret is
// not added; call Apply on the request for that.
func (l *Lease) URL(path string, q url.Values) string {
	u := strings.TrimRight(l.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func ceilSeconds(d time.Duration) int64 {
	return int64(math.Ceil(d.Seconds()))
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	c.setPublicHeaders(req)
}

// setPublicHeaders sets every header but the token.
func (c *Client) setPublicHeaders(req *http.Request) {
	if c.accessPassword != "" {
		req.Header.Set("X-Acnologia-Access", c.accessPassword)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseError(resp.StatusCode, data, resp.Header)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("acnologia: decode response: %w", err)
	}
	return nil
}

func parseError(status int, data []byte, h http.Header) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RetryAfterSec float64 `json:"retry_after_sec"`
		Limit         string  `json:"limit"`
		ResetAt       string  `json:"reset_at"`
	}
	_ = json.Unmarshal(data, &e)
	retry := time.Duration(e.RetryAfterSec * float64(time.Second))
	if retry <= 0 {
		if n, err := strconv.ParseFloat(strings.TrimSpace(h.Get("Retry-After")), 64); err == nil && n > 0 {
			retry = time.Duration(n * float64(time.Second))
		}
	}
	base := APIError{StatusCode: status, Code: e.Error.Code, Message: e.Error.Message, RetryAfter: retry}
	switch e.Error.Code {
	case "service_unavailable":
		return &ErrServiceUnavailable{APIError: base, RetryAfter: retry}
	case "app_limit_reached":
		reset, _ := time.Parse(time.RFC3339Nano, e.ResetAt)
		return &ErrAppLimitReached{APIError: base, Limit: e.Limit, ResetAt: reset}
	case "share_revoked", "share_paused", "share_expired":
		st := strings.TrimSpace(h.Get("X-Acnologia-Share"))
		if st == "" {
			st = strings.TrimPrefix(e.Error.Code, "share_")
		}
		return &ErrShareUnavailable{APIError: base, Status: st}
	}
	if e.Error.Code == "no_key_available" || e.Error.Code == "all_keys_busy" ||
		(status == http.StatusServiceUnavailable && e.Error.Code == "") {
		code := e.Error.Code
		if code == "" {
			code = "no_key_available"
		}
		return &ErrNoKeyAvailable{
			RetryAfter: time.Duration(e.RetryAfterSec * float64(time.Second)),
			Message:    e.Error.Message,
			Code:       code,
			Busy:       code == "all_keys_busy",
		}
	}
	msg := e.Error.Message
	if msg == "" && e.Error.Code == "" {
		msg = strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg == "" {
			msg = http.StatusText(status)
		}
	}
	return &APIError{StatusCode: status, Code: e.Error.Code, Message: msg, RetryAfter: retry}
}
