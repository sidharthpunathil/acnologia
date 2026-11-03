// Package refresh keeps providers' offers (free tiers, prices, published rate
// limits) current. On a schedule the owner sets, or by hand, it asks the
// refresh model to re-read each provider's pricing pages, compares what it
// found with the current offers, and either publishes a new provider version
// or parks the change for review. It only ever changes offers: never auth,
// the base URL or classification rules.
package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// metaKey is the meta table key holding the saved Settings.
const metaKey = "provider_refresh"

// Schedules.
const (
	ScheduleOff      = "off"
	ScheduleWeekly   = "weekly"
	ScheduleBiweekly = "biweekly"
	ScheduleMonthly  = "monthly"
	ScheduleDays     = "days"
)

// Scopes.
const (
	ScopeWithKeys     = "with_keys"
	ScopeWithServices = "with_services"
	ScopeAll          = "all"
)

// Apply modes.
const (
	ApplyAuto   = "auto"
	ApplyReview = "review"
)

// Settings is how and when providers are refreshed.
type Settings struct {
	// Schedule is off, weekly, biweekly, monthly or days (every EveryDays).
	Schedule  string `json:"schedule"`
	EveryDays int    `json:"every_days"`
	// Hour is the UTC hour a scheduled run starts at.
	Hour int `json:"hour"`
	// Scope picks the providers: with_keys, with_services or all. Include
	// adds providers whatever the scope; Exclude removes them, and wins.
	Scope   string   `json:"scope"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
	// IncludePeer also checks providers copied from another server.
	IncludePeer bool `json:"include_peer"`
	// ClaudePrices also re-reads Anthropic's own model prices, the table
	// Acnologia's AI cost accounting uses.
	ClaudePrices bool `json:"claude_prices"`
	// Apply is auto (publish when the guard allows) or review.
	Apply string `json:"apply"`
	// Budget caps, USD, on the refresh model's spend.
	MaxRunUSD   float64 `json:"max_run_usd"`
	MaxMonthUSD float64 `json:"max_month_usd"`
	// MaxProviders caps the providers one run checks.
	MaxProviders int `json:"max_providers"`
	// StaleDays skips providers checked within this many days.
	StaleDays int        `json:"stale_days"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
}

// Defaults is a monthly run at 03:00 UTC over the providers you hold keys
// for, at most 10 of them, skipping any checked in the last 25 days, applying
// sourced offers changes on its own, also re-reading Claude's own prices,
// capped at $2 a run and $5 a month.
func Defaults() Settings {
	return Settings{Schedule: ScheduleMonthly, EveryDays: 30, Hour: 3, Scope: ScopeWithKeys, Include: []string{}, Exclude: []string{},
		ClaudePrices: true, Apply: ApplyAuto, MaxRunUSD: 2, MaxMonthUSD: 5, MaxProviders: 10, StaleDays: 25}
}

// ErrInvalid marks settings that failed validation.
var ErrInvalid = errors.New("invalid refresh settings")

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// Normalize validates the settings and tidies the slug lists.
func (s *Settings) Normalize() error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }
	switch s.Schedule {
	case ScheduleOff, ScheduleWeekly, ScheduleBiweekly, ScheduleMonthly:
	case ScheduleDays:
		if s.EveryDays < 1 || s.EveryDays > 365 {
			return bad("every_days must be 1 to 365")
		}
	default:
		return bad("schedule must be off, weekly, biweekly, monthly or days")
	}
	if s.EveryDays == 0 {
		s.EveryDays = 30
	}
	if s.EveryDays < 1 || s.EveryDays > 365 {
		return bad("every_days must be 1 to 365")
	}
	if s.Hour < 0 || s.Hour > 23 {
		return bad("hour must be 0 to 23 (UTC)")
	}
	switch s.Scope {
	case ScopeWithKeys, ScopeWithServices, ScopeAll:
	default:
		return bad("scope must be with_keys, with_services or all")
	}
	switch s.Apply {
	case ApplyAuto, ApplyReview:
	default:
		return bad("apply must be auto or review")
	}
	usd := func(v, lo, hi float64, name string) error {
		if math.IsNaN(v) || v < lo || v > hi {
			return bad("%s must be %g to %g USD", name, lo, hi)
		}
		return nil
	}
	if err := usd(s.MaxRunUSD, 0.01, 100, "max_run_usd"); err != nil {
		return err
	}
	if err := usd(s.MaxMonthUSD, 0.01, 1000, "max_month_usd"); err != nil {
		return err
	}
	if s.MaxProviders < 1 || s.MaxProviders > 100 {
		return bad("max_providers must be 1 to 100")
	}
	if s.StaleDays < 0 || s.StaleDays > 365 {
		return bad("stale_days must be 0 to 365")
	}
	var err error
	if s.Include, err = slugList(s.Include, "include"); err != nil {
		return err
	}
	if s.Exclude, err = slugList(s.Exclude, "exclude"); err != nil {
		return err
	}
	return nil
}

