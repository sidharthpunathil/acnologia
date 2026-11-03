package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// Key scopes limit which keys of a shared service a share may use. A share
// without an entry for a service may use every key the share app could use
// anyway (restricted and hidden keys still apply).

// Key scope modes.
const (
	ScopeAll   = "all"
	ScopeCount = "count"
	ScopePick  = "pick"
)

// KeyScope is one shared service's key scope.
type KeyScope struct {
	Mode string `json:"mode"`
	// Count is the most keys the share may use, in count mode.
	Count int `json:"count,omitempty"`
	// KeyIDs are the only keys the share may use, in pick mode.
	KeyIDs []int64 `json:"key_ids,omitempty"`
}

// IsAll reports whether the scope allows every key.
func (sc *KeyScope) IsAll() bool { return sc == nil || sc.Mode == "" || sc.Mode == ScopeAll }

func migrateKeyScopes(db *sql.DB) error {
	_, err := addColumn(db, "shares", "key_scopes", "TEXT NOT NULL DEFAULT '{}'")
	return err
}

func encodeScopes(m map[string]KeyScope) string {
	clean := map[string]KeyScope{}
	for slug, sc := range m {
		if !sc.IsAll() {
			clean[slug] = sc
		}
	}
	b, _ := json.Marshal(clean)
	return string(b)
}

func decodeScopes(s string) map[string]KeyScope {
	out := map[string]KeyScope{}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// AllowsApp reports whether a restricted key lets the app use it.
func (k *Key) AllowsApp(appID int64) bool {
	if !k.Restricted {
		return true
	}
	for _, a := range k.AppIDs {
		if a == appID {
			return true
		}
	}
	return false
}

// InRotation reports whether a key can still serve at some point: it is not
// invalid, banned or disabled. Keys cooling down or out of quota still are.
func (k *Key) InRotation() bool {
	return k.Status != StatusInvalid && k.Status != StatusBanned && k.Status != StatusDisabled
}

// ScopeResult is what a key scope resolves to for one service.
type ScopeResult struct {
	// IDs are the keys the share may use.
	IDs map[int64]bool
	// Total counts the keys the share could use without a scope.
	Total int
}

// ResolveScope works out which of a service's keys a share app may use.
// keys are all the service's keys. Only keys the app may use at all count:
// restricted keys must list the app and hidden keys need includeHidden.
// Count mode takes the first N keys still in rotation by priority then id,
// so a key that leaves rotation is replaced by the next one.
func ResolveScope(sc *KeyScope, keys []*Key, appID int64, includeHidden bool) ScopeResult {
	usable := usableKeys(keys, appID, includeHidden)
	res := ScopeResult{IDs: map[int64]bool{}, Total: len(usable)}
	switch {
	case sc.IsAll():
		for _, k := range usable {
			res.IDs[k.ID] = true
		}
	case sc.Mode == ScopePick:
		want := map[int64]bool{}
		for _, id := range sc.KeyIDs {
			want[id] = true
		}
		for _, k := range usable {
			if want[k.ID] {
				res.IDs[k.ID] = true
			}
		}
	case sc.Mode == ScopeCount:
		in := rotationOrder(usable)
		for i := 0; i < len(in) && i < sc.Count; i++ {
			res.IDs[in[i].ID] = true
		}
	}
	return res
}

// ResolveHandoverScope is ResolveScope for keys that leave this server. A
// handed over key cannot be taken back, so in count mode a key that leaves
// rotation does not free its place: the share keeps up to N of the keys it
// was ever given (given), and gets a new key only while fewer than N of
// those still exist. Deleting a key frees its place.
func ResolveHandoverScope(sc *KeyScope, keys []*Key, appID int64, includeHidden bool, given map[int64]bool) ScopeResult {
	if sc.IsAll() || sc.Mode != ScopeCount {
		return ResolveScope(sc, keys, appID, includeHidden)
	}
	usable := usableKeys(keys, appID, includeHidden)
	res := ScopeResult{IDs: map[int64]bool{}, Total: len(usable)}
	held := 0
	for _, k := range keys {
		if given[k.ID] {
			held++
		}
	}
	in := rotationOrder(usable)
	for _, k := range in {
		if given[k.ID] && len(res.IDs) < sc.Count {
			res.IDs[k.ID] = true
		}
	}
	for _, k := range in {
		if held >= sc.Count || len(res.IDs) >= sc.Count {
			break
		}
		if !given[k.ID] {
			res.IDs[k.ID] = true
			held++
		}
	}
	return res
}

// usableKeys keeps the keys an app may use at all.
func usableKeys(keys []*Key, appID int64, includeHidden bool) []*Key {
	usable := make([]*Key, 0, len(keys))
	for _, k := range keys {
		if !k.AllowsApp(appID) || (k.Hidden && !includeHidden) {
			continue
		}
		usable = append(usable, k)
	}
	return usable
}

// rotationOrder returns the keys still in rotation by priority then id.
func rotationOrder(keys []*Key) []*Key {
	in := make([]*Key, 0, len(keys))
	for _, k := range keys {
		if k.InRotation() {
			in = append(in, k)
		}
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Priority != in[j].Priority {
			return in[i].Priority < in[j].Priority
		}
		return in[i].ID < in[j].ID
	})
	return in
}

// ScopeKeys resolves a scope against a service's keys as stored now.
func (s *Store) ScopeKeys(ctx context.Context, serviceID, appID int64, includeHidden bool, sc *KeyScope) (ScopeResult, error) {
	keys, err := s.queryKeys(ctx, `WHERE service_id=?`, serviceID)
	if err != nil {
		return ScopeResult{}, err
	}
	return ResolveScope(sc, keys, appID, includeHidden), nil
}

// scopeCache holds each app's key scopes so a pick for an app that backs no
// share costs no query. Any share change clears it.
type scopeCache struct {
	mu   sync.Mutex
	gen  uint64
	apps map[int64]map[string]KeyScope // nil entry: the app backs no share
}

// invalidateScopes forgets every cached scope.
func (s *Store) invalidateScopes() {
	c := &s.scopes
	c.mu.Lock()
	c.gen++
	c.apps = nil
	c.mu.Unlock()
}

// AppKeyScope returns the key scope an app has for a service, or nil when it
// may use every key (any app that backs no share, or a service without a
// scope).
func (s *Store) AppKeyScope(ctx context.Context, appID int64, serviceSlug string) (*KeyScope, error) {
	if appID == 0 {
		return nil, nil
	}
	c := &s.scopes
	c.mu.Lock()
	scopes, ok := c.apps[appID]
	gen := c.gen
	c.mu.Unlock()
	if !ok {
		var raw string
		err := s.DB.QueryRowContext(ctx, `SELECT key_scopes FROM shares WHERE app_id=?`, appID).Scan(&raw)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			scopes = nil
		case err != nil:
			return nil, err
		default:
			scopes = decodeScopes(raw)
		}
		c.mu.Lock()
		// A share change while the query ran makes this result stale.
		if c.gen == gen {
			if c.apps == nil {
				c.apps = map[int64]map[string]KeyScope{}
			}
			c.apps[appID] = scopes
		}
		c.mu.Unlock()
	}
	sc, ok := scopes[serviceSlug]
	if !ok || sc.IsAll() {
		return nil, nil
	}
	return &sc, nil
}
