package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

type auditPage struct {
	Audit      []store.AuditEntry `json:"audit"`
	NextCursor int64              `json:"next_cursor"`
}

// The audit log says how a change came in, with which token and client:
// the dashboard, an admin token, the CLI and the control MCP (with the
// client from initialize) are told apart, and the log filters on them.
func TestAuditAttribution(t *testing.T) {
	e := newEnv(t)
	var tok struct {
		Token string `json:"token"`
		Info  struct {
			ID int64 `json:"id"`
		} `json:"info"`
	}
	e.do(e.super, "POST", "/api/admin/tokens", map[string]any{"name": "agent", "scope": "admin"}, 201, &tok)
	bearer := "Bearer " + tok.Token

	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "from-dash", "base_url": e.upURL}, 201, nil,
		"User-Agent", "Mozilla/5.0 test")
	e.do(http.DefaultClient, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "from-api", "base_url": e.upURL}, 201, nil,
		"Authorization", bearer, "User-Agent", "curl/8.4")
	e.do(http.DefaultClient, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "from-cli", "base_url": e.upURL}, 201, nil,
		"Authorization", bearer, "X-Requested-With", "acnologia-ctl")
	c := &ctlClient{e: e, tok: tok.Token}
	c.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "claude-code", "version": "2.1"}})
	if _, text, isErr := c.tool("create_service", map[string]any{"provider": "serpapi", "slug": "from-mcp", "base_url": e.upURL}); isErr {
		t.Fatal(text)
	}

	var page auditPage
	e.do(e.super, "GET", "/api/admin/audit?action=service.create", nil, 200, &page)
	by := map[string]store.AuditEntry{}
	for _, a := range page.Audit {
		by[a.Target] = a
	}
	check := func(target, via, client string, tokenID int64) {
		t.Helper()
		a, ok := by[target]
		if !ok {
			t.Fatalf("no audit row for %s in %+v", target, page.Audit)
		}
		var got int64
		if a.TokenID != nil {
			got = *a.TokenID
		}
		if a.Via != via || a.Client != client || got != tokenID {
			t.Fatalf("%s: via %q client %q token %d, want %q %q %d", target, a.Via, a.Client, got, via, client, tokenID)
		}
	}
	check("from-dash", store.ViaDashboard, "Mozilla/5.0 test", 0)
	check("from-api", store.ViaAPI, "curl/8.4", tok.Info.ID)
	check("from-cli", store.ViaCLI, "Go-http-client/1.1", tok.Info.ID)
	check("from-mcp", store.ViaControlMCP, "claude-code/2.1", tok.Info.ID)

	e.do(e.super, "GET", "/api/admin/audit?via=control_mcp", nil, 200, &page)
	for _, a := range page.Audit {
		if a.Via != store.ViaControlMCP {
			t.Fatalf("via filter let %+v through", a)
		}
	}
	if len(page.Audit) == 0 {
		t.Fatal("via filter found nothing")
	}
	e.do(e.super, "GET", "/api/admin/audit?client=CLAUDE&action=service.create", nil, 200, &page)
	if len(page.Audit) != 1 || page.Audit[0].Target != "from-mcp" {
		t.Fatalf("client filter: %+v", page.Audit)
	}
	e.do(e.super, "GET", "/api/admin/audit?action=service.create&token_id="+strconv.FormatInt(tok.Info.ID, 10), nil, 200, &page)
	if len(page.Audit) != 3 {
		t.Fatalf("token filter: %+v", page.Audit)
	}
	e.do(e.super, "GET", "/api/admin/audit?via=robot", nil, 400, nil)

	// Cursors: after_id reads forward, oldest first, and next_cursor
	// continues after the page.
	var first auditPage
	e.do(e.super, "GET", "/api/admin/audit?after_id=0&limit=2", nil, 200, &first)
	if len(first.Audit) != 2 || first.Audit[0].ID >= first.Audit[1].ID || first.NextCursor != first.Audit[1].ID {
		t.Fatalf("first forward page: %+v", first)
	}
	var next auditPage
	e.do(e.super, "GET", "/api/admin/audit?limit=2&after_id="+strconv.FormatInt(first.NextCursor, 10), nil, 200, &next)
	if len(next.Audit) == 0 || next.Audit[0].ID <= first.NextCursor {
		t.Fatalf("next forward page: %+v", next)
	}
}

