package providers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Credentials with more than one part.
//
// Most providers take one API key, but some take a login and a password
// (DataForSEO uses HTTP Basic auth) or several values in separate headers or
// query params (an app id and an app key, a client id and a secret). A key
// stored for such a provider is still one secret string:
//
//   - basic: "login:password". Apply sends Authorization: Basic base64(...).
//   - fields: a JSON object of part name to value, such as
//     {"app_id":"...","app_key":"..."}. Apply puts each part where its
//     AuthField says.
//
// NormalizeSecret turns what a person pastes or types (the parts, the joined
// form, a base64 Basic value, a JSON object) into that one canonical string.

// AuthField is one part of a multi part credential (auth type fields).
type AuthField struct {
	// Key names the part in the stored secret, such as app_id.
	Key string `json:"key"`
	// Label is what the add key form shows, such as "App ID".
	Label string `json:"label,omitempty"`
	// In is header or query.
	In string `json:"in"`
	// Name is the header or query param the part is sent as.
	Name string `json:"name"`
	// Prefix goes before the value, such as "Token ".
	Prefix string `json:"prefix,omitempty"`
	// Public parts (a login, an app id) are not secret on their own; the
	// form shows them in plain text. The key is still stored encrypted.
	Public bool `json:"public,omitempty"`
}

// CredentialPart is one input of the add key form.
type CredentialPart struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Public bool   `json:"public,omitempty"`
}

var fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// MultiPart reports whether a key for this auth is made of several parts.
func (a Auth) MultiPart() bool { return a.Type == "basic" || a.Type == "fields" }

// Parts lists the inputs a key for this auth needs, in order. A single key
// auth has one part, "secret".
func (a Auth) Parts() []CredentialPart {
	switch a.Type {
	case "basic":
		login, password := "Login", "Password"
		if len(a.Fields) == 2 {
			if a.Fields[0].Label != "" {
				login = a.Fields[0].Label
			}
			if a.Fields[1].Label != "" {
				password = a.Fields[1].Label
			}
		}
		return []CredentialPart{{Key: "login", Label: login, Public: true}, {Key: "password", Label: password}}
	case "fields":
		out := make([]CredentialPart, 0, len(a.Fields))
		for _, f := range a.Fields {
			l := f.Label
			if l == "" {
				l = f.Key
			}
			out = append(out, CredentialPart{Key: f.Key, Label: l, Public: f.Public})
		}
		return out
	}
	return []CredentialPart{{Key: "secret", Label: "API key"}}
}

// validateAuth checks the auth block of a spec.
func (a Auth) validate(add func(string, ...any)) {
	switch a.Type {
	case "query", "header":
		if strings.TrimSpace(a.Name) == "" {
			add("auth.name is required for %s auth", a.Type)
		}
	case "bearer":
	case "basic":
		if len(a.Fields) != 0 && len(a.Fields) != 2 {
			add("basic auth takes no fields, or two (login and password) to name them")
		}
	case "fields":
		if len(a.Fields) < 1 || len(a.Fields) > 8 {
			add("fields auth needs 1 to 8 fields")
		}
		seen := map[string]bool{}
		for i, f := range a.Fields {
			if !fieldKeyRe.MatchString(f.Key) {
				add("auth.fields[%d].key must be lowercase letters, digits and underscores", i)
			}
			if seen[f.Key] {
				add("auth.fields[%d].key %q is used twice", i, f.Key)
			}
			seen[f.Key] = true
			if f.In != "header" && f.In != "query" {
				add("auth.fields[%d].in must be header or query", i)
			}
			if strings.TrimSpace(f.Name) == "" {
				add("auth.fields[%d].name is required", i)
			}
		}
	default:
		add("auth.type must be query, header, bearer, basic or fields")
	}
}

// NormalizeSecret turns a credential as typed or pasted into the form this
// auth stores. parts, when given, are the form's inputs by part key and win
// over raw. Single key auth returns raw trimmed.
func (a Auth) NormalizeSecret(raw string, parts map[string]string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !a.MultiPart() {
		if raw == "" && len(parts) > 0 {
			raw = strings.TrimSpace(parts["secret"])
		}
		if raw == "" {
			return "", fmt.Errorf("the key is empty")
		}
		return raw, nil
	}
	want := a.Parts()
	vals := map[string]string{}
	for k, v := range parts {
		vals[k] = strings.TrimSpace(v)
	}
	if len(vals) == 0 && raw != "" {
		vals = splitCredential(raw, a.Type, want)
	}
	for _, p := range want {
		if vals[p.Key] == "" {
			return "", fmt.Errorf("%s is missing: this provider needs %s", p.Label, partList(want))
		}
		if strings.ContainsAny(vals[p.Key], "\r\n") {
			return "", fmt.Errorf("%s cannot span lines", p.Label)
		}
	}
	if a.Type == "basic" {
		if strings.Contains(vals["login"], ":") {
			return "", fmt.Errorf("a Basic auth login cannot contain a colon")
		}
		return vals["login"] + ":" + vals["password"], nil
	}
	// Ordered JSON, so the same parts always store as the same text (the
	// duplicate check compares fingerprints).
	var b strings.Builder
	b.WriteByte('{')
	for i, p := range want {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p.Key)
		v, _ := json.Marshal(vals[p.Key])
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.String(), nil
}

