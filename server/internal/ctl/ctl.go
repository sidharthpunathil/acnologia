// Package ctl is the `acnologia ctl` command line client. It drives a local or
// remote Acnologia through the admin API with a acnadm_ token, so CI jobs and
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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
)

// usage is the help text; %[1]s is the program name ("acn" or
// "acnologia ctl").
const usage = `%[1]s: manage an Acnologia server from the command line

Sign in once, in the browser, then run commands against that server:
  %[1]s login [--domain https://key.example.com] [--no-browser]
                                 sign in through the browser (asks for the URL)
  %[1]s login <url> --token acnadm_... [--name prod]
                                 sign in with a token you already have
  %[1]s logout [host]            revoke this CLI's token and forget the server
  %[1]s whoami                   who the saved token acts as
  %[1]s contexts                 list saved servers
  %[1]s use <host>               switch the default server

Commands:
  status                         server version and pool overview
  services                       services with key counts
  keys <service>                 keys of a service and their state
  keys add <service> [file]      add keys, one per line: secret or label,secret (stdin when no file)
  keys check <service>           health check every key of a service
  events [--service s] [--limit n] [--kind k]
                                 recent activity
  breaker <service> [--reset]    circuit breaker state, or close it
  cache purge <service>          drop cached responses of a service
  usage <app-id>                 an app's requests, spend and limits

--json prints the server's JSON instead of a table (whoami, status,
services, keys, events, usage). ACNOLOGIA_URL and ACNOLOGIA_TOKEN override
the saved server for one command, for CI.
`

// Run executes a ctl command as `acnologia ctl` and returns the process exit
// code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunAs("acnologia ctl", args, stdin, stdout, stderr)
}

// RunAs is Run with the program name used in help and hints, so the
// standalone acn binary says "acn login" where the server says
// "acnologia ctl login".
func RunAs(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &cli{prog: prog, in: stdin, out: stdout, err: stderr}
	return c.exec(args)
}

func (c *cli) exec(args []string) int {
	stderr := c.err
	if err := c.run(args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

type cli struct {
	prog     string
	in       io.Reader
	out, err io.Writer
	cfgPath  string
	http     *http.Client
	// json prints raw server JSON (--json).
	json bool
	// Hooks for tests: the browser opener, the poll interval (overrides
	// the server's), the sleep between polls and the machine name.
	openBrowser func(url string) error
	pollEvery   time.Duration
	sleep       func(time.Duration)
	hostname    func() (string, error)
}

// context is one saved server. Logins through the browser also record who
// the token acts as, its scope and its id (so logout can revoke it).
type context struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	User    string `json:"user,omitempty"`
	Scope   string `json:"scope,omitempty"`
	TokenID int64  `json:"token_id,omitempty"`
}

type configFile struct {
	Current  string             `json:"current"`
	Contexts map[string]context `json:"contexts"`
}

func (c *cli) configPath() string {
	if c.cfgPath != "" {
		return c.cfgPath
	}
	if p := config.Getenv("ACNOLOGIA_CTL_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "acnologia", "ctl.json")
}

// readPath is where saved servers are read from. When the config lives in the
// default place and has not been written yet, the file from before the rename
// (~/.config/cogito/ctl.json) is read instead. Saves always go to configPath.
func (c *cli) readPath() string {
	p := c.configPath()
	if c.cfgPath != "" || config.Getenv("ACNOLOGIA_CTL_CONFIG") != "" {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	legacy := filepath.Join(filepath.Dir(filepath.Dir(p)), "cogito", "ctl.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return p
}

func (c *cli) load() configFile {
	cf := configFile{Contexts: map[string]context{}}
	b, err := os.ReadFile(c.readPath())
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
	if u, t := config.Getenv("ACNOLOGIA_URL"), config.Getenv("ACNOLOGIA_TOKEN"); u != "" && t != "" {
		return context{URL: strings.TrimRight(u, "/"), Token: t}, nil
	}
	cf := c.load()
	ctx, ok := cf.Contexts[cf.Current]
	if !ok {
		return context{}, fmt.Errorf("not logged in; run: %s login", c.name())
	}
	return ctx, nil
}

func (c *cli) name() string {
	if c.prog == "" {
		return "acnologia ctl"
	}
	return c.prog
}

func (c *cli) run(args []string) error {
	// --json may sit anywhere on the line.
	rest := args[:0:0]
	for _, a := range args {
		if a == "--json" || a == "-json" {
			c.json = true
			continue
		}
		rest = append(rest, a)
	}
	args = rest
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(c.out, usage, c.name())
		return nil
	}
	switch args[0] {
	case "login":
		return c.login(args[1:])
	case "logout":
		return c.logout(args[1:])
	case "whoami":
		return c.whoami()
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
		return fmt.Errorf("usage: %s cache purge <service>", c.name())
	case "usage":
		return c.usage(args[1:])
	}
	return fmt.Errorf("unknown command %q; run %s help", args[0], c.name())
}

// ---- HTTP ----

type apiErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	// Interval comes with slow_down on the device poll.
	Interval int `json:"interval"`
}

// httpError is a server answer of 400 or more.
type httpError struct {
	Status   int
	Code     string
	Message  string
	Interval int
}

func (e *httpError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (%d %s)", e.Message, e.Status, e.Code)
	}
	return fmt.Sprintf("server answered %d", e.Status)
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
	if ctx.Token != "" {
		req.Header.Set("Authorization", "Bearer "+ctx.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "acnologia-ctl")
	// A server behind the access gate wants its password on every
	// dashboard call; it is read from the environment, never saved.
	if pw := config.Getenv("ACNOLOGIA_ACCESS_PASSWORD"); pw != "" {
		req.Header.Set("X-Acnologia-Access", pw)
	}
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
		_ = json.Unmarshal(b, &e)
		return &httpError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message, Interval: e.Interval}
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], b...)
		return nil
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
	token := fs.String("token", "", "admin API token (acnadm_...); without it, sign in through the browser")
	name := fs.String("name", "", "name for this server (default: its host)")
	domain := fs.String("domain", "", "the server's URL, for example https://key.example.com")
	noBrowser := fs.Bool("no-browser", false, "print the link instead of opening a browser")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 || (len(pos) == 1 && *domain != "") {
		return fmt.Errorf("usage: %s login [--domain <url>] [--no-browser], or %s login <url> --token acnadm_...", c.name(), c.name())
	}
	raw := *domain
	if len(pos) == 1 {
		raw = pos[0]
	}
	if raw == "" {
		if raw, err = c.ask("Acnologia URL (for example https://key.sidharth.co): "); err != nil {
			return err
		}
	}
	u, err := NormalizeURL(raw)
	if err != nil {
		return err
	}
	if *token != "" {
		return c.loginToken(u, *token, *name)
	}
	return c.loginDevice(u, *name, *noBrowser)
}

