package store

import (
	"context"

	"github.com/sidharthpunathil/acnologia/server/internal/redact"
)

// The store is where text meets the database, so it is the choke point for
// redaction: event, audit, agent step, run and webhook error text, and key
// last_error, all go through Scrub before they are written. The redactor is
// attached by the server at startup (it needs the Box to decrypt the pool);
// without one, Scrub still applies context extras and patterns.

// SetRedactor attaches the Redactor used by Scrub.
func (s *Store) SetRedactor(r *redact.Redactor) { s.red.Store(r) }

// Redactor returns the attached Redactor, or nil.
func (s *Store) Redactor() *redact.Redactor {
	if s == nil {
		return nil
	}
	return s.red.Load()
}

// Scrub removes known and pattern matched secrets from text before it is
// stored, logged or sent anywhere.
func (s *Store) Scrub(ctx context.Context, text string) string {
	return s.Redactor().String(ctx, text)
}

// keysChanged tells the redactor to reload the pool's secrets.
func (s *Store) keysChanged() { s.Redactor().Invalidate() }

// SecretBlobs returns every encrypted secret the redactor should know: pool
// keys, webhook URLs and signing secrets, egress proxy URLs, and the share
// tokens this server holds for other servers.
func (s *Store) SecretBlobs(ctx context.Context) ([][]byte, error) {
	out := [][]byte{}
	for _, q := range []string{
		`SELECT secret_enc FROM keys`,
		`SELECT url_enc FROM webhooks`,
		`SELECT secret_enc FROM webhooks WHERE secret_enc IS NOT NULL`,
		`SELECT url_enc FROM egress_proxies`,
		`SELECT token_enc FROM peer_links WHERE token_enc IS NOT NULL`,
	} {
		rows, err := s.DB.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				rows.Close()
				return nil, err
			}
			if len(b) > 0 {
				out = append(out, b)
			}
		}
		err = rows.Close()
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
