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
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/breaker"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/keyscrub"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/respcache"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	// maxCachedBody is the largest response the response cache keeps.
	maxCachedBody = 1 << 20
)

// resolvedServiceKey carries a service that an in-process caller (MCP's
// proxy_request) already looked up by slug for this request.
type resolvedServiceKey struct{}

func withResolvedService(ctx context.Context, sv *store.Service) context.Context {
	return context.WithValue(ctx, resolvedServiceKey{}, sv)
}

// proxyTargets resolves /proxy/{slug} to the services that may serve it: one
// service, or the members of a service group in fallback order.
func (s *Server) proxyTargets(w http.ResponseWriter, r *http.Request, slug string) ([]*store.Service, *store.ServiceGroup, bool) {
	a := appFrom(r)
	// Caller tags are read first, so the header never reaches a provider,
	// and the app's deny rules are checked on every name /proxy resolves.
	if !readCallerTags(w, r) {
		return nil, nil, false
	}
	rules, ok := s.denyRules(w, r, a)
	if !ok {
		return nil, nil, false
	}
	path := proxyPath(r, slug)
	// A service looked up earlier in this request saves a second query; the
	// access checks below run on it all the same.
	sv, ok := r.Context().Value(resolvedServiceKey{}).(*store.Service)
	var err error
	if !ok || sv == nil || sv.Slug != slug {
		sv, err = s.Store.GetServiceBySlug(r.Context(), slug)
	}
	if err == nil {
		if !(a.CanUse(slug) || capabilityGrants(r, slug)) || !s.sharedAs(r.Context(), a, sv) {
			writeErr(w, 403, "service_not_allowed", "this app cannot use service "+slug)
			return nil, nil, false
		}
		if rule := store.MatchDeny(rules, denyTarget(r.Method, slug, sv, path)); rule != nil {
			writeDenied(w, rule)
			return nil, nil, false
		}
		return []*store.Service{sv}, nil, true
	}
	if !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return nil, nil, false
	}
	g, err := s.Store.GetGroupBySlug(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		// A capability is the third kind of name under /proxy: it runs here
		// and answers the request itself.
		if c, cerr := s.Store.GetCapabilityBySlug(r.Context(), slug); cerr == nil {
			// Its routes come back through here, each checked on its own.
			if rule := store.MatchDeny(rules, denyTarget(r.Method, slug, nil, path)); rule != nil {
				writeDenied(w, rule)
				return nil, nil, false
			}
			s.serveCapability(w, r, c)
			return nil, nil, false
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "unknown_service", "no service, group or capability named "+slug)
		} else {
			writeStoreErr(w, err)
		}
		return nil, nil, false
	}
	// Access to a group grants its members for requests made through it.
	// A share lists only services, so a group under a shared slug is never
	// what was shared.
	if !a.CanUse(slug) || a.ShareID != nil {
		writeErr(w, 403, "service_not_allowed", "this app cannot use group "+slug)
		return nil, nil, false
	}
	// A member a deny rule covers drops out of the group; the request is
	// refused only when that leaves none.
	var out []*store.Service
	var denied *store.DenyRule
	for _, m := range g.Members {
		if sv, err := s.Store.GetServiceBySlug(r.Context(), m); err == nil {
			if rule := store.MatchDeny(rules, denyTarget(r.Method, slug, sv, path)); rule != nil {
				denied = rule
				continue
			}
			out = append(out, sv)
		}
	}
	if len(out) == 0 && denied != nil {
		writeDenied(w, denied)
		return nil, nil, false
	}
	if len(out) == 0 {
		writeErr(w, 503, "empty_group", "group "+slug+" has no services")
		return nil, nil, false
	}
	return out, g, true
}

// sharedAs reports whether a share app may use sv: the slug it asked for must
// still name the service that was shared under it. Other apps pass.
func (s *Server) sharedAs(ctx context.Context, a *store.App, sv *store.Service) bool {
	if a.ShareID == nil {
		return true
	}
	id, ok, err := s.Store.ShareServiceID(ctx, a.ID, sv.Slug)
	return err == nil && ok && id == sv.ID
}

