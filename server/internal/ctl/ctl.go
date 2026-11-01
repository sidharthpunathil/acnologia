// Package ctl is the `cogito ctl` command line client. It drives a local or
// remote Acnologia through the admin API with a cogadm_ token, so CI jobs and
// scripts can add keys, check them and watch the pool without a browser.
package ctl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const usage = `cogito ctl: manage a Acnologia server from the command line

Connect once, then run commands against that server:
  cogito ctl login <url> --token cogadm_...   [--name prod]
  cogito ctl contexts                          list saved servers
  cogito ctl use <name>                        switch the current server

Commands:
  status                         pool overview
  services                       services with key counts
  keys <service>                 keys of a service and their state
  keys add <service> [file]      add keys, one per line: secret or label,secret (stdin when no file)
  keys check <service>           health check every key of a service
  events [--service s] [--limit n] [--kind k]
                                 recent activity
  breaker <service> [--reset]    circuit breaker state, or close it
  cache purge <service>          drop cached responses of a service
  usage <app-id>                 an app's requests, spend and limits

Create a token in the dashboard under Settings, API tokens. COGITO_URL and
COGITO_TOKEN override the saved server for one command.
`

// Run executes a ctl command and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &cli{in: stdin, out: stdout, err: stderr}
	if err := c.run(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

type cli struct {
	in       io.Reader
	out, err io.Writer
	cfgPath  string
	http     *http.Client
}

type context struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type configFile struct {
	Current  string             `json:"current"`
	Contexts map[string]context `json:"contexts"`
}

func (c *cli) configPath() string {
	if c.cfgPath != "" {
		return c.cfgPath
	}
	if p := os.Getenv("COGITO_CTL_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "cogito", "ctl.json")
}

func (c *cli) load() configFile {
	cf := configFile{Contexts: map[string]context{}}
	b, err := os.ReadFile(c.configPath())
	if err == nil {
		_ = json.Unmarshal(b, &cf)
	}
	if cf.Contexts == nil {
		cf.Contexts = map[string]context{}
	}
	return cf
}

func (c *cli) save(cf configFile) error {
	p := c.configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cf, "", "  ")
	return os.WriteFile(p, b, 0o600) // holds tokens
}

func (c *cli) current() (context, error) {
	if u, t := os.Getenv("COGITO_URL"), os.Getenv("COGITO_TOKEN"); u != "" && t != "" {
		return context{URL: strings.TrimRight(u, "/"), Token: t}, nil
	}
	cf := c.load()
	ctx, ok := cf.Contexts[cf.Current]
	if !ok {
		return context{}, errors.New("not logged in; run: cogito ctl login <url> --token cogadm_...")
	}
	return ctx, nil
}

func (c *cli) run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(c.out, usage)
		return nil
	}
	switch args[0] {
	case "login":
		return c.login(args[1:])
	case "contexts":
		return c.contexts()
	case "use":
		return c.use(args[1:])
	case "status":
		return c.status()
	case "services":
		return c.services()
	case "keys":
		if len(args) > 1 && args[1] == "add" {
			return c.keysAdd(args[2:])
		}
		if len(args) > 1 && args[1] == "check" {
			return c.keysCheck(args[2:])
		}
		return c.keys(args[1:])
	case "events":
		return c.events(args[1:])
	case "breaker":
		return c.breaker(args[1:])
	case "cache":
		if len(args) > 2 && args[1] == "purge" {
			return c.cachePurge(args[2])
		}
		return errors.New("usage: cogito ctl cache purge <service>")
	case "usage":
		return c.usage(args[1:])
	}
	return fmt.Errorf("unknown command %q; run cogito ctl help", args[0])
}

// ---- HTTP ----

type apiErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *cli) call(method, path string, body, out any) error {
	ctx, err := c.current()
	if err != nil {
		return err
	}
	return c.callWith(ctx, method, path, body, out)
}

