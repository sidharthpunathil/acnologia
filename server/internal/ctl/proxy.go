package ctl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// The forward proxy from the command line: apps set Acnologia as their
// HTTPS_PROXY and call provider URLs directly.
//
//	acn proxy                      the proxy URL, CA fingerprint and hosts
//	acn proxy ca [--out file|-]    save the CA certificate apps must trust
//	acn proxy env [--token acn_...] [--ca file]
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
	return fmt.Errorf("usage: %s proxy [ca [--out file] | env [--token acn_...] [--ca file]]", c.name())
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

// proxyCA saves the CA certificate, to acnologia-ca.pem by default.
func (c *cli) proxyCA(args []string) error {
	out := "acnologia-ca.pem"
	for i := 0; i < len(args); i++ {
		if (args[i] == "--out" || args[i] == "-o") && i+1 < len(args) {
			out = args[i+1]
			i++
		}
	}
	ctx, err := c.current()
	if err != nil {
		return err
	}
	req, err := http.NewRequest("GET", ctx.URL+"/api/v1/proxy/ca.pem", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient(30 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	pem, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 || !strings.Contains(string(pem), "BEGIN CERTIFICATE") {
		return fmt.Errorf("could not download the CA: server answered %d", resp.StatusCode)
	}
	if out == "-" {
		_, err := c.out.Write(pem)
		return err
	}
	if err := os.WriteFile(out, pem, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Saved the forward proxy CA to %s. Trust it in your app: NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE or SSL_CERT_FILE.\n", out)
	return nil
}

// proxyEnv prints export lines. Without --token the proxy URL reads the
// token from $ACN_APP_TOKEN when the shell runs it.
func (c *cli) proxyEnv(args []string) error {
	token, ca := "", "acnologia-ca.pem"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--token" && i+1 < len(args):
			token = args[i+1]
			i++
		case args[i] == "--ca" && i+1 < len(args):
			ca = args[i+1]
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
	cred := "${ACN_APP_TOKEN}"
	if token != "" {
		cred = url.PathEscape(token)
	}
	proxy := u.Scheme + "://app:" + cred + "@" + u.Host
	q := func(v string) string { return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"` }
	fmt.Fprintf(c.out, "export HTTPS_PROXY=%s\n", q(proxy))
	fmt.Fprintf(c.out, "export HTTP_PROXY=%s\n", q(proxy))
	fmt.Fprintln(c.out, "export NODE_USE_ENV_PROXY=1")
	for _, k := range []string{"NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE"} {
		fmt.Fprintf(c.out, "export %s=%s\n", k, q(ca))
	}
	if token == "" {
		fmt.Fprintln(c.err, "# set ACN_APP_TOKEN to an app token first, or pass --token; save the CA with: "+c.name()+" proxy ca")
	}
	return nil
}