// appLimited writes the refusal and returns true when the request would
// break one of the limits it falls under: the app's, its token's, a caller
// tag value's (429), or the server's daily spend ceiling (503). A request
// whose tags contradict its token's pinned ones is a 400. Otherwise the
// request holds a place under every limit until release, which the caller
// runs after recording usage.
func (s *Server) appLimited(w http.ResponseWriter, r *http.Request, a *store.App, est float64) (release func(), limited bool) {
	if !readCallerTags(w, r) {
		return nil, true
	}
	hit, release, err := s.admit(r.Context(), a, est)
	if err != nil {
		// Limits that cannot be read are not limits that do not exist: the
		// request waits rather than spends past a budget nobody could check.
		retry := int(limitsRetry.Seconds())
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeJSON(w, 503, map[string]any{
			"error":           apiError{Code: "limits_unavailable", Message: "could not read this app's limits, retry shortly"},
			"retry_after_sec": retry,
		})
		return nil, true
	}
	if hit == nil {
		return release, false
	}
	if hit.Limit == limitTagPinned {
		writeJSON(w, 400, map[string]any{
			"error": apiError{Code: "tag_pinned", Message: pinnedMessage(hit.Tag)},
			"tag":   hit.Tag,
		})
		return nil, true
	}
	retry := max(int(math.Ceil(time.Until(hit.ResetAt).Seconds())), 1)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	status, code, msg := 429, "app_limit_reached", "this app reached its "+limitWords(hit)+" limit"
	switch {
	case hit.Limit == limitServerCeiling:
		status, code, msg = 503, limitServerCeiling, "this server reached its daily spend ceiling, it resets at midnight UTC"
	case strings.HasPrefix(hit.Limit, "token_"):
		code, msg = "token_limit_reached", "this token reached its "+limitWords(hit)+" limit"
	case strings.HasPrefix(hit.Limit, "tag_"):
		code = "tag_limit_reached"
	}
	out := map[string]any{
		"error":           apiError{Code: code, Message: msg},
		"limit":           hit.Limit,
		"reset_at":        hit.ResetAt,
		"retry_after_sec": retry,
	}
	if hit.Tag != "" {
		out["tag"] = hit.Tag
	}
	writeJSON(w, status, out)
	return nil, true
}

const (
	// metaHeader carries caller tags: customer=cust_A,workspace=w1.
	metaHeader          = "X-Acnologia-Meta"
	limitTagPinned      = "tag_pinned"
	limitServerCeiling  = "server_spend_ceiling"
	limitTokenPrefix    = "token_"
	limitTagPrefix      = "tag_"
	denyToolPathPrefix  = "/tools/"
	denyRuleErrorPrefix = "denied_by_rule: "
)

// pinnedMessage is the tag_pinned message for the pinned key k.
func pinnedMessage(k string) string {
	return "this token is pinned to its own " + k + " value: send that value or leave " + k + " out of " + metaHeader
}

// limitWords is a limit in words: "daily budget", "token daily requests",
// "tag daily budget for customer=cust_A".
func limitWords(hit *store.LimitHit) string {
	w := strings.ReplaceAll(hit.Limit, "_", " ")
	if hit.Tag != "" {
		w += " for " + hit.Tag
	}
	return w
}

// readCallerTags reads X-Acnologia-Meta into the request's usage scope and
// removes the header, so it neither reaches the provider nor splits the
// cache. A malformed value is a 400. A request without the header keeps
// the tags an outer request (a capability calling its routes) set.
func readCallerTags(w http.ResponseWriter, r *http.Request) bool {
	vs := r.Header.Values(metaHeader)
	if len(vs) == 0 {
		return true
	}
	r.Header.Del(metaHeader)
	tags, err := store.ParseMeta(strings.Join(vs, ","))
	if err != nil {
		writeErr(w, 400, "bad_meta", err.Error())
		return false
	}
	if len(tags) > 0 {
		store.SetUsageTags(r.Context(), tags)
	}
	return true
}

// pending is what admitted requests not yet recorded hold under one limit.
type pending struct {
	reqs int64
	cost float64
}

