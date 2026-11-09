package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/breaker"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/respcache"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
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

// serviceURL is the upstream URL of a proxied path on a service. A first
// segment of "~name" picks one of the provider's hosts (providers.Host);
// anything else goes to the service's base URL.
func serviceURL(sv *store.Service, path string, q url.Values) (string, error) {
	h, rest, ok, err := providers.Get(sv.Provider).ResolveHost(path)
	if err != nil {
		return "", err
	}
	if ok {
		return upstreamURL(h.URL, rest, q)
	}
	return upstreamURL(sv.BaseURL, path, q)
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

// newUpstreamClient connects to any address the base URL resolves to but
// link local and cloud metadata ones, checked on every connection. Loopback
// and private addresses stay allowed for model servers on the same network.
// An operator's HTTP(S)_PROXY still applies; the dialer then sees only the
// proxy, so a literal metadata address in the URL is refused before it.
func newUpstreamClient() *http.Client {
	return upstreamClientWith(catalog.NoMetadata, catalog.GuardProxy(http.ProxyFromEnvironment))
}

// newPublicUpstreamClient connects only to public addresses, checked on the
// resolved address of each connection. It never uses an environment proxy,
// which would hide the real target from the check.
func newPublicUpstreamClient() *http.Client { return upstreamClientWith(catalog.PublicOnly, nil) }

func upstreamClientWith(control func(network, address string, c syscall.RawConn) error, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	t := egress.Tune(&http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: control}).DialContext,
		ForceAttemptHTTP2:     true,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
	})
	return &http.Client{
		Transport: t,
		// Pass redirects through to the caller instead of following them with our key attached.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Host", "Cookie", "X-Acnologia-Token", "X-Cogito-Token", "X-Acnologia-Access",
	"X-Forwarded-For", "X-Real-Ip", "Content-Length",
	// The hop count goes only to another Acnologia, set in proxyService.
	hopHeader,
	// Let the Go transport negotiate gzip so error bodies can be classified.
	"Accept-Encoding",
}

