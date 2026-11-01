package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
)

// proxyIdleTimeout ends a proxied response when the upstream sends nothing
// for this long, so a stalled stream cannot hold a key's slot forever.
const proxyIdleTimeout = 5 * time.Minute

// proxyMethods are the methods forwarded upstream. TRACE and CONNECT are
// refused: an echoing upstream would hand the injected key back to the app.
var proxyMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// errBadProxyPath means the wildcard path could change the query or escape
// the service's base path.
var errBadProxyPath = errors.New("path may not contain a literal ?, #, or . and .. segments")

// upstreamURL joins the service base URL with the proxied path and query.
// It works on parsed URLs, never by concatenation, so an encoded ? or # in
// the path cannot move or drop the injected query key.
func upstreamURL(base, rawPath string, q url.Values) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(rawPath, "?#") {
		return "", errBadProxyPath
	}
	// An encoded ? or # is fine: the URL is built from parts, so it stays in
	// the path. Dot segments are refused so a base path prefix holds.
	segs := strings.Split(strings.TrimPrefix(rawPath, "/"), "/")
	for _, sg := range segs {
		if dec, err := url.PathUnescape(sg); err != nil || dec == "." || dec == ".." {
			return "", errBadProxyPath
		}
	}
	dec, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", errBadProxyPath
	}
	basePath := strings.TrimRight(u.Path, "/")
	baseRaw := strings.TrimRight(u.EscapedPath(), "/")
	u.Path = basePath + "/" + strings.TrimPrefix(dec, "/")
	// RawPath keeps the client's escaping (%2F stays %2F). url.URL drops it
	// by itself when it does not match Path.
	u.RawPath = baseRaw + "/" + strings.TrimPrefix(rawPath, "/")
	// Keep query parameters baked into the base URL (api-version and the
	// like) unless the request sets them. The injected key is already in q.
	merged := url.Values{}
	for k, vs := range u.Query() {
		merged[k] = vs
	}
	for k, vs := range q {
		merged[k] = vs
	}
	u.RawQuery = merged.Encode()
	u.Fragment, u.RawFragment = "", ""
	return u.String(), nil
}

// authPrefix is the provider's credential prefix when the service still
// places the key the way the provider spec says.
func authPrefix(sv *store.Service) string {
	a := providers.Get(sv.Provider).Auth
	if a.Type != sv.AuthType {
		return ""
	}
	name := a.Name
	if a.Type == "bearer" && name == "" {
		name = "Authorization"
	}
	if name != sv.AuthName {
		return ""
	}
	return a.Prefix
}

func newUpstreamClient() *http.Client {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
	}
	return &http.Client{
		Transport: t,
		// Pass redirects through to the caller instead of following them with our key attached.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Host", "Cookie", "X-Cogito-Token",
	"X-Forwarded-For", "X-Real-Ip", "Content-Length",
	// Let the Go transport negotiate gzip so error bodies can be classified.
	"Accept-Encoding",
}

