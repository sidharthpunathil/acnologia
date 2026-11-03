package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/breaker"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/respcache"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	// maxCachedBody is the largest response the cache or idempotency layer keeps.
	maxCachedBody = 1 << 20
	// idempotencyTTL is how long a response is replayed for a repeated Idempotency-Key.
	idempotencyTTL = 10 * time.Minute
)

// proxyTargets resolves /proxy/{slug} to the services that may serve it: one
// service, or the members of a service group in fallback order.
func (s *Server) proxyTargets(w http.ResponseWriter, r *http.Request, slug string) ([]*store.Service, bool) {
	a := appFrom(r)
	sv, err := s.Store.GetServiceBySlug(r.Context(), slug)
	if err == nil {
		if !a.CanUse(slug) {
			writeErr(w, 403, "service_not_allowed", "this app cannot use service "+slug)
			return nil, false
		}
		return []*store.Service{sv}, true
	}
	if !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return nil, false
	}
	g, err := s.Store.GetGroupBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "unknown_service", "no service or group named "+slug)
		} else {
			writeStoreErr(w, err)
		}
		return nil, false
	}
	// Access to a group grants its members for requests made through it.
	if !a.CanUse(slug) {
		writeErr(w, 403, "service_not_allowed", "this app cannot use group "+slug)
		return nil, false
	}
	var out []*store.Service
	for _, m := range g.Members {
		if sv, err := s.Store.GetServiceBySlug(r.Context(), m); err == nil {
			out = append(out, sv)
		}
	}
	if len(out) == 0 {
		writeErr(w, 503, "empty_group", "group "+slug+" has no services")
		return nil, false
	}
	return out, true
}

// appLimited writes a 429 and returns true when the app has reached one of
// its request or budget limits. Otherwise the request holds a place under
// the limits until release, which the caller runs after recording usage.
func (s *Server) appLimited(w http.ResponseWriter, r *http.Request, a *store.App, est float64) (release func(), limited bool) {
	hit, release := s.admitApp(r.Context(), a, est)
	if hit == nil {
		return release, false
	}
	retry := max(int(math.Ceil(time.Until(hit.ResetAt).Seconds())), 1)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	writeJSON(w, 429, map[string]any{
		"error":           apiError{Code: "app_limit_reached", Message: "this app reached its " + strings.ReplaceAll(hit.Limit, "_", " ") + " limit"},
		"limit":           hit.Limit,
		"reset_at":        hit.ResetAt,
		"retry_after_sec": retry,
	})
	return nil, true
}

// admission is one app's requests admitted under its limits whose usage is
// not recorded yet. mu also makes check and reserve one step per app.
type admission struct {
	mu   sync.Mutex
	reqs int64
	cost float64
}

func noRelease() {}

// admitApp returns the limit the app has reached, or nil, and tells webhooks
// about it. Usage is recorded only once a request is served, so a request
// that passes reserves one request and est dollars until release: without
// that, a burst of concurrent requests would all see the same usage and pass
// a limit that had room for one.
func (s *Server) admitApp(ctx context.Context, a *store.App, est float64) (*store.LimitHit, func()) {
	l, err := s.Store.GetAppLimits(ctx, a.ID)
	if err != nil || !l.Any() {
		return nil, noRelease
	}
	v, _ := s.admitted.LoadOrStore(a.ID, &admission{})
	ad := v.(*admission)
	ad.mu.Lock()
	defer ad.mu.Unlock()
	now := time.Now()
	u, err := s.Store.GetAppUsage(ctx, a.ID, now)
	if err != nil {
		return nil, noRelease
	}
	u.TodayRequests += ad.reqs
	u.MonthRequests += ad.reqs
	u.TodayCost += ad.cost
	u.MonthCost += ad.cost
	hit := store.CheckLimits(l, u, now)
	if hit == nil {
		ad.reqs++
		ad.cost += est
		var once sync.Once
		return nil, func() {
			once.Do(func() {
				ad.mu.Lock()
				ad.reqs--
				ad.cost -= est
				ad.mu.Unlock()
			})
		}
	}
	if hit != nil && s.Pool.Notify != nil {
		s.Pool.Notify.Emit(notify.Event{Type: notify.AppLimit, AppID: a.ID, App: a.Name,
			Message: fmt.Sprintf("app %q reached its %s limit, it resets %s", a.Name, strings.ReplaceAll(hit.Limit, "_", " "), hit.ResetAt.Format(time.RFC3339)),
			Data:    map[string]any{"limit": hit.Limit, "reset_at": hit.ResetAt}})
	}
	return hit, nil
}

