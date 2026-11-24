package store

// The trash. Deleting a key, a service or an app from the admin API moves
// it here for TrashTTL instead of removing it: the row stays, with
// deleted_at set, and every read path leaves it out (each query on keys,
// services and apps filters deleted_at IS NULL, or selects from the live*
// subqueries below). Its children (a service's keys, an app's tokens,
// limits and MCP settings) stay as they are, so a restore brings the item
// back exactly as it was. Two columns are renamed while an item is in the
// trash so a new item can take its place: a service's slug (unique, and
// what apps, groups and capabilities name it by) and a key's fingerprint
// (unique per service, and how shared keys are matched). A purge, by a
// superadmin or by the janitor once TrashTTL has passed, deletes for real
// through DeleteKey, DeleteService and DeleteApp.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
)

// TrashTTL is how long a deleted item can be restored.
const TrashTTL = 7 * 24 * time.Hour

// Trash kinds.
const (
	TrashKey     = "key"
	TrashService = "service"
	TrashApp     = "app"
)

// Live views of the three tables, for queries that take a where clause from
// a caller or join several tables. SQLite flattens these subqueries, so the
// indexes of the underlying table still serve them.
const (
	liveKeys     = `(SELECT * FROM keys WHERE deleted_at IS NULL)`
	liveServices = `(SELECT * FROM services WHERE deleted_at IS NULL)`
	liveApps     = `(SELECT * FROM apps WHERE deleted_at IS NULL)`
)

// ErrRestoreConflict means the item's slug or secret was taken while it was
// in the trash. ErrRestoreBlocked means it needs another item restored first
// (a key whose service is in the trash).
var (
	ErrRestoreConflict = errors.New("restore conflict")
	ErrRestoreBlocked  = errors.New("restore blocked")
)

// TrashItem is one deleted item. It never holds a secret.
type TrashItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	TargetID int64  `json:"target_id"`
	// Name is the key's label, the service's slug or the app's name.
	Name string `json:"name"`
	// Service is the slug of the service a key belongs to.
	Service string `json:"service,omitempty"`
	// Hint is a key's masked hint, as the keys list shows it.
	Hint string `json:"hint,omitempty"`
	// Keys counts the keys a service took with it.
	Keys int `json:"keys,omitempty"`
	// Hidden is set when the item is or holds a hidden key: only a
	// superadmin sees, restores or purges it.
	Hidden    bool      `json:"hidden"`
	DeletedBy string    `json:"deleted_by"`
	DeletedAt time.Time `json:"deleted_at"`
	ExpiresAt time.Time `json:"expires_at"`

	detail trashDetail
}

// trashDetail is what a restore needs, stored as JSON.
type trashDetail struct {
	Slug      string  `json:"slug,omitempty"`
	ServiceID int64   `json:"service_id,omitempty"`
	Service   string  `json:"service,omitempty"`
	Hint      string  `json:"hint,omitempty"`
	KeyIDs    []int64 `json:"key_ids,omitempty"`
	Borrowers []int64 `json:"borrowers,omitempty"`
}

const schemaTrash = `
CREATE TABLE IF NOT EXISTS trash(
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  target_id INTEGER NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '{}',
  hidden INTEGER NOT NULL DEFAULT 0,
  deleted_by INTEGER,
  deleted_by_email TEXT NOT NULL DEFAULT '',
  deleted_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS trash_expires ON trash(expires_at);
`

// migrateTrash adds deleted_at to keys, services and apps and creates the
// trash table.
func migrateTrash(db *sql.DB) error {
	for _, t := range []string{"keys", "services", "apps"} {
		if _, err := addColumn(db, t, "deleted_at", "INTEGER"); err != nil {
			return err
		}
	}
	_, err := db.Exec(schemaTrash)
	return err
}

// fpPrefix is what a trashed key's fingerprint starts with.
func fpPrefix(trashID string) string { return "trash:" + trashID + ":" }

func newTrashID() string { return crypto.RandomToken("trs_", 12) }

