package store

import "time"

type Role string

const (
	RoleSuperadmin Role = "superadmin"
	RoleAdmin      Role = "admin"
	RoleMember     Role = "member"
)

func (r Role) Valid() bool { return r == RoleSuperadmin || r == RoleAdmin || r == RoleMember }

// AtLeast reports whether r has at least the privileges of other.
func (r Role) AtLeast(other Role) bool { return rank(r) >= rank(other) }

func rank(r Role) int {
	switch r {
	case RoleSuperadmin:
		return 3
	case RoleAdmin:
		return 2
	case RoleMember:
		return 1
	}
	return 0
}

type User struct {
	ID           int64      `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         Role       `json:"role"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	PasswordHash string     `json:"-"`
}

type KeyCounts struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Cooling   int `json:"cooling"`
	Exhausted int `json:"exhausted"`
	Invalid   int `json:"invalid"`
	Disabled  int `json:"disabled"`
	Backup    int `json:"backup"`
}

type Service struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Provider    string    `json:"provider"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	BaseURL     string    `json:"base_url"`
	AuthType    string    `json:"auth_type"`
	AuthName    string    `json:"auth_name"`
	Strategy    string    `json:"strategy"`
	CooldownSec int       `json:"cooldown_sec"`
	MaxAttempts int       `json:"max_attempts"`
	Icon        string    `json:"icon"`
	CreatedAt   time.Time `json:"created_at"`
	KeyCounts   KeyCounts `json:"key_counts"`
	Requests24h int       `json:"requests_24h"`
}

const (
	StatusActive    = "active"
	StatusDisabled  = "disabled"
	StatusExhausted = "exhausted"
	StatusInvalid   = "invalid"
	StatusCooling   = "cooling"
)

type Key struct {
	ID               int64      `json:"id"`
	ServiceID        int64      `json:"service_id"`
	Label            string     `json:"label"`
	Hint             string     `json:"hint"`
	Tier             string     `json:"tier"`
	Hidden           bool       `json:"hidden"`
	Restricted       bool       `json:"restricted"`
	AppIDs           []int64    `json:"app_ids"`
	Status           string     `json:"status"`
	EffectiveStatus  string     `json:"effective_status"`
	Weight           int        `json:"weight"`
	Priority         int        `json:"priority"`
	CooldownUntil    *time.Time `json:"cooldown_until"`
	ConsecutiveFails int        `json:"consecutive_fails"`
	QuotaLimit       *int64     `json:"quota_limit"`
	QuotaRemaining   *int64     `json:"quota_remaining"`
	QuotaResetAt     *time.Time `json:"quota_reset_at"`
	UsageTotal       int64      `json:"usage_total"`
	UsageToday       int64      `json:"usage_today"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	LastError        string     `json:"last_error"`
	LastCheckedAt    *time.Time `json:"last_checked_at"`
	Notes            string     `json:"notes"`
	CreatedAt        time.Time  `json:"created_at"`

	SecretEnc []byte `json:"-"`
}

// Available reports whether the key can be handed out right now.
func (k *Key) Available(at time.Time) bool {
	if k.Status != StatusActive {
		return false
	}
	if k.CooldownUntil != nil && k.CooldownUntil.After(at) {
		return false
	}
	if k.QuotaRemaining != nil && *k.QuotaRemaining <= 0 {
		return false
	}
	return true
}

func (k *Key) computeEffective(at time.Time) {
	k.EffectiveStatus = k.Status
	if k.Status == StatusActive && k.CooldownUntil != nil && k.CooldownUntil.After(at) {
		k.EffectiveStatus = StatusCooling
	}
}

type App struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	TokenPrefix     string     `json:"token_prefix"`
	AllowedServices []string   `json:"allowed_services"`
	Mode            string     `json:"mode"`
	Active          bool       `json:"active"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at"`
}

func (a *App) CanUse(slug string) bool {
	if len(a.AllowedServices) == 0 {
		return true
	}
	for _, s := range a.AllowedServices {
		if s == slug {
			return true
		}
	}
	return false
}

func (a *App) AllowsLease() bool { return a.Mode == "lease" || a.Mode == "both" }
func (a *App) AllowsProxy() bool { return a.Mode == "proxy" || a.Mode == "both" }

type Lease struct {
	ID        string
	KeyID     int64
	AppID     int64
	ServiceID int64
	CreatedAt time.Time
	ExpiresAt time.Time
	Outcome   string
}

type Event struct {
	ID          int64     `json:"id"`
	TS          time.Time `json:"ts"`
	Kind        string    `json:"kind"`
	ServiceID   *int64    `json:"service_id"`
	ServiceSlug string    `json:"service_slug"`
	KeyID       *int64    `json:"key_id"`
	KeyLabel    string    `json:"key_label"`
	AppID       *int64    `json:"app_id"`
	AppName     string    `json:"app_name"`
	StatusCode  int       `json:"status_code"`
	LatencyMS   int64     `json:"latency_ms"`
	Detail      string    `json:"detail"`

	KeyHidden bool `json:"-"`
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	TS        time.Time `json:"ts"`
	UserEmail string    `json:"user_email"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
}
