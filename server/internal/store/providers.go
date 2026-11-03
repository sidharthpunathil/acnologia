package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Provider struct {
	Slug          string          `json:"slug"`
	Name          string          `json:"name"`
	Category      string          `json:"category"`
	Description   string          `json:"description"`
	Website       string          `json:"website"`
	DocsURL       string          `json:"docs_url"`
	Color         string          `json:"color"`
	Status        string          `json:"status"`
	Builtin       bool            `json:"builtin"`
	Source        string          `json:"source"`
	Version       int             `json:"version"`
	Spec          json.RawMessage `json:"spec"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	LogoURL       *string         `json:"logo_url"`
	ServicesCount int             `json:"services_count"`
	// KeysCount counts visible keys across every service built on this provider.
	KeysCount int `json:"keys_count"`

	HasLogo bool `json:"-"`
	LogoRev int  `json:"-"`
	// VersionSource, when set, is the source recorded on the new version
	// instead of Source, so a change can be labelled (refresh) without
	// changing where the provider came from.
	VersionSource string `json:"-"`
}

type ProviderVersion struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Note      string    `json:"note"`
	Source    string    `json:"source"`
}

const providerCols = `p.slug, p.name, p.category, p.description, p.website, p.docs_url, p.color, p.status, p.builtin, p.source,
p.version, p.spec, p.created_at, p.updated_at, p.logo IS NOT NULL, p.logo_rev,
(SELECT COUNT(*) FROM services s WHERE s.provider=p.slug),
(SELECT COUNT(*) FROM keys k JOIN services s ON s.id=k.service_id WHERE s.provider=p.slug AND k.hidden=0)`

func scanProvider(row interface{ Scan(...any) error }) (*Provider, error) {
	var p Provider
	var builtin int
	var spec string
	var c, u int64
	err := row.Scan(&p.Slug, &p.Name, &p.Category, &p.Description, &p.Website, &p.DocsURL, &p.Color, &p.Status, &builtin,
		&p.Source, &p.Version, &spec, &c, &u, &p.HasLogo, &p.LogoRev, &p.ServicesCount, &p.KeysCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Builtin = builtin == 1
	p.Spec = json.RawMessage(spec)
	p.CreatedAt, p.UpdatedAt = time.Unix(c, 0).UTC(), time.Unix(u, 0).UTC()
	return &p, nil
}

func (s *Store) ListProviders(ctx context.Context) ([]*Provider, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+providerCols+` FROM providers p ORDER BY p.builtin DESC, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProvider(ctx context.Context, slug string) (*Provider, error) {
	return scanProvider(s.DB.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers p WHERE p.slug=?`, slug))
}

// snapshot is what a version stores: metadata plus spec.
func snapshot(p *Provider) string {
	b, _ := json.Marshal(map[string]any{
		"name": p.Name, "category": p.Category, "description": p.Description, "website": p.Website,
		"docs_url": p.DocsURL, "color": p.Color, "status": p.Status, "spec": p.Spec,
	})
	return string(b)
}

// SaveProvider inserts a new provider or writes a new version of an existing one.
func (s *Store) SaveProvider(ctx context.Context, p *Provider, by, note string) (*Provider, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var cur int
	err = tx.QueryRowContext(ctx, `SELECT version FROM providers WHERE slug=?`, p.Slug).Scan(&cur)
	t := now()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		p.Version = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO providers(slug,name,category,description,website,docs_url,color,status,builtin,source,version,spec,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.Slug, p.Name, p.Category, p.Description, p.Website, p.DocsURL, p.Color, p.Status,
			b2i(p.Builtin), p.Source, 1, string(p.Spec), t, t)
	case err == nil:
		p.Version = cur + 1
		_, err = tx.ExecContext(ctx, `UPDATE providers SET name=?,category=?,description=?,website=?,docs_url=?,color=?,status=?,source=?,version=?,spec=?,updated_at=? WHERE slug=?`,
			p.Name, p.Category, p.Description, p.Website, p.DocsURL, p.Color, p.Status, p.Source, p.Version, string(p.Spec), t, p.Slug)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_versions(slug,version,snapshot,source,note,created_by,created_at) VALUES(?,?,?,?,?,?,?)`,
		p.Slug, p.Version, snapshot(p), cmp.Or(p.VersionSource, p.Source), note, by, t); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetProvider(ctx, p.Slug)
}

func (s *Store) ProviderVersions(ctx context.Context, slug string) ([]ProviderVersion, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT version, created_at, created_by, note, source FROM provider_versions WHERE slug=? ORDER BY version DESC LIMIT 100`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderVersion{}
	for rows.Next() {
		var v ProviderVersion
		var c int64
		if err := rows.Scan(&v.Version, &c, &v.CreatedBy, &v.Note, &v.Source); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(c, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// ProviderVersionSnapshot returns a stored version's metadata and spec.
func (s *Store) ProviderVersionSnapshot(ctx context.Context, slug string, version int) (*Provider, error) {
	var snap string
	err := s.DB.QueryRowContext(ctx, `SELECT snapshot FROM provider_versions WHERE slug=? AND version=?`, slug, version).Scan(&snap)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var p Provider
	if err := json.Unmarshal([]byte(snap), &p); err != nil {
		return nil, err
	}
	p.Slug = slug
	return &p, nil
}

func (s *Store) DeleteProvider(ctx context.Context, slug string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM providers WHERE slug=?`, slug)
	return err
}

func (s *Store) SetProviderLogo(ctx context.Context, slug string, data []byte, mime string) error {
	var d any
	if data != nil {
		d = data
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE providers SET logo=?, logo_mime=?, logo_rev=logo_rev+1 WHERE slug=?`, d, mime, slug)
	return err
}

func (s *Store) ProviderLogo(ctx context.Context, slug string) ([]byte, string, error) {
	var data []byte
	var mime string
	err := s.DB.QueryRowContext(ctx, `SELECT logo, logo_mime FROM providers WHERE slug=? AND logo IS NOT NULL`, slug).Scan(&data, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return data, mime, err
}

// ProviderLogoRevs maps provider slug to its logo revision for those with a logo.
func (s *Store) ProviderLogoRevs(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT slug, logo_rev FROM providers WHERE logo IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var slug string
		var rev int
		if err := rows.Scan(&slug, &rev); err != nil {
			return nil, err
		}
		out[slug] = rev
	}
	return out, rows.Err()
}
