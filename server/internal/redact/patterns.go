package redact

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Match is one secret found by pattern: s[Start:End].
type Match struct {
	Start, End int
	Rule       string
}

type rule struct {
	name  string
	re    *regexp.Regexp
	group int               // submatch holding the secret; 0 is the whole match
	ok    func(string) bool // extra check on the secret, nil accepts it
}

// Value characters never include a backslash or a quote, so a redaction
// inside JSON text cannot eat an escape and break the document.
var rules = []rule{
	// Distinctive prefixes, matched even when glued to other text.
	{name: "anthropic", re: regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`)},
	{name: "google", re: regexp.MustCompile(`AIza[0-9A-Za-z_\-]{30,}`)},
	{name: "github", re: regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})`)},
	{name: "slack", re: regexp.MustCompile(`xox[abposr]-[A-Za-z0-9\-]{10,}`)},
	{name: "tavily", re: regexp.MustCompile(`tvly-[A-Za-z0-9_\-]{12,}`)},
	{name: "gitlab", re: regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{16,}`)},
	{name: "aws", re: regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)},
	{name: "stripe", re: regexp.MustCompile(`(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{16,}`)},
	{name: "jwt", re: regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`)},
	// Shorter prefixes need a boundary in front ("task-..." is not "sk-...").
	{name: "openai", re: regexp.MustCompile(`(?:^|[^A-Za-z0-9_\-])(sk-[A-Za-z0-9_\-]{16,})`), group: 1, ok: hasDigit},
	{name: "prefixed", re: regexp.MustCompile(`(?:^|[^A-Za-z0-9_\-])((?:hf|gsk|r8|acn|acnadm|acninv|acnseal[0-9]*|cog|cogadm|whsec)_[A-Za-z0-9_\-]{20,}|(?:pplx|xai|fc)-[A-Za-z0-9_\-]{20,})`), group: 1, ok: notServerID},
	// Credentials by position.
	// The access gate's cookie (acnologia_gate=<issued>.<mac>) is hex after
	// a timestamp, which the entropy rule leaves alone, so it gets its own.
	{name: "gate_cookie", re: regexp.MustCompile(`acnologia_gate=([^\s;,"'\\]{8,})`), group: 1},
	{name: "bearer", re: regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+([A-Za-z0-9_\-.~+/=:]{8,})`), group: 1, ok: secretish},
	{name: "header", re: regexp.MustCompile(`(?i)(?:x-api-key|x-goog-api-key|api-key|x-auth-token|x-access-token|x-acnologia-access|authorization)["']?\s*[:=]\s*["']?([A-Za-z0-9_\-.~+/=]{8,})`), group: 1, ok: notPlaceholder},
	{name: "assign", re: regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_\-])["']?(api[_\-]?key|apikey|access[_\-]?token|auth[_\-]?token|refresh[_\-]?token|client[_\-]?secret|api[_\-]?secret|secret[_\-]?key|access[_\-]?key|private[_\-]?key|token|secret|password|passwd|key)["']?\s*[:=]\s*["']?([^\s"'\\&,;}\]<>()]{6,})`), group: 2},
	// Anything long and random looking.
	{name: "entropy", re: regexp.MustCompile(`[A-Za-z0-9_\-+/=]{24,}`), ok: entropic},
}

// Find returns the secrets in s that match a pattern, in order and without
// overlaps (the earliest, then the longest, wins).
func Find(s string) []Match {
	if len(s) < 6 {
		return nil
	}
	var all []Match
	for _, r := range rules {
		for _, m := range r.re.FindAllStringSubmatchIndex(s, -1) {
			gi := 2 * r.group
			if len(m) <= gi+1 || m[gi] < 0 {
				continue
			}
			start, end := m[gi], m[gi+1]
			v := s[start:end]
			if r.name == "assign" && !assignOK(strings.ToLower(s[m[2]:m[3]]), v) {
				continue
			}
			if r.ok != nil && !r.ok(v) {
				continue
			}
			all = append(all, Match{Start: start, End: end, Rule: r.name})
		}
	}
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Start != all[j].Start {
			return all[i].Start < all[j].Start
		}
		return all[i].End > all[j].End
	})
	out := all[:0]
	end := -1
	for _, m := range all {
		if m.Start < end {
			continue
		}
		out = append(out, m)
		end = m.End
	}
	return out
}

// Patterns replaces every pattern match in s with "[secret ...abcd]".
func Patterns(s string) string {
	ms := Find(s)
	if len(ms) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := 0
	for _, m := range ms {
		b.WriteString(s[prev:m.Start])
		b.WriteString("[secret " + Hint(s[m.Start:m.End]) + "]")
		prev = m.End
	}
	b.WriteString(s[prev:])
	return b.String()
}

