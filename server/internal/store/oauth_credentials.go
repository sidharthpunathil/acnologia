package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// OAuth credentials: keys of oauth2 services (api/oauth_connect.go). An
// admin enters the service's OAuth client (service_oauth_clients), starts a connect
// (oauth_connects: a single use state, the PKCE verifier and who started
// it), and the callback stores the account's tokens (oauth_credentials) next
// to a new key in the service's pool. The key's own secret names the
// connection and holds the extra fields; the refresh and access tokens live
// here, encrypted by the caller, and are never part of the key.
//
// Not to be confused with oauth.go, which is the authorization server MCP
// clients sign in to.

const schemaOAuthCredentials = `
CREATE TABLE IF NOT EXISTS service_oauth_clients(
  service_id INTEGER PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  client_id TEXT NOT NULL,
  client_secret_enc BLOB,
  scopes TEXT NOT NULL DEFAULT '',
  updated_by INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_credentials(
  key_id INTEGER PRIMARY KEY REFERENCES keys(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  account TEXT NOT NULL DEFAULT '',
  scopes TEXT NOT NULL DEFAULT '',
  refresh_enc BLOB,
  access_enc BLOB,
  expires_at INTEGER NOT NULL DEFAULT 0,
  refreshed_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  connected_by INTEGER NOT NULL DEFAULT 0,
  connected_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS oauth_credentials_service ON oauth_credentials(service_id);
CREATE TABLE IF NOT EXISTS oauth_connects(
  id TEXT PRIMARY KEY,
  state_hash TEXT NOT NULL UNIQUE,
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL,
  key_id INTEGER NOT NULL DEFAULT 0,
  verifier_enc BLOB,
  fields_enc BLOB,
  scopes TEXT NOT NULL DEFAULT '',
  redirect_uri TEXT NOT NULL,
  status TEXT NOT NULL,
  account TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS oauth_connects_created ON oauth_connects(created_at);
`

func migrateOAuthCredentials(db *sql.DB) error {
	_, err := db.Exec(schemaOAuthCredentials)
	return err
}

// migrateOAuthClientTenant adds the service's own tenant (Microsoft's
// common, organizations or a tenant id), which fills {tenant} in its
// provider's OAuth URLs. Empty uses the provider's default. addColumn skips
// a column that is there already, so a database whose version number went
// back (as the upgrade tests do) upgrades again.
func migrateOAuthClientTenant(db *sql.DB) error {
	_, err := addColumn(db, "service_oauth_clients", "tenant", "TEXT NOT NULL DEFAULT ''")
	return err
}

// ServiceOAuthClient is the OAuth app a service signs in with.
type ServiceOAuthClient struct {
	ServiceID       int64
	ClientID        string
	ClientSecretEnc []byte
	Scopes          []string
	// Tenant fills {tenant} in the provider's OAuth URLs; empty uses the
	// provider's default.
	Tenant    string
	UpdatedBy int64
	UpdatedAt time.Time
}

// OAuth connect states.
const (
	OAuthConnectPending = "pending"
	// OAuthConnectExchanging is a callback that took the state and is
	// trading the code for tokens. The state cannot be used again.
	OAuthConnectExchanging = "exchanging"
	OAuthConnectDone       = "done"
	OAuthConnectFailed     = "failed"
)

