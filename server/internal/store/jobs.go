package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Job states.
const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// Job is a slow admin operation (check every key, sync a peer) run in the
// background so the caller can poll it instead of holding a request open.
type Job struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Status string `json:"status"`
	// Done and Total are the progress in the job's own unit (keys,
	// services); Total is 0 until the job knows it.
	Done           int             `json:"done"`
	Total          int             `json:"total"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error"`
	CreatedBy      *int64          `json:"created_by"`
	CreatedByEmail string          `json:"created_by_email"`
	Via            string          `json:"via"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

// Finished reports whether the job is done or failed.
func (j *Job) Finished() bool { return j.Status == JobDone || j.Status == JobFailed }

const schemaJobs = `
CREATE TABLE IF NOT EXISTS jobs(
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  done INTEGER NOT NULL DEFAULT 0,
  total INTEGER NOT NULL DEFAULT 0,
  result TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_by INTEGER,
  created_by_email TEXT NOT NULL DEFAULT '',
  via TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  started_at INTEGER,
  finished_at INTEGER
);
CREATE INDEX IF NOT EXISTS jobs_created ON jobs(created_at);
`

// migrateJobs creates the jobs table. Jobs run in the server process, so a
// job still queued or running when the server starts was cut off by the
// last stop: it is marked failed.
func migrateJobs(db *sql.DB) error {
	if _, err := db.Exec(schemaJobs); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE jobs SET status=?, error='the server restarted before the job finished', finished_at=?
		WHERE status IN (?,?)`, JobFailed, now(), JobQueued, JobRunning)
	return err
}

const jobCols = `id, kind, target, status, done, total, result, error, created_by, created_by_email, via, created_at, started_at, finished_at`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var result string
	var by, started, finished sql.NullInt64
	var created int64
	if err := row.Scan(&j.ID, &j.Kind, &j.Target, &j.Status, &j.Done, &j.Total, &result, &j.Error, &by, &j.CreatedByEmail,
		&j.Via, &created, &started, &finished); err != nil {
		return nil, notFound(err)
	}
	if result != "" && json.Valid([]byte(result)) {
		j.Result = json.RawMessage(result)
	}
	j.CreatedBy = toInt(by)
	j.CreatedAt = time.Unix(created, 0).UTC()
	j.StartedAt, j.FinishedAt = toTime(started), toTime(finished)
	return &j, nil
}

// CreateJob adds a queued job. createdBy 0 means the system.
func (s *Store) CreateJob(ctx context.Context, kind, target string, createdBy int64, email, via string) (*Job, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO jobs(kind,target,status,created_by,created_by_email,via,created_at) VALUES(?,?,?,?,?,?,?)`,
		kind, target, JobQueued, nz(createdBy), email, via, now())
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// ActiveJob returns the queued or running job of a kind and target, if any,
// so a second request for the same work joins the first.
func (s *Store) ActiveJob(ctx context.Context, kind, target string) (*Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE kind=? AND target=? AND status IN (?,?) ORDER BY id DESC LIMIT 1`,
		kind, target, JobQueued, JobRunning))
}

func (s *Store) GetJob(ctx context.Context, id int64) (*Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id=?`, id))
}

// StartJob marks a job running.
func (s *Store) StartJob(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status=?, started_at=? WHERE id=?`, JobRunning, now(), id)
	return err
}

// JobProgress records how far a job got.
func (s *Store) JobProgress(ctx context.Context, id int64, done, total int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET done=?, total=? WHERE id=?`, done, total, id)
	return err
}

// FinishJob marks a job done with its result, or failed with errMsg.
func (s *Store) FinishJob(ctx context.Context, id int64, result []byte, errMsg string) (*Job, error) {
	st := JobDone
	if errMsg != "" {
		st = JobFailed
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status=?, result=?, error=?, finished_at=? WHERE id=?`,
		st, s.Scrub(ctx, string(result)), s.Scrub(ctx, errMsg), now(), id); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	// CreatedBy limits the list to one person's jobs; 0 lists everyone's.
	CreatedBy int64
	Kind      string
	Status    string
	Limit     int
}

// ListJobs lists jobs newest first, up to 200, without their results.
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]*Job, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q := `SELECT id, kind, target, status, done, total, '', error, created_by, created_by_email, via, created_at, started_at, finished_at FROM jobs WHERE 1=1`
	args := []any{}
	if f.CreatedBy != 0 {
		q += ` AND created_by=?`
		args = append(args, f.CreatedBy)
	}
	if f.Kind != "" {
		q += ` AND kind=?`
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.rd.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// PruneJobs deletes finished jobs older than d.
func (s *Store) PruneJobs(ctx context.Context, d time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE created_at<? AND status IN (?,?)`, time.Now().Add(-d).Unix(), JobDone, JobFailed)
	return err
}
