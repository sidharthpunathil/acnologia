package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// OAuth for the MCP endpoints (api/oauth.go). Hosted MCP clients register
// (oauth_clients), a signed in person approves a connection (oauth_codes,
// then oauth_grants), and the client holds an access and a refresh token
// (oauth_tokens). A connection to /mcp acts as one app. A connection to
// /mcp/control has no app: it acts as the person who approved it, with the
// admin scope in access (read, write or admin). Only the SHA-256 of a code or token is kept,
// as with app tokens. A grant is one token family: refresh tokens rotate on
// every use, and a spent one coming back revokes the whole grant.

// The table definitions sit apart from schemaOAuth so the migration that
// lets a grant have no app (a control MCP connection acts as a person, not
// an app) rebuilds the tables from the same text.
const (
	oauthCodesDDL = `CREATE TABLE IF NOT EXISTS oauth_codes(
  hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  redirect_uri TEXT NOT NULL,
  code_challenge TEXT NOT NULL,
  resource TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT '',
  app_id INTEGER REFERENCES apps(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  read_only INTEGER NOT NULL DEFAULT 0,
  access TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at INTEGER,
  grant_id TEXT
);`
	oauthGrantsDDL = `CREATE TABLE IF NOT EXISTS oauth_grants(
  id TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  client_name TEXT NOT NULL DEFAULT '',
  app_id INTEGER REFERENCES apps(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  read_only INTEGER NOT NULL DEFAULT 0,
  access TEXT NOT NULL DEFAULT '',
  scope TEXT NOT NULL DEFAULT '',
  resource TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at INTEGER
);`
	oauthTokensDDL = `CREATE TABLE IF NOT EXISTS oauth_tokens(
  id INTEGER PRIMARY KEY,
  hash TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  spent_at INTEGER
);`
	oauthIndexes = `CREATE INDEX IF NOT EXISTS oauth_grants_app ON oauth_grants(app_id);
CREATE INDEX IF NOT EXISTS oauth_grants_user ON oauth_grants(user_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_grant ON oauth_tokens(grant_id);`
)

const schemaOAuth = `
CREATE TABLE IF NOT EXISTS oauth_clients(
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  redirect_uris TEXT NOT NULL DEFAULT '[]',
  kind TEXT NOT NULL DEFAULT 'dcr',
  created_at INTEGER NOT NULL,
  fetched_at INTEGER
);
` + oauthCodesDDL + "\n" + oauthGrantsDDL + "\n" + oauthTokensDDL

func migrateOAuth(db *sql.DB) error {
	if _, err := db.Exec(schemaOAuth); err != nil {
		return err
	}
	if err := migrateOAuthControl(db); err != nil {
		return err
	}
	_, err := db.Exec(oauthIndexes)
	return err
}

