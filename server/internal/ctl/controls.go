package ctl

// Access controls from the command line: per-token limits, tag budgets and
// usage by tag, deny rules and the server's daily spend ceiling. Each
// command is one admin route, the same one the control MCP tool calls.

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// limits is an app's, a token's or a tag budget's caps; nil is no cap.
type limits struct {
	DailyRequests   *int64   `json:"daily_requests,omitempty"`
	MonthlyRequests *int64   `json:"monthly_requests,omitempty"`
	DailyBudget     *float64 `json:"daily_budget,omitempty"`
	MonthlyBudget   *float64 `json:"monthly_budget,omitempty"`
}

func (l limits) any() bool {
	return l.DailyRequests != nil || l.MonthlyRequests != nil || l.DailyBudget != nil || l.MonthlyBudget != nil
}

func (l limits) String() string {
	var parts []string
	if l.DailyRequests != nil {
		parts = append(parts, fmt.Sprintf("%d requests a day", *l.DailyRequests))
	}
	if l.MonthlyRequests != nil {
		parts = append(parts, fmt.Sprintf("%d requests a month", *l.MonthlyRequests))
	}
	if l.DailyBudget != nil {
		parts = append(parts, fmt.Sprintf("$%.2f a day", *l.DailyBudget))
	}
	if l.MonthlyBudget != nil {
		parts = append(parts, fmt.Sprintf("$%.2f a month", *l.MonthlyBudget))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// limitFlags are the four limit flags, as text so a flag left out is told
// apart from one set to 0.
type limitFlags struct {
	dailyReq, monthlyReq, dailyUSD, monthlyUSD *string
}

func addLimitFlags(fs *flag.FlagSet) limitFlags {
	return limitFlags{
		dailyReq:   fs.String("daily-requests", "", "requests a day (UTC)"),
		monthlyReq: fs.String("monthly-requests", "", "requests a month (UTC)"),
		dailyUSD:   fs.String("daily-budget", "", "USD a day"),
		monthlyUSD: fs.String("monthly-budget", "", "USD a month"),
	}
}

// read returns the limits the flags set, and whether any was given.
func (f limitFlags) read() (limits, bool, error) {
	var l limits
	set := false
	num := func(v, name string) (*int64, error) {
		if v == "" {
			return nil, nil
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 0 {
			return nil, usageErrorf("--%s takes a whole number of 0 or more", name)
		}
		set = true
		return &n, nil
	}
	usd := func(v, name string) (*float64, error) {
		if v == "" {
			return nil, nil
		}
		n, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(v), "$"), 64)
		if err != nil || n < 0 {
			return nil, usageErrorf("--%s takes a dollar amount of 0 or more", name)
		}
		set = true
		return &n, nil
	}
	var err error
	if l.DailyRequests, err = num(*f.dailyReq, "daily-requests"); err != nil {
		return l, false, err
	}
	if l.MonthlyRequests, err = num(*f.monthlyReq, "monthly-requests"); err != nil {
		return l, false, err
	}
	if l.DailyBudget, err = usd(*f.dailyUSD, "daily-budget"); err != nil {
		return l, false, err
	}
	if l.MonthlyBudget, err = usd(*f.monthlyUSD, "monthly-budget"); err != nil {
		return l, false, err
	}
	return l, set, nil
}

// pinFlags collects repeated --pin key=value flags.
type pinFlags map[string]string

func (p pinFlags) String() string { return "" }

func (p pinFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(val) == "" {
		return fmt.Errorf("--pin takes key=value, such as customer=cust_A")
	}
	p[strings.TrimSpace(k)] = strings.TrimSpace(val)
	return nil
}

type tokenRow struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	RevokedAt  *string           `json:"revoked_at"`
	Limits     limits            `json:"limits"`
	PinnedTags map[string]string `json:"pinned_tags"`
}

// tokenRef finds an app's token by id or name; revoked tokens are skipped.
func (c *cli) tokenRef(appID int64, ref string) (tokenRow, error) {
	var out struct {
		Tokens []tokenRow `json:"tokens"`
	}
	if err := c.call(http.MethodGet, fmt.Sprintf("/api/admin/apps/%d/tokens", appID), nil, &out); err != nil {
		return tokenRow{}, err
	}
	id, numErr := strconv.ParseInt(ref, 10, 64)
	for _, t := range out.Tokens {
		if t.RevokedAt == nil && ((numErr == nil && t.ID == id) || strings.EqualFold(t.Name, ref)) {
			return t, nil
		}
	}
	return tokenRow{}, &httpError{Status: http.StatusNotFound, Code: "not_found",
		Message: fmt.Sprintf("app %d has no token %s; %s tokens list --app %d lists them", appID, ref, c.name(), appID)}
}