// serverID is the exact shape of a server id (acn_srv_ and 20 lowercase
// base32 characters). Server ids are public, published at
// /.well-known/acnologia, so they are not secrets. Only that exact shape is
// let through: a random acn_ token is longer and uses base64url.
var serverID = regexp.MustCompile(`^acn_srv_[a-z2-7]{20}$`)

func notServerID(s string) bool { return !serverID.MatchString(s) }

func hasDigit(s string) bool { return strings.ContainsAny(s, "0123456789") }

func notPlaceholder(s string) bool { return !strings.HasPrefix(s, "[") }

// secretish is a token that looks generated rather than written: it has a
// digit, or it is long and mixes upper and lower case.
func secretish(s string) bool {
	if hasDigit(s) {
		return true
	}
	up, low := false, false
	for _, c := range s {
		up = up || c >= 'A' && c <= 'Z'
		low = low || c >= 'a' && c <= 'z'
	}
	return len(s) >= 20 && up && low
}

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// assignOK decides whether "name = value" carries a secret.
func assignOK(name, v string) bool {
	lv := strings.ToLower(v)
	switch {
	case strings.HasPrefix(v, "[") || strings.HasPrefix(v, "...") || strings.HasPrefix(v, "…") ||
		strings.HasPrefix(v, "$") || strings.HasPrefix(v, "%") || strings.HasPrefix(v, "{"):
		return false // placeholders, templates and variables
	case lv == "null" || lv == "none" || lv == "true" || lv == "false" || lv == "undefined" ||
		lv == "redacted" || strings.Trim(v, "*x•.") == "":
		return false
	case envName.MatchString(v) || strings.Contains(lv, "env.") || strings.Contains(lv, "getenv"):
		return false // a reference to where the secret lives, not the secret
	}
	// "key" and "token" are common field names for ordinary values (a sort
	// key, a pagination token), so their value must look generated.
	if name == "key" || name == "token" {
		return secretish(v)
	}
	return true
}

// entropic reports whether a long token looks random: it mixes upper case,
// lower case and digits, switches between them often, and is not an
// identifier, a hash, a UUID or a path.
func entropic(s string) bool {
	if len(s) < 24 {
		return false
	}
	for _, p := range []string{"toolu_", "srvtoolu_", "msg_", "req_", "itm_", "act_"} {
		if strings.HasPrefix(s, p) {
			return false // Anthropic and Acnologia ids
		}
	}
	up, low, dig := 0, 0, 0
	switches := 0
	prev := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		cls := 3
		switch {
		case c >= 'A' && c <= 'Z':
			cls, up = 0, up+1
		case c >= 'a' && c <= 'z':
			cls, low = 1, low+1
		case c >= '0' && c <= '9':
			cls, dig = 2, dig+1
		}
		if prev >= 0 && cls != prev {
			switches++
		}
		prev = cls
	}
	if up == 0 || low == 0 || dig == 0 {
		return false // hex hashes, UUIDs, lower case slugs, words
	}
	if float64(switches)/float64(len(s)-1) < 0.3 || lowerRuns(s) >= 2.5 {
		return false // camelCase and PascalCase identifiers with a version in them
	}
	if strings.Contains(s, "/") && pathLike(s) {
		return false
	}
	return shannon(s) >= 3.5
}

// lowerRuns is the average length of the runs of lower case letters. Words
// in identifiers make long runs; random tokens average under two.
func lowerRuns(s string) float64 {
	runs, letters, in := 0, 0, false
	for i := 0; i < len(s); i++ {
		low := s[i] >= 'a' && s[i] <= 'z'
		if low {
			letters++
			if !in {
				runs++
			}
		}
		in = low
	}
	if runs == 0 {
		return 0
	}
	return float64(letters) / float64(runs)
}

// pathLike reports whether most segments of a slash separated token are
// words, as in a file path or a URL path.
func pathLike(s string) bool {
	segs, wordy := 0, 0
	for _, seg := range strings.Split(s, "/") {
		if seg == "" {
			continue
		}
		segs++
		if word(seg) {
			wordy++
		}
	}
	return segs > 1 && 2*wordy >= segs
}

// word is a lower case word, maybe capitalized, maybe with a version or
// separators: Documents, v2, anthropic-sdk-go, read_me.
func word(s string) bool {
	letters := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			letters++
		case c >= 'A' && c <= 'Z':
			if i > 0 && s[i-1] >= 'a' && s[i-1] <= 'z' {
				return false
			}
			letters++
		case c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return letters > 0
}

func shannon(s string) float64 {
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	h := 0.0
	n := float64(len(s))
	for _, f := range freq {
		if f > 0 {
			p := float64(f) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}
