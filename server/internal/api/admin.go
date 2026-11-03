package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/keyparse"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// ---- overview ----

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.Store.FillStats(ctx, svcs, isSuper(r), s.Pool.InFlight()); err != nil {
		writeStoreErr(w, err)
		return
	}
	var tot store.KeyCounts
	for _, sv := range svcs {
		c := sv.KeyCounts
		tot.Total += c.Total
		tot.Available += c.Available
		tot.Cooling += c.Cooling
		tot.Exhausted += c.Exhausted
		tot.Invalid += c.Invalid
		tot.Banned += c.Banned
		tot.Disabled += c.Disabled
		tot.Backup += c.Backup
		tot.Busy += c.Busy
	}
	inflight := 0
	for _, sv := range svcs {
		inflight += sv.InFlight
	}
	since := time.Now().Add(-24 * time.Hour)
	reqs, _ := s.Store.CountEvents(ctx, since, isSuper(r), "lease", "proxy")
	rls, _ := s.Store.CountEvents(ctx, since, isSuper(r), "rate_limited", "busy", "quota_exhausted")
	hits, _ := s.Store.CountEvents(ctx, since, isSuper(r), "cache_hit")
	spend, _ := s.Store.SpendSince(ctx, since, isSuper(r))
	evs, _ := s.Store.ListEvents(ctx, store.EventFilter{Limit: 15})
	writeJSON(w, 200, map[string]any{
		"services": len(svcs), "keys": map[string]int{
			"total": tot.Total, "active": tot.Available, "cooling": tot.Cooling, "exhausted": tot.Exhausted,
			"invalid": tot.Invalid, "banned": tot.Banned, "disabled": tot.Disabled, "backup": tot.Backup, "busy": tot.Busy,
		},
		"in_flight":    inflight,
		"requests_24h": reqs, "rate_limits_24h": rls, "recent_events": maskEvents(r, evs),
		"cache_hits_24h": hits, "spend_24h": spend, "cache": s.Cache.Stats(),
	})
}

// ---- services ----

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	svcs, err := s.Store.ListServices(r.Context())
	if err == nil {
		err = s.Store.FillStats(r.Context(), svcs, isSuper(r), s.Pool.InFlight())
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.fillLogos(r.Context(), svcs...)
	writeJSON(w, 200, map[string]any{"services": svcs})
}

func (s *Server) loadService(w http.ResponseWriter, r *http.Request) (*store.Service, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return nil, false
	}
	sv, err := s.Store.GetService(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return sv, true
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	if err := s.Store.FillStats(r.Context(), []*store.Service{sv}, isSuper(r), s.Pool.InFlight()); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.fillLogos(r.Context(), sv)
	writeJSON(w, 200, sv)
}

type serviceInput struct {
	Slug        *string `json:"slug"`
	Name        *string `json:"name"`
	Provider    *string `json:"provider"`
	Category    *string `json:"category"`
	Description *string `json:"description"`
	BaseURL     *string `json:"base_url"`
	AuthType    *string `json:"auth_type"`
	AuthName    *string `json:"auth_name"`
	Strategy    *string `json:"strategy"`
	CooldownSec *int    `json:"cooldown_sec"`
	MaxAttempts *int    `json:"max_attempts"`
	Icon        *string `json:"icon"`
	MaxConc     *int    `json:"max_concurrency"`
	QueueSec    *int    `json:"queue_timeout_sec"`
	// EgressID routes traffic through an egress proxy; 0 connects directly.
	EgressID *int64 `json:"egress_id"`
	// CacheTTLSec overrides the provider's cache TTL; -1 goes back to the provider default.
	CacheTTLSec *int64 `json:"cache_ttl_sec"`
	// CostPerRequest overrides the provider's USD price; -1 goes back to the provider default.
	CostPerRequest *float64 `json:"cost_per_request"`
	// TargetTrusted true is a superadmin confirming where the service sends
	// keys; false revokes that. Nothing else sets the flag to true.
	TargetTrusted *bool `json:"target_trusted"`
}