// buildUpstream creates the upstream request for one attempt.
func buildUpstream(ctx context.Context, r *http.Request, sv *store.Service, path string, body []byte, secret string) (*http.Request, error) {
	q := r.URL.Query()
	delTokenParams(q)
	prefix := authPrefix(sv)
	if sv.AuthType == "query" {
		q.Set(sv.AuthName, prefix+secret)
	}
	target, err := serviceURL(sv, path, q)
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
	if isBearerAppToken(req.Header.Get("Authorization")) {
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
	slug := chi.URLParam(r, "service")
	hops := requestHops(r)
	if hops > maxPeerHops {
		writeErr(w, 508, "loop_detected", "this request already passed through too many Acnologia servers")
		return
	}
	targets, ok := s.proxyTargets(w, r, slug)
	if !ok {
		return
	}
	for _, sv := range targets {
		if sv.Provider == providers.PeerSlug && hops+1 > maxPeerHops {
			writeErr(w, 508, "loop_detected", "this request already passed through too many Acnologia servers")
			return
		}
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
	if i := strings.Index(path, "/proxy/"+slug); i >= 0 {
		path = strings.TrimPrefix(path[i+len("/proxy/"+slug):], "/")
	} else {
		path = chi.URLParam(r, "*")
	}
	for _, sv := range targets {
		if _, err := serviceURL(sv, path, nil); err != nil {
			writeErr(w, 400, "bad_path", err.Error())
			return
		}
	}
	est := 0.0
	for _, sv := range targets {
		est = max(est, costPerRequest(sv, providers.Get(sv.Provider), r.Method, "/"+path))
	}
	release, limited := s.appLimited(w, r, a, est)
	if limited {
		return
	}
	// Runs after the usage below is recorded.
	defer release()
	idemKey := idempotencyKey(r, a.ID, slug, path)
	if idemKey != "" {
		if e, ok := s.Cache.Get(idemKey); ok {
			writeCached(w, e, "replay", hidesKey(a))
			return
		}
	}

	var last *serviceResult
	var failErr error
	for i, sv := range targets {
		prov := providers.Get(sv.Provider)
		cacheKey, ttl := "", cacheTTL(sv, prov)
		if ttl > 0 && r.Method == http.MethodGet && !noCache(r) {
			cacheKey = responseCacheKey(sv, a, r, path, body)
			if e, ok := s.Cache.Get(cacheKey); ok {
				setHeader(w.Header(), "Service", sv.Slug)
				writeCached(w, e, "hit", hidesKey(a))
				s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "cache_hit", ServiceID: sv.ID, AppID: a.ID, StatusCode: e.Status, Detail: r.Method + " /" + path})
				_ = s.Store.RecordAppUsage(r.Context(), a.ID, 0, true)
				return
			}
		}
		bcfg := breakerSettings(prov)
		if ok, ra := s.Breakers.Allow(sv.ID, bcfg); !ok {
			failErr = &serviceDownError{Service: sv.Name, RetryAfter: ra}
			continue
		}
		res := s.proxyService(w, r, a, sv, prov, path, body, bcfg, capture{cacheKey: cacheKey, ttl: ttl, idemKey: idemKey})
		if res.served {
			_ = s.Store.RecordAppUsage(r.Context(), a.ID, res.cost, false)
			return
		}
		if res.resp == nil {
			failErr = res.err
			continue
		}
		last = res
		// In a group, a failure that another account or region could avoid
		// moves on to the next member. A request the provider rejected as
		// malformed would fail everywhere, so it is relayed as is.
		if i < len(targets)-1 && (res.outcome.Rotates() || res.outcome.ProviderFault()) {
			continue
		}
		break
	}
	if last != nil {
		_ = s.Store.RecordAppUsage(r.Context(), a.ID, 0, false)
		setHeader(w.Header(), "Service", last.service)
		if idemKey != "" && last.resp.StatusCode < 500 {
			s.Cache.Set(idemKey, &respcache.Entry{Status: last.resp.StatusCode, Header: last.resp.Header.Clone(), Body: last.body, KeyID: last.keyID}, idempotencyTTL)
		}
		relay(w, last.resp, view(a, last.keyID, last.attempts), last.body)
		return
	}
	var down *serviceDownError
	switch {
	case errors.As(failErr, &down):
		retry := max(int(math.Ceil(down.RetryAfter.Seconds())), 1)
		w.Header().Set("Retry-After", itoa(retry))
		writeJSON(w, 503, map[string]any{"error": apiError{Code: "service_unavailable",
			Message: down.Service + " keeps failing, so Acnologia paused traffic to it"}, "retry_after_sec": retry})
	case errors.Is(failErr, errUpstreamBlocked):
		writeErr(w, 502, "upstream_blocked", "could not reach "+targets[len(targets)-1].Name+": "+catalog.ErrMetadataAddr)
	case errors.Is(failErr, errUpstreamUnreachable):
		writeErr(w, 502, "upstream_unreachable", "could not reach "+targets[len(targets)-1].Name)
	case failErr != nil:
		if !writeNoKey(w, failErr) {
			writeStoreErr(w, failErr)
		}
	default:
		writeErr(w, 503, "no_key_available", "no service could take this request")
	}
}

// serviceResult is how one service handled a proxied request.
type serviceResult struct {
	served   bool // the response was already written to the client
	cost     float64
	service  string
	resp     *http.Response // final upstream answer to relay, body already read
	body     []byte
	keyID    int64
	attempts int
	outcome  providers.Outcome
	err      error // why no upstream answer exists: no key, or unreachable
}

// capture says which caches a successful response should be stored in.
type capture struct {
	cacheKey string
	ttl      time.Duration
	idemKey  string
}

func (c capture) any() bool { return c.cacheKey != "" || c.idemKey != "" }

var errUpstreamUnreachable = errors.New("upstream unreachable")

// errUpstreamBlocked is a connection the dialer refused: the base URL
// resolves to a link local or cloud metadata address.
var errUpstreamBlocked = fmt.Errorf("%w: %s", errUpstreamUnreachable, catalog.ErrMetadataAddr)