// OAuthConnect is one sign in started from the dashboard, the CLI or an
// agent. StateHash is the SHA-256 of the state sent to the provider; the
// state itself is never stored.
type OAuthConnect struct {
	ID          string
	StateHash   string
	ServiceID   int64
	UserID      int64
	KeyID       int64
	VerifierEnc []byte
	FieldsEnc   []byte
	Scopes      []string
	RedirectURI string
	Status      string
	Account     string
	Error       string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// OAuthCredential is a connected account's tokens, for one key.
type OAuthCredential struct {
	KeyID       int64
	ServiceID   int64
	Account     string
	Scopes      []string
	RefreshEnc  []byte
	AccessEnc   []byte
	ExpiresAt   time.Time // zero when the provider gave no expiry
	RefreshedAt time.Time
	LastError   string
	ConnectedBy int64
	ConnectedAt time.Time
	UpdatedAt   time.Time
}

func joinScopes(s []string) string { return strings.Join(s, " ") }
func splitScopes(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Fields(s)
}

func unixOrZero(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

func zeroOrUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// GetServiceOAuthClient returns a service's OAuth client, ErrNotFound when none.
func (s *Store) GetServiceOAuthClient(ctx context.Context, serviceID int64) (*ServiceOAuthClient, error) {
	var c ServiceOAuthClient
	var scopes string
	var at int64
	err := s.r.QueryRowContext(ctx, `SELECT service_id, client_id, client_secret_enc, scopes, tenant, updated_by, updated_at FROM service_oauth_clients WHERE service_id=?`, serviceID).
		Scan(&c.ServiceID, &c.ClientID, &c.ClientSecretEnc, &scopes, &c.Tenant, &c.UpdatedBy, &at)
	if err != nil {
		return nil, notFound(err)
	}
	c.Scopes, c.UpdatedAt = splitScopes(scopes), time.Unix(at, 0)
	return &c, nil
}

// SetServiceOAuthClient saves a service's OAuth client. A nil ClientSecretEnc
// keeps the stored secret.
func (s *Store) SetServiceOAuthClient(ctx context.Context, c ServiceOAuthClient) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO service_oauth_clients(service_id, client_id, client_secret_enc, scopes, tenant, updated_by, updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(service_id) DO UPDATE SET client_id=excluded.client_id,
			client_secret_enc=COALESCE(excluded.client_secret_enc, service_oauth_clients.client_secret_enc),
			scopes=excluded.scopes, tenant=excluded.tenant, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		c.ServiceID, c.ClientID, c.ClientSecretEnc, joinScopes(c.Scopes), c.Tenant, c.UpdatedBy, now())
	if err == nil {
		s.keysChanged()
	}
	return err
}

// oauthConnectKeep is how long finished connects stay for status polls.
const oauthConnectKeep = 24 * time.Hour

// CreateOAuthConnect stores a new pending connect, and drops connects
// older than a day.
func (s *Store) CreateOAuthConnect(ctx context.Context, c OAuthConnect) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM oauth_connects WHERE created_at<?`, time.Now().Add(-oauthConnectKeep).Unix()); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO oauth_connects(id, state_hash, service_id, user_id, key_id, verifier_enc, fields_enc, scopes, redirect_uri, status, created_at, expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.StateHash, c.ServiceID, c.UserID, c.KeyID, c.VerifierEnc, c.FieldsEnc, joinScopes(c.Scopes), c.RedirectURI, OAuthConnectPending,
		c.CreatedAt.Unix(), c.ExpiresAt.Unix())
	return err
}

const oauthConnectCols = `id, state_hash, service_id, user_id, key_id, verifier_enc, fields_enc, scopes, redirect_uri, status, account, error, created_at, expires_at`

func scanOAuthConnect(row interface{ Scan(...any) error }) (*OAuthConnect, error) {
	var c OAuthConnect
	var scopes string
	var created, expires int64
	if err := row.Scan(&c.ID, &c.StateHash, &c.ServiceID, &c.UserID, &c.KeyID, &c.VerifierEnc, &c.FieldsEnc, &scopes, &c.RedirectURI,
		&c.Status, &c.Account, &c.Error, &created, &expires); err != nil {
		return nil, notFound(err)
	}
	c.Scopes, c.CreatedAt, c.ExpiresAt = splitScopes(scopes), time.Unix(created, 0), time.Unix(expires, 0)
	return &c, nil
}

// GetOAuthConnect returns a connect by its id.
func (s *Store) GetOAuthConnect(ctx context.Context, id string) (*OAuthConnect, error) {
	return scanOAuthConnect(s.w.QueryRowContext(ctx, `SELECT `+oauthConnectCols+` FROM oauth_connects WHERE id=?`, id))
}

// ErrOAuthState is a state that is unknown, used or expired.
var ErrOAuthState = errors.New("unknown, used or expired state")

// TakeOAuthConnect claims the pending connect with this state hash for its
// callback. A state works once and only until it expires.
func (s *Store) TakeOAuthConnect(ctx context.Context, stateHash string, at time.Time) (*OAuthConnect, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE oauth_connects SET status=? WHERE state_hash=? AND status=? AND expires_at>?`,
		OAuthConnectExchanging, stateHash, OAuthConnectPending, at.Unix())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrOAuthState
	}
	return scanOAuthConnect(s.w.QueryRowContext(ctx, `SELECT `+oauthConnectCols+` FROM oauth_connects WHERE state_hash=?`, stateHash))
}

