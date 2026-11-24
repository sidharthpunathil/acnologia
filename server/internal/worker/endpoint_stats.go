package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// EndpointStatsEvery is how often proxied calls are folded into the per
// endpoint evidence.
var EndpointStatsEvery = time.Minute

// maxGenericTemplates caps the paths no catalog entry covers that one
// service keeps rows for; past it they share one row.
const maxGenericTemplates = 200

// otherTemplate is the row those extra paths share.
const otherTemplate = "/{other}"

func (w *Worker) runEndpointStats(ctx context.Context) {
	t := time.NewTicker(EndpointStatsEvery)
	defer t.Stop()
	w.FoldEndpointStats(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.FoldEndpointStats(ctx)
		}
	}
}

// FoldEndpointStats folds the proxy events written since the last fold.
func (w *Worker) FoldEndpointStats(ctx context.Context) {
	svcs, err := w.Store.ListServices(ctx)
	if err != nil {
		return
	}
	specs := make(map[int64]*providers.Spec, len(svcs))
	for _, sv := range svcs {
		specs[sv.ID] = providers.DocSpec(sv.Provider, sv.PeerProvider)
	}
	counts, _ := w.Store.CountServiceTemplates(ctx)
	if counts == nil {
		counts = map[int64]int{}
	}
	added := map[int64]map[string]bool{}
	tmpl := func(serviceID int64, method, path string) string {
		spec, ok := specs[serviceID]
		if !ok {
			return "" // the service is gone
		}
		t, matched := spec.EndpointKey(method, path)
		if matched {
			return t
		}
		seen := added[serviceID]
		if seen == nil {
			seen = map[string]bool{}
			added[serviceID] = seen
		}
		if !seen[method+" "+t] {
			if counts[serviceID] >= maxGenericTemplates {
				return otherTemplate
			}
			seen[method+" "+t] = true
			counts[serviceID]++
		}
		return t
	}
	// 50 batches of 2,000 a minute keeps up with ~1,600 calls a second;
	// a backlog past that is caught up over the next minutes.
	if n, err := w.Store.FoldEndpointStats(ctx, tmpl, 2000, 50); err != nil {
		slog.Warn("endpoint stats fold failed", "err", err)
	} else if n > 0 {
		slog.Debug("folded proxy events into endpoint stats", "events", n)
	}
}
