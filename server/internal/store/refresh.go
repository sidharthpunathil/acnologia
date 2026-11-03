package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// schemaRefresh holds the provider refresh: runs that re-check providers'
// offers, one item per provider in a run, proposals waiting for review, and
// when each provider was last checked.
const schemaRefresh = `
CREATE TABLE IF NOT EXISTS refresh_runs(
  id INTEGER PRIMARY KEY,
  trigger TEXT NOT NULL,
  status TEXT NOT NULL,
  started_by TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  skipped TEXT NOT NULL DEFAULT '[]',
  stop_reason TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  finished_at INTEGER
);
CREATE TABLE IF NOT EXISTS refresh_items(
  id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL REFERENCES refresh_runs(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  status TEXT NOT NULL,
  progress TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  diff TEXT NOT NULL DEFAULT '',
  version INTEGER,
  review_id INTEGER,
  cost_usd REAL NOT NULL DEFAULT 0,
  started_at INTEGER,
  finished_at INTEGER
);
CREATE INDEX IF NOT EXISTS refresh_items_run ON refresh_items(run_id);
CREATE TABLE IF NOT EXISTS refresh_reviews(
  id INTEGER PRIMARY KEY,
  slug TEXT NOT NULL,
  run_id INTEGER NOT NULL,
  status TEXT NOT NULL,
  base_version INTEGER NOT NULL,
  offers TEXT NOT NULL,
  diff TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  decided_at INTEGER,
  decided_by TEXT NOT NULL DEFAULT '',
  version INTEGER
);
CREATE INDEX IF NOT EXISTS refresh_reviews_slug ON refresh_reviews(slug, status);
CREATE TABLE IF NOT EXISTS provider_refresh(
  slug TEXT PRIMARY KEY,
  checked_at INTEGER NOT NULL,
  trigger TEXT NOT NULL,
  outcome TEXT NOT NULL,
  run_id INTEGER,
  changed_at INTEGER,
  changed_version INTEGER
);
`

func migrateRefresh(db *sql.DB) error {
	_, err := db.Exec(schemaRefresh)
	return err
}

// Refresh run triggers.
const (
	RefreshBySchedule = "schedule" // the worker, on the configured schedule
	RefreshByHand     = "manual"   // "Run refresh now" for the whole scope
	RefreshOne        = "provider" // "Check now" on one provider
)

// Refresh run statuses.
const (
	RefreshRunning = "running"
	RefreshDone    = "done"
	// RefreshStopped means a budget cap ended the run early.
	RefreshStopped = "stopped"
	RefreshFailed  = "failed"
)

// Refresh item statuses.
const (
	ItemPending   = "pending"
	ItemRunning   = "running"
	ItemUnchanged = "unchanged"
	ItemApplied   = "applied"
	ItemReview    = "review"
	ItemFailed    = "failed"
	// ItemSkipped is an item a stopped run never reached.
	ItemSkipped = "skipped"
)

// Review statuses.
const (
	ReviewPending    = "pending"
	ReviewApplied    = "applied"
	ReviewDismissed  = "dismissed"
	ReviewSuperseded = "superseded"
)

// RefreshSkip is a provider a run left out, and why.
type RefreshSkip struct {
	Slug   string `json:"slug"`
	Reason string `json:"reason"`
}

// RefreshRun is one pass over the providers in scope.
type RefreshRun struct {
	ID         int64         `json:"id"`
	Trigger    string        `json:"trigger"`
	Status     string        `json:"status"`
	StartedBy  string        `json:"started_by"`
	Model      string        `json:"model"`
	Skipped    []RefreshSkip `json:"skipped"`
	StopReason string        `json:"stop_reason"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at"`
	// Totals over the run's items, filled by the list and get queries.
	Checked   int     `json:"checked"`
	Changed   int     `json:"changed"`
	Reviews   int     `json:"reviews"`
	Failed    int     `json:"failed"`
	Pending   int     `json:"pending"`
	Providers int     `json:"providers"`
	CostUSD   float64 `json:"cost_usd"`
}