// loginToken saves a token made in the dashboard, after checking it.
func (c *cli) loginToken(u, token, name string) error {
	ctx := context{URL: u, Token: token}
	var me struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := c.callWith(ctx, "GET", "/api/admin/me", nil, &me); err != nil {
		return fmt.Errorf("could not sign in to %s: %w", u, err)
	}
	ctx.User = me.Email
	if name == "" {
		name = hostOf(u)
	}
	if err := c.remember(name, ctx); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Signed in to %s as %s (%s). Saved as %q.\n", u, me.Email, me.Role, name)
	return nil
}

func (c *cli) remember(name string, ctx context) error {
	cf := c.load()
	cf.Contexts[name] = ctx
	cf.Current = name
	return c.save(cf)
}

// ask prints prompt and reads one line from stdin.
func (c *cli) ask(prompt string) (string, error) {
	fmt.Fprint(c.err, prompt)
	line, err := bufio.NewReader(c.in).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return "", fmt.Errorf("no URL given; pass it with %s login --domain https://key.example.com", c.name())
	}
	return line, nil
}

// NormalizeURL turns what someone typed into the server's base URL: a bare
// host gets https://, the trailing slash goes, and plain http is allowed
// only for localhost.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("the URL is empty")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("%q is not a URL like https://key.example.com", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch u.Scheme {
	case "https":
	case "http":
		if !isLocalHost(u.Hostname()) {
			return "", fmt.Errorf("%s uses plain http; use https (http is allowed only for localhost)", u.Host)
		}
	default:
		return "", fmt.Errorf("%q: the URL must start with https://", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q: give just the address, like https://key.example.com", raw)
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func isLocalHost(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// hostOf is the name a server is saved under: its host and port.
func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return p.Host + strings.TrimRight(p.Path, "/")
}

type deviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceGrant struct {
	Token   string `json:"token"`
	TokenID int64  `json:"token_id"`
	Scope   string `json:"scope"`
	User    struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

// loginDevice signs in through the browser: the server hands out a short
// code, the person approves it on the website, and the CLI picks up a token
// made for it. The CLI never sees a password, the site's or the account's.
func (c *cli) loginDevice(u, name string, noBrowser bool) error {
	anon := context{URL: u}
	var health struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := c.callWith(anon, "GET", "/healthz", nil, &health); err != nil || !health.OK {
		if err == nil {
			err = errors.New("unexpected answer")
		}
		return fmt.Errorf("%s does not look like an Acnologia server (GET /healthz: %v)", u, err)
	}
	var gate struct {
		Required bool `json:"required"`
		Locked   bool `json:"locked"`
	}
	if err := c.callWith(anon, "GET", "/api/gate", nil, &gate); err != nil {
		return fmt.Errorf("%s: GET /api/gate: %w", u, err)
	}

	hn := "unknown host"
	hostname := c.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	if h, err := hostname(); err == nil && h != "" {
		hn = h
	}
	var st deviceStart
	err := c.callWith(anon, "POST", "/api/cli/device", map[string]string{
		"client": "acn", "hostname": hn, "os": runtime.GOOS + "/" + runtime.GOARCH,
	}, &st)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) && he.Status == 404 {
			return fmt.Errorf("%s is too old for browser sign in; update it, or use %s login %s --token acnadm_...", u, c.name(), u)
		}
		return fmt.Errorf("could not start the sign in: %w", err)
	}
	link := st.VerificationURIComplete
	if link == "" {
		link = st.VerificationURI + "?code=" + url.QueryEscape(st.UserCode)
	}

	fmt.Fprintf(c.out, "Your one-time code: %s\n", st.UserCode)
	if gate.Required && gate.Locked {
		fmt.Fprintln(c.out, "This server is behind a site password: unlock it in the browser, then sign in.")
	}
	opened := false
	if !noBrowser {
		open := c.openBrowser
		if open == nil {
			open = openBrowser
		}
		opened = open(link) == nil
	}
	if opened {
		fmt.Fprintf(c.out, "Opened %s in your browser.\nCheck the code matches, then approve.\n", link)
	} else {
		fmt.Fprintf(c.out, "Open this link in a browser, check the code matches, then approve:\n  %s\n", link)
	}
	fmt.Fprintln(c.out, "Waiting for approval...")

	g, err := c.pollDevice(anon, st)
	if err != nil {
		return err
	}
	ctx := context{URL: u, Token: g.Token, User: g.User.Email, Scope: g.Scope, TokenID: g.TokenID}
	if name == "" {
		name = hostOf(u)
	}
	if err := c.remember(name, ctx); err != nil {
		return fmt.Errorf("signed in, but could not save the token: %w", err)
	}
	fmt.Fprintf(c.out, "Signed in to %s as %s (%s)\n", hostOf(u), g.User.Email, g.Scope)
	return nil
}

