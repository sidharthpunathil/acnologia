package acnologia

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

// DefaultCAPath is where SaveForwardProxyCA writes the CA by default, and
// the path ForwardProxyEnv points the CA variables at.
const DefaultCAPath = "acnologia-ca.pem"

// ForwardHost is a host the forward proxy opens and puts keys on.
type ForwardHost struct {
	Host    string `json:"host"`
	Service string `json:"service"`
	Name    string `json:"name"`
	// Path is the base path under the host, when the service has one.
	Path string `json:"path,omitempty"`
}

// ForwardProxy is how to use the forward proxy, from GET /api/v1/proxy. It
// never holds the token: ProxyURLWithToken and Env carry a "<app token>"
// placeholder. Use ForwardProxyURL or ForwardProxyEnv for the real values.
type ForwardProxy struct {
	ProxyURL          string            `json:"proxy_url"`
	ProxyURLWithToken string            `json:"proxy_url_with_token"`
	CAURL             string            `json:"ca_url"`
	CAFingerprint     string            `json:"ca_fingerprint"`
	Passthrough       bool              `json:"passthrough"`
	Hosts             []ForwardHost     `json:"hosts"`
	Env               map[string]string `json:"env"`
	ServiceHeader     string            `json:"service_header"`
}

// ForwardProxy returns how to use the forward proxy: its URL, the CA, and
// the hosts it puts keys on for this token.
func (c *Client) ForwardProxy(ctx context.Context) (*ForwardProxy, error) {
	var out ForwardProxy
	if err := c.do(ctx, http.MethodGet, "/api/v1/proxy", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ForwardProxyCA returns the forward proxy's CA certificate as PEM. The
// route is public, so the token is not sent.
func (c *Client) ForwardProxyCA(ctx context.Context) (string, error) {
	b, err := c.getText(ctx, "/api/v1/proxy/ca.pem", "application/x-pem-file", false)
	return string(b), err
}

// SaveForwardProxyCA writes the CA certificate to path (DefaultCAPath when
// empty) and returns the path.
func (c *Client) SaveForwardProxyCA(ctx context.Context, path string) (string, error) {
	if path == "" {
		path = DefaultCAPath
	}
	pem, err := c.ForwardProxyCA(ctx)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pem), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// proxyURLWithToken puts user "app" and the client's token on proxyURL.
func (c *Client) proxyURLWithToken(proxyURL string) (*url.URL, error) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("acnologia: bad forward proxy URL %q", proxyURL)
	}
	u.User = url.UserPassword("app", c.token)
	return u, nil
}

// ForwardProxyURL returns the forward proxy URL with user "app" and the
// client's token as the password, such as http://app:acn_...@host:8888.
// The result contains the token: do not log it.
func (c *Client) ForwardProxyURL(ctx context.Context) (string, error) {
	fp, err := c.ForwardProxy(ctx)
	if err != nil {
		return "", err
	}
	u, err := c.proxyURLWithToken(fp.ProxyURL)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// ForwardProxyEnv returns the environment that sends an app's HTTP through
// the forward proxy: HTTPS_PROXY and HTTP_PROXY (the ForwardProxyURL),
// NODE_USE_ENV_PROXY=1, and NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE and
// SSL_CERT_FILE set to caPath (DefaultCAPath when empty). Save the CA there
// with SaveForwardProxyCA. The result contains the token: do not log it.
func (c *Client) ForwardProxyEnv(ctx context.Context, caPath string) (map[string]string, error) {
	if caPath == "" {
		caPath = DefaultCAPath
	}
	u, err := c.ForwardProxyURL(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"HTTPS_PROXY":         u,
		"HTTP_PROXY":          u,
		"NODE_USE_ENV_PROXY":  "1",
		"NODE_EXTRA_CA_CERTS": caPath,
		"REQUESTS_CA_BUNDLE":  caPath,
		"SSL_CERT_FILE":       caPath,
	}, nil
}

// ForwardProxyTransport returns a transport that sends every request
// through the forward proxy, with the token as proxy credentials, and
// trusts the system roots plus the Acnologia CA. Use it as the Transport of
// the http.Client that calls provider URLs directly:
//
//	tr, err := c.ForwardProxyTransport(ctx)
//	hc := &http.Client{Transport: tr}
//	resp, err := hc.Get("https://api.openai.com/v1/models")
func (c *Client) ForwardProxyTransport(ctx context.Context) (*http.Transport, error) {
	fp, err := c.ForwardProxy(ctx)
	if err != nil {
		return nil, err
	}
	u, err := c.proxyURLWithToken(fp.ProxyURL)
	if err != nil {
		return nil, err
	}
	pem, err := c.ForwardProxyCA(ctx)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		return nil, errors.New("acnologia: forward proxy CA is not a PEM certificate")
	}
	var tr *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = dt.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.Proxy = http.ProxyURL(u)
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return tr, nil
}
