package ctl

// Watching a server from the command line: events (with cursors and the
// live stream), the audit log, background jobs, and the demo data and Try it
// panel for a first call.

import (
	"bufio"
	"bytes"
	stdctx "context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

type eventRow struct {
	ID      int64     `json:"id"`
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Service string    `json:"service_slug"`
	Key     string    `json:"key_label"`
	App     string    `json:"app_name"`
	Status  int       `json:"status_code"`
	Latency int64     `json:"latency_ms"`
	Detail  string    `json:"detail"`
}

const eventHeader = "ID\tTIME\tKIND\tSERVICE\tKEY\tAPP\tSTATUS\tMS\tDETAIL"

func (e eventRow) line() string {
	return fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s", e.ID, e.TS.Local().Format("Jan 02 15:04:05"),
		clean(e.Kind), clean(e.Service), clean(e.Key), clean(e.App), e.Status, e.Latency, clip(e.Detail, 70))
}

// eventQuery reads the shared filters of acn events into a query.
func (c *cli) eventQuery(svc, app, token, kind string) (url.Values, error) {
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	if token != "" {
		if _, err := strconv.ParseInt(token, 10, 64); err != nil {
			return nil, usageErrorf("--token takes a token id; %s tokens list --app <app> shows them", c.name())
		}
		q.Set("token_id", token)
	}
	if svc != "" {
		sv, err := c.serviceBySlug(svc)
		if err != nil {
			return nil, err
		}
		q.Set("service_id", strconv.FormatInt(sv.ID, 10))
	}
	if app != "" {
		id, err := c.appID(app)
		if err != nil {
			return nil, err
		}
		q.Set("app_id", strconv.FormatInt(id, 10))
	}
	return q, nil
}

func (c *cli) events(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(c.err)
	svc := fs.String("service", "", "service slug")
	limit := fs.Int("limit", 30, "how many events")
	kind := fs.String("kind", "", "only this kind, for example rate_limited")
	app := fs.String("app", "", "only this app, by id or name")
	token := fs.String("token", "", "only this app token, by id")
	since := fs.String("since", "", "only events since: RFC 3339, Unix seconds, or a duration such as 15m or 24h")
	after := fs.String("after", "", "only events after this id, oldest first (the cursor a last run printed)")
	before := fs.String("before", "", "only events before this id, to read further back")
	follow := fs.Bool("follow", false, "keep printing new events as they happen (Ctrl-C stops)")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	q, err := c.eventQuery(*svc, *app, *token, *kind)
	if err != nil {
		return err
	}
	for name, v := range map[string]string{"after": *after, "before": *before} {
		if v == "" {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err != nil || n < 0 {
			return usageErrorf("--%s takes an event id", name)
		}
	}
	if *follow {
		if *since != "" || *before != "" {
			return usageErrorf("--follow starts now, or after --after <id>; it does not take --since or --before")
		}
		return c.followEvents(q, *after)
	}
	q.Set("limit", strconv.Itoa(*limit))
	for k, v := range map[string]string{"since": *since, "after_id": *after, "before_id": *before} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var out struct {
		Events     []eventRow `json:"events"`
		NextCursor int64      `json:"next_cursor"`
	}
	if done, err := c.callJSON("GET", "/api/admin/events?"+q.Encode(), &out); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, eventHeader)
	for _, e := range out.Events {
		fmt.Fprintln(tw, e.line())
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if out.NextCursor > 0 {
		fmt.Fprintf(c.err, "next: %s events --after %d\n", c.name(), out.NextCursor)
	}
	return nil
}

// followEvents reads the server sent event stream and prints each event as
// it comes: a table line, or with --json one JSON object per line. When the
// server closes the stream (after an hour, or on a restart) it reconnects
// from the last event it printed.
func (c *cli) followEvents(q url.Values, after string) error {
	ctx, err := c.current()
	if err != nil {
		return err
	}
	sctx, stop := signal.NotifyContext(stdctx.Background(), os.Interrupt)
	defer stop()
	cursor := after
	header := false
	for {
		cq := url.Values{}
		for k, v := range q {
			cq[k] = v
		}
		if cursor != "" {
			cq.Set("after_id", cursor)
		}
		req, err := http.NewRequestWithContext(sctx, http.MethodGet, ctx.URL+"/api/admin/events/stream?"+cq.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+ctx.Token)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("X-Requested-With", "acnologia-ctl")
		if pw := accessPasswordFor(ctx.URL); pw != "" {
			req.Header.Set(accessHeader, pw)
		}
		resp, err := c.httpClient(0).Do(req)
		if err != nil {
			if sctx.Err() != nil {
				return nil
			}
			return err
		}
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			var raw struct {
				Error map[string]any `json:"error"`
			}
			_ = json.Unmarshal(b, &raw)
			code, _ := raw.Error["code"].(string)
			msg, _ := raw.Error["message"].(string)
			return &httpError{Status: resp.StatusCode, Code: code, Message: msg, Fields: raw.Error}
		}
		if !header && !c.json {
			fmt.Fprintln(c.out, strings.ReplaceAll(eventHeader, "\t", "  "))
			header = true
		}
		last, err := c.readStream(resp.Body)
		resp.Body.Close()
		if last != "" {
			cursor = last
		}
		if sctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			fmt.Fprintln(c.err, "stream ended ("+clean(err.Error())+"); reconnecting")
		}
		select {
		case <-sctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
}

// readStream prints the events of one stream connection and returns the id
// of the last one.
func (c *cli) readStream(body io.Reader) (string, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var id string
	var data bytes.Buffer
	last := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data.Len() > 0 {
				c.printStreamEvent(data.Bytes())
				if id != "" {
					last = id
				}
			}
			id = ""
			data.Reset()
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return last, err
	}
	return last, io.EOF
}

