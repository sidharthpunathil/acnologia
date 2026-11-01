// Package worker runs periodic housekeeping: daily usage reset, reviving
// exhausted keys, health checks, and pruning old rows.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/store"
)

type Worker struct {
	Store          *store.Store
	Pool           *pool.Pool
	HealthInterval time.Duration
	Retention      time.Duration
}

func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	today := time.Now().UTC().Format("2006-01-02")
	if w.Store.GetMeta(ctx, "usage_day") != today {
		if err := w.Store.ResetDailyUsage(ctx); err == nil {
			_ = w.Store.SetMeta(ctx, "usage_day", today)
		}
	}
	if n, err := w.Store.ReactivateExhausted(ctx); err == nil && n > 0 {
		slog.Info("reactivated exhausted keys", "count", n)
	}
	if time.Now().Minute()%10 == 0 && time.Now().Second() < 30 {
		if err := w.Store.Prune(ctx, w.Retention); err != nil {
			slog.Warn("prune failed", "err", err)
		}
	}
	if w.HealthInterval > 0 {
		w.healthChecks(ctx)
	}
}

// healthChecks re-checks keys whose last check is older than the interval.
// Exhausted keys are rechecked hourly so they come back as soon as quota resets.
func (w *Worker) healthChecks(ctx context.Context) {
	svcs, err := w.Store.ListServices(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, sv := range svcs {
		if sv.Provider == "generic" {
			continue
		}
		keys, err := w.Store.ListKeys(ctx, sv.ID, true)
		if err != nil {
			continue
		}
		for _, k := range keys {
			if k.Status == store.StatusDisabled {
				continue
			}
			interval := w.HealthInterval
			if k.Status == store.StatusExhausted && interval > time.Hour {
				interval = time.Hour
			}
			if k.LastCheckedAt != nil && now.Sub(*k.LastCheckedAt) < interval {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			if err := w.Pool.Check(cctx, sv, k); err != nil {
				slog.Warn("health check failed", "service", sv.Slug, "key", k.ID, "err", err)
			}
			cancel()
		}
	}
}
