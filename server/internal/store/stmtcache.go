package store

import (
	"context"
	"database/sql"
	"sync"
)

// maxCachedStmts caps a stmtCache. The store only sends it constant SQL, a
// few dozen statements in all; the cap keeps a mistake (SQL built from
// input) from growing it without bound.
const maxCachedStmts = 256

// stmtCache keeps prepared statements by SQL text for one connection pool,
// so a hot query is parsed and planned once per connection instead of on
// every call. Only constant SQL goes through it: queries built from filters
// or with a variable number of placeholders use the pool directly.
type stmtCache struct {
	db *sql.DB
	mu sync.RWMutex
	m  map[string]*sql.Stmt
}

func newStmtCache(db *sql.DB) *stmtCache {
	return &stmtCache{db: db, m: map[string]*sql.Stmt{}}
}

// stmt returns the prepared statement for q, preparing it on first use.
// It returns nil when q cannot be prepared or the cache is full; callers
// then run q on the pool, which reports any error in q itself. No lock is
// held while preparing: preparing waits for a connection, and on the writer
// that connection may be held by a transaction that is itself looking up a
// statement here.
func (c *stmtCache) stmt(ctx context.Context, q string) *sql.Stmt {
	if st := c.peek(q); st != nil {
		return st
	}
	c.mu.RLock()
	full := c.m == nil || len(c.m) >= maxCachedStmts
	c.mu.RUnlock()
	if full {
		return nil
	}
	st, err := c.db.PrepareContext(context.WithoutCancel(ctx), q)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if have := c.m[q]; have != nil {
		_ = st.Close()
		return have
	}
	if c.m == nil || len(c.m) >= maxCachedStmts {
		_ = st.Close()
		return nil
	}
	c.m[q] = st
	return st
}

// peek returns the statement for q if it is prepared already.
func (c *stmtCache) peek(q string) *sql.Stmt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[q]
}

func (c *stmtCache) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if st := c.stmt(ctx, q); st != nil {
		return st.QueryRowContext(ctx, args...)
	}
	return c.db.QueryRowContext(ctx, q, args...)
}

func (c *stmtCache) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if st := c.stmt(ctx, q); st != nil {
		return st.QueryContext(ctx, args...)
	}
	return c.db.QueryContext(ctx, q, args...)
}

func (c *stmtCache) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if st := c.stmt(ctx, q); st != nil {
		return st.ExecContext(ctx, args...)
	}
	return c.db.ExecContext(ctx, q, args...)
}

// close closes every cached statement; later calls run unprepared.
func (c *stmtCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, st := range c.m {
		_ = st.Close()
	}
	c.m = nil
}
