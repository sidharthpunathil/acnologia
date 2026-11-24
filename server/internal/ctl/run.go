package ctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/config"
)

// acn run -- <cmd> [args]: run one command with the forward proxy set for
// it and its children only. The shell that runs acn keeps its environment.
//
//	acn run [--token acn_...] [--no-placeholders] -- <cmd> [args...]
//
// The app token comes from --token, else $ACN_APP_TOKEN: the forward proxy
// takes app tokens, never the admin token the CLI signs in with. The server
// is the current context, or $ACNOLOGIA_URL.

// exitCode is an error that ends the CLI with the child's exit code.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// v1Setup is GET /api/v1/proxy, what an app token sees of the proxy.
type v1Setup struct {
	ProxyURL      string `json:"proxy_url"`
	CAFingerprint string `json:"ca_fingerprint"`
}

func (c *cli) runChild(args []string) error {
	token, placeholders := "", true
	i := 0
flags:
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i++
			break flags
		case a == "--token" && i+1 < len(args):
			token = args[i+1]
			i++
		case strings.HasPrefix(a, "--token="):
			token = strings.TrimPrefix(a, "--token=")
		case a == "--no-placeholders":
			placeholders = false
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %s; usage: %s run [--token acn_...] [--no-placeholders] -- <command> [args...]", a, c.name())
		default:
			break flags
		}
	}
	argv := args[i:]
	if len(argv) == 0 {
		return fmt.Errorf("usage: %s run [--token acn_...] [--no-placeholders] -- <command> [args...]", c.name())
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("ACN_APP_TOKEN"))
	}
	if token == "" {
		return fmt.Errorf("the forward proxy needs an app token: set ACN_APP_TOKEN or pass --token acn_... (make one on the Apps page)")
	}
	base, err := c.serverURL()
	if err != nil {
		return err
	}
	env, err := c.proxyRunEnv(base, token, placeholders)
	if err != nil {
		return err
	}
	return c.spawn(argv, env)
}

// serverURL is the server to use: $ACNOLOGIA_URL, else the current context.
func (c *cli) serverURL() (string, error) {
	if u := config.Getenv("ACNOLOGIA_URL"); u != "" {
		return strings.TrimRight(u, "/"), nil
	}
	cf := c.load()
	ctx, ok := cf.Contexts[cf.Current]
	if !ok || ctx.URL == "" {
		return "", fmt.Errorf("no server: run %s login, or set ACNOLOGIA_URL", c.name())
	}
	return ctx.URL, nil
}

// appGet calls a consumer route with the app token.
func (c *cli) appGet(base, path, token string, out any) error {
	req, err := http.NewRequest("GET", base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient(30 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		var e apiErr
		_ = json.Unmarshal(b, &e)
		return &httpError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if s, ok := out.(*[]byte); ok {
		*s = b
		return nil
	}
	return json.Unmarshal(b, out)
}

// proxyRunEnv is the environment overrides for the child: proxy, CA files
// and placeholder keys.
func (c *cli) proxyRunEnv(base, token string, placeholders bool) (map[string]string, error) {
	var s v1Setup
	if err := c.appGet(base, "/api/v1/proxy", token, &s); err != nil {
		return nil, fmt.Errorf("could not read the proxy setup with this app token: %w", err)
	}
	u, err := url.Parse(s.ProxyURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the server gave no usable proxy URL (%q)", s.ProxyURL)
	}
	var pem []byte
	if err := c.appGet(base, "/api/v1/proxy/ca.pem", token, &pem); err != nil || !strings.Contains(string(pem), "BEGIN CERTIFICATE") {
		if err == nil {
			err = errors.New("not a PEM certificate")
		}
		return nil, fmt.Errorf("could not download the proxy CA: %w", err)
	}
	ca, bundle, err := c.writeCAFiles(pem, s.CAFingerprint, "", "")
	if err != nil {
		return nil, err
	}
	u.User = url.UserPassword("app", token)
	proxy := u.String()
	env := map[string]string{
		"HTTPS_PROXY":         proxy,
		"HTTP_PROXY":          proxy,
		"NO_PROXY":            noProxy,
		"NODE_USE_ENV_PROXY":  "1",
		"NODE_EXTRA_CA_CERTS": ca,
	}
	// curl reads only the lowercase names. Windows names ignore case, so
	// there the uppercase ones already cover them.
	if runtime.GOOS != "windows" {
		env["https_proxy"], env["http_proxy"], env["no_proxy"] = proxy, proxy, noProxy
	}
	if bundle != "" {
		env["SSL_CERT_FILE"], env["REQUESTS_CA_BUNDLE"] = bundle, bundle
	} else {
		fmt.Fprintln(c.err, "acn: found no system certificate bundle, so SSL_CERT_FILE and REQUESTS_CA_BUNDLE stay unset (the CA alone would break other hosts); Node uses NODE_EXTRA_CA_CERTS")
	}
	if placeholders {
		for _, name := range c.placeholderNames(base, token) {
			env[name] = placeholderKey
		}
	}
	return env, nil
}

// placeholderNames are the key variables of the providers the app may use
// that the environment does not set yet. A failure to list them only skips
// the placeholders.
func (c *cli) placeholderNames(base, token string) []string {
	var out struct {
		Services []struct {
			Provider string `json:"provider"`
		} `json:"services"`
	}
	if err := c.appGet(base, "/api/v1/services", token, &out); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, sv := range out.Services {
		for _, n := range providerEnvNames[sv.Provider] {
			if !seen[n] && os.Getenv(n) == "" {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names
}

// mergeEnv puts overrides on top of base, replacing a variable of the same
// name (any case on Windows, where names ignore case).
func mergeEnv(base []string, over map[string]string, goos string) []string {
	norm := func(k string) string {
		if goos == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	drop := map[string]bool{}
	for k := range over {
		drop[norm(k)] = true
	}
	out := make([]string, 0, len(base)+len(over))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		// Windows keeps per drive directories as "=C:=C:\...": keep them.
		if k != "" && drop[norm(k)] {
			continue
		}
		out = append(out, kv)
	}
	keys := make([]string, 0, len(over))
	for k := range over {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+over[k])
	}
	return out
}

// spawn runs argv with the overrides, passing stdin, stdout and stderr
// through, forwarding signals and ending with the child's exit code. No
// shell runs in between, on any platform.
func (c *cli) spawn(argv []string, over map[string]string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("could not find %s: %w", argv[0], err)
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Env = mergeEnv(os.Environ(), over, runtime.GOOS)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.in, c.out, c.err
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, forwardSignals...)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				forwardSignal(cmd.Process, sig)
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	close(done)
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitCode(childExitCode(ee))
	}
	return err
}
