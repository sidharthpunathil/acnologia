package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// The forward proxy: an app sets Acnologia as its HTTP(S)_PROXY and calls
// provider URLs as they are,
//
//	HTTPS_PROXY=http://app:acn_...@acnologia:8888 NODE_EXTRA_CA_CERTS=acnologia-ca.pem node --use-env-proxy app.js
//
// and every request to a service's host gets a pool key, with the same
// rotation, limits, caching and logs as /proxy/{service}. Acnologia finds
// the service by the request's host and path (the service whose base URL,
// or provider host, matches it with the longest path), then hands the
// request to the /proxy route. HTTPS to a service host is opened with a
// certificate from the server's own CA (forward_ca.go), which the app
// trusts. HTTPS to any other host is a plain tunnel to a public address
// when ACNOLOGIA_FORWARD_PASSTHROUGH allows it, so an app can send all its
// traffic through the proxy; plain HTTP to another host is refused, so
// this is never an open proxy and a key only ever goes to its service's
// own base URL.
//
// The app token comes in Proxy-Authorization (the password, or the user
// name, of the proxy URL). A request may name the service or group itself
// with X-Acnologia-Service, which is how two services on one host (two
// accounts of one provider) or a group across providers are picked.

// forwardTunnelPorts are the ports a passthrough tunnel may reach.
var forwardTunnelPorts = map[string]bool{"443": true, "8443": true}

func isForwardRequest(r *http.Request) bool {
	return r.Method == http.MethodConnect || strings.HasPrefix(r.RequestURI, "http://") || strings.HasPrefix(r.RequestURI, "https://")
}

// PublicHandler is the router behind the forward proxy entry: what the
// listening server serves. In-process calls (the Assistant, the control
// MCP) use the router alone, since their requests carry absolute URLs.
func (s *Server) PublicHandler() http.Handler { return s.forwardEntry(s.handler()) }

// forwardEntry answers proxy requests (CONNECT, or an absolute URL in the
// request line) before the router, the access gate included: they carry
// an app token of their own, like /proxy/*.
func (s *Server) forwardEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isForwardRequest(r) {
			s.serveForward(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// proxyAuthToken is the app token in Proxy-Authorization: Basic with the
// token as password (or as user name), or Bearer.
func proxyAuthToken(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Proxy-Authorization"))
	if t, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	b, ok := strings.CutPrefix(v, "Basic ")
	if !ok {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b))
	if err != nil {
		return ""
	}
	user, pass, _ := strings.Cut(string(raw), ":")
	pass, _ = url.PathUnescape(pass)
	user, _ = url.PathUnescape(user)
	if store.IsAppToken(pass) {
		return pass
	}
	if store.IsAppToken(user) {
		return user
	}
	return ""
}

func writeProxyAuth(w http.ResponseWriter, msg string) {
	w.Header().Set("Proxy-Authenticate", `Basic realm="Acnologia", charset="UTF-8"`)
	writeErr(w, http.StatusProxyAuthRequired, "proxy_auth_required", msg)
}

// forwardApp checks the proxy credentials.
func (s *Server) forwardApp(w http.ResponseWriter, r *http.Request) (*store.App, string, bool) {
	tok := proxyAuthToken(r)
	if tok == "" {
		writeProxyAuth(w, "put the app token in the proxy URL: http://app:acn_...@host:port")
		return nil, "", false
	}
	probe := r.Clone(r.Context())
	probe.Header = http.Header{}
	probe.Header.Set(headerPrefix+"Token", tok)
	probe.URL = &url.URL{Path: "/"}
	a, f := s.authAppFull(probe)
	if f != nil {
		if f.Status == http.StatusUnauthorized {
			writeProxyAuth(w, f.Message)
		} else {
			f.write(w)
		}
		return nil, "", false
	}
	if !a.AllowsProxy() {
		writeErr(w, 403, "proxy_not_allowed", "this app is lease only, so it cannot use the forward proxy")
		return nil, "", false
	}
	return a, tok, true
}

func (s *Server) serveForward(w http.ResponseWriter, r *http.Request) {
	a, tok, ok := s.forwardApp(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodConnect {
		s.forwardConnect(w, r, a, tok)
		return
	}
	s.forwardDispatch(w, r, a, tok, r.URL.Scheme, r.URL.Host)
}

// ---- routing ----

type fwdTarget struct {
	sv   *store.Service
	rest string // path for /proxy/{service}, "/~host/..." for a provider host
	plen int    // length of the matched base path
}

var errNotService = errors.New("not a service host")

// forwardServices lists the services the forward proxy routes to, cached
// for a few seconds since every request needs them.
func (s *Server) forwardServices(ctx context.Context) ([]*store.Service, error) {
	s.fwdMu.Lock()
	defer s.fwdMu.Unlock()
	if s.fwdSvcs != nil && time.Since(s.fwdAt) < 5*time.Second {
		return s.fwdSvcs, nil
	}
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	out := svcs[:0:0]
	for _, sv := range svcs {
		if sv.PeerLinkID == nil && sv.Provider != providers.PeerSlug {
			out = append(out, sv)
		}
	}
	s.fwdSvcs, s.fwdAt = out, time.Now()
	return out, nil
}

// splitHostPort is a request authority as a lowercase host and a port, the
// scheme's default when it has none.
func splitHostPort(authority, scheme string) (string, string) {
	h, p, err := net.SplitHostPort(authority)
	if err != nil {
		h, p = authority, ""
	}
	if p == "" {
		p = "443"
		if scheme == "http" {
			p = "80"
		}
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), ".")), p
}

