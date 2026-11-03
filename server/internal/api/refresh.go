package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/refresh"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// serverAI hands the refresh the server's current agent at each call, so a
// test (or anything else) that swaps s.Agent is followed.
type serverAI struct{ s *Server }

func (a serverAI) Enabled() bool { return a.s.Agent != nil && a.s.Agent.Enabled() }
func (a serverAI) ModelFor(ctx context.Context, f string) agent.ModelChoice {
	return a.s.Agent.ModelFor(ctx, f)
}
func (a serverAI) ResearchPrices(ctx context.Context, req agent.PricesRequest) (*agent.PricesResult, error) {
	return a.s.Agent.ResearchPrices(ctx, req)
}
func (a serverAI) CurrentPrices(ctx context.Context) []agent.ModelPrice {
	return a.s.Agent.CurrentPrices(ctx)
}
func (a serverAI) ApplyPrices(ctx context.Context, p []agent.ModelPrice, by string) error {
	return a.s.Agent.ApplyPrices(ctx, p, by)
}
func (a serverAI) MarkPricesChecked(ctx context.Context, source string) error {
	return a.s.Agent.MarkPricesChecked(ctx, source)
}
func (a serverAI) PricesCheckedAt(ctx context.Context) time.Time {
	return a.s.Agent.PricesCheckedAt(ctx)
}
func (a serverAI) ResearchOffers(ctx context.Context, req agent.OffersRequest) (*agent.OffersResult, error) {
	return a.s.Agent.ResearchOffers(ctx, req)
}

// ProviderRefresher is the offers refresh, for the worker's schedule.
func (s *Server) ProviderRefresher() *refresh.Refresher { return s.Refresh }

// refreshModel is the refresh slot's effective model with its list price.
type refreshModel struct {
	agent.ModelChoice
	InputPrice   float64 `json:"input_price"`
	OutputPrice  float64 `json:"output_price"`
	PricingKnown bool    `json:"pricing_known"`
}

// refreshState is the Settings card: the settings, the model, and how the
// schedule stands.
type refreshState struct {
	Settings       refresh.Settings  `json:"settings"`
	Defaults       refresh.Settings  `json:"defaults"`
	AIEnabled      bool              `json:"ai_enabled"`
	Model          refreshModel      `json:"model"`
	NextRun        *time.Time        `json:"next_run"`
	LastRun        *store.RefreshRun `json:"last_run"`
	Running        *store.RefreshRun `json:"running"`
	MonthSpendUSD  float64           `json:"month_spend_usd"`
	PendingReviews int               `json:"pending_reviews"`
	CanEdit        bool              `json:"can_edit"`
}

func (s *Server) refreshState(r *http.Request) refreshState {
	ctx := r.Context()
	st := refreshState{Settings: s.Refresh.Settings(ctx), Defaults: refresh.Defaults(), AIEnabled: s.Agent.Enabled(),
		MonthSpendUSD: s.Refresh.MonthSpend(ctx), CanEdit: isSuper(r)}
	m := s.Agent.ModelFor(ctx, agent.FeatureRefresh)
	st.Model.ModelChoice = m
	if p, ok := s.Agent.PriceOf(m.Model); ok {
		st.Model.InputPrice, st.Model.OutputPrice, st.Model.PricingKnown = p.Input, p.Output, true
	}
	if next, ok := s.Refresh.NextRun(ctx); ok && st.AIEnabled {
		st.NextRun = &next
	}
	if runs, err := s.Store.ListRefreshRuns(ctx, 1); err == nil && len(runs) > 0 {
		st.LastRun = runs[0]
	}
	if run, err := s.Store.RunningRefreshRun(ctx); err == nil {
		st.Running = run
	}
	if rv, err := s.Store.ListRefreshReviews(ctx, store.ReviewPending, 200); err == nil {
		st.PendingReviews = len(rv)
	}
	return st
}

func (s *Server) getRefreshSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.refreshState(r))
}

