package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// AI is the part of the agent the refresh uses. *agent.Agent satisfies it;
// tests use a fake.
type AI interface {
	Enabled() bool
	ModelFor(ctx context.Context, feature string) agent.ModelChoice
	ResearchOffers(ctx context.Context, req agent.OffersRequest) (*agent.OffersResult, error)
	// Claude's own prices, a special target of every run.
	ResearchPrices(ctx context.Context, req agent.PricesRequest) (*agent.PricesResult, error)
	CurrentPrices(ctx context.Context) []agent.ModelPrice
	ApplyPrices(ctx context.Context, prices []agent.ModelPrice, by string) error
	MarkPricesChecked(ctx context.Context, source string) error
	PricesCheckedAt(ctx context.Context) time.Time
}

// PricesTarget is the item and review slug of Claude's model prices. It
// cannot clash with a provider: slugs start with a letter or digit.
const PricesTarget = "_claude_prices"

// Errors the API turns into answers.
var (
	ErrBusy      = errors.New("a provider refresh is already running; wait for it to finish")
	ErrBudget    = errors.New("the monthly refresh budget is spent; raise it in Settings or wait for next month")
	ErrNoneDue   = errors.New("no provider is due for a check: every provider in scope was skipped")
	ErrStale     = errors.New("the provider changed since this proposal was made; dismiss it and check again")
	ErrNotReview = errors.New("only a proposal waiting for review can be applied or dismissed")
)

// providerTimeout bounds one provider's check.
const providerTimeout = 5 * time.Minute

// keepRuns is how many runs the history keeps.
const keepRuns = 100

// Refresher runs provider refreshes, one at a time.
type Refresher struct {
	Store   *store.Store
	Catalog *catalog.Catalog
	AI      AI
	// Notify receives provider.offers_changed. Nil is fine.
	Notify *notify.Notifier
	// Now replaces the clock in tests.
	Now func() time.Time

	cache settingsCache
	mu    sync.Mutex
	// active is the id of the run executing now, 0 when none.
	active int64
	wg     sync.WaitGroup
	// decide serializes applying and dismissing reviews.
	decide sync.Mutex
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Wait blocks until runs started so far finish (tests).
func (r *Refresher) Wait() { r.wg.Wait() }

// Active is the id of the run executing now, 0 when none.
func (r *Refresher) Active() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// Tick is the worker's call, about once a minute. It resumes a run a restart
// interrupted, or starts the scheduled run when it is due. It never blocks.
func (r *Refresher) Tick(ctx context.Context) {
	if r.AI == nil || !r.AI.Enabled() || r.Active() != 0 {
		return
	}
	if run, err := r.Store.RunningRefreshRun(ctx); err == nil {
		r.mu.Lock()
		if r.active != 0 {
			r.mu.Unlock()
			return
		}
		r.active = run.ID
		r.mu.Unlock()
		slog.Info("resuming the provider refresh a restart interrupted", "run", run.ID)
		_ = r.Store.RequeueRefreshItems(ctx, run.ID)
		r.wg.Add(1)
		go r.execute(run.ID)
		return
	}
	next, ok := r.NextRun(ctx)
	if !ok || r.now().Before(next) {
		return
	}
	if _, err := r.Start(ctx, store.RefreshBySchedule, "system"); err != nil && !errors.Is(err, ErrBusy) {
		if errors.Is(err, ErrNoneDue) || errors.Is(err, ErrBudget) {
			slog.Info("scheduled provider refresh skipped", "reason", err)
		} else {
			slog.Warn("scheduled provider refresh failed to start", "err", err)
		}
	}
}

// NextRun is when the next scheduled run is due, false when the schedule is off.
func (r *Refresher) NextRun(ctx context.Context) (time.Time, bool) {
	var last *time.Time
	if run, err := r.Store.LastRefreshRun(ctx, store.RefreshBySchedule); err == nil {
		last = &run.StartedAt
	}
	return NextRun(r.Settings(ctx), last, r.since(ctx))
}

// since anchors the first scheduled run: when the settings were saved, or
// else when this server first looked at the schedule.
func (r *Refresher) since(ctx context.Context) time.Time {
	if s := r.Settings(ctx); s.UpdatedAt != nil {
		return *s.UpdatedAt
	}
	if t, err := time.Parse(time.RFC3339, r.Store.GetMeta(ctx, metaKey+"_since")); err == nil {
		return t
	}
	n := r.now()
	_ = r.Store.SetMeta(ctx, metaKey+"_since", n.Format(time.RFC3339))
	return n
}

// MonthSpend is what the refresh model has cost since the 1st of this month, UTC.
func (r *Refresher) MonthSpend(ctx context.Context) float64 {
	n := r.now()
	v, _ := r.Store.AISpendSince(ctx, agent.FeatureRefresh, time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC))
	return v
}

