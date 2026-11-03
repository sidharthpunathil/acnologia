package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

type AgentInput struct {
	Context       string   `json:"context"`
	URLs          []string `json:"urls"`
	GitHub        string   `json:"github"`
	AutoPublish   bool     `json:"auto_publish"`
	CreateService bool     `json:"create_service"`
	HasTestKey    bool     `json:"has_test_key"`
	// TestKeyCount is how many test keys came with the run (test_key plus
	// test_keys). The secrets themselves are never in the input.
	TestKeyCount int `json:"test_key_count,omitempty"`
}

type AgentUsage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	WebSearches  int64   `json:"web_searches"`
	Turns        int     `json:"turns"`
	CostUSD      float64 `json:"cost_usd"`
}

type AgentRun struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	ProviderSlug string          `json:"provider_slug"`
	Input        AgentInput      `json:"input"`
	Draft        json.RawMessage `json:"draft"`
	Report       json.RawMessage `json:"report"`
	Live         json.RawMessage `json:"live"`
	Summary      string          `json:"summary"`
	Error        string          `json:"error"`
	ServiceID    *int64          `json:"service_id"`
	Usage        AgentUsage      `json:"usage"`
	// Model is the Claude model the run works with, fixed when it is queued.
	// Empty for runs from before it was recorded.
	Model      string     `json:"model"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`

	CreatedByID int64  `json:"-"`
	TestKeyEnc  []byte `json:"-"`
	// TestKeysEnc is every test key, an encrypted JSON array of strings. It is
	// added to the service a publish creates and wiped when the run ends.
	TestKeysEnc []byte `json:"-"`
}

// migrateRuns adds the run columns introduced after agent_runs shipped.
func migrateRuns(db *sql.DB) error {
	for _, c := range []struct{ col, def string }{
		{"description", "TEXT NOT NULL DEFAULT ''"},
		{"test_keys_enc", "BLOB"},
		{"model", "TEXT NOT NULL DEFAULT ''"},
	} {
		if _, err := addColumn(db, "agent_runs", c.col, c.def); err != nil {
			return err
		}
	}
	return nil
}

type AgentStep struct {
	Seq    int       `json:"seq"`
	TS     time.Time `json:"ts"`
	Type   string    `json:"type"`
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
}

const runCols = `id, kind, status, title, provider_slug, input, test_key_enc, draft, report, live, summary, error, service_id, usage,
COALESCE(created_by,0), created_by_email, created_at, finished_at, description, test_keys_enc, model`

func nullJSON(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(s)
}

func scanRun(row interface{ Scan(...any) error }) (*AgentRun, error) {
	var r AgentRun
	var input, draft, report, live, usage string
	var svc, fin sql.NullInt64
	var c int64
	err := row.Scan(&r.ID, &r.Kind, &r.Status, &r.Title, &r.ProviderSlug, &input, &r.TestKeyEnc, &draft, &report, &live,
		&r.Summary, &r.Error, &svc, &usage, &r.CreatedByID, &r.CreatedBy, &c, &fin, &r.Description, &r.TestKeysEnc, &r.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(input), &r.Input)
	if r.Input.URLs == nil {
		r.Input.URLs = []string{}
	}
	_ = json.Unmarshal([]byte(usage), &r.Usage)
	r.Draft, r.Report, r.Live = nullJSON(draft), nullJSON(report), nullJSON(live)
	r.ServiceID = toInt(svc)
	r.CreatedAt = time.Unix(c, 0).UTC()
	r.FinishedAt = toTime(fin)
	return &r, nil
}

func (s *Store) CreateRun(ctx context.Context, r *AgentRun) (*AgentRun, error) {
	in, _ := json.Marshal(r.Input)
	in = s.Redactor().JSON(ctx, in)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO agent_runs(kind,status,title,description,provider_slug,input,test_key_enc,test_keys_enc,created_by,created_by_email,created_at,model)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, r.Kind, r.Status, s.Scrub(ctx, r.Title), s.Scrub(ctx, r.Description), r.ProviderSlug, string(in), r.TestKeyEnc, r.TestKeysEnc,
		r.CreatedByID, r.CreatedBy, now(), r.Model)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetRun(ctx, id)
}