func (p *pending) onto(u store.AppUsage) store.AppUsage {
	if p != nil {
		u.TodayRequests += p.reqs
		u.MonthRequests += p.reqs
		u.TodayCost += p.cost
		u.MonthCost += p.cost
	}
	return u
}

// admission is one app's requests admitted under its limits whose usage is
// not recorded yet, for the app as a whole, per token and per tag value.
// mu also makes check and reserve one step per app. The server's own
// admission (serverAdmitKey) uses reqs and cost for the spend ceiling.
type admission struct {
	mu   sync.Mutex
	reqs int64
	cost float64
	tok  map[int64]*pending
	// grant is per OAuth grant, for requests made with an OAuth access
	// token, which the app's per-token default caps like a token.
	grant map[string]*pending
	tag   map[string]*pending
}

// serverAdmitKey holds the server-wide admission in Server.admitted.
type serverAdmitKey struct{}

func noRelease() {}

// limitsRetry is the Retry-After of a request refused because the app's
// limits or usage could not be read.
const limitsRetry = 5 * time.Second

// admitApp returns the limit the app would break with this request, or nil,
// and tells webhooks about it. When the limits or usage cannot be read it
// fails closed, with the limit "limits_unavailable".
func (s *Server) admitApp(ctx context.Context, a *store.App, est float64) (*store.LimitHit, func()) {
	hit, release, err := s.admit(ctx, a, est)
	if err != nil {
		return &store.LimitHit{Limit: "limits_unavailable", ResetAt: time.Now().Add(limitsRetry)}, nil
	}
	return hit, release
}

// spendCeiling is the server-wide daily spend ceiling in effect: the one a
// superadmin saved, else ACNOLOGIA_DAILY_SPEND_CEILING_USD. 0 is none.
func (s *Server) spendCeiling(ctx context.Context) float64 {
	if v, saved := s.Store.SavedSpendCeiling(ctx); saved {
		return v
	}
	return s.Cfg.DailySpendCeilingUSD
}

// tagBudgetCheck is one of the request's tag values that has a budget.
type tagBudgetCheck struct {
	key, value string
	limits     store.AppLimits
}

// pinTags is the request's caller tags with its token's pinned ones laid
// over them, or a tag_pinned hit when the caller sent another value for a
// pinned key.
func pinTags(tags, pinned map[string]string) (map[string]string, *store.LimitHit) {
	if len(pinned) == 0 {
		return tags, nil
	}
	out := make(map[string]string, len(tags)+len(pinned))
	for k, v := range tags {
		out[k] = v
	}
	for k, v := range pinned {
		if cur, ok := out[k]; ok && cur != v {
			return nil, &store.LimitHit{Limit: limitTagPinned, Tag: k}
		}
		out[k] = v
	}
	return out, nil
}