func (s *Server) putRefreshSettings(w http.ResponseWriter, r *http.Request) {
	in := refresh.Defaults()
	if !decode(w, r, &in) {
		return
	}
	u := userFrom(r)
	old, saved, err := s.Refresh.SaveSettings(r.Context(), in, u.Email)
	if err != nil {
		if errors.Is(err, refresh.ErrInvalid) {
			writeErr(w, 400, "invalid", err.Error())
			return
		}
		writeStoreErr(w, err)
		return
	}
	if d := old.Diff(saved); d != "" {
		s.Store.AddAudit(r.Context(), u, "provider.refresh.settings", "", d)
	}
	writeJSON(w, 200, s.refreshState(r))
}

func writeRefreshErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrDisabled):
		writeErr(w, 503, "ai_disabled", err.Error())
	case errors.Is(err, refresh.ErrBusy):
		writeErr(w, 409, "refresh_busy", err.Error())
	case errors.Is(err, refresh.ErrBudget):
		writeErr(w, 409, "refresh_budget", err.Error())
	case errors.Is(err, refresh.ErrNoneDue):
		writeErr(w, 409, "nothing_due", err.Error())
	case errors.Is(err, refresh.ErrStale):
		writeErr(w, 409, "stale", err.Error())
	case errors.Is(err, refresh.ErrNotReview):
		writeErr(w, 409, "not_pending", err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeStoreErr(w, err)
	default:
		writeErr(w, 400, "refresh_error", err.Error())
	}
}

// runRefresh starts a run over the providers in scope, by hand.
func (s *Server) runRefresh(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	run, err := s.Refresh.Start(r.Context(), store.RefreshByHand, u.Email)
	if err != nil {
		writeRefreshErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), u, "provider.refresh.run", "", fmt.Sprintf("run %d, %d providers", run.ID, run.Providers))
	writeJSON(w, 202, map[string]any{"run": run})
}

// checkProviderOffers re-checks one provider now.
func (s *Server) checkProviderOffers(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	slug := chi.URLParam(r, "slug")
	run, err := s.Refresh.CheckOne(r.Context(), slug, u.Email)
	if err != nil {
		writeRefreshErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), u, "provider.refresh.check", slug, fmt.Sprintf("run %d", run.ID))
	writeJSON(w, 202, map[string]any{"run": run})
}

func (s *Server) listRefreshRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.ListRefreshRuns(r.Context(), 20)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

func (s *Server) getRefreshRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRefreshRun(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	items, err := s.Store.RefreshItems(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"run": run, "items": items})
}

func (s *Server) listRefreshReviews(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "":
		status = store.ReviewPending
	case "all":
		status = ""
	}
	rv, err := s.Store.ListRefreshReviews(r.Context(), status, 50)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"reviews": rv})
}

func (s *Server) applyRefreshReview(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	u := userFrom(r)
	rv, p, err := s.Refresh.ApplyReview(r.Context(), id, u.Email)
	if err != nil {
		s.writeReviewErr(w, err)
		return
	}
	if p == nil {
		s.Store.AddAudit(r.Context(), u, "ai.prices", "refresh", fmt.Sprintf("review %d applied", rv.ID))
		writeJSON(w, 200, map[string]any{"review": rv})
		return
	}
	s.Store.AddAudit(r.Context(), u, "provider.refresh.apply", rv.Slug, fmt.Sprintf("review %d applied as v%d", rv.ID, p.Version))
	writeJSON(w, 200, map[string]any{"review": rv, "provider": toDTO(p)})
}

func (s *Server) dismissRefreshReview(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	u := userFrom(r)
	rv, err := s.Refresh.DismissReview(r.Context(), id, u.Email)
	if err != nil {
		writeRefreshErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), u, "provider.refresh.dismiss", rv.Slug, fmt.Sprintf("review %d dismissed", rv.ID))
	writeJSON(w, 200, map[string]any{"review": rv})
}

// writeReviewErr reports a failed publish with its validation detail.
func (s *Server) writeReviewErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, refresh.ErrStale), errors.Is(err, refresh.ErrNotReview), errors.Is(err, store.ErrNotFound):
		writeRefreshErr(w, err)
	default:
		s.writePublishErr(w, err)
	}
}