// RefreshItem is one provider inside a run.
type RefreshItem struct {
	ID       int64  `json:"id"`
	RunID    int64  `json:"run_id"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Progress string `json:"progress"`
	// Detail is the error, or the model's summary of what it read.
	Detail     string          `json:"detail"`
	Diff       json.RawMessage `json:"diff,omitempty"`
	Version    *int64          `json:"version"`
	ReviewID   *int64          `json:"review_id"`
	CostUSD    float64         `json:"cost_usd"`
	StartedAt  *time.Time      `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
}

// RefreshReview is a proposed offers change waiting for a superadmin.
type RefreshReview struct {
	ID          int64           `json:"id"`
	Slug        string          `json:"slug"`
	RunID       int64           `json:"run_id"`
	Status      string          `json:"status"`
	BaseVersion int64           `json:"base_version"`
	Offers      json.RawMessage `json:"offers"`
	Diff        json.RawMessage `json:"diff"`
	Summary     string          `json:"summary"`
	// Reason says why the change waits for review instead of applying.
	Reason    string     `json:"reason"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at"`
	DecidedBy string     `json:"decided_by"`
	Version   *int64     `json:"version"`
}

// ProviderRefresh is when a provider's offers were last checked and changed.
type ProviderRefresh struct {
	Slug           string     `json:"-"`
	CheckedAt      time.Time  `json:"checked_at"`
	Trigger        string     `json:"trigger"`
	Outcome        string     `json:"outcome"`
	RunID          *int64     `json:"run_id"`
	ChangedAt      *time.Time `json:"changed_at"`
	ChangedVersion *int64     `json:"changed_version"`
	// PendingReview is the id of a proposal waiting for review, if any.
	PendingReview *int64 `json:"pending_review"`
}

// CreateRefreshRun records a run started at the given time (the schedule
// counts from it) with its items in order.
func (s *Store) CreateRefreshRun(ctx context.Context, trigger, by, model string, slugs []string, skipped []RefreshSkip, at time.Time) (*RefreshRun, error) {
	if skipped == nil {
		skipped = []RefreshSkip{}
	}
	sk, _ := json.Marshal(skipped)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO refresh_runs(trigger,status,started_by,model,skipped,started_at) VALUES(?,?,?,?,?,?)`,
		trigger, RefreshRunning, by, model, string(sk), at.Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	for _, slug := range slugs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO refresh_items(run_id,slug,status) VALUES(?,?,?)`, id, slug, ItemPending); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRefreshRun(ctx, id)
}

const refreshRunCols = `r.id, r.trigger, r.status, r.started_by, r.model, r.skipped, r.stop_reason, r.started_at, r.finished_at,
(SELECT COUNT(*) FROM refresh_items i WHERE i.run_id=r.id),
(SELECT COUNT(*) FROM refresh_items i WHERE i.run_id=r.id AND i.status IN ('unchanged','applied','review')),
(SELECT COUNT(*) FROM refresh_items i WHERE i.run_id=r.id AND i.status='applied'),
(SELECT COUNT(*) FROM refresh_items i WHERE i.run_id=r.id AND i.status='review'),
(SELECT COUNT(*) FROM refresh_items i WHERE i.run_id=r.id AND i.status='failed'),
(SELECT COUNT(*) FROM refresh_items i WHERE i.run_id=r.id AND i.status IN ('pending','running')),
(SELECT COALESCE(SUM(cost_usd),0) FROM refresh_items i WHERE i.run_id=r.id)`

func scanRefreshRun(row interface{ Scan(...any) error }) (*RefreshRun, error) {
	var r RefreshRun
	var sk string
	var started int64
	var fin sql.NullInt64
	err := row.Scan(&r.ID, &r.Trigger, &r.Status, &r.StartedBy, &r.Model, &sk, &r.StopReason, &started, &fin,
		&r.Providers, &r.Checked, &r.Changed, &r.Reviews, &r.Failed, &r.Pending, &r.CostUSD)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(sk), &r.Skipped)
	if r.Skipped == nil {
		r.Skipped = []RefreshSkip{}
	}
	r.StartedAt = time.Unix(started, 0).UTC()
	r.FinishedAt = toTime(fin)
	return &r, nil
}

