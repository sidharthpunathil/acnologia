// Package keyparse pulls API keys out of pasted or dropped text: JSON
// arrays, CSV with a header row, .env files, and free form lists in any mix of
// separators. The web dashboard mirrors these rules in web/src/lib/keyparse.ts;
// testdata/cases.json holds the shared cases both parsers must pass.
//
// Rules, in order:
//
//  1. JSON: when the trimmed text is a JSON array, each string is a secret and
//     each object takes its secret from key, secret, api_key, apiKey or token
//     and its label from label, name or account (first non-empty wins). A
//     secret here must be 1 to 512 characters with no whitespace. Entries that
//     do not fit go to Ignored as their JSON text.
//  2. Otherwise the text is split into lines, each trimmed. Empty lines and
//     lines starting with # or // are skipped.
//  3. CSV: when the first remaining line, split on commas (or tabs when it
//     has no comma), has a cell named label or name and a cell named key,
//     secret or api_key (case insensitive, quotes stripped), it is a header.
//     Every later line is split the same way; the secret cell must be 1 to
//     512 characters with no whitespace, otherwise the whole line is ignored.
//  4. env: a line NAME=value or NAME: value (a space after the colon), with
//     an optional "export " prefix and NAME matching
//     [A-Za-z_][A-Za-z0-9_.-]{0,63}, is label NAME and secret value, when the
//     cleaned value is key-like. Otherwise the line falls through to rule 5.
//  5. Tokens: the line is split on commas, semicolons, pipes and whitespace.
//     Each token is cleaned: leading quotes, backticks and ( [ { < and
//     trailing quotes, backticks, ) ] } > . , ; : ! ? are stripped. A key-like
//     token is 16 to 512 characters of [A-Za-z0-9_\-.:+/=], has no "://",
//     and contains a digit or is at least 24 characters long. A label-like
//     token is 1 to 64 characters, not key-like, has no "://" and contains a
//     letter (so list bullets such as "-" or "1." never become labels). A
//     label-like token right before a key-like token labels it; other
//     key-like tokens are unlabeled; every other token goes to Ignored.
//
// Keys are deduplicated by secret in input order. A duplicate keeps the
// first occurrence's position, and lends its label only when the first had
// none. Labels are cut to 64 characters. At most MaxKeys keys are returned;
// Overflow counts the rest. Ignored holds at most MaxIgnored entries.
package keyparse

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxKeys     = 5000
	MaxIgnored  = 1000
	MaxSecret   = 512
	MaxLabel    = 64
	minKeyLen   = 16
	longKeyLen  = 24
	headerLines = 1
)

type Key struct {
	Label  string `json:"label"`
	Secret string `json:"secret"`
}

type Result struct {
	Keys    []Key    `json:"keys"`
	Ignored []string `json:"ignored"`
	// Overflow is how many distinct keys were found past MaxKeys.
	Overflow int `json:"overflow,omitempty"`
}

// Parse extracts keys from text. It never fails: anything it cannot use goes
// to Ignored.
func Parse(text string) Result {
	c := &collector{seen: map[string]int{}}
	if !c.parseJSON(text) {
		c.parseText(text)
	}
	if c.res.Keys == nil {
		c.res.Keys = []Key{}
	}
	if c.res.Ignored == nil {
		c.res.Ignored = []string{}
	}
	return c.res
}

type collector struct {
	res  Result
	seen map[string]int // secret -> index in res.Keys, or -1 when over the cap
}

func (c *collector) add(label, secret string) {
	label = cutLabel(strings.TrimSpace(label))
	if i, ok := c.seen[secret]; ok {
		if i >= 0 && c.res.Keys[i].Label == "" && label != "" {
			c.res.Keys[i].Label = label
		}
		return
	}
	if len(c.res.Keys) >= MaxKeys {
		c.seen[secret] = -1
		c.res.Overflow++
		return
	}
	c.seen[secret] = len(c.res.Keys)
	c.res.Keys = append(c.res.Keys, Key{Label: label, Secret: secret})
}

func (c *collector) ignore(s string) {
	if s == "" || len(c.res.Ignored) >= MaxIgnored {
		return
	}
	c.res.Ignored = append(c.res.Ignored, s)
}

func cutLabel(s string) string {
	if utf8.RuneCountInString(s) <= MaxLabel {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:MaxLabel]))
}

// plainSecret is the check for secrets given in an explicit field (JSON or a
// CSV column): present, not too long, one token.
func plainSecret(s string) bool {
	return s != "" && len(s) <= MaxSecret && !strings.ContainsFunc(s, unicode.IsSpace)
}

// ---- JSON ----

var (
	secretFields = []string{"key", "secret", "api_key", "apiKey", "token"}
	labelFields  = []string{"label", "name", "account"}
)

