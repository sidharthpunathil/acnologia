package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

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