func (c *cli) callWith(ctx context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ctx.URL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ctx.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "cogito-ctl")
	hc := c.http
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 400 {
		var e apiErr
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			return fmt.Errorf("%s (%d %s)", e.Error.Message, resp.StatusCode, e.Error.Code)
		}
		return fmt.Errorf("server answered %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// ---- commands ----

func (c *cli) login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(c.err)
	token := fs.String("token", os.Getenv("COGITO_TOKEN"), "admin API token (cogadm_...)")
	name := fs.String("name", "default", "name for this server")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || *token == "" {
		return errors.New("usage: cogito ctl login <url> --token cogadm_... [--name prod]")
	}
	u := strings.TrimRight(pos[0], "/")
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	if _, err := url.Parse(u); err != nil {
		return fmt.Errorf("bad url: %v", err)
	}
	ctx := context{URL: u, Token: *token}
	var me struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := c.callWith(ctx, "GET", "/api/admin/me", nil, &me); err != nil {
		return fmt.Errorf("could not sign in to %s: %w", u, err)
	}
	cf := c.load()
	cf.Contexts[*name] = ctx
	cf.Current = *name
	if err := c.save(cf); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Signed in to %s as %s (%s). Saved as %q.\n", u, me.Email, me.Role, *name)
	return nil
}

func (c *cli) contexts() error {
	cf := c.load()
	names := make([]string, 0, len(cf.Contexts))
	for n := range cf.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\tNAME\tURL")
	for _, n := range names {
		mark := ""
		if n == cf.Current {
			mark = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", mark, n, cf.Contexts[n].URL)
	}
	return tw.Flush()
}

func (c *cli) use(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cogito ctl use <name>")
	}
	cf := c.load()
	if _, ok := cf.Contexts[args[0]]; !ok {
		return fmt.Errorf("no saved server named %q", args[0])
	}
	cf.Current = args[0]
	if err := c.save(cf); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Now using %s.\n", args[0])
	return nil
}

func (c *cli) status() error {
	var ov struct {
		Services    int            `json:"services"`
		Keys        map[string]int `json:"keys"`
		InFlight    int            `json:"in_flight"`
		Requests24h int            `json:"requests_24h"`
		RateLimits  int            `json:"rate_limits_24h"`
		CacheHits   int            `json:"cache_hits_24h"`
		Spend       float64        `json:"spend_24h"`
	}
	if err := c.call("GET", "/api/admin/overview", nil, &ov); err != nil {
		return err
	}
	k := ov.Keys
	fmt.Fprintf(c.out, "Services   %d\n", ov.Services)
	fmt.Fprintf(c.out, "Keys       %d total, %d available, %d cooling, %d exhausted, %d invalid, %d banned, %d disabled\n",
		k["total"], k["active"], k["cooling"], k["exhausted"], k["invalid"], k["banned"], k["disabled"])
	fmt.Fprintf(c.out, "In flight  %d\n", ov.InFlight)
	fmt.Fprintf(c.out, "Last 24h   %d requests, %d rate limits, %d cache hits, $%.2f spend\n", ov.Requests24h, ov.RateLimits, ov.CacheHits, ov.Spend)
	return nil
}

type service struct {
	ID        int64          `json:"id"`
	Slug      string         `json:"slug"`
	Name      string         `json:"name"`
	Provider  string         `json:"provider"`
	KeyCounts map[string]int `json:"key_counts"`
	Requests  int            `json:"requests_24h"`
}

func (c *cli) listServices() ([]service, error) {
	var out struct {
		Services []service `json:"services"`
	}
	err := c.call("GET", "/api/admin/services", nil, &out)
	return out.Services, err
}

func (c *cli) serviceBySlug(slug string) (service, error) {
	svcs, err := c.listServices()
	if err != nil {
		return service{}, err
	}
	for _, s := range svcs {
		if s.Slug == slug {
			return s, nil
		}
	}
	return service{}, fmt.Errorf("no service named %q", slug)
}

func (c *cli) services() error {
	svcs, err := c.listServices()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tPROVIDER\tKEYS\tAVAILABLE\tCOOLING\tEXHAUSTED\tINVALID\tREQ 24H")
	for _, s := range svcs {
		k := s.KeyCounts
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n", s.Slug, s.Provider, k["total"], k["available"], k["cooling"], k["exhausted"], k["invalid"]+k["banned"], s.Requests)
	}
	return tw.Flush()
}

