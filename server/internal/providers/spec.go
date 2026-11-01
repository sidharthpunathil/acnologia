package providers

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Spec is a provider described as data. The engine interprets it; nothing in
// a spec is executed, so specs written by an AI agent are safe to load.
type Spec struct {
	BaseURL          string    `json:"base_url"`
	Auth             Auth      `json:"auth"`
	Defaults         Defaults  `json:"defaults"`
	Quota            QuotaInfo `json:"quota"`
	Rules            []Rule    `json:"rules"`
	UseStandardRules bool      `json:"use_standard_rules"`
	Health           *Health   `json:"health"`
	Examples         []Example `json:"examples"`
	Tests            []Fixture `json:"tests"`
}

type Auth struct {
	Type   string `json:"type"` // query | header | bearer
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

type Defaults struct {
	Strategy        string `json:"strategy"`
	CooldownSec     int    `json:"cooldown_sec"`
	MaxAttempts     int    `json:"max_attempts"`
	MaxConcurrency  int    `json:"max_concurrency"`
	QueueTimeoutSec int    `json:"queue_timeout_sec"`
	// CacheTTLSec caches successful GET responses for this long; 0 is off.
	CacheTTLSec int `json:"cache_ttl_sec,omitempty"`
	// Circuit breaker: after BreakerThreshold provider errors (5xx, network)
	// within BreakerWindowSec, stop sending traffic for BreakerResetSec.
	// 0 uses the defaults (10 in 60s, 30s); a negative threshold turns it off.
	BreakerThreshold int `json:"breaker_threshold,omitempty"`
	BreakerWindowSec int `json:"breaker_window_sec,omitempty"`
	BreakerResetSec  int `json:"breaker_reset_sec,omitempty"`
}

type QuotaInfo struct {
	Unit             string `json:"unit"`
	CountsPerRequest bool   `json:"counts_per_request"`
	// Headers reads quota from every response, so keys stay current without
	// health checks.
	Headers *QuotaHeaders `json:"headers,omitempty"`
	// CostPerRequest is the USD price of one successful request, used for
	// spend tracking and app budgets.
	CostPerRequest float64 `json:"cost_per_request,omitempty"`
	// FreeTier describes the provider's free allowance, for the catalog.
	FreeTier string `json:"free_tier,omitempty"`
}

// QuotaHeaders names the response headers that carry quota. Some providers
// send several windows in one header ("1, 1999" for per second and per
// month); Index picks the entry to use.
type QuotaHeaders struct {
	Limit       string `json:"limit,omitempty"`
	Remaining   string `json:"remaining,omitempty"`
	Reset       string `json:"reset,omitempty"`
	ResetFormat string `json:"reset_format,omitempty"` // seconds | unix | unix_ms | iso8601 | http_date
	Index       int    `json:"index,omitempty"`
}

// Rule maps an upstream response to an outcome. Rules are tried in order.
type Rule struct {
	Name       string      `json:"name"`
	Match      Match       `json:"match"`
	Outcome    Outcome     `json:"outcome"`
	RetryAfter *RetryAfter `json:"retry_after,omitempty"`
}

// Match fields are ANDed together; the values inside each list are ORed.
type Match struct {
	Status        []int    `json:"status,omitempty"`
	BodyContains  []string `json:"body_contains,omitempty"`
	BodyRegex     string   `json:"body_regex,omitempty"`
	JSONPath      string   `json:"json_path,omitempty"`
	JSONEquals    string   `json:"json_equals,omitempty"`
	HeaderPresent string   `json:"header_present,omitempty"`
}

type RetryAfter struct {
	Source string `json:"source"` // header | fixed | next_hour | body_json | none
	Value  string `json:"value,omitempty"`
}

type Health struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Query    map[string]string `json:"query,omitempty"`
	Invalid  *Match            `json:"invalid,omitempty"`
	OKAnyway *Match            `json:"ok_anyway,omitempty"`
	Quota    *QuotaPaths       `json:"quota,omitempty"`
}

type QuotaPaths struct {
	Limit       []string     `json:"limit,omitempty"`
	Remaining   []string     `json:"remaining,omitempty"`
	Used        []string     `json:"used,omitempty"`
	ResetAt     string       `json:"reset_at,omitempty"`
	ResetFormat string       `json:"reset_format,omitempty"` // unix | unix_ms | iso8601
	Window      *QuotaWindow `json:"window,omitempty"`
}

// QuotaWindow detects a short window cap (for example searches per hour).
type QuotaWindow struct {
	Limit  string `json:"limit"`
	Used   string `json:"used"`
	Period string `json:"period"` // minute | hour | day
}

type Example struct {
	Name   string            `json:"name"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   any               `json:"body,omitempty"`
	Safe   bool              `json:"safe"`
}

type Fixture struct {
	Name    string            `json:"name"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
	Expect  Outcome           `json:"expect"`
}

var strategies = map[string]bool{"round_robin": true, "fill_first": true, "least_used": true, "most_quota": true, "random": true}