func formatTags(m map[string]string) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = clean(k) + "=" + clean(m[k])
	}
	return strings.Join(parts, ",")
}

// ---- acn limits ----

func (c *cli) limitsCmd(args []string) error {
	fs := flag.NewFlagSet("limits", flag.ContinueOnError)
	fs.SetOutput(c.err)
	token := fs.String("token", "", "a token of the app, by id or name: show or set its own limits")
	clear := fs.Bool("clear", false, "remove the limits")
	clearPins := fs.Bool("clear-pins", false, "remove the token's pinned caller tags (with --token)")
	pins := pinFlags{}
	fs.Var(pins, "pin", "pin a caller tag on the token, key=value (with --token; repeat for more)")
	lf := addLimitFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	usage := usageErrorf("usage: %s limits <app> [--token id|name] [--daily-requests n] [--monthly-requests n] [--daily-budget usd] [--monthly-budget usd] [--clear] [--pin key=value] [--clear-pins]", c.name())
	if len(pos) != 1 {
		return usage
	}
	l, set, err := lf.read()
	if err != nil {
		return err
	}
	if set && *clear {
		return usageErrorf("--clear removes the limits; leave out the limit flags with it")
	}
	if (len(pins) > 0 || *clearPins) && *token == "" {
		return usageErrorf("--pin and --clear-pins work on one token: add --token")
	}
	appID, err := c.appID(pos[0])
	if err != nil {
		return err
	}
	change := set || *clear
	if *token == "" {
		if !change {
			return c.showControls(appID)
		}
		var raw json.RawMessage
		if err := c.call(http.MethodPut, fmt.Sprintf("/api/admin/apps/%d/token-limits", appID), map[string]any{"limits": l}, &raw); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(raw)
		}
		fmt.Fprintf(c.out, "Every token of app %d now gets: %s (unless it sets its own).\n", appID, l)
		return nil
	}
	t, err := c.tokenRef(appID, *token)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if change {
		body["limits"] = l
	}
	if len(pins) > 0 {
		body["pinned_tags"] = map[string]string(pins)
	} else if *clearPins {
		body["pinned_tags"] = map[string]string{}
	}
	if len(body) == 0 {
		if c.json {
			b, _ := json.Marshal(t)
			return c.printJSON(b)
		}
		fmt.Fprintf(c.out, "Token %s (id %d) of app %d\n", clean(t.Name), t.ID, appID)
		fmt.Fprintf(c.out, "Own limits    %s\n", t.Limits)
		fmt.Fprintf(c.out, "Pinned tags   %s\n", formatTags(t.PinnedTags))
		return nil
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPatch, fmt.Sprintf("/api/admin/apps/%d/tokens/%d", appID, t.ID), body, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var nt tokenRow
	_ = json.Unmarshal(raw, &nt)
	fmt.Fprintf(c.out, "Token %s: own limits %s; pinned tags %s.\n", clean(t.Name), nt.Limits, formatTags(nt.PinnedTags))
	return nil
}

type denyRule struct {
	ID         int64  `json:"id"`
	Method     string `json:"method"`
	Service    string `json:"service"`
	Host       string `json:"host"`
	PathPrefix string `json:"path_prefix"`
	Note       string `json:"note"`
}

func (r denyRule) target() string {
	switch {
	case r.Service != "":
		return r.Service
	case r.Host != "":
		return r.Host
	}
	return "any target"
}

type tagBudget struct {
	Key string `json:"key"`
	limits
}

