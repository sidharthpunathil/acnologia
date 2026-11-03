package store

import (
	"fmt"
	"time"
)

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
	Banned    int `json:"banned"`
	Disabled  int `json:"disabled"`
	Backup    int `json:"backup"`
	Busy      int `json:"busy"`
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
	// MaxConcurrency is the default number of in-flight requests per key; 0 means unlimited.
	MaxConcurrency int `json:"max_concurrency"`
	// QueueTimeoutSec is how long a proxied request waits for a free key when all are busy.
	QueueTimeoutSec int `json:"queue_timeout_sec"`
	InFlight        int `json:"in_flight"`
	// LogoURL is the service's own logo, else its provider's, else null.
	LogoURL *string `json:"logo_url"`
	HasLogo bool    `json:"-"`
	LogoRev int     `json:"-"`
	// TargetTrusted is false after someone other than a superadmin set the
	// base URL or auth placement. Hidden keys are never sent to such a target.
	TargetTrusted bool `json:"target_trusted"`
	// EgressID routes this service's traffic through an outbound proxy.
	EgressID *int64 `json:"egress_id"`
	// CacheTTLSec overrides the provider's response cache TTL; nil uses the spec.
	CacheTTLSec *int64 `json:"cache_ttl_sec"`
	// CostPerRequest overrides the provider's USD price per request; nil uses the spec.
	CostPerRequest *float64 `json:"cost_per_request"`
	// PeerLinkID is set on a service made for another server's share; it
	// proxies through that server. PeerName names that server.
	PeerLinkID     *int64 `json:"peer_link_id"`
	PeerName       string `json:"peer_name,omitempty"`
	PeerRemoteSlug string `json:"peer_remote_slug,omitempty"`
	// PeerProvider is the owner's provider slug for a peer service. Provider
	// stays acnologia-peer (it classifies the share token's answers); this
	// one names the provider for display, docs, examples and MCP tools.
	PeerProvider string `json:"peer_provider,omitempty"`
	// PeerTrusted is the link's private network flag. Without it the owner
	// controls where this service connects, so connections go to public
	// addresses only (checked when dialing, not once at sync).
	PeerTrusted bool `json:"-"`
}

// PublicOnly reports whether connections for this service must go to public
// addresses only: it belongs to another server's share on a link that is
// not on a private network.
func (s *Service) PublicOnly() bool { return s.PeerLinkID != nil && !s.PeerTrusted }

// DocProvider is the provider whose name, logo, docs and examples describe
// the service: the owner's provider for a peer service, else its own.
func (s *Service) DocProvider() string {
	if s.PeerProvider != "" {
		return s.PeerProvider
	}
	return s.Provider
}

const (
	StatusActive    = "active"
	StatusDisabled  = "disabled"
	StatusExhausted = "exhausted"
	StatusInvalid   = "invalid"
	// StatusBanned marks a key whose account the provider suspended. Only an
	// operator reset brings it back.
	StatusBanned  = "banned"
	StatusCooling = "cooling"
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
	// Account says who the key belongs to: the provider account's holder name,
	// email, phone, or any other identifier (Ref). All optional.
	AccountName  string    `json:"account_name"`
	AccountEmail string    `json:"account_email"`
	AccountPhone string    `json:"account_phone"`
	AccountRef   string    `json:"account_ref"`
	CreatedAt    time.Time `json:"created_at"`
	// MaxConcurrency overrides the service default when set; 0 means unlimited.
	MaxConcurrency *int64 `json:"max_concurrency"`
	InFlight       int    `json:"in_flight"`
	// LastOKAt is the last time the key served a successful request or passed a health check.
	LastOKAt *time.Time `json:"last_ok_at"`
	// Validity summarizes whether the key works: valid, invalid or unknown.
	Validity       string `json:"validity"`
	ValidityReason string `json:"validity_reason"`
	// QuotaUnit comes from the provider spec, for example searches or characters.
	QuotaUnit string `json:"quota_unit"`
	// EgressID routes this key's traffic through an outbound proxy,
	// overriding the service's.
	EgressID *int64 `json:"egress_id"`
	// PeerLinkID is set on a key another server handed over (handover.go):
	// the link it came through. PeerValidUntil is when it is deleted unless
	// the owner renews it; PeerRemoteKeyID is the owner's id for it.
	PeerLinkID      *int64     `json:"peer_link_id"`
	PeerValidUntil  *time.Time `json:"peer_valid_until"`
	PeerRemoteKeyID *int64     `json:"peer_remote_key_id"`
	// Handovers lists the other servers this key was handed over to, on the
	// owner. Filled by the key list handlers.
	Handovers []KeyHandover `json:"handed_over,omitempty"`
	// RevealedTo lists the people this key was shown to by a reveal share,
	// on the owner. Filled by the key list handlers. Revoke cannot take a
	// revealed key back.
	RevealedTo []KeyReveal `json:"revealed_to,omitempty"`

	SecretEnc []byte `json:"-"`
}

// IsHandedOver reports whether another server handed this key over. Such a
// key is never revealed, leased, shared onward or exported.
func (k *Key) IsHandedOver() bool { return k.PeerLinkID != nil }

// ConcurrencyLimit is the effective in-flight cap for the key; 0 means unlimited.
func (k *Key) ConcurrencyLimit(svc *Service) int {
	if k.MaxConcurrency != nil {
		return int(*k.MaxConcurrency)
	}
	return svc.MaxConcurrency
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
	switch {
	case k.Status == StatusInvalid:
		k.Validity, k.ValidityReason = "invalid", k.LastError
		if k.ValidityReason == "" {
			k.ValidityReason = "the provider rejected this key"
		}
	case k.Status == StatusBanned:
		k.Validity, k.ValidityReason = "invalid", k.LastError
		if k.ValidityReason == "" {
			k.ValidityReason = "the provider suspended this account"
		}
	case k.LastOKAt != nil:
		k.Validity, k.ValidityReason = "valid", "last worked "+ago(at, *k.LastOKAt)
	default:
		k.Validity, k.ValidityReason = "unknown", "not checked or used yet"
	}
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
	// LeaseHidden lets the app lease hidden keys. Only a superadmin can set
	// it, and a token rotation by anyone else clears it.
	LeaseHidden bool `json:"lease_hidden"`
	// Limits caps the app's upstream requests and spend.
	Limits AppLimits `json:"limits"`
	// TokenCount is the number of the app's tokens that still work.
	TokenCount int `json:"token_count"`
	// TokenID is the token the current request authenticated with.
	TokenID int64 `json:"-"`
	// ShareID is set on an app that backs a share to another server. Such
	// apps are managed on the Sharing page, not the Apps page.
	ShareID *string `json:"share_id,omitempty"`
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

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