func (c *cli) printStreamEvent(b []byte) {
	if c.json {
		var buf bytes.Buffer
		if json.Compact(&buf, b) == nil {
			buf.WriteByte('\n')
			_, _ = c.out.Write(cleanJSON(buf.Bytes()))
		}
		return
	}
	var e eventRow
	if json.Unmarshal(b, &e) != nil {
		return
	}
	fmt.Fprintln(c.out, strings.ReplaceAll(e.line(), "\t", "  "))
}

// ---- acn audit ----

func (c *cli) audit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(c.err)
	limit := fs.Int("limit", 50, "how many entries, up to 1000")
	via := fs.String("via", "", "how the change came in: dashboard, api, cli, control_mcp, app_mcp, assistant or oauth")
	token := fs.String("token", "", "only changes made with this token id (admin API token or app token)")
	app := fs.String("app", "", "only changes made by this app, by id or name")
	client := fs.String("client", "", "only changes whose client contains this text, such as acn/ or oauth-grant/<id>")
	action := fs.String("action", "", "only this action, such as key.create")
	svc := fs.String("service", "", "only changes to this service and its keys")
	since := fs.String("since", "", "only entries since: RFC 3339, Unix seconds, or a duration such as 24h")
	after := fs.String("after", "", "only entries after this id, oldest first")
	before := fs.String("before", "", "only entries before this id")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	q := url.Values{"limit": {strconv.Itoa(*limit)}}
	for k, v := range map[string]string{"via": *via, "token_id": *token, "client": *client, "action": *action,
		"since": *since, "after_id": *after, "before_id": *before} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if *app != "" {
		id, err := c.appID(*app)
		if err != nil {
			return err
		}
		q.Set("app_id", strconv.FormatInt(id, 10))
	}
	if *svc != "" {
		sv, err := c.serviceBySlug(*svc)
		if err != nil {
			return err
		}
		q.Set("service_id", strconv.FormatInt(sv.ID, 10))
	}
	var out struct {
		Audit []struct {
			ID     int64     `json:"id"`
			TS     time.Time `json:"ts"`
			User   string    `json:"user_email"`
			Action string    `json:"action"`
			Target string    `json:"target"`
			Detail string    `json:"detail"`
			Via    string    `json:"via"`
			Client string    `json:"client"`
		} `json:"audit"`
		NextCursor int64 `json:"next_cursor"`
	}
	if done, err := c.callJSON(http.MethodGet, "/api/admin/audit?"+q.Encode(), &out); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTIME\tUSER\tVIA\tCLIENT\tACTION\tTARGET\tDETAIL")
	for _, a := range out.Audit {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.TS.Local().Format("Jan 02 15:04:05"), clean(a.User), clean(orDash(a.Via)),
			clip(orDash(a.Client), 32), clean(a.Action), clip(a.Target, 40), clip(a.Detail, 60))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if out.NextCursor > 0 {
		fmt.Fprintf(c.err, "next: %s audit --after %d\n", c.name(), out.NextCursor)
	}
	return nil
}

// ---- acn jobs ----

