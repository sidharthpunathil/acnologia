package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
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
}

type authDTO struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

func (s *Server) v1Services(w http.ResponseWriter, r *http.Request) {
	a := appFrom(r)
	svcs, err := s.Store.ListServices(r.Context())
	if err == nil {
		err = s.Store.FillStats(r.Context(), svcs, true)
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := []map[string]any{}
	for _, sv := range svcs {
		if !a.CanUse(sv.Slug) {
			continue
		}
		// Apps learn only whether a key is available, never how many exist.
		out = append(out, map[string]any{
			"slug": sv.Slug, "name": sv.Name, "provider": sv.Provider, "category": sv.Category,
			"available": sv.KeyCounts.Available > 0, "base_url": sv.BaseURL,
			"auth": authDTO{Type: sv.AuthType, Name: sv.AuthName},
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
	writeJSON(w, 503, map[string]any{
		"error":           apiError{Code: "no_key_available", Message: "every key for this service is cooling down, exhausted or disabled"},
		"retry_after_sec": retry,
	})
	return true
}

func (s *Server) issueLease(ctx context.Context, a *store.App, sv *store.Service, exclude []int64, ttl time.Duration) (*leaseDTO, error) {
	ex := map[int64]bool{}
	for _, id := range exclude {
		ex[id] = true
	}
	p, err := s.Pool.Pick(ctx, sv, a.ID, ex)
	if err != nil {
		s.Store.AddEvent(ctx, store.NewEvent{Kind: "error", ServiceID: sv.ID, AppID: a.ID, StatusCode: 503, Detail: "lease: " + err.Error()})
		return nil, err
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 5 * time.Minute
	}
	now := time.Now().UTC()
	l := &store.Lease{ID: crypto.RandomToken("lse_", 16), KeyID: p.Key.ID, AppID: a.ID, ServiceID: sv.ID, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if err := s.Store.CreateLease(ctx, l); err != nil {
		return nil, err
	}
	s.Store.AddEvent(ctx, store.NewEvent{Kind: "lease", ServiceID: sv.ID, KeyID: p.Key.ID, AppID: a.ID})
	return &leaseDTO{
		LeaseID: l.ID, KeyID: p.Key.ID, Service: sv.Slug, Secret: p.Secret,
		Auth: authDTO{Type: sv.AuthType, Name: sv.AuthName}, BaseURL: sv.BaseURL, ExpiresAt: l.ExpiresAt,
	}, nil
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
	}
	if !decode(w, r, &body) {
		return
	}
	sv, ok := s.serviceForApp(w, r, body.Service)
	if !ok {
		return
	}
	l, err := s.issueLease(r.Context(), a, sv, body.ExcludeKeyIDs, time.Duration(body.TTLSec)*time.Second)
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

// report applies an outcome to a lease and optionally issues the next lease.
func (s *Server) report(ctx context.Context, a *store.App, leaseID string, in reportInput) (*leaseDTO, int, error) {
	out, ok := providers.ParseOutcome(in.Outcome)
	if !ok {
		return nil, 400, errors.New("outcome must be ok, rate_limited, quota_exhausted, invalid or error")
	}
	l, err := s.Store.GetLease(ctx, leaseID)
	if err != nil || l.AppID != a.ID {
		return nil, 404, errors.New("unknown lease")
	}
	if l.Outcome != "" {
		return nil, 409, errors.New("lease already reported")
	}
	k, err := s.Store.GetKey(ctx, l.KeyID)
	if err != nil {
		return nil, 404, errors.New("key no longer exists")
	}
	sv, err := s.Store.GetService(ctx, l.ServiceID)
	if err != nil {
		return nil, 404, errors.New("service no longer exists")
	}
	res := providers.Result{Outcome: out, RetryAfter: time.Duration(in.RetryAfterSec) * time.Second, Message: in.Message}
	if err := s.Pool.Apply(ctx, sv, k, res); err != nil {
		return nil, 500, err
	}
	_ = s.Store.SetLeaseOutcome(ctx, l.ID, string(out))
	if out != providers.OK {
		s.Store.AddEvent(ctx, store.NewEvent{Kind: string(out), ServiceID: sv.ID, KeyID: k.ID, AppID: a.ID, StatusCode: in.StatusCode, Detail: in.Message})
	}
	if !in.Rotate || out == providers.OK {
		return nil, 200, nil
	}
	next, err := s.issueLease(ctx, a, sv, []int64{k.ID}, 0)
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