// vouchErr refuses target_trusted=true from anyone but a superadmin acting
// directly. An Assistant tool call carries the superadmin's session but the
// model's choice, so it cannot confirm a target.
func vouchErr(r *http.Request, in serviceInput) string {
	if in.TargetTrusted != nil && *in.TargetTrusted && (!isSuper(r) || fromAssistant(r)) {
		return "only a superadmin, from the service's edit form or the API, can confirm where a service sends keys"
	}
	return ""
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

func (in serviceInput) apply(sv *store.Service) string {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&sv.Slug, in.Slug)
	set(&sv.Name, in.Name)
	set(&sv.Provider, in.Provider)
	set(&sv.Category, in.Category)
	set(&sv.Description, in.Description)
	set(&sv.BaseURL, in.BaseURL)
	set(&sv.AuthType, in.AuthType)
	set(&sv.AuthName, in.AuthName)
	set(&sv.Strategy, in.Strategy)
	set(&sv.Icon, in.Icon)
	if in.CooldownSec != nil {
		sv.CooldownSec = *in.CooldownSec
	}
	if in.MaxAttempts != nil {
		sv.MaxAttempts = *in.MaxAttempts
	}
	if in.MaxConc != nil {
		sv.MaxConcurrency = *in.MaxConc
	}
	if in.QueueSec != nil {
		sv.QueueTimeoutSec = *in.QueueSec
	}
	if in.EgressID != nil {
		sv.EgressID = nil
		if *in.EgressID > 0 {
			sv.EgressID = in.EgressID
		}
	}
	if in.CacheTTLSec != nil {
		if *in.CacheTTLSec < -1 || *in.CacheTTLSec > 86400 {
			return "cache_ttl_sec must be between 0 and 86400, or -1 for the provider default"
		}
		sv.CacheTTLSec = nil
		if *in.CacheTTLSec >= 0 {
			sv.CacheTTLSec = in.CacheTTLSec
		}
	}
	if in.CostPerRequest != nil {
		if *in.CostPerRequest != -1 && (*in.CostPerRequest < 0 || *in.CostPerRequest > 100) {
			return "cost_per_request must be between 0 and 100 USD, or -1 for the provider default"
		}
		sv.CostPerRequest = nil
		if *in.CostPerRequest >= 0 {
			sv.CostPerRequest = in.CostPerRequest
		}
	}
	sv.BaseURL = strings.TrimRight(sv.BaseURL, "/")
	switch {
	case !slugRe.MatchString(sv.Slug):
		return "slug must be lowercase letters, digits and hyphens"
	case sv.Name == "":
		return "name is required"
	case sv.Provider == "":
		return "provider is required"
	case sv.AuthType != "query" && sv.AuthType != "header" && sv.AuthType != "bearer":
		return "auth_type must be query, header or bearer"
	case sv.AuthName == "" && sv.AuthType != "bearer":
		return "auth_name is required"
	case !pool.ValidStrategy(sv.Strategy):
		return "unknown strategy"
	case sv.CooldownSec < 1 || sv.CooldownSec > 86400:
		return "cooldown_sec must be between 1 and 86400"
	case sv.MaxAttempts < 1 || sv.MaxAttempts > 20:
		return "max_attempts must be between 1 and 20"
	case sv.MaxConcurrency < 0 || sv.MaxConcurrency > 1000:
		return "max_concurrency must be between 0 (unlimited) and 1000"
	case sv.QueueTimeoutSec < 0 || sv.QueueTimeoutSec > 300:
		return "queue_timeout_sec must be between 0 and 300"
	}
	u, err := url.Parse(sv.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "base_url must be an http(s) URL"
	}
	if sv.AuthType == "bearer" && sv.AuthName == "" {
		sv.AuthName = "Authorization"
	}
	return ""
}

func (s *Server) createService(w http.ResponseWriter, r *http.Request) {
	var in serviceInput
	if !decode(w, r, &in) {
		return
	}
	prov := "generic"
	if in.Provider != nil {
		prov = *in.Provider
	}
	p, err := s.Store.GetProvider(r.Context(), prov)
	if err != nil {
		writeErr(w, 400, "unknown_provider", "no provider named "+prov)
		return
	}
	sv, err := catalog.ServiceDefaults(p)
	if err != nil {
		writeErr(w, 400, "bad_provider", "provider spec is unreadable: "+err.Error())
		return
	}
	if in.Slug == nil {
		sv.Slug = catalog.UniqueServiceSlug(r.Context(), s.Store, sv.Slug)
	}
	if msg := in.apply(sv); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if !s.checkServiceRefs(w, r, sv, "") {
		return
	}
	if msg := vouchErr(r, in); msg != "" {
		writeErr(w, 403, "forbidden", msg)
		return
	}
	// A superadmin creating a service picks its target; the Assistant
	// creating one does not count as that.
	sv.TargetTrusted = isSuper(r) && !fromAssistant(r)
	if in.TargetTrusted != nil {
		sv.TargetTrusted = *in.TargetTrusted
	}
	created, err := s.Store.CreateService(r.Context(), sv)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.create", created.Slug, "")
	s.fillLogos(r.Context(), created)
	writeJSON(w, 201, created)
}

