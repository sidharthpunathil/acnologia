package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/egress"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// checkServiceRefs rejects a service whose slug collides with a group or a
// capability, or whose egress proxy does not exist. oldSlug is the slug
// before an update.
func (s *Server) checkServiceRefs(w http.ResponseWriter, r *http.Request, sv *store.Service, oldSlug string) bool {
	if sv.Slug != oldSlug {
		if _, err := s.Store.GetGroupBySlug(r.Context(), sv.Slug); err == nil {
			writeErr(w, 409, "duplicate", "a service group already uses the slug "+sv.Slug)
			return false
		}
		if _, err := s.Store.GetCapabilityBySlug(r.Context(), sv.Slug); err == nil {
			writeErr(w, 409, "duplicate", "a capability already uses the slug "+sv.Slug)
			return false
		}
	}
	if sv.EgressID != nil {
		if _, err := s.Store.GetEgress(r.Context(), *sv.EgressID); err != nil {
			writeErr(w, 400, "unknown_egress", "no egress proxy with that id")
			return false
		}
	}
	return true
}

// ---- webhooks ----

type webhookInput struct {
	Name   *string   `json:"name"`
	URL    *string   `json:"url"`
	Format *string   `json:"format"`
	Events *[]string `json:"events"`
	Active *bool     `json:"active"`
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.Store.ListWebhooks(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"webhooks": hooks, "event_types": notify.Types, "formats": notify.Formats})
}

// webhookURLHint shows the host and hides the path, which for Slack,
// Discord and Telegram is the secret.
func webhookURLHint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("url must be an http(s) URL")
	}
	if u.User != nil {
		return "", errors.New("put credentials in the path or query, not user:pass@")
	}
	return u.Scheme + "://" + u.Host + "/…", nil
}

func (in webhookInput) apply(h *store.Webhook, box *crypto.Box) string {
	if in.Name != nil {
		h.Name = strings.TrimSpace(*in.Name)
	}
	if in.Format != nil {
		h.Format = *in.Format
	}
	if in.Active != nil {
		h.Active = *in.Active
	}
	if in.Events != nil {
		h.Events = []string{}
		for _, e := range *in.Events {
			if !notify.ValidType(e) {
				return "unknown event type " + e
			}
			h.Events = append(h.Events, e)
		}
	}
	if in.URL != nil {
		hint, err := webhookURLHint(*in.URL)
		if err != nil {
			return err.Error()
		}
		enc, err := box.Encrypt(strings.TrimSpace(*in.URL))
		if err != nil {
			return "cannot encrypt the URL"
		}
		h.URLEnc, h.URLHint = enc, hint
	}
	switch {
	case h.Name == "" || len(h.Name) > 100:
		return "name is required (at most 100 characters)"
	case !notify.ValidFormat(h.Format):
		return "format must be json, slack, discord or telegram"
	case len(h.URLEnc) == 0:
		return "url is required"
	}
	return ""
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var in webhookInput
	if !decode(w, r, &in) {
		return
	}
	h := &store.Webhook{Format: "json", Active: true}
	if msg := in.apply(h, s.Pool.Box); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	secret := crypto.RandomToken("whsec_", 32)
	enc, err := s.Pool.Box.Encrypt(secret)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	h.SecretEnc = enc
	created, err := s.Store.CreateWebhook(r.Context(), h)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Pool.Notify.Invalidate()
	s.Store.AddAudit(r.Context(), userFrom(r), "webhook.create", created.Name, created.URLHint)
	out := map[string]any{"webhook": created}
	if created.Format == "json" {
		out["signing_secret"] = secret // shown once; verify X-Acnologia-Signature with it
	}
	writeJSON(w, 201, out)
}

