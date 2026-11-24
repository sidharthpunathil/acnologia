package api

// Background jobs for slow admin operations. Checking every key, a
// service's keys, a peer sync and a service's MCP sync answer as before by
// default; with ?async=1 or Prefer: respond-async they answer 202 with a
// job id at once and run in the background. GET /api/admin/jobs/{id} reads
// the job: its status (queued, running, done, failed), progress, the result
// the synchronous call would have answered, or the error. A job.done event
// reaches the event streams (and webhooks that list it) when it ends.
//
// Jobs run in this process: at most two at a time, the rest queued. A job
// still running at shutdown is cut off and marked failed at the next start.
// A job is shown to the person who started it and to superadmins.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// maxRunningJobs is how many jobs run at once.
var maxRunningJobs = 2

// maxJobResult caps a stored result; a larger one keeps only its message.
const maxJobResult = 2 << 20

var jobSlots sync.Map // *Server -> chan struct{}

func (s *Server) jobSem() chan struct{} {
	v, _ := jobSlots.LoadOrStore(s, make(chan struct{}, maxRunningJobs))
	return v.(chan struct{})
}

// wantsAsync reports whether the caller asked for a job instead of waiting.
func wantsAsync(r *http.Request) bool {
	if truthy(r.URL.Query().Get("async")) {
		return true
	}
	for _, p := range strings.Split(r.Header.Get("Prefer"), ",") {
		if strings.EqualFold(strings.TrimSpace(p), "respond-async") {
			return true
		}
	}
	return false
}

func jobURL(id int64) string { return "/api/admin/jobs/" + strconv.FormatInt(id, 10) }

// jobProgressKey carries the running job's progress recorder.
type jobProgressKey struct{}

type progressFunc func(done, total int)

// jobProgress records progress when ctx belongs to a job; else it does
// nothing.
func jobProgress(ctx context.Context, done, total int) {
	if f, ok := ctx.Value(jobProgressKey{}).(progressFunc); ok {
		f(done, total)
	}
}

// detachRoute copies the chi route context: chi reuses the original once
// the request that started a job has been answered.
func detachRoute(ctx context.Context) context.Context {
	rc := chi.RouteContext(ctx)
	if rc == nil {
		return ctx
	}
	nrc := chi.NewRouteContext()
	nrc.URLParams.Keys = append([]string(nil), rc.URLParams.Keys...)
	nrc.URLParams.Values = append([]string(nil), rc.URLParams.Values...)
	nrc.RoutePatterns = append([]string(nil), rc.RoutePatterns...)
	return context.WithValue(ctx, chi.RouteCtxKey, nrc)
}

// startJob records a job and runs it in the background with the request's
// identity (user, token, attribution) but not its lifetime. With dedupe, a
// queued or running job of the same kind and target the same person started
// is returned instead of a new one.
func (s *Server) startJob(r *http.Request, kind, target string, dedupe bool, run func(ctx context.Context) (any, error)) (*store.Job, error) {
	var uid int64
	email := "system"
	if u := userFrom(r); u != nil {
		uid, email = u.ID, u.Email
	}
	if dedupe {
		if j, err := s.Store.ActiveJob(r.Context(), kind, target); err == nil && j.CreatedBy != nil && *j.CreatedBy == uid {
			return j, nil
		}
	}
	job, err := s.Store.CreateJob(r.Context(), kind, target, uid, email, store.AttributionFrom(r.Context()).Via)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(detachRoute(context.WithoutCancel(r.Context())))
	var done <-chan struct{}
	if s.Pool != nil {
		done = s.Pool.Notify.Done()
	}
	go func() {
		defer cancel()
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-done:
				cancel()
			case <-stop:
			}
		}()
		s.runJob(ctx, job, run)
	}()
	return job, nil
}

func (s *Server) runJob(ctx context.Context, job *store.Job, run func(ctx context.Context) (any, error)) {
	// Job bookkeeping must land even when the job's context ends.
	bg := context.WithoutCancel(ctx)
	sem := s.jobSem()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		s.finishJob(bg, job, nil, errors.New("the server shut down before the job started"))
		return
	}
	defer func() { <-sem }()
	_ = s.Store.StartJob(bg, job.ID)
	var mu sync.Mutex
	var last time.Time
	progress := progressFunc(func(done, total int) {
		mu.Lock()
		defer mu.Unlock()
		if done < total && time.Since(last) < 500*time.Millisecond {
			return
		}
		last = time.Now()
		_ = s.Store.JobProgress(bg, job.ID, done, total)
	})
	var result any
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("the job stopped on an internal error")
			}
		}()
		result, err = run(context.WithValue(ctx, jobProgressKey{}, progress))
	}()
	if err == nil && ctx.Err() != nil {
		err = errors.New("the server shut down before the job finished")
	}
	s.finishJob(bg, job, result, err)
}

