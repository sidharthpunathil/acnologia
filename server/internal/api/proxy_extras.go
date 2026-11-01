package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cogito-keys/cogito/internal/breaker"
	"github.com/cogito-keys/cogito/internal/egress"
	"github.com/cogito-keys/cogito/internal/notify"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/respcache"
	"github.com/cogito-keys/cogito/internal/store"
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
// its request or budget limits.
func (s *Server) appLimited(w http.ResponseWriter, r *http.Request, a *store.App) bool {
	hit := s.appLimitHit(r.Context(), a)
	if hit == nil {
		return false
	}
	retry := max(int(math.Ceil(time.Until(hit.ResetAt).Seconds())), 1)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	writeJSON(w, 429, map[string]any{
		"error":           apiError{Code: "app_limit_reached", Message: "this app reached its " + strings.ReplaceAll(hit.Limit, "_", " ") + " limit"},
		"limit":           hit.Limit,
		"reset_at":        hit.ResetAt,
		"retry_after_sec": retry,
	})
	return true
}

// appLimitHit returns the limit the app has reached, or nil, and tells
// webhooks about it.
func (s *Server) appLimitHit(ctx context.Context, a *store.App) *store.LimitHit {
	l, err := s.Store.GetAppLimits(ctx, a.ID)
	if err != nil || !l.Any() {
		return nil
	}
	now := time.Now()
	u, err := s.Store.GetAppUsage(ctx, a.ID, now)
	if err != nil {
		return nil
	}
	hit := store.CheckLimits(l, u, now)
	if hit != nil && s.Pool.Notify != nil {
		s.Pool.Notify.Emit(notify.Event{Type: notify.AppLimit, AppID: a.ID, App: a.Name,
			Message: fmt.Sprintf("app %q reached its %s limit, it resets %s", a.Name, strings.ReplaceAll(hit.Limit, "_", " "), hit.ResetAt.Format(time.RFC3339)),
			Data:    map[string]any{"limit": hit.Limit, "reset_at": hit.ResetAt}})
	}
	return hit
}

// idempotencyKey scopes a client's Idempotency-Key to the app, target and request line.
func idempotencyKey(r *http.Request, appID int64, slug, path string) string {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" || len(k) > 200 {
		return ""
	}
	return fmt.Sprintf("idem|%d|%s|%s|%s?%s|%s", appID, slug, r.Method, path, cacheQuery(r), k)
}

// noCache reports whether the client asked to skip the response cache.
func noCache(r *http.Request) bool {
	cc := strings.ToLower(r.Header.Get("Cache-Control"))
	return strings.Contains(cc, "no-cache") || strings.Contains(cc, "no-store") || r.Header.Get("Pragma") == "no-cache"
}

func cacheQuery(r *http.Request) string {
	q := r.URL.Query()
	q.Del("cogito_token")
	return q.Encode() // sorted by key, so parameter order does not matter
}

// responseCacheKey is per service so changing a service can purge its entries.
func responseCacheKey(serviceID int64, r *http.Request, path string) string {
	return fmt.Sprintf("%s%s|%s?%s|%s", servicePrefix(serviceID), r.Method, path, cacheQuery(r), r.Header.Get("Accept"))
}

func servicePrefix(serviceID int64) string { return "svc|" + strconv.FormatInt(serviceID, 10) + "|" }

func cacheTTL(sv *store.Service, prov *providers.Spec) time.Duration {
	if sv.CacheTTLSec != nil {
		return time.Duration(*sv.CacheTTLSec) * time.Second
	}
	return time.Duration(prov.Defaults.CacheTTLSec) * time.Second
}

func costPerRequest(sv *store.Service, prov *providers.Spec) float64 {
	if sv.CostPerRequest != nil {
		return *sv.CostPerRequest
	}
	return prov.Quota.CostPerRequest
}

func breakerSettings(prov *providers.Spec) breaker.Settings {
	th, win, reset := prov.BreakerSettings()
	return breaker.Settings{Threshold: th, Window: win, Reset: reset}
}

// cacheable decides whether a successful response may be shared with other
// apps. Responses made with hidden or restricted keys, responses that echo
// the key, and responses the upstream marks private are never shared.
func cacheable(resp *http.Response, body []byte, k *store.Key, secret string) bool {
	if resp.StatusCode < 200 || resp.StatusCode > 299 || k.Hidden || k.Restricted {
		return false
	}
	cc := strings.ToLower(resp.Header.Get("Cache-Control"))
	if strings.Contains(cc, "no-store") || strings.Contains(cc, "private") {
		return false
	}
	if secret != "" && (bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte(url.QueryEscape(secret)))) {
		return false
	}
	return true
}

// writeCached replays a stored response. mode is hit or replay.
func writeCached(w http.ResponseWriter, e *respcache.Entry, mode string) {
	copyResponseHeaders(w, &http.Response{Header: e.Header}, e.KeyID, 0)
	if mode == "replay" {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.Header().Set("X-Cogito-Cache", mode)
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
				Message: sv.Name + " keeps failing (" + reason + "), Cogito paused traffic to it"})
		}
		return
	}
	s.Store.AddEvent(ctx, store.NewEvent{Kind: "service_up", ServiceID: sv.ID, Detail: "answers again, traffic resumed"})
	if s.Pool.Notify != nil {
		s.Pool.Notify.Emit(notify.Event{Type: notify.ServiceUp, ServiceID: sv.ID, Service: sv.Slug, Message: sv.Name + " answers again"})
	}
}