// pollDevice waits for the browser's answer, at the server's interval.
func (c *cli) pollDevice(anon context, st deviceStart) (deviceGrant, error) {
	interval := time.Duration(max(st.Interval, 1)) * time.Second
	if c.pollEvery > 0 {
		interval = c.pollEvery
	}
	sleep := c.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	expires := st.ExpiresIn
	if expires <= 0 {
		expires = 600
	}
	deadline := time.Now().Add(time.Duration(expires) * time.Second)
	for {
		sleep(interval)
		var g deviceGrant
		err := c.callWith(anon, "POST", "/api/cli/device/token", map[string]string{"device_code": st.DeviceCode}, &g)
		if err == nil {
			if g.Token == "" {
				return g, errors.New("the server approved the sign in but sent no token")
			}
			return g, nil
		}
		var he *httpError
		if !errors.As(err, &he) {
			// A network blip: keep waiting until the code expires.
			if time.Now().After(deadline) {
				return g, fmt.Errorf("the code expired; run %s login again", c.name())
			}
			continue
		}
		switch he.Code {
		case "authorization_pending":
		case "slow_down":
			if c.pollEvery == 0 {
				if he.Interval > 0 {
					interval = time.Duration(he.Interval) * time.Second
				} else {
					interval += 5 * time.Second
				}
			}
		case "access_denied":
			return g, errors.New("the sign in was denied in the browser")
		case "expired_token":
			return g, fmt.Errorf("the code expired; run %s login again", c.name())
		default:
			return g, fmt.Errorf("sign in failed: %w", err)
		}
		if time.Now().After(deadline) {
			return g, fmt.Errorf("the code expired; run %s login again", c.name())
		}
	}
}

