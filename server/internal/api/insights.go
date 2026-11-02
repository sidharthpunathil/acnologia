package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// keyRow is a key plus the service it belongs to, for the all-keys view.
type keyRow struct {
	*store.Key
	ServiceSlug    string  `json:"service_slug"`
	ServiceName    string  `json:"service_name"`
	ServiceLogoURL *string `json:"service_logo_url"`
	HasHealthCheck bool    `json:"has_health_check"`
}

// listAllKeys powers the Keys page: every key across services with its
// validity, last use and quota, filterable and sortable.
func (s *Server) listAllKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.fillLogos(ctx, svcs...)
	q := r.URL.Query()
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	wantSvc, _ := strconv.ParseInt(q.Get("service_id"), 10, 64)
	wantValidity := q.Get("validity")
	rows := []keyRow{}
	// Members cannot see or search who a key belongs to.
	canSeeAccounts := userFrom(r).Role.AtLeast(store.RoleAdmin)
	for _, sv := range svcs {
		if wantSvc != 0 && sv.ID != wantSvc {
			continue
		}
		keys, err := s.Store.ListKeys(ctx, sv.ID, isSuper(r))
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.fillInFlight(keys...)
		maskAccounts(r, keys...)
		hc := providers.Get(sv.Provider).Health != nil
		for _, k := range keys {
			if wantValidity != "" && k.Validity != wantValidity {
				continue
			}
			hay := k.Label + " " + k.Hint + " " + sv.Name + " " + sv.Slug + " " + k.Notes
			if canSeeAccounts {
				hay += " " + k.AccountName + " " + k.AccountEmail + " " + k.AccountPhone + " " + k.AccountRef
			}
			if search != "" && !strings.Contains(strings.ToLower(hay), search) {
				continue
			}
			rows = append(rows, keyRow{Key: k, ServiceSlug: sv.Slug, ServiceName: sv.Name, ServiceLogoURL: sv.LogoURL, HasHealthCheck: hc})
		}
	}
	unix := func(t *time.Time) int64 {
		if t == nil {
			return 0
		}
		return t.Unix()
	}
	rank := map[string]int{"invalid": 0, "unknown": 1, "valid": 2}
	switch q.Get("sort") {
	case "quota":
		frac := func(k *store.Key) float64 {
			if k.QuotaRemaining == nil || k.QuotaLimit == nil || *k.QuotaLimit == 0 {
				return 2
			}
			return float64(*k.QuotaRemaining) / float64(*k.QuotaLimit)
		}
		sort.SliceStable(rows, func(i, j int) bool { return frac(rows[i].Key) < frac(rows[j].Key) })
	case "validity":
		sort.SliceStable(rows, func(i, j int) bool { return rank[rows[i].Validity] < rank[rows[j].Validity] })
	case "label":
		sort.SliceStable(rows, func(i, j int) bool { return strings.ToLower(rows[i].Label) < strings.ToLower(rows[j].Label) })
	default: // last used, most recent first
		sort.SliceStable(rows, func(i, j int) bool { return unix(rows[i].LastUsedAt) > unix(rows[j].LastUsedAt) })
	}
	sum := map[string]int{"total": len(rows), "valid": 0, "invalid": 0, "unknown": 0, "low_quota": 0, "unused_7d": 0}
	week := time.Now().Add(-7 * 24 * time.Hour)
	for _, row := range rows {
		sum[row.Validity]++
		if row.QuotaRemaining != nil && row.QuotaLimit != nil && *row.QuotaLimit > 0 && float64(*row.QuotaRemaining) < 0.1*float64(*row.QuotaLimit) {
			sum["low_quota"]++
		}
		if row.LastUsedAt == nil || row.LastUsedAt.Before(week) {
			sum["unused_7d"]++
		}
	}
	writeJSON(w, 200, map[string]any{"keys": rows, "summary": sum})
}

type dayStat struct {
	Day         string `json:"day"`
	Requests    int    `json:"requests"`
	Errors      int    `json:"errors"`
	RateLimited int    `json:"rate_limited"`
}

// keyInsights returns one key with its recent events and 14 days of activity.
func (s *Server) keyInsights(w http.ResponseWriter, r *http.Request) {
	k, sv, ok := s.loadKey(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	s.fillInFlight(k)
	maskAccounts(r, k)
	s.fillLogos(ctx, sv)
	evs, err := s.Store.ListEvents(ctx, store.EventFilter{KeyID: k.ID, Limit: 50})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	days, err := s.Store.KeyDailyStats(ctx, k.ID, 14)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	spec := providers.Get(sv.Provider)
	writeJSON(w, 200, map[string]any{
		"key": k,
		"service": map[string]any{"id": sv.ID, "slug": sv.Slug, "name": sv.Name, "logo_url": sv.LogoURL,
			"max_concurrency": sv.MaxConcurrency},
		"has_health_check": spec.Health != nil,
		"quota_unit":       spec.Quota.Unit,
		"events":           evs,
		"daily":            fillDays(days, 14),
	})
}

func fillDays(got []store.DayStat, n int) []dayStat {
	by := map[string]store.DayStat{}
	for _, d := range got {
		by[d.Day] = d
	}
	out := make([]dayStat, 0, n)
	for i := n - 1; i >= 0; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		d := by[day]
		out = append(out, dayStat{Day: day, Requests: d.Requests, Errors: d.Errors, RateLimited: d.RateLimited})
	}
	return out
}