// migrateOAuthControl lets oauth_codes and oauth_grants hold a connection
// with no app, for the control MCP, and adds the access column. SQLite
// cannot drop a NOT NULL, so tables made before it are rebuilt. Foreign keys
// stay on inside the migration's transaction and dropping oauth_grants would
// cascade into oauth_tokens, so the tokens wait in a plain copy while their
// parent is rebuilt.
func migrateOAuthControl(db *sql.DB) error {
	var strict int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('oauth_grants') WHERE name='app_id' AND "notnull"=1`).Scan(&strict); err != nil {
		return err
	}
	if strict == 0 {
		return nil
	}
	const grantCols = `id,client_id,client_name,app_id,user_id,read_only,scope,resource,created_at,last_used_at,revoked_at`
	const tokenCols = `id,hash,kind,grant_id,created_at,expires_at,spent_at`
	const codeCols = `hash,client_id,redirect_uri,code_challenge,resource,scope,app_id,user_id,read_only,created_at,expires_at,used_at,grant_id`
	for _, q := range []string{
		`CREATE TABLE oauth_tokens_keep AS SELECT ` + tokenCols + ` FROM oauth_tokens`,
		`DROP TABLE oauth_tokens`,
		strings.Replace(oauthGrantsDDL, "oauth_grants(", "oauth_grants_next(", 1),
		`INSERT INTO oauth_grants_next(` + grantCols + `) SELECT ` + grantCols + ` FROM oauth_grants`,
		`DROP TABLE oauth_grants`,
		`ALTER TABLE oauth_grants_next RENAME TO oauth_grants`,
		strings.Replace(oauthCodesDDL, "oauth_codes(", "oauth_codes_next(", 1),
		`INSERT INTO oauth_codes_next(` + codeCols + `) SELECT ` + codeCols + ` FROM oauth_codes`,
		`DROP TABLE oauth_codes`,
		`ALTER TABLE oauth_codes_next RENAME TO oauth_codes`,
		oauthTokensDDL,
		`INSERT INTO oauth_tokens(` + tokenCols + `) SELECT ` + tokenCols + ` FROM oauth_tokens_keep`,
		`DROP TABLE oauth_tokens_keep`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Token kinds in oauth_tokens.
const (
	OAuthAccess  = "access"
	OAuthRefresh = "refresh"
)

// OAuth client kinds: registered with POST /oauth/register, or a client ID
// metadata document fetched from the https URL that is the client_id.
const (
	OAuthClientDCR  = "dcr"
	OAuthClientCIMD = "cimd"
)

var (
	// ErrOAuthUsed is a code or refresh token presented a second time.
	ErrOAuthUsed = errors.New("already used")
	// ErrOAuthExpired is a code or token past its expiry.
	ErrOAuthExpired = errors.New("expired")
	// ErrOAuthRevoked is a token whose grant was revoked.
	ErrOAuthRevoked = errors.New("revoked")
)

// OAuthClient is a registered MCP client. Public clients only: there is no
// secret, PKCE proves the token request comes from whoever started it.
type OAuthClient struct {
	ID           string     `json:"client_id"`
	Name         string     `json:"client_name"`
	RedirectURIs []string   `json:"redirect_uris"`
	Kind         string     `json:"kind"`
	CreatedAt    time.Time  `json:"created_at"`
	FetchedAt    *time.Time `json:"fetched_at,omitempty"`
}

// SaveOAuthClient inserts a client, or replaces a metadata document client
// with what was just fetched.
func (s *Store) SaveOAuthClient(ctx context.Context, c *OAuthClient) error {
	uris, _ := json.Marshal(nonNil(c.RedirectURIs))
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_clients(id,name,redirect_uris,kind,created_at,fetched_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, redirect_uris=excluded.redirect_uris, fetched_at=excluded.fetched_at`,
		c.ID, c.Name, string(uris), c.Kind, c.CreatedAt.Unix(), fromTime(c.FetchedAt))
	return err
}

// GetOAuthClient returns a client by its client_id.
func (s *Store) GetOAuthClient(ctx context.Context, id string) (*OAuthClient, error) {
	var c OAuthClient
	var uris string
	var created int64
	var fetched sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,redirect_uris,kind,created_at,fetched_at FROM oauth_clients WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &uris, &c.Kind, &created, &fetched)
	if err != nil {
		return nil, notFound(err)
	}
	c.RedirectURIs = []string{}
	_ = json.Unmarshal([]byte(uris), &c.RedirectURIs)
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.FetchedAt = toTime(fetched)
	return &c, nil
}

// OAuthCode is an authorization code waiting to be exchanged. Hash is the
// SHA-256 of the code.
type OAuthCode struct {
	Hash          string
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Resource      string
	Scope         string
	// AppID is 0 for a control MCP connection, which acts as UserID.
	AppID    int64
	UserID   int64
	ReadOnly bool
	// Access is a control MCP connection's admin scope: read, write or admin.
	Access    string
	ExpiresAt time.Time
	// GrantID is set once the code was exchanged.
	GrantID string
}

// appRef stores an app id, or NULL for a connection with no app.
func appRef(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// CreateOAuthCode stores a new code and drops codes that expired an hour ago.
func (s *Store) CreateOAuthCode(ctx context.Context, c *OAuthCode) error {
	t := now()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM oauth_codes WHERE expires_at<?`, t-3600); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_codes(hash,client_id,redirect_uri,code_challenge,resource,scope,app_id,user_id,read_only,access,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Hash, c.ClientID, c.RedirectURI, c.CodeChallenge, c.Resource, c.Scope, appRef(c.AppID), c.UserID, b2i(c.ReadOnly), c.Access, t, c.ExpiresAt.Unix())
	return err
}

// GetOAuthCode reads a code by hash, used or not.
func (s *Store) GetOAuthCode(ctx context.Context, hash string) (*OAuthCode, bool, error) {
	var c OAuthCode
	var ro int
	var exp int64
	var used sql.NullInt64
	var grant sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT hash,client_id,redirect_uri,code_challenge,resource,scope,COALESCE(app_id,0),user_id,read_only,access,expires_at,used_at,grant_id
		FROM oauth_codes WHERE hash=?`, hash).
		Scan(&c.Hash, &c.ClientID, &c.RedirectURI, &c.CodeChallenge, &c.Resource, &c.Scope, &c.AppID, &c.UserID, &ro, &c.Access, &exp, &used, &grant)
	if err != nil {
		return nil, false, notFound(err)
	}
	c.ReadOnly = ro != 0
	c.ExpiresAt = time.Unix(exp, 0).UTC()
	c.GrantID = grant.String
	return &c, used.Valid, nil
}

