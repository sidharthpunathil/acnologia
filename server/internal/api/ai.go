package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/agent"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Agent.Status())
}

func (s *Server) aiGetRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Agent.GetRules(r.Context()))
}

func (s *Server) aiSetRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules string `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	rules, err := s.Agent.SetRules(r.Context(), body.Rules, userFrom(r).Email)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "ai.rules", "", "updated AI rules")
	writeJSON(w, 200, rules)
}

func writeAIErr(w http.ResponseWriter, err error) {
	if errors.Is(err, agent.ErrDisabled) {
		writeErr(w, 503, "ai_disabled", err.Error())
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	writeErr(w, 400, "ai_error", err.Error())
}

func (s *Server) aiAssist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Form        string         `json:"form"`
		Instruction string         `json:"instruction"`
		Current     map[string]any `json:"current"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, err := s.Agent.Assist(r.Context(), userFrom(r), body.Form, body.Instruction, body.Current)
	if err != nil {
		writeAIErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) aiListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.ListRuns(r.Context(), 50)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

func (s *Server) aiCreateRun(w http.ResponseWriter, r *http.Request) {
	var req agent.RunRequest
	if !decode(w, r, &req) {
		return
	}
	run, err := s.Agent.Submit(r.Context(), userFrom(r), req)
	if err != nil {
		writeAIErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "ai.run", run.Title, req.Kind)
	writeJSON(w, 201, run)
}

func (s *Server) aiGetRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	steps, err := s.Store.ListSteps(r.Context(), id, after)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"run": run, "steps": steps})
}

func (s *Server) aiCancelRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := s.Agent.Cancel(r.Context(), id); err != nil {
		writeAIErr(w, err)
		return
	}
	run, _ := s.Store.GetRun(r.Context(), id)
	writeJSON(w, 200, run)
}

func (s *Server) aiDeleteRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.Store.DeleteRun(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrRunBusy) {
			writeErr(w, 409, "run_busy", err.Error())
			return
		}
		writeStoreErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "ai.run.delete", run.Title, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) aiClearRuns(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.ClearRuns(r.Context())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	if n > 0 {
		s.Store.AddAudit(r.Context(), userFrom(r), "ai.run.clear", fmt.Sprintf("%d runs", n), "")
	}
	writeJSON(w, 200, map[string]int64{"deleted": n})
}

func (s *Server) aiPublishRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	run, err := s.Agent.PublishDraft(r.Context(), id, userFrom(r).Email)
	if err != nil {
		s.writePublishErr(w, err)
		return
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "ai.publish", run.Title, "")
	writeJSON(w, 200, run)
}

// aiModelChoice is one feature's effective model with its context window.
type aiModelChoice struct {
	agent.ModelChoice
	ContextWindow int `json:"context_window"`
}

// aiModelsState is the model settings as the Settings page shows them.
type aiModelsState struct {
	Settings  agent.ModelSettings      `json:"settings"`
	Effective map[string]aiModelChoice `json:"effective"`
	// EnvModel is ACNOLOGIA_AI_MODEL (empty when unset), BuiltinModel the
	// model used when neither it nor a saved default is set.
	EnvModel     string `json:"env_model"`
	BuiltinModel string `json:"builtin_model"`
	// CompactTokens is the chat compaction threshold now in effect, and
	// CompactFromEnv whether ACNOLOGIA_CHAT_COMPACT_TOKENS sets it.
	CompactTokens  int  `json:"compact_tokens"`
	CompactFromEnv bool `json:"compact_from_env"`
	AIEnabled      bool `json:"ai_enabled"`
	// CanEdit is whether the caller may save (superadmins only).
	CanEdit bool `json:"can_edit"`
}

func (s *Server) aiModelsState(r *http.Request) aiModelsState {
	ctx := r.Context()
	eff := map[string]aiModelChoice{}
	for k, c := range s.Agent.EffectiveModels(ctx) {
		eff[k] = aiModelChoice{ModelChoice: c, ContextWindow: s.Agent.ContextWindowOf(c.Model)}
	}
	return aiModelsState{Settings: s.Agent.Models(ctx), Effective: eff, EnvModel: s.Agent.EnvModel(),
		BuiltinModel: agent.BuiltinModel, CompactTokens: s.compactAt(), CompactFromEnv: s.Cfg.ChatCompactTokens > 0,
		AIEnabled: s.Agent.Enabled(), CanEdit: isSuper(r)}
}

// aiModels lists the models to pick from with the saved and effective choice.
func (s *Server) aiModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cat := s.Agent.ListModels(r.Context())
	writeJSON(w, 200, struct {
		agent.ModelCatalog
		aiModelsState
	}{cat, s.aiModelsState(r)})
}

// aiSetModels saves the default model and the per feature overrides.
func (s *Server) aiSetModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Default   string `json:"default"`
		Assistant string `json:"assistant"`
		Builder   string `json:"builder"`
		Fill      string `json:"fill"`
		Summary   string `json:"summary"`
		Refresh   string `json:"refresh"`
	}
	if !decode(w, r, &body) {
		return
	}
	u := userFrom(r)
	in := agent.ModelSettings{Default: body.Default, Assistant: body.Assistant, Builder: body.Builder, Fill: body.Fill, Summary: body.Summary, Refresh: body.Refresh}
	old, saved, warnings, err := s.Agent.SaveModels(r.Context(), in, u.Email)
	if err != nil {
		if errors.Is(err, agent.ErrInvalidModel) {
			writeErr(w, 400, "invalid_model", err.Error())
			return
		}
		writeStoreErr(w, err)
		return
	}
	if d := modelsDiff(old, saved); d != "" {
		s.Store.AddAudit(r.Context(), u, "ai.models", "", d)
	}
	writeJSON(w, 200, struct {
		aiModelsState
		Warnings []string `json:"warnings"`
	}{s.aiModelsState(r), warnings})
}

// modelsDiff describes what changed, "default: claude-opus-5-5 -> claude-sonnet-5-5;
// fill: (default) -> claude-haiku-5-5", or "" when nothing did.
func modelsDiff(old, next agent.ModelSettings) string {
	show := func(v string) string {
		if v == "" {
			return "(default)"
		}
		return v
	}
	parts := []string{}
	for _, f := range append([]string{"default"}, agent.ModelFeatures...) {
		if a, b := old.Field(f), next.Field(f); a != b {
			parts = append(parts, fmt.Sprintf("%s: %s -> %s", f, show(a), show(b)))
		}
	}
	return strings.Join(parts, "; ")
}
