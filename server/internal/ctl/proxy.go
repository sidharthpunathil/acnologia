package ctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"
)

// The forward proxy from the command line: apps set Acnologia as their
// HTTPS_PROXY and call provider URLs directly.
//
//	acn proxy                      the proxy URL, CA fingerprint and hosts
//	acn proxy ca [--out file|-] [--bundle]
//	                               save the CA certificate apps must trust,
//	                               or the system roots plus the CA
//	acn proxy env [--token acn_...] [--ca file] [--bundle file]
//	                               export lines for a shell

type proxySetup struct {
	ProxyURL      string `json:"proxy_url"`
	CAURL         string `json:"ca_url"`
	CAFingerprint string `json:"ca_fingerprint"`
	Passthrough   bool   `json:"passthrough"`
	ListenAddr    string `json:"listen_addr"`
	Hosts         []struct {
		Host    string `json:"host"`
		Service string `json:"service"`
		Path    string `json:"path"`
	} `json:"hosts"`
}

func (c *cli) proxy(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "hosts":
		return c.proxyShow()
	case "ca":
		return c.proxyCA(args)
	case "env":
		return c.proxyEnv(args)
	}
	return fmt.Errorf("usage: %s proxy [ca [--out file] [--bundle] | env [--token acn_...] [--ca file] [--bundle file]]", c.name())
}

func (c *cli) proxyShow() error {
	var raw json.RawMessage
	if err := c.call("GET", "/api/admin/forward-proxy", nil, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var s proxySetup
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Proxy URL:      %s  (app token as the password: http://app:acn_...@host:port)\n", s.ProxyURL)
	fmt.Fprintf(c.out, "CA certificate: %s\n", s.CAURL)
	fmt.Fprintf(c.out, "CA SHA-256:     %s\n", s.CAFingerprint)
	pass := "off: other hosts are refused, list them in NO_PROXY"
	if s.Passthrough {
		pass = "on: HTTPS to other hosts is tunneled untouched"
	}
	fmt.Fprintf(c.out, "Passthrough:    %s\n\n", pass)
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tSERVICE")
	for _, h := range s.Hosts {
		fmt.Fprintf(tw, "%s%s\t%s\n", h.Host, h.Path, h.Service)
	}
	return tw.Flush()
}

// fetchCA downloads the forward proxy's CA certificate (public, no token).
func (c *cli) fetchCA() ([]byte, error) {
	ctx, err := c.current()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", ctx.URL+"/api/v1/proxy/ca.pem", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	pem, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 || !strings.Contains(string(pem), "BEGIN CERTIFICATE") {
		return nil, fmt.Errorf("could not download the CA: server answered %d", resp.StatusCode)
	}
	return pem, nil
}

// systemRootPaths are the usual places a system keeps its root certificates
// as one PEM file: Debian and Ubuntu, RHEL and Fedora, openSUSE, then Alpine
// and macOS. Go cannot export the system pool's certificates on every
// platform, so the CLI reads these files instead.
var systemRootPaths = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// systemRoots finds a PEM file of root certificates and returns its path and
// contents. An SSL_CERT_FILE that is already set comes first, unless it lives
// in skipDir: that is a bundle this command wrote, which already holds the
// Acnologia CA. Windows keeps its roots in the certificate store, not in a
// file, so there is none there.
func systemRoots(goos string, getenv func(string) string, skipDir string) (string, []byte) {
	if goos == "windows" {
		return "", nil
	}
	paths := systemRootPaths
	if f := getenv("SSL_CERT_FILE"); f != "" {
		if skipDir == "" || filepath.Dir(filepath.Clean(f)) != filepath.Clean(skipDir) {
			paths = append([]string{f}, paths...)
		}
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil && bytes.Contains(b, []byte("BEGIN CERTIFICATE")) {
			return p, b
		}
	}
	return "", nil
}

// caBundle is the system roots followed by the Acnologia CA. Python
// (requests, httpx), Ruby and OpenSSL read SSL_CERT_FILE and
// REQUESTS_CA_BUNDLE as the whole trust store, so the CA alone would break
// TLS to every host the proxy passes through untouched.
func caBundle(roots, ca []byte) []byte {
	var b bytes.Buffer
	b.Write(bytes.TrimRight(roots, "\n"))
	b.WriteString("\n\n# Acnologia forward proxy CA\n")
	b.Write(bytes.TrimRight(ca, "\n"))
	b.WriteString("\n")
	return b.Bytes()
}

// proxyDir is where proxy env keeps its CA files: next to the CLI config.
func (c *cli) proxyDir() string {
	return filepath.Dir(c.configPath())
}

// writeCAFiles saves the CA, and a bundle of the system roots plus the CA,
// next to the CLI config: one pair of files per CA, so two servers' files do
// not overwrite each other. A ca or bundle path the caller gives is used
// instead of the default, and a given ca is not written. The bundle path
// comes back empty when no system roots were found (Windows keeps them in
// the certificate store), since the CA alone would break every other host.
func (c *cli) writeCAFiles(pem []byte, fingerprint, ca, bundle string) (string, string, error) {
	dir := c.proxyDir()
	tag := strings.ToLower(strings.ReplaceAll(fingerprint, ":", ""))
	if len(tag) > 16 {
		tag = tag[:16]
	}
	if tag != "" {
		tag = "-" + tag
	}
	if ca == "" || bundle == "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", err
		}
	}
	if ca == "" {
		ca = filepath.Join(dir, "proxy-ca"+tag+".pem")
		if err := os.WriteFile(ca, pem, 0o644); err != nil {
			return "", "", err
		}
	}
	src, roots := systemRoots(runtime.GOOS, os.Getenv, dir)
	if src == "" {
		return ca, "", nil
	}
	if bundle == "" {
		bundle = filepath.Join(dir, "proxy-ca-bundle"+tag+".pem")
	}
	if err := os.WriteFile(bundle, caBundle(roots, pem), 0o644); err != nil {
		return "", "", err
	}
	return ca, bundle, nil
}