type checkSummary struct {
	Checked   int          `json:"checked"`
	Valid     int          `json:"valid"`
	Invalid   int          `json:"invalid"`
	Exhausted int          `json:"exhausted"`
	Failed    int          `json:"failed"`
	Skipped   int          `json:"skipped"`
	Message   string       `json:"message"`
	Keys      []*store.Key `json:"keys"`
}

// checkKeys health checks keys four at a time and reports what it found.
func (s *Server) checkKeys(ctx context.Context, sv *store.Service, keys []*store.Key) checkSummary {
	sum := checkSummary{Keys: []*store.Key{}}
	if providers.Get(sv.Provider).Health == nil {
		sum.Skipped = len(keys)
		sum.Message = pool.ErrNoHealthCheck.Error()
		return sum
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, k := range keys {
		if k.Status == store.StatusDisabled || k.Status == store.StatusBanned || (k.Hidden && !sv.TargetTrusted) {
			mu.Lock()
			sum.Skipped++
			mu.Unlock()
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(k *store.Key) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			start := time.Now()
			err := s.Pool.Check(cctx, sv, k)
			cancel()
			latency := time.Since(start).Milliseconds()
			nk, gerr := s.Store.GetKey(context.Background(), k.ID)
			if gerr != nil {
				nk = nil
			}
			s.Store.AddEvent(context.Background(), store.NewEvent{Kind: "health", ServiceID: sv.ID, KeyID: k.ID,
				LatencyMS: latency, Detail: healthDetail(err, nk)})
			mu.Lock()
			defer mu.Unlock()
			sum.Checked++
			switch {
			case err != nil:
				sum.Failed++
			case gerr == nil && nk.Status == store.StatusInvalid:
				sum.Invalid++
			case gerr == nil && (nk.Status == store.StatusExhausted || (nk.QuotaRemaining != nil && *nk.QuotaRemaining <= 0)):
				sum.Exhausted++
			default:
				sum.Valid++
			}
			if gerr == nil {
				sum.Keys = append(sum.Keys, nk)
			}
		}(k)
	}
	wg.Wait()
	s.fillInFlight(sum.Keys...)
	sum.Message = checkMessage(sum)
	return sum
}

func (s *Server) checkServiceKeys(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	keys, err := s.Store.ListKeys(ctx, sv.ID, isSuper(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	sum := s.checkKeys(ctx, sv, keys)
	s.Store.AddAudit(r.Context(), userFrom(r), "keys.check", sv.Slug, sum.Message)
	writeJSON(w, 200, sum)
}

func (s *Server) checkAllKeys(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	total := checkSummary{Keys: []*store.Key{}}
	for _, sv := range svcs {
		keys, err := s.Store.ListKeys(ctx, sv.ID, isSuper(r))
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		sum := s.checkKeys(ctx, sv, keys)
		total.Checked += sum.Checked
		total.Valid += sum.Valid
		total.Invalid += sum.Invalid
		total.Failed += sum.Failed
		total.Skipped += sum.Skipped
		total.Exhausted += sum.Exhausted
		total.Keys = append(total.Keys, sum.Keys...)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		total.Message = "Stopped after 3 minutes. "
	}
	total.Message += checkMessage(total)
	if total.Skipped > 0 {
		total.Message += fmt.Sprintf(" Skipped %d (disabled, or the provider has no health check).", total.Skipped)
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "keys.check_all", "", total.Message)
	writeJSON(w, 200, total)
}

// healthDetail is the text a health event records: what the check found,
// read from the key after the check, or the error when it could not run.
func healthDetail(err error, k *store.Key) string {
	switch {
	case err != nil:
		return err.Error()
	case k == nil:
		return "checked"
	case k.Status == store.StatusInvalid:
		if why := strings.TrimPrefix(k.LastError, "invalid key: "); why != "" {
			return "invalid: " + why
		}
		return "invalid"
	case k.Status == store.StatusExhausted || (k.QuotaRemaining != nil && *k.QuotaRemaining <= 0):
		return "out of quota"
	default:
		return "valid"
	}
}

func checkMessage(s checkSummary) string {
	noun := "keys"
	if s.Checked == 1 {
		noun = "key"
	}
	msg := fmt.Sprintf("Checked %d %s: %d working, %d out of quota, %d invalid", s.Checked, noun, s.Valid, s.Exhausted, s.Invalid)
	if s.Failed > 0 {
		msg += fmt.Sprintf(", %d could not be reached", s.Failed)
	}
	return msg + "."
}
