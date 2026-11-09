package providers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// GuideMeta is the provider's catalog entry, as the guide shows it.
type GuideMeta struct {
	Slug        string
	Name        string
	Description string
	Category    string
	DocsURL     string
	Website     string
}

// GuideContext says who reads the guide and where they send requests.
type GuideContext struct {
	// Service is the service slug the caller uses. Empty renders a
	// placeholder, for a provider guide with no service picked.
	Service     string
	ServiceName string
	// PublicURL is this server's public base URL, without a trailing slash.
	PublicURL string
	// Admin is true for a dashboard user, false for an app with a token.
	Admin bool
}

// GuideInput is everything RenderGuide needs.
type GuideInput struct {
	Meta GuideMeta
	Spec *Spec
	Ctx  GuideContext
}

// GuideDoc is the JSON form of a rendered guide: the same content as the
// Markdown, in fields an app can read without parsing text.
type GuideDoc struct {
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	Service   string `json:"service,omitempty"`
	Summary   string `json:"summary"`
	Generated bool   `json:"generated"`
	// ProxyBase is where requests go; append a path from Endpoints.
	ProxyBase string          `json:"proxy_base"`
	DocsURL   string          `json:"docs_url,omitempty"`
	Website   string          `json:"website,omitempty"`
	Endpoints []GuideEndpoint `json:"endpoints"`
	// Hosts are the provider's products on hosts of their own, each picked
	// with a "/~name" path prefix. Empty for most providers.
	Hosts    []GuideHost    `json:"hosts,omitempty"`
	Examples []GuideExample `json:"examples"`
	Limits    []string        `json:"limits"`
	Errors    []string        `json:"errors"`
	Notes     []string        `json:"notes"`
	Markdown  string          `json:"markdown,omitempty"`
}