func (s *Store) GetRun(ctx context.Context, id int64) (*AgentRun, error) {
	return scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM agent_runs WHERE id=?`, id))
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]*AgentRun, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+runCols+` FROM agent_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AgentRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunPatch updates the fields that are set.
type RunPatch struct {
	Status, Title, Description, ProviderSlug, Summary, Error *string
	Draft, Report, Live                                      any
	ServiceID                                                *int64
	Usage                                                    *AgentUsage
	Finished                                                 bool
	ClearTestKey                                             bool
	// ClearTestKeys wipes the full list of test keys (test_keys_enc).
	ClearTestKeys bool
}

func (s *Store) UpdateRun(ctx context.Context, id int64, p RunPatch) error {
	sets, args := []string{}, []any{}
	add := func(q string, v any) { sets = append(sets, q); args = append(args, v) }
	if p.Status != nil {
		add("status=?", *p.Status)
	}
	if p.Title != nil {
		add("title=?", s.Scrub(ctx, *p.Title))
	}
	if p.Description != nil {
		add("description=?", s.Scrub(ctx, *p.Description))
	}
	if p.ProviderSlug != nil {
		add("provider_slug=?", *p.ProviderSlug)
	}
	if p.Summary != nil {
		add("summary=?", s.Scrub(ctx, *p.Summary))
	}
	if p.Error != nil {
		add("error=?", s.Scrub(ctx, *p.Error))
	}
	for col, v := range map[string]any{"draft": p.Draft, "report": p.Report, "live": p.Live} {
		if v != nil {
			b, _ := json.Marshal(v)
			add(col+"=?", string(s.Redactor().JSON(ctx, b)))
		}
	}
	if p.ServiceID != nil {
		add("service_id=?", *p.ServiceID)
	}
	if p.Usage != nil {
		b, _ := json.Marshal(p.Usage)
		add("usage=?", string(b))
	}
	if p.Finished {
		add("finished_at=?", now())
	}
	if p.ClearTestKey {
		sets = append(sets, "test_key_enc=NULL")
	}
	if p.ClearTestKeys {
		sets = append(sets, "test_keys_enc=NULL")
	}
	if len(sets) == 0 {
		return nil
	}
	q := "UPDATE agent_runs SET "
	for i, st := range sets {
		if i > 0 {
			q += ", "
		}
		q += st
	}
	_, err := s.DB.ExecContext(ctx, q+" WHERE id=?", append(args, id)...)
	return err
}

func (s *Store) AddStep(ctx context.Context, runID int64, typ, title, detail string) {
	title, detail = s.Scrub(ctx, title), s.Scrub(ctx, detail)
	if len(detail) > 8000 {
		detail = TruncateUTF8(detail, 8000) + "\n[truncated]"
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO agent_steps(run_id,seq,ts,type,title,detail)
		VALUES(?, COALESCE((SELECT MAX(seq) FROM agent_steps WHERE run_id=?),0)+1, ?, ?, ?, ?)`, runID, runID, now(), typ, title, detail)
}

// TruncateUTF8 cuts s to at most n bytes without splitting a UTF-8 character.
func TruncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ClaimRun moves a run from one status to another only if it is still in the
// expected status, so two concurrent callers cannot both act on it.
func (s *Store) ClaimRun(ctx context.Context, id int64, from, to string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE agent_runs SET status=? WHERE id=? AND status=?`, to, id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) ListSteps(ctx context.Context, runID int64, after int) ([]AgentStep, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT seq, ts, type, title, detail FROM agent_steps WHERE run_id=? AND seq>? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentStep{}
	for rows.Next() {
		var st AgentStep
		var ts int64
		if err := rows.Scan(&st.Seq, &ts, &st.Type, &st.Title, &st.Detail); err != nil {
			return nil, err
		}
		st.TS = time.Unix(ts, 0).UTC()
		out = append(out, st)
	}
	return out, rows.Err()
}

// FailStaleRuns marks runs that were in progress when the server stopped.
func (s *Store) FailStaleRuns(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE agent_runs SET status='failed', error='server restarted during the run', finished_at=?, test_key_enc=NULL, test_keys_enc=NULL
		WHERE status IN ('queued','running','testing')`, now())
	return err
}

// RenameRun sets a run's title and description, only while its title is
// still ifTitle, so a late AI naming call never overwrites the name taken
// from an accepted draft. It reports whether the run changed.
func (s *Store) RenameRun(ctx context.Context, id int64, title, description, ifTitle string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE agent_runs SET title=?, description=? WHERE id=? AND title=?`,
		s.Scrub(ctx, title), s.Scrub(ctx, description), id, ifTitle)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// busyRunStatuses are the statuses a run can be deleted out from under.
const busyRunStatuses = `('queued','running','testing','publishing')`

// ErrRunBusy means the run is still working and cannot be deleted yet.
var ErrRunBusy = errors.New("run is still in progress; cancel it first")

// DeleteRun removes one finished run and its steps. Spend stays in ai_usage.
func (s *Store) DeleteRun(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM agent_runs WHERE id=? AND status NOT IN `+busyRunStatuses, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, err := s.GetRun(ctx, id); err != nil {
		return err
	}
	return ErrRunBusy
}

// ClearRuns removes every run that has finished for good: failed, cancelled
// and published. Runs waiting for review keep their draft. It returns how
// many runs it removed.
func (s *Store) ClearRuns(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM agent_runs WHERE status IN ('failed','cancelled','published')`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