func (c *cli) keys(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cogito ctl keys <service>")
	}
	sv, err := c.serviceBySlug(args[0])
	if err != nil {
		return err
	}
	var out struct {
		Keys []struct {
			ID        int64      `json:"id"`
			Label     string     `json:"label"`
			Hint      string     `json:"hint"`
			Status    string     `json:"effective_status"`
			Remaining *int64     `json:"quota_remaining"`
			Limit     *int64     `json:"quota_limit"`
			Today     int64      `json:"usage_today"`
			LastError string     `json:"last_error"`
			Cooldown  *time.Time `json:"cooldown_until"`
		} `json:"keys"`
	}
	if err := c.call("GET", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), nil, &out); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLABEL\tKEY\tSTATUS\tQUOTA\tTODAY\tNOTE")
	for _, k := range out.Keys {
		q := "-"
		if k.Remaining != nil {
			q = strconv.FormatInt(*k.Remaining, 10)
			if k.Limit != nil {
				q += "/" + strconv.FormatInt(*k.Limit, 10)
			}
		}
		note := k.LastError
		if k.Status == "cooling" && k.Cooldown != nil {
			note = "back in " + time.Until(*k.Cooldown).Round(time.Second).String()
		}
		if len(note) > 60 {
			note = note[:57] + "..."
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%s\n", k.ID, k.Label, k.Hint, k.Status, q, k.Today, note)
	}
	return tw.Flush()
}

func (c *cli) keysAdd(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: cogito ctl keys add <service> [file]")
	}
	sv, err := c.serviceBySlug(args[0])
	if err != nil {
		return err
	}
	in := c.in
	if len(args) == 2 && args[1] != "-" {
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	keys := []map[string]any{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k := map[string]any{"secret": line}
		if label, secret, ok := strings.Cut(line, ","); ok {
			k = map[string]any{"label": strings.TrimSpace(label), "secret": strings.TrimSpace(secret)}
		}
		keys = append(keys, k)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("no keys to add")
	}
	var out struct {
		Keys    []any `json:"keys"`
		Skipped []any `json:"skipped"`
	}
	if err := c.call("POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"keys": keys}, &out); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Added %d keys to %s", len(out.Keys), sv.Slug)
	if len(out.Skipped) > 0 {
		fmt.Fprintf(c.out, ", skipped %d duplicates", len(out.Skipped))
	}
	fmt.Fprintln(c.out, ".")
	return nil
}

func (c *cli) keysCheck(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cogito ctl keys check <service>")
	}
	sv, err := c.serviceBySlug(args[0])
	if err != nil {
		return err
	}
	var out map[string]any
	if err := c.call("POST", fmt.Sprintf("/api/admin/services/%d/check", sv.ID), nil, &out); err != nil {
		return err
	}
	if msg, ok := out["message"].(string); ok && msg != "" {
		fmt.Fprintln(c.out, msg)
		return nil
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(b))
	return nil
}