func (s *Store) GetRefreshRun(ctx context.Context, id int64) (*RefreshRun, error) {
	return scanRefreshRun(s.DB.QueryRowContext(ctx, `SELECT `+refreshRunCols+` FROM refresh_runs r WHERE r.id=?`, id))
}

// ListRefreshRuns returns the newest runs first.
func (s *Store) ListRefreshRuns(ctx context.Context, limit int) ([]*RefreshRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+refreshRunCols+` FROM refresh_runs r ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RefreshRun{}
	for rows.Next() {
		r, err := scanRefreshRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunningRefreshRun is the run still marked running, if any. After a restart
// it is the run to resume.
func (s *Store) RunningRefreshRun(ctx context.Context) (*RefreshRun, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM refresh_runs WHERE status=? ORDER BY id LIMIT 1`, RefreshRunning).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetRefreshRun(ctx, id)
}

// LastRefreshRun is the newest run with a trigger, or ErrNotFound.
func (s *Store) LastRefreshRun(ctx context.Context, trigger string) (*RefreshRun, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM refresh_runs WHERE trigger=? ORDER BY id DESC LIMIT 1`, trigger).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetRefreshRun(ctx, id)
}

func (s *Store) FinishRefreshRun(ctx context.Context, id int64, status, stopReason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE refresh_runs SET status=?, stop_reason=?, finished_at=? WHERE id=?`, status, stopReason, now(), id)
	if err != nil {
		return err
	}
	if status == RefreshStopped || status == RefreshFailed {
		_, err = s.DB.ExecContext(ctx, `UPDATE refresh_items SET status=?, detail=?, finished_at=? WHERE run_id=? AND status IN (?,?)`,
			ItemSkipped, stopReason, now(), id, ItemPending, ItemRunning)
	}
	return err
}

// PruneRefreshRuns keeps the newest keep runs.
func (s *Store) PruneRefreshRuns(ctx context.Context, keep int) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM refresh_runs WHERE status<>? AND id NOT IN (SELECT id FROM refresh_runs ORDER BY id DESC LIMIT ?)`, RefreshRunning, keep)
	return err
}

const refreshItemCols = `id, run_id, slug, status, progress, detail, diff, version, review_id, cost_usd, started_at, finished_at`

func scanRefreshItem(row interface{ Scan(...any) error }) (*RefreshItem, error) {
	var it RefreshItem
	var diff string
	var ver, rev, st, fin sql.NullInt64
	if err := row.Scan(&it.ID, &it.RunID, &it.Slug, &it.Status, &it.Progress, &it.Detail, &diff, &ver, &rev, &it.CostUSD, &st, &fin); err != nil {
		return nil, err
	}
	if diff != "" {
		it.Diff = json.RawMessage(diff)
	}
	it.Version, it.ReviewID = toInt(ver), toInt(rev)
	it.StartedAt, it.FinishedAt = toTime(st), toTime(fin)
	return &it, nil
}

// RefreshItems lists a run's items in the order they run.
func (s *Store) RefreshItems(ctx context.Context, runID int64) ([]*RefreshItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+refreshItemCols+` FROM refresh_items WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RefreshItem{}
	for rows.Next() {
		it, err := scanRefreshItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RequeueRefreshItems puts items a restart interrupted back in the queue.
func (s *Store) RequeueRefreshItems(ctx context.Context, runID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE refresh_items SET status=?, progress='' WHERE run_id=? AND status=?`, ItemPending, runID, ItemRunning)
	return err
}

func (s *Store) StartRefreshItem(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE refresh_items SET status=?, started_at=?, progress='' WHERE id=?`, ItemRunning, now(), id)
	return err
}

func (s *Store) RefreshItemProgress(ctx context.Context, id int64, progress string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE refresh_items SET progress=? WHERE id=?`, TruncateUTF8(progress, 200), id)
}

// RefreshItemCost sets the running cost of an item.
func (s *Store) RefreshItemCost(ctx context.Context, id int64, cost float64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE refresh_items SET cost_usd=? WHERE id=?`, cost, id)
}

// ItemResult is how an item ended.
type ItemResult struct {
	Status   string
	Detail   string
	Diff     any
	Version  int64
	ReviewID int64
	CostUSD  float64
}