// Start plans a run over the providers in scope and starts it in the
// background. A scheduled run that finds nothing due is still recorded, so
// the schedule moves on; a run by hand returns ErrNoneDue instead.
func (r *Refresher) Start(ctx context.Context, trigger, by string) (*store.RefreshRun, error) {
	if !r.AI.Enabled() {
		return nil, agent.ErrDisabled
	}
	if r.busy(ctx) {
		return nil, ErrBusy
	}
	s := r.Settings(ctx)
	if r.MonthSpend(ctx) >= s.MaxMonthUSD {
		return nil, ErrBudget
	}
	slugs, skipped, err := r.Plan(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(slugs) == 0 && trigger != store.RefreshBySchedule {
		return nil, ErrNoneDue
	}
	return r.begin(ctx, trigger, by, slugs, skipped)
}

// CheckOne re-checks one provider now, whatever the scope and staleness say.
func (r *Refresher) CheckOne(ctx context.Context, slug, by string) (*store.RefreshRun, error) {
	if !r.AI.Enabled() {
		return nil, agent.ErrDisabled
	}
	if _, err := r.Store.GetProvider(ctx, slug); err != nil {
		return nil, err
	}
	if r.busy(ctx) {
		return nil, ErrBusy
	}
	if slug == providers.PeerSlug {
		return nil, errors.New("this provider stands for other servers' providers and has no offers of its own")
	}
	if r.MonthSpend(ctx) >= r.Settings(ctx).MaxMonthUSD {
		return nil, ErrBudget
	}
	return r.begin(ctx, store.RefreshOne, by, []string{slug}, nil)
}

// busy reports a run executing now, or one a restart interrupted.
func (r *Refresher) busy(ctx context.Context) bool {
	if r.Active() != 0 {
		return true
	}
	_, err := r.Store.RunningRefreshRun(ctx)
	return err == nil
}

func (r *Refresher) begin(ctx context.Context, trigger, by string, slugs []string, skipped []store.RefreshSkip) (*store.RefreshRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != 0 {
		return nil, ErrBusy
	}
	if _, err := r.Store.RunningRefreshRun(ctx); err == nil {
		return nil, ErrBusy // interrupted by a restart; the next tick resumes it
	}
	run, err := r.Store.CreateRefreshRun(ctx, trigger, by, r.AI.ModelFor(ctx, agent.FeatureRefresh).Model, slugs, skipped, r.now())
	if err != nil {
		return nil, err
	}
	r.active = run.ID
	r.wg.Add(1)
	go r.execute(run.ID)
	return run, nil
}

// Plan picks the providers a run checks, oldest checked first, and says why
// each provider in scope was left out.
func (r *Refresher) Plan(ctx context.Context, s Settings) ([]string, []store.RefreshSkip, error) {
	ps, err := r.Store.ListProviders(ctx)
	if err != nil {
		return nil, nil, err
	}
	states, err := r.Store.ProviderRefreshStates(ctx)
	if err != nil {
		return nil, nil, err
	}
	var scope map[string]bool
	switch s.Scope {
	case ScopeWithKeys:
		scope, err = r.Store.ProvidersWithUsableKeys(ctx)
	case ScopeWithServices:
		scope, err = r.Store.ProvidersWithServices(ctx)
	}
	if err != nil {
		return nil, nil, err
	}
	include, exclude := set(s.Include), set(s.Exclude)
	type cand struct {
		slug string
		last time.Time
	}
	cands := []cand{}
	skipped := []store.RefreshSkip{}
	now := r.now()
	for _, p := range ps {
		inc := include[p.Slug]
		if !inc && scope != nil && !scope[p.Slug] {
			continue
		}
		if p.Slug == providers.PeerSlug {
			continue // stands for other servers' providers; nothing to price
		}
		skip := func(reason string) { skipped = append(skipped, store.RefreshSkip{Slug: p.Slug, Reason: reason}) }
		switch {
		case exclude[p.Slug]:
			skip("excluded in Settings")
			continue
		case strings.HasPrefix(p.Source, "peer:") && !s.IncludePeer && !inc:
			skip("copied from another server, whose owner keeps it current")
			continue
		case p.Status == "disabled" && !inc:
			skip("disabled")
			continue
		}
		last := LastChecked(p, states[p.Slug])
		if s.StaleDays > 0 && !last.IsZero() && now.Sub(last) < time.Duration(s.StaleDays)*24*time.Hour {
			skip(fmt.Sprintf("checked %s, within %d days", last.Format("2006-01-02"), s.StaleDays))
			continue
		}
		cands = append(cands, cand{p.Slug, last})
	}
	if s.ClaudePrices {
		last := r.AI.PricesCheckedAt(ctx)
		if s.StaleDays > 0 && !last.IsZero() && now.Sub(last) < time.Duration(s.StaleDays)*24*time.Hour {
			skipped = append(skipped, store.RefreshSkip{Slug: PricesTarget, Reason: fmt.Sprintf("checked %s, within %d days", last.Format("2006-01-02"), s.StaleDays)})
		} else {
			// Zero time: it sorts first, and it does not count toward the
			// providers per run.
			cands = append(cands, cand{PricesTarget, time.Time{}})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if !cands[i].last.Equal(cands[j].last) {
			return cands[i].last.Before(cands[j].last)
		}
		return cands[i].slug < cands[j].slug
	})
	out := []string{}
	n := 0
	for _, c := range cands {
		if c.slug == PricesTarget {
			out = append(out, c.slug)
			continue
		}
		n++
		if n > s.MaxProviders {
			skipped = append(skipped, store.RefreshSkip{Slug: c.slug, Reason: fmt.Sprintf("over the limit of %d providers per run; a later run checks it", s.MaxProviders)})
			continue
		}
		out = append(out, c.slug)
	}
	return out, skipped, nil
}

// LastChecked is when a provider's offers were last read: the later of its
// last refresh check and the newest checked_at on its offers. Zero is never.
func LastChecked(p *store.Provider, st *store.ProviderRefresh) time.Time {
	var t time.Time
	if st != nil && !st.CheckedAt.IsZero() && st.Outcome != "" {
		t = st.CheckedAt
	}
	if spec, err := providers.DecodeSpec(p.Spec); err == nil {
		for _, o := range spec.Offers {
			if d, err := time.Parse("2006-01-02", o.CheckedAt); err == nil && d.After(t) {
				t = d
			}
		}
	}
	return t
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// execute works through a run's pending items. One provider failing never
// stops the others; a budget cap stops the run.
func (r *Refresher) execute(runID int64) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		r.active = 0
		r.mu.Unlock()
	}()
	ctx := context.Background()
	defer func() {
		if p := recover(); p != nil {
			slog.Error("provider refresh crashed", "run", runID, "panic", p)
			_ = r.Store.FinishRefreshRun(ctx, runID, store.RefreshFailed, "the run stopped on an internal error")
		}
	}()
	run, err := r.Store.GetRefreshRun(ctx, runID)
	if err != nil {
		return
	}
	s := r.Settings(ctx)
	caller := agent.Caller{Email: run.StartedBy}
	items, err := r.Store.RefreshItems(ctx, runID)
	if err != nil {
		_ = r.Store.FinishRefreshRun(ctx, runID, store.RefreshFailed, err.Error())
		return
	}
	runCost := 0.0
	for _, it := range items {
		runCost += it.CostUSD
	}
	for _, it := range items {
		if it.Status != store.ItemPending {
			continue
		}
		month := r.MonthSpend(ctx)
		if runCost >= s.MaxRunUSD {
			r.stop(ctx, runID, fmt.Sprintf("Stopped: this run reached its budget of $%.2f.", s.MaxRunUSD))
			return
		}
		if month >= s.MaxMonthUSD {
			r.stop(ctx, runID, fmt.Sprintf("Stopped: refresh reached its monthly budget of $%.2f.", s.MaxMonthUSD))
			return
		}
		budget := min(s.MaxRunUSD-runCost, s.MaxMonthUSD-month)
		cost, outOfBudget := r.checkProvider(ctx, run, it, s, caller, budget)
		runCost += cost
		if outOfBudget {
			r.stop(ctx, runID, "Stopped: the budget ran out while checking "+it.Slug+".")
			return
		}
	}
	_ = r.Store.FinishRefreshRun(ctx, runID, store.RefreshDone, "")
	_ = r.Store.PruneRefreshRuns(ctx, keepRuns)
}