func (s *Store) insertTrash(ctx context.Context, tx *sql.Tx, it *TrashItem, by *User) error {
	d, _ := json.Marshal(it.detail)
	var uid any
	email := "system"
	if by != nil {
		uid, email = by.ID, by.Email
	}
	it.DeletedBy = email
	_, err := tx.ExecContext(ctx, `INSERT INTO trash(id,kind,target_id,name,detail,hidden,deleted_by,deleted_by_email,deleted_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, it.ID, it.Kind, it.TargetID, it.Name, string(d), b2i(it.Hidden), uid, email,
		it.DeletedAt.Unix(), it.ExpiresAt.Unix())
	return err
}

func newTrashItem(kind string, target int64, name string) *TrashItem {
	t := time.Unix(now(), 0).UTC()
	return &TrashItem{ID: newTrashID(), Kind: kind, TargetID: target, Name: name, DeletedAt: t, ExpiresAt: t.Add(TrashTTL)}
}

// TrashKeyByID moves a key to the trash.
func (s *Store) TrashKeyByID(ctx context.Context, id int64, by *User) (*TrashItem, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var label, hint, slug string
	var hidden int
	var sid int64
	err = tx.QueryRowContext(ctx, `SELECT k.label, k.hint, k.hidden, k.service_id, sv.slug FROM keys k JOIN services sv ON sv.id=k.service_id
		WHERE k.id=? AND k.deleted_at IS NULL AND sv.deleted_at IS NULL`, id).Scan(&label, &hint, &hidden, &sid, &slug)
	if err != nil {
		return nil, notFound(err)
	}
	it := newTrashItem(TrashKey, id, label)
	it.Hidden, it.Service, it.Hint = hidden == 1, slug, hint
	it.detail = trashDetail{ServiceID: sid, Service: slug, Hint: hint}
	if _, err := tx.ExecContext(ctx, `UPDATE keys SET deleted_at=?, secret_fp=?||secret_fp WHERE id=?`, it.DeletedAt.Unix(), fpPrefix(it.ID), id); err != nil {
		return nil, err
	}
	if err := s.insertTrash(ctx, tx, it, by); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return it, nil
}

// TrashServiceByID moves a service and its keys to the trash. Services that
// used its keys go back to their own, and get them again on a restore.
func (s *Store) TrashServiceByID(ctx context.Context, id int64, by *User) (*TrashItem, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var slug string
	if err := tx.QueryRowContext(ctx, `SELECT slug FROM services WHERE id=? AND deleted_at IS NULL`, id).Scan(&slug); err != nil {
		return nil, notFound(err)
	}
	it := newTrashItem(TrashService, id, slug)
	it.detail.Slug = slug
	rows, err := tx.QueryContext(ctx, `SELECT id, hidden FROM keys WHERE service_id=? AND deleted_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kid int64
		var hidden int
		if err := rows.Scan(&kid, &hidden); err != nil {
			rows.Close()
			return nil, err
		}
		it.detail.KeyIDs = append(it.detail.KeyIDs, kid)
		if hidden == 1 {
			it.Hidden = true
		}
	}
	rows.Close()
	if it.detail.Borrowers, err = int64s(ctx, tx, `SELECT id FROM services WHERE key_source_id=? AND deleted_at IS NULL`, id); err != nil {
		return nil, err
	}
	it.Keys = len(it.detail.KeyIDs)
	at := it.DeletedAt.Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE services SET deleted_at=?, slug=? WHERE id=?`, at, ".trash."+it.ID, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE keys SET deleted_at=?, secret_fp=?||secret_fp WHERE service_id=? AND deleted_at IS NULL`, at, fpPrefix(it.ID), id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET key_source_id=NULL WHERE key_source_id=?`, id); err != nil {
		return nil, err
	}
	if err := s.insertTrash(ctx, tx, it, by); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return it, nil
}

// TrashAppByID moves an app to the trash; its tokens stop working until a
// restore. Apps backing a share are never trashed.
func (s *Store) TrashAppByID(ctx context.Context, id int64, by *User) (*TrashItem, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM apps WHERE id=? AND deleted_at IS NULL AND share_id IS NULL`, id).Scan(&name); err != nil {
		return nil, notFound(err)
	}
	it := newTrashItem(TrashApp, id, name)
	if _, err := tx.ExecContext(ctx, `UPDATE apps SET deleted_at=? WHERE id=?`, it.DeletedAt.Unix(), id); err != nil {
		return nil, err
	}
	if err := s.insertTrash(ctx, tx, it, by); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateScopes()
	return it, nil
}

