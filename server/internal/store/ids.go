package store

import (
	"context"
	"database/sql"
	"strconv"
)

// Apps and services, like keys, never reuse an id. Their tables use a plain
// INTEGER PRIMARY KEY, which SQLite would hand out again after the newest
// row is deleted, and events (kept for the retention period, with no
// foreign key) would then show the deleted app's or service's traffic under
// the new one: in the event log, the overview counts, the usage report and
// a share's per service counts. Each new row takes the next id above a
// high water mark: a counter in meta, the table's own ids, and the ids its
// events still carry (events_app and events_service make that MAX cheap).
const (
	appsHighWater = `SELECT MAX(COALESCE((SELECT CAST(v AS INTEGER) FROM meta WHERE k='apps_seq'),0),
		COALESCE((SELECT MAX(id) FROM apps),0), COALESCE((SELECT MAX(app_id) FROM events),0))`
	servicesHighWater = `SELECT MAX(COALESCE((SELECT CAST(v AS INTEGER) FROM meta WHERE k='services_seq'),0),
		COALESCE((SELECT MAX(id) FROM services),0), COALESCE((SELECT MAX(service_id) FROM events),0))`
)

// nextID returns the id for a new row and records it as the high water
// mark. Call it in the transaction that inserts the row.
func nextID(ctx context.Context, tx *sql.Tx, highWater, seqKey string) (int64, error) {
	var hw sql.NullInt64
	if err := tx.QueryRowContext(ctx, highWater).Scan(&hw); err != nil {
		return 0, err
	}
	id := hw.Int64 + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`,
		seqKey, strconv.FormatInt(id, 10)); err != nil {
		return 0, err
	}
	return id, nil
}
