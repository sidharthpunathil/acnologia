package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// usageMaxDays bounds the range of GET /api/admin/usage.
const usageMaxDays = 366

// usageReport is GET /api/admin/usage?days=30: what Claude calls and provider
// requests through the pool have cost. Members can read it; provider spend of
// hidden keys is left out for anyone but a superadmin.
func (s *Server) usageReport(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > usageMaxDays {
			writeErr(w, 400, "invalid", "days must be a whole number from 1 to "+strconv.Itoa(usageMaxDays))
			return
		}
		days = n
	}
	rep, err := s.Store.UsageReport(r.Context(), time.Now(), days, isSuper(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	st := s.Agent.Status()
	writeJSON(w, 200, struct {
		*store.UsageReport
		AIEnabled bool        `json:"ai_enabled"`
		AIModel   string      `json:"ai_model"`
		AIPrice   agent.Price `json:"ai_price"`
		// EventRetentionDays bounds how far back provider spend goes; 0 keeps events forever.
		EventRetentionDays int `json:"event_retention_days"`
	}{rep, st.Enabled, st.Model, s.Agent.EffectivePrice(st.Model), s.Cfg.EventRetentionDays})
}
