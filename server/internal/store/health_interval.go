package store

import (
	"context"
	"time"
)

// The background key health check interval: how often every key is checked
// against its provider. A superadmin can set it on the Settings page; it is
// saved in meta and wins over ACNOLOGIA_HEALTH_INTERVAL, which stays the
// fallback when nothing is saved.

// metaHealthInterval holds the saved interval as a Go duration ("6h") or
// "off". Empty means use the environment.
const metaHealthInterval = "health_interval"

// SavedHealthInterval returns the saved interval and whether one is saved.
// "off" reads as 0.
func (s *Store) SavedHealthInterval(ctx context.Context) (time.Duration, bool) {
	v := s.GetMeta(ctx, metaHealthInterval)
	if v == "" {
		return 0, false
	}
	if v == "off" {
		return 0, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// HealthInterval is the interval in effect: the saved one, else fallback
// (the environment's). 0 means background checks are off.
func (s *Store) HealthInterval(ctx context.Context, fallback time.Duration) time.Duration {
	if d, ok := s.SavedHealthInterval(ctx); ok {
		return d
	}
	return fallback
}

// SetHealthInterval saves the interval; 0 saves "off". ClearHealthInterval
// goes back to the environment.
func (s *Store) SetHealthInterval(ctx context.Context, d time.Duration) error {
	v := "off"
	if d > 0 {
		v = d.String()
	}
	return s.SetMeta(ctx, metaHealthInterval, v)
}

// ClearHealthInterval drops the saved interval, so the environment's applies.
func (s *Store) ClearHealthInterval(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM meta WHERE k=?`, metaHealthInterval)
	return err
}