func (r *Refresher) stop(ctx context.Context, runID int64, reason string) {
	slog.Info("provider refresh stopped", "run", runID, "reason", reason)
	_ = r.Store.FinishRefreshRun(ctx, runID, store.RefreshStopped, reason)
}

// checkProvider runs one item to its end and returns what it cost and
// whether it stopped on the budget.
func (r *Refresher) checkProvider(ctx context.Context, run *store.RefreshRun, it *store.RefreshItem, s Settings, caller agent.Caller, budget float64) (float64, bool) {
	_ = r.Store.StartRefreshItem(ctx, it.ID)
	if it.Slug == PricesTarget {
		return r.checkPrices(ctx, run, it, s, caller, budget)
	}
	fail := func(cost float64, msg string) {
		_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemFailed, Detail: msg, CostUSD: cost})
	}
	p, err := r.Store.GetProvider(ctx, it.Slug)
	if err != nil {
		fail(0, "The provider no longer exists.")
		return 0, false
	}
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	res, err := r.research(cctx, agent.OffersRequest{
		Provider: p, Caller: caller, Ref: agent.RefreshRef(run.ID), MaxCostUSD: budget,
		Progress: func(line string) { r.Store.RefreshItemProgress(ctx, it.ID, line) },
	}, it.ID)
	cost := 0.0
	if res != nil {
		cost = res.CostUSD
	}
	if err != nil {
		fail(cost, sentence(err.Error()))
		return cost, errors.Is(err, agent.ErrRefreshBudget)
	}
	if !res.Verified {
		msg := "The model could not read the provider's pricing pages, so nothing changed."
		if res.Summary != "" {
			msg += " " + sentence(res.Summary)
		}
		fail(cost, msg)
		return cost, false
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		fail(cost, "The provider's spec is unreadable.")
		return cost, false
	}
	d := DiffOffers(spec.Offers, res.Offers)
	if d.Empty() {
		// Nothing changed: move checked_at on the current version in place,
		// with no new version, so a built in provider stays unedited.
		if raw, err := withOffers(p.Spec, res.Offers); err == nil {
			if ok, err := r.Store.TouchProviderSpec(ctx, p.Slug, p.Version, raw); err == nil && ok {
				if sp, err := providers.DecodeSpec(raw); err == nil {
					providers.Set(p.Slug, sp)
				}
			}
		}
		_ = r.Store.MarkProviderChecked(ctx, p.Slug, run.Trigger, store.ItemUnchanged, run.ID, 0)
		_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemUnchanged, Detail: res.Summary, Diff: d, CostUSD: cost})
		return cost, false
	}
	auto, reason := false, "Apply is set to review in Settings."
	if s.Apply == ApplyAuto {
		auto, reason = AutoApply(d, res.Offers)
		if !auto {
			reason = "Not applied on its own: " + reason + "."
		}
	}
	if auto {
		note := truncate(fmt.Sprintf("offers refreshed (run %d): %s", run.ID, d.Summary()), 300)
		np, err := r.publish(ctx, p, res.Offers, "refresh", note)
		if err == nil {
			_ = r.Store.MarkProviderChecked(ctx, p.Slug, run.Trigger, store.ItemApplied, run.ID, int64(np.Version))
			_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemApplied, Detail: res.Summary, Diff: d, Version: int64(np.Version), CostUSD: cost})
			r.Store.AddAudit(ctx, nil, "provider.refresh.apply", p.Slug, fmt.Sprintf("v%d applied on its own by run %d: %s", np.Version, run.ID, d.Summary()))
			r.emit(np, d, "auto")
			return cost, false
		}
		reason = "Publishing failed, so it waits for review: " + sentence(err.Error())
	}
	offers, _ := json.Marshal(res.Offers)
	diff, _ := json.Marshal(d)
	id, err := r.Store.CreateRefreshReview(ctx, &store.RefreshReview{Slug: p.Slug, RunID: run.ID, BaseVersion: int64(p.Version),
		Offers: offers, Diff: diff, Summary: res.Summary, Reason: reason})
	if err != nil {
		fail(cost, "Could not save the proposal: "+err.Error())
		return cost, false
	}
	_ = r.Store.MarkProviderChecked(ctx, p.Slug, run.Trigger, store.ItemReview, run.ID, 0)
	_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemReview, Detail: res.Summary, Diff: d, ReviewID: id, CostUSD: cost})
	return cost, false
}