func slugList(in []string, name string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		if !slugRe.MatchString(v) {
			return nil, fmt.Errorf("%w: %s: %q is not a provider slug", ErrInvalid, name, v)
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) > 200 {
		return nil, fmt.Errorf("%w: %s: at most 200 providers", ErrInvalid, name)
	}
	sort.Strings(out)
	return out, nil
}

// settingsCache is the saved settings, read once and replaced on save, so
// the worker's tick does not hit the database for them.
type settingsCache struct {
	mu sync.RWMutex
	s  *Settings
}

// Settings is the saved settings, or the defaults.
func (r *Refresher) Settings(ctx context.Context) Settings {
	r.cache.mu.RLock()
	c := r.cache.s
	r.cache.mu.RUnlock()
	if c != nil {
		return clone(*c)
	}
	s := Defaults()
	if raw := r.Store.GetMeta(ctx, metaKey); raw != "" {
		saved := Defaults()
		if err := json.Unmarshal([]byte(raw), &saved); err == nil && saved.Normalize() == nil {
			s = saved
		} else {
			slog.Warn("saved provider refresh settings are unreadable, using the defaults")
		}
	}
	r.cache.mu.Lock()
	if r.cache.s == nil {
		r.cache.s = &s
	}
	s = *r.cache.s
	r.cache.mu.Unlock()
	return clone(s)
}

// SaveSettings validates and saves the settings and returns the old ones.
func (r *Refresher) SaveSettings(ctx context.Context, in Settings, by string) (old, saved Settings, err error) {
	old = r.Settings(ctx)
	if err := in.Normalize(); err != nil {
		return old, old, err
	}
	t := r.now()
	in.UpdatedAt, in.UpdatedBy = &t, by
	raw, _ := json.Marshal(in)
	if err := r.Store.SetMeta(ctx, metaKey, string(raw)); err != nil {
		return old, old, err
	}
	r.cache.mu.Lock()
	r.cache.s = &in
	r.cache.mu.Unlock()
	return old, clone(in), nil
}

func clone(s Settings) Settings {
	s.Include = append([]string{}, s.Include...)
	s.Exclude = append([]string{}, s.Exclude...)
	return s
}

// Diff describes what changed between two settings for the audit log, or ""
// when nothing did.
func (s Settings) Diff(next Settings) string {
	parts := []string{}
	add := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			parts = append(parts, fmt.Sprintf("%s: %v -> %v", name, a, b))
		}
	}
	add("schedule", s.Schedule, next.Schedule)
	if next.Schedule == ScheduleDays || s.Schedule == ScheduleDays {
		add("every_days", s.EveryDays, next.EveryDays)
	}
	add("hour", s.Hour, next.Hour)
	add("scope", s.Scope, next.Scope)
	add("include", s.Include, next.Include)
	add("exclude", s.Exclude, next.Exclude)
	add("include_peer", s.IncludePeer, next.IncludePeer)
	add("claude_prices", s.ClaudePrices, next.ClaudePrices)
	add("apply", s.Apply, next.Apply)
	add("max_run_usd", s.MaxRunUSD, next.MaxRunUSD)
	add("max_month_usd", s.MaxMonthUSD, next.MaxMonthUSD)
	add("max_providers", s.MaxProviders, next.MaxProviders)
	add("stale_days", s.StaleDays, next.StaleDays)
	return strings.Join(parts, "; ")
}