// proxyService tries the request on one service, rotating keys on failures
// that are a key's fault.
func (s *Server) proxyService(w http.ResponseWriter, r *http.Request, a *store.App, sv *store.Service, prov *providers.Spec,
	path string, body []byte, bcfg breaker.Settings, cap capture) *serviceResult {
	out := &serviceResult{service: sv.Slug}
	verdict := false // whether the breaker heard about this service
	defer func() {
		if !verdict {
			s.Breakers.Abort(sv.ID)
		}
	}()
	cost := costPerRequest(sv, prov, r.Method, "/"+path)
	pr := pool.Request{Method: r.Method, Path: "/" + path}
	tried := map[int64]bool{}
	queue := time.Duration(sv.QueueTimeoutSec) * time.Second
	for attempt := 1; attempt <= sv.MaxAttempts; attempt++ {
		waitStart := time.Now()
		// Upstreams can echo the key (account endpoints, redirects that keep
		// the query), so proxying can disclose it just like a lease. Hidden
		// keys therefore need the same superadmin grant as leasing them.
		// A service shared by another server holds its share token as a
		// hidden key, which any app may proxy through (never lease), and so
		// does a service holding keys another server handed over.
		allowHidden := a.LeaseHidden || sv.Provider == providers.PeerSlug || isHandoverService(sv)
		picked, err := s.Pool.PickFor(r.Context(), sv, a.ID, allowHidden, tried, queue, false, pr)
		waited := time.Since(waitStart)
		if err != nil && r.Context().Err() != nil {
			out.served = true // client went away while queued; nothing to write
			return out
		}
		if err != nil {
			if out.resp != nil {
				return out // out of keys, relay the last upstream answer
			}
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, AppID: a.ID, StatusCode: 503, Detail: "proxy: " + err.Error()})
			var nk *pool.NoKeyError
			if errors.As(err, &nk) && !nk.Busy {
				s.Pool.EmitPoolEmpty(sv, err)
			}
			out.err = err
			return out
		}
		k := picked.Key
		tried[k.ID] = true
		client, err := s.upstreamClient(r.Context(), sv, k)
		if err != nil {
			picked.Release()
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, Detail: "egress: " + err.Error()})
			out.err = errUpstreamUnreachable
			return out
		}
		ctx, cancel := context.WithCancel(r.Context())
		req, err := buildUpstream(ctx, r, sv, path, body, picked.Secret)
		if err != nil {
			cancel()
			picked.Release()
			writeErr(w, 400, "bad_request", err.Error())
			out.served = true
			return out
		}
		if sv.Provider == providers.PeerSlug {
			req.Header.Set(hopHeader, itoa(requestHops(r)+1))
		}
		start := time.Now()
		resp, err := client.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			cancel()
			picked.Release()
			msg := scrub(err.Error(), picked.Secret)
			s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "error", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, LatencyMS: latency, Detail: "upstream: " + msg})
			blocked := strings.Contains(msg, catalog.ErrMetadataAddr)
			if r.Context().Err() == nil && !blocked {
				s.Breakers.Failure(sv.ID, bcfg, "could not connect: "+msg)
				verdict = true
			}
			if out.resp == nil {
				out.err = errUpstreamUnreachable
				if blocked {
					out.err = errUpstreamBlocked
				}
			}
			return out // relay an earlier answer instead of hiding it behind a 502
		}
		// Headers lose the key and any Acnologia header a non-Acnologia
		// upstream made up, before anything reads or stores them.
		sanitizeUpstreamHeaders(resp.Header, picked.Secret, sv.Provider == providers.PeerSlug)
		detail := r.Method + " /" + path
		if waited >= 100*time.Millisecond {
			detail += " (queued " + waited.Round(10*time.Millisecond).String() + " for a free key)"
		}
		ev := store.NewEvent{Kind: "proxy", ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: resp.StatusCode, LatencyMS: latency, Detail: detail}
		if resp.StatusCode < 400 {
			ev.Cost = cost
		}
		s.Store.AddEvent(r.Context(), ev)

		if resp.StatusCode < 400 {
			s.Breakers.Success(sv.ID)
			verdict = true
			_ = s.Pool.ApplyProxied(r.Context(), sv, k, a.ID, providers.Result{Outcome: providers.OK, Quota: prov.ReadQuota(resp.Header)})
			setHeader(w.Header(), "Service", sv.Slug)
			// The slot stays held until the (possibly streamed) body is fully
			// sent, or the upstream goes quiet for proxyIdleTimeout.
			var meter *tokenMeter
			if meterable(sv, resp) {
				meter = &tokenMeter{rc: resp.Body}
				resp.Body = meter
			}
			func() {
				defer picked.Release()
				defer cancel()
				s.deliver(w, resp, k, view(a, k.ID, attempt), cancel, picked.Secret, !mayReadKey(a, sv), cap)
			}()
			if meter != nil {
				in, outTok := meter.counts()
				_ = s.Store.RecordTokens(context.WithoutCancel(r.Context()), a.ID, sv.ID, in, outTok)
			}
			out.served, out.cost = true, cost
			return out
		}
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		cancel()
		picked.Release()
		res := prov.Classify(resp.StatusCode, resp.Header, rb)
		if !mayReadKey(a, sv) {
			rb = scrubBytes(rb, keyForms(picked.Secret))
		}
		if sv.Provider == providers.PeerSlug && sv.PeerLinkID != nil && resp.StatusCode == 403 {
			// The owner says the share is revoked, paused or expired: mark
			// the link and disable its services now, not at the next sync.
			// The owner's own refusal carries the matching error code too,
			// which an upstream answer relayed through the owner cannot.
			if st := resp.Header.Get("X-Acnologia-Share"); (st == "revoked" || st == "paused" || st == "expired") && errorCodeOf(rb) == "share_"+st {
				s.peers.MarkRemoteState(context.WithoutCancel(r.Context()), *sv.PeerLinkID, st)
			}
		}
		res.Message = scrub(res.Message, picked.Secret)
		if res.Outcome.ProviderFault() {
			s.Breakers.Failure(sv.ID, bcfg, fmt.Sprintf("%d: %s", resp.StatusCode, res.Message))
		} else {
			s.Breakers.Success(sv.ID)
		}
		verdict = true
		// The upstream has answered, so its verdict must land on the key even
		// if the client hangs up now: a lost 429 leaves the key in rotation.
		wctx := context.WithoutCancel(r.Context())
		_ = s.Pool.ApplyProxied(wctx, sv, k, a.ID, res)
		if res.Outcome != providers.OK {
			s.Store.AddEvent(wctx, store.NewEvent{Kind: string(res.Outcome), ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: resp.StatusCode, Detail: res.Message})
		}
		// rb was scrubbed above, right after it was classified.
		out.resp, out.body, out.keyID, out.attempts, out.outcome, out.err = resp, rb, k.ID, len(tried), res.Outcome, nil
		if !res.Outcome.Rotates() {
			return out
		}
	}
	return out
}

