package store

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// Deny rules block an app's requests that match them, whatever its access
// allows otherwise. A rule names a method (or *), a target (a service slug,
// a host, or neither for any) and a path prefix. Paths are compared after
// decoding and cleaning (decodePath), so %2F, double slashes, dot segments
// and backslashes cannot step around a rule.

const schemaDeny = `
CREATE TABLE IF NOT EXISTS app_deny_rules(
  id INTEGER PRIMARY KEY,
  app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  method TEXT NOT NULL DEFAULT '*',
  service TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL DEFAULT '',
  path_prefix TEXT NOT NULL DEFAULT '/',
  note TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS app_deny_rules_app ON app_deny_rules(app_id);
`

// MaxDenyRules is the most rules an app may hold.
const MaxDenyRules = 100

// ErrTooManyDenyRules refuses a rule past MaxDenyRules.
var ErrTooManyDenyRules = errors.New("an app holds at most 100 deny rules")

// DenyRule blocks matching requests with 403 denied_by_rule.
type DenyRule struct {
	ID    int64 `json:"id"`
	AppID int64 `json:"app_id"`
	// Method is an HTTP method in capitals, or * for any.
	Method string `json:"method"`
	// Service is a service, group or capability slug; Host a hostname. At
	// most one is set; with neither the rule covers every target.
	Service string `json:"service"`
	Host    string `json:"host"`
	// PathPrefix is a cleaned path. It matches itself and everything under
	// it on whole segments; a trailing * makes it a plain string prefix.
	PathPrefix string    `json:"path_prefix"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}

// Describe is the rule in one line: "POST openai /v1/files".
func (r *DenyRule) Describe() string {
	target := "any target"
	if r.Service != "" {
		target = r.Service
	} else if r.Host != "" {
		target = r.Host
	}
	return r.Method + " " + target + " " + r.PathPrefix
}

var denyMethods = map[string]bool{"*": true, "GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "OPTIONS": true, "CONNECT": true}

// Normalize checks a rule and puts it in the form matching expects: method
// in capitals, host lowercased, path decoded and cleaned.
func (r *DenyRule) Normalize() error {
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method == "" {
		r.Method = "*"
	}
	if !denyMethods[r.Method] {
		return errors.New("method must be *, GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS or CONNECT")
	}
	r.Service = strings.TrimSpace(r.Service)
	r.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Host), "."))
	if r.Service != "" && r.Host != "" {
		return errors.New("a rule names a service or a host, not both")
	}
	if len(r.Service) > 100 || strings.ContainsAny(r.Service, "/ ") {
		return errors.New("service must be a slug")
	}
	if len(r.Host) > 253 || strings.ContainsAny(r.Host, "/ :@") {
		return errors.New("host must be a bare hostname, like api.openai.com")
	}
	p := strings.TrimSpace(r.PathPrefix)
	star := strings.HasSuffix(p, "*")
	p = strings.TrimSuffix(p, "*")
	if strings.Contains(p, "*") {
		return errors.New("path_prefix may end with * and have no other")
	}
	if len(p) > 500 {
		return errors.New("path_prefix is too long")
	}
	clean := DecodePath(p)
	if star && strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	if star && clean != "/" {
		clean += "*"
	}
	r.PathPrefix = clean
	if len(r.Note) > 200 {
		return errors.New("note must be at most 200 characters")
	}
	return nil
}

// DecodePath is the form deny rules compare paths in: percent-decoded until
// it stops changing (so %252F is caught too), backslashes read as slashes,
// cleaned of dot segments and repeated slashes, and lowercased.
func DecodePath(p string) string {
	for i := 0; i < 4; i++ {
		d, err := url.PathUnescape(p)
		if err != nil || d == p {
			break
		}
		p = d
	}
	p = strings.ReplaceAll(p, "\\", "/")
	// A NUL ends the path for some servers; match on what comes before.
	if i := strings.IndexByte(p, 0); i >= 0 {
		p = p[:i]
	}
	return strings.ToLower(path.Clean("/" + p))
}

// pathForms are the forms of a path a rule is tried on: DecodePath's, and
// the same with ;parameters cut from each segment, which some servers drop.
func pathForms(p string) []string {
	d := DecodePath(p)
	if !strings.Contains(d, ";") {
		return []string{d}
	}
	segs := strings.Split(d, "/")
	for i, sg := range segs {
		segs[i], _, _ = strings.Cut(sg, ";")
	}
	return []string{d, path.Clean("/" + strings.Join(segs, "/"))}
}

func prefixMatches(prefix, p string) bool {
	if prefix == "/" || prefix == "" || prefix == "*" {
		return true
	}
	if raw, ok := strings.CutSuffix(prefix, "*"); ok {
		return strings.HasPrefix(p, raw)
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// DenyTarget is one request as deny rules see it.
type DenyTarget struct {
	Method string
	// Slugs are the names the request reaches: the slug asked for and the
	// service serving it.
	Slugs []string
	// Host is the upstream hostname, HostPath the full path on it.
	Host     string
	HostPath string
	// Path is the path below the service (after /proxy/{slug}).
	Path string
}

// MatchDeny returns the first rule that matches t, or nil.
func MatchDeny(rules []DenyRule, t DenyTarget) *DenyRule {
	if len(rules) == 0 {
		return nil
	}
	method := strings.ToUpper(t.Method)
	host := strings.ToLower(strings.TrimSuffix(t.Host, "."))
	svcPaths := pathForms(t.Path)
	// A first segment of ~name picks one of the provider's hosts; the
	// endpoint path is what follows it.
	if seg := strings.TrimPrefix(svcPaths[0], "/"); strings.HasPrefix(seg, "~") {
		_, rest, _ := strings.Cut(seg, "/")
		svcPaths = append(svcPaths, pathForms(rest)...)
	}
	hostPaths := pathForms(t.HostPath)
	for i := range rules {
		r := &rules[i]
		if r.Method != "*" && r.Method != method {
			continue
		}
		var paths []string
		switch {
		case r.Service != "":
			hit := false
			for _, s := range t.Slugs {
				if strings.EqualFold(s, r.Service) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			paths = svcPaths
		case r.Host != "":
			if host == "" || r.Host != host {
				continue
			}
			paths = hostPaths
		default:
			paths = append(append([]string(nil), svcPaths...), hostPaths...)
		}
		for _, p := range paths {
			if prefixMatches(r.PathPrefix, p) {
				return r
			}
		}
	}
	return nil
}

// Deny rules are read on every proxied request, so they are cached per app
// and the cache is dropped whenever they change.
type denyKey struct {
	s     *Store
	appID int64
}

type denyEntry struct {
	rules []DenyRule
	at    time.Time
}

var denyCache sync.Map // denyKey -> denyEntry

const denyTTL = 30 * time.Second

func (s *Store) dropDenyCache(appID int64) { denyCache.Delete(denyKey{s, appID}) }

// DenyRules lists an app's rules, oldest first.
func (s *Store) DenyRules(ctx context.Context, appID int64) ([]DenyRule, error) {
	k := denyKey{s, appID}
	if v, ok := denyCache.Load(k); ok {
		if e := v.(denyEntry); time.Since(e.at) < denyTTL {
			return e.rules, nil
		}
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, app_id, method, service, host, path_prefix, note, created_at FROM app_deny_rules WHERE app_id=? ORDER BY id`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DenyRule{}
	for rows.Next() {
		var r DenyRule
		var created int64
		if err := rows.Scan(&r.ID, &r.AppID, &r.Method, &r.Service, &r.Host, &r.PathPrefix, &r.Note, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	denyCache.Store(k, denyEntry{rules: out, at: time.Now()})
	return out, nil
}

// AddDenyRule stores a rule (Normalize it first) and returns it.
func (s *Store) AddDenyRule(ctx context.Context, r DenyRule) (*DenyRule, error) {
	defer s.dropDenyCache(r.AppID)
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_deny_rules WHERE app_id=?`, r.AppID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxDenyRules {
		return nil, ErrTooManyDenyRules
	}
	t := now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO app_deny_rules(app_id, method, service, host, path_prefix, note, created_at) VALUES(?,?,?,?,?,?,?)`,
		r.AppID, r.Method, r.Service, r.Host, r.PathPrefix, r.Note, t)
	if err != nil {
		return nil, err
	}
	r.ID, _ = res.LastInsertId()
	r.CreatedAt = time.Unix(t, 0).UTC()
	return &r, nil
}

// DeleteDenyRule removes one of an app's rules.
func (s *Store) DeleteDenyRule(ctx context.Context, appID, id int64) error {
	defer s.dropDenyCache(appID)
	return execOne(s.DB.ExecContext(ctx, `DELETE FROM app_deny_rules WHERE app_id=? AND id=?`, appID, id))
}
