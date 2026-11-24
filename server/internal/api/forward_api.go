package api

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Routes that tell apps and admins how to use the forward proxy.

// forwardHost is one host the forward proxy opens and puts keys on.
type forwardHost struct {
	Host    string `json:"host"`
	Service string `json:"service"`
	Name    string `json:"name"`
	// Path is the base path under the host, when the service has one.
	Path string `json:"path,omitempty"`
}

// forwardHosts lists the hosts of the services the app may use (every
// service for a nil app), one row per host and service.
func (s *Server) forwardHosts(ctx context.Context, a *store.App) ([]forwardHost, error) {
	svcs, err := s.forwardServices(ctx)
	if err != nil {
		return nil, err
	}
	out := []forwardHost{}
	seen := map[string]bool{}
	add := func(raw string, sv *store.Service) {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return
		}
		row := forwardHost{Host: strings.ToLower(u.Host), Service: sv.Slug, Name: sv.Name, Path: strings.TrimRight(u.Path, "/")}
		k := row.Host + row.Path + "|" + row.Service
		if !seen[k] {
			seen[k] = true
			out = append(out, row)
		}
	}
	for _, sv := range svcs {
		if a != nil && !a.CanUse(sv.Slug) {
			continue
		}
		add(sv.BaseURL, sv)
		for _, h := range providers.Get(sv.Provider).Hosts {
			add(h.Resolve(sv.BaseURL), sv)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// v1ProxyCA is GET /api/v1/proxy/ca.pem: the CA certificate apps trust.
func (s *Server) v1ProxyCA(w http.ResponseWriter, r *http.Request) {
	ca, err := s.ca()
	if err != nil {
		writeErr(w, 500, "proxy_ca", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="acnologia-ca.pem"`)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(ca.PEM())
}

// forwardSetup is what an app needs to use the forward proxy.
func (s *Server) forwardSetup(r *http.Request, a *store.App) (map[string]any, error) {
	ca, err := s.ca()
	if err != nil {
		return nil, err
	}
	hosts, err := s.forwardHosts(r.Context(), a)
	if err != nil {
		return nil, err
	}
	proxyURL := s.forwardProxyURL(r.Host)
	withToken := proxyURL
	if u, err := url.Parse(proxyURL); err == nil {
		u.User = url.UserPassword("app", "ACN_TOKEN")
		withToken = strings.Replace(u.String(), "ACN_TOKEN", "<app token>", 1)
	}
	caURL := strings.TrimRight(s.baseURL(r), "/") + "/api/v1/proxy/ca.pem"
	return map[string]any{
		"proxy_url":            proxyURL,
		"proxy_url_with_token": withToken,
		"ca_url":               caURL,
		"ca_fingerprint":       ca.Fingerprint(),
		"passthrough":          s.Cfg.ForwardPassthrough && mayPassthrough(a),
		"hosts":                hosts,
		// File names in env are where the app keeps the files: caFile is
		// the CA from ca_url, caBundleFile the system roots plus that CA.
		"env": map[string]string{
			"HTTPS_PROXY":         withToken,
			"HTTP_PROXY":          withToken,
			"NO_PROXY":            forwardNoProxy,
			"no_proxy":            forwardNoProxy,
			"NODE_USE_ENV_PROXY":  "1",
			"NODE_EXTRA_CA_CERTS": caFile,
			"REQUESTS_CA_BUNDLE":  caBundleFile,
			"SSL_CERT_FILE":       caBundleFile,
		},
		"env_notes": map[string]string{
			"NO_PROXY":            "keeps local calls off the proxy, which refuses hosts that are no service",
			"NODE_EXTRA_CA_CERTS": "adds to Node's built in roots, so the CA alone is right",
			"REQUESTS_CA_BUNDLE":  "replaces the whole trust store: point it at a bundle of the system roots plus the CA, never the CA alone",
			"SSL_CERT_FILE":       "replaces the whole trust store: point it at a bundle of the system roots plus the CA, never the CA alone",
		},
		"ca_bundle": map[string]string{
			"file":  caBundleFile,
			"build": "cat /etc/ssl/certs/ca-certificates.crt " + caFile + " > " + caBundleFile,
			"note":  "the system roots first, then the Acnologia CA. The roots file is /etc/ssl/certs/ca-certificates.crt on Debian and Ubuntu, /etc/pki/tls/certs/ca-bundle.crt on RHEL and Fedora, /etc/ssl/cert.pem on Alpine and macOS, or $(python -m certifi) for Python. acn proxy env builds it for you.",
		},
		"service_header": headerPrefix + "Service",
	}, nil
}

const (
	// caFile is the file name the setup suggests for the CA alone.
	caFile = "acnologia-ca.pem"
	// caBundleFile holds the system roots plus the CA, for the variables
	// that replace the trust store (SSL_CERT_FILE, REQUESTS_CA_BUNDLE).
	caBundleFile = "acnologia-ca-bundle.pem"
	// forwardNoProxy is the NO_PROXY the setup suggests.
	forwardNoProxy = "localhost,127.0.0.1,::1"
)

// mayPassthrough reports whether an app's token may tunnel HTTPS to hosts
// that are no service (when the server allows passthrough at all). A
// share's token and a read-only token reach known services only. A nil app
// is the admin view: what the server allows.
func mayPassthrough(a *store.App) bool {
	return a == nil || (a.ShareID == nil && !a.TokenReadOnly)
}

// v1ProxySetup is GET /api/v1/proxy for an app token.
func (s *Server) v1ProxySetup(w http.ResponseWriter, r *http.Request) {
	out, err := s.forwardSetup(r, appFrom(r))
	if err != nil {
		writeErr(w, 500, "proxy_ca", err.Error())
		return
	}
	writeJSON(w, 200, out)
}

// adminForwardProxy is GET /api/admin/forward-proxy: the setup for every
// service, plus how the server listens.
func (s *Server) adminForwardProxy(w http.ResponseWriter, r *http.Request) {
	out, err := s.forwardSetup(r, nil)
	if err != nil {
		writeErr(w, 500, "proxy_ca", err.Error())
		return
	}
	out["listen_addr"] = s.Cfg.ForwardProxyAddr
	out["main_port"] = true
	out["configured_url"] = s.Cfg.ForwardProxyURL != ""
	writeJSON(w, 200, out)
}

// rotateForwardCA is POST /api/admin/forward-proxy/ca/rotate: a new CA.
// Every app must download and trust the new certificate.
func (s *Server) rotateForwardCA(w http.ResponseWriter, r *http.Request) {
	ca, err := s.rotateCA()
	if err != nil {
		writeErr(w, 500, "proxy_ca", err.Error())
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "forward_proxy.ca_rotate", "", ca.Fingerprint())
	writeJSON(w, 200, map[string]any{"ca_fingerprint": ca.Fingerprint()})
}

func (s *Server) bgCtx() context.Context { return context.Background() }
