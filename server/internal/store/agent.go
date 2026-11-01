package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type AgentInput struct {
	Context       string   `json:"context"`
	URLs          []string `json:"urls"`
	GitHub        string   `json:"github"`
	AutoPublish   bool     `json:"auto_publish"`
	CreateService bool     `json:"create_service"`
	HasTestKey    bool     `json:"has_test_key"`
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
	ProviderSlug string          `json:"provider_slug"`
	Input        AgentInput      `json:"input"`
	Draft        json.RawMessage `json:"draft"`
	Report       json.RawMessage `json:"report"`
	Live         json.RawMessage `json:"live"`
	Summary      string          `json:"summary"`
	Error        string          `json:"error"`
	ServiceID    *int64          `json:"service_id"`
	Usage        AgentUsage      `json:"usage"`
	CreatedBy    string          `json:"created_by"`
	CreatedAt    time.Time       `json:"created_at"`
	FinishedAt   *time.Time      `json:"finished_at"`

	CreatedByID int64  `json:"-"`
	TestKeyEnc  []byte `json:"-"`
}

type AgentStep struct {
	Seq    int       `json:"seq"`
	TS     time.Time `json:"ts"`
	Type   string    `json:"type"`
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
}

const runCols = `id, kind, status, title, provider_slug, input, test_key_enc, draft, report, live, summary, error, service_id, usage,
COALESCE(created_by,0), created_by_email, created_at, finished_at`

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
		&r.Summary, &r.Error, &svc, &usage, &r.CreatedByID, &r.CreatedBy, &c, &fin)
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
	res, err := s.DB.ExecContext(ctx, `INSERT INTO agent_runs(kind,status,title,provider_slug,input,test_key_enc,created_by,created_by_email,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, r.Kind, r.Status, r.Title, r.ProviderSlug, string(in), r.TestKeyEnc, r.CreatedByID, r.CreatedBy, now())
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
	Status, Title, ProviderSlug, Summary, Error *string
	Draft, Report, Live                         any
	ServiceID                                   *int64
	Usage                                       *AgentUsage
	Finished                                    bool
	ClearTestKey                                bool
}

func (s *Store) UpdateRun(ctx context.Context, id int64, p RunPatch) error {
	sets, args := []string{}, []any{}
	add := func(q string, v any) { sets = append(sets, q); args = append(args, v) }
	if p.Status != nil {
		add("status=?", *p.Status)
	}
	if p.Title != nil {
		add("title=?", *p.Title)
	}
	if p.ProviderSlug != nil {
		add("provider_slug=?", *p.ProviderSlug)
	}
	if p.Summary != nil {
		add("summary=?", *p.Summary)
	}
	if p.Error != nil {
		add("error=?", *p.Error)
	}
	for col, v := range map[string]any{"draft": p.Draft, "report": p.Report, "live": p.Live} {
		if v != nil {
			b, _ := json.Marshal(v)
			add(col+"=?", string(b))
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
	if len(detail) > 8000 {
		detail = detail[:8000] + "\n[truncated]"
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO agent_steps(run_id,seq,ts,type,title,detail)
		VALUES(?, COALESCE((SELECT MAX(seq) FROM agent_steps WHERE run_id=?),0)+1, ?, ?, ?, ?)`, runID, runID, now(), typ, title, detail)
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
	_, err := s.DB.ExecContext(ctx, `UPDATE agent_runs SET status='failed', error='server restarted during the run', finished_at=?, test_key_enc=NULL
		WHERE status IN ('queued','running','testing')`, now())
	return err
}