// admit is admitApp that returns a read error instead. Usage is recorded
// only once a request is served, so a request that passes reserves one
// request and est dollars until release: without that, a burst of
// concurrent requests would all see the same usage and pass a limit that
// had room for one. The check counts the request itself (store.AdmitLimits),
// so the last request under a budget cannot overshoot it by its own cost.
//
// Every limit a request falls under is checked and reserved in one step
// under the app's lock: the app's, the token's (the token's own over the
// app's per-token default; for an OAuth access token, its grant's, which is
// the per-token default), each caller tag value's, and last the server's
// daily spend ceiling under the server's lock (always taken after an app's,
// so the two cannot deadlock). Nothing is reserved unless all pass.
func (s *Server) admit(ctx context.Context, a *store.App, est float64) (*store.LimitHit, func(), error) {
	in, err := s.Store.GetAdmissionLimits(ctx, a.ID, a.TokenID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, noRelease, nil
	}
	if err != nil {
		return nil, nil, err
	}
	tags, pinHit := pinTags(store.UsageTags(ctx), in.Pinned)
	if pinHit != nil {
		return pinHit, nil, nil
	}
	store.SetUsageTags(ctx, tags)
	var budgets []tagBudgetCheck
	for _, b := range in.TagBudgets {
		if v, ok := tags[b.Key]; ok {
			budgets = append(budgets, tagBudgetCheck{b.Key, v, b.AppLimits})
		}
	}
	tokLimited := a.TokenID != 0 && in.Token.Any()
	// An OAuth access token has no app token: its grant stands in for one
	// and gets the app's per-token default.
	grantID := ""
	if a.TokenID == 0 && a.OAuthGrantID != "" {
		store.SetUsageGrant(ctx, a.OAuthGrantID)
		if in.TokenDefault.Any() {
			grantID = a.OAuthGrantID
		}
	}
	ceiling := s.spendCeiling(ctx)
	if !in.App.Any() && !tokLimited && grantID == "" && len(budgets) == 0 && ceiling <= 0 {
		return nil, noRelease, nil
	}
	v, _ := s.admitted.LoadOrStore(a.ID, &admission{})
	ad := v.(*admission)
	ad.mu.Lock()
	defer ad.mu.Unlock()
	now := time.Now()
	hit, err := s.checkLimits(ctx, ad, a, in, tokLimited, grantID, budgets, now, est)
	if err != nil {
		return nil, nil, err
	}
	var server *admission
	if hit == nil && ceiling > 0 {
		v, _ := s.admitted.LoadOrStore(serverAdmitKey{}, &admission{})
		server = v.(*admission)
		if hit, err = s.reserveCeiling(ctx, server, ceiling, now, est); err != nil {
			return nil, nil, err
		}
		if hit != nil {
			server = nil
		}
	}
	if hit != nil {
		if s.Pool.Notify != nil {
			s.Pool.Notify.Emit(notify.Event{Type: notify.AppLimit, AppID: a.ID, App: a.Name,
				Message: fmt.Sprintf("app %q reached its %s limit, it resets %s", a.Name, limitWords(hit), hit.ResetAt.Format(time.RFC3339)),
				Data:    map[string]any{"limit": hit.Limit, "reset_at": hit.ResetAt, "tag": hit.Tag}})
		}
		return hit, nil, nil
	}
	// Reserve under every limit the request falls under.
	held := []*pending{{}}
	ad.reqs++
	ad.cost += est
	if a.TokenID != 0 {
		if ad.tok == nil {
			ad.tok = map[int64]*pending{}
		}
		p := ad.tok[a.TokenID]
		if p == nil {
			p = &pending{}
			ad.tok[a.TokenID] = p
		}
		held = append(held, p)
	}
	if grantID != "" {
		if ad.grant == nil {
			ad.grant = map[string]*pending{}
		}
		p := ad.grant[grantID]
		if p == nil {
			p = &pending{}
			ad.grant[grantID] = p
		}
		held = append(held, p)
	}
	tagKeys := make([]string, 0, len(tags))
	for k, v := range tags {
		if ad.tag == nil {
			ad.tag = map[string]*pending{}
		}
		tk := k + "=" + v
		p := ad.tag[tk]
		if p == nil {
			p = &pending{}
			ad.tag[tk] = p
		}
		held = append(held, p)
		tagKeys = append(tagKeys, tk)
	}
	for _, p := range held[1:] {
		p.reqs++
		p.cost += est
	}
	tokID := a.TokenID
	var once sync.Once
	return nil, func() {
		once.Do(func() {
			ad.mu.Lock()
			ad.reqs--
			ad.cost -= est
			if tokID != 0 {
				if p := ad.tok[tokID]; p != nil {
					if p.reqs--; p.reqs <= 0 {
						delete(ad.tok, tokID)
					} else {
						p.cost -= est
					}
				}
			}
			if grantID != "" {
				if p := ad.grant[grantID]; p != nil {
					if p.reqs--; p.reqs <= 0 {
						delete(ad.grant, grantID)
					} else {
						p.cost -= est
					}
				}
			}
			for _, tk := range tagKeys {
				if p := ad.tag[tk]; p != nil {
					if p.reqs--; p.reqs <= 0 {
						delete(ad.tag, tk)
					} else {
						p.cost -= est
					}
				}
			}
			ad.mu.Unlock()
			if server != nil {
				server.mu.Lock()
				server.reqs--
				server.cost -= est
				server.mu.Unlock()
			}
		})
	}, nil
}

