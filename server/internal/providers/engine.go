package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// standardRules apply after a spec's own rules when UseStandardRules is set.
var standardRules = []Rule{
	{Name: "429 Too Many Requests", Match: Match{Status: []int{429}}, Outcome: RateLimited, RetryAfter: &RetryAfter{Source: "header", Value: "Retry-After"}},
	{Name: "402 Payment Required", Match: Match{Status: []int{402}}, Outcome: QuotaExhausted},
	{Name: "401/403 rejected key", Match: Match{Status: []int{401, 403}}, Outcome: Invalid},
	{Name: "5xx upstream error", Match: Match{Status: []int{500, 501, 502, 503, 504, 520, 521, 522, 523, 524, 529}}, Outcome: Error},
}

func (s *Spec) CountsPerRequest() bool { return s.Quota.CountsPerRequest }

// Classify turns an upstream response into an outcome using the spec rules.
func (s *Spec) Classify(status int, h http.Header, body []byte) Result {
	r, _ := s.ClassifyExplain(status, h, body)
	return r
}

// ClassifyExplain also returns the name of the rule that matched.
func (s *Spec) ClassifyExplain(status int, h http.Header, body []byte) (Result, string) {
	rules := s.Rules
	if s.UseStandardRules {
		rules = append(append([]Rule{}, s.Rules...), standardRules...)
	}
	var parsed any
	parsedOK := false
	parse := func() any {
		if !parsedOK {
			parsedOK = true
			_ = json.Unmarshal(body, &parsed)
		}
		return parsed
	}
	low := strings.ToLower(string(body))
	for _, r := range rules {
		if !r.Match.matches(status, h, body, low, parse) {
			continue
		}
		res := Result{Outcome: r.Outcome, Message: errorMessage(body, parse()), Quota: s.ReadQuota(h)}
		res.RetryAfter = r.RetryAfter.resolve(h, parse)
		if res.Outcome == QuotaExhausted && res.RetryAfter > 0 {
			// A retry time on exhaustion is when the quota comes back.
			t := time.Now().Add(res.RetryAfter)
			res.ResetAt = &t
		}
		return res, r.Name
	}
	if status >= 500 {
		return Result{Outcome: Error, Message: errorMessage(body, parse()), Quota: s.ReadQuota(h)}, "default 5xx"
	}
	return Result{Outcome: OK, Quota: s.ReadQuota(h)}, ""
}

