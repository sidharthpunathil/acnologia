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
	"runtime"
	"strings"
)

// DefaultCAPath is where SaveForwardProxyCA writes the CA by default, and
// the path ForwardProxyEnv points NODE_EXTRA_CA_CERTS at.
const DefaultCAPath = "acnologia-ca.pem"

// DefaultCABundlePath is where SaveForwardProxyCABundle writes the system
// roots plus the CA by default, and the path ForwardProxyEnv points
// SSL_CERT_FILE and REQUESTS_CA_BUNDLE at.
const DefaultCABundlePath = "acnologia-ca-bundle.pem"

// NoProxy keeps local calls off the forward proxy, which refuses hosts that
// are no service.
const NoProxy = "localhost,127.0.0.1,::1"

// systemRootPaths are the usual system root bundles, tried in order.
var systemRootPaths = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

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

// SaveForwardProxyCABundle writes the system roots followed by the CA
// certificate to path (DefaultCABundlePath when empty) and returns the path.
// SSL_CERT_FILE and REQUESTS_CA_BUNDLE replace the whole trust store, so
// they need this bundle, not the CA alone. The roots come from
// $SSL_CERT_FILE when it is a PEM bundle other than path, else the first
// usual system bundle found. It fails when there is none, as on Windows.
func (c *Client) SaveForwardProxyCABundle(ctx context.Context, path string) (string, error) {
	if path == "" {
		path = DefaultCABundlePath
	}
	roots := systemRootsPEM(path)
	if roots == "" {
		return "", errors.New("acnologia: no system root certificates found. SSL_CERT_FILE and REQUESTS_CA_BUNDLE need a bundle of the system roots plus the Acnologia CA; NODE_EXTRA_CA_CERTS takes the CA alone")
	}
	pem, err := c.ForwardProxyCA(ctx)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(caBundle(roots, pem)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// systemRootsPEM returns the first system root bundle found, skipping the
// file at target.
func systemRootsPEM(target string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	paths := systemRootPaths
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		paths = append([]string{f}, paths...)
	}
	ts, terr := os.Stat(target)
	for _, p := range paths {
		if ps, err := os.Stat(p); err == nil && terr == nil && os.SameFile(ps, ts) {
			continue
		}
		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), "BEGIN CERTIFICATE") {
			return string(b)
		}
	}
	return ""
}

// caBundle is the system roots followed by the Acnologia CA.
func caBundle(roots, ca string) string {
	return strings.TrimRight(roots, "\n") + "\n\n# Acnologia forward proxy CA\n" + strings.TrimRight(ca, "\n") + "\n"
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
// NODE_USE_ENV_PROXY=1, NODE_EXTRA_CA_CERTS set to caPath (DefaultCAPath
// when empty), SSL_CERT_FILE and REQUESTS_CA_BUNDLE set to the optional
// bundlePath (DefaultCABundlePath when empty), and NO_PROXY and no_proxy
// set to NoProxy. Save the CA with SaveForwardProxyCA and the bundle with
// SaveForwardProxyCABundle: SSL_CERT_FILE and REQUESTS_CA_BUNDLE replace
// the whole trust store, so they need the system roots plus the CA. The
// result contains the token: do not log it.
func (c *Client) ForwardProxyEnv(ctx context.Context, caPath string, bundlePath ...string) (map[string]string, error) {
	if caPath == "" {
		caPath = DefaultCAPath
	}
	bundle := DefaultCABundlePath
	if len(bundlePath) > 0 && bundlePath[0] != "" {
		bundle = bundlePath[0]
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
		"REQUESTS_CA_BUNDLE":  bundle,
		"SSL_CERT_FILE":       bundle,
		"NO_PROXY":            NoProxy,
		"no_proxy":            NoProxy,
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