// showControls prints an app's per-token default, tag budgets and deny
// rules.
func (c *cli) showControls(appID int64) error {
	var out struct {
		TokenLimits limits      `json:"token_limits"`
		TagBudgets  []tagBudget `json:"tag_budgets"`
		DenyRules   []denyRule  `json:"deny_rules"`
	}
	if done, err := c.callJSON(http.MethodGet, fmt.Sprintf("/api/admin/apps/%d/controls", appID), &out); done || err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Per-token default  %s\n", out.TokenLimits)
	if len(out.TagBudgets) == 0 {
		fmt.Fprintln(c.out, "Tag budgets        none")
	}
	for i, b := range out.TagBudgets {
		label := "Tag budgets        "
		if i > 0 {
			label = "                   "
		}
		fmt.Fprintf(c.out, "%seach %s: %s\n", label, clean(b.Key), b.limits)
	}
	fmt.Fprintf(c.out, "Deny rules         %d", len(out.DenyRules))
	if len(out.DenyRules) > 0 {
		fmt.Fprintf(c.out, " (%s deny list %d)", c.name(), appID)
	}
	fmt.Fprintln(c.out)
	return nil
}

// ---- acn deny ----

func (c *cli) deny(args []string) error {
	usage := usageErrorf("usage: %[1]s deny list <app>, %[1]s deny add <app> [--method M] [--service s | --host h] [--path /prefix] [--note text], or %[1]s deny rm <app> <rule-id>", c.name())
	if len(args) == 0 {
		return usage
	}
	sub := args[0]
	fs := flag.NewFlagSet("deny "+sub, flag.ContinueOnError)
	fs.SetOutput(c.err)
	method := fs.String("method", "", "HTTP method, or * for any (the default)")
	service := fs.String("service", "", "service, group or capability slug")
	host := fs.String("host", "", "upstream hostname")
	path := fs.String("path", "", "path prefix; a trailing * makes it a plain string prefix")
	note := fs.String("note", "", "why the rule exists")
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usage
	}
	appID, err := c.appID(pos[0])
	if err != nil {
		return err
	}
	base := fmt.Sprintf("/api/admin/apps/%d/deny-rules", appID)
	switch sub {
	case "list", "ls":
		if len(pos) != 1 {
			return usage
		}
		var out struct {
			Rules []denyRule `json:"rules"`
		}
		if done, err := c.callJSON(http.MethodGet, base, &out); done || err != nil {
			return err
		}
		tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tMETHOD\tTARGET\tPATH\tNOTE")
		for _, r := range out.Rules {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", r.ID, clean(r.Method), clean(r.target()), clean(orDash(r.PathPrefix)), clip(r.Note, 60))
		}
		return tw.Flush()
	case "add":
		if len(pos) != 1 {
			return usage
		}
		body := map[string]any{"method": *method, "service": *service, "host": *host, "path_prefix": *path, "note": *note}
		var raw json.RawMessage
		if err := c.call(http.MethodPost, base, body, &raw); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(raw)
		}
		var r denyRule
		_ = json.Unmarshal(raw, &r)
		fmt.Fprintf(c.out, "Added deny rule %d: %s %s %s.\n", r.ID, clean(r.Method), clean(r.target()), clean(orDash(r.PathPrefix)))
		return nil
	case "rm", "remove", "delete":
		if len(pos) != 2 {
			return usage
		}
		id, err := strconv.ParseInt(pos[1], 10, 64)
		if err != nil {
			return usageErrorf("the rule id is a number; %s deny list %d shows them", c.name(), appID)
		}
		if err := c.call(http.MethodDelete, fmt.Sprintf("%s/%d", base, id), nil, nil); err != nil {
			return err
		}
		if c.json {
			return c.printJSON([]byte(`{"ok":true}`))
		}
		fmt.Fprintf(c.out, "Removed deny rule %d from app %d.\n", id, appID)
		return nil
	}
	return usage
}

// ---- acn tags ----

