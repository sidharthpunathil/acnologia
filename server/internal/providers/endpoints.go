package providers

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Endpoint templates name the calls a provider's catalog knows: each
// example's path (without its query) and each guide endpoint's path, where
// {name} stands for one path segment or part of one. Measured evidence
// (success rate, latency) is kept per service, method and template, so a
// request to /v1/users/42 counts for the guide's /v1/users/{id}.

// MCPMethod is the method of an upstream MCP tool call, whose template is
// the tool's own name.
const MCPMethod = "MCP"

// EndpointTemplate is one method and path template of a spec.
type EndpointTemplate struct {
	Method string
	Path   string
	re     *regexp.Regexp
	// literal counts the characters outside placeholders: a longer literal
	// is a more specific template.
	literal int
}

var placeholderRe = regexp.MustCompile(`\{[^/{}]*\}`)

// TemplatePath is a path as a template key: no query, no trailing slash.
func TemplatePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p != "/" {
		p = strings.TrimRight(p, "/")
	}
	return p
}

func newTemplate(method, path string) EndpointTemplate {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "GET"
	}
	path = TemplatePath(path)
	t := EndpointTemplate{Method: method, Path: path}
	locs := placeholderRe.FindAllStringIndex(path, -1)
	if len(locs) == 0 {
		t.literal = len(path)
		return t
	}
	var b strings.Builder
	b.WriteString("^")
	last := 0
	for _, l := range locs {
		b.WriteString(regexp.QuoteMeta(path[last:l[0]]))
		b.WriteString("[^/]+")
		t.literal += l[0] - last
		last = l[1]
	}
	b.WriteString(regexp.QuoteMeta(path[last:]))
	t.literal += len(path) - last
	b.WriteString("$")
	t.re = regexp.MustCompile(b.String())
	return t
}

// Matches reports whether a request path falls under the template.
func (t EndpointTemplate) Matches(method, path string) bool {
	if !strings.EqualFold(t.Method, method) {
		return false
	}
	path = TemplatePath(path)
	if t.re == nil {
		return path == t.Path
	}
	return t.re.MatchString(path)
}

// MatchesTemplate reports whether path falls under the template path, for
// any method.
func MatchesTemplate(template, path string) bool {
	t := newTemplate("GET", template)
	return t.Matches("GET", path)
}

type templateSet struct {
	list []EndpointTemplate
}

var (
	templatesMu    sync.Mutex
	templatesCache = map[*Spec]*templateSet{}
)

// EndpointTemplates lists the spec's templates, most specific first.
// Specs are not changed once loaded (an update loads a new one), so the
// list is kept per spec.
func (s *Spec) EndpointTemplates() []EndpointTemplate {
	if s == nil {
		return nil
	}
	templatesMu.Lock()
	defer templatesMu.Unlock()
	if ts, ok := templatesCache[s]; ok {
		return ts.list
	}
	seen := map[string]bool{}
	list := []EndpointTemplate{}
	add := func(method, path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		t := newTemplate(method, path)
		if k := t.Method + " " + t.Path; !seen[k] {
			seen[k] = true
			list = append(list, t)
		}
	}
	for _, ex := range s.Examples {
		add(ex.Method, ex.Path)
	}
	if s.Guide != nil {
		for _, ep := range s.Guide.Endpoints {
			add(ep.Method, ep.Path)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		// Exact paths first, then the longest literal.
		if (list[i].re == nil) != (list[j].re == nil) {
			return list[i].re == nil
		}
		return list[i].literal > list[j].literal
	})
	if len(templatesCache) > 2000 {
		templatesCache = map[*Spec]*templateSet{}
	}
	templatesCache[s] = &templateSet{list: list}
	return list
}

// MatchEndpoint returns the most specific template a request falls under.
// An upstream MCP tool call (method MCP) is its own template.
func (s *Spec) MatchEndpoint(method, path string) (string, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == MCPMethod {
		return path, path != ""
	}
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	for _, t := range s.EndpointTemplates() {
		if t.Matches(method, path) {
			return t.Path, true
		}
	}
	return "", false
}

// DocSpec is the spec that describes a service's API: the owner's provider
// for a service another server shares, else its own.
func DocSpec(provider, peerProvider string) *Spec {
	if peerProvider != "" {
		if spec, ok := Lookup(peerProvider); ok {
			return spec
		}
	}
	return Get(provider)
}

// EndpointKey is the template a request counts under: the most specific
// catalog template, else a generic one (GenericTemplate). matched says
// which.
func (s *Spec) EndpointKey(method, path string) (template string, matched bool) {
	if t, ok := s.MatchEndpoint(method, path); ok {
		return t, true
	}
	return GenericTemplate(path), false
}

var (
	idSegment  = regexp.MustCompile(`^([0-9]+|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)
	hasDigit   = regexp.MustCompile(`[0-9]`)
	hasLetters = regexp.MustCompile(`[A-Za-z]`)
)

// GenericTemplate makes a template of a path no catalog entry covers:
// segments that look like ids (numbers, UUIDs, long mixes of letters and
// digits) become {id}, so calls to one resource share a row.
func GenericTemplate(path string) string {
	path = TemplatePath(path)
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	segs := strings.Split(path, "/")
	for i, sg := range segs {
		if sg == "" {
			continue
		}
		if idSegment.MatchString(sg) || (len(sg) >= 16 && hasDigit.MatchString(sg) && hasLetters.MatchString(sg)) {
			segs[i] = "{id}"
		}
	}
	out := strings.Join(segs, "/")
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}
