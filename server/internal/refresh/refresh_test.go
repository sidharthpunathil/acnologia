package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// fakeAI answers ResearchOffers from a function and records the calls.
type fakeAI struct {
	mu      sync.Mutex
	off     bool
	calls   []string
	answer  func(p *store.Provider, spec *providers.Spec) (*agent.OffersResult, error)
	release chan struct{} // when set, every call waits for it

	prices       []agent.ModelPrice
	priceAnswer  *agent.PricesResult
	pricesAt     time.Time
	appliedPrice []agent.ModelPrice
}

func (f *fakeAI) ResearchPrices(ctx context.Context, req agent.PricesRequest) (*agent.PricesResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, PricesTarget)
	f.mu.Unlock()
	if f.priceAnswer != nil {
		return f.priceAnswer, nil
	}
	return &agent.PricesResult{Verified: true, Prices: f.prices, CostUSD: 0.01}, nil
}
func (f *fakeAI) CurrentPrices(context.Context) []agent.ModelPrice { return f.prices }
func (f *fakeAI) ApplyPrices(_ context.Context, ps []agent.ModelPrice, _ string) error {
	f.appliedPrice = append(f.appliedPrice, ps...)
	for _, p := range ps {
		for i := range f.prices {
			if f.prices[i].Model == p.Model {
				f.prices[i] = p
			}
		}
	}
	return nil
}
func (f *fakeAI) MarkPricesChecked(context.Context, string) error { f.pricesAt = testNow; return nil }
func (f *fakeAI) PricesCheckedAt(context.Context) time.Time       { return f.pricesAt }

func (f *fakeAI) Enabled() bool { return !f.off }
func (f *fakeAI) ModelFor(context.Context, string) agent.ModelChoice {
	return agent.ModelChoice{Model: "claude-haiku-5-5", Source: agent.SourceOverride}
}
func (f *fakeAI) ResearchOffers(ctx context.Context, req agent.OffersRequest) (*agent.OffersResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req.Provider.Slug)
	f.mu.Unlock()
	if f.release != nil {
		<-f.release
	}
	spec, _ := providers.DecodeSpec(req.Provider.Spec)
	if f.answer == nil {
		return &agent.OffersResult{Verified: true, Offers: stamp(spec.Offers), CostUSD: 0.01}, nil
	}
	return f.answer(req.Provider, spec)
}

func (f *fakeAI) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

// stamp copies offers with today's checked_at, as the agent does.
func stamp(os []providers.Offer) []providers.Offer {
	out := append([]providers.Offer{}, os...)
	for i := range out {
		out[i].CheckedAt = "2027-01-15"
	}
	return out
}

var testNow = time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)

type tenv struct {
	t  *testing.T
	st *store.Store
	r  *Refresher
	ai *fakeAI
}

func newTEnv(t *testing.T) *tenv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat := &catalog.Catalog{Store: st}
	if err := cat.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	ai := &fakeAI{}
	r := &Refresher{Store: st, Catalog: cat, AI: ai, Now: func() time.Time { return testNow }}
	e := &tenv{t: t, st: st, r: r, ai: ai}
	e.save(func(*Settings) {})
	return e
}

