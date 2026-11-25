package api

// Event streams: server sent events instead of polling GET /events.
//
// GET /api/admin/events/stream (admin token or session) sends every event a
// signed in person may see. GET /api/v1/events/stream (app token) sends
// that app's own events, plus resting, pool and service notifications about
// the services it can use, never key labels or messages about other apps.
//
// Every event is a row of the events table, and its id is the SSE id, so a
// client that reconnects with Last-Event-ID (or ?after_id=) gets what it
// missed, within event retention. Notifications (key.cooling,
// app.limit_near, job.done and the rest) are kept as events too, which is
// how they reach the streams. A stream polls for new rows every second and
// wakes at once when a notification is recorded; it sends a comment every
// 25 seconds to keep proxies from closing it, closes after an hour (the
// client reconnects with its cursor) and ends when the server shuts down.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// eventStreamKeepaliveNs is the ping interval in nanoseconds. It is atomic
// because tests shorten it while streams from an earlier test may still run.
var eventStreamKeepaliveNs atomic.Int64

func init() { eventStreamKeepaliveNs.Store(int64(25 * time.Second)) }

var (
	eventStreamPoll   = time.Second
	eventStreamMaxAge = time.Hour
	// maxEventStreams caps the open streams on a server, and
	// maxAppEventStreams those of one app (or one person).
	maxEventStreams    = 64
	maxAppEventStreams = 4
)

// eventStreamBatch is how many rows one read takes.
const eventStreamBatch = 200

// cursorParams reads after_id, before_id and since. A present after_id of 0
// reads from the oldest event. On a bad value it writes a 400.
func cursorParams(w http.ResponseWriter, r *http.Request) (after, before int64, since time.Time, ok bool) {
	q := r.URL.Query()
	var err error
	if v := q.Get("after_id"); v != "" {
		if after, err = strconv.ParseInt(v, 10, 64); err != nil || after < 0 {
			writeErr(w, 400, "invalid", "after_id is an event id (0 for the oldest)")
			return 0, 0, since, false
		}
		if after == 0 {
			after = -1
		}
	}
	if v := q.Get("before_id"); v != "" {
		if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 0 {
			writeErr(w, 400, "invalid", "before_id is an event id")
			return 0, 0, since, false
		}
	}
	if v := q.Get("since"); v != "" {
		if since, ok = parseSince(v, time.Now()); !ok {
			writeErr(w, 400, "invalid", "since is RFC 3339, Unix seconds or a duration back from now such as 15m or 24h")
			return 0, 0, since, false
		}
	}
	return after, before, since, true
}

// parseSince reads RFC 3339, Unix seconds, or a Go duration meaning that
// long ago.
func parseSince(v string, now time.Time) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
		return time.Unix(n, 0), true
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), true
	}
	return time.Time{}, false
}

// nextCursor is the after_id that continues after a page: the highest id
// seen, or the cursor the page was read from when it was empty.
func nextCursor(after int64, ids []int64) int64 {
	next := max(after, 0)
	for _, id := range ids {
		next = max(next, id)
	}
	return next
}

// streamSlots counts open streams per server and per owner.
type streamSlots struct {
	mu    sync.Mutex
	total int
	by    map[string]int
}

var eventStreamSlots sync.Map // *Server -> *streamSlots

func (s *Server) streamSlots() *streamSlots {
	v, _ := eventStreamSlots.LoadOrStore(s, &streamSlots{by: map[string]int{}})
	return v.(*streamSlots)
}

func (sl *streamSlots) acquire(owner string) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.total >= maxEventStreams || sl.by[owner] >= maxAppEventStreams {
		return false
	}
	sl.total++
	sl.by[owner]++
	return true
}

func (sl *streamSlots) release(owner string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.total--
	if sl.by[owner]--; sl.by[owner] <= 0 {
		delete(sl.by, owner)
	}
}

// streamStart reads the cursor a stream starts after: Last-Event-ID, then
// ?after_id=, else the newest event (only new events).
func (s *Server) streamStart(w http.ResponseWriter, r *http.Request) (int64, bool) {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after_id")
	}
	if v != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || id < 0 {
			writeErr(w, 400, "invalid", "Last-Event-ID and after_id are event ids")
			return 0, false
		}
		return id, true
	}
	id, err := s.Store.MaxEventID(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return 0, false
	}
	return id, true
}

// eventFetch reads the events after a cursor, oldest first.
type eventFetch func(ctx context.Context, after int64) ([]*store.Event, error)

