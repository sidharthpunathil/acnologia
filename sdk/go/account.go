package acnologia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MeApp names the app behind the token.
type MeApp struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// MeLimits are the app's request and budget limits; nil means no limit.
type MeLimits struct {
	DailyRequests   *int64   `json:"daily_requests"`
	MonthlyRequests *int64   `json:"monthly_requests"`
	DailyBudget     *float64 `json:"daily_budget"`
	MonthlyBudget   *float64 `json:"monthly_budget"`
}

// LimitHit names the limit the app reached and when it resets.
type LimitHit struct {
	Limit   string    `json:"limit"`
	ResetAt time.Time `json:"reset_at"`
}

// MeUsage is the app's use so far today and this month.
type MeUsage struct {
	TodayRequests int64      `json:"today_requests"`
	TodayCost     float64    `json:"today_cost"`
	MonthRequests int64      `json:"month_requests"`
	MonthCost     float64    `json:"month_cost"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	LimitHit      *LimitHit  `json:"limit_hit"`
}

// MeService is a service the token may call, as GET /api/v1/me lists it.
type MeService struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Category string `json:"category"`
	// Available counts keys free right now for a share's token; for an
	// ordinary app it is 1 or 0.
	Available int `json:"available"`
	// LogoURL is the logo as a data: URI, or nil.
	LogoURL *string `json:"logo_url"`
}

// MeShare describes the share behind a share's token.
type MeShare struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerName string `json:"owner_name"`
	Recipient string `json:"recipient"`
	Label     string `json:"label"`
	// Status is "active", "paused", "expired" or "revoked".
	Status string     `json:"status"`
	Mode   string     `json:"mode"`
	EndsAt *time.Time `json:"ends_at"`
}

// Me is the token's own access, from GET /api/v1/me.
type Me struct {
	App      MeApp       `json:"app"`
	Limits   MeLimits    `json:"limits"`
	Usage    MeUsage     `json:"usage"`
	LimitHit *LimitHit   `json:"limit_hit"`
	Services []MeService `json:"services"`
	// Share is nil for an ordinary app.
	Share *MeShare `json:"share"`
}

// Me returns the token's own access: its app, limits, usage, services and,
// for a share's token, the share. A share that stopped still answers here,
// with its Status.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Guide is a service's usage guide.
type Guide struct {
	Markdown string `json:"markdown"`
	// Generated is true when nobody wrote the guide and it was built from
	// the provider spec.
	Generated bool `json:"generated"`
	// Guide is the structured guide, as JSON.
	Guide json.RawMessage `json:"guide"`
}

func guidePath(slug string) string {
	return "/api/v1/services/" + url.PathEscape(slug) + "/guide"
}

// Guide returns the usage guide of a service the token may use.
func (c *Client) Guide(ctx context.Context, slug string) (*Guide, error) {
	var out Guide
	if err := c.do(ctx, http.MethodGet, guidePath(slug), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GuideMarkdown returns the usage guide of a service as Markdown.
func (c *Client) GuideMarkdown(ctx context.Context, slug string) (string, error) {
	b, err := c.getText(ctx, guidePath(slug), "text/markdown", true)
	return string(b), err
}

// getText GETs path with the given Accept header and returns the body. With
// auth false the token is not sent.
func (c *Client) getText(ctx context.Context, path, accept string, auth bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if auth {
		c.setHeaders(req)
	} else {
		c.setPublicHeaders(req)
	}
	req.Header.Set("Accept", accept)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, parseError(resp.StatusCode, data, resp.Header)
	}
	return data, nil
}

// CapabilityRoute is one provider that can serve a capability.
type CapabilityRoute struct {
	Service string `json:"service"`
	// Mapped is true when the route maps the provider's answer to the
	// capability's common shape.
	Mapped bool `json:"mapped"`
}

// Capability is one call served by whichever provider can, from
// GET /api/v1/capabilities.
type Capability struct {
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Strategy    string            `json:"strategy"`
	Params      json.RawMessage   `json:"params"`
	Routes      []CapabilityRoute `json:"routes"`
	URL         string            `json:"url"`
}

// Capabilities lists the capabilities the app may call. It is empty for a
// share's token.
func (c *Client) Capabilities(ctx context.Context) ([]Capability, error) {
	var out struct {
		Capabilities []Capability `json:"capabilities"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/capabilities", nil, &out); err != nil {
		return nil, err
	}
	return out.Capabilities, nil
}

// CapabilityOptions are optional parameters for Capability.
type CapabilityOptions struct {
	// Service uses only that provider's route (?_service=).
	Service string
	// Raw returns the provider's own body, unmapped (?_raw=1).
	Raw bool
	// IdempotencyKey is sent as Idempotency-Key.
	IdempotencyKey string
	// NoCache sends Cache-Control: no-cache.
	NoCache bool
}

// Capability calls a capability: POST /proxy/{slug} with params as the JSON
// body. The answer comes back as is, like a Proxy call; read
// X-Acnologia-Capability, X-Acnologia-Service and X-Acnologia-Tried with
// ProxyInfo. Bound the call with ctx.
func (c *Client) Capability(ctx context.Context, slug string, params any, opts *CapabilityOptions) (*http.Response, error) {
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("acnologia: encode params: %w", err)
	}
	q := url.Values{}
	if opts != nil {
		if opts.Service != "" {
			q.Set("_service", opts.Service)
		}
		if opts.Raw {
			q.Set("_raw", "1")
		}
	}
	u := c.baseURL + "/proxy/" + url.PathEscape(slug)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	if opts != nil {
		if k := strings.TrimSpace(opts.IdempotencyKey); k != "" {
			req.Header.Set("Idempotency-Key", k)
		}
		if opts.NoCache {
			req.Header.Set("Cache-Control", "no-cache")
		}
	}
	return c.proxyHTTPClient().Do(req)
}