func int64s(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const trashCols = `id, kind, target_id, name, detail, hidden, deleted_by_email, deleted_at, expires_at`

func scanTrash(row interface{ Scan(...any) error }) (*TrashItem, error) {
	var it TrashItem
	var detail string
	var hidden int
	var del, exp int64
	if err := row.Scan(&it.ID, &it.Kind, &it.TargetID, &it.Name, &detail, &hidden, &it.DeletedBy, &del, &exp); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(detail), &it.detail)
	it.Hidden = hidden == 1
	it.DeletedAt, it.ExpiresAt = time.Unix(del, 0).UTC(), time.Unix(exp, 0).UTC()
	it.Service, it.Hint, it.Keys = it.detail.Service, it.detail.Hint, len(it.detail.KeyIDs)
	return &it, nil
}

// trashTargetExists keeps entries whose item is still in the trash: a
// purge elsewhere (a service taking its trashed keys with it, a peer
// revoke) leaves its entry behind until the janitor clears it.
const trashTargetExists = `((kind='key' AND EXISTS (SELECT 1 FROM keys WHERE id=trash.target_id AND deleted_at IS NOT NULL))
 OR (kind='service' AND EXISTS (SELECT 1 FROM services WHERE id=trash.target_id AND deleted_at IS NOT NULL))
 OR (kind='app' AND EXISTS (SELECT 1 FROM apps WHERE id=trash.target_id AND deleted_at IS NOT NULL)))`