func (m Match) matches(status int, h http.Header, body []byte, low string, parse func() any) bool {
	if len(m.Status) > 0 {
		if !containsInt(m.Status, status) {
			return false
		}
	} else if status < 400 {
		return false // rules without an explicit status only look at errors
	}
	if len(m.BodyContains) > 0 {
		hit := false
		for _, s := range m.BodyContains {
			if s != "" && strings.Contains(low, strings.ToLower(s)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if m.BodyRegex != "" {
		re, err := cachedRegex(m.BodyRegex)
		if err != nil || !re.Match(body) {
			return false
		}
	}
	if m.JSONPath != "" {
		v, ok := JSONPath(parse(), m.JSONPath)
		if !ok {
			return false
		}
		if m.JSONEquals != "" && !strings.EqualFold(fmt.Sprint(v), m.JSONEquals) {
			return false
		}
	}
	if m.HeaderPresent != "" && h.Get(m.HeaderPresent) == "" {
		return false
	}
	return true
}

func (r *RetryAfter) resolve(h http.Header, parse func() any) time.Duration {
	if r == nil {
		return 0
	}
	switch r.Source {
	case "header":
		name := r.Value
		if name == "" {
			name = "Retry-After"
		}
		hh := http.Header{}
		hh.Set("Retry-After", h.Get(name))
		return ParseRetryAfter(hh)
	case "fixed":
		d, _ := parseSeconds(r.Value)
		return d
	case "next_hour":
		return time.Until(time.Now().Truncate(time.Hour).Add(time.Hour))
	case "body_json":
		if v, ok := JSONPath(parse(), r.Value); ok {
			if f, ok := toFloat(v); ok && f > 0 {
				return ClampSeconds(f)
			}
		}
	}
	return 0
}

// errorMessage pulls a readable message out of common error shapes.
func errorMessage(body []byte, parsed any) string {
	for _, p := range []string{"error.message", "error", "detail.message", "message", "detail", "errors.0.message"} {
		if v, ok := JSONPath(parsed, p); ok {
			if s, ok := v.(string); ok && s != "" {
				return truncate(s, 300)
			}
		}
	}
	return snippet(body)
}

// Check calls the health endpoint and reads validity and quota.
func (s *Spec) Check(ctx context.Context, c *http.Client, baseURL, secret string) (*CheckResult, error) {
	h := s.Health
	if h == nil {
		return &CheckResult{Valid: true, Message: "this provider has no health check"}, nil
	}
	if baseURL == "" {
		baseURL = s.BaseURL
	}
	q := url.Values{}
	for k, v := range h.Query {
		q.Set(k, v)
	}
	method := h.Method
	if method == "" {
		method = http.MethodGet
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	// The request carries the key, so its path may only add to base_url:
	// "@host" or ".host" would move it to another host, and dot segments or
	// from_origin would leave a base path prefix. Checked again here on the
	// service's own base URL, for specs saved before these rules.
	if msg := h.PathProblem(baseURL); msg != "" {
		return nil, errors.New(msg)
	}
	target := strings.TrimRight(baseURL, "/") + h.Path
	if h.FromOrigin {
		target = base.Scheme + "://" + base.Host + h.Path
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.User != nil || !strings.EqualFold(req.URL.Host, base.Host) {
		return nil, fmt.Errorf("the health check would leave base_url's host")
	}
	s.Auth.Apply(req, q, secret)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed any
	_ = json.Unmarshal(body, &parsed)
	parse := func() any { return parsed }
	low := strings.ToLower(string(body))

	if h.OKAnyway != nil && h.OKAnyway.matchesAny(resp.StatusCode, resp.Header, body, low, parse) {
		return &CheckResult{HTTPStatus: resp.StatusCode, Valid: true, Message: "key works, but quota cannot be read with it"}, nil
	}
	if h.Invalid != nil && h.Invalid.matchesAny(resp.StatusCode, resp.Header, body, low, parse) {
		return &CheckResult{HTTPStatus: resp.StatusCode, Valid: false, Message: errorMessage(body, parsed)}, nil
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return &CheckResult{HTTPStatus: resp.StatusCode, Valid: false, Message: errorMessage(body, parsed)}, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("health check returned %d: %s", resp.StatusCode, errorMessage(body, parsed))
	}
	r := &CheckResult{Valid: true, HTTPStatus: resp.StatusCode}
	if qp := h.Quota; qp != nil {
		limit, hasLimit := sumPaths(parsed, qp.Limit)
		remaining, hasRemaining := sumPaths(parsed, qp.Remaining)
		used, hasUsed := sumPaths(parsed, qp.Used)
		if hasLimit {
			r.QuotaLimit = &limit
		}
		if !hasRemaining && hasLimit && hasUsed {
			remaining, hasRemaining = max(limit-used, 0), true
		}
		if hasRemaining {
			r.QuotaRemaining = &remaining
		}
		if qp.ResetAt != "" {
			if v, ok := JSONPath(parsed, qp.ResetAt); ok {
				r.ResetAt = parseTime(v, qp.ResetFormat)
			}
		}
		if w := qp.Window; w != nil {
			lv, ok1 := JSONPath(parsed, w.Limit)
			uv, ok2 := JSONPath(parsed, w.Used)
			l, ok3 := toFloat(lv)
			u, ok4 := toFloat(uv)
			if ok1 && ok2 && ok3 && ok4 && l > 0 && u >= l {
				var next time.Time
				switch w.Period {
				case "minute":
					next = time.Now().Truncate(time.Minute).Add(time.Minute)
				case "day":
					y, m, d := time.Now().UTC().Date()
					next = time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
				default:
					next = time.Now().Truncate(time.Hour).Add(time.Hour)
				}
				r.CooldownUntil = &next
				r.Message = w.Period + "ly cap reached"
			}
		}
	}
	return r, nil
}

// matchesAny is like matches but allows 2xx responses when no status is given,
// which suits health checks that report problems in a 200 body.
func (m Match) matchesAny(status int, h http.Header, body []byte, low string, parse func() any) bool {
	if len(m.Status) == 0 {
		mm := m
		mm.Status = []int{status}
		return mm.matches(status, h, body, low, parse)
	}
	return m.matches(status, h, body, low, parse)
}

// Value is the credential exactly as Apply sends it: the secret with the
// spec's prefix, or "Bearer " for bearer auth without a prefix.
func (a Auth) Value(secret string) string {
	if a.Type == "bearer" && a.Prefix == "" {
		return "Bearer " + secret
	}
	return a.Prefix + secret
}

// Apply puts the secret where the provider expects it.
func (a Auth) Apply(req *http.Request, q url.Values, secret string) {
	switch a.Type {
	case "query":
		q.Set(a.Name, a.Prefix+secret)
	case "bearer":
		name := a.Name
		if name == "" {
			name = "Authorization"
		}
		prefix := a.Prefix
		if prefix == "" {
			prefix = "Bearer "
		}
		req.Header.Set(name, prefix+secret)
	default:
		req.Header.Set(a.Name, a.Prefix+secret)
	}
}

// ---- fixture tests ----

type TestResult struct {
	Name   string `json:"name"`
	Expect string `json:"expect"`
	Got    string `json:"got"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type TestReport struct {
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Results []TestResult `json:"results"`
}

func (s *Spec) RunTests() TestReport {
	rep := TestReport{Results: []TestResult{}}
	for _, t := range s.Tests {
		h := http.Header{}
		for k, v := range t.Headers {
			h.Set(k, v)
		}
		res, rule := s.ClassifyExplain(t.Status, h, []byte(t.Body))
		tr := TestResult{Name: t.Name, Expect: string(t.Expect), Got: string(res.Outcome), OK: res.Outcome == t.Expect}
		if rule != "" {
			tr.Detail = "matched rule: " + rule
		} else {
			tr.Detail = "no rule matched"
		}
		if res.RetryAfter > 0 {
			tr.Detail += fmt.Sprintf(", retry after %s", res.RetryAfter.Round(time.Second))
		}
		if tr.OK {
			rep.Passed++
		} else {
			rep.Failed++
		}
		rep.Results = append(rep.Results, tr)
	}
	return rep
}

// ---- helpers ----

// JSONPath reads a dot path such as "$.error.message" or "items.0.id".
func JSONPath(v any, path string) (any, bool) {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	if path == "" {
		return v, v != nil
	}
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			nv, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = nv
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

func sumPaths(v any, paths []string) (int64, bool) {
	var total float64
	found := false
	for _, p := range paths {
		if x, ok := JSONPath(v, p); ok {
			if f, ok := toFloat(x); ok {
				total += f
				found = true
			}
		}
	}
	return int64(math.Round(total)), found
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case bool, nil:
		return 0, false
	}
	return 0, false
}

func parseTime(v any, format string) *time.Time {
	var t time.Time
	switch format {
	case "iso8601":
		s, ok := v.(string)
		if !ok {
			return nil
		}
		// Providers send either a full timestamp or a plain date (2026-11-23).
		p, err := time.Parse(time.RFC3339, s)
		if err != nil {
			if p, err = time.Parse("2006-01-02", s); err != nil {
				return nil
			}
		}
		t = p
	default:
		f, ok := toFloat(v)
		if !ok || f <= 0 {
			return nil
		}
		if format == "unix_ms" {
			f /= 1000
		}
		t = time.Unix(int64(f), 0)
	}
	t = t.UTC()
	return &t
}

func parseSeconds(s string) (time.Duration, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || !(f >= 0) {
		return 0, fmt.Errorf("not seconds")
	}
	return ClampSeconds(f), nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var regexCache sync.Map

func cachedRegex(p string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(p); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile("(?i)" + p)
	if err != nil {
		return nil, err
	}
	regexCache.Store(p, re)
	return re, nil
}

// DecodeSpec parses and normalizes a spec.
func DecodeSpec(raw []byte) (*Spec, error) {
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	s.Normalize()
	return &s, nil
}

// Normalize fills defaults so the rest of the system can rely on them.
func (s *Spec) Normalize() {
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if s.Defaults.Strategy == "" {
		s.Defaults.Strategy = "round_robin"
	}
	if s.Defaults.CooldownSec == 0 {
		s.Defaults.CooldownSec = 60
	}
	if s.Defaults.MaxAttempts == 0 {
		s.Defaults.MaxAttempts = 5
	}
	if s.Auth.Type == "bearer" && s.Auth.Name == "" {
		s.Auth.Name = "Authorization"
	}
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	if s.Examples == nil {
		s.Examples = []Example{}
	}
	if s.Tests == nil {
		s.Tests = []Fixture{}
	}
	if s.Health != nil && s.Health.Method == "" {
		s.Health.Method = "GET"
	}
}

// ---- registry ----

// The registry is replaced wholesale on every change, so readers never lock
// and in-flight requests keep the spec they started with.
var registry sync.Map // slug -> *Spec

// Set installs or replaces a provider spec. Safe while serving traffic.
func Set(slug string, s *Spec) {
	registry.Store(slug, s)
	version.Add(1)
}

// version counts registry changes, so caches built from specs (the MCP
// tool catalog) know when to rebuild.
var version atomic.Int64

// Version is the registry's change counter.
func Version() int64 { return version.Load() }

// Remove drops a provider from the registry.
func Remove(slug string) {
	registry.Delete(slug)
	version.Add(1)
}

// Get returns the spec for a provider slug, or a generic spec with standard
// rules when the slug is unknown.
func Get(slug string) *Spec {
	if v, ok := registry.Load(slug); ok {
		return v.(*Spec)
	}
	return genericFallback
}

// Lookup returns the spec for a provider slug and whether it is known.
func Lookup(slug string) (*Spec, bool) {
	if v, ok := registry.Load(slug); ok {
		return v.(*Spec), true
	}
	return nil, false
}

var genericFallback = &Spec{UseStandardRules: true, Auth: Auth{Type: "header", Name: "Authorization"}}
