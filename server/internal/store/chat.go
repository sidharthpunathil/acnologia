package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ChatThread is one assistant conversation. Data is an encrypted blob the
// api package owns (messages, transcript, pending confirmations).
type ChatThread struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"-"`
	Title     string    `json:"title"`
	Data      []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func scanThread(row interface{ Scan(...any) error }, withData bool) (*ChatThread, error) {
	var t ChatThread
	var c, u int64
	dest := []any{&t.ID, &t.UserID, &t.Title, &c, &u}
	if withData {
		dest = append(dest, &t.Data)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt, t.UpdatedAt = time.Unix(c, 0).UTC(), time.Unix(u, 0).UTC()
	return &t, nil
}

func (s *Store) CreateThread(ctx context.Context, userID int64, title string, data []byte) (*ChatThread, error) {
	n := now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO chat_threads(user_id,title,data,created_at,updated_at) VALUES(?,?,?,?,?)`, userID, title, data, n, n)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetThread(ctx, id, userID)
}

// GetThread returns a thread only to the user who owns it.
func (s *Store) GetThread(ctx context.Context, id, userID int64) (*ChatThread, error) {
	return scanThread(s.DB.QueryRowContext(ctx,
		`SELECT id,user_id,title,created_at,updated_at,data FROM chat_threads WHERE id=? AND user_id=?`, id, userID), true)
}

func (s *Store) ListThreads(ctx context.Context, userID int64) ([]*ChatThread, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id,user_id,title,created_at,updated_at FROM chat_threads WHERE user_id=? ORDER BY updated_at DESC, id DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ChatThread{}
	for rows.Next() {
		t, err := scanThread(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SaveThread(ctx context.Context, id, userID int64, title string, data []byte) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE chat_threads SET title=?, data=?, updated_at=? WHERE id=? AND user_id=?`, title, data, now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteThread(ctx context.Context, id, userID int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM chat_threads WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