// Validate returns human readable problems; empty means the spec is usable.
func (s *Spec) Validate() []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if u, err := url.Parse(s.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("base_url must be an http(s) URL")
	}
	switch s.Auth.Type {
	case "query", "header":
		if strings.TrimSpace(s.Auth.Name) == "" {
			add("auth.name is required for %s auth", s.Auth.Type)
		}
	case "bearer":
	default:
		add("auth.type must be query, header or bearer")
	}
	d := s.Defaults
	if d.Strategy != "" && !strategies[d.Strategy] {
		add("defaults.strategy %q is unknown", d.Strategy)
	}
	if d.CooldownSec < 0 || d.CooldownSec > 86400 {
		add("defaults.cooldown_sec must be 0 to 86400")
	}
	if d.MaxAttempts < 0 || d.MaxAttempts > 20 {
		add("defaults.max_attempts must be 0 to 20")
	}
	if d.MaxConcurrency < 0 || d.MaxConcurrency > 1000 {
		add("defaults.max_concurrency must be 0 to 1000")
	}
	if d.QueueTimeoutSec < 0 || d.QueueTimeoutSec > 300 {
		add("defaults.queue_timeout_sec must be 0 to 300")
	}
	if d.CacheTTLSec < 0 || d.CacheTTLSec > 86400 {
		add("defaults.cache_ttl_sec must be 0 to 86400")
	}
	if d.BreakerThreshold > 1000 {
		add("defaults.breaker_threshold must be at most 1000")
	}
	if d.BreakerWindowSec < 0 || d.BreakerWindowSec > 3600 {
		add("defaults.breaker_window_sec must be 0 to 3600")
	}
	if d.BreakerResetSec < 0 || d.BreakerResetSec > 3600 {
		add("defaults.breaker_reset_sec must be 0 to 3600")
	}
	if s.Quota.CostPerRequest < 0 || s.Quota.CostPerRequest > 100 {
		add("quota.cost_per_request must be 0 to 100 USD")
	}
	if len(s.Quota.FreeTier) > 200 {
		add("quota.free_tier must be at most 200 characters")
	}
	if qh := s.Quota.Headers; qh != nil {
		if qh.Limit == "" && qh.Remaining == "" && qh.Reset == "" {
			add("quota.headers needs at least one of limit, remaining or reset")
		}
		switch qh.ResetFormat {
		case "", "seconds", "unix", "unix_ms", "iso8601", "http_date":
		default:
			add("quota.headers.reset_format must be seconds, unix, unix_ms, iso8601 or http_date")
		}
		if qh.Index < 0 || qh.Index > 5 {
			add("quota.headers.index must be 0 to 5")
		}
	}
	for i, r := range s.Rules {
		label := fmt.Sprintf("rules[%d] %q", i, r.Name)
		if _, ok := ParseOutcome(string(r.Outcome)); !ok {
			add("%s: outcome must be ok, rate_limited, busy, quota_exhausted, invalid, banned or error", label)
		}
		if r.Match.empty() {
			add("%s: match needs at least one condition", label)
		}
		errs = append(errs, r.Match.validate(label)...)
		if r.RetryAfter != nil {
			switch r.RetryAfter.Source {
			case "header", "next_hour", "none", "":
			case "fixed":
				if _, err := parseSeconds(r.RetryAfter.Value); err != nil {
					add("%s: retry_after.value must be seconds for source fixed", label)
				}
			case "body_json":
				if r.RetryAfter.Value == "" {
					add("%s: retry_after.value must be a JSON path for source body_json", label)
				}
			default:
				add("%s: retry_after.source must be header, fixed, next_hour, body_json or none", label)
			}
		}
	}
	if h := s.Health; h != nil {
		if h.Method == "" {
			h.Method = "GET"
		}
		if h.Method != "GET" && h.Method != "POST" {
			add("health.method must be GET or POST")
		}
		if !strings.HasPrefix(h.Path, "/") {
			add("health.path must start with /")
		}
		if h.Invalid != nil {
			errs = append(errs, h.Invalid.validate("health.invalid")...)
		}
		if h.OKAnyway != nil {
			errs = append(errs, h.OKAnyway.validate("health.ok_anyway")...)
		}
		if q := h.Quota; q != nil {
			switch q.ResetFormat {
			case "", "unix", "unix_ms", "iso8601":
			default:
				add("health.quota.reset_format must be unix, unix_ms or iso8601")
			}
			if q.Window != nil && q.Window.Period != "minute" && q.Window.Period != "hour" && q.Window.Period != "day" {
				add("health.quota.window.period must be minute, hour or day")
			}
		}
	}
	for i, e := range s.Examples {
		if !strings.HasPrefix(e.Path, "/") {
			add("examples[%d]: path must start with /", i)
		}
		switch strings.ToUpper(e.Method) {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			add("examples[%d]: method %q is not supported", i, e.Method)
		}
	}
	for i, t := range s.Tests {
		if t.Status < 100 || t.Status > 599 {
			add("tests[%d] %q: status must be an HTTP status code", i, t.Name)
		}
		if _, ok := ParseOutcome(string(t.Expect)); !ok {
			add("tests[%d] %q: expect must be an outcome", i, t.Name)
		}
	}
	return errs
}

func (m Match) empty() bool {
	return len(m.Status) == 0 && len(m.BodyContains) == 0 && m.BodyRegex == "" && m.JSONPath == "" && m.HeaderPresent == ""
}

func (m Match) validate(label string) []string {
	var errs []string
	if m.BodyRegex != "" {
		if _, err := regexp.Compile(m.BodyRegex); err != nil {
			errs = append(errs, fmt.Sprintf("%s: body_regex does not compile: %v", label, err))
		}
	}
	if m.JSONEquals != "" && m.JSONPath == "" {
		errs = append(errs, fmt.Sprintf("%s: json_equals needs json_path", label))
	}
	for _, st := range m.Status {
		if st < 100 || st > 599 {
			errs = append(errs, fmt.Sprintf("%s: status %d is not an HTTP status", label, st))
		}
	}
	return errs
}
