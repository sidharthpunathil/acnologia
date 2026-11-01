// Package worker runs periodic housekeeping: daily usage reset, reviving
// exhausted keys, health checks, and pruning old rows.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cogito-keys/cogito/internal/pool"
	"github.com/cogito-keys/cogito/internal/providers"
	"github.com/cogito-keys/cogito/internal/store"
)

type Worker struct {
	Store          *store.Store
	Pool           *pool.Pool
	HealthInterval time.Duration
	Retention      time.Duration

	lastPrune time.Time
}

// Run does housekeeping every 30 seconds. Health checks run in their own
// loop: they make one upstream call per key and can take minutes, which must
// not delay freeing expired lease slots or reviving exhausted keys.
func (w *Worker) Run(ctx context.Context) {
	if w.HealthInterval > 0 {
		go w.runHealth(ctx)
	}
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

func (w *Worker) runHealth(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	w.healthChecks(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.healthChecks(ctx)
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
	if n := w.Pool.SweepLeases(); n > 0 {
		slog.Info("released expired lease slots", "count", n)
	}
	if n, err := w.Store.ReactivateExhausted(ctx); err == nil && n > 0 {
		slog.Info("reactivated exhausted keys", "count", n)
	}
	if time.Since(w.lastPrune) >= 10*time.Minute {
		if err := w.Store.Prune(ctx, w.Retention); err != nil {
			slog.Warn("prune failed", "err", err)
		} else {
			w.lastPrune = time.Now()
		}
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
		if providers.Get(sv.Provider).Health == nil {
			continue // no free endpoint to test keys with
		}
		keys, err := w.Store.ListKeys(ctx, sv.ID, true)
		if err != nil {
			continue
		}
		for _, k := range keys {
			if k.Status == store.StatusDisabled || k.Status == store.StatusBanned || (k.Hidden && !sv.TargetTrusted) {
				continue
			}
			// A check is one more request; do not push a capped key over its limit.
			if lim := k.ConcurrencyLimit(sv); lim > 0 && w.Pool.InFlight()[k.ID] >= lim {
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
			if err := w.Pool.Check(cctx, sv, k); err != nil && !errors.Is(err, pool.ErrNoHealthCheck) {
				slog.Warn("health check failed", "service", sv.Slug, "key", k.ID, "err", err)
			}
			cancel()
		}
	}
}
