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
	// appTokensHighWater leaves events out: no index covers events.token_id,
	// so MAX(token_id) would scan every event on each new token. The counter
	// covers it instead. migrateAppTokenIDs seeds it once from the events,
	// and the app_token_ids_not_reused trigger keeps it current on every
	// insert, so it is never below an id the events carry.
	appTokensHighWater = `SELECT MAX(COALESCE((SELECT CAST(v AS INTEGER) FROM meta WHERE k='app_tokens_seq'),0),
		COALESCE((SELECT MAX(id) FROM app_tokens),0))`
)

// App tokens never reuse an id either: events.token_id records which token
// made each call, and a reused id would put a deleted token's (or a deleted
// app's) calls under a new token. The app_token_ids_not_reused trigger
// keeps the app_tokens_seq counter: after each insert it moves a row whose
// id is not above the counter (SQLite picks MAX(id)+1, which can be a
// deleted token's id) to counter+1, then raises the counter to the highest
// id. Inserts that let SQLite pick the id (share and people claims) are
// covered by it; nextAppTokenID gives the others their id up front, so
// they need not trust LastInsertId. It runs on admin writes only (token
// create, rotate, claim), never on a request.
//
// The seed reads MAX(token_id) over every event once, the first time the
// counter is missing (about 16ms per million events); after that the
// NOT EXISTS skips it.
func migrateAppTokenIDs(db *sql.DB) error {
	if _, err := db.Exec(`INSERT INTO meta(k,v) SELECT 'app_tokens_seq',
		MAX(COALESCE((SELECT MAX(id) FROM app_tokens),0), COALESCE((SELECT MAX(token_id) FROM events),0))
		WHERE NOT EXISTS (SELECT 1 FROM meta WHERE k='app_tokens_seq')`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE TRIGGER IF NOT EXISTS app_token_ids_not_reused AFTER INSERT ON app_tokens BEGIN
		UPDATE app_tokens SET id=(SELECT CAST(v AS INTEGER) FROM meta WHERE k='app_tokens_seq')+1
			WHERE id=NEW.id AND NEW.id<=(SELECT CAST(v AS INTEGER) FROM meta WHERE k='app_tokens_seq');
		UPDATE meta SET v=(SELECT MAX(id) FROM app_tokens) WHERE k='app_tokens_seq';
		INSERT INTO meta(k,v) SELECT 'app_tokens_seq', (SELECT MAX(id) FROM app_tokens)
			WHERE NOT EXISTS (SELECT 1 FROM meta WHERE k='app_tokens_seq');
	END`)
	return err
}

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

// nextAppTokenID returns the id for a new app token. Unlike nextID it does
// not record the id: the trigger does that after the insert, and a counter
// already at the id would make the trigger move the row.
func nextAppTokenID(ctx context.Context, tx *sql.Tx) (int64, error) {
	var hw sql.NullInt64
	if err := tx.QueryRowContext(ctx, appTokensHighWater).Scan(&hw); err != nil {
		return 0, err
	}
	return hw.Int64 + 1, nil
}