func (s *Server) finishJob(ctx context.Context, job *store.Job, result any, err error) {
	var b []byte
	if result != nil {
		b, _ = json.Marshal(result)
		if len(b) > maxJobResult {
			b, _ = json.Marshal(map[string]any{"truncated": true, "message": jobMessage(b)})
		}
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		if msg == "" {
			msg = "failed"
		}
	}
	j, ferr := s.Store.FinishJob(ctx, job.ID, b, msg)
	if ferr != nil {
		return
	}
	// A sync can change what MCP clients see.
	s.catalogChanged()
	if s.Pool == nil || s.Pool.Notify == nil {
		return
	}
	text := fmt.Sprintf("job %d (%s) is %s", j.ID, j.Kind, j.Status)
	if j.Error != "" {
		text += ": " + j.Error
	}
	data := map[string]any{"job_id": j.ID, "kind": j.Kind, "target": j.Target, "status": j.Status, "status_url": jobURL(j.ID)}
	if j.CreatedBy != nil {
		data["created_by"] = *j.CreatedBy
	}
	if j.Error != "" {
		data["error"] = j.Error
	}
	s.Pool.Notify.Emit(notify.Event{Type: notify.JobDone, Message: text, Data: data})
}

// jobMessage reads "message" from a JSON object, for a result too big to keep.
func jobMessage(b []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &m)
	return m.Message
}

// writeJobAccepted answers 202 with the job and where to read it.
func writeJobAccepted(w http.ResponseWriter, r *http.Request, j *store.Job) {
	u := jobURL(j.ID)
	w.Header().Set("Location", u)
	if strings.Contains(strings.ToLower(r.Header.Get("Prefer")), "respond-async") {
		w.Header().Set("Preference-Applied", "respond-async")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": j.ID, "status": j.Status, "status_url": u, "job": j})
}

// asyncable runs h as is, or with ?async=1 (or Prefer: respond-async) as a
// job whose result is what h would have answered. A failed answer (4xx or
// 5xx) fails the job with its message.
func (s *Server) asyncable(kind string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !wantsAsync(r) {
			h(w, r)
			return
		}
		var body []byte
		if r.Body != nil {
			var err error
			if body, err = io.ReadAll(io.LimitReader(r.Body, 4<<20)); err != nil {
				writeErr(w, 400, "invalid", "could not read the request body")
				return
			}
		}
		job, err := s.startJob(r, kind, chi.URLParam(r, "id"), true, func(ctx context.Context) (any, error) {
			req := r.Clone(ctx)
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			rec := httptest.NewRecorder()
			h(rec, req)
			out := rec.Body.Bytes()
			var res any = json.RawMessage(out)
			if !json.Valid(out) {
				res = string(out)
			}
			if rec.Code >= 400 {
				return res, errors.New(errorMessage(out, rec.Code))
			}
			return res, nil
		})
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		writeJobAccepted(w, r, job)
	}
}

// errorMessage reads the message of an error answer.
func errorMessage(b []byte, code int) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return "HTTP " + strconv.Itoa(code)
}

// checkNewKeys checks keys just added in the background, as a job the
// add keys answer points to. It returns nil when the provider has no way to
// check a key.
func (s *Server) checkNewKeys(r *http.Request, sv *store.Service, keys []*store.Key) *store.Job {
	spec := providers.Get(sv.Provider)
	if len(keys) == 0 || (spec.Health == nil && providers.ResolveMCPURL(sv.BaseURL, sv.MCPURL, spec.MCP) == "") {
		return nil
	}
	job, err := s.startJob(r, "keys.check_new", sv.Slug, false, func(ctx context.Context) (any, error) {
		ctx = context.WithValue(withKeyCount(ctx, len(keys)), quietHealthKey{}, true)
		sum := s.checkKeys(ctx, sv, keys)
		return sum, nil
	})
	if err != nil {
		// Still check them, without a job to read.
		go s.checkMany(sv, keys)
		return nil
	}
	return job
}

// canSeeJob: the person who started a job, or a superadmin.
func canSeeJob(r *http.Request, j *store.Job) bool {
	u := userFrom(r)
	return u != nil && (u.Role == store.RoleSuperadmin || (j.CreatedBy != nil && *j.CreatedBy == u.ID))
}

// getJob serves GET /api/admin/jobs/{id}.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	j, err := s.Store.GetJob(r.Context(), id)
	if err == nil && !canSeeJob(r, j) {
		err = store.ErrNotFound
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": j, "status_url": jobURL(j.ID)})
}

// listJobs serves GET /api/admin/jobs: the caller's jobs, every job for a
// superadmin, newest first, without results. Filters kind, status, limit
// (max 200) and, for a superadmin, mine=1.
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.JobFilter{Kind: q.Get("kind"), Status: q.Get("status")}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if u := userFrom(r); u.Role != store.RoleSuperadmin || truthy(q.Get("mine")) {
		f.CreatedBy = u.ID
	}
	js, err := s.Store.ListJobs(r.Context(), f)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": js})
}