type jobRow struct {
	ID       int64           `json:"id"`
	Kind     string          `json:"kind"`
	Target   string          `json:"target"`
	Status   string          `json:"status"`
	Done     int             `json:"done"`
	Total    int             `json:"total"`
	Result   json.RawMessage `json:"result"`
	Error    string          `json:"error"`
	By       string          `json:"created_by_email"`
	Via      string          `json:"via"`
	Created  time.Time       `json:"created_at"`
	Finished *time.Time      `json:"finished_at"`
}

func (j jobRow) finished() bool { return j.Status == "done" || j.Status == "failed" }

func (j jobRow) progress() string {
	if j.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", j.Done, j.Total)
}

func (c *cli) jobs(args []string) error {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	fs.SetOutput(c.err)
	kind := fs.String("kind", "", "only this kind, such as keys.check_all")
	status := fs.String("status", "", "only jobs with this status: queued, running, done or failed")
	limit := fs.Int("limit", 30, "how many jobs, up to 200")
	mine := fs.Bool("mine", false, "only jobs you started (a superadmin sees everyone's)")
	wait := fs.Bool("wait", false, "with a job id: wait until it ends")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("usage: %s jobs [--kind k] [--status s] [--mine], or %s jobs <id> [--wait]", c.name(), c.name())
	}
	if len(pos) == 1 {
		id, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil {
			return usageErrorf("the job id is a number")
		}
		return c.job(id, *wait)
	}
	if *wait {
		return usageErrorf("--wait needs a job id")
	}
	q := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *kind != "" {
		q.Set("kind", *kind)
	}
	if *status != "" {
		q.Set("status", *status)
	}
	if *mine {
		q.Set("mine", "1")
	}
	var out struct {
		Jobs []jobRow `json:"jobs"`
	}
	if done, err := c.callJSON(http.MethodGet, "/api/admin/jobs?"+q.Encode(), &out); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSTATUS\tPROGRESS\tSTARTED BY\tCREATED\tERROR")
	for _, j := range out.Jobs {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, clean(j.Kind), clean(orDash(j.Target)), clean(j.Status), j.progress(),
			clean(j.By), j.Created.Local().Format("Jan 02 15:04:05"), clip(j.Error, 60))
	}
	return tw.Flush()
}

// job prints one job, after waiting for it to end with wait.
func (c *cli) job(id int64, wait bool) error {
	path := fmt.Sprintf("/api/admin/jobs/%d", id)
	var raw json.RawMessage
	var out struct {
		Job jobRow `json:"job"`
	}
	for {
		if err := c.call(http.MethodGet, path, nil, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		if !wait || out.Job.finished() {
			break
		}
		time.Sleep(time.Second)
	}
	if c.json {
		return c.printJSON(raw)
	}
	j := out.Job
	fmt.Fprintf(c.out, "Job %d  %s %s: %s", j.ID, clean(j.Kind), clean(j.Target), clean(j.Status))
	if p := j.progress(); p != "-" {
		fmt.Fprintf(c.out, " (%s)", p)
	}
	fmt.Fprintln(c.out)
	if j.Error != "" {
		fmt.Fprintln(c.out, "Error:", clean(j.Error))
	}
	if len(j.Result) > 0 {
		var res struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(j.Result, &res) == nil && res.Message != "" {
			fmt.Fprintln(c.out, clean(res.Message))
		} else {
			return c.printJSON(j.Result)
		}
	}
	return nil
}

// printJobStarted reports a 202 job answer and how to follow it.
func (c *cli) printJobStarted(raw json.RawMessage) error {
	if c.json {
		return c.printJSON(raw)
	}
	var acc struct {
		JobID  int64  `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &acc); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Started job %d (%s); follow it with: %s jobs %d --wait\n", acc.JobID, clean(acc.Status), c.name(), acc.JobID)
	return nil
}

// ---- acn keys check ----

func (c *cli) keysCheck(args []string) error {
	fs := flag.NewFlagSet("keys check", flag.ContinueOnError)
	fs.SetOutput(c.err)
	all := fs.Bool("all", false, "check every key of every service")
	async := fs.Bool("async", false, "start a background job and return at once")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if (*all && len(pos) != 0) || (!*all && len(pos) != 1) {
		return usageErrorf("usage: %s keys check <service> [--async], or %s keys check --all [--async]", c.name(), c.name())
	}
	path := "/api/admin/keys/check-all"
	if !*all {
		sv, err := c.serviceBySlug(pos[0])
		if err != nil {
			return err
		}
		path = fmt.Sprintf("/api/admin/services/%d/check", sv.ID)
	}
	if *async {
		path += "?async=1"
	}
	var raw json.RawMessage
	if err := c.call("POST", path, nil, &raw); err != nil {
		return err
	}
	if *async {
		return c.printJobStarted(raw)
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if msg, ok := out["message"].(string); ok && msg != "" {
		fmt.Fprintln(c.out, clean(msg))
		return nil
	}
	return c.printJSON(raw)
}

// ---- acn demo ----

func (c *cli) demo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(c.err)
	force := fs.Bool("force", false, "load it even though the server has services")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	sub := "status"
	if len(pos) == 1 {
		sub = pos[0]
	}
	if len(pos) > 1 || (sub != "status" && sub != "load" && sub != "remove") {
		return usageErrorf("usage: %[1]s demo [status], %[1]s demo load [--force], or %[1]s demo remove", c.name())
	}
	var raw json.RawMessage
	switch sub {
	case "status":
		if err := c.call(http.MethodGet, "/api/admin/demo", nil, &raw); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(raw)
		}
		var st struct {
			Loaded bool   `json:"loaded"`
			Slug   string `json:"service_slug"`
			AppID  int64  `json:"app_id"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		if !st.Loaded {
			fmt.Fprintf(c.out, "No demo data. %s demo load adds a demo service, two keys and an app.\n", c.name())
			return nil
		}
		fmt.Fprintf(c.out, "Demo data is loaded: service %s, app %d.\n", clean(st.Slug), st.AppID)
	case "load":
		if err := c.call(http.MethodPost, "/api/admin/demo", map[string]any{"force": *force}, &raw); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(raw)
		}
		var out struct {
			Service struct {
				Slug string `json:"slug"`
			} `json:"service"`
			App struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"app"`
			Token string `json:"token"`
			Try   struct {
				Curl string `json:"curl"`
			} `json:"try"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "Loaded demo service %s and app %s (id %d).\nToken (shown once): %s\nTry it: %s\n",
			clean(out.Service.Slug), clean(out.App.Name), out.App.ID, clean(out.Token), clean(out.Try.Curl))
	case "remove":
		if err := c.call(http.MethodDelete, "/api/admin/demo", nil, &raw); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(raw)
		}
		fmt.Fprintln(c.out, "Removed the demo data.")
	}
	return nil
}

