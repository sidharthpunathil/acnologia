package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// Provider specs a receiver imported from a peer. A row exists for every
// provider imported through a link: the ones the owner shared on purpose
// (kind shared) and the ones pulled in because a shared service needs them
// (kind dependency). The owner's version at import and the newest one seen
// let the dashboard say an update is available; pulling it is always a
// superadmin's explicit action.

// Kinds of imported peer provider.
const (
	PeerProviderShared     = "shared"
	PeerProviderDependency = "dependency"
)

// PeerProvider is one provider spec imported from a peer link.
type PeerProvider struct {
	LinkID   int64  `json:"link_id"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	// ForServices lists the owner's service slugs that needed it (dependency).
	ForServices []string `json:"for_services"`
	// ImportedVersion is the owner's spec version this server imported.
	ImportedVersion int `json:"imported_version"`
	// RemoteVersion is the newest owner version seen in a manifest.
	RemoteVersion int `json:"remote_version"`
	// LocalVersion is this server's version of the provider right after the
	// import; a newer local version means someone here edited it.
	LocalVersion int       `json:"local_version"`
	ImportedAt   time.Time `json:"imported_at"`
}

const schemaPeerProviders = `
CREATE TABLE IF NOT EXISTS peer_link_providers(
  link_id INTEGER NOT NULL REFERENCES peer_links(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  kind TEXT NOT NULL,
  for_services TEXT NOT NULL DEFAULT '[]',
  imported_version INTEGER NOT NULL DEFAULT 0,
  remote_version INTEGER NOT NULL DEFAULT 0,
  local_version INTEGER NOT NULL DEFAULT 0,
  imported_at INTEGER NOT NULL,
  PRIMARY KEY(link_id, provider)
);
`

func migratePeerProviders(db *sql.DB) error {
	if _, err := db.Exec(schemaPeerProviders); err != nil {
		return err
	}
	// The owner's provider slug for a service made for a peer share, used to
	// show that provider's name, logo and docs. The service's own provider
	// stays acnologia-peer, which classifies the share token's answers.
	_, err := addColumn(db, "services", "peer_provider", "TEXT NOT NULL DEFAULT ''")
	return err
}

// SetServicePeerProvider records the owner's provider slug for a peer service.
func (s *Store) SetServicePeerProvider(ctx context.Context, serviceID int64, provider string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET peer_provider=? WHERE id=?`, provider, serviceID)
	return err
}

const peerProviderCols = `link_id, provider, kind, for_services, imported_version, remote_version, local_version, imported_at`

func scanPeerProvider(row interface{ Scan(...any) error }) (*PeerProvider, error) {
	var p PeerProvider
	var svcs string
	var at int64
	if err := row.Scan(&p.LinkID, &p.Provider, &p.Kind, &svcs, &p.ImportedVersion, &p.RemoteVersion, &p.LocalVersion, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.ForServices = []string{}
	_ = json.Unmarshal([]byte(svcs), &p.ForServices)
	p.ImportedAt = time.Unix(at, 0).UTC()
	return &p, nil
}

// SavePeerProvider inserts or replaces an imported provider's row.
func (s *Store) SavePeerProvider(ctx context.Context, p *PeerProvider) error {
	svcs := nonNil(p.ForServices)
	sort.Strings(svcs)
	b, _ := json.Marshal(svcs)
	at := p.ImportedAt
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO peer_link_providers(`+peerProviderCols+`) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(link_id, provider) DO UPDATE SET kind=excluded.kind, for_services=excluded.for_services,
		imported_version=excluded.imported_version, remote_version=excluded.remote_version, local_version=excluded.local_version`,
		p.LinkID, p.Provider, p.Kind, string(b), p.ImportedVersion, p.RemoteVersion, p.LocalVersion, at.Unix())
	return err
}

// GetPeerProvider returns one imported provider's row.
func (s *Store) GetPeerProvider(ctx context.Context, linkID int64, provider string) (*PeerProvider, error) {
	return scanPeerProvider(s.DB.QueryRowContext(ctx, `SELECT `+peerProviderCols+` FROM peer_link_providers WHERE link_id=? AND provider=?`, linkID, provider))
}

// ListPeerProviders returns the providers imported through a link.
func (s *Store) ListPeerProviders(ctx context.Context, linkID int64) ([]*PeerProvider, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+peerProviderCols+` FROM peer_link_providers WHERE link_id=? ORDER BY provider`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PeerProvider{}
	for rows.Next() {
		p, err := scanPeerProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