func (s *Server) loadWebhook(w http.ResponseWriter, r *http.Request) (*store.Webhook, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return nil, false
	}
	h, err := s.Store.GetWebhook(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return h, true
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadWebhook(w, r)
	if !ok {
		return
	}
	var in webhookInput
	if !decode(w, r, &in) {
		return
	}
	if msg := in.apply(h, s.Pool.Box); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if in.URL == nil {
		h.URLEnc = nil // UpdateWebhook keeps the stored URL
	}
	if err := s.Store.UpdateWebhook(r.Context(), h); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Pool.Notify.Invalidate()
	s.Store.AddAudit(r.Context(), userFrom(r), "webhook.update", h.Name, "")
	nh, _ := s.Store.GetWebhook(r.Context(), h.ID)
	writeJSON(w, 200, nh)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadWebhook(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteWebhook(r.Context(), h.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Pool.Notify.Invalidate()
	s.Store.AddAudit(r.Context(), userFrom(r), "webhook.delete", h.Name, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// testWebhook sends a test event right away and reports the receiver's answer.
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	h, ok := s.loadWebhook(w, r)
	if !ok {
		return
	}
	e := notify.Event{Type: notify.Test, TS: time.Now().UTC(), Message: "test event from " + userFrom(r).Email + ", webhooks are working"}
	status, err := s.Pool.Notify.Deliver(r.Context(), h, e)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = s.Store.RecordWebhookDelivery(r.Context(), h.ID, status, msg)
	writeJSON(w, 200, map[string]any{"ok": err == nil, "status": status, "error": msg})
}

// ---- egress proxies ----

type egressInput struct {
	Name   *string `json:"name"`
	URL    *string `json:"url"`
	Region *string `json:"region"`
	Notes  *string `json:"notes"`
}

func (in egressInput) apply(p *store.EgressProxy, box *crypto.Box) string {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Region != nil {
		p.Region = strings.TrimSpace(*in.Region)
	}
	if in.Notes != nil {
		p.Notes = strings.TrimSpace(*in.Notes)
	}
	if in.URL != nil {
		_, hint, err := egress.Parse(*in.URL)
		if err != nil {
			return err.Error()
		}
		enc, err := box.Encrypt(strings.TrimSpace(*in.URL))
		if err != nil {
			return "cannot encrypt the URL"
		}
		p.URLEnc, p.URLHint = enc, hint
	}
	switch {
	case p.Name == "" || len(p.Name) > 100:
		return "name is required (at most 100 characters)"
	case len(p.URLEnc) == 0:
		return "url is required"
	case len(p.Region) > 50 || len(p.Notes) > 500:
		return "region or notes too long"
	}
	return ""
}

func (s *Server) listEgress(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListEgress(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"proxies": ps})
}

func (s *Server) loadEgress(w http.ResponseWriter, r *http.Request) (*store.EgressProxy, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return nil, false
	}
	p, err := s.Store.GetEgress(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return p, true
}

func (s *Server) createEgress(w http.ResponseWriter, r *http.Request) {
	var in egressInput
	if !decode(w, r, &in) {
		return
	}
	p := &store.EgressProxy{}
	if msg := in.apply(p, s.Pool.Box); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	created, err := s.Store.CreateEgress(r.Context(), p)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "egress.create", created.Name, created.URLHint)
	writeJSON(w, 201, created)
}

// Changing or removing a proxy changes where keys travel, which is a
// superadmin decision for hidden keys. Proxies in use by a trusted service
// therefore need a superadmin to change.
func (s *Server) egressGuard(w http.ResponseWriter, r *http.Request, p *store.EgressProxy) bool {
	if isSuper(r) {
		return true
	}
	svcs, _ := s.Store.ListServices(r.Context())
	for _, sv := range svcs {
		if sv.EgressID != nil && *sv.EgressID == p.ID && sv.TargetTrusted {
			writeErr(w, 403, "forbidden", "service "+sv.Slug+" uses this proxy; only a superadmin can change it")
			return false
		}
	}
	keys, _ := s.Store.ListAllKeys(r.Context(), `WHERE egress_id=? AND hidden=1`, p.ID)
	if len(keys) > 0 {
		writeErr(w, 403, "forbidden", "only a superadmin can change this proxy")
		return false
	}
	return true
}

func (s *Server) updateEgress(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadEgress(w, r)
	if !ok {
		return
	}
	var in egressInput
	if !decode(w, r, &in) {
		return
	}
	if in.URL != nil && !s.egressGuard(w, r, p) {
		return
	}
	if msg := in.apply(p, s.Pool.Box); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if in.URL == nil {
		p.URLEnc = nil
	}
	if err := s.Store.UpdateEgress(r.Context(), p); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Pool.Egress.Invalidate(p.ID)
	s.Store.AddAudit(r.Context(), userFrom(r), "egress.update", p.Name, "")
	np, _ := s.Store.GetEgress(r.Context(), p.ID)
	writeJSON(w, 200, np)
}

func (s *Server) deleteEgress(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadEgress(w, r)
	if !ok || !s.egressGuard(w, r, p) {
		return
	}
	if err := s.Store.DeleteEgress(r.Context(), p.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Pool.Egress.Invalidate(p.ID)
	s.Store.AddAudit(r.Context(), userFrom(r), "egress.delete", p.Name, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) testEgress(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadEgress(w, r)
	if !ok {
		return
	}
	ip, err := s.Pool.Egress.Test(r.Context(), p.ID)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = s.Store.RecordEgressCheck(r.Context(), p.ID, ip, msg)
	writeJSON(w, 200, map[string]any{"ok": err == nil, "ip": ip, "error": msg})
}

// ---- service groups ----

type groupInput struct {
	Slug        *string   `json:"slug"`
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Members     *[]string `json:"members"`
	Strategy    *string   `json:"strategy"`
}

func (s *Server) applyGroup(ctx context.Context, in groupInput, g *store.ServiceGroup) string {
	if in.Slug != nil {
		g.Slug = strings.TrimSpace(*in.Slug)
	}
	if in.Name != nil {
		g.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		g.Description = strings.TrimSpace(*in.Description)
	}
	if in.Members != nil {
		g.Members = []string{}
		seen := map[string]bool{}
		for _, m := range *in.Members {
			m = strings.TrimSpace(m)
			if seen[m] {
				continue
			}
			if _, err := s.Store.GetServiceBySlug(ctx, m); err != nil {
				return "no service named " + m
			}
			seen[m] = true
			g.Members = append(g.Members, m)
		}
	}
	if in.Strategy != nil {
		g.Strategy = strings.TrimSpace(*in.Strategy)
	}
	if g.Strategy == "" {
		g.Strategy = StrategyPriority
	}
	switch {
	case !slugRe.MatchString(g.Slug):
		return "slug must be lowercase letters, digits and hyphens"
	case g.Name == "" || len(g.Name) > 100:
		return "name is required (at most 100 characters)"
	case !routingStrategies[g.Strategy] || g.Strategy == StrategyWeighted:
		return "strategy must be priority, round_robin, random, cheapest or most_keys"
	case len(g.Members) < 2:
		return "a group needs at least two services"
	case len(g.Members) > 20:
		return "a group can have at most 20 services"
	}
	return ""
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Store.ListGroups(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"groups": gs})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var in groupInput
	if !decode(w, r, &in) {
		return
	}
	g := &store.ServiceGroup{}
	if msg := s.applyGroup(r.Context(), in, g); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if taken, err := s.Store.SlugTaken(r.Context(), g.Slug); err != nil || taken {
		writeErr(w, 409, "duplicate", "a service or group already uses the slug "+g.Slug)
		return
	}
	created, err := s.Store.CreateGroup(r.Context(), g)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "group.create", created.Slug, strings.Join(created.Members, " > "))
	writeJSON(w, 201, created)
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	g, err := s.Store.GetGroup(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var in groupInput
	if !decode(w, r, &in) {
		return
	}
	in.Slug = nil // the slug is the public URL; delete and recreate to change it
	if msg := s.applyGroup(r.Context(), in, g); msg != "" {
		writeErr(w, 400, "invalid", msg)
		return
	}
	if err := s.Store.UpdateGroup(r.Context(), g); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "group.update", g.Slug, strings.Join(g.Members, " > "))
	writeJSON(w, 200, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	g, err := s.Store.GetGroup(r.Context(), id)
	if err == nil {
		err = s.Store.DeleteGroup(r.Context(), id)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "group.delete", g.Slug, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- admin API tokens ----

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	uid := userFrom(r).ID
	if isSuper(r) && r.URL.Query().Get("all") == "1" {
		uid = 0
	}
	ts, err := s.Store.ListTokens(r.Context(), uid)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": ts})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := userFrom(r)
	in.Name = strings.TrimSpace(in.Name)
	role, ok := store.ScopeRole(in.Scope)
	switch {
	case in.Name == "" || len(in.Name) > 100:
		writeErr(w, 400, "invalid", "name is required (at most 100 characters)")
		return
	case !ok:
		writeErr(w, 400, "invalid", "scope must be read, write or admin")
		return
	case !u.Role.AtLeast(role):
		writeErr(w, 403, "forbidden", "a token cannot have more access than you")
		return
	case in.ExpiresInDays < 0 || in.ExpiresInDays > 3650:
		writeErr(w, 400, "invalid", "expires_in_days must be 0 (never) to 3650")
		return
	}
	tok := crypto.RandomToken(store.AdminTokenPrefix, 40)
	t := &store.AdminToken{Name: in.Name, Scope: in.Scope, UserID: u.ID, TokenPrefix: tok[:12]}
	if in.ExpiresInDays > 0 {
		exp := time.Now().AddDate(0, 0, in.ExpiresInDays).UTC()
		t.ExpiresAt = &exp
	}
	created, err := s.Store.CreateToken(r.Context(), t, crypto.SHA256(tok))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), u, "token.create", created.Name, "scope="+created.Scope)
	writeJSON(w, 201, map[string]any{"token": tok, "info": created})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	t, err := s.Store.GetToken(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if t.UserID != userFrom(r).ID && !isSuper(r) {
		writeErr(w, 404, "not_found", "not found")
		return
	}
	if err := s.Store.DeleteToken(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "token.delete", t.Name, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- usage, breakers, cache ----

func (s *Server) appUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 366 {
		days = d
	}
	u, err := s.Store.GetAppUsage(r.Context(), a.ID, time.Now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	hist, err := s.Store.AppUsageHistory(r.Context(), a.ID, days)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"usage": u, "limits": a.Limits, "limit_hit": store.CheckLimits(a.Limits, u, time.Now()), "history": hist})
}

func (s *Server) serviceBreaker(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, s.breakerStatus(sv))
}

func (s *Server) breakerStatus(sv *store.Service) map[string]any {
	cfg := breakerSettings(providers.Get(sv.Provider))
	return map[string]any{
		"status":     s.Breakers.Status(sv.ID, cfg),
		"threshold":  cfg.Threshold,
		"window_sec": cfg.Window.Seconds(),
		"reset_sec":  cfg.Reset.Seconds(),
	}
}

func (s *Server) resetServiceBreaker(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	s.Breakers.Reset(sv.ID)
	s.Store.AddAudit(r.Context(), userFrom(r), "service.breaker_reset", sv.Slug, "")
	writeJSON(w, 200, s.breakerStatus(sv))
}

func (s *Server) purgeServiceCache(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	n := s.Cache.PurgePrefix(servicePrefix(sv.ID))
	s.Store.AddAudit(r.Context(), userFrom(r), "service.cache_purge", sv.Slug, fmt.Sprintf("%d entries", n))
	writeJSON(w, 200, map[string]any{"purged": n})
}

func (s *Server) cacheStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Cache.Stats())
}
