package api

import (
	"net/http"
	"strings"
	"time"
)

// Background key health checks: how often every key is checked against its
// provider. GET answers admins with the interval in effect and where it comes
// from; PUT lets a superadmin pick one of a few intervals, "off", or ""
// to go back to ACNOLOGIA_HEALTH_INTERVAL. The worker reads the value on
// every pass, so a change applies within a minute, without a restart.

// healthChoices are the intervals the Settings page offers. Short enough to
// notice a dead key the same hour, long enough not to spend free quota.
var healthChoices = []struct {
	ID string
	D  time.Duration
}{
	{"off", 0},
	{"15m", 15 * time.Minute},
	{"30m", 30 * time.Minute},
	{"1h", time.Hour},
	{"6h", 6 * time.Hour},
	{"12h", 12 * time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
}

// intervalID names d the way the choices do ("6h", "7d", "off"), else Go's
// form with the zero tail cut ("45m").
func intervalID(d time.Duration) string {
	for _, c := range healthChoices {
		if c.D == d {
			return c.ID
		}
	}
	return strings.TrimSuffix(strings.TrimSuffix(d.String(), "0s"), "0m")
}

func (s *Server) healthSettingsView(r *http.Request) map[string]any {
	ctx := r.Context()
	every := s.Store.HealthInterval(ctx, s.Cfg.HealthInterval)
	saved, isSaved := s.Store.SavedHealthInterval(ctx)
	source := "env"
	if isSaved {
		source = "saved"
	}
	choices := make([]string, 0, len(healthChoices))
	for _, c := range healthChoices {
		choices = append(choices, c.ID)
	}
	savedID := ""
	if isSaved {
		savedID = intervalID(saved)
	}
	return map[string]any{
		"interval":     intervalID(every),
		"interval_sec": int(every.Seconds()),
		"source":       source,
		"saved":        savedID,
		"env":          intervalID(s.Cfg.HealthInterval),
		"choices":      choices,
	}
}

func (s *Server) getHealthSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.healthSettingsView(r))
}

func (s *Server) putHealthSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Interval *string `json:"interval"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Interval == nil {
		writeErr(w, 400, "invalid", "interval is required: one of off, 15m, 30m, 1h, 6h, 12h, 24h, 7d, or \"\" to use ACNOLOGIA_HEALTH_INTERVAL")
		return
	}
	ctx := r.Context()
	before := intervalID(s.Store.HealthInterval(ctx, s.Cfg.HealthInterval))
	id := strings.TrimSpace(*in.Interval)
	var err error
	if id == "" {
		err = s.Store.ClearHealthInterval(ctx)
	} else {
		found := false
		for _, c := range healthChoices {
			if c.ID == id {
				err, found = s.Store.SetHealthInterval(ctx, c.D), true
				break
			}
		}
		if !found {
			writeErr(w, 400, "invalid", "interval must be one of off, 15m, 30m, 1h, 6h, 12h, 24h, 7d, or \"\" to use ACNOLOGIA_HEALTH_INTERVAL")
			return
		}
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v := s.healthSettingsView(r)
	detail := before + " to " + v["interval"].(string)
	if id == "" {
		detail += " (from ACNOLOGIA_HEALTH_INTERVAL)"
	}
	s.Store.AddAudit(ctx, userFrom(r), "settings.health_interval", "", detail)
	writeJSON(w, 200, v)
}
