package store

import (
	"context"
	"testing"
	"time"
)

// A database from before control MCP connections has app_id NOT NULL on
// oauth_codes and oauth_grants and no access column. The upgrade rebuilds
// both tables and keeps every grant and token: a connection made before it
// keeps working, and a grant with no app can be stored after it.
func TestMigrateOAuthControlKeepsConnections(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE oauth_tokens`, `DROP TABLE oauth_grants`, `DROP TABLE oauth_codes`,
		`CREATE TABLE oauth_codes(hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL,
			resource TEXT NOT NULL, scope TEXT NOT NULL DEFAULT '', app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, read_only INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER, grant_id TEXT)`,
		`CREATE TABLE oauth_grants(id TEXT PRIMARY KEY, client_id TEXT NOT NULL, client_name TEXT NOT NULL DEFAULT '',
			app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			read_only INTEGER NOT NULL DEFAULT 0, scope TEXT NOT NULL DEFAULT '', resource TEXT NOT NULL, created_at INTEGER NOT NULL,
			last_used_at INTEGER, revoked_at INTEGER)`,
		`CREATE TABLE oauth_tokens(id INTEGER PRIMARY KEY, hash TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
			grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, spent_at INTEGER)`,
		`INSERT INTO users(id,email,name,password_hash,role,created_at) VALUES(5,'a@x.io','','h','admin',1)`,
		`INSERT INTO apps(id,name,token_hash,token_prefix,created_at) VALUES(3,'claude','h','p',1)`,
		`INSERT INTO oauth_grants(id,client_id,client_name,app_id,user_id,scope,resource,created_at) VALUES('g1','c1','Claude',3,5,'mcp','https://x/mcp',1)`,
		`INSERT INTO oauth_tokens(hash,kind,grant_id,created_at,expires_at) VALUES('ha','access','g1',1,4102444800)`,
	} {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ctx := context.Background()
	a, ti, _, err := s2.AppByOAuthToken(ctx, "ha")
	if err != nil || a.ID != 3 || ti.Grant.ID != "g1" || ti.Grant.Access != "" {
		t.Fatalf("old connection after the upgrade: %v %+v %v", a, ti, err)
	}
	var strict int
	if err := s2.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('oauth_codes') WHERE name='app_id' AND "notnull"=1`).Scan(&strict); err != nil || strict != 0 {
		t.Fatalf("oauth_codes.app_id still NOT NULL: %d %v", strict, err)
	}
	g := &OAuthGrant{ID: "g2", ClientID: "c1", ClientName: "Claude", UserID: 5, Access: "write", Scope: "mcp:control", Resource: "https://x/mcp/control"}
	if err := s2.CreateOAuthCode(ctx, &OAuthCode{Hash: "code", ClientID: "c1", RedirectURI: "r", CodeChallenge: "c", Resource: g.Resource,
		Scope: g.Scope, UserID: 5, Access: "write", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	p := OAuthTokenPair{AccessHash: "ha2", RefreshHash: "hr2", AccessExpiry: time.Now().Add(time.Hour), RefreshExpiry: time.Now().Add(time.Hour)}
	if err := s2.RedeemOAuthCode(ctx, "code", g, p); err != nil {
		t.Fatal(err)
	}
	u, ti, err := s2.UserByOAuthToken(ctx, "ha2")
	if err != nil || u.ID != 5 || ti.Grant.AppID != 0 || ti.Grant.Access != "write" {
		t.Fatalf("control grant: %+v %+v %v", u, ti, err)
	}
	list, err := s2.ListControlGrants(ctx, "mcp:control", 5)
	if err != nil || len(list) != 1 || list[0].ID != "g2" {
		t.Fatalf("control grants: %+v %v", list, err)
	}
	// Deleting the person still takes their connections and tokens with them.
	if _, err := s2.DB.Exec(`DELETE FROM users WHERE id=5`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s2.DB.QueryRow(`SELECT COUNT(*) FROM oauth_tokens`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tokens left after the user went: %d %v", n, err)
	}
}