// ListTrash lists the trash, newest first. Hidden items are left out unless
// includeHidden.
func (s *Store) ListTrash(ctx context.Context, includeHidden bool) ([]*TrashItem, error) {
	q := `SELECT ` + trashCols + ` FROM trash WHERE ` + trashTargetExists
	if !includeHidden {
		q += ` AND hidden=0`
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY deleted_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TrashItem{}
	for rows.Next() {
		it, err := scanTrash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetTrash returns one trash entry whose item is still in the trash.
func (s *Store) GetTrash(ctx context.Context, id string) (*TrashItem, error) {
	return scanTrash(s.DB.QueryRowContext(ctx, `SELECT `+trashCols+` FROM trash WHERE id=? AND `+trashTargetExists, id))
}

// RestoreTrash brings an item back as it was: a key rejoins its service's
// pool, a service comes back with the keys it took along (and the services
// that used its keys use them again), an app's tokens work again.
func (s *Store) RestoreTrash(ctx context.Context, id string) (*TrashItem, error) {
	it, err := s.GetTrash(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	unique := func(err error, what string) error {
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("%w: %s", ErrRestoreConflict, what)
		}
		return err
	}
	pre := fpPrefix(it.ID)
	switch it.Kind {
	case TrashKey:
		var svDeleted sql.NullInt64
		var slug string
		if err := tx.QueryRowContext(ctx, `SELECT sv.deleted_at, sv.slug FROM keys k JOIN services sv ON sv.id=k.service_id WHERE k.id=?`, it.TargetID).Scan(&svDeleted, &slug); err != nil {
			return nil, notFound(err)
		}
		if svDeleted.Valid {
			return nil, fmt.Errorf("%w: its service %s is in the trash; restore the service first", ErrRestoreBlocked, it.detail.Service)
		}
		_, err := tx.ExecContext(ctx, `UPDATE keys SET deleted_at=NULL, secret_fp=CASE WHEN substr(secret_fp,1,?)=? THEN substr(secret_fp,?) ELSE secret_fp END WHERE id=?`,
			len(pre), pre, len(pre)+1, it.TargetID)
		if err = unique(err, "the service already has a key with this secret"); err != nil {
			return nil, err
		}
	case TrashService:
		var prov string
		var known int
		if err := tx.QueryRowContext(ctx, `SELECT provider, (SELECT COUNT(*) FROM providers WHERE slug=services.provider) FROM services WHERE id=?`, it.TargetID).Scan(&prov, &known); err != nil {
			return nil, notFound(err)
		}
		if known == 0 {
			return nil, fmt.Errorf("%w: its provider %s was deleted; add the provider again first", ErrRestoreBlocked, prov)
		}
		_, err := tx.ExecContext(ctx, `UPDATE services SET deleted_at=NULL, slug=? WHERE id=?`, it.detail.Slug, it.TargetID)
		if err = unique(err, "another service now has the slug "+it.detail.Slug+"; rename it, then restore"); err != nil {
			return nil, err
		}
		for _, kid := range it.detail.KeyIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE keys SET deleted_at=NULL, secret_fp=substr(secret_fp,?) WHERE id=? AND service_id=? AND substr(secret_fp,1,?)=?`,
				len(pre)+1, kid, it.TargetID, len(pre), pre); err != nil {
				return nil, err
			}
		}
		for _, b := range it.detail.Borrowers {
			if _, err := tx.ExecContext(ctx, `UPDATE services SET key_source_id=? WHERE id=? AND key_source_id IS NULL AND deleted_at IS NULL`, it.TargetID, b); err != nil {
				return nil, err
			}
		}
	case TrashApp:
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET deleted_at=NULL WHERE id=?`, it.TargetID); err != nil {
			return nil, err
		}
	default:
		return nil, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM trash WHERE id=?`, it.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateScopes()
	return it, nil
}

// PurgeTrash deletes an item for good. A service takes the trash entries
// of its own trashed keys with it.
func (s *Store) PurgeTrash(ctx context.Context, id string) (*TrashItem, error) {
	it, err := scanTrash(s.DB.QueryRowContext(ctx, `SELECT `+trashCols+` FROM trash WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if err := s.purgeItem(ctx, it); err != nil {
		return nil, err
	}
	return it, nil
}

// trashedTables names each kind's table.
var trashedTables = map[string]string{TrashKey: "keys", TrashService: "services", TrashApp: "apps"}

func (s *Store) purgeItem(ctx context.Context, it *TrashItem) error {
	table, ok := trashedTables[it.Kind]
	if !ok {
		return ErrNotFound
	}
	// Only a row still in the trash goes: ids are never reused, but be sure.
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id=? AND deleted_at IS NOT NULL`, it.TargetID).Scan(&n); err != nil {
		return err
	}
	var err error
	if n > 0 {
		switch it.Kind {
		case TrashKey:
			err = s.DeleteKey(ctx, it.TargetID)
		case TrashService:
			err = s.DeleteService(ctx, it.TargetID)
		case TrashApp:
			err = s.DeleteApp(ctx, it.TargetID)
		}
	}
	if err == nil && it.Kind == TrashService {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM trash WHERE kind='key' AND json_extract(detail,'$.service_id')=?`, it.TargetID)
	}
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM trash WHERE id=?`, it.ID)
	return err
}

// PurgeExpiredTrash deletes every item whose time in the trash is up, and
// entries whose item is gone already. Each purge is audited.
func (s *Store) PurgeExpiredTrash(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+trashCols+` FROM trash WHERE expires_at<=? OR NOT `+trashTargetExists, now())
	if err != nil {
		return 0, err
	}
	items := []*TrashItem{}
	for rows.Next() {
		it, err := scanTrash(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	n := 0
	for _, it := range items {
		if err := s.purgeItem(ctx, it); err != nil {
			return n, err
		}
		if it.ExpiresAt.Unix() <= now() {
			s.AddAudit(ctx, nil, "trash.purge", it.Kind+" "+it.Name, "expired after 7 days in the trash")
		}
		n++
	}
	return n, nil
}

// LiveKeyCount counts a service's keys outside the trash, hidden ones too.
func (s *Store) LiveKeyCount(ctx context.Context, serviceID int64) int {
	var n int
	_ = s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM keys WHERE service_id=? AND deleted_at IS NULL`, serviceID).Scan(&n)
	return n
}

// KeyFingerprintsIn returns which of fps a service's live keys already have,
// for a dry run of adding keys.
func (s *Store) KeyFingerprintsIn(ctx context.Context, serviceID int64, fps []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(fps) == 0 {
		return out, nil
	}
	args := []any{serviceID}
	for _, fp := range fps {
		args = append(args, fp)
	}
	rows, err := s.rd.QueryContext(ctx, `SELECT secret_fp FROM keys WHERE service_id=? AND deleted_at IS NULL AND secret_fp IN (`+placeholders(len(fps))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		out[fp] = true
	}
	return out, rows.Err()
}