// OAuthGrant is one approved connection: a client acting as an app for the
// person who approved it, or, with no app, as that person on the control
// MCP. Its tokens form one family.
type OAuthGrant struct {
	ID         string `json:"id"`
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
	// AppID is 0 for a control MCP connection.
	AppID     int64  `json:"app_id"`
	UserID    int64  `json:"user_id"`
	UserEmail string `json:"user_email"`
	ReadOnly  bool   `json:"read_only"`
	// Access is a control MCP connection's admin scope (read, write or
	// admin), empty for an app connection.
	Access     string     `json:"access"`
	Scope      string     `json:"scope"`
	Resource   string     `json:"resource"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// OAuthTokenPair is a new access token and refresh token, by hash, and when
// each expires.
type OAuthTokenPair struct {
	AccessHash, RefreshHash     string
	AccessExpiry, RefreshExpiry time.Time
}

func (s *Store) insertPair(ctx context.Context, tx *sql.Tx, grantID string, p OAuthTokenPair) error {
	t := now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens(hash,kind,grant_id,created_at,expires_at) VALUES(?,?,?,?,?),(?,?,?,?,?)`,
		p.AccessHash, OAuthAccess, grantID, t, p.AccessExpiry.Unix(),
		p.RefreshHash, OAuthRefresh, grantID, t, p.RefreshExpiry.Unix()); err != nil {
		return err
	}
	// Tokens that expired a day ago serve neither requests nor reuse
	// detection any more.
	_, err := tx.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE expires_at<?`, t-86400)
	return err
}

// RedeemOAuthCode marks a code used and creates its grant and first tokens,
// in one transaction. A code that was already used returns ErrOAuthUsed and
// revokes the grant it produced, since someone else holds it.
func (s *Store) RedeemOAuthCode(ctx context.Context, codeHash string, g *OAuthGrant, p OAuthTokenPair) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.ExecContext(ctx, `UPDATE oauth_codes SET used_at=?, grant_id=? WHERE hash=? AND used_at IS NULL AND expires_at>=?`, t, g.ID, codeHash, t)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var grant sql.NullString
		var used sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT used_at, grant_id FROM oauth_codes WHERE hash=?`, codeHash).Scan(&used, &grant); err != nil {
			return notFound(err)
		}
		if !used.Valid {
			return ErrOAuthExpired
		}
		if grant.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, t, grant.String); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
		return ErrOAuthUsed
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_grants(id,client_id,client_name,app_id,user_id,read_only,access,scope,resource,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.ClientID, g.ClientName, appRef(g.AppID), g.UserID, b2i(g.ReadOnly), g.Access, g.Scope, g.Resource, t); err != nil {
		return err
	}
	if err := s.insertPair(ctx, tx, g.ID, p); err != nil {
		return err
	}
	return tx.Commit()
}

const grantCols = `g.id, g.client_id, g.client_name, COALESCE(g.app_id,0), g.user_id, COALESCE(u.email,''), g.read_only, g.access, g.scope, g.resource, g.created_at, g.last_used_at, g.revoked_at`

type grantScan struct {
	g             OAuthGrant
	ro            int
	created       int64
	last, revoked sql.NullInt64
}

func (s *grantScan) dests() []any {
	g := &s.g
	return []any{&g.ID, &g.ClientID, &g.ClientName, &g.AppID, &g.UserID, &g.UserEmail, &s.ro, &g.Access, &g.Scope, &g.Resource, &s.created, &s.last, &s.revoked}
}

func (s *grantScan) finish() *OAuthGrant {
	g := &s.g
	g.ReadOnly = s.ro != 0
	g.CreatedAt = time.Unix(s.created, 0).UTC()
	g.LastUsedAt, g.RevokedAt = toTime(s.last), toTime(s.revoked)
	return g
}