// openBrowser opens link in the default browser, and fails quietly where
// there is none (a server over ssh).
func openBrowser(link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// logout revokes the saved token on the server, then forgets the server.
func (c *cli) logout(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: %s logout [host]", c.name())
	}
	cf := c.load()
	name := cf.Current
	if len(args) == 1 {
		name = args[0]
	}
	ctx, ok := cf.Contexts[name]
	if !ok {
		if len(args) == 1 {
			return fmt.Errorf("no saved server named %q", name)
		}
		return errors.New("not logged in")
	}
	revoked := false
	if ctx.TokenID > 0 {
		err := c.callWith(ctx, "DELETE", "/api/admin/tokens/"+strconv.FormatInt(ctx.TokenID, 10), nil, nil)
		var he *httpError
		switch {
		case err == nil:
			revoked = true
		case errors.As(err, &he) && (he.Status == 401 || he.Status == 404):
			revoked = true // already revoked or expired
		default:
			fmt.Fprintf(c.err, "warning: could not revoke the token on the server (%v); revoke it in the dashboard under Account, API tokens\n", err)
		}
	}
	delete(cf.Contexts, name)
	if cf.Current == name {
		cf.Current = ""
		if len(cf.Contexts) == 1 {
			for n := range cf.Contexts {
				cf.Current = n
			}
		}
	}
	if err := c.save(cf); err != nil {
		return err
	}
	switch {
	case revoked:
		fmt.Fprintf(c.out, "Signed out of %s and revoked the token.\n", name)
	case ctx.TokenID == 0:
		fmt.Fprintf(c.out, "Signed out of %s. The token was added by hand: revoke it in the dashboard under Account, API tokens.\n", name)
	default:
		fmt.Fprintf(c.out, "Signed out of %s.\n", name)
	}
	return nil
}

func (c *cli) whoami() error {
	ctx, err := c.current()
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.callWith(ctx, "GET", "/api/admin/me", nil, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var me struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s on %s\n", me.Email, hostOf(ctx.URL))
	fmt.Fprintf(c.out, "Acts as  %s", me.Role)
	if ctx.Scope != "" {
		fmt.Fprintf(c.out, " (token scope %s)", ctx.Scope)
	}
	fmt.Fprintln(c.out)
	return nil
}

// printJSON writes raw indented.
func (c *cli) printJSON(raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		_, err = c.out.Write(raw)
		return err
	}
	buf.WriteByte('\n')
	_, err := c.out.Write(buf.Bytes())
	return err
}

// callJSON is call, but with --json it prints the answer and reports done.
func (c *cli) callJSON(method, path string, out any) (done bool, err error) {
	var raw json.RawMessage
	if err := c.call(method, path, nil, &raw); err != nil {
		return false, err
	}
	if c.json {
		return true, c.printJSON(raw)
	}
	return false, json.Unmarshal(raw, out)
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
		return fmt.Errorf("usage: %s use <host>", c.name())
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
	ctx, err := c.current()
	if err != nil {
		return err
	}
	var health struct {
		Version string `json:"version"`
	}
	_ = c.callWith(context{URL: ctx.URL}, "GET", "/healthz", nil, &health)
	var raw json.RawMessage
	if err := c.callWith(ctx, "GET", "/api/admin/overview", nil, &raw); err != nil {
		return err
	}
	if c.json {
		out, _ := json.Marshal(map[string]any{"server": ctx.URL, "version": health.Version, "overview": raw})
		return c.printJSON(out)
	}
	if err := json.Unmarshal(raw, &ov); err != nil {
		return err
	}
	k := ov.Keys
	fmt.Fprintf(c.out, "Server     %s", hostOf(ctx.URL))
	if health.Version != "" {
		fmt.Fprintf(c.out, " (version %s)", health.Version)
	}
	fmt.Fprintln(c.out)
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
	if c.json {
		_, err := c.callJSON("GET", "/api/admin/services", nil)
		return err
	}
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
		return fmt.Errorf("usage: %s keys <service>", c.name())
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
	if done, err := c.callJSON("GET", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), &out); done || err != nil {
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
		return fmt.Errorf("usage: %s keys add <service> [file]", c.name())
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
		return fmt.Errorf("usage: %s keys check <service>", c.name())
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
	if done, err := c.callJSON("GET", "/api/admin/events?"+q.Encode(), &out); done || err != nil {
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
		return fmt.Errorf("usage: %s breaker <service> [--reset]", c.name())
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
		return fmt.Errorf("usage: %s usage <app-id>", c.name())
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
	if done, err := c.callJSON("GET", "/api/admin/apps/"+url.PathEscape(args[0])+"/usage", &out); done || err != nil {
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
