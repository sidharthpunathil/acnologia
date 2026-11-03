package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// A share lists services by slug, and an admin can rename a service or give
// a freed slug to another service or a group. shares.service_ids records
// which service each shared slug meant when it was shared, so a share keeps
// reaching that service and nothing that later took its slug.

func migrateShareServiceIDs(db *sql.DB) error {
	added, err := addColumn(db, "shares", "service_ids", "TEXT NOT NULL DEFAULT '{}'")
	if err != nil || !added {
		return err
	}
	// Existing shares bind to what their slugs name today.
	rows, err := db.Query(`SELECT id, services FROM shares`)
	if err != nil {
		return err
	}
	type row struct{ id, services string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.services); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		var slugs []string
		_ = json.Unmarshal([]byte(r.services), &slugs)
		ids, err := bindServiceIDs(context.Background(), db, slugs, nil)
		if err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE shares SET service_ids=? WHERE id=?`, ids, r.id); err != nil {
			return err
		}
	}
	return nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// bindServiceIDs maps each slug to its service id, keeping the ids already
// bound in old so an edit of the share does not re-bind a slug that now
// names another service.
func bindServiceIDs(ctx context.Context, q queryer, slugs []string, old map[string]int64) (string, error) {
	out := map[string]int64{}
	for _, slug := range slugs {
		if id, ok := old[slug]; ok {
			out[slug] = id
			continue
		}
		var id int64
		err := q.QueryRowContext(ctx, `SELECT id FROM services WHERE slug=?`, slug).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		out[slug] = id
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func decodeServiceIDs(s string) map[string]int64 {
	out := map[string]int64{}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// ShareServiceID returns the id of the service a share app's slug was
// shared as. ok is false when the app backs no share or the slug was not
// bound to a service.
func (s *Store) ShareServiceID(ctx context.Context, appID int64, slug string) (id int64, ok bool, err error) {
	var raw string
	err = s.DB.QueryRowContext(ctx, `SELECT service_ids FROM shares WHERE app_id=?`, appID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	id, ok = decodeServiceIDs(raw)[slug]
	return id, ok, nil
}
