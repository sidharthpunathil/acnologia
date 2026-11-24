package worker

// Alerts the pool cannot raise on the request path: a key that started
// resting (key.cooling), a share whose timer is about to end
// (share.expiring) and an app near a limit (app.limit_near). Each goes out
// once per occurrence: a key once per rest, a share once per warning and
// end time, an app once per limit and period (kept in the store, so a
// restart does not send them again).

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// AlertInterval is how often resting keys are looked for. Share timers and
// app limits are looked at every fourth pass.
var AlertInterval = 15 * time.Second

// limitNear is the share of a limit or budget that raises app.limit_near.
const limitNear = 0.8

func (w *Worker) runAlerts(ctx context.Context) {
	t := time.NewTicker(AlertInterval)
	defer t.Stop()
	for i := 0; ; i++ {
		now := time.Now()
		w.CheckCooling(ctx, now)
		if i%4 == 0 {
			w.CheckShareExpiry(ctx, now)
			w.CheckAppLimits(ctx, now)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckCooling reports keys that started resting since the last pass. A
// key still in the rest it was reported for is not reported again.
func (w *Worker) CheckCooling(ctx context.Context, now time.Time) {
	keys, err := w.Store.CoolingKeys(ctx, now)
	if err != nil {
		slog.Warn("cannot list resting keys", "err", err)
		return
	}
	if w.cooling == nil {
		w.cooling = map[int64]time.Time{}
	}
	resting := make(map[int64]bool, len(keys))
	for _, k := range keys {
		resting[k.ID] = true
		if last, ok := w.cooling[k.ID]; ok && last.After(now) {
			// The same rest, perhaps made longer: already reported.
			if k.Until.After(last) {
				w.cooling[k.ID] = k.Until
			}
			continue
		}
		w.cooling[k.ID] = k.Until
		w.Pool.Notify.Emit(notify.Event{Type: notify.KeyCooling, ServiceID: k.ServiceID, Service: k.ServiceSlug,
			KeyID: k.ID, Key: k.Label,
			Message: fmt.Sprintf("key %s of %s is resting after a rate limit until %s", k.Label, k.ServiceSlug, k.Until.Format(time.RFC3339)),
			Data:    map[string]any{"until": k.Until, "reason": "rate_limited"}})
	}
	for id := range w.cooling {
		if !resting[id] {
			delete(w.cooling, id)
		}
	}
}

// shareWarnings are the warnings before a share's timer ends, latest first.
var shareWarnings = []struct {
	name   string
	before time.Duration
}{{"1h", time.Hour}, {"24h", 24 * time.Hour}}

// CheckShareExpiry warns about active shares whose timer ends within 24
// hours, and again within 1 hour. Moving the timer (extend) makes new
// warnings.
func (w *Worker) CheckShareExpiry(ctx context.Context, now time.Time) {
	shares, err := w.Store.ListShares(ctx)
	if err != nil {
		slog.Warn("cannot list shares", "err", err)
		return
	}
	for _, sh := range shares {
		if sh.Status != store.ShareActive || sh.ExpiresAt == nil || !sh.ExpiresAt.After(now) {
			continue
		}
		left := sh.ExpiresAt.Sub(now)
		for i, wn := range shareWarnings {
			if left > wn.before {
				continue
			}
			mark := "share.expiring:" + sh.ID + ":" + wn.name + ":" + strconv.FormatInt(sh.ExpiresAt.Unix(), 10)
			if fresh, err := w.Store.MarkOnce(ctx, mark); err != nil || !fresh {
				break
			}
			// The 1 hour warning covers the 24 hour one when both are due.
			for _, later := range shareWarnings[i+1:] {
				_, _ = w.Store.MarkOnce(ctx, "share.expiring:"+sh.ID+":"+later.name+":"+strconv.FormatInt(sh.ExpiresAt.Unix(), 10))
			}
			var appID int64
			if sh.AppID != nil {
				appID = *sh.AppID
			}
			w.Pool.Notify.Emit(notify.Event{Type: notify.ShareExpiring, AppID: appID, App: sh.Name,
				Message: fmt.Sprintf("share %s ends at %s, in %s", sh.Name, sh.ExpiresAt.Format(time.RFC3339), roundLeft(left)),
				Data: map[string]any{"share_id": sh.ID, "peer_id": sh.PeerID, "status": sh.Status, "mode": sh.Mode,
					"recipient": sh.Recipient, "ends_at": sh.ExpiresAt, "within": wn.name}})
			break
		}
	}
}

func roundLeft(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()+0.5))
	}
	return fmt.Sprintf("%.0f hours", d.Hours())
}

// CheckAppLimits warns about apps past 80% of a request limit or budget,
// once per limit and day or month.
func (w *Worker) CheckAppLimits(ctx context.Context, now time.Time) {
	apps, err := w.Store.ListApps(ctx)
	if err != nil {
		slog.Warn("cannot list apps", "err", err)
		return
	}
	now = now.UTC()
	day, month := now.Format("2006-01-02"), now.Format("2006-01")
	for _, a := range apps {
		l := a.Limits
		if !l.Any() {
			continue
		}
		u, err := w.Store.GetAppUsage(ctx, a.ID, now)
		if err != nil {
			continue
		}
		type check struct {
			limit, period string
			used, max     float64
		}
		var checks []check
		if l.DailyRequests != nil {
			checks = append(checks, check{"daily_requests", day, float64(u.TodayRequests), float64(*l.DailyRequests)})
		}
		if l.DailyBudget != nil {
			checks = append(checks, check{"daily_budget", day, u.TodayCost, *l.DailyBudget})
		}
		if l.MonthlyRequests != nil {
			checks = append(checks, check{"monthly_requests", month, float64(u.MonthRequests), float64(*l.MonthlyRequests)})
		}
		if l.MonthlyBudget != nil {
			checks = append(checks, check{"monthly_budget", month, u.MonthCost, *l.MonthlyBudget})
		}
		for _, c := range checks {
			if c.max <= 0 || c.used < limitNear*c.max {
				continue
			}
			fresh, err := w.Store.MarkOnce(ctx, "app.limit_near:"+strconv.FormatInt(a.ID, 10)+":"+c.limit+":"+c.period)
			if err != nil || !fresh {
				continue
			}
			pct := int(100 * c.used / c.max)
			w.Pool.Notify.Emit(notify.Event{Type: notify.AppLimitNear, AppID: a.ID, App: a.Name,
				Message: fmt.Sprintf("app %q used %d%% of its %s limit (%s of %s)", a.Name, pct, humanLimit(c.limit), num(c.limit, c.used), num(c.limit, c.max)),
				Data:    map[string]any{"limit": c.limit, "period": c.period, "used": c.used, "max": c.max, "percent": pct}})
		}
	}
}

func humanLimit(l string) string {
	switch l {
	case "daily_requests":
		return "daily request"
	case "monthly_requests":
		return "monthly request"
	case "daily_budget":
		return "daily budget"
	case "monthly_budget":
		return "monthly budget"
	}
	return l
}

func num(limit string, v float64) string {
	if limit == "daily_budget" || limit == "monthly_budget" {
		return fmt.Sprintf("$%.2f", v)
	}
	return strconv.FormatInt(int64(v), 10)
}
