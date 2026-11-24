package providers

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Key affinity: some providers keep what a request creates (a prediction,
// an actor run, a batch, an uploaded file) on the account that created it
// and answer 404 to any other account. A spec's affinity rules say which
// requests create such a resource, where its id is in the answer, and which
// later requests name it, so the proxy sends those to the key that created
// it (api/proxy_affinity.go).

// Limits on affinity rules.
const (
	MaxAffinityRules  = 20
	MaxAffinityFollow = 20
	// MaxResourceID is the longest resource id recorded.
	MaxResourceID = 200
)

// AffinityHit is what a spec's affinity rules say about one request.
type AffinityHit struct {
	// FollowID is the id of a resource the request is about, "" when none.
	FollowID string
	// CreateID is the JSON path of the id the answer gives a resource the
	// request creates, "" when it creates none.
	CreateID string
	// Related are JSON paths of more ids in the answer that live on the
	// same account.
	Related []string
}

// Grab reports whether the answer must be read for ids.
func (h AffinityHit) Grab() bool { return h.CreateID != "" || len(h.Related) > 0 }

// MatchAffinity matches a request against the spec's affinity rules.
func (s *Spec) MatchAffinity(method, path string, body []byte) AffinityHit {
	var hit AffinityHit
	if len(s.Affinity) == 0 {
		return hit
	}
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var parsed any
	parsedOK := false
	for i := range s.Affinity {
		r := &s.Affinity[i]
		if hit.CreateID == "" && strings.EqualFold(r.Create.Method, method) && pathMatches(r.Create.Path, path) {
			hit.CreateID = r.ID
			hit.Related = append(hit.Related, r.Related...)
		}
		for _, f := range r.Follow {
			if f.Method != "" && !strings.EqualFold(f.Method, method) {
				continue
			}
			id, ok := followMatch(f.Path, path)
			if !ok {
				continue
			}
			if f.Body != "" {
				if !parsedOK {
					parsed, parsedOK = parseJSON(body), true
				}
				id = idAt(parsed, f.Body)
			}
			if id == "" {
				continue
			}
			if hit.FollowID == "" {
				hit.FollowID = id
				if f.Body == "" {
					hit.Related = append(hit.Related, r.Related...)
				}
			}
			break
		}
	}
	return hit
}

// AffinityIDs reads the ids an answer gives: the created resource's (at
// createID) and the related ones.
func AffinityIDs(body []byte, createID string, related []string) []string {
	v := parseJSON(body)
	if v == nil {
		return nil
	}
	var out []string
	add := func(p string) {
		if id := idAt(v, p); id != "" {
			out = append(out, id)
		}
	}
	if createID != "" {
		add(createID)
	}
	for _, p := range related {
		add(p)
	}
	return out
}

func parseJSON(body []byte) any {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil
	}
	return v
}

// idAt is the string or number at path, "" when there is none or it is
// too long to be an id.
func idAt(v any, path string) string {
	if v == nil {
		return ""
	}
	x, ok := JSONPath(v, path)
	if !ok {
		return ""
	}
	var id string
	switch t := x.(type) {
	case string:
		id = t
	case json.Number:
		id = t.String()
	case float64:
		id = strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
	if id == "" || len(id) > MaxResourceID {
		return ""
	}
	return id
}

// followMatch matches a follow path pattern segment by segment and returns
// the {id} segment. * matches any one segment. The path may go on past the
// pattern unless the pattern ends with $.
func followMatch(pattern, path string) (string, bool) {
	exact := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(xs) < len(ps) || (exact && len(xs) != len(ps)) {
		return "", false
	}
	id := ""
	for i, p := range ps {
		switch {
		case p == "{id}":
			if xs[i] == "" {
				return "", false
			}
			id = xs[i]
		case p == "*":
			if xs[i] == "" {
				return "", false
			}
		case p != xs[i]:
			return "", false
		}
	}
	return id, true
}

func (s *Spec) validateAffinity(add func(string, ...any)) {
	if len(s.Affinity) > MaxAffinityRules {
		add("affinity: at most %d rules", MaxAffinityRules)
	}
	methodOK := func(m string) bool {
		switch strings.ToUpper(m) {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			return true
		}
		return false
	}
	pathOK := func(p string) bool {
		return strings.HasPrefix(p, "/") && len(p) <= 300 && !strings.ContainsAny(p, "?#")
	}
	for i, r := range s.Affinity {
		label := "affinity[" + strconv.Itoa(i) + "]"
		if !methodOK(r.Create.Method) {
			add("%s: create.method must be GET, POST, PUT, PATCH or DELETE", label)
		}
		if !pathOK(r.Create.Path) || strings.Contains(r.Create.Path, "{id}") {
			add("%s: create.path must be a path starting with /, without {id}, a query or a fragment", label)
		}
		if r.ID == "" || len(r.ID) > 100 {
			add("%s: id must be the JSON path of the new resource's id in the answer, such as id or data.id", label)
		}
		for j, p := range r.Related {
			if p == "" || len(p) > 100 {
				add("%s: related[%d] must be a JSON path", label, j)
			}
		}
		if len(r.Follow) == 0 || len(r.Follow) > MaxAffinityFollow {
			add("%s: follow needs 1 to %d requests", label, MaxAffinityFollow)
		}
		for j, f := range r.Follow {
			fl := label + ".follow[" + strconv.Itoa(j) + "]"
			if f.Method != "" && !methodOK(f.Method) {
				add("%s: method must be GET, POST, PUT, PATCH or DELETE", fl)
			}
			if !pathOK(f.Path) {
				add("%s: path must be a path starting with /, without a query or a fragment", fl)
				continue
			}
			n := 0
			for _, seg := range strings.Split(strings.TrimSuffix(f.Path, "$"), "/") {
				if seg == "{id}" {
					n++
				} else if strings.Contains(seg, "{") || strings.Contains(seg, "}") {
					n = -100
				}
			}
			switch {
			case n < 0:
				add("%s: {id} must be a whole path segment", fl)
			case f.Body == "" && n != 1:
				add("%s: path must hold {id} once, as a whole segment, or set body to the JSON path of the id in the request body", fl)
			case f.Body != "" && n != 0:
				add("%s: a follow with body takes the id from the request body, so its path has no {id}", fl)
			case len(f.Body) > 100:
				add("%s: body must be a JSON path", fl)
			}
		}
	}
}