func (c *collector) parseJSON(text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "[") {
		return false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(t), &arr); err != nil {
		return false
	}
	for _, raw := range arr {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s = strings.TrimSpace(s); plainSecret(s) {
				c.add("", s)
			} else {
				c.ignore(compact(raw))
			}
			continue
		}
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil || obj == nil {
			c.ignore(compact(raw))
			continue
		}
		secret, label := firstString(obj, secretFields), firstString(obj, labelFields)
		if !plainSecret(secret) {
			c.ignore(compact(raw))
			continue
		}
		c.add(label, secret)
	}
	return true
}

func firstString(obj map[string]any, fields []string) string {
	for _, f := range fields {
		if s, ok := obj[f].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) == nil {
		return b.String()
	}
	return string(raw)
}

// ---- text ----

var envRe = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_.-]{0,63})\s*(?:=\s*|:\s+)(.+)$`)

func (c *collector) parseText(text string) {
	lines := []string{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "//") {
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return
	}
	if sep, labelCol, secretCol, ok := csvHeader(lines[0]); ok {
		for _, l := range lines[headerLines:] {
			cells := splitCells(l, sep)
			secret := ""
			if secretCol < len(cells) {
				secret = cells[secretCol]
			}
			if !plainSecret(secret) {
				c.ignore(l)
				continue
			}
			label := ""
			if labelCol >= 0 && labelCol < len(cells) {
				label = cells[labelCol]
			}
			c.add(label, secret)
		}
		return
	}
	for _, l := range lines {
		c.parseLine(l)
	}
}

func splitCells(line, sep string) []string {
	cells := strings.Split(line, sep)
	for i := range cells {
		cells[i] = strings.TrimSpace(stripQuotes(strings.TrimSpace(cells[i])))
	}
	return cells
}

func stripQuotes(s string) string {
	return strings.Trim(s, "\"'`")
}

// csvHeader reports whether line is a CSV header naming a label and a secret
// column.
func csvHeader(line string) (sep string, labelCol, secretCol int, ok bool) {
	sep = "\t"
	if strings.Contains(line, ",") {
		sep = ","
	} else if !strings.Contains(line, "\t") {
		return "", 0, 0, false
	}
	labelCol, secretCol = -1, -1
	nameCol := -1
	for i, cell := range splitCells(line, sep) {
		switch strings.ToLower(cell) {
		case "label":
			if labelCol < 0 {
				labelCol = i
			}
		case "name":
			if nameCol < 0 {
				nameCol = i
			}
		case "key", "secret", "api_key":
			if secretCol < 0 {
				secretCol = i
			}
		}
	}
	if labelCol < 0 {
		labelCol = nameCol
	}
	return sep, labelCol, secretCol, labelCol >= 0 && secretCol >= 0
}

func (c *collector) parseLine(line string) {
	if m := envRe.FindStringSubmatch(line); m != nil {
		v := cleanToken(strings.TrimSpace(m[2]))
		if keyLike(v) {
			c.add(m[1], v)
			return
		}
	}
	tokens := strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || unicode.IsSpace(r)
	})
	pending := ""
	for _, raw := range tokens {
		tok := cleanToken(raw)
		switch {
		case tok == "":
			c.ignore(pending)
			pending = ""
		case keyLike(tok):
			c.add(pending, tok)
			pending = ""
		case labelLike(tok):
			c.ignore(pending)
			pending = tok
		default:
			c.ignore(pending)
			pending = ""
			c.ignore(tok)
		}
	}
	c.ignore(pending)
}

const (
	leadingJunk  = "\"'`([{<"
	trailingJunk = "\"'`)]}>.,;:!?"
)

func cleanToken(s string) string {
	for {
		t := strings.TrimLeft(s, leadingJunk)
		t = strings.TrimRight(t, trailingJunk)
		if t == s {
			return s
		}
		s = t
	}
}

func keyChar(r rune) bool {
	return r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		strings.ContainsRune("_-.:+/=", r))
}

// KeyLike reports whether a token looks like an API key under rule 5.
func KeyLike(s string) bool { return keyLike(s) }

func keyLike(s string) bool {
	if len(s) < minKeyLen || len(s) > MaxSecret || strings.Contains(s, "://") {
		return false
	}
	digit := false
	for _, r := range s {
		if !keyChar(r) {
			return false
		}
		if r >= '0' && r <= '9' {
			digit = true
		}
	}
	return digit || len(s) >= longKeyLen
}

func labelLike(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > MaxLabel || keyLike(s) || strings.Contains(s, "://") {
		return false
	}
	return strings.ContainsFunc(s, unicode.IsLetter)
}
