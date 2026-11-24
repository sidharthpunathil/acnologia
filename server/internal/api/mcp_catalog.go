package api

// The MCP tool catalog. Service tools come from the provider data itself:
// every example in a provider's spec, then every endpoint of its usage
// guide that no example already covers. So adding a provider (or the
// builder writing its guide) adds its tools, with no code and no restart.
//
// An app with many tools does not have to list them all: in search mode
// tools/list holds the core tools plus search_tools and call_tool, and the
// agent finds what it needs by asking ("linkedin jobs", "tiktok video").

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/sidharthpunathil/acnologia/server/internal/breaker"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Tool modes for an app's MCP server.
const (
	toolModeAuto   = "auto"   // list every tool while there are few, else search
	toolModeAll    = "all"    // list every tool
	toolModeSearch = "search" // list core tools plus search_tools and call_tool
)

// mcpAutoListMax is how many service tools auto mode still lists in full.
const mcpAutoListMax = 30

var discoveryTools = withTitles([]mcpTool{
	{Name: "search_tools", Set: setService, Annotations: readOnly("Find tools"),
		Description: "Find the tools for a job across every provider this app can use, for example \"linkedin jobs\", \"tiktok video\", \"google maps reviews\" or \"generate image\". Returns the best matching tools with their input schemas; call one with call_tool (or by its name). An empty query lists the services and how many tools each has. The catalog follows the providers on the server, so new providers show up here at once.",
		InputSchema: obj(map[string]any{
			"query":   str("What you want to do, or a provider, product or site name"),
			"service": str("Optional: only tools of this service slug"),
			"limit":   integer("How many tools to return (default 8)", 1, 25),
		})},
	{Name: "call_tool", Set: setService, Annotations: hints("Call a tool", true, false, true),
		Description: "Call a tool that search_tools found, by name, with its arguments.",
		InputSchema: obj(map[string]any{
			"name":      str("Tool name from search_tools"),
			"arguments": map[string]any{"type": "object", "description": "The tool's arguments, as its input schema describes"},
		}, "name")},
})

// toolSource is one callable endpoint of a service: an example or a guide
// endpoint.
type toolSource struct {
	ex     providers.Example
	name   string // the tool name's base, joined with the service slug
	title  string
	desc   string
	group  string
	params []providers.GuideParam
	guide  bool
}

// toolSources lists a spec's examples, then the guide endpoints whose
// method and path no example has.
func toolSources(spec *providers.Spec) []toolSource {
	out := []toolSource{}
	have := map[string]bool{}
	for _, ex := range spec.Examples {
		m := strings.ToUpper(orDefault(ex.Method, "GET"))
		have[m+" "+ex.Path] = true
		out = append(out, toolSource{ex: ex, name: ex.Name, title: ex.Name})
	}
	if spec.Guide == nil {
		return out
	}
	for _, ep := range spec.Guide.Endpoints {
		m := strings.ToUpper(orDefault(ep.Method, "GET"))
		if ep.Path == "" || ep.Name == "" || have[m+" "+ep.Path] {
			continue
		}
		have[m+" "+ep.Path] = true
		name := ep.Name
		title := ep.Name
		if ep.Group != "" && !strings.Contains(strings.ToLower(ep.Name), strings.ToLower(ep.Group)) {
			name = ep.Group + " " + ep.Name
			title = ep.Group + ": " + ep.Name
		}
		out = append(out, toolSource{
			ex:   providers.Example{Name: ep.Name, Method: m, Path: ep.Path, Body: ep.Body},
			name: name, title: title, desc: ep.Description, group: ep.Group, params: ep.Params, guide: true,
		})
	}
	return out
}

func jsonType(t string) string {
	switch strings.ToLower(t) {
	case "integer", "int", "int64":
		return "integer"
	case "number", "float", "double":
		return "number"
	case "boolean", "bool":
		return "boolean"
	case "array", "object":
		return strings.ToLower(t)
	}
	return "string"
}

