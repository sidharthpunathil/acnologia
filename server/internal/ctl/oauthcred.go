package ctl

// acn oauth: connect accounts to services that sign in with OAuth (Google
// APIs, Microsoft Graph and the like). connect prints the provider's sign
// in link, opens it, and waits until the account's owner approved it.

import (
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const oauthUsage = `usage:
  %[1]s oauth status <service>       the OAuth client, redirect URI and connected accounts
  %[1]s oauth setup <service> --client-id ID [--client-secret S] [--scopes a,b] [--tenant T]
                                    set the OAuth app registered with the provider
  %[1]s oauth connect <service> [--field key=value ...] [--no-browser] [--no-wait]
                                    print and open a sign in link, then wait until it is used
  %[1]s oauth reconnect <key-id> [--no-browser] [--no-wait]
                                    sign an account in again after its access was revoked`

type oauthConn struct {
	KeyID          int64      `json:"key_id"`
	Label          string     `json:"label"`
	Account        string     `json:"account"`
	Status         string     `json:"status"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastError      string     `json:"last_error"`
	NeedsReconnect bool       `json:"needs_reconnect"`
}

type oauthSettings struct {
	Service         string      `json:"service"`
	Configured      bool        `json:"configured"`
	ClientID        string      `json:"client_id"`
	HasClientSecret bool        `json:"has_client_secret"`
	Scopes          []string    `json:"scopes"`
	TakesTenant     bool        `json:"takes_tenant"`
	Tenant          string      `json:"tenant"`
	DefaultTenant   string      `json:"default_tenant"`
	RedirectURI     string      `json:"redirect_uri"`
	PublicURLSet    bool        `json:"public_url_set"`
	Connections     []oauthConn `json:"connections"`
	Fields          []struct {
		Key      string `json:"key"`
		Label    string `json:"label"`
		Optional bool   `json:"optional"`
	} `json:"fields"`
}

type oauthConnectState struct {
	ConnectID string `json:"connect_id"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	StatusURL string `json:"status_url"`
	KeyID     int64  `json:"key_id"`
	Account   string `json:"account"`
	Error     string `json:"error"`
}

// fieldList collects repeated --field key=value flags.
type fieldList map[string]string

func (f fieldList) String() string { return "" }
func (f fieldList) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("--field takes key=value")
	}
	f[strings.TrimSpace(k)] = val
	return nil
}

func (c *cli) oauth(args []string) error {
	if len(args) == 0 {
		return usageErrorf(oauthUsage, c.name())
	}
	switch args[0] {
	case "status":
		return c.oauthStatus(args[1:])
	case "setup":
		return c.oauthSetup(args[1:])
	case "connect":
		return c.oauthConnect(args[1:], false)
	case "reconnect":
		return c.oauthConnect(args[1:], true)
	}
	return usageErrorf(oauthUsage, c.name())
}

