package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func contextTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

type leaseDTO struct {
	LeaseID   string    `json:"lease_id"`
	KeyID     int64     `json:"key_id"`
	Service   string    `json:"service"`
	Secret    string    `json:"secret"`
	Auth      authDTO   `json:"auth"`
	BaseURL   string    `json:"base_url"`
	ExpiresAt time.Time `json:"expires_at"`
	Exclusive bool      `json:"exclusive"`
}

type authDTO struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Prefix goes before the secret, for example "Token ". Empty means the
	// usual form for the type ("Bearer " for bearer, nothing otherwise).
	Prefix string `json:"prefix,omitempty"`
}

func (s *Server) v1Services(w http.ResponseWriter, r *http.Request) {
	a := appFrom(r)
	svcs, err := s.Store.ListServices(r.Context())
	if err == nil {
		err = s.Store.FillStats(r.Context(), svcs, true, s.Pool.InFlight())
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := []map[string]any{}
	base := s.publicURL(r)
	for _, sv := range svcs {
		if !a.CanUse(sv.Slug) {
			continue
		}
		// Apps learn only whether a key is available, never how many exist.
		out = append(out, map[string]any{
			"slug": sv.Slug, "name": sv.Name, "provider": sv.Provider, "category": sv.Category,
			"available": sv.KeyCounts.Available > sv.KeyCounts.Busy, "base_url": sv.BaseURL,
			"auth":      authDTO{Type: sv.AuthType, Name: sv.AuthName, Prefix: authPrefix(sv)},
			"guide_url": guideURL(base, sv.Slug),
		})
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) serviceForApp(w http.ResponseWriter, r *http.Request, slug string) (*store.Service, bool) {
	a := appFrom(r)
	if !a.CanUse(slug) {
		writeErr(w, 403, "service_not_allowed", "this app cannot use service "+slug)
		return nil, false
	}
	sv, err := s.Store.GetServiceBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "unknown_service", "no service named "+slug)
		} else {
			writeStoreErr(w, err)
		}
		return nil, false
	}
	if !s.sharedAs(r.Context(), a, sv) {
		writeErr(w, 403, "service_not_allowed", "this app cannot use service "+slug)
		return nil, false
	}
	return sv, true
}

func writeNoKey(w http.ResponseWriter, err error) bool {
	var nk *pool.NoKeyError
	if !errors.As(err, &nk) {
		return false
	}
	retry := int(math.Ceil(nk.RetryAfter.Seconds()))
	if retry > 0 {
		w.Header().Set("Retry-After", itoa(retry))
	}
	e := apiError{Code: "no_key_available", Message: "every key for this service is cooling down, exhausted or disabled"}
	if nk.Busy {
		e = apiError{Code: "all_keys_busy", Message: "every usable key is at its concurrency limit, retry shortly"}
		if nk.Paced {
			e.Message = "every usable key is at its concurrency limit or at a rate limit its provider publishes, retry shortly"
		}
	}
	writeJSON(w, 503, map[string]any{"error": e, "retry_after_sec": retry})
	return true
}

// errPeerLease refuses a lease on a service shared by another server: a
// lease would hand out the share token. Those services are proxy only.
var errPeerLease = errors.New("this service is shared by another Acnologia server and is proxy only, use /proxy/{service}/...")

// errHandoverLease refuses a lease on keys another server handed over: a
// lease hands out the raw key, and those keys never leave this server.
var errHandoverLease = errors.New("this service holds keys another Acnologia server handed over and is proxy only, use /proxy/{service}/...")

// isHandoverService reports whether a service holds keys another server
// handed over (made for a handover link, with the owner's provider).
func isHandoverService(sv *store.Service) bool {
	return sv.PeerLinkID != nil && sv.Provider != providers.PeerSlug
}