func (s *Store) FinishRefreshItem(ctx context.Context, id int64, r ItemResult) error {
	diff := ""
	if r.Diff != nil {
		b, _ := json.Marshal(r.Diff)
		diff = string(b)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE refresh_items SET status=?, detail=?, diff=?, version=?, review_id=?, cost_usd=?, progress='', finished_at=? WHERE id=?`,
		r.Status, s.Scrub(ctx, TruncateUTF8(r.Detail, 4000)), diff, nz(r.Version), nz(r.ReviewID), r.CostUSD, now(), id)
	return err
}

// ---- reviews ----

// CreateRefreshReview stores a proposal and supersedes any older pending
// proposal for the same provider.
func (s *Store) CreateRefreshReview(ctx context.Context, r *RefreshReview) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	t := now()
	if _, err := tx.ExecContext(ctx, `UPDATE refresh_reviews SET status=?, decided_at=?, decided_by='refresh' WHERE slug=? AND status=?`,
		ReviewSuperseded, t, r.Slug, ReviewPending); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO refresh_reviews(slug,run_id,status,base_version,offers,diff,summary,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		r.Slug, r.RunID, ReviewPending, r.BaseVersion, string(r.Offers), string(r.Diff), s.Scrub(ctx, TruncateUTF8(r.Summary, 4000)), r.Reason, t)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

const refreshReviewCols = `id, slug, run_id, status, base_version, offers, diff, summary, reason, created_at, decided_at, decided_by, version`

func scanRefreshReview(row interface{ Scan(...any) error }) (*RefreshReview, error) {
	var r RefreshReview
	var offers, diff string
	var created int64
	var dec, ver sql.NullInt64
	err := row.Scan(&r.ID, &r.Slug, &r.RunID, &r.Status, &r.BaseVersion, &offers, &diff, &r.Summary, &r.Reason, &created, &dec, &r.DecidedBy, &ver)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Offers, r.Diff = json.RawMessage(offers), json.RawMessage(diff)
	r.CreatedAt = time.Unix(created, 0).UTC()
	r.DecidedAt, r.Version = toTime(dec), toInt(ver)
	return &r, nil
}

func (s *Store) GetRefreshReview(ctx context.Context, id int64) (*RefreshReview, error) {
	return scanRefreshReview(s.DB.QueryRowContext(ctx, `SELECT `+refreshReviewCols+` FROM refresh_reviews WHERE id=?`, id))
}

// ListRefreshReviews returns reviews with a status ("" for every status),
// newest first.
func (s *Store) ListRefreshReviews(ctx context.Context, status string, limit int) ([]*RefreshReview, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + refreshReviewCols + ` FROM refresh_reviews`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RefreshReview{}
	for rows.Next() {
		r, err := scanRefreshReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DecideRefreshReview moves a pending review to applied or dismissed. It
// reports false when the review was no longer pending, so two clicks
// cannot both apply it.
func (s *Store) DecideRefreshReview(ctx context.Context, id int64, status, by string, version int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE refresh_reviews SET status=?, decided_at=?, decided_by=?, version=? WHERE id=? AND status=?`,
		status, now(), by, nz(version), id, ReviewPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- per provider state ----

// MarkProviderChecked records a check. A change also records when and which
// version it made.
func (s *Store) MarkProviderChecked(ctx context.Context, slug, trigger, outcome string, runID int64, changedVersion int64) error {
	t := now()
	if changedVersion > 0 {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO provider_refresh(slug,checked_at,trigger,outcome,run_id,changed_at,changed_version) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(slug) DO UPDATE SET checked_at=excluded.checked_at, trigger=excluded.trigger, outcome=excluded.outcome,
			run_id=excluded.run_id, changed_at=excluded.changed_at, changed_version=excluded.changed_version`,
			slug, t, trigger, outcome, nz(runID), t, changedVersion)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO provider_refresh(slug,checked_at,trigger,outcome,run_id) VALUES(?,?,?,?,?)
		ON CONFLICT(slug) DO UPDATE SET checked_at=excluded.checked_at, trigger=excluded.trigger, outcome=excluded.outcome, run_id=excluded.run_id`,
		slug, t, trigger, outcome, nz(runID))
	return err
}

// MarkProviderChanged records a version made from a review applied by hand,
// without touching when it was checked.
func (s *Store) MarkProviderChanged(ctx context.Context, slug string, version int64) error {
	t := now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO provider_refresh(slug,checked_at,trigger,outcome,changed_at,changed_version) VALUES(?,?,?,?,?,?)
		ON CONFLICT(slug) DO UPDATE SET outcome=excluded.outcome, changed_at=excluded.changed_at, changed_version=excluded.changed_version`,
		slug, t, RefreshByHand, ItemApplied, t, version)
	return err
}

// ProviderRefreshStates maps provider slug to its refresh state, with the
// pending review if one waits.
func (s *Store) ProviderRefreshStates(ctx context.Context) (map[string]*ProviderRefresh, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT slug, checked_at, trigger, outcome, run_id, changed_at, changed_version FROM provider_refresh`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ProviderRefresh{}
	for rows.Next() {
		var p ProviderRefresh
		var checked int64
		var run, chAt, chV sql.NullInt64
		if err := rows.Scan(&p.Slug, &checked, &p.Trigger, &p.Outcome, &run, &chAt, &chV); err != nil {
			return nil, err
		}
		p.CheckedAt = time.Unix(checked, 0).UTC()
		p.RunID, p.ChangedAt, p.ChangedVersion = toInt(run), toTime(chAt), toInt(chV)
		out[p.Slug] = &p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	prow, err := s.DB.QueryContext(ctx, `SELECT slug, MAX(id) FROM refresh_reviews WHERE status=? GROUP BY slug`, ReviewPending)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var slug string
		var id int64
		if err := prow.Scan(&slug, &id); err != nil {
			return nil, err
		}
		p := out[slug]
		if p == nil {
			p = &ProviderRefresh{Slug: slug}
			out[slug] = p
		}
		p.PendingReview = &id
	}
	return out, prow.Err()
}

// TouchProviderSpec rewrites the spec of a provider's current version in
// place, in the provider row and in that version's snapshot, without making
// a new version. The refresh uses it to bump checked_at when nothing
// changed. It reports false when the version moved on meanwhile.
func (s *Store) TouchProviderSpec(ctx context.Context, slug string, version int, spec json.RawMessage) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE providers SET spec=? WHERE slug=? AND version=?`, string(spec), slug, version)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	var snap string
	if err := tx.QueryRowContext(ctx, `SELECT snapshot FROM provider_versions WHERE slug=? AND version=?`, slug, version).Scan(&snap); err == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(snap), &m) == nil {
			m["spec"] = spec
			b, _ := json.Marshal(m)
			if _, err := tx.ExecContext(ctx, `UPDATE provider_versions SET snapshot=? WHERE slug=? AND version=?`, string(b), slug, version); err != nil {
				return false, err
			}
		}
	}
	return true, tx.Commit()
}

// ---- scope helpers ----

// ProvidersWithUsableKeys lists providers behind at least one service with a
// key that can serve: active, cooling or out of quota until it resets.
func (s *Store) ProvidersWithUsableKeys(ctx context.Context) (map[string]bool, error) {
	return s.slugSet(ctx, `SELECT DISTINCT s.provider FROM services s JOIN keys k ON k.service_id=s.id WHERE k.status IN (?,?,?)`,
		StatusActive, StatusCooling, StatusExhausted)
}

// ProvidersWithServices lists providers at least one service is built on.
func (s *Store) ProvidersWithServices(ctx context.Context) (map[string]bool, error) {
	return s.slugSet(ctx, `SELECT DISTINCT provider FROM services`)
}

func (s *Store) slugSet(ctx context.Context, q string, args ...any) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out[slug] = true
	}
	return out, rows.Err()
}

// AISpendSince sums the ledger's cost for one feature since a time.
func (s *Store) AISpendSince(ctx context.Context, feature string, since time.Time) (float64, error) {
	var v float64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM ai_usage WHERE feature=? AND ts>=?`, feature, since.Unix()).Scan(&v)
	return v, err
}