func (c *cli) oauthStatus(args []string) error {
	if len(args) != 1 {
		return usageErrorf(oauthUsage, c.name())
	}
	sv, err := c.serviceBySlug(args[0])
	if err != nil {
		return err
	}
	var out oauthSettings
	if done, err := c.callJSON("GET", fmt.Sprintf("/api/admin/services/%d/oauth", sv.ID), &out); done || err != nil {
		return err
	}
	client := "not set"
	if out.Configured {
		client = clean(out.ClientID)
		if !out.HasClientSecret {
			client += " (no secret)"
		}
	}
	fmt.Fprintf(c.out, "%s: OAuth client %s\nscopes: %s\nredirect URI to register: %s\n", clean(sv.Slug), client, clean(strings.Join(out.Scopes, " ")), clean(out.RedirectURI))
	if out.TakesTenant {
		t := out.Tenant
		if t == "" {
			t = out.DefaultTenant + " (the provider's default)"
		}
		fmt.Fprintf(c.out, "tenant: %s\n", clean(t))
	}
	if !out.PublicURLSet {
		fmt.Fprintln(c.out, "note: ACNOLOGIA_PUBLIC_URL is not set, so the redirect URI comes from how you reached the server")
	}
	if len(out.Connections) == 0 {
		fmt.Fprintf(c.out, "no accounts connected; run %s oauth connect %s\n", c.name(), clean(sv.Slug))
		return nil
	}
	tw := tabwriter.NewWriter(c.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tACCOUNT\tSTATUS\tTOKEN EXPIRES\tNOTE")
	for _, k := range out.Connections {
		exp := "-"
		if k.ExpiresAt != nil {
			exp = k.ExpiresAt.Local().Format("15:04")
		}
		note := clean(k.LastError)
		if k.NeedsReconnect {
			note = fmt.Sprintf("reconnect: %s oauth reconnect %d", c.name(), k.KeyID)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", k.KeyID, clean(orDash(k.Account)), clean(k.Status), exp, note)
	}
	return tw.Flush()
}

func (c *cli) oauthSetup(args []string) error {
	fs := flag.NewFlagSet("oauth setup", flag.ContinueOnError)
	fs.SetOutput(c.err)
	id := fs.String("client-id", "", "OAuth client id")
	secret := fs.String("client-secret", "", "OAuth client secret (asked for when left out)")
	scopes := fs.String("scopes", "", "comma separated scopes; empty keeps them")
	tenant := fs.String("tenant", "", "the tenant, for a provider whose OAuth URLs take one (Microsoft Graph): common, organizations, consumers, a tenant id or a domain; \"\" for the provider's default")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	tenantSet := false
	fs.Visit(func(f *flag.Flag) { tenantSet = tenantSet || f.Name == "tenant" })
	if len(pos) != 1 || *id == "" {
		return usageErrorf(oauthUsage, c.name())
	}
	sv, err := c.serviceBySlug(pos[0])
	if err != nil {
		return err
	}
	body := map[string]any{"client_id": *id}
	sec := *secret
	if sec == "" {
		sec, _ = c.ask("Client secret (empty keeps the stored one): ")
	}
	if strings.TrimSpace(sec) != "" {
		body["client_secret"] = strings.TrimSpace(sec)
	}
	if *scopes != "" {
		list := []string{}
		for _, s := range strings.Split(*scopes, ",") {
			if s = strings.TrimSpace(s); s != "" {
				list = append(list, s)
			}
		}
		body["scopes"] = list
	}
	if tenantSet {
		body["tenant"] = strings.TrimSpace(*tenant)
	}
	var raw json.RawMessage
	if err := c.call("PUT", fmt.Sprintf("/api/admin/services/%d/oauth", sv.ID), body, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.out, "OAuth client saved for %s. Next: %s oauth connect %s\n", clean(sv.Slug), c.name(), clean(sv.Slug))
	return nil
}

func (c *cli) oauthConnect(args []string, reconnect bool) error {
	fs := flag.NewFlagSet("oauth connect", flag.ContinueOnError)
	fs.SetOutput(c.err)
	fields := fieldList{}
	fs.Var(fields, "field", "an extra field for the account, key=value (repeatable)")
	noBrowser := fs.Bool("no-browser", false, "print the link without opening it")
	noWait := fs.Bool("no-wait", false, "print the link and return without waiting")
	timeout := fs.Duration("timeout", 15*time.Minute, "how long to wait for the sign in")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf(oauthUsage, c.name())
	}
	body := map[string]any{}
	if len(fields) > 0 {
		body["fields"] = map[string]string(fields)
	}
	var path string
	if reconnect {
		id, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil {
			return usageErrorf("reconnect takes a key id (see %s oauth status <service>)", c.name())
		}
		path = fmt.Sprintf("/api/admin/keys/%d/oauth/reconnect", id)
	} else {
		sv, err := c.serviceBySlug(pos[0])
		if err != nil {
			return err
		}
		path = fmt.Sprintf("/api/admin/services/%d/oauth/connect", sv.ID)
	}
	var st oauthConnectState
	if err := c.call("POST", path, body, &st); err != nil {
		return err
	}
	if *noWait {
		if c.json {
			b, _ := json.Marshal(st)
			return c.printJSON(b)
		}
		fmt.Fprintf(c.out, "Open this link in a browser signed in to the account:\n%s\nThen check: %s oauth status <service>\n", st.URL, c.name())
		return nil
	}
	fmt.Fprintf(c.err, "Open this link in a browser signed in to the account (or send it to the account's owner):\n%s\n", st.URL)
	if !*noBrowser {
		open := c.openBrowser
		if open == nil {
			open = openBrowser
		}
		_ = open(st.URL)
	}
	fmt.Fprintln(c.err, "Waiting for the sign in...")
	sleep := c.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	every := 2 * time.Second
	if c.pollEvery > 0 {
		every = c.pollEvery
	}
	deadline := time.Now().Add(*timeout)
	for {
		sleep(every)
		var cur oauthConnectState
		if err := c.call("GET", st.StatusURL, nil, &cur); err != nil {
			return err
		}
		switch cur.Status {
		case "done":
			if c.json {
				b, _ := json.Marshal(cur)
				return c.printJSON(b)
			}
			fmt.Fprintf(c.out, "Connected %s as key %d.\n", clean(orDash(cur.Account)), cur.KeyID)
			return nil
		case "failed":
			return fmt.Errorf("the sign in failed: %s", clean(cur.Error))
		case "expired":
			return fmt.Errorf("the link expired before it was used; run the command again")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gave up waiting; the link may still be used, check with %s oauth status", c.name())
		}
	}
}