// idempotencyKey scopes a client's Idempotency-Key to the app, target and request line.
func idempotencyKey(r *http.Request, appID int64, slug, path string) string {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" || len(k) > 200 {
		return ""
	}
	return fmt.Sprintf("%s%s|%s|%s?%s|%s", idempotencyPrefix(appID), slug, r.Method, path, cacheQuery(r), k)
}

// idempotencyPrefix starts every idempotency entry of one app. The closing
// bar keeps app 1's prefix from matching app 12.
func idempotencyPrefix(appID int64) string { return "idem|" + strconv.FormatInt(appID, 10) + "|" }

// noCache reports whether the client asked to skip the response cache.
func noCache(r *http.Request) bool {
	cc := strings.ToLower(r.Header.Get("Cache-Control"))
	return strings.Contains(cc, "no-cache") || strings.Contains(cc, "no-store") || r.Header.Get("Pragma") == "no-cache"
}

func cacheQuery(r *http.Request) string {
	q := r.URL.Query()
	delTokenParams(q)
	return q.Encode() // sorted by key, so parameter order does not matter
}

// responseCacheKey is per service so changing a service can purge its
// entries; the slug is in it too, so a reused service id cannot match an
// entry written for another service. A share (with a server or a person)
// gets entries of its own: its key scope may pick other keys, and what one
// side stored is never served to the other. The request variant covers the
// headers and body that reach the upstream, so a caller cannot store an
// answer shaped by them for callers that did not send them.
func responseCacheKey(sv *store.Service, a *store.App, r *http.Request, path string, body []byte) string {
	who := "all"
	if a.ShareID != nil {
		who = "app" + strconv.FormatInt(a.ID, 10)
	}
	return fmt.Sprintf("%s%s|%s|%s|%s?%s|%s|%s", servicePrefix(sv.ID), sv.Slug, who, r.Method, path, cacheQuery(r), r.Header.Get("Accept"), requestVariant(r, sv, body))
}

// variantIgnored are request headers that do not reach the upstream or do
// not change its answer, so they do not split the cache.
var variantIgnored = map[string]bool{
	"Accept": true, "Cache-Control": true, "Pragma": true, "Idempotency-Key": true, "User-Agent": true,
	"X-Requested-With": true, "X-Request-Id": true, "X-Correlation-Id": true, "Traceparent": true, "Tracestate": true, "Baggage": true,
}

