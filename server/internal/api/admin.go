package api

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
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
		tot.Disabled += c.Disabled
		tot.Backup += c.Backup
		tot.Busy += c.Busy
	}
	inflight := 0
	for _, sv := range svcs {
		inflight += sv.InFlight
	}
	since := time.Now().Add(-24 * time.Hour)
	reqs, _ := s.Store.CountEvents(ctx, since, "lease", "proxy")
	rls, _ := s.Store.CountEvents(ctx, since, "rate_limited", "quota_exhausted")
	evs, _ := s.Store.ListEvents(ctx, store.EventFilter{Limit: 15})
	writeJSON(w, 200, map[string]any{
		"services": len(svcs), "keys": map[string]int{
			"total": tot.Total, "active": tot.Available, "cooling": tot.Cooling, "exhausted": tot.Exhausted,
			"invalid": tot.Invalid, "disabled": tot.Disabled, "backup": tot.Backup, "busy": tot.Busy,
		},
		"in_flight":    inflight,
		"requests_24h": reqs, "rate_limits_24h": rls, "recent_events": maskEvents(r, evs),
	})
}

// ---- services ----

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"providers": providers.Templates(), "strategies": pool.Strategies})
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	svcs, err := s.Store.ListServices(r.Context())
	if err == nil {
		err = s.Store.FillStats(r.Context(), svcs, isSuper(r), s.Pool.InFlight())
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
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
	sv.BaseURL = strings.TrimRight(sv.BaseURL, "/")
	switch {
	case !slugRe.MatchString(sv.Slug):
		return "slug must be lowercase letters, digits and hyphens"
	case sv.Name == "":
		return "name is required"
	case sv.Provider != "serpapi" && sv.Provider != "elevenlabs" && sv.Provider != "generic":
		return "provider must be serpapi, elevenlabs or generic"
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
	t := providers.Get(prov).Template()
	sv := &store.Service{
		Slug: prov, Name: t.Name, Provider: t.Provider, Category: t.Category, Description: t.Description,
		BaseURL: t.BaseURL, AuthType: t.AuthType, AuthName: t.AuthName, Strategy: "round_robin",
		CooldownSec: t.DefaultCooldownSec, MaxAttempts: 5, Icon: t.Icon,
		MaxConcurrency: t.DefaultMaxConcurrency, QueueTimeoutSec: t.DefaultQueueTimeoutSec,
	}
	if msg := in.apply(sv); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	created, err := s.Store.CreateService(r.Context(), sv)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.create", created.Slug, "")
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
	if msg := in.apply(sv); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if err := s.Store.UpdateService(r.Context(), sv); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "service.update", sv.Slug, "")
	_ = s.Store.FillStats(r.Context(), []*store.Service{sv}, isSuper(r), s.Pool.InFlight())
	writeJSON(w, 200, sv)
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
	writeJSON(w, 200, map[string]any{"keys": keys})
}

func (s *Server) fillInFlight(keys ...*store.Key) {
	inf := s.Pool.InFlight()
	for _, k := range keys {
		k.InFlight = inf[k.ID]
	}
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
}