// checkLimits is the first limit of the app, the token (or the OAuth grant
// grantID, when not empty) or a tag value the request would break, counting
// what is admitted and not yet recorded. The caller holds ad.mu.
func (s *Server) checkLimits(ctx context.Context, ad *admission, a *store.App, in store.AdmissionLimits, tokLimited bool, grantID string, budgets []tagBudgetCheck, now time.Time, est float64) (*store.LimitHit, error) {
	if in.App.Any() {
		u, err := s.Store.GetAppUsage(ctx, a.ID, now)
		if err != nil {
			return nil, err
		}
		u.TodayRequests += ad.reqs
		u.MonthRequests += ad.reqs
		u.TodayCost += ad.cost
		u.MonthCost += ad.cost
		if hit := store.AdmitLimits(in.App, u, now, est); hit != nil {
			return hit, nil
		}
	}
	if tokLimited {
		u, err := s.Store.GetTokenUsage(ctx, a.TokenID, now)
		if err != nil {
			return nil, err
		}
		if hit := store.AdmitLimits(in.Token, ad.tok[a.TokenID].onto(u), now, est); hit != nil {
			hit.Limit = limitTokenPrefix + hit.Limit
			return hit, nil
		}
	}
	if grantID != "" {
		u, err := s.Store.GetGrantUsage(ctx, grantID, now)
		if err != nil {
			return nil, err
		}
		if hit := store.AdmitLimits(in.TokenDefault, ad.grant[grantID].onto(u), now, est); hit != nil {
			hit.Limit = limitTokenPrefix + hit.Limit
			return hit, nil
		}
	}
	for _, b := range budgets {
		u, err := s.Store.GetTagUsage(ctx, a.ID, b.key, b.value, now)
		if err != nil {
			return nil, err
		}
		tk := b.key + "=" + b.value
		if hit := store.AdmitLimits(b.limits, ad.tag[tk].onto(u), now, est); hit != nil {
			hit.Limit, hit.Tag = limitTagPrefix+hit.Limit, tk
			return hit, nil
		}
	}
	return nil, nil
}

// reserveCeiling checks the server's spend for the UTC day, with what is
// admitted and not yet recorded, against the ceiling, and reserves est
// under it when the request fits.
func (s *Server) reserveCeiling(ctx context.Context, server *admission, ceiling float64, now time.Time, est float64) (*store.LimitHit, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	spent, err := s.Store.ServerSpendToday(ctx, now)
	if err != nil {
		return nil, err
	}
	u := store.AppUsage{TodayCost: spent + server.cost}
	if hit := store.AdmitLimits(store.AppLimits{DailyBudget: &ceiling}, u, now, est); hit != nil {
		hit.Limit = limitServerCeiling
		return hit, nil
	}
	server.reqs++
	server.cost += est
	return nil, nil
}

// ---- deny rules ----

// proxyPath is the path below /proxy/{slug}, escaped as the client sent it,
// worked out the way the proxy handler does.
func proxyPath(r *http.Request, slug string) string {
	p := r.URL.EscapedPath()
	if i := strings.Index(p, "/proxy/"+slug); i >= 0 {
		return strings.TrimPrefix(p[i+len("/proxy/"+slug):], "/")
	}
	return chi.URLParam(r, "*")
}

// denyTarget is a proxied request to sv as deny rules see it: the slug
// asked for and the service's, the upstream host and its full path there.
func denyTarget(method, asked string, sv *store.Service, path string) store.DenyTarget {
	t := store.DenyTarget{Method: method, Slugs: []string{asked}, Path: path}
	if sv == nil {
		return t
	}
	t.Slugs = append(t.Slugs, sv.Slug)
	if raw, err := serviceURL(sv, path, nil); err == nil {
		if u, err := url.Parse(raw); err == nil {
			t.Host, t.HostPath = u.Hostname(), u.EscapedPath()
		}
	}
	return t
}

// writeDenied answers 403 denied_by_rule, naming the rule.
func writeDenied(w http.ResponseWriter, rule *store.DenyRule) {
	writeJSON(w, 403, map[string]any{
		"error": apiError{Code: "denied_by_rule", Message: fmt.Sprintf("deny rule %d (%s) blocks this request", rule.ID, rule.Describe())},
		"rule":  rule,
	})
}

