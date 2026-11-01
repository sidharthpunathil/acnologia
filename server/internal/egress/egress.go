// Package egress routes upstream traffic through outbound proxies.
//
// A key can name a proxy, else its service can, else traffic goes direct
// (still honoring HTTPS_PROXY from the environment). One transport is kept per
// proxy so connections are reused.
package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/store"
)

// Parse checks a proxy URL and returns it with a display hint that leaves
// out the credentials.
func Parse(raw string) (*url.URL, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, "", errors.New("proxy URL does not parse")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, "", errors.New("proxy URL must start with http://, https://, socks5:// or socks5h://")
	}
	if u.Hostname() == "" {
		return nil, "", errors.New("proxy URL needs a host")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, "", errors.New("proxy URL must not have a path")
	}
	hint := u.Scheme + "://" + u.Host
	if u.User != nil {
		hint = u.Scheme + "://" + u.User.Username() + ":***@" + u.Host
	}
	return u, hint, nil
}

// NewTransport builds the upstream transport, through proxyURL when given.
func NewTransport(proxyURL *url.URL) *http.Transport {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
	}
	if proxyURL != nil {
		t.Proxy = http.ProxyURL(proxyURL)
	}
	return t
}

type Manager struct {
	Store *store.Store
	Box   *crypto.Box
	// Direct is used when no proxy applies.
	Direct http.RoundTripper
	// EchoURL answers with the caller's IP; used by Test.
	EchoURL string

	mu sync.Mutex
	m  map[int64]*http.Transport
}

func New(s *store.Store, box *crypto.Box, direct http.RoundTripper) *Manager {
	return &Manager{Store: s, Box: box, Direct: direct, EchoURL: "https://www.cloudflare.com/cdn-cgi/trace", m: map[int64]*http.Transport{}}
}

// Pick returns the proxy id that applies: the key's, else the service's.
func Pick(keyEgress, serviceEgress *int64) *int64 {
	if keyEgress != nil {
		return keyEgress
	}
	return serviceEgress
}

// Transport returns the round tripper for a proxy id; nil means direct.
func (m *Manager) Transport(ctx context.Context, id *int64) (http.RoundTripper, error) {
	if m == nil || id == nil {
		if m == nil || m.Direct == nil {
			return http.DefaultTransport, nil
		}
		return m.Direct, nil
	}
	m.mu.Lock()
	t, ok := m.m[*id]
	m.mu.Unlock()
	if ok {
		return t, nil
	}
	p, err := m.Store.GetEgress(ctx, *id)
	if err != nil {
		return nil, fmt.Errorf("egress proxy %d: %w", *id, err)
	}
	raw, err := m.Box.Decrypt(p.URLEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt egress proxy URL")
	}
	u, _, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	t = NewTransport(u)
	m.mu.Lock()
	if old, ok := m.m[*id]; ok {
		t.CloseIdleConnections()
		t = old
	} else {
		m.m[*id] = t
	}
	m.mu.Unlock()
	return t, nil
}

// Invalidate drops the cached transport after a proxy changes or is deleted.
func (m *Manager) Invalidate(id int64) {
	m.mu.Lock()
	if t, ok := m.m[id]; ok {
		t.CloseIdleConnections()
		delete(m.m, id)
	}
	m.mu.Unlock()
}

// Client wraps a round tripper like an upstream client: redirects are passed
// back to the caller instead of being followed with a key attached.
func Client(rt http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{Transport: rt, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Test makes a request through the proxy and returns the egress IP it reports.
func (m *Manager) Test(ctx context.Context, id int64) (string, error) {
	m.Invalidate(id)
	rt, err := m.Transport(ctx, &id)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, m.EchoURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := Client(rt, 15*time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("echo service answered %d", resp.StatusCode)
	}
	return parseIP(io.LimitReader(resp.Body, 8<<10)), nil
}

// parseIP reads "ip=1.2.3.4" from a Cloudflare trace, or a bare IP body.
func parseIP(r io.Reader) string {
	sc := bufio.NewScanner(r)
	first := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first == "" {
			first = line
		}
		if ip, ok := strings.CutPrefix(line, "ip="); ok {
			return ip
		}
	}
	if net.ParseIP(first) != nil {
		return first
	}
	return ""
}
