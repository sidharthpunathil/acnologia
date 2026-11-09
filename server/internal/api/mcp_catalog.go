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
	"net/http"
	"sort"
	"strings"
	"unicode"

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

var discoveryTools = []mcpTool{
	{Name: "search_tools", Set: setService, Annotations: readOnly("Find tools"),
		Description: "Find the tools for a job across every provider this app can use, for example \"linkedin jobs\", \"tiktok video\", \"google maps reviews\" or \"generate image\". Returns the best matching tools with their input schemas; call one with call_tool (or by its name). An empty query lists the services and how many tools each has. The catalog follows the providers on the server, so new providers show up here at once.",
		InputSchema: obj(map[string]any{
			"query":   str("What you want to do, or a provider, product or site name"),
			"service": str("Optional: only tools of this service slug"),
			"limit":   map[string]any{"type": "integer", "description": "How many tools to return (default 8, max 25)"},
		})},
	{Name: "call_tool", Set: setService, Annotations: map[string]any{"title": "Call a tool", "openWorldHint": true},
		Description: "Call a tool that search_tools found, by name, with its arguments.",
		InputSchema: obj(map[string]any{
			"name":      str("Tool name from search_tools"),
			"arguments": map[string]any{"type": "object", "description": "The tool's arguments, as its input schema describes"},
		}, "name")},
}

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
		keywords: strings.Join([]string{sv.Slug, sv.Name, sv.Provider, sv.Category, src.group, hostsText(spec, src.ex.Path)}, " "),
	}
}

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
		return len(s.toolCatalog(ctx).view(a).ids) > mcpAutoListMax
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

var stopWords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "for": true, "to": true, "on": true, "in": true, "and": true, "or": true, "with": true, "by": true, "me": true, "get": true, "i": true, "want": true, "my": true, "is": true, "from": true, "acnologia": true, "adds": true, "pooled": true, "key": true, "rotates": true, "rate": true, "limits": true}

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
	off := map[string]bool{}
	for _, n := range st.DisabledTools {
		off[n] = true
	}
	// usable is the app's tools, narrowed to one service when asked.
	usable := make([]bool, len(c.tools))
	ids := []int{}
	for _, id := range v.ids {
		t := &c.tools[id]
		if off[t.Name] || (in.Service != "" && t.service != in.Service && t.capability != in.Service) {
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
		}
		out = append(out, m)
	}
	res := map[string]any{"tools": out, "matches": total, "catalog_size": len(ids)}
	if strings.TrimSpace(in.Query) == "" || total == 0 {
		res["services"] = directory()
	}
	if total == 0 {
		res["hint"] = "No tool matched. Try a provider or site name, or other words; the services list shows what exists. proxy_request calls any endpoint of a service, and service_guide lists its endpoints."
	} else {
		res["hint"] = "Call a tool with call_tool {name, arguments}, or by its name."
	}
	return res, nil
}

// callTool answers call_tool: it runs another tool of this app by name.
func (s *Server) callTool(ctx context.Context, r *http.Request, a *store.App, st store.MCPSettings, raw json.RawMessage) (map[string]any, error) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Name == "" {
		return nil, errors.New("name is required")
	}
	if isDiscovery(in.Name) {
		return nil, errors.New("call_tool cannot call " + in.Name)
	}
	t := s.mcpTool(ctx, a, st, in.Name)
	if t == nil {
		return nil, errors.New("unknown or disabled tool: " + in.Name + ". Use search_tools to find one.")
	}
	return s.runTool(ctx, r, a, st, t, in.Arguments)
}

// runTool calls one resolved tool.
func (s *Server) runTool(ctx context.Context, r *http.Request, a *store.App, st store.MCPSettings, t *mcpTool, args json.RawMessage) (map[string]any, error) {
	switch {
	case t.Name == "search_tools":
		return s.searchTools(ctx, a, st, args)
	case t.Name == "call_tool":
		return s.callTool(ctx, r, a, st, args)
	case t.Set == setService:
		return s.callServiceTool(ctx, r, t, args)
	case t.Set == setInsights, t.Set == setManage:
		return s.mcpAdminCall(ctx, a, t.Name, args)
	}
	return s.mcpCall(ctx, r, t.Name, args)
}