// FinishOAuthConnect records how a connect ended. The PKCE verifier and the
// extra fields are dropped: they are not needed again.
func (s *Store) FinishOAuthConnect(ctx context.Context, id, status string, keyID int64, account, msg string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE oauth_connects SET status=?, key_id=?, account=?, error=?, verifier_enc=NULL, fields_enc=NULL WHERE id=?`,
		status, keyID, account, s.Scrub(ctx, msg), id)
	return err
}

const oauthCredCols = `key_id, service_id, account, scopes, refresh_enc, access_enc, expires_at, refreshed_at, last_error, connected_by, connected_at, updated_at`

func scanOAuthCredential(row interface{ Scan(...any) error }) (*OAuthCredential, error) {
	var c OAuthCredential
	var scopes string
	var expires, refreshed, connected, updated int64
	if err := row.Scan(&c.KeyID, &c.ServiceID, &c.Account, &scopes, &c.RefreshEnc, &c.AccessEnc, &expires, &refreshed, &c.LastError,
		&c.ConnectedBy, &connected, &updated); err != nil {
		return nil, notFound(err)
	}
	c.Scopes = splitScopes(scopes)
	c.ExpiresAt, c.RefreshedAt = unixOrZero(expires), unixOrZero(refreshed)
	c.ConnectedAt, c.UpdatedAt = time.Unix(connected, 0), time.Unix(updated, 0)
	return &c, nil
}

// PutOAuthCredential stores a connected account's tokens for a key,
// replacing what the key had (a reconnect).
func (s *Store) PutOAuthCredential(ctx context.Context, c OAuthCredential) error {
	t := now()
	_, err := s.w.ExecContext(ctx, `INSERT INTO oauth_credentials(`+oauthCredCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(key_id) DO UPDATE SET account=excluded.account, scopes=excluded.scopes, refresh_enc=excluded.refresh_enc,
			access_enc=excluded.access_enc, expires_at=excluded.expires_at, refreshed_at=excluded.refreshed_at, last_error='',
			connected_by=excluded.connected_by, connected_at=excluded.connected_at, updated_at=excluded.updated_at`,
		c.KeyID, c.ServiceID, c.Account, joinScopes(c.Scopes), c.RefreshEnc, c.AccessEnc, zeroOrUnix(c.ExpiresAt), t, "",
		c.ConnectedBy, t, t)
	if err == nil {
		s.keysChanged()
	}
	return err
}

// GetOAuthCredential returns a key's tokens, ErrNotFound when it has none.
// It reads through the writer, so a token another request just stored is
// seen at once.
func (s *Store) GetOAuthCredential(ctx context.Context, keyID int64) (*OAuthCredential, error) {
	return scanOAuthCredential(s.w.QueryRowContext(ctx, `SELECT `+oauthCredCols+` FROM oauth_credentials WHERE key_id=?`, keyID))
}

// ListOAuthCredentials returns the connected accounts of a service.
func (s *Store) ListOAuthCredentials(ctx context.Context, serviceID int64) ([]*OAuthCredential, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+oauthCredCols+` FROM oauth_credentials WHERE service_id=? ORDER BY key_id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OAuthCredential{}
	for rows.Next() {
		c, err := scanOAuthCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateOAuthTokens stores refreshed tokens. A nil refreshEnc keeps the
// refresh token (the provider did not rotate it).
func (s *Store) UpdateOAuthTokens(ctx context.Context, keyID int64, accessEnc, refreshEnc []byte, expires time.Time) error {
	t := now()
	_, err := s.w.ExecContext(ctx, `UPDATE oauth_credentials SET access_enc=?, refresh_enc=COALESCE(?, refresh_enc), expires_at=?, refreshed_at=?, last_error='', updated_at=? WHERE key_id=?`,
		accessEnc, refreshEnc, zeroOrUnix(expires), t, t, keyID)
	if err == nil {
		s.keysChanged()
	}
	return err
}

// SetOAuthError notes why a key's last refresh failed ("" clears it).
func (s *Store) SetOAuthError(ctx context.Context, keyID int64, msg string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE oauth_credentials SET last_error=?, updated_at=? WHERE key_id=?`, s.Scrub(ctx, msg), now(), keyID)
	return err
}