func partList(ps []CredentialPart) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Label
	}
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// splitCredential reads a pasted credential: a JSON object, "Basic xyz",
// base64 of login:password, or the parts joined by a colon or whitespace.
func splitCredential(raw, typ string, want []CredentialPart) map[string]string {
	out := map[string]string{}
	if strings.HasPrefix(raw, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) == nil {
			for k, v := range m {
				if s, ok := v.(string); ok {
					out[k] = strings.TrimSpace(s)
				}
			}
			// Common spellings of a login and password.
			if typ == "basic" {
				for _, alt := range []string{"username", "user", "api_login", "email", "client_id"} {
					if out["login"] == "" && out[alt] != "" {
						out["login"] = out[alt]
					}
				}
				for _, alt := range []string{"pass", "api_password", "secret", "client_secret", "api_key"} {
					if out["password"] == "" && out[alt] != "" {
						out["password"] = out[alt]
					}
				}
			}
			return out
		}
	}
	s := raw
	if v, ok := strings.CutPrefix(s, "Basic "); ok {
		s = strings.TrimSpace(v)
	}
	if typ == "basic" && !strings.ContainsAny(s, ": \t") {
		if dec, err := base64.StdEncoding.DecodeString(s); err == nil && strings.Contains(string(dec), ":") && isPrintable(string(dec)) {
			s = string(dec)
		}
	}
	var pieces []string
	if typ == "basic" {
		if i := strings.IndexByte(s, ':'); i > 0 {
			pieces = []string{s[:i], s[i+1:]}
		} else {
			pieces = strings.Fields(s)
		}
	} else {
		pieces = strings.Split(s, ":")
		if len(pieces) != len(want) {
			pieces = strings.Fields(s)
		}
	}
	if len(pieces) != len(want) {
		return out
	}
	for i, p := range want {
		out[p.Key] = strings.TrimSpace(pieces[i])
	}
	return out
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// SecretParts reads a stored multi part secret back into its parts. A
// single key auth returns {"secret": secret}.
func (a Auth) SecretParts(secret string) map[string]string {
	switch a.Type {
	case "basic":
		login, password, _ := strings.Cut(secret, ":")
		return map[string]string{"login": login, "password": password}
	case "fields":
		out := map[string]string{}
		var m map[string]any
		if json.Unmarshal([]byte(secret), &m) == nil {
			for k, v := range m {
				if s, ok := v.(string); ok {
					out[k] = s
				}
			}
		}
		return out
	}
	return map[string]string{"secret": secret}
}

// SecretHint is the "...abcd" shown for a stored secret: the last part's
// last four characters, so a JSON secret does not show as "...\"}".
func (a Auth) SecretHint(secret string) string {
	s := secret
	switch a.Type {
	case "basic":
		_, s, _ = strings.Cut(secret, ":")
	case "fields":
		ps := a.Parts()
		if len(ps) > 0 {
			s = a.SecretParts(secret)[ps[len(ps)-1].Key]
		}
	}
	if len(s) <= 4 {
		return "..."
	}
	return "..." + s[len(s)-4:]
}

// SecretVariants are the forms of a stored secret that can appear on the
// wire or in a paste: the secret, its parts, and the Basic base64 value.
// The redactor scrubs them all. Parts shorter than 8 characters are left
// out, since they would match ordinary text.
func SecretVariants(secret string) []string {
	out := []string{secret}
	if strings.HasPrefix(secret, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(secret), &m) == nil {
			for _, v := range m {
				if s, ok := v.(string); ok && len(s) >= 8 {
					out = append(out, s)
				}
			}
		}
		return out
	}
	if login, password, ok := strings.Cut(secret, ":"); ok && password != "" && !strings.Contains(secret, "://") {
		out = append(out, base64.StdEncoding.EncodeToString([]byte(secret)))
		if len(password) >= 8 {
			out = append(out, password)
		}
		_ = login // a login alone is not secret
	}
	return out
}

// applyQuery sets the query params this auth uses.
func (a Auth) applyQuery(q url.Values, secret string) {
	switch a.Type {
	case "query":
		q.Set(a.Name, a.Prefix+secret)
	case "fields":
		parts := a.SecretParts(secret)
		for _, f := range a.Fields {
			if f.In == "query" {
				q.Set(f.Name, f.Prefix+parts[f.Key])
			}
		}
	}
}

// ApplyHeaders sets the headers this auth uses.
func (a Auth) ApplyHeaders(h http.Header, secret string) {
	switch a.Type {
	case "query":
	case "bearer":
		name := a.Name
		if name == "" {
			name = "Authorization"
		}
		prefix := a.Prefix
		if prefix == "" {
			prefix = "Bearer "
		}
		h.Set(name, prefix+secret)
	case "basic":
		name := a.Name
		if name == "" {
			name = "Authorization"
		}
		h.Set(name, "Basic "+base64.StdEncoding.EncodeToString([]byte(secret)))
	case "fields":
		parts := a.SecretParts(secret)
		for _, f := range a.Fields {
			if f.In == "header" {
				h.Set(f.Name, f.Prefix+parts[f.Key])
			}
		}
	default:
		h.Set(a.Name, a.Prefix+secret)
	}
}

// ApplyQuery sets the query params this auth uses.
func (a Auth) ApplyQuery(q url.Values, secret string) { a.applyQuery(q, secret) }

// HeaderNames are the request headers the credential replaces.
func (a Auth) HeaderNames() []string {
	switch a.Type {
	case "query":
		return nil
	case "bearer", "basic":
		if a.Name == "" {
			return []string{"Authorization"}
		}
		return []string{a.Name}
	case "fields":
		out := []string{}
		for _, f := range a.Fields {
			if f.In == "header" {
				out = append(out, f.Name)
			}
		}
		return out
	}
	return []string{a.Name}
}

// QueryNames are the query params the credential replaces.
func (a Auth) QueryNames() []string {
	switch a.Type {
	case "query":
		return []string{a.Name}
	case "fields":
		out := []string{}
		for _, f := range a.Fields {
			if f.In == "query" {
				out = append(out, f.Name)
			}
		}
		return out
	}
	return nil
}