// App MCP calls carry the app, its token and the client from initialize.
func TestAppMCPAttribution(t *testing.T) {
	e := newEnv(t)
	_, tok := e.app(map[string]any{"name": "agent"})
	ctx := context.Background()
	_, at, err := e.st.AppByToken(ctx, crypto.SHA256(tok))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	init := rpcReq{Method: "initialize", Params: json.RawMessage(`{"clientInfo":{"name":"cursor","version":"1.7"}}`)}
	actx := e.api.mcpAttribution(ctx, r, init, &store.App{ID: at.AppID, TokenID: at.ID})
	got := store.AttributionFrom(actx)
	if got.Via != store.ViaAppMCP || got.Client != "cursor/1.7" || got.TokenID != at.ID || got.AppID != at.AppID {
		t.Fatalf("initialize attribution %+v", got)
	}
	// Later calls with the same token remember the client.
	call := rpcReq{Method: "tools/call"}
	got = store.AttributionFrom(e.api.mcpAttribution(ctx, r, call, &store.App{ID: at.AppID, TokenID: at.ID}))
	if got.Client != "cursor/1.7" {
		t.Fatalf("tools/call attribution %+v", got)
	}
}

type eventPage struct {
	Events     []store.Event `json:"events"`
	NextCursor int64         `json:"next_cursor"`
}

func TestEventCursors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	appID, _ := e.app(map[string]any{"name": "a"})
	for i := range 5 {
		ne := store.NewEvent{Kind: "proxy", ServiceID: 1, StatusCode: 200, Detail: "call " + strconv.Itoa(i)}
		if i%2 == 0 {
			ne.AppID = appID
		}
		e.st.AddEvent(ctx, ne)
	}
	var newest eventPage
	e.do(e.super, "GET", "/api/admin/events?limit=2", nil, 200, &newest)
	if len(newest.Events) != 2 || newest.Events[0].ID < newest.Events[1].ID || newest.NextCursor != newest.Events[0].ID {
		t.Fatalf("newest page: %+v", newest)
	}
	var fwd eventPage
	e.do(e.super, "GET", "/api/admin/events?after_id=0&limit=3", nil, 200, &fwd)
	if len(fwd.Events) != 3 || fwd.Events[0].Detail != "call 0" || fwd.NextCursor != fwd.Events[2].ID {
		t.Fatalf("forward page: %+v", fwd)
	}
	var rest eventPage
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/events?after_id=%d", fwd.NextCursor), nil, 200, &rest)
	if len(rest.Events) != 2 || rest.Events[1].Detail != "call 4" {
		t.Fatalf("rest: %+v", rest)
	}
	var empty eventPage
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/events?after_id=%d", rest.NextCursor), nil, 200, &empty)
	if len(empty.Events) != 0 || empty.NextCursor != rest.NextCursor {
		t.Fatalf("caught up: %+v", empty)
	}
	var byApp eventPage
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/events?app_id=%d&since=1h", appID), nil, 200, &byApp)
	if len(byApp.Events) != 3 {
		t.Fatalf("app filter: %+v", byApp)
	}
	var older eventPage
	e.do(e.super, "GET", fmt.Sprintf("/api/admin/events?before_id=%d", fwd.Events[1].ID), nil, 200, &older)
	if len(older.Events) != 1 || older.Events[0].Detail != "call 0" {
		t.Fatalf("before_id: %+v", older)
	}
	e.do(e.super, "GET", "/api/admin/events?since=yesterday", nil, 400, nil)
}

// sseEvent is one event read from a stream.
type sseEvent struct {
	ID   int64
	Data map[string]any
}

// openStream opens an event stream and returns its events on a channel,
// closed when the stream ends.
func openStream(t *testing.T, e *env, c *http.Client, path string, hdr ...string) (<-chan sseEvent, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("Accept", "text/event-stream")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan sseEvent, 64)
	if resp.StatusCode != 200 {
		resp.Body.Close()
		close(ch)
		return ch, resp
	}
	t.Cleanup(func() { resp.Body.Close() })
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				cur.ID, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.Data)
			case line == ": ping":
				ch <- sseEvent{ID: -1}
			case line == "" && cur.Data != nil:
				ch <- cur
				cur = sseEvent{}
			}
		}
	}()
	return ch, resp
}

func next(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream ended")
			}
			if ev.ID == -1 {
				continue
			}
			return ev
		case <-time.After(3 * time.Second):
			t.Fatal("no event within 3s")
		}
	}
}