// deliver sends a successful upstream response to the client. When the
// response may be cached it is buffered (up to maxCachedBody) and stored;
// larger bodies stream through untouched.
//
// When scrubBody is set (the caller may not read the key), every form of the
// secret in the body is replaced, streamed or buffered.
func (s *Server) deliver(w http.ResponseWriter, resp *http.Response, k *store.Key, v relayView, cancel context.CancelFunc, secret string, scrubBody bool, cap capture) {
	if scrubBody {
		defer resp.Body.Close()
		resp.Body = readCloser{newScrubReader(resp.Body, secret), resp.Body}
	}
	if !cap.any() || !s.Cache.Enabled() {
		relayStream(w, resp, v, cancel, !scrubBody)
		return
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxCachedBody+1))
	if err != nil || len(buf) > maxCachedBody {
		resp.Body = readCloser{io.MultiReader(bytes.NewReader(buf), resp.Body), resp.Body}
		setHeader(w.Header(), "Cache", "bypass")
		relayStream(w, resp, v, cancel, !scrubBody)
		return
	}
	resp.Body.Close()
	entry := &respcache.Entry{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: buf, KeyID: k.ID}
	// A scrubbed body may have had the key in it, which rules out sharing it.
	if cap.cacheKey != "" && (!scrubBody || !bytes.Contains(buf, scrubMark)) && cacheable(resp, buf, k, secret) {
		s.Cache.Set(cap.cacheKey, entry, cap.ttl)
		setHeader(w.Header(), "Cache", "miss")
	} else if cap.cacheKey != "" {
		setHeader(w.Header(), "Cache", "bypass")
	}
	if cap.idemKey != "" {
		s.Cache.Set(cap.idemKey, entry, idempotencyTTL)
	}
	relay(w, resp, v, buf)
}

// droppedResponseHeaders never pass from an upstream to the client. Besides
// hop-by-hop headers this covers anything that would let an upstream act on
// the dashboard origin: cookies, CORS grants and Acnologia's own security headers.
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

// relayView is what one caller learns about how its request was served.
type relayView struct {
	keyID    int64
	attempts int
	// hideKey leaves out which key served and how many were tried: a share
	// or person principal must not learn the owner's key ids or their count.
	hideKey bool
}

func view(a *store.App, keyID int64, attempts int) relayView {
	return relayView{keyID: keyID, attempts: attempts, hideKey: hidesKey(a)}
}

// hidesKey reports whether the app backs a share (with a server or a person).
func hidesKey(a *store.App) bool { return a.ShareID != nil }

// mayReadKey reports whether the app could hold the key anyway by leasing
// it, so an echo of the key in a response body tells it nothing new. Share
// and proxy only apps cannot, and no app may lease another server's token or
// a handed over key.
func mayReadKey(a *store.App, sv *store.Service) bool {
	return a.ShareID == nil && a.AllowsLease() && sv.Provider != providers.PeerSlug && !isHandoverService(sv)
}