func baseMatches(base, host, port string) (*url.URL, bool) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, false
	}
	h, p := splitHostPort(u.Host, u.Scheme)
	return u, h == host && p == port
}

// pathUnder reports whether path sits under base path bp, on a segment
// boundary, and returns what follows it.
func pathUnder(path, bp string) (string, bool) {
	bp = strings.TrimRight(bp, "/")
	if bp == "" {
		return path, true
	}
	if path == bp {
		return "", true
	}
	if strings.HasPrefix(path, bp+"/") {
		return path[len(bp):], true
	}
	return "", false
}

// forwardTargets are the services a request to host:port and path may go
// to, the longest base path first.
func (s *Server) forwardTargets(ctx context.Context, host, port, path string) ([]fwdTarget, error) {
	svcs, err := s.forwardServices(ctx)
	if err != nil {
		return nil, err
	}
	var out []fwdTarget
	for _, sv := range svcs {
		if u, ok := baseMatches(sv.BaseURL, host, port); ok {
			if rest, ok := pathUnder(path, u.EscapedPath()); ok {
				out = append(out, fwdTarget{sv: sv, rest: rest, plen: len(strings.TrimRight(u.EscapedPath(), "/"))})
				continue
			}
		}
		for _, h := range providers.Get(sv.Provider).Hosts {
			if u, ok := baseMatches(h.URL, host, port); ok {
				if rest, ok := pathUnder(path, u.EscapedPath()); ok {
					out = append(out, fwdTarget{sv: sv, rest: providers.HostPath(h.Name) + rest, plen: len(strings.TrimRight(u.EscapedPath(), "/"))})
					break
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].plen > out[j].plen })
	return out, nil
}

// forwardHostKnown reports whether any service sends requests to host:port.
func (s *Server) forwardHostKnown(ctx context.Context, host, port string) (bool, error) {
	svcs, err := s.forwardServices(ctx)
	if err != nil {
		return false, err
	}
	for _, sv := range svcs {
		if _, ok := baseMatches(sv.BaseURL, host, port); ok {
			return true, nil
		}
		for _, h := range providers.Get(sv.Provider).Hosts {
			if _, ok := baseMatches(h.URL, host, port); ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// forwardRoute picks the /proxy name and path for a request. Among services
// with the same longest base path it takes the first one the app may use;
// explicit (X-Acnologia-Service) overrides the name, for a group or one
// account of several.
func (s *Server) forwardRoute(ctx context.Context, a *store.App, host, port, path, explicit string) (string, string, error) {
	ts, err := s.forwardTargets(ctx, host, port, path)
	if err != nil {
		return "", "", err
	}
	if len(ts) == 0 {
		return "", "", errNotService
	}
	pick := ts[0]
	for _, t := range ts {
		if t.plen != ts[0].plen {
			break
		}
		if a.CanUse(t.sv.Slug) {
			pick = t
			break
		}
	}
	if explicit != "" {
		for _, t := range ts {
			if t.sv.Slug == explicit {
				return t.sv.Slug, t.rest, nil
			}
		}
		return explicit, pick.rest, nil
	}
	return pick.sv.Slug, pick.rest, nil
}

// forwardDispatch hands one proxied request to /proxy/{service}.
func (s *Server) forwardDispatch(w http.ResponseWriter, r *http.Request, a *store.App, tok, scheme, authority string) {
	host, port := splitHostPort(authority, scheme)
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	slug, rest, err := s.forwardRoute(r.Context(), a, host, port, path, reqHeader(r, "Service"))
	if errors.Is(err, errNotService) && scheme == "http" && port == "80" {
		// An http:// URL for a service on https: the upstream call still
		// uses the service's own base URL, so it goes out over TLS.
		slug, rest, err = s.forwardRoute(r.Context(), a, host, "443", path, reqHeader(r, "Service"))
	}
	if errors.Is(err, errNotService) {
		writeErr(w, 403, "not_a_service", host+" is not the host of any service here, so the forward proxy will not send it (it only tunnels HTTPS to other hosts); call it directly or list it in NO_PROXY")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	target := "/proxy/" + url.PathEscape(slug) + "/" + strings.TrimPrefix(rest, "/")
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	u, err := url.Parse(target)
	if err != nil {
		writeErr(w, 400, "bad_path", "could not read the request path")
		return
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, nil)
	in := r.Clone(ctx)
	in.URL, in.RequestURI, in.Host = u, u.RequestURI(), ""
	in.Header.Del("Proxy-Authorization")
	in.Header.Del("Proxy-Connection")
	in.Header.Del(headerPrefix + "Service")
	in.Header.Del(legacyHeaderPrefix + "Service")
	in.Header.Set(headerPrefix+"Token", tok)
	s.handler().ServeHTTP(w, in)
}

// ---- CONNECT ----

func (s *Server) forwardConnect(w http.ResponseWriter, r *http.Request, a *store.App, tok string) {
	authority := r.Host
	if authority == "" {
		authority = r.URL.Host
	}
	host, port := splitHostPort(authority, "https")
	known, err := s.forwardHostKnown(r.Context(), host, port)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if !known && (!s.Cfg.ForwardPassthrough || a.ShareID != nil) {
		writeErr(w, 403, "not_a_service", host+" is not the host of any service here; call it directly or list it in NO_PROXY")
		return
	}
	if !known && !forwardTunnelPorts[port] {
		writeErr(w, 403, "port_not_allowed", "the forward proxy tunnels to other hosts on ports 443 and 8443 only")
		return
	}
	var ca *forwardCA
	if known {
		if ca, err = s.ca(); err != nil {
			writeErr(w, 500, "proxy_ca", "the proxy certificate authority is not available: "+err.Error())
			return
		}
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeErr(w, 501, "connect_unsupported", "CONNECT needs HTTP/1.1 to the proxy")
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	client := &bufConn{Conn: conn, r: brw.Reader}
	if !known {
		go s.tunnel(client, net.JoinHostPort(host, port))
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		return
	}
	tc := tls.Server(client, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return ca.leaf(host)
		},
	})
	s.fwdInner().push(&fwdConn{Conn: tc, app: a, token: tok, authority: net.JoinHostPort(host, port)})
}

// tunnel joins the client to a public address, for HTTPS to a host that is
// no service.
func (s *Server) tunnel(client net.Conn, addr string) {
	defer client.Close()
	d := &net.Dialer{Timeout: 10 * time.Second, Control: catalog.PublicOnly}
	up, err := d.Dial("tcp", addr)
	if err != nil {
		_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	_ = client.SetDeadline(time.Time{})
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, client); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); closeWrite(client); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// bufConn reads first what the HTTP server already buffered.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *bufConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// fwdConn is one opened CONNECT tunnel to a service host, with whose token
// and for which host its requests run.
type fwdConn struct {
	net.Conn
	app       *store.App
	token     string
	authority string
}

type fwdConnKey struct{}

// fwdInner is the HTTP server for requests inside opened tunnels.
func (s *Server) fwdInner() *chanListener {
	s.fwdInnerOnce.Do(func() {
		s.fwdInnerLn = newChanListener(nil)
		hs := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fc, _ := r.Context().Value(fwdConnKey{}).(*fwdConn)
				if fc == nil {
					writeErr(w, 500, "internal", "lost the tunnel")
					return
				}
				s.forwardDispatch(w, r, fc.app, fc.token, "https", fc.authority)
			}),
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       2 * time.Minute,
			ConnContext: func(ctx context.Context, c net.Conn) context.Context {
				if fc, ok := c.(*fwdConn); ok {
					return context.WithValue(ctx, fwdConnKey{}, fc)
				}
				return ctx
			},
		}
		go func() { _ = hs.Serve(s.fwdInnerLn) }()
	})
	return s.fwdInnerLn
}

// chanListener hands connections made elsewhere to an http.Server.
type chanListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
	addr net.Addr
}