// serveEventStream runs one stream: it sends what fetch returns after the
// cursor, then waits for more. view turns an event into what is sent.
func (s *Server) serveEventStream(w http.ResponseWriter, r *http.Request, owner string, cursor int64, fetch eventFetch, view func(*store.Event) any) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no_stream", "streaming is not supported here")
		return
	}
	slots := s.streamSlots()
	if !slots.acquire(owner) {
		w.Header().Set("Retry-After", "30")
		writeErr(w, 503, "too_many_streams", "too many event streams are open; close one or try again later")
		return
	}
	defer slots.release(owner)
	ctx := r.Context()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: 3000\n: acnologia events after %d\n\n", cursor)
	fl.Flush()

	// drain sends every event after the cursor, a batch at a time.
	drain := func() bool {
		for {
			evs, err := fetch(ctx, cursor)
			if err != nil {
				return ctx.Err() == nil
			}
			for _, e := range evs {
				b, _ := json.Marshal(view(e))
				if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.ID, b); err != nil {
					return false
				}
				cursor = e.ID
			}
			if len(evs) > 0 {
				fl.Flush()
			}
			if len(evs) < eventStreamBatch {
				return true
			}
		}
	}
	if !drain() {
		return
	}
	var n *notify.Notifier
	if s.Pool != nil {
		n = s.Pool.Notify
	}
	keep := time.NewTicker(time.Duration(eventStreamKeepaliveNs.Load()))
	defer keep.Stop()
	poll := time.NewTicker(eventStreamPoll)
	defer poll.Stop()
	end := time.NewTimer(eventStreamMaxAge)
	defer end.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.Done():
			return
		case <-end.C:
			return
		case <-keep.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-n.Wake():
			if !drain() {
				return
			}
		case <-poll.C:
			if !drain() {
				return
			}
		}
	}
}

// adminEventStream serves GET /api/admin/events/stream with the filters of
// GET /events: service_id, key_id, app_id, token_id and kind.
func (s *Server) adminEventStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EventFilter{Kind: q.Get("kind"), ExcludeHidden: !isSuper(r), Limit: eventStreamBatch}
	f.ServiceID, _ = strconv.ParseInt(q.Get("service_id"), 10, 64)
	f.KeyID, _ = strconv.ParseInt(q.Get("key_id"), 10, 64)
	f.AppID, _ = strconv.ParseInt(q.Get("app_id"), 10, 64)
	f.TokenID, _ = strconv.ParseInt(q.Get("token_id"), 10, 64)
	cursor, ok := s.streamStart(w, r)
	if !ok {
		return
	}
	fetch := func(ctx context.Context, after int64) ([]*store.Event, error) {
		ff := f
		ff.AfterID = after
		if ff.AfterID == 0 {
			ff.AfterID = -1
		}
		return s.Store.ListEvents(ctx, ff)
	}
	owner := "user:" + strconv.FormatInt(userFrom(r).ID, 10)
	s.serveEventStream(w, r, owner, cursor, fetch, func(e *store.Event) any { return e })
}

// appOwnNotices are the notifications about an app it reads on its stream.
var appOwnNotices = []string{notify.AppLimit, notify.AppLimitNear}

// appServiceNotices are the notifications about a service an app reads on
// its stream when it can use the service.
var appServiceNotices = []string{notify.KeyCooling, notify.KeyExhausted, notify.KeyRecovered, notify.PoolEmpty,
	notify.ServiceDown, notify.ServiceUp}

// appEventView is an event as an app's stream sends it: no key label, and
// for a notification about a service no key and no message.
type appEventView struct {
	ID          int64           `json:"id"`
	TS          time.Time       `json:"ts"`
	Kind        string          `json:"kind"`
	ServiceID   *int64          `json:"service_id,omitempty"`
	ServiceSlug string          `json:"service_slug,omitempty"`
	KeyID       *int64          `json:"key_id,omitempty"`
	TokenID     *int64          `json:"token_id,omitempty"`
	StatusCode  int             `json:"status_code,omitempty"`
	LatencyMS   int64           `json:"latency_ms,omitempty"`
	Detail      string          `json:"detail,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

func appEvent(appID int64) func(*store.Event) any {
	return func(e *store.Event) any {
		v := appEventView{ID: e.ID, TS: e.TS, Kind: e.Kind, ServiceID: e.ServiceID, ServiceSlug: e.ServiceSlug, Data: e.Data}
		if e.AppID != nil && *e.AppID == appID {
			v.KeyID, v.TokenID, v.StatusCode, v.LatencyMS, v.Detail = e.KeyID, e.TokenID, e.StatusCode, e.LatencyMS, e.Detail
		}
		return v
	}
}

// appEventStream serves GET /api/v1/events/stream for an app token.
func (s *Server) appEventStream(w http.ResponseWriter, r *http.Request) {
	a := appFrom(r)
	sc := store.AppEventScope{AppID: a.ID, OwnNotices: appOwnNotices, ServiceKinds: appServiceNotices}
	if len(a.AllowedServices) > 0 {
		svcs, err := s.Store.ListServices(r.Context())
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		sc.ServiceIDs = []int64{}
		for _, sv := range svcs {
			if a.CanUse(sv.Slug) {
				sc.ServiceIDs = append(sc.ServiceIDs, sv.ID)
			}
		}
	}
	cursor, ok := s.streamStart(w, r)
	if !ok {
		return
	}
	fetch := func(ctx context.Context, after int64) ([]*store.Event, error) {
		return s.Store.ListAppEvents(ctx, sc, after, eventStreamBatch)
	}
	owner := "app:" + strconv.FormatInt(a.ID, 10)
	s.serveEventStream(w, r, owner, cursor, fetch, appEvent(a.ID))
}
