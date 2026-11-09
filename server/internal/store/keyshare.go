package store

import "context"

// Shared keys: one account's key often works for several services (Omkar
// Cloud runs each API on its own host with one key). A service can name a
// key source and use that service's keys as well as its own, and keys whose
// secret is the same in several services are reported as twins.

// SetKeySource links a service to the service whose keys it may use, or
// clears the link with nil.
func (s *Store) SetKeySource(ctx context.Context, serviceID int64, source *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET key_source_id=? WHERE id=?`, fromInt(source), serviceID)
	return err
}

// ClearKeySourcesOf drops every link to a service, for when it is deleted.
func (s *Store) ClearKeySourcesOf(ctx context.Context, sourceID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET key_source_id=NULL WHERE key_source_id=?`, sourceID)
	return err
}

// SharedCandidateKeys returns the source's keys a linked service may use:
// active, neither hidden nor restricted to some apps, and not the same
// secret as one of the linked service's own keys.
func (s *Store) SharedCandidateKeys(ctx context.Context, sourceID, serviceID int64) ([]*Key, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+keyCols+` FROM keys WHERE service_id=? AND status='active' AND hidden=0 AND restricted=0
		AND secret_fp NOT IN (SELECT secret_fp FROM keys WHERE service_id=?)`, sourceID, serviceID)
	if err != nil {
		return nil, err
	}
	return scanKeys(rows)
}

// KeyTwin is a key in another service with the same secret.
type KeyTwin struct {
	KeyID       int64  `json:"key_id"`
	Label       string `json:"label"`
	ServiceID   int64  `json:"service_id"`
	ServiceSlug string `json:"service_slug"`
	ServiceName string `json:"service_name"`
}

// FillTwins sets Twins on each key that has the same secret in another
// service. Hidden twins are listed only when includeHidden is set.
func (s *Store) FillTwins(ctx context.Context, keys []*Key, includeHidden bool) error {
	if len(keys) == 0 {
		return nil
	}
	byID := map[int64]*Key{}
	args := []any{}
	for _, k := range keys {
		byID[k.ID] = k
		args = append(args, k.ID)
	}
	q := `SELECT k.id, t.id, t.label, sv.id, sv.slug, sv.name FROM keys k
		JOIN keys t ON t.secret_fp=k.secret_fp AND t.service_id<>k.service_id
		JOIN services sv ON sv.id=t.service_id
		WHERE k.id IN (` + placeholders(len(args)) + `)`
	if !includeHidden {
		q += ` AND t.hidden=0`
	}
	rows, err := s.r.QueryContext(ctx, q+` ORDER BY sv.name, t.id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t KeyTwin
		if err := rows.Scan(&id, &t.KeyID, &t.Label, &t.ServiceID, &t.ServiceSlug, &t.ServiceName); err != nil {
			return err
		}
		if k := byID[id]; k != nil {
			k.Twins = append(k.Twins, t)
		}
	}
	return rows.Err()
}

// DuplicateKeyGroups lists secrets stored in more than one service, as the
// keys that hold them. Hidden keys are left out.
func (s *Store) DuplicateKeyGroups(ctx context.Context) ([][]KeyTwin, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT k.id, k.label, k.secret_fp, sv.id, sv.slug, sv.name FROM keys k JOIN services sv ON sv.id=k.service_id
		WHERE k.hidden=0 AND k.secret_fp IN (SELECT secret_fp FROM keys WHERE hidden=0 GROUP BY secret_fp HAVING COUNT(DISTINCT service_id)>1)
		ORDER BY k.secret_fp, sv.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]KeyTwin{}
	last := ""
	for rows.Next() {
		var t KeyTwin
		var fp string
		if err := rows.Scan(&t.KeyID, &t.Label, &fp, &t.ServiceID, &t.ServiceSlug, &t.ServiceName); err != nil {
			return nil, err
		}
		if fp != last || len(out) == 0 {
			out = append(out, nil)
			last = fp
		}
		out[len(out)-1] = append(out[len(out)-1], t)
	}
	return out, rows.Err()
}