// runNotifier runs the notifier like main does and returns its stop.
func runNotifier(t *testing.T, e *env) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.api.Pool.Notify.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func TestAdminEventStream(t *testing.T) {
	e := newEnv(t)
	stop := runNotifier(t, e)
	ctx := context.Background()
	oldKeep := eventStreamKeepaliveNs.Load()
	eventStreamKeepaliveNs.Store(int64(50 * time.Millisecond))
	t.Cleanup(func() { eventStreamKeepaliveNs.Store(oldKeep) })

	ch, resp := openStream(t, e, e.super, "/api/admin/events/stream")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream answered %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// A heartbeat arrives while nothing happens.
	select {
	case ev := <-ch:
		if ev.ID != -1 {
			t.Fatalf("expected a heartbeat first, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat")
	}
	e.st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: 1, StatusCode: 200, Detail: "first"})
	first := next(t, ch)
	if first.Data["kind"] != "proxy" || first.Data["detail"] != "first" || first.ID == 0 {
		t.Fatalf("first event %+v", first)
	}
	// A notification reaches the stream as soon as it is recorded.
	e.api.Pool.Notify.Emit(notify.Event{Type: notify.KeyCooling, ServiceID: 1, KeyID: 9, Message: "resting",
		Data: map[string]any{"until": "2030-01-01T00:00:00Z"}})
	cool := next(t, ch)
	if cool.Data["kind"] != notify.KeyCooling || cool.Data["data"].(map[string]any)["until"] != "2030-01-01T00:00:00Z" {
		t.Fatalf("cooling event %+v", cool)
	}

	// Resume: Last-Event-ID replays what came after it.
	ch2, _ := openStream(t, e, e.super, "/api/admin/events/stream", "Last-Event-ID", strconv.FormatInt(first.ID, 10))
	if ev := next(t, ch2); ev.ID != cool.ID {
		t.Fatalf("resume sent %+v, want id %d", ev, cool.ID)
	}

	// The cap: one person may hold only so many streams.
	old := maxAppEventStreams
	maxAppEventStreams = 2
	t.Cleanup(func() { maxAppEventStreams = old })
	_, over := openStream(t, e, e.super, "/api/admin/events/stream")
	if over.StatusCode != 503 {
		t.Fatalf("third stream answered %d, want 503", over.StatusCode)
	}

	// Shutdown ends open streams.
	stop()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream still open after shutdown")
		}
	}
}

// An app's stream has its own events and resting notifications for the
// services it can use, never other apps' events, key labels or share news.
func TestAppEventStream(t *testing.T) {
	e := newEnv(t)
	runNotifier(t, e)
	ctx := context.Background()
	keys := e.addKeys(map[string]any{"secret": "k-stream", "label": "secret label"})
	var other store.Service
	e.do(e.super, "POST", "/api/admin/services", map[string]any{"provider": "serpapi", "slug": "other", "base_url": e.upURL}, 201, &other)
	appID, tok := e.app(map[string]any{"name": "mine", "allowed_services": []string{"serpapi"}})
	otherApp, _ := e.app(map[string]any{"name": "theirs"})

	start, _ := e.st.MaxEventID(ctx)
	e.st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: 1, KeyID: keys[0].ID, AppID: appID, StatusCode: 200, Detail: "mine"})
	e.st.AddEvent(ctx, store.NewEvent{Kind: "proxy", ServiceID: 1, AppID: otherApp, StatusCode: 200, Detail: "theirs"})
	e.st.AddEvent(ctx, store.NewEvent{Kind: notify.ShareUpdated, AppID: appID, Detail: "share news"})
	e.st.AddEvent(ctx, store.NewEvent{Kind: notify.KeyCooling, ServiceID: other.ID, KeyID: keys[0].ID, Detail: "other service"})
	e.st.AddEvent(ctx, store.NewEvent{Kind: notify.KeyCooling, ServiceID: 1, KeyID: keys[0].ID, Detail: "key secret label resting",
		Data: map[string]any{"until": "2030-01-01T00:00:00Z"}})
	e.st.AddEvent(ctx, store.NewEvent{Kind: notify.AppLimitNear, AppID: appID, Detail: "80%"})

	ch, resp := openStream(t, e, http.DefaultClient, "/api/v1/events/stream?after_id="+strconv.FormatInt(start, 10), "Authorization", "Bearer "+tok)
	if resp.StatusCode != 200 {
		t.Fatalf("app stream answered %d", resp.StatusCode)
	}
	got := []map[string]any{next(t, ch).Data, next(t, ch).Data, next(t, ch).Data}
	if got[0]["detail"] != "mine" || got[0]["key_label"] != nil {
		t.Fatalf("own event %+v", got[0])
	}
	if got[1]["kind"] != notify.KeyCooling || got[1]["detail"] != nil || got[1]["key_id"] != nil || got[1]["data"] == nil {
		t.Fatalf("cooling notice %+v", got[1])
	}
	if got[2]["kind"] != notify.AppLimitNear {
		t.Fatalf("limit notice %+v", got[2])
	}
	select {
	case ev := <-ch:
		if ev.ID != -1 {
			t.Fatalf("app stream sent more: %+v", ev)
		}
	case <-time.After(1500 * time.Millisecond):
	}
	// The admin stream is not for app tokens.
	_, r := openStream(t, e, http.DefaultClient, "/api/admin/events/stream", "Authorization", "Bearer "+tok)
	if r.StatusCode != 401 {
		t.Fatalf("admin stream with an app token answered %d", r.StatusCode)
	}
}