// sourceSchema builds a tool's input schema and the query params it sends.
func sourceSchema(src toolSource) (map[string]any, []string) {
	props := map[string]any{}
	required := []string{}
	params := []string{}
	inPath := map[string]bool{}
	for _, m := range pathParamRe.FindAllStringSubmatch(src.ex.Path, -1) {
		inPath[m[1]] = true
		props[m[1]] = str("Path parameter")
		required = append(required, m[1])
	}
	if !src.guide {
		keys := make([]string, 0, len(src.ex.Query))
		for k := range src.ex.Query {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			props[k] = map[string]any{"type": "string", "description": fmt.Sprintf("Query parameter (example: %s)", src.ex.Query[k])}
			params = append(params, k)
		}
	}
	bodyProps := map[string]any{}
	bodyReq := []string{}
	for _, p := range src.params {
		if p.Name == "" {
			continue
		}
		prop := map[string]any{"type": jsonType(p.Type)}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if prop["type"] == "array" {
			prop["items"] = map[string]any{}
		}
		switch strings.ToLower(p.In) {
		case "path":
			if inPath[p.Name] {
				d := p.Description
				if d == "" {
					d = "Path parameter"
				}
				props[p.Name] = str(d)
			}
		case "body":
			bodyProps[p.Name] = prop
			if p.Required {
				bodyReq = append(bodyReq, p.Name)
			}
		case "header":
		default:
			if inPath[p.Name] || p.Name == "body" || p.Name == "extra_query" {
				continue
			}
			props[p.Name] = prop
			params = append(params, p.Name)
			if p.Required {
				required = append(required, p.Name)
			}
		}
	}
	props["extra_query"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Any other query parameters"}
	if _, ok := props["idempotency_key"]; !ok {
		props["idempotency_key"] = str(idempotencyArgDesc)
	}
	if src.ex.Body != nil || len(bodyProps) > 0 {
		b := map[string]any{"type": "object", "description": "JSON body"}
		if src.ex.Body != nil {
			eb, _ := json.Marshal(src.ex.Body)
			b["description"] = "JSON body. Example: " + string(eb)
		}
		if len(bodyProps) > 0 {
			b["properties"] = bodyProps
			if len(bodyReq) > 0 {
				b["required"] = bodyReq
			}
		}
		props["body"] = b
		if !src.guide || len(bodyReq) > 0 {
			required = append(required, "body")
		}
	}
	return obj(props, required...), params
}

// sourceTool turns one source of a service into its tool.
func sourceTool(sv *store.Service, spec *providers.Spec, src toolSource, name string) mcpTool {
	schema, params := sourceSchema(src)
	method := strings.ToUpper(orDefault(src.ex.Method, "GET"))
	desc := ""
	if src.desc != "" {
		desc = strings.TrimSpace(src.desc) + " "
	}
	// search_tools reads only the tool's own words: the sentence every
	// service tool shares would match every query for "rate" or "key".
	search := strings.Join([]string{src.desc, src.title, method, src.ex.Path, sv.Description}, " ")
	desc += fmt.Sprintf("%s on %s (%s %s). Acnologia adds a pooled key and rotates on rate limits.", src.title, sv.Name, method, src.ex.Path)
	desc += offerHint(spec, method, src.ex.Path)
	if sv.Description != "" {
		desc += " " + sv.Description
	}
	ex := src.ex
	return mcpTool{
		Name: name, Title: sv.Name + ": " + src.title, Description: desc, Set: setService,
		InputSchema: schema,
		Annotations: map[string]any{"title": sv.Name + ": " + src.title, "openWorldHint": true, "readOnlyHint": method == "GET"},
		service:     sv.Slug, example: &ex, params: params,
		keywords:   strings.Join([]string{sv.Slug, sv.Name, sv.Provider, sv.Category, src.group, hostsText(spec, src.ex.Path)}, " "),
		searchText: search,
	}
}

const idempotencyArgDesc = "Optional: sent as the Idempotency-Key header. A retry with the same key within 24 hours gets the first answer back instead of calling the provider again. Use one per logical request on calls that create or charge."

// hostsText names the provider host a path is on ("/~tiktok-scraper/..."),
// so a search for the product finds its tools.
func hostsText(spec *providers.Spec, path string) string {
	for _, h := range spec.Hosts {
		if p := "/~" + h.Name; path == p || strings.HasPrefix(path, p+"/") {
			return strings.Join([]string{h.Name, h.Title, h.Group, h.URL}, " ")
		}
	}
	return ""
}

// mcpListedTools is what tools/list shows: every tool in mode all, the
// core and discovery tools in mode search, and in mode auto whichever fits.
func (s *Server) mcpListedTools(ctx context.Context, a *store.App, st store.MCPSettings) []mcpTool {
	if s.searchModeFor(ctx, a, st) {
		return s.mcpToolSet(ctx, a, st, false)
	}
	return s.mcpTools(ctx, a, st)
}

// searchModeFor reports whether tools/list leaves the service tools to
// search_tools for this app.
func (s *Server) searchModeFor(ctx context.Context, a *store.App, st store.MCPSettings) bool {
	if !st.ServiceTools || !a.AllowsProxy() {
		return false
	}
	switch st.ToolMode {
	case toolModeSearch:
		return true
	case toolModeAuto:
		c := s.toolCatalog(ctx)
		ids := c.view(a).ids
		if !st.ReadOnly {
			return len(ids) > mcpAutoListMax
		}
		n := 0
		for _, id := range ids {
			if toolIsReadOnly(c.tools[id]) {
				n++
			}
		}
		return n > mcpAutoListMax
	}
	return false
}

func isDiscovery(name string) bool { return findTool(discoveryTools, name) != nil }

// ---- search ----

var toolSynonyms = map[string][]string{
	"search":   {"find", "query", "lookup", "serp"},
	"find":     {"search", "lookup"},
	"scrape":   {"extract", "crawl", "scraper", "fetch"},
	"crawl":    {"scrape", "crawler"},
	"extract":  {"scrape", "parse"},
	"twitter":  {"tweet", "x"},
	"tweet":    {"twitter"},
	"maps":     {"places", "business", "map", "location"},
	"places":   {"maps", "business"},
	"business": {"maps", "places", "company"},
	"company":  {"business", "organization"},
	"job":      {"jobs", "career", "hiring"},
	"jobs":     {"job", "career", "hiring"},
	"image":    {"picture", "photo", "img"},
	"photo":    {"image", "picture"},
	"video":    {"clip", "reel"},
	"llm":      {"chat", "completion", "model", "generate"},
	"chat":     {"llm", "completion", "message"},
	"generate": {"create", "generation"},
	"speech":   {"voice", "tts", "audio"},
	"voice":    {"speech", "tts", "audio"},
	"profile":  {"user", "account"},
	"user":     {"profile", "account"},
	"product":  {"item", "listing"},
	"price":    {"pricing", "cost"},
	"home":     {"house", "property", "real", "estate"},
	"news":     {"article", "headline"},
	"weather":  {"forecast"},
	"email":    {"mail"},
	"balance":  {"usage", "credit", "quota", "account"},
	"usage":    {"balance", "quota", "credit"},
}

var stopWords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "for": true, "to": true, "on": true, "in": true, "and": true, "or": true, "with": true, "by": true, "me": true, "get": true, "i": true, "want": true, "my": true, "is": true, "from": true, "acnologia": true}

func tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := f[:0]
	for _, t := range f {
		if !stopWords[t] {
			out = append(out, stem(t))
		}
	}
	return out
}

// stem folds plurals and a few endings so "jobs" finds "job".
func stem(t string) string {
	switch {
	case len(t) > 5 && strings.HasSuffix(t, "ing"):
		return t[:len(t)-3]
	case len(t) > 4 && strings.HasSuffix(t, "ies"):
		return t[:len(t)-3] + "y"
	case len(t) > 3 && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss"):
		return t[:len(t)-1]
	}
	return t
}

// within1 reports whether a and b differ by at most one edit, for typos
// such as "linkdin".
func within1(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < la && j < lb {
		if a[i] == b[j] {
			i, j = i+1, j+1
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		switch {
		case la > lb:
			i++
		case lb > la:
			j++
		default:
			i, j = i+1, j+1
		}
	}
	return edits+(la-i)+(lb-j) <= 1
}

// searchTools answers the search_tools tool.
func (s *Server) searchTools(ctx context.Context, a *store.App, st store.MCPSettings, raw json.RawMessage) (map[string]any, error) {
	var in struct {
		Query   string `json:"query"`
		Service string `json:"service"`
		Limit   int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	if in.Limit <= 0 {
		in.Limit = 8
	}
	if in.Limit > 25 {
		in.Limit = 25
	}
	if !st.ServiceTools || !a.AllowsProxy() {
		return nil, errors.New("service tools are off for this app")
	}
	c := s.toolCatalog(ctx)
	v := c.view(a)
	off := disabledSet(st.DisabledTools)
	// usable is the app's tools, narrowed to one service when asked.
	usable := make([]bool, len(c.tools))
	ids := []int{}
	for _, id := range v.ids {
		t := &c.tools[id]
		if off[t.Name] || (st.ReadOnly && !toolIsReadOnly(*t)) || (in.Service != "" && t.service != in.Service && t.capability != in.Service) {
			continue
		}
		usable[id] = true
		ids = append(ids, id)
	}
	// The directory: services with their tool counts, so an agent can see
	// what exists before it knows what to ask.
	type svcCount struct {
		Service string `json:"service"`
		Name    string `json:"name"`
		Tools   int    `json:"tools"`
	}
	directory := func() []svcCount {
		counts := map[string]*svcCount{}
		order := []string{}
		for _, id := range ids {
			t := &c.tools[id]
			key := t.service
			name := strings.SplitN(t.Title, ":", 2)[0]
			if t.capability != "" {
				key, name = t.capability, t.Title+" (capability)"
			}
			if counts[key] == nil {
				counts[key] = &svcCount{Service: key, Name: name}
				order = append(order, key)
			}
			counts[key].Tools++
		}
		out := make([]svcCount, 0, len(order))
		for _, k := range order {
			out = append(out, *counts[k])
		}
		return out
	}
	live := s.newAvailLookup(ctx, c)
	var picked []*mcpTool
	total := 0
	if strings.TrimSpace(in.Query) == "" {
		total = len(ids)
		for _, id := range ids {
			if len(picked) == in.Limit {
				break
			}
			picked = append(picked, &c.tools[id])
		}
	} else {
		hits := c.index().rank(in.Query, usable, len(ids))
		hits = withCapabilities(c, ids, hits)
		live.order(hits)
		total = len(hits)
		for _, h := range hits {
			if len(picked) == in.Limit {
				break
			}
			picked = append(picked, &c.tools[h.id])
		}
	}
	out := []map[string]any{}
	for _, t := range picked {
		m := map[string]any{"name": t.Name, "title": t.Title, "description": t.Description, "inputSchema": t.InputSchema}
		if t.service != "" {
			m["service"] = t.service
		}
		if t.capability != "" {
			m["capability"] = t.capability
			m["served_by"] = t.routes
		}
		if t.upstreamMCP {
			m["upstream_mcp"] = true
			m["read_only"] = toolIsReadOnly(*t)
		}
		if av, ok := live.tool(t); ok {
			m["available"] = av
		}
		if e := live.evidence(t); e.Enough() {
			m["evidence"] = toolEvidence(e)
		}
		out = append(out, m)
	}
	res := map[string]any{"tools": out, "matches": total, "catalog_size": len(ids)}
	if strings.TrimSpace(in.Query) == "" || total == 0 {
		res["services"] = directory()
	}
	if total == 0 {
		res["hint"] = publicText("No tool matched. Try a provider or site name, or other words; the services list shows what exists. proxy_request calls any endpoint of a service, and service_guide lists its endpoints.")
	} else {
		res["hint"] = publicText("Call a tool with call_tool {name, arguments}, or by its name.")
	}
	return res, nil
}

// ---- live availability ----

// toolAvail is how ready a service tool's keys are right now, so an agent
// can pass over a tool whose keys all rest.
type toolAvail struct {
	// State is available (a key is free), resting (every key cools down,
	// the first is back at resting_until) or down (the provider keeps
	// failing, or no key is usable).
	State         string     `json:"state"`
	FreeKeys      int        `json:"free_keys"`
	RestingUntil  *time.Time `json:"resting_until,omitempty"`
	Breaker       string     `json:"breaker"`
	RetryAfterSec int        `json:"retry_after_sec,omitempty"`
}

func (a toolAvail) rank() int {
	switch a.State {
	case "available":
		return 0
	case "resting":
		return 1
	}
	return 2
}

// availLookup answers availability for one search: the free key counts
// (cached for a few seconds, shared with routing) and the breakers, which
// are in memory. When a matched service has no free key it reads, once,
// when each service's first cooling key is back.
type availLookup struct {
	s       *Server
	ctx     context.Context
	c       *toolCatalog
	free    map[int64]int
	resting map[int64]time.Time
	memo    map[string]toolAvail
	ev      *store.Evidence
}

func (s *Server) newAvailLookup(ctx context.Context, c *toolCatalog) *availLookup {
	return &availLookup{s: s, ctx: ctx, c: c, memo: map[string]toolAvail{}}
}

func (l *availLookup) service(slug string) (toolAvail, bool) {
	if av, ok := l.memo[slug]; ok {
		return av, true
	}
	ref, ok := l.c.svcs[slug]
	if !ok {
		return toolAvail{}, false
	}
	if l.free == nil {
		l.free = l.s.freeKeys(l.ctx)
	}
	st := l.s.Breakers.Status(ref.id, breakerSettings(providers.Get(ref.provider)))
	av := toolAvail{FreeKeys: l.free[ref.id], Breaker: string(st.State)}
	switch {
	case st.State == breaker.Open:
		av.State = "down"
		av.RetryAfterSec = int(math.Ceil(st.RetryAfter))
	case av.FreeKeys > 0:
		av.State = "available"
	default:
		if l.resting == nil {
			l.resting, _ = l.s.Store.KeysRestingUntil(l.ctx)
			if l.resting == nil {
				l.resting = map[int64]time.Time{}
			}
		}
		if until, ok := l.resting[ref.id]; ok {
			av.State = "resting"
			av.RestingUntil = &until
			av.RetryAfterSec = max(int(math.Ceil(time.Until(until).Seconds())), 1)
		} else {
			av.State = "down"
		}
	}
	l.memo[slug] = av
	return av, true
}

// tool is a tool's availability: its service's, or for a capability the
// best of its routes', with the free keys of all of them.
func (l *availLookup) tool(t *mcpTool) (toolAvail, bool) {
	if t.capability == "" {
		if t.service == "" {
			return toolAvail{}, false
		}
		return l.service(t.service)
	}
	var best toolAvail
	found := false
	free := 0
	for _, r := range t.routes {
		av, ok := l.service(r)
		if !ok {
			continue
		}
		free += av.FreeKeys
		if !found || av.rank() < best.rank() || (av.rank() == best.rank() && av.RestingUntil != nil && best.RestingUntil != nil && av.RestingUntil.Before(*best.RestingUntil)) {
			best, found = av, true
		}
	}
	best.FreeKeys = free
	return best, found
}

// order sorts hits by score, then by availability: among equally good
// matches a tool with a free key comes before one whose keys rest, and
// that before one that is down. Then the measured success rate: a tool
// with enough evidence comes before one without, the higher rate first. A
// capability comes before a single provider at the same score.
func (l *availLookup) order(hits []toolHit) {
	rank := func(id int) int {
		if av, ok := l.tool(&l.c.tools[id]); ok {
			return av.rank()
		}
		return 0
	}
	rate := func(id int) float64 {
		if e := l.evidence(&l.c.tools[id]); e.Enough() {
			return e.SuccessRate()
		}
		return -1
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if ri, rj := rank(hits[i].id), rank(hits[j].id); ri != rj {
			return ri < rj
		}
		if ri, rj := rate(hits[i].id), rate(hits[j].id); ri != rj {
			return ri > rj
		}
		ci, cj := l.c.tools[hits[i].id].capability != "", l.c.tools[hits[j].id].capability != ""
		if ci != cj {
			return ci
		}
		return hits[i].id < hits[j].id
	})
}

// withCapabilities adds to the hits each capability the app may use that a
// matched service tool serves, scored as its best matching member, so an
// agent asking for "web search" also sees the tool that rotates across
// providers. A member is the service tool that makes the same call as one
// of the capability's routes; a route whose call no tool of its service
// makes counts every tool of that service.
func withCapabilities(c *toolCatalog, ids []int, hits []toolHit) []toolHit {
	byCall := map[string][]int{}
	bySvc := map[string][]int{}
	for _, id := range ids {
		t := &c.tools[id]
		for i, r := range t.routes {
			byCall[t.routeCalls[i]] = append(byCall[t.routeCalls[i]], id)
			bySvc[r] = append(bySvc[r], id)
		}
	}
	if len(bySvc) == 0 {
		return hits
	}
	// Calls some service tool makes: their capabilities need that tool.
	made := c.index().calls
	best := map[int]float64{}
	add := func(cid int, sc float64) {
		if sc > best[cid] {
			best[cid] = sc
		}
	}
	for _, h := range hits {
		t := &c.tools[h.id]
		if t.capability != "" || t.service == "" {
			continue
		}
		if t.example != nil {
			for _, cid := range byCall[endpointKey(t.service, t.example.Method, t.example.Path)] {
				add(cid, h.score)
			}
		}
		for _, cid := range bySvc[t.service] {
			ct := &c.tools[cid]
			for i, r := range ct.routes {
				if r == t.service && !made[ct.routeCalls[i]] {
					add(cid, h.score)
				}
			}
		}
	}
	if len(best) == 0 {
		return hits
	}
	at := map[int]int{}
	for i, h := range hits {
		at[h.id] = i
	}
	for cid, sc := range best {
		if i, ok := at[cid]; ok {
			hits[i].score = math.Max(hits[i].score, sc)
			continue
		}
		hits = append(hits, toolHit{id: cid, score: sc})
	}
	return hits
}

// ---- calling by name ----

// errNameRequired is call_tool without a name.
var errNameRequired = errors.New("name is required")

// resolveCallTool finds the tool a call_tool names, with its arguments.
func (s *Server) resolveCallTool(ctx context.Context, a *store.App, st store.MCPSettings, raw json.RawMessage) (*mcpTool, json.RawMessage, error) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Name == "" {
		return nil, nil, errNameRequired
	}
	if isDiscovery(canonicalTool(in.Name)) {
		return nil, nil, errors.New("call_tool cannot call " + in.Name)
	}
	t := s.mcpTool(ctx, a, st, in.Name)
	if t == nil {
		return nil, nil, errors.New(s.unknownToolText(ctx, a, st, in.Name) + " Use search_tools to find one.")
	}
	return t, in.Arguments, nil
}