// keyHeaders name the key that served a request.
var keyHeaders = []string{"Key-Id", "Attempts"}

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response, v relayView) {
	for k, vs := range resp.Header {
		ck := http.CanonicalHeaderKey(k)
		if droppedResponseHeaders[ck] {
			continue
		}
		// Another Acnologia upstream (a shared service) sends its own
		// X-Acnologia-* headers; this server's own ones win.
		if (strings.HasPrefix(ck, headerPrefix) || strings.HasPrefix(ck, legacyHeaderPrefix)) && w.Header().Get(ck) != "" {
			continue
		}
		w.Header()[k] = vs
	}
	h := w.Header()
	h.Set("Content-Security-Policy", proxyCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	if v.hideKey {
		for _, n := range keyHeaders {
			h.Del(headerPrefix + n)
			h.Del(legacyHeaderPrefix + n)
		}
		return
	}
	setHeader(h, "Key-Id", itoa(int(v.keyID)))
	setHeader(h, "Attempts", itoa(v.attempts))
}

// relay writes a buffered upstream response to the client.
func relay(w http.ResponseWriter, resp *http.Response, v relayView, body []byte) {
	copyResponseHeaders(w, resp, v)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// relayBufSize is the copy buffer of a streamed relay.
const relayBufSize = 32 << 10

// relayBufs holds copy buffers for relays that pass the upstream body
// through unchanged, so a proxied request does not allocate one each time.
var relayBufs = sync.Pool{New: func() any { b := make([]byte, relayBufSize); return &b }}

// relayStream streams the upstream body to the client. If no bytes arrive for
// proxyIdleTimeout it calls cancel, which aborts the upstream read.
//
// pooled takes the copy buffer from relayBufs. Only the unscrubbed relay
// sets it; the buffer goes back, cleared, after the last Write has returned.
func relayStream(w http.ResponseWriter, resp *http.Response, v relayView, cancel context.CancelFunc, pooled bool) {
	defer resp.Body.Close()
	copyResponseHeaders(w, resp, v)
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	idle := time.AfterFunc(proxyIdleTimeout, cancel)
	defer idle.Stop()
	var buf []byte
	used := 0 // high water mark of buf, cleared before it is pooled again
	if pooled {
		bp := relayBufs.Get().(*[]byte)
		buf = *bp
		defer func() {
			clear(buf[:used])
			relayBufs.Put(bp)
		}()
	} else {
		buf = make([]byte, relayBufSize)
	}
	for {
		n, err := resp.Body.Read(buf)
		used = max(used, n)
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

// sanitizeUpstreamHeaders removes the secret from upstream response headers
// (a redirect that keeps the query string would otherwise hand a query-auth
// key to the app in Location). Unless the upstream is another Acnologia (the
// owner of a shared service), it also drops X-Acnologia-* and X-Cogito-*
// headers: a provider has no business stating share status or key ids, and
// a made up X-Acnologia-Share would make a receiving server drop its link.
func sanitizeUpstreamHeaders(h http.Header, secret string, fromPeer bool) {
	for k, vs := range h {
		ck := http.CanonicalHeaderKey(k)
		if !fromPeer && (strings.HasPrefix(ck, headerPrefix) || strings.HasPrefix(ck, legacyHeaderPrefix)) {
			delete(h, k)
			continue
		}
		if secret == "" {
			continue
		}
		for i, v := range vs {
			vs[i] = crypto.Scrub(v, secret)
		}
		h[k] = vs
	}
}

// errorCodeOf returns error.code from an Acnologia error body, or "".
func errorCodeOf(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return e.Error.Code
}

// scrub removes the secret from upstream messages before they are stored.
func scrub(msg, secret string) string { return crypto.Scrub(msg, secret) }

// hopHeader counts the Acnologia servers a proxied request passed through.
const hopHeader = "X-Acnologia-Hop"

// maxPeerHops is how many servers a request may pass through; a share
// cannot be shared onward, so more means a loop.
const maxPeerHops = 3

func requestHops(r *http.Request) int {
	n := 0
	for _, c := range strings.TrimSpace(r.Header.Get(hopHeader)) {
		if c < '0' || c > '9' {
			return maxPeerHops + 1
		}
		n = n*10 + int(c-'0')
		if n > 1000 {
			return n
		}
	}
	return n
}