func (c *cli) events(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(c.err)
	svc := fs.String("service", "", "service slug")
	limit := fs.Int("limit", 30, "how many events")
	kind := fs.String("kind", "", "only this kind, for example rate_limited")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	q := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *kind != "" {
		q.Set("kind", *kind)
	}
	if *svc != "" {
		sv, err := c.serviceBySlug(*svc)
		if err != nil {
			return err
		}
		q.Set("service_id", strconv.FormatInt(sv.ID, 10))
	}
	var out struct {
		Events []struct {
			TS      time.Time `json:"ts"`
			Kind    string    `json:"kind"`
			Service string    `json:"service_slug"`
			Key     string    `json:"key_label"`
			App     string    `json:"app_name"`
			Status  int       `json:"status_code"`
			Latency int64     `json:"latency_ms"`
			Detail  string    `json:"detail"`
		} `json:"events"`
	}
	if err := c.call("GET", "/api/admin/events?"+q.Encode(), nil, &out); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tKIND\tSERVICE\tKEY\tAPP\tSTATUS\tMS\tDETAIL")
	for _, e := range out.Events {
		d := e.Detail
		if len(d) > 70 {
			d = d[:67] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", e.TS.Local().Format("Jan 02 15:04:05"), e.Kind, e.Service, e.Key, e.App, e.Status, e.Latency, d)
	}
	return tw.Flush()
}

func (c *cli) breaker(args []string) error {
	fs := flag.NewFlagSet("breaker", flag.ContinueOnError)
	fs.SetOutput(c.err)
	reset := fs.Bool("reset", false, "close the breaker now")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: cogito ctl breaker <service> [--reset]")
	}
	sv, err := c.serviceBySlug(pos[0])
	if err != nil {
		return err
	}
	var out struct {
		Status struct {
			State      string  `json:"state"`
			Failures   int     `json:"failures"`
			RetryAfter float64 `json:"retry_after_sec"`
			Reason     string  `json:"reason"`
		} `json:"status"`
		Threshold int     `json:"threshold"`
		Window    float64 `json:"window_sec"`
	}
	method, path := "GET", fmt.Sprintf("/api/admin/services/%d/breaker", sv.ID)
	if *reset {
		method, path = "POST", path+"/reset"
	}
	if err := c.call(method, path, nil, &out); err != nil {
		return err
	}
	st := out.Status
	fmt.Fprintf(c.out, "%s: %s", sv.Slug, strings.ReplaceAll(st.State, "_", " "))
	if st.State == "open" {
		fmt.Fprintf(c.out, ", next probe in %.0fs (%s)", st.RetryAfter, st.Reason)
	}
	if out.Threshold > 0 {
		fmt.Fprintf(c.out, "; opens after %d provider errors in %.0fs", out.Threshold, out.Window)
	} else {
		fmt.Fprint(c.out, "; breaker turned off for this provider")
	}
	fmt.Fprintln(c.out)
	return nil
}

func (c *cli) cachePurge(slug string) error {
	sv, err := c.serviceBySlug(slug)
	if err != nil {
		return err
	}
	var out struct {
		Purged int `json:"purged"`
	}
	if err := c.call("POST", fmt.Sprintf("/api/admin/services/%d/cache/purge", sv.ID), nil, &out); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Dropped %d cached responses for %s.\n", out.Purged, sv.Slug)
	return nil
}

func (c *cli) usage(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cogito ctl usage <app-id>")
	}
	var out struct {
		Usage struct {
			TodayRequests int64   `json:"today_requests"`
			TodayCost     float64 `json:"today_cost"`
			MonthRequests int64   `json:"month_requests"`
			MonthCost     float64 `json:"month_cost"`
			TodayHits     int64   `json:"today_cache_hits"`
		} `json:"usage"`
		Limits struct {
			DailyRequests   *int64   `json:"daily_requests"`
			MonthlyRequests *int64   `json:"monthly_requests"`
			DailyBudget     *float64 `json:"daily_budget"`
			MonthlyBudget   *float64 `json:"monthly_budget"`
		} `json:"limits"`
		LimitHit *struct {
			Limit   string    `json:"limit"`
			ResetAt time.Time `json:"reset_at"`
		} `json:"limit_hit"`
	}
	if err := c.call("GET", "/api/admin/apps/"+url.PathEscape(args[0])+"/usage", nil, &out); err != nil {
		return err
	}
	u, l := out.Usage, out.Limits
	of := func(n *int64) string {
		if n == nil {
			return ""
		}
		return " of " + strconv.FormatInt(*n, 10)
	}
	ofUSD := func(n *float64) string {
		if n == nil {
			return ""
		}
		return fmt.Sprintf(" of $%.2f", *n)
	}
	fmt.Fprintf(c.out, "Today       %d requests%s, $%.4f%s, %d cache hits\n", u.TodayRequests, of(l.DailyRequests), u.TodayCost, ofUSD(l.DailyBudget), u.TodayHits)
	fmt.Fprintf(c.out, "This month  %d requests%s, $%.4f%s\n", u.MonthRequests, of(l.MonthlyRequests), u.MonthCost, ofUSD(l.MonthlyBudget))
	if out.LimitHit != nil {
		fmt.Fprintf(c.out, "Blocked     %s limit reached, resets %s\n", strings.ReplaceAll(out.LimitHit.Limit, "_", " "), out.LimitHit.ResetAt.Local().Format(time.RFC1123))
	}
	return nil
}

// parseInterleaved lets flags come before or after positional arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
