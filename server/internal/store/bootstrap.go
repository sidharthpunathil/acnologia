package store

import (
	"context"
	"errors"
	"strings"
)

// BootstrapTokenName is the name the token from ACNOLOGIA_BOOTSTRAP_TOKEN
// gets in the token list.
const BootstrapTokenName = "bootstrap"

// minBootstrapSecret is the least number of characters after the prefix a
// bootstrap token must have: as hard to guess as a generated one.
const minBootstrapSecret = 32

// CheckBootstrapToken says whether tok can serve as an admin API token: the
// acnadm_ prefix, at least 32 more characters, and no spaces. The error
// never repeats the token.
func CheckBootstrapToken(tok string) error {
	rest, ok := strings.CutPrefix(tok, AdminTokenPrefix)
	switch {
	case !ok:
		return errors.New("ACNOLOGIA_BOOTSTRAP_TOKEN must start with " + AdminTokenPrefix)
	case len(rest) < minBootstrapSecret:
		return errors.New("ACNOLOGIA_BOOTSTRAP_TOKEN needs at least 32 characters after " + AdminTokenPrefix + "; make one with: echo acnadm_$(openssl rand -hex 24)")
	case strings.ContainsAny(tok, " \t\r\n"):
		return errors.New("ACNOLOGIA_BOOTSTRAP_TOKEN must not contain spaces")
	}
	return nil
}

// AddBootstrapToken stores the hash of a token set in the environment as
// an admin scope API token of u, the first superadmin. Only the hash and
// the prefix are kept, like a token made in the dashboard.
func (s *Store) AddBootstrapToken(ctx context.Context, u *User, hash, tok string) (*AdminToken, error) {
	if err := CheckBootstrapToken(tok); err != nil {
		return nil, err
	}
	t, err := s.CreateToken(ctx, &AdminToken{Name: BootstrapTokenName, Scope: ScopeAdmin, UserID: u.ID, TokenPrefix: tok[:12]}, hash)
	if err != nil {
		return nil, err
	}
	s.AddAudit(ctx, u, "token.create", t.Name, "scope=admin, from ACNOLOGIA_BOOTSTRAP_TOKEN")
	return t, nil
}
