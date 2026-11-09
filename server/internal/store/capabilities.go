package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Capability is one thing an app wants done, such as "search Google Maps",
// that several providers can do with requests of their own shape. Apps call
// it at /proxy/{slug} with the capability's own params; each route says how
// one service does it. Acnologia picks a route by Strategy, translates the
// params into that service's request, and moves to the next route when a
// provider is out of keys, quota or working order. Within a route, the
// service's own pool rotates keys as for any proxied request.
type Capability struct {
	ID          int64             `json:"id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Strategy    string            `json:"strategy"`
	Params      []CapabilityParam `json:"params"`
	Routes      []CapabilityRoute `json:"routes"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// CapabilityParam is one input the capability takes.
type CapabilityParam struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // string, integer, number, boolean
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// CapabilityRoute is how one service does the capability. Query values,
// Path and Body strings may hold {{param}} placeholders.
type CapabilityRoute struct {
	Service string            `json:"service"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Body    any               `json:"body,omitempty"`
	// Weight is the route's share under the weighted strategy (default 1).
	Weight   int  `json:"weight,omitempty"`
	Disabled bool `json:"disabled,omitempty"`
	// Output maps the provider's answer to the capability's own fields.
	// Without it the provider's body is returned as is.
	Output *CapabilityOutput `json:"output,omitempty"`
	Note   string            `json:"note,omitempty"`
}

// CapabilityOutput maps a JSON response. Items is the dot path of the list
// of results ("" for a single object at the root); Fields maps each output
// field to a dot path inside one item.
type CapabilityOutput struct {
	Items  string            `json:"items,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

const capCols = `id, slug, name, description, strategy, params, routes, created_at, updated_at`

func scanCapability(row interface{ Scan(...any) error }) (*Capability, error) {
	var c Capability
	var params, routes string
	var created, updated int64
	if err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Strategy, &params, &routes, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Params, c.Routes = []CapabilityParam{}, []CapabilityRoute{}
	_ = json.Unmarshal([]byte(params), &c.Params)
	_ = json.Unmarshal([]byte(routes), &c.Routes)
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return &c, nil
}

func (s *Store) ListCapabilities(ctx context.Context) ([]*Capability, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+capCols+` FROM capabilities ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Capability{}
	for rows.Next() {
		c, err := scanCapability(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCapability(ctx context.Context, id int64) (*Capability, error) {
	return scanCapability(s.r.QueryRowContext(ctx, `SELECT `+capCols+` FROM capabilities WHERE id=?`, id))
}

func (s *Store) GetCapabilityBySlug(ctx context.Context, slug string) (*Capability, error) {
	return scanCapability(s.r.QueryRowContext(ctx, `SELECT `+capCols+` FROM capabilities WHERE slug=?`, slug))
}

func capJSON(c *Capability) (string, string) {
	p, _ := json.Marshal(nonNilSlice(c.Params))
	r, _ := json.Marshal(nonNilSlice(c.Routes))
	return string(p), string(r)
}

func nonNilSlice[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (s *Store) CreateCapability(ctx context.Context, c *Capability) (*Capability, error) {
	params, routes := capJSON(c)
	t := now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO capabilities(slug,name,description,strategy,params,routes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		c.Slug, c.Name, c.Description, orStrategy(c.Strategy), params, routes, t, t)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCapability(ctx, id)
}

func (s *Store) UpdateCapability(ctx context.Context, c *Capability) error {
	params, routes := capJSON(c)
	_, err := s.DB.ExecContext(ctx, `UPDATE capabilities SET name=?, description=?, strategy=?, params=?, routes=?, updated_at=? WHERE id=?`,
		c.Name, c.Description, orStrategy(c.Strategy), params, routes, now(), c.ID)
	return err
}

func (s *Store) DeleteCapability(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM capabilities WHERE id=?`, id)
	return err
}

// AvailableKeyCounts counts the keys of each service that could take a
// request now: active, not cooling down, with quota left. Hidden keys are
// left out. Routing uses it to try a provider with keys before one without.
func (s *Store) AvailableKeyCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT service_id, COUNT(*) FROM keys
		WHERE hidden=0 AND status=? AND (cooldown_until IS NULL OR cooldown_until<=?) AND (quota_remaining IS NULL OR quota_remaining>0)
		GROUP BY service_id`, StatusActive, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
