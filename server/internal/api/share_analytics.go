package api

// Sharing analytics: GET /api/admin/shares/analytics. For every share, how
// long access has been live, how much it was used (requests, errors,
// latency, LLM tokens), what that cost in USD, and who used it: the other
// server, the person, or each claimant of a reusable link.
//
// Proxy shares are measured here: their calls go through this server. A
// handover share's keys are used on the other server, which reports request
// counts on sync; their cost is estimated from the provider's price. A
// reveal share hands the keys out, so its use is not visible at all.

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const analyticsMaxDays = 90

type analyticsDay struct {
	Day          string  `json:"day"`
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	Cost         float64 `json:"cost"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

type analyticsService struct {
	Slug         string  `json:"slug"`
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	Cost         float64 `json:"cost"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

// analyticsClaimant is one token of a share: on a reusable link, one claim.
type analyticsClaimant struct {
	TokenID    int64      `json:"token_id"`
	Name       string     `json:"name"`
	Requests   int64      `json:"requests"`
	Cost       float64    `json:"cost"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Revoked    bool       `json:"revoked"`
}

type analyticsShare struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Recipient string `json:"recipient"` // server, person or link
	// Who is the other server's name or the person's label.
	Who    string `json:"who"`
	Mode   string `json:"mode"`
	Status string `json:"status"`
	// Tracking is measured (proxy), reported (handover: the other server
	// counts) or none (reveal).
	Tracking  string     `json:"tracking"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at"`
	EndsAt    *time.Time `json:"ends_at"`
	EndedAt   *time.Time `json:"ended_at"`
	// LiveSeconds is how long access has been live: from the claim or
	// redeem to the end, or to now.
	LiveSeconds int64 `json:"live_seconds"`

	Requests     int64   `json:"requests"`
	CacheHits    int64   `json:"cache_hits"`
	Errors       int64   `json:"errors"`
	Cost         float64 `json:"cost"`
	CostEstimate bool    `json:"cost_estimate"`
	AvgLatencyMS int64   `json:"avg_latency_ms"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	// TokenCalls counts answers that reported tokens.
	TokenCalls  int64      `json:"token_calls"`
	FirstUsedAt *time.Time `json:"first_used_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	ActiveDays  int        `json:"active_days"`
	// KeysRevealed counts keys a reveal share showed.
	KeysRevealed int `json:"keys_revealed"`
	ClaimCount   int `json:"claim_count"`

	Services  []analyticsService  `json:"services"`
	Claimants []analyticsClaimant `json:"claimants"`
	Days      []analyticsDay      `json:"days"`
}

type analyticsTotals struct {
	Shares       int     `json:"shares"`
	LiveShares   int     `json:"live_shares"`
	Recipients   int     `json:"recipients"`
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	Cost         float64 `json:"cost"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	LiveSeconds  int64   `json:"live_seconds"`
}

type analyticsReport struct {
	Days          int              `json:"days"`
	Since         time.Time        `json:"since"`
	RetentionDays int              `json:"retention_days"`
	Totals        analyticsTotals  `json:"totals"`
	Series        []analyticsDay   `json:"series"`
	Shares        []analyticsShare `json:"shares"`
}

func (s *Server) shareAnalytics(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > analyticsMaxDays {
			writeErr(w, 400, "invalid", "days must be a whole number from 1 to "+strconv.Itoa(analyticsMaxDays))
			return
		}
		days = n
	}
	rep, err := s.buildShareAnalytics(r.Context(), time.Now(), days)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rep.RetentionDays = s.Cfg.EventRetentionDays
	writeJSON(w, 200, rep)
}

func (s *Server) buildShareAnalytics(ctx context.Context, now time.Time, days int) (analyticsReport, error) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days+1)
	rep := analyticsReport{Days: days, Since: start, Series: []analyticsDay{}, Shares: []analyticsShare{}}
	shs, err := s.Store.ListShares(ctx)
	if err != nil {
		return rep, err
	}
	series := map[string]*analyticsDay{}
	for d := start; !d.After(now); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		series[k] = &analyticsDay{Day: k}
	}
	whos := map[string]bool{}
	for _, sh := range shs {
		a := s.analyzeShare(ctx, sh, now, start)
		rep.Shares = append(rep.Shares, a)
		t := &rep.Totals
		t.Shares++
		if a.EndedAt == nil && a.StartedAt != nil && sh.Status == store.ShareActive {
			t.LiveShares++
		}
		if a.StartedAt != nil {
			whos[a.Recipient+"|"+a.Who] = true
		}
		t.Requests += a.Requests
		t.Errors += a.Errors
		t.Cost += a.Cost
		t.InputTokens += a.InputTokens
		t.OutputTokens += a.OutputTokens
		t.LiveSeconds += a.LiveSeconds
		for _, d := range a.Days {
			if sd := series[d.Day]; sd != nil {
				sd.Requests += d.Requests
				sd.Errors += d.Errors
				sd.Cost += d.Cost
				sd.InputTokens += d.InputTokens
				sd.OutputTokens += d.OutputTokens
			}
		}
	}
	rep.Totals.Recipients = len(whos)
	for _, d := range series {
		rep.Series = append(rep.Series, *d)
	}
	sort.Slice(rep.Series, func(i, j int) bool { return rep.Series[i].Day < rep.Series[j].Day })
	// Most used first, then newest.
	sort.SliceStable(rep.Shares, func(i, j int) bool {
		a, b := rep.Shares[i], rep.Shares[j]
		if a.Cost != b.Cost {
			return a.Cost > b.Cost
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
	return rep, nil
}

func (s *Server) analyzeShare(ctx context.Context, sh *store.Share, now, start time.Time) analyticsShare {
	a := analyticsShare{ID: sh.ID, Name: sh.Name, Recipient: sh.Recipient, Mode: sh.Mode, Status: sh.Status, CreatedAt: sh.CreatedAt,
		EndsAt: sh.ExpiresAt, ClaimCount: sh.ClaimCount, Services: []analyticsService{}, Claimants: []analyticsClaimant{}, Days: []analyticsDay{}}
	if a.Recipient == "" {
		a.Recipient = store.RecipientServer
	}
	if sh.IsPerson() {
		a.Who = sh.Label
		if sh.Reusable {
			a.Recipient = "link"
		}
	} else {
		a.Who = sh.PeerName
	}
	a.StartedAt = sh.ClaimedAt
	if a.StartedAt == nil {
		a.StartedAt = sh.RedeemedAt
	}
	switch {
	case sh.RevokedAt != nil:
		a.EndedAt = sh.RevokedAt
	case sh.LeftAt != nil:
		a.EndedAt = sh.LeftAt
	case sh.Status == store.ShareExpired && sh.ExpiresAt != nil:
		a.EndedAt = sh.ExpiresAt
	}
	if a.StartedAt != nil {
		end := now
		if a.EndedAt != nil {
			end = *a.EndedAt
		}
		if d := end.Sub(*a.StartedAt); d > 0 {
			a.LiveSeconds = int64(d / time.Second)
		}
	}

	switch {
	case sh.IsReveal():
		a.Tracking = "none"
		if rs, err := s.Store.ListReveals(ctx, sh.ID); err == nil {
			a.KeysRevealed = len(rs)
		}
		return a
	case sh.IsHandover():
		a.Tracking = "reported"
		s.handoverUsage(ctx, sh, &a)
		return a
	}
	a.Tracking = "measured"
	if sh.AppID == nil {
		return a
	}
	s.proxyUsage(ctx, *sh.AppID, start, &a)
	return a
}

// proxyUsage fills a proxy share's use from the event log and token table.
func (s *Server) proxyUsage(ctx context.Context, appID int64, start time.Time, a *analyticsShare) {
	rows, err := s.Store.AppActivity(ctx, appID, start)
	if err != nil {
		return
	}
	svcs := map[string]*analyticsService{}
	svcByID := map[int64]string{}
	days := map[string]*analyticsDay{}
	toks := map[int64]*analyticsClaimant{}
	var latSum, latN int64
	svc := func(slug string) *analyticsService {
		if slug == "" {
			slug = "(deleted)"
		}
		if v := svcs[slug]; v != nil {
			return v
		}
		v := &analyticsService{Slug: slug}
		svcs[slug] = v
		return v
	}
	day := func(d string) *analyticsDay {
		if v := days[d]; v != nil {
			return v
		}
		v := &analyticsDay{Day: d}
		days[d] = v
		return v
	}
	for _, r := range rows {
		svcByID[r.ServiceID] = r.ServiceSlug
		a.Requests += r.Requests
		a.CacheHits += r.CacheHits
		a.Errors += r.Errors
		a.Cost += r.Cost
		latSum += r.LatencySum
		latN += r.LatencyN
		sv := svc(r.ServiceSlug)
		sv.Requests += r.Requests
		sv.Errors += r.Errors
		sv.Cost += r.Cost
		d := day(r.Day)
		d.Requests += r.Requests
		d.Errors += r.Errors
		d.Cost += r.Cost
		if r.Requests > 0 {
			if a.FirstUsedAt == nil || r.First.Before(*a.FirstUsedAt) {
				f := r.First
				a.FirstUsedAt = &f
			}
			if a.LastUsedAt == nil || r.Last.After(*a.LastUsedAt) {
				l := r.Last
				a.LastUsedAt = &l
			}
		}
		if r.TokenID != 0 {
			c := toks[r.TokenID]
			if c == nil {
				c = &analyticsClaimant{TokenID: r.TokenID}
				toks[r.TokenID] = c
			}
			c.Requests += r.Requests
			c.Cost += r.Cost
			if r.Requests > 0 && (c.LastUsedAt == nil || r.Last.After(*c.LastUsedAt)) {
				l := r.Last
				c.LastUsedAt = &l
			}
		}
	}
	if latN > 0 {
		a.AvgLatencyMS = latSum / latN
	}
	if tds, err := s.Store.AppTokenDays(ctx, appID, start.Format("2006-01-02")); err == nil {
		for _, t := range tds {
			a.InputTokens += t.Input
			a.OutputTokens += t.Output
			a.TokenCalls += t.Calls
			slug, ok := svcByID[t.ServiceID]
			if !ok {
				if sv, err := s.Store.GetService(ctx, t.ServiceID); err == nil {
					slug = sv.Slug
				}
				svcByID[t.ServiceID] = slug
			}
			sv := svc(slug)
			sv.InputTokens += t.Input
			sv.OutputTokens += t.Output
			d := day(t.Day)
			d.InputTokens += t.Input
			d.OutputTokens += t.Output
		}
	}
	for _, d := range days {
		if d.Requests > 0 {
			a.ActiveDays++
		}
		a.Days = append(a.Days, *d)
	}
	sort.Slice(a.Days, func(i, j int) bool { return a.Days[i].Day < a.Days[j].Day })
	for _, v := range svcs {
		a.Services = append(a.Services, *v)
	}
	sort.Slice(a.Services, func(i, j int) bool { return a.Services[i].Requests > a.Services[j].Requests })

	// Name every token the share holds, used or not: on a reusable link
	// each claim is one token, so this is who claimed and who used it.
	if tl, err := s.Store.ListAppTokens(ctx, appID); err == nil {
		for _, t := range tl {
			c := toks[t.ID]
			if c == nil {
				c = &analyticsClaimant{TokenID: t.ID}
				toks[t.ID] = c
			}
			c.Name, c.Revoked = t.Name, t.RevokedAt != nil
		}
	}
	for _, c := range toks {
		if c.Name == "" {
			c.Name = "Token " + strconv.FormatInt(c.TokenID, 10)
		}
		a.Claimants = append(a.Claimants, *c)
	}
	sort.Slice(a.Claimants, func(i, j int) bool {
		if a.Claimants[i].Requests != a.Claimants[j].Requests {
			return a.Claimants[i].Requests > a.Claimants[j].Requests
		}
		return a.Claimants[i].TokenID < a.Claimants[j].TokenID
	})
}

// handoverUsage sums what the other server reported for each handed over
// key and prices it at the provider's per request price.
func (s *Server) handoverUsage(ctx context.Context, sh *store.Share, a *analyticsShare) {
	rows, err := s.Store.ListHandovers(ctx, sh.ID)
	if err != nil {
		return
	}
	svcs := map[string]*analyticsService{}
	for _, h := range rows {
		if h.ReportedRequests == nil {
			continue
		}
		n := *h.ReportedRequests
		var cost float64
		if sv, err := s.Store.GetServiceBySlug(ctx, h.Service); err == nil {
			cost = float64(n) * costPerRequest(sv, docSpec(sv), "", "")
		}
		v := svcs[h.Service]
		if v == nil {
			v = &analyticsService{Slug: h.Service}
			svcs[h.Service] = v
		}
		v.Requests += n
		v.Cost += cost
		a.Requests += n
		a.Cost += cost
		if h.ReportedAt != nil && (a.LastUsedAt == nil || h.ReportedAt.After(*a.LastUsedAt)) {
			t := *h.ReportedAt
			a.LastUsedAt = &t
		}
	}
	a.CostEstimate = a.Requests > 0
	for _, v := range svcs {
		a.Services = append(a.Services, *v)
	}
	sort.Slice(a.Services, func(i, j int) bool { return a.Services[i].Requests > a.Services[j].Requests })
}
