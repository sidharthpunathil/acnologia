package store

import (
	"context"
	"database/sql"
	"errors"
)

// LookupMeta reads a meta value. A missing key is found=false with a nil
// error; any other failure is returned, so a caller that would create the
// value on a miss cannot mistake a failed read for an empty row.
func (s *Store) LookupMeta(ctx context.Context, k string) (v string, found bool, err error) {
	err = s.w.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// InsertMetaIfAbsent stores v under k only when k has no value yet, so two
// first users of a value made once (a server identity) agree on one. Read
// the value back afterwards to get the one that won.
func (s *Store) InsertMetaIfAbsent(ctx context.Context, k, v string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO NOTHING`, k, v)
	return err
}