func (c *cli) tags(args []string) error {
	fs := flag.NewFlagSet("tags", flag.ContinueOnError)
	fs.SetOutput(c.err)
	days := fs.Int("days", 30, "days of history, 1 to 366")
	budget := fs.String("budget", "", "set the budget of this tag key, such as customer, with the limit flags")
	clear := fs.Bool("clear", false, "remove the --budget key's budget")
	lf := addLimitFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: %s tags <app> [--days 30], or %s tags <app> --budget <key> [--daily-budget usd] [--daily-requests n] ... [--clear]", c.name(), c.name())
	}
	appID, err := c.appID(pos[0])
	if err != nil {
		return err
	}
	l, set, err := lf.read()
	if err != nil {
		return err
	}
	if *budget != "" {
		return c.setTagBudget(appID, *budget, l, set, *clear)
	}
	if set || *clear {
		return usageErrorf("the limit flags and --clear set a tag budget: add --budget <key>")
	}
	var out struct {
		Days int `json:"days"`
		Tags []struct {
			Key           string  `json:"key"`
			Value         string  `json:"value"`
			TodayRequests int64   `json:"today_requests"`
			TodayCost     float64 `json:"today_cost"`
			MonthRequests int64   `json:"month_requests"`
			MonthCost     float64 `json:"month_cost"`
			Requests      int64   `json:"requests"`
			Cost          float64 `json:"cost"`
		} `json:"tags"`
		Budgets   []tagBudget `json:"tag_budgets"`
		Truncated bool        `json:"truncated"`
	}
	q := url.Values{"days": {strconv.Itoa(*days)}}
	if done, err := c.callJSON(http.MethodGet, fmt.Sprintf("/api/admin/apps/%d/usage/tags?%s", appID, q.Encode()), &out); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "TAG\tTODAY\tTODAY $\tMONTH\tMONTH $\t%dD\t%dD $\n", out.Days, out.Days)
	for _, t := range out.Tags {
		fmt.Fprintf(tw, "%s=%s\t%d\t%.4f\t%d\t%.4f\t%d\t%.4f\n", clean(t.Key), clean(t.Value), t.TodayRequests, t.TodayCost, t.MonthRequests, t.MonthCost, t.Requests, t.Cost)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, b := range out.Budgets {
		fmt.Fprintf(c.out, "Budget: each %s value gets %s\n", clean(b.Key), b.limits)
	}
	if out.Truncated {
		fmt.Fprintln(c.out, "Only the most expensive values are shown.")
	}
	return nil
}

// setTagBudget replaces one key's budget, keeping the others.
func (c *cli) setTagBudget(appID int64, key string, l limits, set, clear bool) error {
	if set == clear {
		return usageErrorf("--budget %s needs the limit flags, or --clear to remove it", key)
	}
	var cur struct {
		Budgets []tagBudget `json:"tag_budgets"`
	}
	if err := c.call(http.MethodGet, fmt.Sprintf("/api/admin/apps/%d/controls", appID), nil, &cur); err != nil {
		return err
	}
	next := []tagBudget{}
	for _, b := range cur.Budgets {
		if !strings.EqualFold(b.Key, key) {
			next = append(next, b)
		}
	}
	if !clear {
		next = append(next, tagBudget{Key: key, limits: l})
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPut, fmt.Sprintf("/api/admin/apps/%d/tag-budgets", appID), map[string]any{"budgets": next}, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	if clear {
		fmt.Fprintf(c.out, "Removed the %s budget of app %d.\n", clean(key), appID)
	} else {
		fmt.Fprintf(c.out, "Each %s value of app %d now gets %s.\n", clean(key), appID, l)
	}
	return nil
}

// ---- acn ceiling ----

func (c *cli) ceiling(args []string) error {
	usage := usageErrorf("usage: %[1]s ceiling, %[1]s ceiling set <usd>, %[1]s ceiling off, or %[1]s ceiling reset (back to ACNOLOGIA_DAILY_SPEND_CEILING_USD)", c.name())
	const path = "/api/admin/settings/spend-ceiling"
	var body map[string]any
	switch {
	case len(args) == 0:
	case args[0] == "set" && len(args) == 2:
		v, err := strconv.ParseFloat(strings.TrimPrefix(args[1], "$"), 64)
		if err != nil || v < 0 {
			return usageErrorf("the ceiling is a dollar amount of 0 or more")
		}
		body = map[string]any{"usd": v}
	case args[0] == "off" && len(args) == 1:
		body = map[string]any{"usd": 0}
	case args[0] == "reset" && len(args) == 1:
		body = map[string]any{"usd": nil}
	default:
		return usage
	}
	var raw json.RawMessage
	if body == nil {
		if err := c.call(http.MethodGet, path, nil, &raw); err != nil {
			return err
		}
	} else if err := c.call(http.MethodPut, path, body, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out struct {
		Ceiling float64 `json:"ceiling_usd"`
		Source  string  `json:"source"`
		Spent   float64 `json:"spent_today_usd"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out.Ceiling <= 0 {
		fmt.Fprintf(c.out, "No daily spend ceiling (%s); spent today $%.4f.\n", clean(out.Source), out.Spent)
		return nil
	}
	fmt.Fprintf(c.out, "Daily spend ceiling $%.2f (%s); spent today $%.4f.\n", out.Ceiling, clean(out.Source), out.Spent)
	return nil
}