// ---- acn try ----

func (c *cli) try(args []string) error {
	fs := flag.NewFlagSet("try", flag.ContinueOnError)
	fs.SetOutput(c.err)
	data := fs.String("data", "", "JSON body: inline, @file, or @- for stdin")
	app := fs.String("app", "", "run as this app, by id or name (default: the internal Try it app)")
	readOnly := fs.Bool("read-only", false, "run as a read-only token would: GET only")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	usage := usageErrorf("usage: %s try <service> [METHOD] <path> [--data json|@file|@-] [--app id|name] [--read-only]", c.name())
	var method, path string
	switch len(pos) {
	case 2:
		method, path = http.MethodGet, pos[1]
		if *data != "" {
			method = http.MethodPost
		}
	case 3:
		method, path = strings.ToUpper(pos[1]), pos[2]
	default:
		return usage
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return usageErrorf("method must be GET, POST, PUT, PATCH or DELETE")
	}
	sv, err := c.serviceBySlug(pos[0])
	if err != nil {
		return err
	}
	body := map[string]any{"method": method, "path": path, "read_only": *readOnly}
	if *data != "" {
		raw, err := c.readData(*data)
		if err != nil {
			return err
		}
		body["body"] = string(raw)
	}
	if *app != "" {
		id, err := c.appID(*app)
		if err != nil {
			return err
		}
		body["app_id"] = id
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPost, fmt.Sprintf("/api/admin/services/%d/try", sv.ID), body, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out struct {
		Status    int    `json:"status"`
		Latency   int64  `json:"latency_ms"`
		Body      string `json:"body"`
		Truncated bool   `json:"body_truncated"`
		App       struct {
			Name     string `json:"name"`
			ReadOnly bool   `json:"read_only"`
		} `json:"app"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	as := clean(out.App.Name)
	if out.App.ReadOnly {
		as += ", read only"
	}
	fmt.Fprintf(c.err, "%d %s in %d ms as %s\n", out.Status, http.StatusText(out.Status), out.Latency, as)
	b := []byte(out.Body)
	if json.Valid(b) {
		return c.printJSON(b)
	}
	fmt.Fprintln(c.out, clean(out.Body))
	if out.Truncated {
		fmt.Fprintln(c.err, "(the body was cut at 256 KB)")
	}
	return nil
}