func newChanListener(addr net.Addr) *chanListener {
	if addr == nil {
		addr = &net.TCPAddr{}
	}
	return &chanListener{ch: make(chan net.Conn), done: make(chan struct{}), addr: addr}
}

func (l *chanListener) push(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// ---- the dedicated listener ----

// ServeForwardProxy serves ACNOLOGIA_FORWARD_PROXY_ADDR until ctx ends. One
// port speaks both plain HTTP (http:// proxy URLs) and TLS (https:// proxy
// URLs, with a certificate from the proxy CA); ordinary requests, such as
// GET /api/v1/proxy/ca.pem, are served as on the main port.
func (s *Server) ServeForwardProxy(ctx context.Context, ln net.Listener) error {
	cl := newChanListener(ln.Addr())
	hs := &http.Server{Handler: s.PublicHandler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() { _ = hs.Serve(cl) }()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
		cl.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go s.sniffForward(c, cl)
	}
}

// sniffForward tells TLS from plain HTTP by the first byte.
func (s *Server) sniffForward(c net.Conn, cl *chanListener) {
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(c)
	b, err := br.Peek(1)
	if err != nil {
		c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	bc := &bufConn{Conn: c, r: br}
	if b[0] != 0x16 {
		cl.push(bc)
		return
	}
	ca, err := s.ca()
	if err != nil {
		c.Close()
		return
	}
	fallback := hostOf(s.forwardProxyURL(""))
	cl.push(tls.Server(bc, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := h.ServerName
			if name == "" {
				// A proxy URL with an IP address sends no name.
				if ta, ok := c.LocalAddr().(*net.TCPAddr); ok && ta.IP != nil && !ta.IP.IsUnspecified() {
					name = ta.IP.String()
				} else {
					name = fallback
				}
			}
			return ca.leaf(name)
		},
	}))
}

// authorityOf is a URL's host and port.
func authorityOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// forwardProxyURL is the proxy address apps should use, without a token:
// ACNOLOGIA_FORWARD_PROXY_URL, else the public URL's host on the dedicated
// port, else the public URL (or the request's host) itself.
func (s *Server) forwardProxyURL(reqHost string) string {
	if s.Cfg.ForwardProxyURL != "" {
		return s.Cfg.ForwardProxyURL
	}
	host := hostOf(s.Cfg.PublicURL)
	if host == "" {
		host, _, _ = strings.Cut(reqHost, ":")
	}
	if host == "" {
		host = "localhost"
	}
	if s.Cfg.ForwardProxyAddr != "" {
		_, port, err := net.SplitHostPort(s.Cfg.ForwardProxyAddr)
		if err == nil && port != "" {
			return "http://" + net.JoinHostPort(host, port)
		}
	}
	if s.Cfg.PublicURL != "" {
		return s.Cfg.PublicURL
	}
	if reqHost != "" {
		return "http://" + reqHost
	}
	return "http://localhost:8080"
}