// service adds a service on provider with one key per status.
func (e *tenv) service(provider string, statuses ...string) {
	e.t.Helper()
	ctx := context.Background()
	p, err := e.st.GetProvider(ctx, provider)
	if err != nil {
		e.t.Fatal(err)
	}
	sv, err := catalog.ServiceDefaults(p)
	if err != nil {
		e.t.Fatal(err)
	}
	sv.Slug = catalog.UniqueServiceSlug(ctx, e.st, sv.Slug)
	created, err := e.st.CreateService(ctx, sv)
	if err != nil {
		e.t.Fatal(err)
	}
	for i, s := range statuses {
		if _, err := e.st.DB.Exec(`INSERT INTO keys(service_id,label,secret_enc,secret_fp,hint,status,created_at) VALUES(?,?,?,?,?,?,?)`,
			created.ID, "k", []byte("x"), created.Slug+s+string(rune('a'+i)), "...x", s, 1); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *tenv) save(mut func(*Settings)) {
	e.t.Helper()
	s := Defaults()
	s.ClaudePrices = false // the price target has its own test
	mut(&s)
	if _, _, err := e.r.SaveSettings(context.Background(), s, "root@x.io"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *tenv) plan() ([]string, map[string]string) {
	e.t.Helper()
	slugs, skipped, err := e.r.Plan(context.Background(), e.r.Settings(context.Background()))
	if err != nil {
		e.t.Fatal(err)
	}
	why := map[string]string{}
	for _, s := range skipped {
		why[s.Slug] = s.Reason
	}
	return slugs, why
}

func eq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestScopeSelection(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.service("serpapi", store.StatusActive)
	e.service("tavily", store.StatusCooling)
	e.service("exa", store.StatusExhausted)
	e.service("brave-search", store.StatusInvalid, store.StatusBanned, store.StatusDisabled)
	e.service("jina") // a service with no keys

	// with_keys: only providers with a usable key.
	slugs, _ := e.plan()
	if !eq(slugs, []string{"exa", "serpapi", "tavily"}) {
		t.Fatalf("with_keys: %v", slugs)
	}
	// with_services adds services without usable keys.
	e.save(func(s *Settings) { s.Scope = ScopeWithServices })
	if slugs, _ = e.plan(); !eq(slugs, []string{"brave-search", "exa", "jina", "serpapi", "tavily"}) {
		t.Fatalf("with_services: %v", slugs)
	}
	// Include adds a provider outside the scope; exclude wins over both.
	e.save(func(s *Settings) { s.Include = []string{"firecrawl", "exa"}; s.Exclude = []string{"exa", "tavily"} })
	slugs, why := e.plan()
	if !eq(slugs, []string{"firecrawl", "serpapi"}) || why["exa"] == "" || why["tavily"] == "" {
		t.Fatalf("include/exclude: %v %v", slugs, why)
	}
	// all: every provider but the peer stand in.
	e.save(func(s *Settings) { s.Scope = ScopeAll; s.MaxProviders = 100 })
	slugs, _ = e.plan()
	for _, s := range slugs {
		if s == providers.PeerSlug {
			t.Fatal("the peer provider is never checked")
		}
	}
	if len(slugs) < 10 {
		t.Fatalf("all: %v", slugs)
	}

	// A provider copied from a peer server is skipped unless asked for.
	p, _ := e.st.GetProvider(ctx, "serpapi")
	p.Source = "peer:example.com"
	if _, err := e.st.SaveProvider(ctx, p, "x", "pulled"); err != nil {
		t.Fatal(err)
	}
	e.save(func(s *Settings) {})
	slugs, why = e.plan()
	if !eq(slugs, []string{"exa", "tavily"}) || !strings.Contains(why["serpapi"], "another server") {
		t.Fatalf("peer skip: %v %v", slugs, why)
	}
	e.save(func(s *Settings) { s.IncludePeer = true })
	if slugs, _ = e.plan(); !eq(slugs, []string{"exa", "serpapi", "tavily"}) {
		t.Fatalf("include peer: %v", slugs)
	}

	// Staleness: a provider checked recently is skipped; oldest goes first.
	if err := e.st.MarkProviderChecked(ctx, "tavily", store.RefreshBySchedule, store.ItemUnchanged, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.Exec(`UPDATE provider_refresh SET checked_at=? WHERE slug='tavily'`, testNow.AddDate(0, 0, -3).Unix()); err != nil {
		t.Fatal(err)
	}
	e.save(func(s *Settings) { s.IncludePeer = true; s.StaleDays = 25 })
	slugs, why = e.plan()
	if eq(slugs, nil) || strings.Contains(strings.Join(slugs, ","), "tavily") || !strings.Contains(why["tavily"], "within 25 days") {
		t.Fatalf("staleness: %v %v", slugs, why)
	}
	e.save(func(s *Settings) { s.IncludePeer = true; s.StaleDays = 0 })
	if slugs, _ = e.plan(); slugs[len(slugs)-1] != "tavily" {
		t.Fatalf("the most recently checked goes last: %v", slugs)
	}
	// Max providers per run.
	e.save(func(s *Settings) { s.IncludePeer = true; s.StaleDays = 0; s.MaxProviders = 1 })
	slugs, why = e.plan()
	if len(slugs) != 1 || len(why) < 2 {
		t.Fatalf("max providers: %v %v", slugs, why)
	}
}

func TestNextRun(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ptr := func(s string) *time.Time { v := at(s); return &v }
	cases := []struct {
		name  string
		s     Settings
		last  *time.Time
		since string
		want  string
		isOff bool
	}{
		{"off", Settings{Schedule: ScheduleOff}, nil, "2027-01-15T12:00:00Z", "", true},
		{"first run later today", Settings{Schedule: ScheduleWeekly, Hour: 18}, nil, "2027-01-15T12:00:00Z", "2027-01-15T18:00:00Z", false},
		{"first run tomorrow", Settings{Schedule: ScheduleMonthly, Hour: 3}, nil, "2027-01-15T12:00:00Z", "2027-01-16T03:00:00Z", false},
		{"weekly", Settings{Schedule: ScheduleWeekly, Hour: 3}, ptr("2027-01-15T03:00:07Z"), "2027-01-16T00:00:00Z", "2027-01-22T03:00:00Z", false},
		{"weekly over a month end", Settings{Schedule: ScheduleWeekly, Hour: 3}, ptr("2027-01-28T03:00:00Z"), "2027-01-29T00:00:00Z", "2027-02-04T03:00:00Z", false},
		{"biweekly", Settings{Schedule: ScheduleBiweekly, Hour: 22}, ptr("2027-02-20T22:00:00Z"), "2027-02-21T00:00:00Z", "2027-03-06T22:00:00Z", false},
		{"monthly from the 31st", Settings{Schedule: ScheduleMonthly, Hour: 3}, ptr("2027-01-31T09:00:00Z"), "2027-02-01T00:00:00Z", "2027-02-01T03:00:00Z", false},
		{"monthly over a year end", Settings{Schedule: ScheduleMonthly, Hour: 0}, ptr("2027-12-01T00:00:00Z"), "2027-12-02T00:00:00Z", "2028-01-01T00:00:00Z", false},
		{"every 10 days", Settings{Schedule: ScheduleDays, EveryDays: 10, Hour: 5}, ptr("2027-02-25T05:01:00Z"), "2027-02-26T00:00:00Z", "2027-03-07T05:00:00Z", false},
		{"every day, run late", Settings{Schedule: ScheduleDays, EveryDays: 1, Hour: 3}, ptr("2027-01-15T15:00:00Z"), "2027-01-15T16:00:00Z", "2027-01-16T03:00:00Z", false},
		{"overdue is due", Settings{Schedule: ScheduleWeekly, Hour: 3}, ptr("2026-12-01T03:00:00Z"), "2027-01-15T12:00:00Z", "2026-12-08T03:00:00Z", false},
	}
	for _, c := range cases {
		got, ok := NextRun(c.s, c.last, at(c.since))
		if c.isOff {
			if ok {
				t.Fatalf("%s: want off, got %v", c.name, got)
			}
			continue
		}
		if !ok || !got.Equal(at(c.want)) {
			t.Fatalf("%s: got %v want %s", c.name, got, c.want)
		}
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	if s := e.r.Settings(ctx); s.Schedule != ScheduleMonthly || s.Apply != ApplyAuto || s.MaxProviders != 10 || s.StaleDays != 25 || s.Scope != ScopeWithKeys {
		t.Fatalf("defaults: %+v", s)
	}
	bad := []func(*Settings){
		func(s *Settings) { s.Schedule = "hourly" },
		func(s *Settings) { s.Schedule = ScheduleDays; s.EveryDays = 400 },
		func(s *Settings) { s.Hour = 24 },
		func(s *Settings) { s.Scope = "mine" },
		func(s *Settings) { s.Apply = "yolo" },
		func(s *Settings) { s.MaxRunUSD = 0 },
		func(s *Settings) { s.MaxMonthUSD = 5000 },
		func(s *Settings) { s.MaxProviders = 0 },
		func(s *Settings) { s.StaleDays = -1 },
		func(s *Settings) { s.Include = []string{"Not A Slug!"} },
	}
	for i, m := range bad {
		s := Defaults()
		m(&s)
		if _, _, err := e.r.SaveSettings(ctx, s, "x"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	s := Defaults()
	s.Include = []string{" Exa ", "exa", "tavily"}
	_, saved, err := e.r.SaveSettings(ctx, s, "x")
	if err != nil || !eq(saved.Include, []string{"exa", "tavily"}) {
		t.Fatalf("normalize: %v %v", saved.Include, err)
	}
	// The cache is replaced on save; a fresh refresher reads it back.
	r2 := &Refresher{Store: e.st}
	if got := r2.Settings(ctx); !eq(got.Include, []string{"exa", "tavily"}) || got.UpdatedBy != "x" {
		t.Fatalf("reload: %+v", got)
	}
}

func runAndWait(t *testing.T, e *tenv, trigger string) *store.RefreshRun {
	t.Helper()
	run, err := e.r.Start(context.Background(), trigger, "root@x.io")
	if err != nil {
		t.Fatal(err)
	}
	e.r.Wait()
	run, err = e.st.GetRefreshRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func items(t *testing.T, e *tenv, runID int64) map[string]*store.RefreshItem {
	t.Helper()
	list, err := e.st.RefreshItems(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*store.RefreshItem{}
	for _, it := range list {
		m[it.Slug] = it
	}
	return m
}

func TestNoChangeBumpsCheckedAtWithoutAVersion(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.service("serpapi", store.StatusActive)
	before, _ := e.st.GetProvider(ctx, "serpapi")
	run := runAndWait(t, e, store.RefreshByHand)
	if run.Status != store.RefreshDone || run.Checked != 1 || run.Changed != 0 || run.CostUSD < 0.0099 {
		t.Fatalf("run: %+v", run)
	}
	after, _ := e.st.GetProvider(ctx, "serpapi")
	if after.Version != before.Version {
		t.Fatalf("a no change check made v%d", after.Version)
	}
	spec, _ := providers.DecodeSpec(after.Spec)
	for _, o := range spec.Offers {
		if o.CheckedAt != "2027-01-15" {
			t.Fatalf("checked_at not bumped: %+v", o)
		}
	}
	if live := providers.Get("serpapi"); live.Offers[0].CheckedAt != "2027-01-15" {
		t.Fatal("the live spec was not reloaded")
	}
	snap, _ := e.st.ProviderVersionSnapshot(ctx, "serpapi", after.Version)
	if !strings.Contains(string(snap.Spec), "2027-01-15") {
		t.Fatal("the version snapshot keeps the old date")
	}
	states, _ := e.st.ProviderRefreshStates(ctx)
	if st := states["serpapi"]; st == nil || st.Outcome != store.ItemUnchanged || st.Trigger != store.RefreshByHand {
		t.Fatalf("state: %+v", st)
	}
}

func changedPrice(p *store.Provider, spec *providers.Spec, source string) (*agent.OffersResult, error) {
	offers := stamp(spec.Offers)
	if offers[0].Price == nil {
		offers[0].Price = &providers.Price{Unit: "request"}
	}
	pr := *offers[0].Price
	pr.Amount += 0.001
	offers[0].Price = &pr
	offers[0].Free = false
	offers[0].SourceURL = source
	return &agent.OffersResult{Verified: true, Offers: offers, Summary: "Price went up.", CostUSD: 0.02}, nil
}

func TestAutoApplyOnlyWithSources(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.service("serpapi", store.StatusActive)
	e.service("tavily", store.StatusActive)
	e.ai.answer = func(p *store.Provider, spec *providers.Spec) (*agent.OffersResult, error) {
		if p.Slug == "serpapi" {
			return changedPrice(p, spec, "https://serpapi.com/pricing")
		}
		return changedPrice(p, spec, "") // no source: must wait for review
	}
	before, _ := e.st.GetProvider(ctx, "serpapi")
	run := runAndWait(t, e, store.RefreshByHand)
	it := items(t, e, run.ID)
	if it["serpapi"].Status != store.ItemApplied || it["tavily"].Status != store.ItemReview {
		t.Fatalf("items: %+v %+v", it["serpapi"], it["tavily"])
	}
	after, _ := e.st.GetProvider(ctx, "serpapi")
	if after.Version != before.Version+1 || after.Source != before.Source || !after.Builtin {
		t.Fatalf("applied provider: v%d source %s", after.Version, after.Source)
	}
	vs, _ := e.st.ProviderVersions(ctx, "serpapi")
	if vs[0].Source != "refresh" || !strings.Contains(vs[0].Note, "offers refreshed") || !strings.Contains(vs[0].Note, "changed") {
		t.Fatalf("version: %+v", vs[0])
	}
	// Only offers changed: the rest of the spec is the same.
	var a, b map[string]json.RawMessage
	_ = json.Unmarshal(before.Spec, &a)
	_ = json.Unmarshal(after.Spec, &b)
	for k := range a {
		if k != "offers" && string(a[k]) != string(b[k]) {
			t.Fatalf("%s changed", k)
		}
	}
	rv, _ := e.st.ListRefreshReviews(ctx, store.ReviewPending, 10)
	if len(rv) != 1 || rv[0].Slug != "tavily" || !strings.Contains(rv[0].Reason, "cites no source") {
		t.Fatalf("reviews: %+v", rv)
	}
	tav, _ := e.st.GetProvider(ctx, "tavily")
	if tav.Version != 1 {
		t.Fatal("a review must not publish")
	}
	// Applying the review publishes it; a second apply is refused.
	if _, np, err := e.r.ApplyReview(ctx, rv[0].ID, "root@x.io"); err != nil || np.Version != 2 {
		t.Fatalf("apply: %v", err)
	}
	if _, _, err := e.r.ApplyReview(ctx, rv[0].ID, "root@x.io"); !errors.Is(err, ErrNotReview) {
		t.Fatalf("second apply: %v", err)
	}
	states, _ := e.st.ProviderRefreshStates(ctx)
	if st := states["tavily"]; st.ChangedVersion == nil || *st.ChangedVersion != 2 || st.PendingReview != nil {
		t.Fatalf("tavily state: %+v", st)
	}
	audits, _ := e.st.ListAudit(ctx, 20)
	found := false
	for _, a := range audits {
		if a.Action == "provider.refresh.apply" && a.Target == "serpapi" {
			found = true
		}
	}
	if !found {
		t.Fatal("the automatic apply was not audited")
	}

	// With apply set to review, even a sourced change waits.
	e.save(func(s *Settings) { s.Apply = ApplyReview; s.StaleDays = 0 })
	run = runAndWait(t, e, store.RefreshByHand)
	if it := items(t, e, run.ID); it["serpapi"].Status != store.ItemReview {
		t.Fatalf("review mode: %+v", it["serpapi"])
	}
	// A proposal made against an old version cannot be applied.
	rv, _ = e.st.ListRefreshReviews(ctx, store.ReviewPending, 10)
	p, _ := e.st.GetProvider(ctx, rv[0].Slug)
	if _, err := e.st.SaveProvider(ctx, p, "x", "edit"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.r.ApplyReview(ctx, rv[0].ID, "root@x.io"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale apply: %v", err)
	}
	if _, err := e.r.DismissReview(ctx, rv[0].ID, "root@x.io"); err != nil {
		t.Fatal(err)
	}
}

func TestRemovingEveryOfferWaitsForReview(t *testing.T) {
	d := DiffOffers([]providers.Offer{{Name: "A"}}, nil)
	if ok, why := AutoApply(d, []providers.Offer{{Name: "A"}}, nil, nil); ok || why == "" {
		t.Fatal("removing every offer must be reviewed")
	}
	d = DiffOffers([]providers.Offer{{Name: "A", Notes: "x  y", CheckedAt: "2026-01-01"}}, []providers.Offer{{Name: "a", Notes: "x y", CheckedAt: "2027-01-01"}})
	if !d.Empty() {
		t.Fatalf("checked_at, case and spacing are not changes: %+v", d)
	}
}

func TestFailureIsolation(t *testing.T) {
	e := newTEnv(t)
	e.service("serpapi", store.StatusActive)
	e.service("tavily", store.StatusActive)
	e.service("exa", store.StatusActive)
	e.ai.answer = func(p *store.Provider, spec *providers.Spec) (*agent.OffersResult, error) {
		switch p.Slug {
		case "exa":
			return &agent.OffersResult{CostUSD: 0.01}, errors.New("AI request failed: boom")
		case "serpapi":
			return &agent.OffersResult{Verified: false, Offers: spec.Offers, CostUSD: 0.01, Summary: "Pricing page was down"}, nil
		}
		return &agent.OffersResult{Verified: true, Offers: stamp(spec.Offers), CostUSD: 0.01}, nil
	}
	run := runAndWait(t, e, store.RefreshByHand)
	it := items(t, e, run.ID)
	if it["exa"].Status != store.ItemFailed || it["serpapi"].Status != store.ItemFailed || it["tavily"].Status != store.ItemUnchanged {
		t.Fatalf("items: %s %s %s", it["exa"].Status, it["serpapi"].Status, it["tavily"].Status)
	}
	if run.Status != store.RefreshDone || run.Failed != 2 || run.Checked != 1 {
		t.Fatalf("run: %+v", run)
	}
	if !strings.Contains(it["exa"].Detail, "boom") || it["exa"].CostUSD != 0.01 {
		t.Fatalf("failed item: %+v", it["exa"])
	}
	// A failure is not a check: the provider is retried next run.
	states, _ := e.st.ProviderRefreshStates(context.Background())
	if states["exa"] != nil || states["serpapi"] != nil {
		t.Fatal("failed checks must not count as checked")
	}
}

// spend writes ledger rows as if the refresh model had spent usd.
func spend(t *testing.T, st *store.Store, usd float64) {
	t.Helper()
	if err := st.RecordAICall(context.Background(), store.AICall{Feature: agent.FeatureRefresh, Model: "claude-haiku-5-5", CostUSD: usd, TS: testNow}); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetCapsStopTheRun(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.service("serpapi", store.StatusActive)
	e.service("tavily", store.StatusActive)
	e.service("exa", store.StatusActive)
	// Each check costs $0.60 against a $1 run cap: the second one reaches
	// it and the third never starts.
	e.ai.answer = func(p *store.Provider, spec *providers.Spec) (*agent.OffersResult, error) {
		spend(t, e.st, 0.6)
		return &agent.OffersResult{Verified: true, Offers: stamp(spec.Offers), CostUSD: 0.6}, nil
	}
	e.save(func(s *Settings) { s.MaxRunUSD = 1; s.MaxMonthUSD = 100 })
	run := runAndWait(t, e, store.RefreshByHand)
	if run.Status != store.RefreshStopped || !strings.Contains(run.StopReason, "budget") || len(e.ai.called()) != 2 {
		t.Fatalf("run cap: %+v calls %v", run, e.ai.called())
	}
	it := items(t, e, run.ID)
	skipped := 0
	for _, x := range it {
		if x.Status == store.ItemSkipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("one item never ran: %d", skipped)
	}

	// The month cap: $1.20 already spent this month, cap $1.20, nothing starts.
	e.save(func(s *Settings) { s.MaxRunUSD = 10; s.MaxMonthUSD = 1.2; s.StaleDays = 0 })
	if _, err := e.r.Start(ctx, store.RefreshByHand, "x"); !errors.Is(err, ErrBudget) {
		t.Fatalf("month cap: %v", err)
	}
	if _, err := e.r.CheckOne(ctx, "serpapi", "x"); !errors.Is(err, ErrBudget) {
		t.Fatalf("month cap, one provider: %v", err)
	}
	// Mid run: the month cap is reached after the first provider.
	e.save(func(s *Settings) { s.MaxRunUSD = 10; s.MaxMonthUSD = 1.5; s.StaleDays = 0 })
	e.ai.calls = nil
	run = runAndWait(t, e, store.RefreshByHand)
	if run.Status != store.RefreshStopped || !strings.Contains(run.StopReason, "monthly") || len(e.ai.called()) != 1 {
		t.Fatalf("month cap mid run: %+v calls %v", run, e.ai.called())
	}
}

func TestOneRunAtATimeAndResume(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.service("serpapi", store.StatusActive)
	e.service("tavily", store.StatusActive)
	e.ai.release = make(chan struct{})
	run, err := e.r.Start(ctx, store.RefreshByHand, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Start(ctx, store.RefreshByHand, "x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second run: %v", err)
	}
	if _, err := e.r.CheckOne(ctx, "exa", "x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("check during a run: %v", err)
	}
	close(e.ai.release)
	e.r.Wait()
	e.ai.release = nil

	// A restart: the run is still marked running with one item mid check
	// and one waiting. A fresh refresher resumes it on its first tick.
	if _, err := e.st.DB.Exec(`UPDATE refresh_runs SET status='running', finished_at=NULL WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.Exec(`UPDATE refresh_items SET status='running' WHERE run_id=? AND slug='serpapi'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.Exec(`UPDATE refresh_items SET status='pending' WHERE run_id=? AND slug='tavily'`, run.ID); err != nil {
		t.Fatal(err)
	}
	r2 := &Refresher{Store: e.st, Catalog: e.r.Catalog, AI: e.ai, Now: e.r.Now}
	if _, err := r2.Start(ctx, store.RefreshByHand, "x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("start before resume: %v", err)
	}
	e.ai.calls = nil
	r2.Tick(ctx)
	r2.Wait()
	got, _ := e.st.GetRefreshRun(ctx, run.ID)
	if got.Status != store.RefreshDone || got.Checked != 2 || len(e.ai.called()) != 2 {
		t.Fatalf("resumed: %+v calls %v", got, e.ai.called())
	}
}

func TestScheduledTick(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.service("serpapi", store.StatusActive)
	e.save(func(s *Settings) { s.Schedule = ScheduleWeekly; s.Hour = 3 })
	// Never run: due at the next 03:00, which is tomorrow from noon.
	e.r.Tick(ctx)
	e.r.Wait()
	if runs, _ := e.st.ListRefreshRuns(ctx, 5); len(runs) != 0 {
		t.Fatal("ran before it was due")
	}
	e.r.Now = func() time.Time { return testNow.Add(16 * time.Hour) }
	e.r.Tick(ctx)
	e.r.Wait()
	runs, _ := e.st.ListRefreshRuns(ctx, 5)
	if len(runs) != 1 || runs[0].Trigger != store.RefreshBySchedule || runs[0].Checked != 1 {
		t.Fatalf("scheduled: %+v", runs)
	}
	// Not due again for a week, even though the clock moved on a day.
	e.r.Now = func() time.Time { return testNow.Add(40 * time.Hour) }
	e.r.Tick(ctx)
	e.r.Wait()
	if runs, _ = e.st.ListRefreshRuns(ctx, 5); len(runs) != 1 {
		t.Fatalf("ran twice: %d", len(runs))
	}
	// Off and AI off never run.
	e.save(func(s *Settings) { s.Schedule = ScheduleOff })
	e.r.Now = func() time.Time { return testNow.AddDate(0, 1, 0) }
	e.r.Tick(ctx)
	e.ai.off = true
	e.save(func(s *Settings) {})
	e.r.Tick(ctx)
	e.r.Wait()
	if runs, _ = e.st.ListRefreshRuns(ctx, 5); len(runs) != 1 {
		t.Fatalf("ran while off: %d", len(runs))
	}
}

func TestCheckOneIgnoresScopeAndStaleness(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	// No service, no key, just checked: a check by hand still runs.
	if err := e.st.MarkProviderChecked(ctx, "linkup", store.RefreshBySchedule, store.ItemUnchanged, 0, 0); err != nil {
		t.Fatal(err)
	}
	run, err := e.r.CheckOne(ctx, "linkup", "root@x.io")
	if err != nil {
		t.Fatal(err)
	}
	e.r.Wait()
	run, _ = e.st.GetRefreshRun(ctx, run.ID)
	if run.Trigger != store.RefreshOne || run.Checked != 1 || run.StartedBy != "root@x.io" {
		t.Fatalf("run: %+v", run)
	}
	if _, err := e.r.CheckOne(ctx, "no-such", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing provider: %v", err)
	}
	if _, err := e.r.CheckOne(ctx, providers.PeerSlug, "x"); err == nil {
		t.Fatal("the peer provider has no offers to check")
	}
}

func TestClaudePricesTarget(t *testing.T) {
	e := newTEnv(t)
	ctx := context.Background()
	e.ai.prices = []agent.ModelPrice{
		{Model: "claude-haiku-5-5", Input: 0.1, Output: 0.5, CacheWrite: 0.125, CacheRead: 0.01},
		{Model: "claude-opus-5-5", Input: 4, Output: 20, CacheWrite: 5, CacheRead: 0.2},
	}
	e.save(func(s *Settings) { s.ClaudePrices = true; s.MaxProviders = 1 })
	e.service("serpapi", store.StatusActive)
	e.service("tavily", store.StatusActive)
	// The price target goes first and does not count toward the provider cap.
	slugs, _ := e.plan()
	if !eq(slugs, []string{PricesTarget, "serpapi"}) {
		t.Fatalf("plan: %v", slugs)
	}
	// Unchanged: recorded as checked, nothing applied, then stale skipped.
	e.ai.priceAnswer = &agent.PricesResult{Verified: true, Prices: e.ai.prices, CostUSD: 0.01}
	run := runAndWait(t, e, store.RefreshByHand)
	if it := items(t, e, run.ID)[PricesTarget]; it.Status != store.ItemUnchanged || len(e.ai.appliedPrice) != 0 {
		t.Fatalf("unchanged: %+v", it)
	}
	if _, why := e.plan(); !strings.Contains(why[PricesTarget], "within 25 days") {
		t.Fatalf("stale: %v", why)
	}

	// A sourced change applies on its own.
	e.ai.pricesAt = time.Time{}
	e.save(func(s *Settings) { s.ClaudePrices = true; s.StaleDays = 0 })
	up := []agent.ModelPrice{
		{Model: "claude-haiku-5-5", Input: 0.2, Output: 0.5, CacheWrite: 0.125, CacheRead: 0.01, SourceURL: agent.PricesSourceURL},
		{Model: "claude-opus-5-5", Input: 4, Output: 20, CacheWrite: 5, CacheRead: 0.2},
	}
	e.ai.priceAnswer = &agent.PricesResult{Verified: true, Prices: up, CostUSD: 0.01}
	run = runAndWait(t, e, store.RefreshByHand)
	it := items(t, e, run.ID)[PricesTarget]
	if it.Status != store.ItemApplied || len(e.ai.appliedPrice) != 1 || e.ai.appliedPrice[0].Model != "claude-haiku-5-5" {
		t.Fatalf("auto: %+v %+v", it, e.ai.appliedPrice)
	}
	var d Diff
	_ = json.Unmarshal(it.Diff, &d)
	if len(d.Changed) != 1 || d.Changed[0].Fields[0].From != "$0.1 / MTok" || d.Changed[0].Fields[0].To != "$0.2 / MTok" {
		t.Fatalf("diff: %+v", d)
	}

	// An unsourced change waits for review, then applies by hand.
	e.ai.appliedPrice = nil
	e.ai.priceAnswer = &agent.PricesResult{Verified: true, CostUSD: 0.01, Prices: []agent.ModelPrice{
		{Model: "claude-opus-5-5", Input: 3, Output: 20, CacheWrite: 5, CacheRead: 0.2}}}
	run = runAndWait(t, e, store.RefreshByHand)
	it = items(t, e, run.ID)[PricesTarget]
	if it.Status != store.ItemReview || it.ReviewID == nil || len(e.ai.appliedPrice) != 0 {
		t.Fatalf("review: %+v", it)
	}
	rv, _ := e.st.GetRefreshReview(ctx, *it.ReviewID)
	if !strings.Contains(rv.Reason, "cites no source") {
		t.Fatalf("reason: %s", rv.Reason)
	}
	if _, p, err := e.r.ApplyReview(ctx, rv.ID, "root@x.io"); err != nil || p != nil || len(e.ai.appliedPrice) != 1 || e.ai.appliedPrice[0].Input != 3 {
		t.Fatalf("apply: %v %+v", err, e.ai.appliedPrice)
	}
}
