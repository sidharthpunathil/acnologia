package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Webhook delivers pool events (a key went bad, the pool ran dry, a provider
// is down) to Slack, Discord, Telegram or any URL. The URL is encrypted at
// rest because Slack, Discord and Telegram URLs carry their own secret.
type Webhook struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	URLHint    string     `json:"url_hint"`
	Format     string     `json:"format"` // json | slack | discord | telegram
	Events     []string   `json:"events"` // empty means every event
	Active     bool       `json:"active"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSentAt *time.Time `json:"last_sent_at"`
	LastStatus int        `json:"last_status"`
	LastError  string     `json:"last_error"`

	URLEnc    []byte `json:"-"`
	SecretEnc []byte `json:"-"`
}

// Wants reports whether the hook subscribes to an event type.
func (w *Webhook) Wants(event string) bool {
	if len(w.Events) == 0 {
		return true
	}
	for _, e := range w.Events {
		if e == event {
			return true
		}
	}
	return false
}

const webhookCols = `id, name, url_enc, url_hint, format, events, secret_enc, active, created_at, last_sent_at, last_status, last_error`

func scanWebhook(row interface{ Scan(...any) error }) (*Webhook, error) {
	var w Webhook
	var events string
	var active int
	var created int64
	var last sql.NullInt64
	if err := row.Scan(&w.ID, &w.Name, &w.URLEnc, &w.URLHint, &w.Format, &events, &w.SecretEnc, &active, &created, &last, &w.LastStatus, &w.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	w.Events = []string{}
	_ = json.Unmarshal([]byte(events), &w.Events)
	w.Active = active == 1
	w.CreatedAt = time.Unix(created, 0).UTC()
	w.LastSentAt = toTime(last)
	return &w, nil
}

func (s *Store) ListWebhooks(ctx context.Context) ([]*Webhook, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+webhookCols+` FROM webhooks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) GetWebhook(ctx context.Context, id int64) (*Webhook, error) {
	return scanWebhook(s.DB.QueryRowContext(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id=?`, id))
}

func (s *Store) CreateWebhook(ctx context.Context, w *Webhook) (*Webhook, error) {
	defer s.keysChanged()
	events, _ := json.Marshal(nonNil(w.Events))
	res, err := s.DB.ExecContext(ctx, `INSERT INTO webhooks(name,url_enc,url_hint,format,events,secret_enc,active,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		w.Name, w.URLEnc, w.URLHint, w.Format, string(events), w.SecretEnc, b2i(w.Active), now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetWebhook(ctx, id)
}

// UpdateWebhook saves name, format, events and active. A non-nil URLEnc also
// replaces the URL.
func (s *Store) UpdateWebhook(ctx context.Context, w *Webhook) error {
	defer s.keysChanged()
	events, _ := json.Marshal(nonNil(w.Events))
	if w.URLEnc != nil {
		_, err := s.DB.ExecContext(ctx, `UPDATE webhooks SET name=?, format=?, events=?, active=?, url_enc=?, url_hint=? WHERE id=?`,
			w.Name, w.Format, string(events), b2i(w.Active), w.URLEnc, w.URLHint, w.ID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE webhooks SET name=?, format=?, events=?, active=? WHERE id=?`,
		w.Name, w.Format, string(events), b2i(w.Active), w.ID)
	return err
}

func (s *Store) DeleteWebhook(ctx context.Context, id int64) error {
	defer s.keysChanged()
	_, err := s.DB.ExecContext(ctx, `DELETE FROM webhooks WHERE id=?`, id)
	return err
}

// RecordWebhookDelivery stores the result of the latest delivery attempt.
func (s *Store) RecordWebhookDelivery(ctx context.Context, id int64, status int, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE webhooks SET last_sent_at=?, last_status=?, last_error=? WHERE id=?`, now(), status, s.Scrub(ctx, errMsg), id)
	return err
}