// issueLease picks a key and creates a lease. On keys with a concurrency
// limit the lease holds a slot until it is reported or expires, so set a
// ttl close to how long you need the key.
func (s *Server) issueLease(ctx context.Context, a *store.App, sv *store.Service, exclude []int64, ttl, wait time.Duration) (*leaseDTO, error) {
	return s.issueLeaseOpts(ctx, a, sv, exclude, ttl, wait, false)
}

// issueLeaseOpts can also reserve the key exclusively: Acnologia then hands it
// to no one else, through leases or the proxy, until the lease ends.
func (s *Server) issueLeaseOpts(ctx context.Context, a *store.App, sv *store.Service, exclude []int64, ttl, wait time.Duration, exclusive bool) (*leaseDTO, error) {
	if sv.Provider == providers.PeerSlug {
		return nil, errPeerLease
	}
	// A lease hands out the raw key; keys another server handed over stay
	// inside this server's proxy.
	if isHandoverService(sv) {
		return nil, errHandoverLease
	}
	ex := map[int64]bool{}
	for _, id := range exclude {
		ex[id] = true
	}
	// A lease returns the plaintext secret, so hidden keys go only to apps a
	// superadmin cleared for them.
	p, err := s.Pool.PickWaitOpts(ctx, sv, a.ID, a.LeaseHidden, ex, wait, exclusive)
	if err != nil {
		s.Store.AddEvent(ctx, store.NewEvent{Kind: "error", ServiceID: sv.ID, AppID: a.ID, StatusCode: 503, Detail: "lease: " + err.Error()})
		var nk *pool.NoKeyError
		if errors.As(err, &nk) && !nk.Busy {
			s.Pool.EmitPoolEmpty(sv, err)
		}
		return nil, err
	}
	if p.Key.IsHandedOver() {
		p.Release()
		return nil, errHandoverLease
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 5 * time.Minute
		if pool.ConcurrencyLimit(p.Key, sv, providers.Get(sv.Provider)) > 0 {
			// A forgotten lease blocks a slot, so default to a shorter hold.
			ttl = 2 * time.Minute
		}
	}
	now := time.Now().UTC()
	l := &store.Lease{ID: crypto.RandomToken("lse_", 16), KeyID: p.Key.ID, AppID: a.ID, ServiceID: sv.ID, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if err := s.Store.CreateLease(ctx, l); err != nil {
		p.Release()
		return nil, err
	}
	if exclusive {
		_ = s.Store.MarkLeaseExclusive(ctx, l.ID)
	}
	s.Pool.HoldLease(p, l.ID, l.ExpiresAt)
	// A lease is charged as one request: Acnologia cannot see what the app does with the key.
	cost := costPerRequest(sv, providers.Get(sv.Provider), "", "")
	s.Store.AddEvent(ctx, store.NewEvent{Kind: "lease", ServiceID: sv.ID, KeyID: p.Key.ID, AppID: a.ID, Cost: cost})
	_ = s.Store.RecordAppUsage(ctx, a.ID, cost, false)
	return &leaseDTO{
		LeaseID: l.ID, KeyID: p.Key.ID, Service: sv.Slug, Secret: p.Secret,
		Auth: authDTO{Type: sv.AuthType, Name: sv.AuthName, Prefix: authPrefix(sv)}, BaseURL: sv.BaseURL, ExpiresAt: l.ExpiresAt,
		Exclusive: exclusive,
	}, nil
}

var (
	errLeaseNotFound = errors.New("lease not found, already reported, or expired")
	// errLeaseRevoked ends a lease the app may no longer hold.
	errLeaseRevoked = errors.New("this app may no longer hold this key; the lease has ended")
)

// renewLease extends an open lease so long jobs keep their key. Access is
// checked again as for a new lease: an app whose mode, services, share scope
// or hidden key grant changed, or whose key an admin disabled, cannot keep
// a key (and an exclusive hold on it) by renewing. Such a lease ends.
func (s *Server) renewLease(ctx context.Context, a *store.App, id string, ttl time.Duration) (time.Time, error) {
	l, err := s.Store.GetLease(ctx, id)
	if err != nil || l.AppID != a.ID || l.Outcome != "" || !time.Now().Before(l.ExpiresAt) {
		return time.Time{}, errLeaseNotFound
	}
	sv, err := s.Store.GetService(ctx, l.ServiceID)
	k, kerr := s.Store.GetKey(ctx, l.KeyID)
	if err != nil || kerr != nil || !a.AllowsLease() || !a.CanUse(sv.Slug) || sv.Provider == providers.PeerSlug || isHandoverService(sv) ||
		k.Status == store.StatusDisabled || k.Status == store.StatusBanned || !s.Pool.MayUse(ctx, sv, a.ID, a.LeaseHidden, k) {
		if s.Store.ClaimLeaseOutcome(ctx, l.ID, "revoked") == nil {
			s.Pool.ReleaseLease(l.ID)
		}
		return time.Time{}, errLeaseRevoked
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	exp := time.Now().UTC().Add(ttl)
	if err := s.Store.RenewLease(ctx, id, a.ID, exp); err != nil {
		return time.Time{}, errLeaseNotFound
	}
	s.Pool.RenewLease(id, exp)
	return exp, nil
}

func (s *Server) v1Renew(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TTLSec int `json:"ttl_sec"`
	}
	if !decode(w, r, &body) {
		return
	}
	exp, err := s.renewLease(r.Context(), appFrom(r), chi.URLParam(r, "id"), time.Duration(body.TTLSec)*time.Second)
	if errors.Is(err, errLeaseRevoked) {
		writeErr(w, 403, "lease_revoked", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 404, "lease_not_found", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "expires_at": exp})
}

func (s *Server) v1Lease(w http.ResponseWriter, r *http.Request) {
	a := appFrom(r)
	if !a.AllowsLease() {
		writeErr(w, 403, "lease_not_allowed", "this app is proxy only, use /proxy/{service}/...")
		return
	}
	var body struct {
		Service       string  `json:"service"`
		ExcludeKeyIDs []int64 `json:"exclude_key_ids"`
		TTLSec        int     `json:"ttl_sec"`
		WaitSec       int     `json:"wait_sec"`
		Exclusive     bool    `json:"exclusive"`
	}
	if !decode(w, r, &body) {
		return
	}
	sv, ok := s.serviceForApp(w, r, body.Service)
	if !ok {
		return
	}
	release, limited := s.appLimited(w, r, a, costPerRequest(sv, providers.Get(sv.Provider), "", ""))
	if limited {
		return
	}
	defer release()
	wait := time.Duration(min(max(body.WaitSec, 0), 60)) * time.Second
	l, err := s.issueLeaseOpts(r.Context(), a, sv, body.ExcludeKeyIDs, time.Duration(body.TTLSec)*time.Second, wait, body.Exclusive)
	if errors.Is(err, errPeerLease) || errors.Is(err, errHandoverLease) {
		writeErr(w, 403, "lease_not_allowed", err.Error())
		return
	}
	if err != nil {
		if !writeNoKey(w, err) {
			writeStoreErr(w, err)
		}
		return
	}
	writeJSON(w, 200, l)
}

type reportInput struct {
	Outcome       string `json:"outcome"`
	StatusCode    int    `json:"status_code"`
	RetryAfterSec int    `json:"retry_after_sec"`
	Message       string `json:"message"`
	Rotate        bool   `json:"rotate"`
}

// leaseReportGrace is how long after expiry a lease can still be reported.
// Later reports are refused: the key may have been replaced or reassigned.
const leaseReportGrace = 10 * time.Minute

// maxReportRetryAfter caps the cooldown one app can put on a shared key.
const maxReportRetryAfter = 24 * 60 * 60

// report applies an outcome to a lease and optionally issues the next lease.
func (s *Server) report(ctx context.Context, a *store.App, leaseID string, in reportInput) (*leaseDTO, int, error) {
	out, ok := providers.ParseOutcome(in.Outcome)
	if !ok {
		return nil, 400, errors.New("outcome must be ok, rate_limited, busy, quota_exhausted, invalid, banned or error")
	}
	l, err := s.Store.GetLease(ctx, leaseID)
	if err != nil || l.AppID != a.ID {
		return nil, 404, errors.New("unknown lease")
	}
	if l.Outcome != "" {
		return nil, 409, errors.New("lease already reported")
	}
	if time.Now().After(l.ExpiresAt.Add(leaseReportGrace)) {
		s.Pool.ReleaseLease(l.ID)
		return nil, 410, errors.New("lease expired, report within 10 minutes of expiry")
	}
	sv, err := s.Store.GetService(ctx, l.ServiceID)
	if err != nil {
		return nil, 404, errors.New("service no longer exists")
	}
	// Access may have been revoked since the lease was issued.
	if !a.AllowsLease() || !a.CanUse(sv.Slug) || !s.sharedAs(ctx, a, sv) {
		s.Pool.ReleaseLease(l.ID)
		return nil, 403, errors.New("this app can no longer use service " + sv.Slug)
	}
	// Claim the lease first so two concurrent reports cannot both apply.
	if err := s.Store.ClaimLeaseOutcome(ctx, l.ID, string(out)); err != nil {
		if errors.Is(err, store.ErrLeaseReported) {
			return nil, 409, errors.New("lease already reported")
		}
		return nil, 500, err
	}
	// Reporting ends the lease and frees its concurrency slot.
	s.Pool.ReleaseLease(l.ID)
	// The claim has committed, so a retried report gets 409: the verdict must
	// land even if the caller hangs up from here on.
	wctx := context.WithoutCancel(ctx)
	k, err := s.Store.GetKey(wctx, l.KeyID)
	if err != nil {
		return nil, 404, errors.New("key no longer exists")
	}
	// Client errors often quote the request URL, which carries query-auth
	// keys. The message is shown to every dashboard user, so drop the secret.
	msg := in.Message
	if secret, derr := s.Pool.Box.Decrypt(k.SecretEnc); derr == nil {
		msg = crypto.Scrub(msg, secret)
	}
	msg = crypto.Truncate(msg, 500)
	retry := min(max(in.RetryAfterSec, 0), maxReportRetryAfter)
	res := providers.Result{Outcome: out, RetryAfter: time.Duration(retry) * time.Second, Message: msg}
	// A key an admin disabled (or a suspended one) keeps its state until a
	// person changes it; the store also refuses to move its status.
	if k.Status != store.StatusDisabled && k.Status != store.StatusBanned {
		if err := s.Pool.Apply(wctx, sv, k, res); err != nil {
			return nil, 500, err
		}
	}
	if out != providers.OK {
		s.Store.AddEvent(wctx, store.NewEvent{Kind: string(out), ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: in.StatusCode, Detail: msg})
	}
	if !in.Rotate || out == providers.OK {
		return nil, 200, nil
	}
	// The next lease is a new lease: an app at its request or budget limit
	// gets none, as on /lease and get_key. The report itself still counts.
	hit, release := s.admitApp(ctx, a, costPerRequest(sv, providers.Get(sv.Provider), "", ""))
	if hit != nil {
		return nil, 200, nil
	}
	defer release()
	// A rotated exclusive lease hands over an exclusive key too, so the caller
	// keeps owning its key outright across rotations.
	exclusive, _ := s.Store.IsLeaseExclusive(ctx, l.ID)
	next, err := s.issueLeaseOpts(ctx, a, sv, []int64{k.ID}, 0, 0, exclusive)
	if err != nil {
		var nk *pool.NoKeyError
		if errors.As(err, &nk) {
			return nil, 200, nil
		}
		return nil, 500, err
	}
	return next, 200, nil
}

func (s *Server) v1Report(w http.ResponseWriter, r *http.Request) {
	var in reportInput
	if !decode(w, r, &in) {
		return
	}
	next, code, err := s.report(r.Context(), appFrom(r), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, code, "report_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "next": next})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