// buildUpstream creates the upstream request for one attempt.
func buildUpstream(ctx context.Context, r *http.Request, sv *store.Service, path string, body []byte, secret string) (*http.Request, error) {
	q := r.URL.Query()
	q.Del("cogito_token")
	prefix := authPrefix(sv)
	if sv.AuthType == "query" {
		q.Set(sv.AuthName, prefix+secret)
	}
	target, err := upstreamURL(sv.BaseURL, path, q)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		req.Header[k] = append([]string(nil), vs...)
	}
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	if strings.HasPrefix(req.Header.Get("Authorization"), "Bearer cog_") {
		req.Header.Del("Authorization")
	}
	switch sv.AuthType {
	case "header":
		req.Header.Set(sv.AuthName, prefix+secret)
	case "bearer":
		name := sv.AuthName
		if name == "" {
			name = "Authorization"
		}
		if prefix == "" {
			prefix = "Bearer "
		}
		req.Header.Set(name, prefix+secret)
	}
	req.ContentLength = int64(len(body))
	return req, nil
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	a := appFrom(r)
	if !a.AllowsProxy() {
		writeErr(w, 403, "proxy_not_allowed", "this app is lease only, use POST /api/v1/lease")
		return
	}
	if !proxyMethods[r.Method] {
		writeErr(w, 405, "method_not_allowed", "the proxy forwards GET, HEAD, POST, PUT, PATCH, DELETE and OPTIONS")
		return
	}
	sv, ok := s.serviceForApp(w, r, chi.URLParam(r, "service"))
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.Cfg.MaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, 413, "body_too_large", "request body is too large")
		} else {
			writeErr(w, 400, "bad_body", "could not read the request body")
		}
		return
	}
	// The escaped form keeps %2F and friends as the client sent them.
	path := r.URL.EscapedPath()
	if i := strings.Index(path, "/proxy/"+chi.URLParam(r, "service")); i >= 0 {
		path = strings.TrimPrefix(path[i+len("/proxy/"+chi.URLParam(r, "service")):], "/")
	} else {
		path = chi.URLParam(r, "*")
	}
	if _, err := upstreamURL(sv.BaseURL, path, nil); err != nil {
		writeErr(w, 400, "bad_path", err.Error())
		return
	}
	prov := providers.Get(sv.Provider)
	tried := map[int64]bool{}
	var last *http.Response
	var lastBody []byte
	var lastKey int64

	queue := time.Duration(sv.QueueTimeoutSec) * time.Second
	for attempt := 1; attempt <= sv.MaxAttempts; attempt++ {
		waitStart := time.Now()
		// Upstreams can echo the key (account endpoints, redirects that keep
		// the query), so proxying can disclose it just like a lease. Hidden
		// keys therefore need the same superadmin grant as leasing them.
		picked, err := s.Pool.PickWait(r.Context(), sv, a.ID, a.LeaseHidden, tried, queue)
		waited := time.Since(waitStart)
		if err != nil && r.Context().Err() != nil {
			return // client went away while queued
		}
		if err != nil {
			if last != nil {
				break // out of keys, relay the last upstream answer
			}
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, AppID: a.ID, StatusCode: 503, Detail: "proxy: " + err.Error()})
			if !writeNoKey(w, err) {
				writeStoreErr(w, err)
			}
			return
		}
		k := picked.Key
		tried[k.ID] = true
		ctx, cancel := context.WithCancel(r.Context())
		req, err := buildUpstream(ctx, r, sv, path, body, picked.Secret)
		if err != nil {
			cancel()
			picked.Release()
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		start := time.Now()
		resp, err := s.UpClient.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			cancel()
			picked.Release()
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, LatencyMS: latency, Detail: "upstream: " + scrub(err.Error(), picked.Secret)})
			if last != nil {
				break // relay the earlier answer instead of hiding it behind a 502
			}
			writeErr(w, 502, "upstream_unreachable", "could not reach "+sv.Name)
			return
		}
		detail := r.Method + " /" + path
		if waited >= 100*time.Millisecond {
			detail += " (queued " + waited.Round(10*time.Millisecond).String() + " for a free key)"
		}
		s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "proxy", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: resp.StatusCode, LatencyMS: latency,
			Detail: detail})

		if resp.StatusCode < 400 {
			_ = s.Pool.Apply(r.Context(), sv, k, providers.Result{Outcome: providers.OK})
			// The slot stays held until the (possibly streamed) body is fully
			// sent, or the upstream goes quiet for proxyIdleTimeout.
			func() {
				defer picked.Release()
				defer cancel()
				relayStream(w, resp, k.ID, attempt, cancel, picked.Secret)
			}()
			return
		}
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		cancel()
		picked.Release()
		res := prov.Classify(resp.StatusCode, resp.Header, rb)
		res.Message = scrub(res.Message, picked.Secret)
		_ = s.Pool.Apply(r.Context(), sv, k, res)
		if res.Outcome != providers.OK {
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: string(res.Outcome), ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: resp.StatusCode, Detail: res.Message})
		}
		scrubHeaders(resp.Header, picked.Secret)
		last, lastBody, lastKey = resp, rb, k.ID
		if !res.Outcome.Rotates() {
			break
		}
	}
	relay(w, last, lastKey, len(tried), lastBody)
}

// droppedResponseHeaders never pass from an upstream to the client. Besides
// hop-by-hop headers this covers anything that would let an upstream act on
// the dashboard origin: cookies, CORS grants and Cogito's own security headers.
var droppedResponseHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Transfer-Encoding": true, "Upgrade": true, "Content-Length": true,
	"Set-Cookie": true, "Set-Cookie2": true, "Proxy-Authenticate": true, "Trailer": true,
	"Content-Security-Policy": true, "Content-Security-Policy-Report-Only": true,
	"X-Content-Type-Options": true, "X-Frame-Options": true, "Referrer-Policy": true,
	"Access-Control-Allow-Origin": true, "Access-Control-Allow-Credentials": true,
	"Access-Control-Allow-Headers": true, "Access-Control-Allow-Methods": true,
	"Access-Control-Expose-Headers": true, "Access-Control-Max-Age": true,
	"Strict-Transport-Security": true, "Clear-Site-Data": true, "Refresh": true, "Link": true,
}

// proxyCSP sandboxes proxied responses: they are served from the dashboard
// origin, so an upstream returning HTML must not run script there.
const proxyCSP = "sandbox; default-src 'none'; frame-ancestors 'none'"

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response, keyID int64, attempts int) {
	for k, vs := range resp.Header {
		if droppedResponseHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		w.Header()[k] = vs
	}
	h := w.Header()
	h.Set("Content-Security-Policy", proxyCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Cogito-Key-Id", itoa(int(keyID)))
	h.Set("X-Cogito-Attempts", itoa(attempts))
}

// relay writes a buffered upstream response to the client.
func relay(w http.ResponseWriter, resp *http.Response, keyID int64, attempts int, body []byte) {
	copyResponseHeaders(w, resp, keyID, attempts)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// relayStream streams the upstream body to the client. If no bytes arrive for
// proxyIdleTimeout it calls cancel, which aborts the upstream read.
func relayStream(w http.ResponseWriter, resp *http.Response, keyID int64, attempts int, cancel context.CancelFunc, secret string) {
	defer resp.Body.Close()
	scrubHeaders(resp.Header, secret)
	copyResponseHeaders(w, resp, keyID, attempts)
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	idle := time.AfterFunc(proxyIdleTimeout, cancel)
	defer idle.Stop()
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			idle.Reset(proxyIdleTimeout)
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// scrubHeaders removes the secret from upstream response headers. A
// redirect that keeps the query string would otherwise hand a query-auth key
// to the app in Location.
func scrubHeaders(h http.Header, secret string) {
	if secret == "" {
		return
	}
	for k, vs := range h {
		for i, v := range vs {
			vs[i] = crypto.Scrub(v, secret)
		}
		h[k] = vs
	}
}

// scrub removes the secret from upstream messages before they are stored.
func scrub(msg, secret string) string { return crypto.Scrub(msg, secret) }