// GetOAuthGrant returns one grant.
func (s *Store) GetOAuthGrant(ctx context.Context, id string) (*OAuthGrant, error) {
	var gs grantScan
	if err := s.DB.QueryRowContext(ctx, `SELECT `+grantCols+` FROM oauth_grants g LEFT JOIN users u ON u.id=g.user_id WHERE g.id=?`, id).
		Scan(gs.dests()...); err != nil {
		return nil, notFound(err)
	}
	return gs.finish(), nil
}

// ListOAuthGrants lists an app's connections, live ones first, newest first.
func (s *Store) ListOAuthGrants(ctx context.Context, appID int64) ([]*OAuthGrant, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+grantCols+` FROM oauth_grants g LEFT JOIN users u ON u.id=g.user_id
		WHERE g.app_id=? ORDER BY g.revoked_at IS NOT NULL, g.created_at DESC, g.id`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OAuthGrant{}
	for rows.Next() {
		var gs grantScan
		if err := rows.Scan(gs.dests()...); err != nil {
			return nil, err
		}
		out = append(out, gs.finish())
	}
	return out, rows.Err()
}

// ListControlGrants lists control MCP connections (grants with no app) with
// the given scope: one person's, or everyone's for userID 0. Live ones
// first, newest first.
func (s *Store) ListControlGrants(ctx context.Context, scope string, userID int64) ([]*OAuthGrant, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+grantCols+` FROM oauth_grants g LEFT JOIN users u ON u.id=g.user_id
		WHERE g.app_id IS NULL AND g.scope=? AND (?=0 OR g.user_id=?) ORDER BY g.revoked_at IS NOT NULL, g.created_at DESC, g.id`, scope, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OAuthGrant{}
	for rows.Next() {
		var gs grantScan
		if err := rows.Scan(gs.dests()...); err != nil {
			return nil, err
		}
		out = append(out, gs.finish())
	}
	return out, rows.Err()
}

// UserByOAuthToken finds the person a control MCP access token acts as,
// with the token's grant. Like AppByOAuthToken it returns expired tokens,
// revoked grants and grants that have an app; the caller decides. Nothing
// is cached, so a role change or a disabled account shows at once.
func (s *Store) UserByOAuthToken(ctx context.Context, hash string) (*User, *OAuthTokenInfo, error) {
	var ti OAuthTokenInfo
	var exp int64
	var spent sql.NullInt64
	var gs grantScan
	err := s.r.QueryRowContext(ctx, `SELECT t.id, t.kind, t.expires_at, t.spent_at, `+grantCols+`
		FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id LEFT JOIN users u ON u.id=g.user_id
		WHERE t.hash=? AND t.kind=?`, hash, OAuthAccess).Scan(append([]any{&ti.ID, &ti.Kind, &exp, &spent}, gs.dests()...)...)
	if err != nil {
		return nil, nil, notFound(err)
	}
	ti.ExpiresAt = time.Unix(exp, 0).UTC()
	ti.Spent = spent.Valid
	ti.Grant = gs.finish()
	u, err := s.GetUser(ctx, ti.Grant.UserID)
	if err != nil {
		return nil, nil, err
	}
	return u, &ti, nil
}

// RevokeOAuthGrant revokes a grant and so every token in its family.
// Revoking twice keeps the first time. It reports whether this call
// revoked it.
func (s *Store) RevokeOAuthGrant(ctx context.Context, id string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, now(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// OAuthTokenInfo is a token looked up by hash with its grant.
type OAuthTokenInfo struct {
	ID        int64
	Kind      string
	ExpiresAt time.Time
	Spent     bool
	Grant     *OAuthGrant
}

// OAuthTokenByHash finds a token of either kind and its grant.
func (s *Store) OAuthTokenByHash(ctx context.Context, hash string) (*OAuthTokenInfo, error) {
	var ti OAuthTokenInfo
	var exp int64
	var spent sql.NullInt64
	var gs grantScan
	err := s.DB.QueryRowContext(ctx, `SELECT t.id, t.kind, t.expires_at, t.spent_at, `+grantCols+`
		FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id LEFT JOIN users u ON u.id=g.user_id WHERE t.hash=?`, hash).
		Scan(append([]any{&ti.ID, &ti.Kind, &exp, &spent}, gs.dests()...)...)
	if err != nil {
		return nil, notFound(err)
	}
	ti.ExpiresAt = time.Unix(exp, 0).UTC()
	ti.Spent = spent.Valid
	ti.Grant = gs.finish()
	return &ti, nil
}

// RotateOAuthRefresh spends a refresh token and issues the next pair in the
// same family. A token already spent returns ErrOAuthUsed and revokes the
// whole family: either the client or a thief replayed it, and the server
// cannot tell which.
func (s *Store) RotateOAuthRefresh(ctx context.Context, tokenID int64, grantID string, p OAuthTokenPair) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.ExecContext(ctx, `UPDATE oauth_tokens SET spent_at=? WHERE id=? AND kind=? AND spent_at IS NULL`, t, tokenID, OAuthRefresh)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, t, grantID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrOAuthUsed
	}
	var revoked sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT revoked_at FROM oauth_grants WHERE id=?`, grantID).Scan(&revoked); err != nil {
		return notFound(err)
	}
	if revoked.Valid {
		return ErrOAuthRevoked
	}
	// The access tokens issued before this refresh stop now: the client
	// asked for a new one, so the old one is done with.
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_tokens SET expires_at=MIN(expires_at,?) WHERE grant_id=? AND kind=?`, t, grantID, OAuthAccess); err != nil {
		return err
	}
	if err := s.insertPair(ctx, tx, grantID, p); err != nil {
		return err
	}
	return tx.Commit()
}

