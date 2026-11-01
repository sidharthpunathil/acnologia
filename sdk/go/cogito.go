// Package cogito is a Go client for Cogito, a self-hosted API key pool.
//
// Cogito can be consumed in two ways:
//
//   - Lease mode: Lease a key, call the provider yourself, then Report the
//     outcome. WithKey wraps this loop and rotates keys automatically.
//   - Proxy mode: send requests through Proxy; Cogito injects a key and
//     rotates on rate limits without ever exposing the secret.
package cogito

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Version is the SDK version.
const Version = "0.1.0"

// Client talks to a Cogito server using an app token (cog_...).
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	userAgent  string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.httpClient = hc } }

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// New creates a client. baseURL is the Cogito server, e.g. http://localhost:8080.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: http.DefaultClient,
		userAgent:  "cogito-go/" + Version,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Auth describes where a provider expects the key.
// Type is "query", "header" or "bearer"; Name is the param or header name.
type Auth struct {
	Type string `json:"type"`
	Name string `json:"name"`
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

	client *Client
}

type leaseWire struct {
	LeaseID   string `json:"lease_id"`
	KeyID     int64  `json:"key_id"`
	Service   string `json:"service"`
	Secret    string `json:"secret"`
	Auth      Auth   `json:"auth"`
	BaseURL   string `json:"base_url"`
	ExpiresAt string `json:"expires_at"`
}

func (c *Client) leaseFromWire(w *leaseWire) *Lease {
	if w == nil {
		return nil
	}
	l := &Lease{LeaseID: w.LeaseID, KeyID: w.KeyID, Service: w.Service, Secret: w.Secret,
		Auth: w.Auth, BaseURL: w.BaseURL, client: c}
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
			return nil, fmt.Errorf("cogito: lease wait must be between 0 and %s", MaxWait)
		}
		if opts.Wait > 0 {
			body["wait_sec"] = ceilSeconds(opts.Wait)
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
		return nil, fmt.Errorf("cogito: lease %q has no client", l.LeaseID)
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

// Apply puts the secret on req where the provider expects it: a query
// parameter, a header, or an "Authorization: Bearer" style header.
func (l *Lease) Apply(req *http.Request) {
	switch l.Auth.Type {
	case "query":
		q := req.URL.Query()
		q.Set(l.Auth.Name, l.Secret)
		req.URL.RawQuery = q.Encode()
	case "bearer":
		name := l.Auth.Name
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, "Bearer "+l.Secret)
	default: // "header"
		name := l.Auth.Name
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, l.Secret)
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
		return parseError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("cogito: decode response: %w", err)
	}
	return nil
}

func parseError(status int, data []byte) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RetryAfterSec float64 `json:"retry_after_sec"`
	}
	_ = json.Unmarshal(data, &e)
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
	return &APIError{StatusCode: status, Code: e.Error.Code, Message: msg}
}
