package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// Group commit for the small writes every request makes (its event, usage
// counters, the key's OK mark, the token's last use). With one writer
// connection, each of those used to be its own transaction and its own WAL
// sync. When requests arrive together, the writes queued while one commit
// runs go into the next transaction as a batch, each under its own
// SAVEPOINT, so one failing write rolls back only itself.
//
// Every caller waits until the transaction holding its write has committed,
// so a write is as durable when the call returns as a direct one, and limits
// and counters read afterwards include it. When nothing else is queued the
// write runs directly, with no transaction around it.
//
// There is no background goroutine: the first caller to find the writer
// free runs one batch (its own write first), then hands the next batch to
// the oldest waiting caller. Writes always run without the caller's
// cancellation, since a client hanging up must not lose usage already
// served.

// maxBatch caps the writes in one group transaction.
const maxBatch = 128

// errStoreClosed is returned for a write queued after Close.
var errStoreClosed = errors.New("store closed")

type wstmt struct {
	q    string // constant SQL
	args []any
}

type wjob struct {
	stmts []wstmt
	err   error
	// done is closed when the job has committed (or failed), or when the job
	// is handed the lead to run the next batch, which lead then says.
	done chan struct{}
	lead bool
}

type groupWriter struct {
	db *sql.DB
	c  *stmtCache

	mu      sync.Mutex
	queue   []*wjob
	leading bool
	closed  bool
	pending int        // jobs written and not finished yet, queued or running
	idle    *sync.Cond // signalled when leading turns false
}

func newGroupWriter(db *sql.DB, c *stmtCache) *groupWriter {
	g := &groupWriter{db: db, c: c}
	g.idle = sync.NewCond(&g.mu)
	return g
}

// write runs stmts as one job and returns once it has committed.
func (g *groupWriter) write(stmts ...wstmt) error {
	j := &wjob{stmts: stmts, done: make(chan struct{})}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return errStoreClosed
	}
	g.queue = append(g.queue, j)
	g.pending++
	if g.leading {
		g.mu.Unlock()
		<-j.done
		if !j.lead {
			return j.err
		}
	} else {
		g.leading = true
		g.mu.Unlock()
	}
	g.runNext()
	return j.err
}

// runNext runs one batch from the head of the queue, then hands the lead to
// the oldest job still waiting, or gives it up.
func (g *groupWriter) runNext() {
	g.mu.Lock()
	n := min(len(g.queue), maxBatch)
	batch := make([]*wjob, n)
	copy(batch, g.queue)
	g.queue = g.queue[n:]
	g.mu.Unlock()

	g.run(batch)

	g.mu.Lock()
	g.pending -= len(batch)
	var next *wjob
	if len(g.queue) > 0 {
		next = g.queue[0]
		next.lead = true
	} else {
		g.leading = false
		g.queue = nil
		g.idle.Broadcast()
	}
	g.mu.Unlock()
	// A job handed the lead had its done closed already for the handoff.
	for _, j := range batch {
		if !j.lead {
			close(j.done)
		}
	}
	if next != nil {
		close(next.done)
	}
}

// run executes a batch and sets each job's err. It does not close done.
func (g *groupWriter) run(batch []*wjob) {
	ctx := context.Background()
	if len(batch) == 1 {
		batch[0].err = g.direct(ctx, batch[0])
		return
	}
	// Prepare first: once the transaction holds the one writer connection,
	// preparing on the pool would wait for it forever.
	for _, q := range []string{`SAVEPOINT acn_job`, `ROLLBACK TO acn_job`, `RELEASE acn_job`} {
		g.c.stmt(ctx, q)
	}
	for _, j := range batch {
		for _, st := range j.stmts {
			g.c.stmt(ctx, st.q)
		}
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		for _, j := range batch {
			j.err = g.direct(ctx, j)
		}
		return
	}
	stmts := map[string]*sql.Stmt{}
	exec := func(q string, args ...any) error {
		st, ok := stmts[q]
		if !ok {
			if p := g.c.peek(q); p != nil {
				st = tx.StmtContext(ctx, p)
			}
			stmts[q] = st
		}
		var err error
		if st != nil {
			_, err = st.ExecContext(ctx, args...)
		} else {
			_, err = tx.ExecContext(ctx, q, args...)
		}
		return err
	}
	for i, j := range batch {
		if err := exec(`SAVEPOINT acn_job`); err != nil {
			j.err = err
			continue
		}
		for _, st := range j.stmts {
			if j.err = exec(st.q, st.args...); j.err != nil {
				break
			}
		}
		if j.err != nil {
			if err := exec(`ROLLBACK TO acn_job`); err != nil {
				// Some errors (a full disk, an I/O error) make SQLite roll
				// back the whole transaction, so there is no savepoint left.
				// The jobs before this one were rolled back with it; the rest
				// must not run on a connection that is out of the
				// transaction, where a SAVEPOINT would start one and its
				// RELEASE commit it while the job was told it failed.
				g.abandon(ctx, tx, batch[:i], batch[i+1:], j.err)
				return
			}
		}
		if err := exec(`RELEASE acn_job`); err != nil && j.err == nil {
			j.err = err
		}
	}
	if err := tx.Commit(); err != nil {
		for _, j := range batch {
			if j.err == nil {
				j.err = err
			}
		}
	}
}

// abandon handles a batch whose transaction SQLite rolled back: the jobs
// already run fail with the cause, and the jobs not run yet run on their own.
func (g *groupWriter) abandon(ctx context.Context, tx *sql.Tx, ran, rest []*wjob, cause error) {
	// Rollback ends the database/sql transaction and frees the one writer
	// connection; SQLite has already rolled back, so its error is expected.
	_ = tx.Rollback()
	for _, j := range ran {
		if j.err == nil {
			j.err = fmt.Errorf("batch rolled back: %w", cause)
		}
	}
	for _, j := range rest {
		j.err = g.direct(ctx, j)
	}
}

// direct runs one job's statements outside any group transaction.
func (g *groupWriter) direct(ctx context.Context, j *wjob) error {
	for _, st := range j.stmts {
		if _, err := g.c.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return nil
}

// close refuses new writes and waits until every queued one has committed.
func (g *groupWriter) close() {
	g.mu.Lock()
	g.closed = true
	for g.leading || len(g.queue) > 0 {
		g.idle.Wait()
	}
	g.mu.Unlock()
}