// AppByOAuthToken finds the app an access token acts as, with the token's
// grant. It returns expired tokens and revoked grants too; the caller
// decides. Nothing is cached.
func (s *Store) AppByOAuthToken(ctx context.Context, hash string) (*App, *OAuthTokenInfo, *User, error) {
	var ti OAuthTokenInfo
	var exp int64
	var spent sql.NullInt64
	var gs grantScan
	var as appScan
	var role string
	var disabled int
	dests := append([]any{&ti.ID, &ti.Kind, &exp, &spent}, gs.dests()...)
	dests = append(dests, &role, &disabled)
	dests = append(dests, as.dests()...)
	err := s.r.QueryRowContext(ctx, `SELECT t.id, t.kind, t.expires_at, t.spent_at, `+grantCols+`, u.role, u.disabled, `+appColsAs("a")+`
		FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id JOIN users u ON u.id=g.user_id JOIN apps a ON a.id=g.app_id
		WHERE t.hash=? AND t.kind=? AND a.deleted_at IS NULL`, hash, OAuthAccess).Scan(dests...)
	if err != nil {
		return nil, nil, nil, notFound(err)
	}
	ti.ExpiresAt = time.Unix(exp, 0).UTC()
	ti.Spent = spent.Valid
	ti.Grant = gs.finish()
	a := as.finish()
	a.TokenReadOnly = ti.Grant.ReadOnly
	u := &User{ID: ti.Grant.UserID, Email: ti.Grant.UserEmail, Role: Role(role), Disabled: disabled != 0}
	return a, &ti, u, nil
}

type grantKey struct {
	s  *Store
	id string
}

var grantTouched = struct {
	sync.Mutex
	at map[grantKey]time.Time
}{at: map[grantKey]time.Time{}}

// TouchOAuthGrant records that a grant (and its app) was just used, at most
// once per TokenTouchInterval per grant.
func (s *Store) TouchOAuthGrant(appID int64, grantID string) {
	t := time.Now()
	k := grantKey{s, grantID}
	grantTouched.Lock()
	if last, ok := grantTouched.at[k]; ok && t.Sub(last) < TokenTouchInterval {
		grantTouched.Unlock()
		return
	}
	grantTouched.at[k] = t
	grantTouched.Unlock()
	_ = s.gw.write(
		wstmt{`UPDATE oauth_grants SET last_used_at=? WHERE id=?`, []any{t.Unix(), grantID}},
		wstmt{`UPDATE apps SET last_used_at=? WHERE id=?`, []any{t.Unix(), appID}})
}

// CreateBareApp creates an app with no token, for a connection approved on
// the consent page: the OAuth grant is its credential. apps.token_hash gets
// a placeholder that can never match a token's hash.
func (s *Store) CreateBareApp(ctx context.Context, a *App, placeholder string) (*App, error) {
	allowed, _ := json.Marshal(nonNil(a.AllowedServices))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id, err := nextID(ctx, tx, appsHighWater, "apps_seq")
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO apps(id,name,description,token_hash,token_prefix,allowed_services,mode,active,created_at,lease_hidden) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, a.Name, a.Description, "oauth:"+placeholder, "", string(allowed), a.Mode, b2i(a.Active), now(), 0); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetApp(ctx, id)
}