// denyRules reads the app's deny rules, writing the error when it fails:
// rules that cannot be read fail closed.
func (s *Server) denyRules(w http.ResponseWriter, r *http.Request, a *store.App) ([]store.DenyRule, bool) {
	rules, err := s.Store.DenyRules(r.Context(), a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return rules, true
}

// forwardDenied writes a 403 and returns true when one of the app's deny
// rules covers a forward proxy request to host and fullPath, routed to
// slug with rest as the path below the service (CONNECT carries no path,
// so only a rule for the whole host covers a tunnel).
func (s *Server) forwardDenied(w http.ResponseWriter, r *http.Request, a *store.App, host, slug, rest, fullPath string) bool {
	rules, ok := s.denyRules(w, r, a)
	if !ok {
		return true
	}
	t := store.DenyTarget{Method: r.Method, Host: host, HostPath: fullPath, Path: rest}
	if slug != "" {
		t.Slugs = []string{slug}
	}
	if rule := store.MatchDeny(rules, t); rule != nil {
		writeDenied(w, rule)
		return true
	}
	return false
}

// denyMCPTool is an error naming the deny rule that blocks a call to an
// upstream MCP tool, matched as POST /tools/<name> on the service, or nil.
func (s *Server) denyMCPTool(ctx context.Context, a *store.App, sv *store.Service, tool string) error {
	rules, err := s.Store.DenyRules(ctx, a.ID)
	if err != nil {
		return fmt.Errorf("could not read this app's deny rules: %w", err)
	}
	p := denyToolPathPrefix + tool
	t := store.DenyTarget{Method: http.MethodPost, Slugs: []string{sv.Slug}, Path: p, HostPath: p}
	if endpoint, _, _ := serviceMCPInfo(sv); endpoint != "" {
		if u, err := url.Parse(endpoint); err == nil {
			t.Host = u.Hostname()
		}
	}
	if rule := store.MatchDeny(rules, t); rule != nil {
		return fmt.Errorf("%sdeny rule %d (%s) blocks tool %s", denyRuleErrorPrefix, rule.ID, rule.Describe(), tool)
	}
	return nil
}

// idempotencyKey scopes a client's Idempotency-Key to the app, the token it
// authenticated with (the claimants of one share link share an app but not
// a token), the target and the request line. The body is checked apart,
// through the entry's fingerprint, so a reused key with another body is an
// error rather than a new request.
func idempotencyKey(r *http.Request, a *store.App, slug, path string) string {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" || len(k) > 200 {
		return ""
	}
	return fmt.Sprintf("%st%d|%s|%s|%s?%s|%s", idempotencyPrefix(a.ID), a.TokenID, slug, r.Method, path, cacheQuery(r), k)
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
	for _, name := range serviceAuth(sv).HeaderNames() {
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
// method and gets the provider wide price. Tokens are priced apart, once
// the answer has said how many it used (tokenCost).
func costPerRequest(sv *store.Service, prov *providers.Spec, method, path string) float64 {
	c, _ := costKnown(sv, prov, method, path)
	return c
}

// costKnown is costPerRequest and whether the price is known: a service
// override, a deciding offer, or a quota.cost_per_request above 0.
func costKnown(sv *store.Service, prov *providers.Spec, method, path string) (float64, bool) {
	if sv.CostPerRequest != nil {
		return *sv.CostPerRequest, true
	}
	return prov.CostKnown(method, path)
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
	if keyscrub.Contains(body, keyscrub.Forms(secret)) {
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
	// k is nil when an upstream MCP sync runs without a key (an open server):
	// only the service's own egress applies then.
	var keyEgress *int64
	if k != nil {
		keyEgress = k.EgressID
	}
	id := egress.Pick(keyEgress, sv.EgressID)
	if id == nil || s.Pool.Egress == nil {
		if sv.PublicOnly() {
			// The owner of the share controls this host's DNS: check the
			// address on every connection, not only when the link synced.
			return s.PublicUpClient, nil
		}
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