// requestVariant hashes the forwarded request headers and body, or is empty
// when there are none that matter.
func requestVariant(r *http.Request, sv *store.Service, body []byte) string {
	skip := map[string]bool{}
	for _, h := range hopHeaders {
		skip[http.CanonicalHeaderKey(h)] = true
	}
	if sv.AuthType == "header" || sv.AuthType == "bearer" {
		name := sv.AuthName
		if name == "" {
			name = "Authorization"
		}
		skip[http.CanonicalHeaderKey(name)] = true // replaced by the key
	}
	if isBearerAppToken(r.Header.Get("Authorization")) {
		skip["Authorization"] = true
	}
	if len(body) == 0 {
		skip["Content-Type"] = true
	}
	var names []string
	for k := range r.Header {
		ck := http.CanonicalHeaderKey(k)
		if !skip[ck] && !variantIgnored[ck] {
			names = append(names, ck)
		}
	}
	if len(names) == 0 && len(body) == 0 {
		return ""
	}
	sort.Strings(names)
	h := sha256.New()
	for _, k := range names {
		fmt.Fprintf(h, "%s\x00%q\x00", k, r.Header.Values(k))
	}
	fmt.Fprintf(h, "\x01%d\x01", len(body))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func servicePrefix(serviceID int64) string { return "svc|" + strconv.FormatInt(serviceID, 10) + "|" }

func cacheTTL(sv *store.Service, prov *providers.Spec) time.Duration {
	if sv.CacheTTLSec != nil {
		return time.Duration(*sv.CacheTTLSec) * time.Second
	}
	return time.Duration(prov.Defaults.CacheTTLSec) * time.Second
}

// costPerRequest is the USD cost of one successful request: the service's
// override, else the price of the provider offer that matches the request
// (free offers cost 0), else quota.cost_per_request. A lease passes an empty
// method and gets the provider wide price.
func costPerRequest(sv *store.Service, prov *providers.Spec, method, path string) float64 {
	if sv.CostPerRequest != nil {
		return *sv.CostPerRequest
	}
	return prov.CostFor(method, path)
}

func breakerSettings(prov *providers.Spec) breaker.Settings {
	th, win, reset := prov.BreakerSettings()
	return breaker.Settings{Threshold: th, Window: win, Reset: reset}
}

// cacheable decides whether a successful response may be shared with other
// apps. Responses made with hidden or restricted keys, responses that echo
// the key, and responses the upstream marks private are never shared.
func cacheable(resp *http.Response, body []byte, k *store.Key, secret string) bool {
	// A partial answer (206) depends on the caller's Range; never shared.
	if resp.StatusCode < 200 || resp.StatusCode > 299 || resp.StatusCode == http.StatusPartialContent || k.Hidden || k.Restricted {
		return false
	}
	cc := strings.ToLower(resp.Header.Get("Cache-Control"))
	if strings.Contains(cc, "no-store") || strings.Contains(cc, "private") {
		return false
	}
	if at, _ := firstForm(body, keyForms(secret)); at >= 0 {
		return false
	}
	return true
}

// writeCached replays a stored response. mode is hit or replay.
func writeCached(w http.ResponseWriter, e *respcache.Entry, mode string, hideKey bool) {
	copyResponseHeaders(w, &http.Response{Header: e.Header}, relayView{keyID: e.KeyID, hideKey: hideKey})
	if mode == "replay" {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	setHeader(w.Header(), "Cache", mode)
	w.WriteHeader(e.Status)
	_, _ = w.Write(e.Body)
}

type readCloser struct {
	io.Reader
	io.Closer
}

// serviceDownError means the service's circuit breaker is open.
type serviceDownError struct {
	Service    string
	RetryAfter time.Duration
}

func (e *serviceDownError) Error() string {
	return e.Service + " is paused after repeated provider errors"
}

// upstreamClient returns the client for a key: through its egress proxy, or
// its service's, or direct.
func (s *Server) upstreamClient(ctx context.Context, sv *store.Service, k *store.Key) (*http.Client, error) {
	id := egress.Pick(k.EgressID, sv.EgressID)
	if id == nil || s.Pool.Egress == nil {
		return s.UpClient, nil
	}
	rt, err := s.Pool.Egress.Transport(ctx, id)
	if err != nil {
		return nil, err
	}
	return egress.Client(rt, 0), nil
}

// onBreakerChange turns breaker transitions into webhook events and activity.
func (s *Server) onBreakerChange(serviceID int64, down bool, reason string) {
	ctx := context.Background()
	sv, err := s.Store.GetService(ctx, serviceID)
	if err != nil {
		return
	}
	if down {
		s.Store.AddEvent(ctx, store.NewEvent{Kind: "service_down", ServiceID: sv.ID, Detail: "paused after repeated provider errors: " + reason})
		if s.Pool.Notify != nil {
			s.Pool.Notify.Emit(notify.Event{Type: notify.ServiceDown, ServiceID: sv.ID, Service: sv.Slug,
				Message: sv.Name + " keeps failing (" + reason + "), Acnologia paused traffic to it"})
		}
		return
	}
	s.Store.AddEvent(ctx, store.NewEvent{Kind: "service_up", ServiceID: sv.ID, Detail: "answers again, traffic resumed"})
	if s.Pool.Notify != nil {
		s.Pool.Notify.Emit(notify.Event{Type: notify.ServiceUp, ServiceID: sv.ID, Service: sv.Slug, Message: sv.Name + " answers again"})
	}
}