// proxyCA saves the CA certificate, to acnologia-ca.pem by default. With
// --bundle it saves the system roots plus the CA instead
// (acnologia-ca-bundle.pem by default), for SSL_CERT_FILE and
// REQUESTS_CA_BUNDLE.
func (c *cli) proxyCA(args []string) error {
	out, bundle := "", false
	for i := 0; i < len(args); i++ {
		switch {
		case (args[i] == "--out" || args[i] == "-o") && i+1 < len(args):
			out = args[i+1]
			i++
		case args[i] == "--bundle":
			bundle = true
		}
	}
	if out == "" {
		out = "acnologia-ca.pem"
		if bundle {
			out = "acnologia-ca-bundle.pem"
		}
	}
	pem, err := c.fetchCA()
	if err != nil {
		return err
	}
	data := pem
	if bundle {
		src, roots := systemRoots(runtime.GOOS, os.Getenv, c.proxyDir())
		if src == "" {
			return fmt.Errorf("found no system certificate bundle to combine with the CA (looked at SSL_CERT_FILE and %s); on Windows use NODE_EXTRA_CA_CERTS with the CA alone, or build a bundle yourself", strings.Join(systemRootPaths, ", "))
		}
		data = caBundle(roots, pem)
	}
	if out == "-" {
		_, err := c.out.Write(data)
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	if bundle {
		fmt.Fprintf(c.out, "Saved the system roots plus the forward proxy CA to %s. Point SSL_CERT_FILE and REQUESTS_CA_BUNDLE at it.\n", out)
		return nil
	}
	fmt.Fprintf(c.out, "Saved the forward proxy CA to %s. Point NODE_EXTRA_CA_CERTS at it. SSL_CERT_FILE and REQUESTS_CA_BUNDLE replace the whole trust store, so give them a bundle instead: %s proxy ca --bundle\n", out, c.name())
	return nil
}

// noProxy keeps local calls off the proxy, which would refuse them as
// not_a_service.
const noProxy = "localhost,127.0.0.1,::1"

// proxyEnv prints export lines. Without --token the proxy URL reads the
// token from $ACN_APP_TOKEN when the shell runs it. It saves the CA, and a
// bundle of the system roots plus the CA, next to the CLI config:
// NODE_EXTRA_CA_CERTS adds to Node's roots, but SSL_CERT_FILE and
// REQUESTS_CA_BUNDLE replace the trust store and need the bundle.
func (c *cli) proxyEnv(args []string) error {
	token, ca, bundle := "", "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--token" && i+1 < len(args):
			token = args[i+1]
			i++
		case args[i] == "--ca" && i+1 < len(args):
			ca = args[i+1]
			i++
		case args[i] == "--bundle" && i+1 < len(args):
			bundle = args[i+1]
			i++
		}
	}
	var s proxySetup
	if err := c.call("GET", "/api/admin/forward-proxy", nil, &s); err != nil {
		return err
	}
	u, err := url.Parse(s.ProxyURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("the server gave no usable proxy URL (%q)", s.ProxyURL)
	}
	pem, err := c.fetchCA()
	if err != nil {
		return err
	}
	ca, bundle, err = c.writeCAFiles(pem, s.CAFingerprint, ca, bundle)
	if err != nil {
		return err
	}
	src := bundle
	cred := "${ACN_APP_TOKEN}"
	if token != "" {
		cred = url.PathEscape(token)
	}
	proxy := u.Scheme + "://app:" + cred + "@" + u.Host
	q := func(v string) string { return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"` }
	fmt.Fprintf(c.out, "export HTTPS_PROXY=%s\n", q(proxy))
	fmt.Fprintf(c.out, "export HTTP_PROXY=%s\n", q(proxy))
	fmt.Fprintf(c.out, "export NO_PROXY=%s\n", q(noProxy))
	fmt.Fprintf(c.out, "export no_proxy=%s\n", q(noProxy))
	fmt.Fprintln(c.out, "export NODE_USE_ENV_PROXY=1")
	fmt.Fprintf(c.out, "export NODE_EXTRA_CA_CERTS=%s\n", q(ca))
	if src != "" {
		fmt.Fprintf(c.out, "export SSL_CERT_FILE=%s\n", q(bundle))
		fmt.Fprintf(c.out, "export REQUESTS_CA_BUNDLE=%s\n", q(bundle))
	} else {
		fmt.Fprintln(c.err, "# Found no system certificate bundle (looked at SSL_CERT_FILE and "+strings.Join(systemRootPaths, ", ")+"),")
		fmt.Fprintln(c.err, "# so SSL_CERT_FILE and REQUESTS_CA_BUNDLE are left unset. They replace the whole trust store, and the CA")
		fmt.Fprintln(c.err, "# alone would break HTTPS to every other host. For Python or Ruby, build a bundle of the system roots")
		fmt.Fprintln(c.err, "# plus "+ca+" and point both at it.")
	}
	if token == "" {
		fmt.Fprintln(c.err, "# set ACN_APP_TOKEN to an app token first, or pass --token")
	}
	return nil
}