func (in keyInput) validate(r *http.Request) (int, string) {
	if in.Hidden != nil && *in.Hidden && !isSuper(r) {
		return 403, "only a superadmin can hide keys"
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
	return 0, ""
}

func (s *Server) createKeys(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var body struct {
		Keys []keyInput `json:"keys"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Keys) == 0 || len(body.Keys) > 1000 {
		writeErr(w, 400, "invalid", "send between 1 and 1000 keys")
		return
	}
	created := []*store.Key{}
	skipped := []string{}
	for i, in := range body.Keys {
		if in.Secret == nil {
			writeErr(w, 400, "invalid", fmt.Sprintf("key %d has no secret", i+1))
			return
		}
		if code, msg := in.validate(r); code != 0 {
			writeErr(w, code, "invalid", msg)
			return
		}
	}
	for i, in := range body.Keys {
		secret := strings.TrimSpace(*in.Secret)
		enc, err := s.Pool.Box.Encrypt(secret)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		nk := store.NewKey{
			ServiceID: sv.ID, SecretEnc: enc, SecretFP: s.Pool.Box.Fingerprint(secret), Hint: crypto.Hint(secret),
			Tier: "primary", Weight: 1, Priority: 100, CreatedBy: userFrom(r).ID,
		}
		nk.Label = fmt.Sprintf("%s key %s", sv.Name, crypto.Hint(secret)[3:])
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
		nk.QuotaLimit = in.QuotaLimit
		if in.MaxConc != nil && *in.MaxConc >= 0 {
			nk.MaxConc = in.MaxConc
		}
		k, err := s.Store.CreateKey(r.Context(), nk)
		if err == store.ErrDuplicate {
			skipped = append(skipped, fmt.Sprintf("line %d (%s) already exists", i+1, crypto.Hint(secret)))
			continue
		}
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		created = append(created, k)
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "key.create", sv.Slug, fmt.Sprintf("%d added, %d skipped", len(created), len(skipped)))
	// Check new keys in the background so quotas show up without a manual click.
	go s.checkMany(sv, created)
	writeJSON(w, 201, map[string]any{"keys": created, "skipped": skipped})
}

func (s *Server) checkMany(sv *store.Service, keys []*store.Key) {
	if sv.Provider == "generic" {
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
	if code, msg := in.validate(r); code != 0 {
		writeErr(w, code, "invalid", msg)
		return
	}
	p := store.KeyPatch{
		Label: in.Label, Tier: in.Tier, Hidden: in.Hidden, Restricted: in.Restricted, AppIDs: in.AppIDs,
		Status: in.Status, Weight: in.Weight, Priority: in.Priority, Notes: in.Notes,
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
	if err := s.Store.UpdateKey(r.Context(), k.ID, p); err != nil {
		writeStoreErr(w, err)
		return
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
	secret, err := s.Pool.Box.Decrypt(k.SecretEnc)
	if err != nil {
		writeErr(w, 500, "decrypt_failed", "cannot decrypt, check COGITO_MASTER_KEY")
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
	detail := "ok"
	if err != nil {
		detail = err.Error()
	}
	s.Store.AddEvent(r.Context(), store.NewEvent{Kind: "health", ServiceID: sv.ID, KeyID: k.ID,
		LatencyMS: time.Since(start).Milliseconds(), Detail: detail})
	nk, gerr := s.Store.GetKey(r.Context(), k.ID)
	if gerr != nil {
		writeStoreErr(w, gerr)
		return
	}
	if err != nil {
		writeErr(w, 502, "check_failed", err.Error())
		return
	}
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
}

func (in appInput) apply(a *store.App) string {
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
	token = crypto.RandomToken("cog_", 32)
	return token, token[:8], crypto.SHA256(token)
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var in appInput
	if !decode(w, r, &in) {
		return
	}
	a := &store.App{Mode: "both", Active: true}
	if msg := in.apply(a); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	tok, prefix, hash := newAppToken()
	a.TokenPrefix = prefix
	created, err := s.Store.CreateApp(r.Context(), a, hash)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.create", created.Name, "")
	writeJSON(w, 201, map[string]any{"app": created, "token": tok})
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var in appInput
	if !decode(w, r, &in) {
		return
	}
	if msg := in.apply(a); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if err := s.Store.UpdateApp(r.Context(), a); err != nil {
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
	s.Store.AddAudit(r.Context(), userFrom(r), "app.delete", a.Name, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) rotateAppToken(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	tok, prefix, hash := newAppToken()
	if err := s.Store.SetAppToken(r.Context(), id, hash, prefix); err != nil {
		writeStoreErr(w, err)
		return
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.rotate_token", a.Name, "")
	writeJSON(w, 200, map[string]any{"app": a, "token": tok})
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
		writeErr(w, 400, "invalid", "valid email, role, and a password of at least 10 characters are required")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	u, err := s.Store.CreateUser(r.Context(), email, body.Name, string(h), body.Role)
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
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if body.Role != nil && !body.Role.Valid() {
		writeErr(w, 400, "invalid", "unknown role")
		return
	}
	demoting := (body.Role != nil && *body.Role != store.RoleSuperadmin) || (body.Disabled != nil && *body.Disabled)
	if target.Role == store.RoleSuperadmin && demoting {
		if n, _ := s.Store.CountSuperadmins(r.Context()); n <= 1 {
			writeErr(w, 400, "last_superadmin", "keep at least one active superadmin")
			return
		}
	}
	p := store.UserPatch{Name: body.Name, Role: body.Role, Disabled: body.Disabled}
	if body.Password != nil {
		if !validPassword(*body.Password) {
			writeErr(w, 400, "weak_password", "password must be at least 10 characters")
			return
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		hs := string(h)
		p.PasswordHash = &hs
	}
	if err := s.Store.UpdateUser(r.Context(), id, p); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "user.update", target.Email, "")
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
	if target.Role == store.RoleSuperadmin {
		if n, _ := s.Store.CountSuperadmins(r.Context()); n <= 1 {
			writeErr(w, 400, "last_superadmin", "keep at least one active superadmin")
			return
		}
	}
	if err := s.Store.DeleteUser(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "user.delete", target.Email, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}
