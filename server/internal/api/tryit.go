package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// POST /api/admin/services/{id}/try runs one request through the proxy, the
// same path an app's request takes, and shows what came back: status,
// headers, latency and the body, scrubbed of keys as usual. It runs as a
// chosen app (with that app's allowed services, and read-only when its
// token is), or as the internal "Try it" app, which is a plain proxy app:
// no hidden keys, no lease. Each try is recorded as an event of kind "try".

const (
	tryAppMeta = "tryit_app_id"
	// tryBodyMax is how much of the answer the panel shows.
	tryBodyMax = 256 << 10
)

type tryInput struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query"`
	// Body is the request body as text; it must be JSON when set.
	Body string `json:"body"`
	// AppID runs the request as that app; 0 uses the internal Try it app.
	AppID int64 `json:"app_id"`
	// ReadOnly runs it as a read-only token would: GET only.
	ReadOnly bool `json:"read_only"`
}

// tryApp is the internal app tries run as by default, made on first use.
func (s *Server) tryApp(ctx context.Context, by int64) (*store.App, error) {
	s.ctlAppMu.Lock()
	defer s.ctlAppMu.Unlock()
	if id, err := strconv.ParseInt(s.Store.GetMeta(ctx, tryAppMeta), 10, 64); err == nil && id > 0 {
		if a, err := s.Store.GetApp(ctx, id); err == nil {
			return a, nil
		}
	}
	// Its token is never shown: tries run in process, not over the wire.
	_, prefix, hash := newAppToken()
	a := &store.App{Name: "Try it", Description: "Requests sent from a service page's Try it panel. Limit or turn it off here.",
		Mode: "proxy", Active: true, TokenPrefix: prefix}
	created, tok, err := s.Store.CreateAppWithToken(ctx, a, "Try it", store.TokenPrefixHint(prefix), hash, by)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetMeta(ctx, tryAppMeta, strconv.FormatInt(created.ID, 10)); err != nil {
		return nil, err
	}
	created.TokenID = tok.ID
	return created, nil
}

// tryAs loads the app to run as, with the token it acts through: the first
// usable one that may write, else a read-only one.
func (s *Server) tryAs(ctx context.Context, id int64) (*store.App, error) {
	a, err := s.Store.GetApp(ctx, id)
	if err != nil {
		return nil, err
	}
	ts, err := s.Store.ListAppTokens(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var ro *store.AppToken
	for _, t := range ts {
		if !t.Usable(now) {
			continue
		}
		if !t.ReadOnly {
			a.TokenID, a.TokenReadOnly = t.ID, false
			return a, nil
		}
		if ro == nil {
			ro = t
		}
	}
	if ro == nil {
		return nil, fmt.Errorf("app %s has no working token", a.Name)
	}
	a.TokenID, a.TokenReadOnly = ro.ID, true
	return a, nil
}

func (s *Server) tryService(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadService(w, r)
	if !ok {
		return
	}
	var in tryInput
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	path := strings.TrimSpace(in.Path)
	q := map[string]string{}
	if p, raw, ok := strings.Cut(path, "?"); ok {
		path = p
		vals, err := url.ParseQuery(raw)
		if err != nil {
			writeErr(w, 400, "bad_query", "could not read the query string")
			return
		}
		for k := range vals {
			q[k] = vals.Get(k)
		}
	}
	for k, v := range in.Query {
		if strings.TrimSpace(k) != "" {
			q[k] = v
		}
	}
	var body json.RawMessage
	if b := strings.TrimSpace(in.Body); b != "" {
		if !json.Valid([]byte(b)) {
			writeErr(w, 400, "bad_body", "the body must be JSON")
			return
		}
		body = json.RawMessage(b)
	}
	var a *store.App
	var err error
	if in.AppID != 0 {
		a, err = s.tryAs(ctx, in.AppID)
		if err == nil && !a.Active {
			err = fmt.Errorf("app %s is turned off", a.Name)
		}
		if err != nil {
			writeErr(w, 400, "bad_app", err.Error())
			return
		}
	} else if a, err = s.tryApp(ctx, userID(r)); err != nil {
		writeStoreErr(w, err)
		return
	} else if !a.Active {
		writeErr(w, 403, "app_off", "the Try it app is turned off; turn it on in Apps")
		return
	}
	if in.ReadOnly {
		a.TokenReadOnly = true
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}
	actx := store.WithAppToken(context.WithValue(ctx, appKey, a), a.TokenID)
	start := time.Now()
	rec, err := s.proxyCapture(actx, r, sv.Slug, method, path, q, body, nil, tryBodyMax)
	if err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	ms := time.Since(start).Milliseconds()
	raw := rec.Body.Bytes()
	cut := len(raw) > tryBodyMax
	if cut {
		raw = raw[:tryBodyMax]
	}
	detail := method + " /" + strings.TrimPrefix(path, "/") + " (Try it)"
	s.Store.AddEvent(ctx, store.NewEvent{Kind: "try", ServiceID: sv.ID, AppID: a.ID, StatusCode: rec.Code, LatencyMS: ms, Detail: detail})
	hdr := map[string]string{}
	for k, v := range rec.Header() {
		hdr[k] = s.Store.Scrub(ctx, strings.Join(v, ", "))
	}
	writeJSON(w, 200, map[string]any{
		"status":         rec.Code,
		"latency_ms":     ms,
		"headers":        hdr,
		"content_type":   rec.Header().Get("Content-Type"),
		"body":           s.Store.Scrub(ctx, string(raw)),
		"body_truncated": cut,
		"app":            map[string]any{"id": a.ID, "name": a.Name, "read_only": a.TokenReadOnly},
	})
}
