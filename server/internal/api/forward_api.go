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
			add(h.URL, sv)
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
		"passthrough":          s.Cfg.ForwardPassthrough && (a == nil || a.ShareID == nil),
		"hosts":                hosts,
		"env": map[string]string{
			"HTTPS_PROXY":         withToken,
			"HTTP_PROXY":          withToken,
			"NODE_USE_ENV_PROXY":  "1",
			"NODE_EXTRA_CA_CERTS": "acnologia-ca.pem",
			"REQUESTS_CA_BUNDLE":  "acnologia-ca.pem",
			"SSL_CERT_FILE":       "acnologia-ca.pem",
		},
		"service_header": headerPrefix + "Service",
	}, nil
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