// research calls the model and keeps the item's running cost current.
func (r *Refresher) research(ctx context.Context, req agent.OffersRequest, itemID int64) (res *agent.OffersResult, err error) {
	defer func() {
		if res != nil {
			r.Store.RefreshItemCost(context.WithoutCancel(ctx), itemID, res.CostUSD)
		}
	}()
	return r.AI.ResearchOffers(ctx, req)
}

// publish writes a new provider version with the offers replaced and the
// rest of the spec untouched. The provider keeps its source; the version is
// labelled source.
func (r *Refresher) publish(ctx context.Context, p *store.Provider, offers []providers.Offer, by, note string) (*store.Provider, error) {
	raw, err := withOffers(p.Spec, offers)
	if err != nil {
		return nil, err
	}
	in := catalog.Input{Slug: p.Slug, Name: p.Name, Category: p.Category, Description: p.Description, Website: p.Website,
		DocsURL: p.DocsURL, Color: p.Color, Status: p.Status, Spec: raw}
	np, _, err := r.Catalog.PublishVersion(ctx, in, "refresh", by, note)
	return np, err
}

// withOffers replaces the offers in a raw spec and keeps every other field
// byte for byte.
func withOffers(spec json.RawMessage, offers []providers.Offer) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(spec, &m); err != nil {
		return nil, err
	}
	if offers == nil {
		offers = []providers.Offer{}
	}
	b, err := json.Marshal(offers)
	if err != nil {
		return nil, err
	}
	m["offers"] = b
	return json.Marshal(m)
}