// callTool answers call_tool: it runs another tool of this app by name. An
// upstream MCP tool's result comes back as the upstream gave it.
func (s *Server) callTool(ctx context.Context, r *http.Request, a *store.App, st store.MCPSettings, raw json.RawMessage) (map[string]any, error) {
	t, args, err := s.resolveCallTool(ctx, a, st, raw)
	if err != nil {
		return nil, err
	}
	return s.runTool(ctx, r, a, st, t, args)
}

// unknownToolText says a tool name is unknown, with up to three close
// names the app can call.
func (s *Server) unknownToolText(ctx context.Context, a *store.App, st store.MCPSettings, name string) string {
	msg := "unknown or disabled tool: " + name + "."
	if near := s.closeToolNames(ctx, a, st, name, 3); len(near) > 0 {
		msg += " Did you mean " + strings.Join(near, ", ") + "?"
	}
	return msg
}

// closeToolNames are the names of tools this app may call that are a few
// edits from name, or contain it, closest first, as agents see them.
func (s *Server) closeToolNames(ctx context.Context, a *store.App, st store.MCPSettings, name string, n int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 128 {
		return nil
	}
	short := strings.TrimPrefix(name, toolPrefix)
	type cand struct {
		name string
		d    int
	}
	limit := max(2, len(short)/3)
	var cands []cand
	seen := map[string]bool{}
	consider := func(tn string) {
		if seen[tn] {
			return
		}
		seen[tn] = true
		pub := publicTool(tn)
		d := min(levenshtein(name, pub), levenshtein(short, tn))
		if d > limit {
			// A name an agent shortened or lengthened: "google_search"
			// for "serpapi_google_search".
			if len(short) < 4 || (!strings.Contains(tn, short) && !strings.Contains(short, tn)) {
				return
			}
			d = limit + 1 + abs(len(tn)-len(short))
		}
		cands = append(cands, cand{pub, d})
	}
	for _, t := range s.mcpToolSet(ctx, a, st, false) {
		consider(t.Name)
	}
	if st.ServiceTools && a.AllowsProxy() {
		c := s.toolCatalog(ctx)
		off := disabledSet(st.DisabledTools)
		for _, id := range c.view(a).ids {
			t := &c.tools[id]
			if off[t.Name] || (st.ReadOnly && !toolIsReadOnly(*t)) {
				continue
			}
			consider(t.Name)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].d != cands[j].d {
			return cands[i].d < cands[j].d
		}
		return cands[i].name < cands[j].name
	})
	out := []string{}
	for _, c := range cands {
		if len(out) == n {
			break
		}
		out = append(out, c.name)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// levenshtein is the edit distance between two names, byte wise: tool
// names are ASCII.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// runTool calls one resolved tool.
func (s *Server) runTool(ctx context.Context, r *http.Request, a *store.App, st store.MCPSettings, t *mcpTool, args json.RawMessage) (map[string]any, error) {
	switch {
	case t.Name == "search_tools":
		return s.searchTools(ctx, a, st, args)
	case t.Name == "call_tool":
		return s.callTool(ctx, r, a, st, args)
	case t.upstreamMCP:
		return s.callUpstreamMCPTool(ctx, r, a, st, t, args)
	case t.Set == setService:
		return s.callServiceTool(ctx, r, t, args)
	case t.Set == setInsights, t.Set == setManage:
		return s.mcpAdminCall(ctx, a, t.Name, args)
	}
	return s.mcpCall(ctx, r, t.Name, args)
}