// GuideExample is a spec example with a ready curl command through the proxy.
type GuideExample struct {
	Name   string            `json:"name"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   any               `json:"body,omitempty"`
	Curl   string            `json:"curl"`
}

// GuideHost is one of the provider's hosts as the guide shows it.
type GuideHost struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Group       string `json:"group,omitempty"`
	Description string `json:"description,omitempty"`
	DocsURL     string `json:"docs_url,omitempty"`
	// Prefix is the path prefix that picks it, such as "/~tiktok-scraper".
	Prefix string `json:"prefix"`
}

// ServicePlaceholder stands in for the service slug when no service is known.
const ServicePlaceholder = "{service}"

// BuildGuide assembles the guide. When the spec has no Guide, one is made
// from its examples and Generated is set.
func BuildGuide(in GuideInput) GuideDoc {
	spec := in.Spec
	if spec == nil {
		spec = &Spec{}
	}
	m := in.Meta
	svc := in.Ctx.Service
	if svc == "" {
		svc = ServicePlaceholder
	}
	name := m.Name
	if n := strings.TrimSpace(in.Ctx.ServiceName); n != "" && n != m.Name && m.Name != "" {
		name = n + " (" + m.Name + ")"
	}
	d := GuideDoc{
		Provider: m.Slug, Name: name, Service: in.Ctx.Service,
		ProxyBase: strings.TrimRight(in.Ctx.PublicURL, "/") + "/proxy/" + svc,
		DocsURL:   httpsOnly(m.DocsURL), Website: httpsOnly(m.Website),
		Endpoints: []GuideEndpoint{}, Examples: []GuideExample{},
		Limits: guideLimits(spec), Errors: guideErrors(spec), Notes: []string{},
	}
	if g := spec.Guide; g != nil {
		d.Summary = strings.TrimSpace(g.Summary)
		d.Endpoints = append(d.Endpoints, g.Endpoints...)
		d.Notes = append(d.Notes, g.Notes...)
		d.Markdown = strings.TrimSpace(g.Markdown)
	} else {
		d.Generated = true
		d.Summary = strings.TrimSpace(m.Description)
		d.Endpoints = endpointsFromExamples(spec.Examples)
	}
	if d.Summary == "" {
		d.Summary = m.Name + " through Acnologia."
	}
	for _, h := range spec.Hosts {
		d.Hosts = append(d.Hosts, GuideHost{Name: h.Name, Title: h.Title, Group: h.Group, Description: h.Description,
			DocsURL: httpsOnly(h.DocsURL), Prefix: HostPath(h.Name)})
	}
	for _, ex := range spec.Examples {
		method := strings.ToUpper(orElse(ex.Method, "GET"))
		d.Examples = append(d.Examples, GuideExample{
			Name: ex.Name, Method: method, Path: ex.Path, Query: ex.Query, Body: ex.Body,
			Curl: curlFor(d.ProxyBase, method, ex.Path, ex.Query, ex.Body),
		})
	}
	return d
}

// RenderGuide renders the guide for one service of a provider as Markdown.
func RenderGuide(in GuideInput) string {
	return BuildGuide(in).Render(in.Ctx.Admin)
}

// Render writes the guide as Markdown. admin adds the steps a dashboard
// user takes to give an app access.
func (d GuideDoc) Render(admin bool) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	title := d.Name
	if title == "" {
		title = d.Provider
	}
	p("# %s usage guide\n\n", title)
	p("%s\n\n", d.Summary)
	if d.Generated {
		p("> This guide was generated from the provider's examples and pricing. It covers only what Acnologia knows; read the provider's own docs for the full API.\n\n")
	}

	p("## How to call it through Acnologia\n\n")
	p("Send requests to the Acnologia proxy instead of the provider. The path after the proxy base is the provider's own path.\n\n")
	p("- Base URL: `%s`\n", d.ProxyBase)
	if d.Service == "" {
		p("- Replace `%s` with the slug of a service built on this provider.\n", ServicePlaceholder)
	}
	p("- Auth: send your app token as `Authorization: Bearer acn_...` (or `X-Acnologia-Token: acn_...`).\n")
	p("- Acnologia adds the provider's API key for you. Do not send one: whatever you send in its place is overwritten.\n")
	p("- Every response carries `X-Acnologia-Key-Id` and `X-Acnologia-Attempts`, so you can see which key served it.\n")
	slug := d.Service
	if slug == "" {
		slug = ServicePlaceholder
	}
	p("- MCP: with the app MCP server at `/mcp`, call `proxy_request` with `service=%s`, a `method` and a `path`. From the control MCP at `/mcp/control`, use `call_service`.\n", slug)
	if admin {
		p("- To get a token, create an app under Apps, allow it this service, and copy its token.\n")
	}
	p("\n")

	if len(d.Hosts) > 0 {
		p("## APIs\n\n")
		p("This provider serves %d APIs, each on its own host, and one key works on all of them. Start the path with an API's prefix to call it, for example `%s%s/...`. A path without a prefix goes to the main host.\n\n",
			len(d.Hosts), d.ProxyBase, d.Hosts[0].Prefix)
		for _, g := range hostGroups(d.Hosts) {
			if g.name != "" {
				p("### %s\n\n", g.name)
			}
			p("| API | Prefix | About |\n|---|---|---|\n")
			for _, h := range g.hosts {
				title := orElse(h.Title, h.Name)
				if h.DocsURL != "" {
					title = "[" + cell(title) + "](" + h.DocsURL + ")"
				} else {
					title = cell(title)
				}
				p("| %s | `%s` | %s |\n", title, h.Prefix, orElse(cell(h.Description), "-"))
			}
			p("\n")
		}
	}

	if len(d.Endpoints) > 0 {
		p("## Endpoints\n\n")
		grouped := false
		for _, e := range d.Endpoints {
			grouped = grouped || strings.TrimSpace(e.Group) != ""
		}
		group := "\x00"
		for _, e := range endpointOrder(d.Endpoints, grouped) {
			method := strings.ToUpper(orElse(e.Method, "GET"))
			if grouped {
				if g := orElse(strings.TrimSpace(e.Group), "General"); g != group {
					group = g
					p("### %s\n\n", g)
				}
				p("#### %s\n\n", e.Name)
			} else {
				p("### %s\n\n", e.Name)
			}
			p("`%s %s%s`\n\n", method, d.ProxyBase, e.Path)
			if s := strings.TrimSpace(e.Description); s != "" {
				p("%s\n\n", s)
			}
			if len(e.Params) > 0 {
				p("| Parameter | In | Type | Required | Description |\n|---|---|---|---|---|\n")
				for _, pr := range e.Params {
					req := "no"
					if pr.Required {
						req = "yes"
					}
					p("| `%s` | %s | %s | %s | %s |\n", pr.Name, pr.In, orElse(pr.Type, "-"), req, cell(pr.Description))
				}
				p("\n")
			}
			if e.Body != nil {
				if bb, err := json.MarshalIndent(e.Body, "", "  "); err == nil {
					p("Example body:\n\n```json\n%s\n```\n\n", bb)
				}
			}
			if s := strings.TrimSpace(e.Response); s != "" {
				p("Response: %s\n\n", s)
			}
		}
	}

	if len(d.Examples) > 0 {
		p("## Examples\n\n")
		for _, ex := range d.Examples {
			p("%s:\n\n```sh\n%s\n```\n\n", ex.Name, ex.Curl)
		}
	}

	if len(d.Limits) > 0 {
		p("## Limits and pricing\n\n")
		for _, l := range d.Limits {
			p("- %s\n", l)
		}
		p("\n")
	}

	p("## Errors and retries\n\n")
	for _, e := range d.Errors {
		p("- %s\n", e)
	}
	p("\n")

	if len(d.Notes) > 0 {
		p("## Notes\n\n")
		for _, n := range d.Notes {
			p("- %s\n", strings.TrimSpace(n))
		}
		p("\n")
	}
	if d.Markdown != "" {
		p("%s\n\n", d.Markdown)
	}

	if d.DocsURL != "" || d.Website != "" {
		p("## Provider docs\n\n")
		if d.DocsURL != "" {
			p("- API reference: %s\n", d.DocsURL)
		}
		if d.Website != "" {
			p("- Website: %s\n", d.Website)
		}
		p("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

type hostGroup struct {
	name  string
	hosts []GuideHost
}

// hostGroups groups hosts by Group in first seen order.
func hostGroups(hs []GuideHost) []hostGroup {
	idx := map[string]int{}
	out := []hostGroup{}
	for _, h := range hs {
		g := strings.TrimSpace(h.Group)
		i, ok := idx[g]
		if !ok {
			i = len(out)
			idx[g] = i
			out = append(out, hostGroup{name: g})
		}
		out[i].hosts = append(out[i].hosts, h)
	}
	return out
}

// endpointOrder keeps endpoints of one group together, groups in first seen
// order, and leaves an ungrouped list as it is.
func endpointOrder(es []GuideEndpoint, grouped bool) []GuideEndpoint {
	if !grouped {
		return es
	}
	idx := map[string]int{}
	buckets := [][]GuideEndpoint{}
	for _, e := range es {
		g := strings.TrimSpace(e.Group)
		i, ok := idx[g]
		if !ok {
			i = len(buckets)
			idx[g] = i
			buckets = append(buckets, nil)
		}
		buckets[i] = append(buckets[i], e)
	}
	out := make([]GuideEndpoint, 0, len(es))
	for _, b := range buckets {
		out = append(out, b...)
	}
	return out
}

var guidePathParam = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// endpointsFromExamples makes a fallback endpoint list, one per distinct
// method and path in the examples.
func endpointsFromExamples(exs []Example) []GuideEndpoint {
	out := []GuideEndpoint{}
	seen := map[string]bool{}
	for _, ex := range exs {
		method := strings.ToUpper(orElse(ex.Method, "GET"))
		key := method + " " + ex.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		e := GuideEndpoint{Name: ex.Name, Method: method, Path: ex.Path, Body: ex.Body}
		for _, m := range guidePathParam.FindAllStringSubmatch(ex.Path, -1) {
			e.Params = append(e.Params, GuideParam{Name: m[1], In: "path", Type: "string", Required: true, Description: "Path parameter."})
		}
		for _, k := range sortedKeys(ex.Query) {
			e.Params = append(e.Params, GuideParam{Name: k, In: "query", Type: "string", Description: "Example value: " + ex.Query[k]})
		}
		out = append(out, e)
	}
	return out
}

// guideLimits lists free allowances, prices and published caps.
func guideLimits(s *Spec) []string {
	out := []string{}
	if s.Quota.FreeTier != "" {
		out = append(out, "Free tier: "+s.Quota.FreeTier)
	}
	if s.Quota.Unit != "" {
		u := "Quota is counted in " + s.Quota.Unit
		if s.Quota.CountsPerRequest {
			u += ", one per request"
		}
		out = append(out, u+".")
	}
	if s.Quota.CostPerRequest > 0 && len(s.Offers) == 0 {
		out = append(out, fmt.Sprintf("Cost: about $%s per request.", trimFloat(s.Quota.CostPerRequest)))
	}
	for i := range s.Offers {
		o := &s.Offers[i]
		line := o.Summary()
		if n := strings.TrimSpace(o.Notes); n != "" {
			line += ". " + n
		}
		out = append(out, line)
	}
	if c := s.Defaults.MaxConcurrency; c > 0 {
		out = append(out, fmt.Sprintf("Acnologia sends at most %d requests at once per key; extra requests wait for a free key.", c))
	}
	if t := s.Defaults.CacheTTLSec; t > 0 {
		out = append(out, fmt.Sprintf("Successful GET responses are cached for %d seconds. Send `Cache-Control: no-cache` to skip the cache.", t))
	}
	return out
}

// guideErrors explains how Acnologia treats this provider's failures and
// what the caller sees.
func guideErrors(s *Spec) []string {
	rules := s.Rules
	if s.UseStandardRules {
		rules = append(append([]Rule{}, s.Rules...), standardRules...)
	}
	names := map[Outcome][]string{}
	for _, r := range rules {
		names[r.Outcome] = append(names[r.Outcome], r.Name)
	}
	which := func(o Outcome) string {
		if len(names[o]) == 0 {
			return ""
		}
		return " (" + strings.Join(names[o], "; ") + ")"
	}
	attempts := s.Defaults.MaxAttempts
	if attempts <= 0 {
		attempts = 5
	}
	out := []string{}
	if names[RateLimited] != nil || names[Busy] != nil {
		out = append(out, fmt.Sprintf("Rate limits%s: Acnologia cools the key down and retries on the next key, up to %d attempts. You get the first success.", which(RateLimited)+which(Busy), attempts))
	}
	if names[QuotaExhausted] != nil {
		out = append(out, "Out of quota"+which(QuotaExhausted)+": the key is parked until its quota resets and the request moves to the next key.")
	}
	if names[Invalid] != nil || names[Banned] != nil {
		out = append(out, "Rejected keys"+which(Invalid)+which(Banned)+": the key is marked invalid and skipped; the request moves to the next key.")
	}
	if names[Error] != nil {
		out = append(out, "Provider errors"+which(Error)+": relayed to you as is, not retried. If they keep coming, Acnologia pauses traffic to the service and answers 503 `service_unavailable` with `Retry-After`.")
	}
	out = append(out,
		"When no key can take the request, you get 503 `no_key_available` (or `all_keys_busy`) with `Retry-After`. Wait that long and retry.",
		"When your app reaches its own budget or request limit, you get 429 `app_limit_reached`.",
		"Other provider errors (400, 404, 422 and the like) mean the request itself is wrong. They are relayed unchanged; fix the request rather than retrying.",
	)
	return out
}

func curlFor(base, method, path string, query map[string]string, body any) string {
	u := base + path
	if len(query) > 0 {
		v := url.Values{}
		for k, val := range query {
			v.Set(k, val)
		}
		u += "?" + v.Encode()
	}
	var b strings.Builder
	b.WriteString("curl")
	if method != "GET" {
		b.WriteString(" -X " + method)
	}
	b.WriteString(" " + shellQuote(u) + " \\\n  -H \"Authorization: Bearer $ACN_TOKEN\"")
	if body != nil {
		bb, err := json.Marshal(body)
		if err == nil {
			b.WriteString(" \\\n  -H \"Content-Type: application/json\" \\\n  -d " + shellQuote(string(bb)))
		}
	}
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func orElse(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func httpsOnly(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}