type jobView struct {
	Job store.Job `json:"job"`
}

func waitJob(t *testing.T, e *env, c *http.Client, url string, hdr ...string) store.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var v jobView
		e.do(c, "GET", url, nil, 200, &v, hdr...)
		if v.Job.Finished() {
			return v.Job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s", v.Job.Status)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestAsyncJobs(t *testing.T) {
	e := newEnv(t)
	runNotifier(t, e)
	var added struct {
		Keys           []store.Key `json:"keys"`
		CheckJobID     int64       `json:"check_job_id"`
		CheckStatusURL string      `json:"check_status_url"`
	}
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{"keys": []map[string]any{{"secret": "kj1"}, {"secret": "kj2"}}}, 201, &added)
	if added.CheckJobID == 0 || added.CheckStatusURL != "/api/admin/jobs/"+strconv.FormatInt(added.CheckJobID, 10) {
		t.Fatalf("add keys answer has no check job: %+v", added)
	}
	j := waitJob(t, e, e.super, added.CheckStatusURL)
	if j.Status != store.JobDone || j.Kind != "keys.check_new" || j.Done != 2 || j.Total != 2 || !strings.Contains(string(j.Result), `"checked":2`) {
		t.Fatalf("new keys job %s %d/%d %s", j.Status, j.Done, j.Total, j.Result)
	}

	// The synchronous call is unchanged.
	var sum checkSummary
	e.do(e.super, "POST", "/api/admin/keys/check-all", nil, 200, &sum)
	if sum.Checked != 2 {
		t.Fatalf("sync check-all %+v", sum)
	}

	var acc struct {
		JobID     int64     `json:"job_id"`
		Status    string    `json:"status"`
		StatusURL string    `json:"status_url"`
		Job       store.Job `json:"job"`
	}
	resp := e.do(e.super, "POST", "/api/admin/keys/check-all?async=1", nil, 202, &acc)
	if acc.JobID == 0 || resp.Header.Get("Location") != acc.StatusURL {
		t.Fatalf("async answer %+v, Location %q", acc, resp.Header.Get("Location"))
	}
	j = waitJob(t, e, e.super, acc.StatusURL)
	if j.Status != store.JobDone || j.Kind != "keys.check_all" || j.Via != store.ViaDashboard || !strings.Contains(string(j.Result), "Checked 2 keys") {
		t.Fatalf("check-all job %+v %s", j, j.Result)
	}

	// Prefer: respond-async on the service check; the route's {id} still
	// reaches the handler after the request was answered.
	resp = e.do(e.super, "POST", "/api/admin/services/1/check", nil, 202, &acc, "Prefer", "respond-async")
	if resp.Header.Get("Preference-Applied") != "respond-async" {
		t.Fatal("Preference-Applied missing")
	}
	if j = waitJob(t, e, e.super, acc.StatusURL); j.Status != store.JobDone || j.Target != "1" {
		t.Fatalf("service check job %+v %s", j, j.Result)
	}
	// A job whose call fails is failed with the message.
	e.do(e.super, "POST", "/api/admin/services/999/check?async=1", nil, 202, &acc)
	if j = waitJob(t, e, e.super, acc.StatusURL); j.Status != store.JobFailed || j.Error == "" {
		t.Fatalf("failed job %+v", j)
	}

	// Another person cannot read the job, nor list it.
	admin := e.adminClient()
	e.do(admin, "GET", acc.StatusURL, nil, 404, nil)
	var list struct {
		Jobs []store.Job `json:"jobs"`
	}
	e.do(admin, "GET", "/api/admin/jobs", nil, 200, &list)
	if len(list.Jobs) != 0 {
		t.Fatalf("admin sees root's jobs: %+v", list.Jobs)
	}
	e.do(e.super, "GET", "/api/admin/jobs", nil, 200, &list)
	if len(list.Jobs) != 4 || list.Jobs[0].Result != nil {
		t.Fatalf("root's jobs %+v", list.Jobs)
	}

	// Each finished job is a job.done event.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var evs eventPage
		e.do(e.super, "GET", "/api/admin/events?kind=job.done", nil, 200, &evs)
		if len(evs.Events) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job.done events: %d", len(evs.Events))
		}
		time.Sleep(30 * time.Millisecond)
	}
}