func (s *Server) updateService(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var in serviceInput
	if !decode(w, r, &in) {
		return
	}
	before, oldSlug := targetOf(sv), sv.Slug
	if msg := in.apply(sv); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if !s.checkServiceRefs(w, r, sv, oldSlug) {
		return
	}
	// A service holding keys another server handed over sends them where
	// the owner said; nobody here may point them somewhere else.
	if isHandoverService(sv) && targetOf(sv) != before {
		writeErr(w, 409, "handover_service", "this service holds keys another server handed over; its base URL, auth, provider and egress follow the owner")
		return
	}
	// Where keys are sent decides who can read them. Only an explicit
	// target_trusted=true from a superadmin (the edit form sends it) vouches
	// for the target; any change to it without that revokes trust, so hidden
	// keys stop flowing until a superadmin confirms.
	if msg := vouchErr(r, in); msg != "" {
		writeErr(w, 403, "forbidden", msg)
		return
	}
	if in.TargetTrusted != nil {
		sv.TargetTrusted = *in.TargetTrusted
	} else if targetOf(sv) != before {
		sv.TargetTrusted = false
	}
	if _, err := s.Store.GetProvider(r.Context(), sv.Provider); err != nil {
		writeErr(w, 400, "unknown_provider", "no provider named "+sv.Provider)
		return
	}
	if err := s.Store.UpdateService(r.Context(), sv); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.update", sv.Slug, "")
	// Settings decide what may be served from cache, so start fresh.
	s.Cache.PurgePrefix(servicePrefix(sv.ID))
	_ = s.Store.FillStats(r.Context(), []*store.Service{sv}, isSuper(r), s.Pool.InFlight())
	s.fillLogos(r.Context(), sv)
	writeJSON(w, 200, sv)
}

