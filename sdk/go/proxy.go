package acnologia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Proxy sends requests through Acnologia's proxy for one service. Acnologia injects
// a key, retries on rate limits with the next key, and never returns the
// secret. Non-2xx upstream responses are returned as-is, never as errors.
// Responses carry X-Acnologia-Key-Id and X-Acnologia-Attempts headers; read
// them, and the other Acnologia headers, with ProxyInfo.
//
// The service may also be a service group or a capability slug.
type Proxy struct {
	client  *Client
	service string
	header  http.Header
}

// Proxy returns a proxy helper for service.
func (c *Client) Proxy(service string) *Proxy { return &Proxy{client: c, service: service} }

// with returns a copy of p that also sends header k: v.
func (p *Proxy) with(k, v string) *Proxy {
	h := p.header.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Set(k, v)
	return &Proxy{client: p.client, service: p.service, header: h}
}

// WithIdempotencyKey returns a copy of p whose requests send
// Idempotency-Key: key, so a retried POST is answered from the first
// result (the response then has Idempotent-Replayed: true).
func (p *Proxy) WithIdempotencyKey(key string) *Proxy { return p.with("Idempotency-Key", key) }

// WithNoCache returns a copy of p whose requests send Cache-Control:
// no-cache, so the server skips its response cache.
func (p *Proxy) WithNoCache() *Proxy { return p.with("Cache-Control", "no-cache") }

func (p *Proxy) prefix() string {
	return p.client.baseURL + "/proxy/" + url.PathEscape(p.service)
}

// URL returns the full proxy URL for a provider path and query.
func (p *Proxy) URL(path string, q url.Values) string {
	u := p.prefix() + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// Do sends req through the proxy. The request URL may be relative
// ("/search.json?q=x") or absolute; only its path and query are used, and the
// path is resolved under /proxy/{service}/. A path that already starts with
// /proxy/{service}/ is kept as is.
func (p *Proxy) Do(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(p.prefix())
	if err != nil {
		return nil, err
	}
	path := req.URL.EscapedPath()
	svcPrefix := "/proxy/" + url.PathEscape(p.service)
	if path == svcPrefix || strings.HasPrefix(path, svcPrefix+"/") {
		path = strings.TrimPrefix(path, svcPrefix)
	}
	full := strings.TrimRight(target.EscapedPath(), "/") + "/" + strings.TrimLeft(path, "/")
	rel, err := url.Parse(full)
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	u := *target
	u.Path = rel.Path
	u.RawPath = rel.RawPath
	u.RawQuery = req.URL.RawQuery
	u.Fragment = ""
	out.URL = &u
	out.Host = ""
	out.RequestURI = ""
	if out.Header == nil {
		out.Header = http.Header{}
	}
	for k, vs := range p.header {
		out.Header[k] = append([]string(nil), vs...)
	}
	p.client.setHeaders(out)
	return p.client.proxyHTTPClient().Do(out)
}

// proxyHTTPClient is a shallow copy of the configured client that never
// follows redirects, so the app token is not replayed to another host. The
// SDK's default timeout is dropped because proxied bodies may stream for
// longer; bound proxy calls with the request context instead.
func (c *Client) proxyHTTPClient() *http.Client {
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.defaultHTTP {
		hc.Timeout = 0
	}
	return &hc
}

// Request builds and sends a request through the proxy.
func (p *Proxy) Request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.URL(path, nil), body)
	if err != nil {
		return nil, err
	}
	return p.Do(req)
}

// Get sends a GET with optional query parameters.
func (p *Proxy) Get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL(path, q), nil)
	if err != nil {
		return nil, err
	}
	return p.Do(req)
}

// Post sends a POST with the given content type and body.
func (p *Proxy) Post(ctx context.Context, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL(path, nil), body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return p.Do(req)
}

// PostJSON encodes v as JSON and POSTs it.
func (p *Proxy) PostJSON(ctx context.Context, path string, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return p.Post(ctx, path, "application/json", bytes.NewReader(b))
}

// Tried is one route a capability call tried: the service and the status it
// answered.
type Tried struct {
	Service string
	Status  int
}

// ProxyMeta is what Acnologia says about a proxied answer in its headers.
type ProxyMeta struct {
	// KeyID is the key that served the request (X-Acnologia-Key-Id), or 0
	// when absent, as for a share's token.
	KeyID int64
	// Attempts is how many keys were tried (X-Acnologia-Attempts).
	Attempts int
	// Service answered the request (X-Acnologia-Service).
	Service string
	// Cache is "hit", "miss" and so on, when caching applies (X-Acnologia-Cache).
	Cache string
	// Replayed is true when the answer was replayed for an Idempotency-Key.
	Replayed bool
	// Capability is the capability called (X-Acnologia-Capability).
	Capability string
	// Tried lists every route a capability call tried (X-Acnologia-Tried).
	Tried []Tried
}

// ProxyInfo reads the Acnologia headers of a response from Proxy or
// Capability.
func ProxyInfo(resp *http.Response) ProxyMeta {
	if resp == nil {
		return ProxyMeta{}
	}
	h := resp.Header
	m := ProxyMeta{
		Service:    h.Get("X-Acnologia-Service"),
		Cache:      h.Get("X-Acnologia-Cache"),
		Replayed:   strings.EqualFold(strings.TrimSpace(h.Get("Idempotent-Replayed")), "true"),
		Capability: h.Get("X-Acnologia-Capability"),
		Tried:      parseTried(h.Get("X-Acnologia-Tried")),
	}
	m.KeyID, _ = strconv.ParseInt(strings.TrimSpace(h.Get("X-Acnologia-Key-Id")), 10, 64)
	m.Attempts, _ = strconv.Atoi(strings.TrimSpace(h.Get("X-Acnologia-Attempts")))
	return m
}

// parseTried parses "svc=200, other=429" into Tried pairs.
func parseTried(v string) []Tried {
	var out []Tried
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		svc, st, _ := strings.Cut(part, "=")
		n, _ := strconv.Atoi(strings.TrimSpace(st))
		out = append(out, Tried{Service: strings.TrimSpace(svc), Status: n})
	}
	return out
}
