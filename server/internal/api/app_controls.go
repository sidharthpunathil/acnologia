package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Finer access control for an app: the per-token default limits, budgets
// per caller tag value, deny rules, usage by tag, and the server-wide daily
// spend ceiling. Admission (proxy_extras.go) enforces them.

// appControls answers the app's controls in one read for the dashboard.
func (s *Server) appControls(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	def, err := s.Store.GetTokenDefaultLimits(ctx, a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	budgets, err := s.Store.GetTagBudgets(ctx, a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rules, err := s.Store.DenyRules(ctx, a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"token_limits": def, "tag_budgets": budgets, "deny_rules": rules})
}

// putAppTokenLimits sets the limits every token of the app gets on its own
// unless the token sets its own: each claimant of a share link, or each
// agent's token, then has a cap of its own inside the app's.
func (s *Server) putAppTokenLimits(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	var in struct {
		Limits store.AppLimits `json:"limits"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := in.Limits.Validate(); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	if err := s.Store.SetTokenDefaultLimits(r.Context(), a.ID, in.Limits); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.token_limits", a.Name, limitsText(in.Limits))
	writeJSON(w, 200, map[string]any{"token_limits": in.Limits})
}

// putAppTagBudgets replaces the app's tag budgets.
func (s *Server) putAppTagBudgets(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	var in struct {
		Budgets []store.TagBudget `json:"budgets"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := store.ValidateTagBudgets(in.Budgets); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	if err := s.Store.SetTagBudgets(r.Context(), a.ID, in.Budgets); err != nil {
		writeStoreErr(w, err)
		return
	}
	detail := "none"
	if len(in.Budgets) > 0 {
		detail = ""
		for i, b := range in.Budgets {
			if i > 0 {
				detail += "; "
			}
			detail += "each " + b.Key + ": " + limitsText(b.AppLimits)
		}
	}
	if in.Budgets == nil {
		in.Budgets = []store.TagBudget{}
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.tag_budgets", a.Name, detail)
	writeJSON(w, 200, map[string]any{"tag_budgets": in.Budgets})
}

// limitsText is limits in a short line for the audit log.
func limitsText(l store.AppLimits) string {
	if !l.Any() {
		return "no limits"
	}
	out := ""
	add := func(s string) {
		if out != "" {
			out += ", "
		}
		out += s
	}
	if l.DailyRequests != nil {
		add(fmt.Sprintf("%d requests a day", *l.DailyRequests))
	}
	if l.MonthlyRequests != nil {
		add(fmt.Sprintf("%d requests a month", *l.MonthlyRequests))
	}
	if l.DailyBudget != nil {
		add(fmt.Sprintf("$%g a day", *l.DailyBudget))
	}
	if l.MonthlyBudget != nil {
		add(fmt.Sprintf("$%g a month", *l.MonthlyBudget))
	}
	return out
}

func (s *Server) listDenyRules(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	rules, err := s.Store.DenyRules(r.Context(), a.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"rules": rules})
}

func (s *Server) createDenyRule(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	var in store.DenyRule
	if !decode(w, r, &in) {
		return
	}
	in.ID, in.AppID = 0, a.ID
	if err := in.Normalize(); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	rule, err := s.Store.AddDenyRule(r.Context(), in)
	if err != nil {
		if errors.Is(err, store.ErrTooManyDenyRules) {
			writeErr(w, 400, "too_many_rules", err.Error())
			return
		}
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.deny_rule_add", a.Name, rule.Describe())
	writeJSON(w, 201, rule)
}

func (s *Server) deleteDenyRule(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "ruleId"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad_id", "rule id must be a number")
		return
	}
	var desc string
	if rules, err := s.Store.DenyRules(r.Context(), a.ID); err == nil {
		for _, rl := range rules {
			if rl.ID == id {
				desc = rl.Describe()
			}
		}
	}
	if err := s.Store.DeleteDenyRule(r.Context(), a.ID, id); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "app.deny_rule_delete", a.Name, desc)
	w.WriteHeader(http.StatusNoContent)
}

// tagUsageDays reads ?days, 1 to 366, 30 by default.
func tagUsageDays(r *http.Request) int {
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 366 {
		return d
	}
	return 30
}

// maxTagUsageRows caps a usage by tag answer.
const maxTagUsageRows = 500

// appTagUsage is an app's use per caller tag value, with its tag budgets.
func (s *Server) appTagUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := s.tokenApp(w, r)
	if !ok {
		return
	}
	s.writeTagUsage(w, r, a.ID)
}

// v1TagUsage is the calling app's own use per tag value, and its token's.
func (s *Server) v1TagUsage(w http.ResponseWriter, r *http.Request) {
	s.writeTagUsage(w, r, appFrom(r).ID)
}

func (s *Server) writeTagUsage(w http.ResponseWriter, r *http.Request, appID int64) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	days := tagUsageDays(r)
	rows, err := s.Store.TagUsage(ctx, appID, days, maxTagUsageRows, time.Now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	budgets, err := s.Store.GetTagBudgets(ctx, appID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"days": days, "tags": rows, "tag_budgets": budgets, "truncated": len(rows) == maxTagUsageRows})
}

// spendCeilingView is the ceiling in effect and where it comes from, with
// what the server has spent so far today.
func (s *Server) spendCeilingView(r *http.Request) map[string]any {
	ctx := r.Context()
	saved, isSaved := s.Store.SavedSpendCeiling(ctx)
	source := "env"
	if isSaved {
		source = "saved"
	}
	now := time.Now().UTC()
	spent, _ := s.Store.ServerSpendToday(ctx, now)
	y, m, d := now.Date()
	out := map[string]any{
		"ceiling_usd":     s.spendCeiling(ctx),
		"source":          source,
		"env_usd":         s.Cfg.DailySpendCeilingUSD,
		"spent_today_usd": spent,
		"reset_at":        time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC),
	}
	if isSaved {
		out["saved_usd"] = saved
	}
	return out
}

func (s *Server) getSpendCeiling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.spendCeilingView(r))
}

// putSpendCeiling saves the server-wide daily spend ceiling: a dollar
// amount, 0 for none, or null to go back to the environment's.
func (s *Server) putSpendCeiling(w http.ResponseWriter, r *http.Request) {
	var in struct {
		USD json.RawMessage `json:"usd"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	var err error
	detail := ""
	if len(in.USD) == 0 || string(in.USD) == "null" {
		err = s.Store.ClearSpendCeiling(ctx)
		detail = "back to the environment's"
	} else {
		var v float64
		if json.Unmarshal(in.USD, &v) != nil || v < 0 || v > 1e12 {
			writeErr(w, 400, "invalid", "usd must be a dollar amount of 0 or more, or null")
			return
		}
		err = s.Store.SetSpendCeiling(ctx, v)
		detail = fmt.Sprintf("$%g a day", v)
		if v == 0 {
			detail = "off"
		}
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(ctx, userFrom(r), "settings.spend_ceiling", "server", detail)
	writeJSON(w, 200, s.spendCeilingView(r))
}