// targetOf is everything that decides where and how a key leaves the box.
func targetOf(sv *store.Service) string {
	egressID := int64(0)
	if sv.EgressID != nil {
		egressID = *sv.EgressID
	}
	return sv.BaseURL + "\x00" + sv.AuthType + "\x00" + sv.AuthName + "\x00" + sv.Provider + "\x00" + strconv.FormatInt(egressID, 10)
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	// Admins cannot see hidden keys, so they must not be able to destroy them either.
	if !isSuper(r) {
		all, _ := s.Store.ListKeys(r.Context(), sv.ID, true)
		for _, k := range all {
			if k.Hidden {
				writeErr(w, 403, "forbidden", "this service holds keys only a superadmin can remove")
				return
			}
		}
	}
	if err := s.Store.DeleteService(r.Context(), sv.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	// SQLite may hand the id to the next service: its cached answers go now.
	s.Cache.PurgePrefix(servicePrefix(sv.ID))
	s.Store.AddAudit(r.Context(), userFrom(r), "service.delete", sv.Slug, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- keys ----

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	keys, err := s.Store.ListKeys(r.Context(), sv.ID, isSuper(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.fillInFlight(keys...)
	maskAccounts(r, keys...)
	writeJSON(w, 200, map[string]any{"keys": keys})
}

func (s *Server) fillInFlight(keys ...*store.Key) {
	inf := s.Pool.InFlight()
	units := map[int64]string{}
	for _, k := range keys {
		k.InFlight = inf[k.ID]
		u, ok := units[k.ServiceID]
		if !ok {
			if sv, err := s.Store.GetService(context.Background(), k.ServiceID); err == nil {
				u = providers.Get(sv.Provider).Quota.Unit
			}
			units[k.ServiceID] = u
		}
		k.QuotaUnit = u
	}
	s.fillHandovers(context.Background(), keys...)
}

type keyInput struct {
	Label      *string  `json:"label"`
	Secret     *string  `json:"secret"`
	Tier       *string  `json:"tier"`
	Hidden     *bool    `json:"hidden"`
	Restricted *bool    `json:"restricted"`
	AppIDs     *[]int64 `json:"app_ids"`
	Status     *string  `json:"status"`
	Weight     *int     `json:"weight"`
	Priority   *int     `json:"priority"`
	Notes      *string  `json:"notes"`
	QuotaLimit *int64   `json:"quota_limit"`
	// MaxConc overrides the service concurrency limit; -1 clears the override.
	MaxConc *int64 `json:"max_concurrency"`
	// EgressID sends this key's traffic through an egress proxy; 0 falls back to the service's.
	EgressID *int64 `json:"egress_id"`
	// Who the key belongs to. Personal data: shown to admins and up only.
	AccountName  *string `json:"account_name"`
	AccountEmail *string `json:"account_email"`
	AccountPhone *string `json:"account_phone"`
	AccountRef   *string `json:"account_ref"`
}

// accountPatch trims the account fields that were sent.
func (in keyInput) accountPatch() store.AccountPatch {
	t := func(v *string) *string {
		if v == nil {
			return nil
		}
		x := strings.TrimSpace(*v)
		return &x
	}
	return store.AccountPatch{Name: t(in.AccountName), Email: t(in.AccountEmail), Phone: t(in.AccountPhone), Ref: t(in.AccountRef)}
}

// validAccount checks lengths and the email shape.
func (in keyInput) validAccount() string {
	p := in.accountPatch()
	for _, f := range []struct {
		v   *string
		max int
		n   string
	}{{p.Name, 200, "account_name"}, {p.Email, 254, "account_email"}, {p.Phone, 40, "account_phone"}, {p.Ref, 200, "account_ref"}} {
		if f.v != nil && len(*f.v) > f.max {
			return fmt.Sprintf("%s must be at most %d characters", f.n, f.max)
		}
	}
	if p.Email != nil && *p.Email != "" && (!strings.Contains(*p.Email, "@") || strings.ContainsAny(*p.Email, " \t\n")) {
		return "account_email must look like name@example.com"
	}
	return ""
}

// maskAccounts hides who keys belong to from members: the account fields
// are personal data (email, phone).
func maskAccounts(r *http.Request, keys ...*store.Key) {
	if u := userFrom(r); u != nil && u.Role.AtLeast(store.RoleAdmin) {
		return
	}
	for _, k := range keys {
		k.AccountName, k.AccountEmail, k.AccountPhone, k.AccountRef = "", "", "", ""
	}
}

func (s *Server) validateKey(in keyInput, r *http.Request) (int, string) {
	if in.Hidden != nil && *in.Hidden && !isSuper(r) {
		return 403, "only a superadmin can hide keys"
	}
	if msg := in.validAccount(); msg != "" {
		return 400, msg
	}
	if in.Tier != nil && *in.Tier != "primary" && *in.Tier != "backup" {
		return 400, "tier must be primary or backup"
	}
	if in.Status != nil && *in.Status != store.StatusActive && *in.Status != store.StatusDisabled {
		return 400, "status can be set to active or disabled"
	}
	if in.Weight != nil && (*in.Weight < 1 || *in.Weight > 1000) {
		return 400, "weight must be between 1 and 1000"
	}
	if in.Secret != nil && strings.TrimSpace(*in.Secret) == "" {
		return 400, "secret cannot be empty"
	}
	if in.MaxConc != nil && (*in.MaxConc < -1 || *in.MaxConc > 1000) {
		return 400, "max_concurrency must be -1 (use service default), 0 (unlimited) or up to 1000"
	}
	if in.QuotaLimit != nil && *in.QuotaLimit < -1 {
		return 400, "quota_limit must be -1 (clear) or a positive number"
	}
	if in.AppIDs != nil && len(*in.AppIDs) > 0 {
		ok, err := s.Store.AppsExist(r.Context(), *in.AppIDs)
		if err != nil {
			return 500, "could not check app_ids"
		}
		if !ok {
			return 400, "app_ids names an app that does not exist"
		}
	}
	return 0, ""
}

// maxKeysPerRequest caps one key creation call. All keys go in one
// transaction, so a failure stores none of them.
const maxKeysPerRequest = keyparse.MaxKeys

// createKeys adds keys to a service. The body is {keys: [...]} with one
// object per key, or {text: "..."} with pasted text, a CSV, a .env file or a
// JSON array, parsed by keyparse; both may be sent together.
func (s *Server) createKeys(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var body struct {
		Keys []keyInput `json:"keys"`
		Text string     `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	var parsed *keyparse.Result
	if strings.TrimSpace(body.Text) != "" {
		res := keyparse.Parse(body.Text)
		parsed = &res
		for _, k := range res.Keys {
			in := keyInput{Secret: &k.Secret}
			if k.Label != "" {
				in.Label = &k.Label
			}
			body.Keys = append(body.Keys, in)
		}
		if res.Overflow > 0 {
			body.Keys = append(body.Keys, make([]keyInput, res.Overflow)...) // fail the cap below
		}
	}
	if len(body.Keys) == 0 {
		msg := "send between 1 and 5000 keys"
		if parsed != nil {
			msg = "no keys found in the text"
		}
		writeErr(w, 400, "invalid", msg)
		return
	}
	if len(body.Keys) > maxKeysPerRequest {
		writeErr(w, 400, "invalid", fmt.Sprintf("send at most %d keys per request (got %d)", maxKeysPerRequest, len(body.Keys)))
		return
	}
	for i, in := range body.Keys {
		if in.Secret == nil {
			writeErr(w, 400, "invalid", fmt.Sprintf("key %d has no secret", i+1))
			return
		}
		if code, msg := s.validateKey(in, r); code != 0 {
			writeErr(w, code, "invalid", msg)
			return
		}
	}
	nks := make([]store.NewKey, 0, len(body.Keys))
	for _, in := range body.Keys {
		nk, err := s.newKey(sv, in, userFrom(r).ID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		nks = append(nks, nk)
	}
	created, dups, err := s.Store.CreateKeys(r.Context(), nks)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	skipped := []string{}
	for _, i := range dups {
		skipped = append(skipped, fmt.Sprintf("line %d (%s) already exists", i+1, nks[i].Hint))
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "key.create", sv.Slug, fmt.Sprintf("%d added, %d skipped", len(created), len(skipped)))
	// Check new keys in the background so quotas show up without a manual click.
	go s.checkMany(sv, created)
	resp := map[string]any{"keys": created, "skipped": skipped}
	if parsed != nil {
		resp["ignored"] = len(parsed.Ignored)
	}
	if !sv.TargetTrusted {
		for _, k := range created {
			if k.Hidden {
				resp["warning"] = "hidden keys are not used until a superadmin saves this service's settings, because its base URL was set by someone else"
				break
			}
		}
	}
	writeJSON(w, 201, resp)
}

// DefaultKeyLabel is the label of a key added without one.
func DefaultKeyLabel(sv *store.Service, secret string) string {
	return fmt.Sprintf("%s key %s", sv.Name, crypto.Hint(secret)[3:])
}

// newKey builds the row for one validated key input.
func (s *Server) newKey(sv *store.Service, in keyInput, by int64) (store.NewKey, error) {
	secret := strings.TrimSpace(*in.Secret)
	enc, err := s.Pool.Box.Encrypt(secret)
	if err != nil {
		return store.NewKey{}, err
	}
	nk := store.NewKey{
		ServiceID: sv.ID, SecretEnc: enc, SecretFP: s.Pool.Box.Fingerprint(secret), Hint: crypto.Hint(secret),
		Tier: "primary", Weight: 1, Priority: 100, CreatedBy: by, Label: DefaultKeyLabel(sv, secret),
	}
	if in.Label != nil && strings.TrimSpace(*in.Label) != "" {
		nk.Label = strings.TrimSpace(*in.Label)
	}
	if in.Tier != nil {
		nk.Tier = *in.Tier
	}
	if in.Hidden != nil {
		nk.Hidden = *in.Hidden
	}
	if in.Restricted != nil {
		nk.Restricted = *in.Restricted
	}
	if in.AppIDs != nil {
		nk.AppIDs = *in.AppIDs
	}
	if in.Weight != nil {
		nk.Weight = *in.Weight
	}
	if in.Priority != nil {
		nk.Priority = *in.Priority
	}
	if in.Notes != nil {
		nk.Notes = *in.Notes
	}
	deref := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	a := in.accountPatch()
	nk.Account = store.KeyAccount{Name: deref(a.Name), Email: deref(a.Email), Phone: deref(a.Phone), Ref: deref(a.Ref)}
	if in.QuotaLimit != nil && *in.QuotaLimit >= 0 {
		nk.QuotaLimit = in.QuotaLimit
	}
	if in.MaxConc != nil && *in.MaxConc >= 0 {
		nk.MaxConc = in.MaxConc
	}
	return nk, nil
}

func (s *Server) checkMany(sv *store.Service, keys []*store.Key) {
	if providers.Get(sv.Provider).Health == nil {
		return
	}
	for _, k := range keys {
		ctx, cancel := contextTimeout(20 * time.Second)
		_ = s.Pool.Check(ctx, sv, k)
		cancel()
	}
}

// loadKey fetches a key and hides hidden keys from non superadmins.
func (s *Server) loadKey(w http.ResponseWriter, r *http.Request) (*store.Key, *store.Service, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return nil, nil, false
	}
	k, err := s.Store.GetKey(r.Context(), id)
	if err == nil && k.Hidden && !isSuper(r) {
		err = store.ErrNotFound
	}
	if err != nil {
		writeStoreErr(w, err)
		return nil, nil, false
	}
	sv, err := s.Store.GetService(r.Context(), k.ServiceID)
	if err != nil {
		writeStoreErr(w, err)
		return nil, nil, false
	}
	return k, sv, true
}

func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	var in keyInput
	if !decode(w, r, &in) {
		return
	}
	if code, msg := s.validateKey(in, r); code != 0 {
		writeErr(w, code, "invalid", msg)
		return
	}
	if k.IsHandedOver() && (in.Secret != nil || in.Hidden != nil || in.EgressID != nil) {
		writeErr(w, 403, "handed_over", "this key was handed over by another server; its secret, hidden flag and egress cannot change here")
		return
	}
	p := store.KeyPatch{
		Label: in.Label, Tier: in.Tier, Hidden: in.Hidden, Restricted: in.Restricted, AppIDs: in.AppIDs,
		Status: in.Status, Weight: in.Weight, Priority: in.Priority, Notes: in.Notes, Account: in.accountPatch(),
	}
	if in.QuotaLimit != nil {
		q := in.QuotaLimit
		if *q < 0 {
			q = nil
		}
		p.QuotaLimit = &q
	}
	if in.MaxConc != nil {
		m := in.MaxConc
		if *m < 0 {
			m = nil
		}
		p.MaxConc = &m
	}
	if in.Secret != nil {
		secret := strings.TrimSpace(*in.Secret)
		enc, err := s.Pool.Box.Encrypt(secret)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		p.SecretEnc, p.SecretFP, p.Hint = enc, s.Pool.Box.Fingerprint(secret), crypto.Hint(secret)
	}
	if in.EgressID != nil {
		if *in.EgressID > 0 {
			if _, err := s.Store.GetEgress(r.Context(), *in.EgressID); err != nil {
				writeErr(w, 400, "unknown_egress", "no egress proxy with that id")
				return
			}
		}
		// A proxy sees where a key goes; hidden keys follow superadmin decisions only.
		if k.Hidden && !isSuper(r) {
			writeErr(w, 403, "forbidden", "only a superadmin can route hidden keys through a proxy")
			return
		}
	}
	if err := s.Store.UpdateKey(r.Context(), k.ID, p); err != nil {
		writeStoreErr(w, err)
		return
	}
	if in.EgressID != nil {
		var id *int64
		if *in.EgressID > 0 {
			id = in.EgressID
		}
		if err := s.Store.SetKeyEgress(r.Context(), k.ID, id); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	detail := []string{}
	if in.Secret != nil {
		detail = append(detail, "secret replaced")
	}
	if in.Hidden != nil {
		detail = append(detail, "hidden="+strconv.FormatBool(*in.Hidden))
	}
	if in.Status != nil {
		detail = append(detail, "status="+*in.Status)
	}
	if in.EgressID != nil {
		detail = append(detail, "egress="+strconv.FormatInt(*in.EgressID, 10))
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "key.update", fmt.Sprintf("%s/%s", sv.Slug, k.Label), strings.Join(detail, ", "))
	nk, err := s.Store.GetKey(r.Context(), k.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.fillInFlight(nk)
	writeJSON(w, 200, nk)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteKey(r.Context(), k.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "key.delete", fmt.Sprintf("%s/%s", sv.Slug, k.Label), k.Hint)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) revealKey(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	if sv.Provider == providers.PeerSlug {
		writeErr(w, 403, "share_token", "this key is a share token from another server and is never revealed")
		return
	}
	if k.IsHandedOver() {
		writeErr(w, 403, "handed_over", "this key was handed over by another server and is never revealed")
		return
	}
	secret, err := s.Pool.Box.Decrypt(k.SecretEnc)
	if err != nil {
		writeErr(w, 500, "decrypt_failed", "cannot decrypt, check ACNOLOGIA_MASTER_KEY")
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "key.reveal", fmt.Sprintf("%s/%s", sv.Slug, k.Label), k.Hint)
	writeJSON(w, 200, map[string]any{"secret": secret})
}

func (s *Server) checkKey(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	start := time.Now()
	err := s.Pool.Check(r.Context(), sv, k)
	if errors.Is(err, pool.ErrNoHealthCheck) {
		writeErr(w, 422, "no_health_check", err.Error())
		return
	}
	if errors.Is(err, pool.ErrUntrustedTarget) {
		writeErr(w, 409, "untrusted_target", err.Error())
		return
	}
	if errors.Is(err, pool.ErrBanned) {
		writeErr(w, 409, "key_banned", err.Error())
		return
	}
	latency := time.Since(start).Milliseconds()
	nk, gerr := s.Store.GetKey(r.Context(), k.ID)
	logged := nk
	if gerr != nil {
		logged = nil
	}
	s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "health", ServiceID: sv.ID, KeyID: k.ID,
		LatencyMS: latency, Detail: healthDetail(err, logged)})
	if gerr != nil {
		writeStoreErr(w, gerr)
		return
	}
	if err != nil {
		writeErr(w, 502, "check_failed", err.Error())
		return
	}
	s.fillInFlight(nk)
	writeJSON(w, 200, nk)
}

func (s *Server) resetKey(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	active := store.StatusActive
	if err := s.Store.UpdateKey(r.Context(), k.ID, store.KeyPatch{Status: &active}); err != nil {
		writeStoreErr(w, err)
		return
	}
	var nilq *int64
	if k.QuotaRemaining != nil && *k.QuotaRemaining <= 0 {
		_ = s.Store.ApplyEffect(r.Context(), k.ID, store.KeyEffect{QuotaRemaining: &nilq, LastError: new(string)})
	} else {
		_ = s.Store.ApplyEffect(r.Context(), k.ID, store.KeyEffect{LastError: new(string)})
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "key.reset", fmt.Sprintf("%s/%s", sv.Slug, k.Label), "")
	nk, _ := s.Store.GetKey(r.Context(), k.ID)
	writeJSON(w, 200, nk)
}

// ---- apps ----

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.Store.ListApps(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"apps": apps})
}

type appInput struct {
	Name            *string   `json:"name"`
	Description     *string   `json:"description"`
	AllowedServices *[]string `json:"allowed_services"`
	Mode            *string   `json:"mode"`
	Active          *bool     `json:"active"`
	LeaseHidden     *bool     `json:"lease_hidden"`
	// Limits replaces all four limits; a null field means no limit.
	Limits *store.AppLimits `json:"limits"`
}

func (in appInput) apply(r *http.Request, a *store.App) (int, string) {
	if l := in.Limits; l != nil {
		if (l.DailyRequests != nil && *l.DailyRequests < 0) || (l.MonthlyRequests != nil && *l.MonthlyRequests < 0) ||
			(l.DailyBudget != nil && *l.DailyBudget < 0) || (l.MonthlyBudget != nil && *l.MonthlyBudget < 0) {
			return 400, "limits must not be negative"
		}
		a.Limits = *l
	}
	if in.LeaseHidden != nil {
		// Leasing hands out the plaintext secret, so only a superadmin may let
		// an app lease hidden keys.
		if *in.LeaseHidden && !isSuper(r) {
			return 403, "only a superadmin can let an app lease hidden keys"
		}
		a.LeaseHidden = *in.LeaseHidden
	}
	if msg := in.applyFields(a); msg != "" {
		return 400, msg
	}
	return 0, ""
}

func (in appInput) applyFields(a *store.App) string {
	if in.Name != nil {
		a.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		a.Description = *in.Description
	}
	if in.AllowedServices != nil {
		a.AllowedServices = *in.AllowedServices
	}
	if in.Mode != nil {
		a.Mode = *in.Mode
	}
	if in.Active != nil {
		a.Active = *in.Active
	}
	if a.Name == "" {
		return "name is required"
	}
	if a.Mode != "proxy" && a.Mode != "lease" && a.Mode != "both" {
		return "mode must be proxy, lease or both"
	}
	return ""
}

func newAppToken() (token, prefix, hash string) {
	token = crypto.RandomToken(store.AppTokenPrefix, 32)
	return token, token[:8], crypto.SHA256(token)
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var in appInput
	if !decode(w, r, &in) {
		return
	}
	a := &store.App{Mode: "both", Active: true}
	if code, msg := in.apply(r, a); code != 0 {
		writeErr(w, code, "invalid", msg)
		return
	}
	tok, prefix, hash := newAppToken()
	a.TokenPrefix = prefix
	created, first, err := s.Store.CreateAppWithToken(r.Context(), a, "Default", store.TokenPrefixHint(prefix), hash, userID(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Limits.Any() {
		if err := s.Store.SetAppLimits(r.Context(), created.ID, a.Limits); err != nil {
			writeStoreErr(w, err)
			return
		}
		created.Limits = a.Limits
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.create", created.Name, "")
	writeJSON(w, 201, map[string]any{"app": created, "token": tok, "app_token": first})
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in appInput
	if !decode(w, r, &in) {
		return
	}
	if _, err := s.Store.GetApp(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	// Validate on a scratch app, then write only the fields sent. Writing the
	// whole row from a copy read before the body arrived could restore
	// lease_hidden after a token rotation cleared it.
	probe := &store.App{Name: "app", Mode: "both"}
	if code, msg := in.apply(r, probe); code != 0 {
		writeErr(w, code, "invalid", msg)
		return
	}
	patch := store.AppPatch{Description: in.Description, AllowedServices: in.AllowedServices,
		Mode: in.Mode, Active: in.Active, LeaseHidden: in.LeaseHidden}
	if in.Name != nil {
		patch.Name = &probe.Name
	}
	if err := s.Store.PatchApp(r.Context(), id, patch); err != nil {
		writeStoreErr(w, err)
		return
	}
	if in.Limits != nil {
		if err := s.Store.SetAppLimits(r.Context(), id, probe.Limits); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.update", a.Name, "")
	writeJSON(w, 200, a)
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err == nil {
		err = s.Store.DeleteApp(r.Context(), id)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// The id may be reused, so the app's idempotent replays go with it.
	s.Cache.PurgePrefix(idempotencyPrefix(id))
	s.Store.AddAudit(r.Context(), userFrom(r), "app.delete", a.Name, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// rotateAppToken predates multiple tokens per app. It still means "replace
// the token": it creates a new token and revokes every other one.
func (s *Server) rotateAppToken(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	tok, prefix, hash := newAppToken()
	// Whoever rotates gets the token. If that is not a superadmin, the app
	// loses access to hidden keys until a superadmin grants it again.
	t, err := s.Store.RotateAppToken(r.Context(), id, "Rotated "+time.Now().UTC().Format(time.DateOnly), prefix, hash, userID(r), isSuper(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.rotate_token", a.Name, "new token "+t.Name+", other tokens revoked")
	writeJSON(w, 200, map[string]any{"app": a, "token": tok, "app_token": t})
}

// ---- events and audit ----

func maskEvents(r *http.Request, evs []*store.Event) []*store.Event {
	if isSuper(r) {
		return evs
	}
	out := make([]*store.Event, 0, len(evs))
	for _, e := range evs {
		if e.KeyHidden {
			// Drop events for hidden keys entirely so the pool size cannot be inferred.
			continue
		}
		out = append(out, e)
	}
	return out
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EventFilter{Kind: q.Get("kind")}
	f.ServiceID, _ = strconv.ParseInt(q.Get("service_id"), 10, 64)
	f.KeyID, _ = strconv.ParseInt(q.Get("key_id"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	evs, err := s.Store.ListEvents(r.Context(), f)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": maskEvents(r, evs)})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	a, err := s.Store.ListAudit(r.Context(), limit)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"audit": a})
}

// ---- users ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.Store.ListUsers(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"users": us})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		credentials
		Role store.Role `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if !strings.Contains(email, "@") || !validPassword(body.Password) || !body.Role.Valid() {
		writeErr(w, 400, "invalid", "valid email, role, and a "+passwordRule+" are required")
		return
	}
	h, ok := hashPassword(w, body.Password)
	if !ok {
		return
	}
	u, err := s.Store.CreateUser(r.Context(), email, body.Name, h, body.Role)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "user.create", u.Email, string(u.Role))
	writeJSON(w, 201, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Name     *string     `json:"name"`
		Role     *store.Role `json:"role"`
		Disabled *bool       `json:"disabled"`
		Password *string     `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Role != nil && !body.Role.Valid() {
		writeErr(w, 400, "invalid", "unknown role")
		return
	}
	p := store.UserPatch{Name: body.Name, Role: body.Role, Disabled: body.Disabled}
	if body.Password != nil {
		if !validPassword(*body.Password) {
			writeErr(w, 400, "weak_password", passwordRule)
			return
		}
		hs, ok := hashPassword(w, *body.Password)
		if !ok {
			return
		}
		p.PasswordHash = &hs
	}
	s.userMu.Lock()
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		s.userMu.Unlock()
		writeStoreErr(w, err)
		return
	}
	// Only removing an active superadmin can drop the active count.
	demoting := (body.Role != nil && *body.Role != store.RoleSuperadmin) || (body.Disabled != nil && *body.Disabled)
	if target.Role == store.RoleSuperadmin && !target.Disabled && demoting {
		if n, _ := s.Store.CountSuperadmins(r.Context()); n <= 1 {
			s.userMu.Unlock()
			writeErr(w, 400, "last_superadmin", "keep at least one active superadmin")
			return
		}
	}
	err = s.Store.UpdateUser(r.Context(), id, p)
	s.userMu.Unlock()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if p.PasswordHash != nil {
		// A reset must end every session of that user, or a stolen session
		// outlives it. Keep the caller's own session when they reset themselves.
		keep := ""
		if id == userFrom(r).ID {
			keep, _ = r.Context().Value(sessionHashKey).(string)
		}
		_ = s.Store.DeleteUserSessions(r.Context(), id, keep)
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "user.update", target.Email, userChangeDetail(target, body.Name, body.Role, body.Disabled, body.Password != nil))
	u, _ := s.Store.GetUser(r.Context(), id)
	writeJSON(w, 200, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if id == userFrom(r).ID {
		writeErr(w, 400, "self_delete", "you cannot delete yourself")
		return
	}
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.userMu.Lock()
	if target.Role == store.RoleSuperadmin && !target.Disabled {
		if n, _ := s.Store.CountSuperadmins(r.Context()); n <= 1 {
			s.userMu.Unlock()
			writeErr(w, 400, "last_superadmin", "keep at least one active superadmin")
			return
		}
	}
	err = s.Store.DeleteUser(r.Context(), id)
	s.userMu.Unlock()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "user.delete", target.Email, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// userChangeDetail describes a user update for the audit log.
func userChangeDetail(before *store.User, name *string, role *store.Role, disabled *bool, password bool) string {
	parts := []string{}
	if name != nil && *name != before.Name {
		parts = append(parts, "name changed")
	}
	if role != nil && *role != before.Role {
		parts = append(parts, fmt.Sprintf("role %s to %s", before.Role, *role))
	}
	if disabled != nil && *disabled != before.Disabled {
		parts = append(parts, "disabled="+strconv.FormatBool(*disabled))
	}
	if password {
		parts = append(parts, "password reset")
	}
	return strings.Join(parts, ", ")
}
