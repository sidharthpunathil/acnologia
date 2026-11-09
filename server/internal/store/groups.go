package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ServiceGroup puts several services behind one slug. A proxied request goes
// to the first member that can serve it and falls through to the next when a
// member has no usable key, is down, or runs out of keys to rotate through.
// Members should share a request shape, for example two accounts of one API.
type ServiceGroup struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Members     []string  `json:"members"` // service slugs, in fallback order
	// Strategy picks which member goes first: priority (the order above),
	// round_robin, random, cheapest or most_keys. The rest follow as fallbacks.
	Strategy  string    `json:"strategy"`
	CreatedAt time.Time `json:"created_at"`
}

const groupCols = `id, slug, name, description, members, strategy, created_at`

func scanGroup(row interface{ Scan(...any) error }) (*ServiceGroup, error) {
	var g ServiceGroup
	var members string
	var created int64
	if err := row.Scan(&g.ID, &g.Slug, &g.Name, &g.Description, &members, &g.Strategy, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	g.Members = []string{}
	_ = json.Unmarshal([]byte(members), &g.Members)
	g.CreatedAt = time.Unix(created, 0).UTC()
	return &g, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]*ServiceGroup, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+groupCols+` FROM service_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ServiceGroup{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GetGroup(ctx context.Context, id int64) (*ServiceGroup, error) {
	return scanGroup(s.r.QueryRowContext(ctx, `SELECT `+groupCols+` FROM service_groups WHERE id=?`, id))
}

func (s *Store) GetGroupBySlug(ctx context.Context, slug string) (*ServiceGroup, error) {
	return scanGroup(s.r.QueryRowContext(ctx, `SELECT `+groupCols+` FROM service_groups WHERE slug=?`, slug))
}

func (s *Store) CreateGroup(ctx context.Context, g *ServiceGroup) (*ServiceGroup, error) {
	members, _ := json.Marshal(nonNil(g.Members))
	res, err := s.DB.ExecContext(ctx, `INSERT INTO service_groups(slug,name,description,members,strategy,created_at) VALUES(?,?,?,?,?,?)`,
		g.Slug, g.Name, g.Description, string(members), orStrategy(g.Strategy), now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetGroup(ctx, id)
}

func (s *Store) UpdateGroup(ctx context.Context, g *ServiceGroup) error {
	members, _ := json.Marshal(nonNil(g.Members))
	_, err := s.DB.ExecContext(ctx, `UPDATE service_groups SET name=?, description=?, members=?, strategy=? WHERE id=?`,
		g.Name, g.Description, string(members), orStrategy(g.Strategy), g.ID)
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM service_groups WHERE id=?`, id)
	return err
}

// SlugTaken reports whether a slug is used by a service, a group or a
// capability, since all three share the /proxy/{slug} namespace.
func (s *Store) SlugTaken(ctx context.Context, slug string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM services WHERE slug=?) + (SELECT COUNT(*) FROM service_groups WHERE slug=?)
		+ (SELECT COUNT(*) FROM capabilities WHERE slug=?)`, slug, slug, slug).Scan(&n)
	return n > 0, err
}

func orStrategy(s string) string {
	if s == "" {
		return "priority"
	}
	return s
}