func (r *Refresher) emit(p *store.Provider, d Diff, how string) {
	r.Notify.Emit(notify.Event{Type: notify.ProviderOffersChanged,
		Message: fmt.Sprintf("%s offers changed: %s", p.Name, d.Summary()),
		Data:    map[string]any{"provider": p.Slug, "version": p.Version, "applied": how, "diff": d}})
}

// ApplyReview publishes a pending proposal as a new provider version.
func (r *Refresher) ApplyReview(ctx context.Context, id int64, by string) (*store.RefreshReview, *store.Provider, error) {
	r.decide.Lock()
	defer r.decide.Unlock()
	rv, err := r.Store.GetRefreshReview(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if rv.Status != store.ReviewPending {
		return nil, nil, ErrNotReview
	}
	if rv.Slug == PricesTarget {
		var prices []agent.ModelPrice
		if err := json.Unmarshal(rv.Offers, &prices); err != nil {
			return nil, nil, err
		}
		if err := r.AI.ApplyPrices(ctx, prices, by); err != nil {
			return nil, nil, err
		}
		if _, err := r.Store.DecideRefreshReview(ctx, id, store.ReviewApplied, by, 0); err != nil {
			return nil, nil, err
		}
		rv, _ = r.Store.GetRefreshReview(ctx, id)
		return rv, nil, nil
	}
	p, err := r.Store.GetProvider(ctx, rv.Slug)
	if err != nil {
		return nil, nil, err
	}
	if int64(p.Version) != rv.BaseVersion {
		return nil, nil, ErrStale
	}
	var offers []providers.Offer
	if err := json.Unmarshal(rv.Offers, &offers); err != nil {
		return nil, nil, err
	}
	var d Diff
	_ = json.Unmarshal(rv.Diff, &d)
	note := truncate(fmt.Sprintf("offers refreshed (review %d): %s", rv.ID, d.Summary()), 300)
	np, err := r.publish(ctx, p, offers, by, note)
	if err != nil {
		return nil, nil, err
	}
	if _, err := r.Store.DecideRefreshReview(ctx, id, store.ReviewApplied, by, int64(np.Version)); err != nil {
		return nil, nil, err
	}
	_ = r.Store.MarkProviderChanged(ctx, rv.Slug, int64(np.Version))
	r.emit(np, d, "review")
	rv, _ = r.Store.GetRefreshReview(ctx, id)
	return rv, np, nil
}

// DismissReview drops a pending proposal.
func (r *Refresher) DismissReview(ctx context.Context, id int64, by string) (*store.RefreshReview, error) {
	r.decide.Lock()
	defer r.decide.Unlock()
	ok, err := r.Store.DecideRefreshReview(ctx, id, store.ReviewDismissed, by, 0)
	if err != nil {
		return nil, err
	}
	if !ok {
		if _, err := r.Store.GetRefreshReview(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrNotReview
	}
	return r.Store.GetRefreshReview(ctx, id)
}

func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func truncate(s string, n int) string {
	if len(s) > n {
		return store.TruncateUTF8(s, n) + "..."
	}
	return s
}

// checkPrices re-reads Claude's model prices: the same model, budget and
// apply policy as a provider's offers.
func (r *Refresher) checkPrices(ctx context.Context, run *store.RefreshRun, it *store.RefreshItem, s Settings, caller agent.Caller, budget float64) (float64, bool) {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	cur := r.AI.CurrentPrices(ctx)
	res, err := r.AI.ResearchPrices(cctx, agent.PricesRequest{Caller: caller, Ref: agent.RefreshRef(run.ID), MaxCostUSD: budget,
		Progress: func(line string) { r.Store.RefreshItemProgress(ctx, it.ID, line) }})
	cost := 0.0
	if res != nil {
		cost = res.CostUSD
		r.Store.RefreshItemCost(ctx, it.ID, cost)
	}
	fail := func(msg string) {
		_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemFailed, Detail: msg, CostUSD: cost})
	}
	if err != nil {
		fail(sentence(err.Error()))
		return cost, errors.Is(err, agent.ErrRefreshBudget)
	}
	if !res.Verified {
		fail("The model could not read Anthropic's pricing page, so nothing changed.")
		return cost, false
	}
	d, changed := DiffPrices(cur, res.Prices)
	if d.Empty() {
		_ = r.AI.MarkPricesChecked(ctx, firstSource(res.Prices))
		_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemUnchanged, Detail: res.Summary, Diff: d, CostUSD: cost})
		return cost, false
	}
	auto, reason := false, "Apply is set to review in Settings."
	if s.Apply == ApplyAuto {
		auto, reason = AutoApplyPrices(changed)
		if !auto {
			reason = "Not applied on its own: " + reason + "."
		}
	}
	if auto {
		err := r.AI.ApplyPrices(ctx, changed, "refresh")
		if err == nil {
			_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemApplied, Detail: res.Summary, Diff: d, CostUSD: cost})
			r.Store.AddAudit(ctx, nil, "ai.prices", "refresh", fmt.Sprintf("applied on its own by run %d: %s", run.ID, d.Summary()))
			return cost, false
		}
		reason = "Saving failed, so it waits for review: " + sentence(err.Error())
	}
	prices, _ := json.Marshal(changed)
	diff, _ := json.Marshal(d)
	id, err := r.Store.CreateRefreshReview(ctx, &store.RefreshReview{Slug: PricesTarget, RunID: run.ID, Offers: prices, Diff: diff, Summary: res.Summary, Reason: reason})
	if err != nil {
		fail("Could not save the proposal: " + err.Error())
		return cost, false
	}
	_ = r.Store.FinishRefreshItem(ctx, it.ID, store.ItemResult{Status: store.ItemReview, Detail: res.Summary, Diff: d, ReviewID: id, CostUSD: cost})
	return cost, false
}

func firstSource(ps []agent.ModelPrice) string {
	for _, p := range ps {
		if p.SourceURL != "" {
			return p.SourceURL
		}
	}
	return ""
}
