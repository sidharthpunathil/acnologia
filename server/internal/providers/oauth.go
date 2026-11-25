package providers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

// OAuth credentials.
//
// Some APIs take no static key: a person signs in to their account, approves
// the scopes, and the API is then called with a short lived access token that
// a refresh token renews (Google APIs, Microsoft Graph and many more). A
// provider with auth type oauth2 describes that sign in here. The server
// runs the authorization code flow (with PKCE when the provider supports
// it), keeps the refresh token encrypted, and refreshes the access token
// before it expires. Each connected account is one key in the service's
// pool, so rotation, cooldowns and health work as they do for API keys.
//
// The OAuth client id and secret are the self-hoster's own: each server
// registers its own OAuth app with the provider and enters them on the
// service. A spec never carries them.

// AuthOAuth2 is the auth type of OAuth providers.
const AuthOAuth2 = "oauth2"

// OAuthConfig is how a provider's OAuth sign in works.
type OAuthConfig struct {
	// AuthorizeURL is where the browser goes to sign in and approve.
	AuthorizeURL string `json:"authorize_url"`
	// TokenURL exchanges the code, and later the refresh token, for tokens.
	TokenURL string `json:"token_url"`
	// Scopes are asked for when the service sets none of its own.
	Scopes []string `json:"scopes,omitempty"`
	// AllowedScopes, when set, are the only scopes a service may ask for.
	AllowedScopes []string `json:"allowed_scopes,omitempty"`
	// ScopeSeparator joins scopes in the authorize URL; a space by default.
	ScopeSeparator string `json:"scope_separator,omitempty"`
	// ClientAuth is how the client id and secret reach the token endpoint:
	// "body" (form fields, the default) or "basic" (HTTP Basic auth).
	ClientAuth string `json:"client_auth,omitempty"`
	// AuthorizeParams are added to the authorize URL, such as
	// access_type=offline and prompt=consent, which Google needs before it
	// hands out a refresh token.
	AuthorizeParams map[string]string `json:"authorize_params,omitempty"`
	// PKCE sends an S256 code challenge with the authorize request.
	PKCE bool `json:"pkce,omitempty"`
	// UserinfoURL is called with the new access token to name the account,
	// when the token answer has no id_token. Optional.
	UserinfoURL string `json:"userinfo_url,omitempty"`
	// AccountField is the claim of the id_token or the field of the
	// userinfo answer that names the account; email by default.
	AccountField string `json:"account_field,omitempty"`
	// TokenPath is where the access token sits in the token endpoint's
	// answer, as a dotted path; access_token by default. The refresh token,
	// expires_in, scope and id_token are read from the same object. Slack
	// puts its bot token at the top (access_token) and the user token under
	// authed_user (authed_user.access_token).
	TokenPath string `json:"token_path,omitempty"`
	// Tenant is the default for {tenant} in the authorize, token and
	// userinfo URLs (Microsoft's common, say). A service may set its own
	// tenant with its OAuth client.
	Tenant string `json:"tenant,omitempty"`
}

// TenantPlaceholder is replaced by the tenant in an OAuth block's URLs.
const TenantPlaceholder = "{tenant}"

var tenantRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,99}$`)

// ValidTenant reports whether t can stand for {tenant}: letters, digits,
// dots and hyphens, as tenant names (common, organizations), ids (GUIDs)
// and domain names (contoso.onmicrosoft.com) are.
func ValidTenant(t string) bool { return tenantRe.MatchString(t) && !strings.Contains(t, "..") }

// HasTenant reports whether the block's URLs take a tenant.
func (c *OAuthConfig) HasTenant() bool {
	return c != nil && (strings.Contains(c.AuthorizeURL, TenantPlaceholder) || strings.Contains(c.TokenURL, TenantPlaceholder) ||
		strings.Contains(c.UserinfoURL, TenantPlaceholder))
}

// ForTenant is the block with {tenant} in its URLs replaced by tenant, or
// by the block's default when tenant is empty or not valid. A block
// without the placeholder comes back as it is.
func (c *OAuthConfig) ForTenant(tenant string) *OAuthConfig {
	if !c.HasTenant() {
		return c
	}
	if !ValidTenant(tenant) {
		tenant = c.Tenant
	}
	out := *c
	out.AuthorizeURL = strings.ReplaceAll(c.AuthorizeURL, TenantPlaceholder, tenant)
	out.TokenURL = strings.ReplaceAll(c.TokenURL, TenantPlaceholder, tenant)
	out.UserinfoURL = strings.ReplaceAll(c.UserinfoURL, TenantPlaceholder, tenant)
	return &out
}

var tokenPathRe = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+){0,3}$`)

// TokenObject finds the object of a token answer that holds the access
// token, and the access token's field name in it (TokenPath).
func (c *OAuthConfig) TokenObject(answer map[string]any) (map[string]any, string) {
	path := "access_token"
	if c != nil && c.TokenPath != "" {
		path = c.TokenPath
	}
	parts := strings.Split(path, ".")
	obj := answer
	for _, p := range parts[:len(parts)-1] {
		next, ok := obj[p].(map[string]any)
		if !ok {
			return nil, ""
		}
		obj = next
	}
	return obj, parts[len(parts)-1]
}

// equalOAuth reports whether two OAuth blocks are the same.
func equalOAuth(a, b *OAuthConfig) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(*a, *b)
}

// Separator is the scope separator in effect.
func (c *OAuthConfig) Separator() string {
	if c == nil || c.ScopeSeparator == "" {
		return " "
	}
	return c.ScopeSeparator
}

// Account is the claim or field that names the account.
func (c *OAuthConfig) Account() string {
	if c == nil || c.AccountField == "" {
		return "email"
	}
	return c.AccountField
}

// ScopeProblem says why a service may not ask for these scopes, or "".
func (c *OAuthConfig) ScopeProblem(scopes []string) string {
	if len(scopes) > 50 {
		return "at most 50 scopes"
	}
	for _, sc := range scopes {
		if strings.TrimSpace(sc) == "" || strings.ContainsAny(sc, " \t\r\n") {
			return fmt.Sprintf("scope %q is not a single word", sc)
		}
	}
	if c == nil || len(c.AllowedScopes) == 0 {
		return ""
	}
	allowed := map[string]bool{}
	for _, sc := range c.AllowedScopes {
		allowed[sc] = true
	}
	for _, sc := range scopes {
		if !allowed[sc] {
			return fmt.Sprintf("scope %q is not one this provider allows (%s)", sc, strings.Join(c.AllowedScopes, ", "))
		}
	}
	return ""
}

func validOAuthURL(raw string) bool {
	u, err := url.Parse(strings.ReplaceAll(raw, TenantPlaceholder, "common"))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.Fragment == ""
}

// validateOAuth checks an oauth2 auth block.
func (a Auth) validateOAuth(add func(string, ...any)) {
	c := a.OAuth
	if c == nil {
		add("oauth2 auth needs an oauth block with authorize_url and token_url")
		return
	}
	if !validOAuthURL(c.AuthorizeURL) {
		add("auth.oauth.authorize_url must be an http(s) URL")
	}
	if !validOAuthURL(c.TokenURL) {
		add("auth.oauth.token_url must be an http(s) URL")
	}
	if c.UserinfoURL != "" && !validOAuthURL(c.UserinfoURL) {
		add("auth.oauth.userinfo_url must be an http(s) URL")
	}
	switch c.ClientAuth {
	case "", "body", "basic":
	default:
		add("auth.oauth.client_auth must be body or basic")
	}
	if c.TokenPath != "" && !tokenPathRe.MatchString(c.TokenPath) {
		add("auth.oauth.token_path must be a dotted path of at most 4 names, such as authed_user.access_token")
	}
	switch {
	case c.HasTenant() && !ValidTenant(c.Tenant):
		add("auth.oauth.tenant is required when a URL has {tenant}: letters, digits, dots and hyphens, such as common")
	case !c.HasTenant() && c.Tenant != "":
		add("auth.oauth.tenant is set but no URL has {tenant}")
	}
	if msg := (&OAuthConfig{}).ScopeProblem(c.Scopes); msg != "" {
		add("auth.oauth.scopes: %s", msg)
	}
	if msg := c.ScopeProblem(c.Scopes); len(c.AllowedScopes) > 0 && msg != "" {
		add("auth.oauth.scopes: %s", msg)
	}
	if len(c.AuthorizeParams) > 20 {
		add("auth.oauth.authorize_params takes at most 20 params")
	}
	for k := range c.AuthorizeParams {
		switch k {
		case "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method", "response_type", "scope":
			add("auth.oauth.authorize_params cannot set %s: the server sets it", k)
		}
	}
	if len(a.Fields) > 8 {
		add("oauth2 auth takes at most 8 extra fields")
	}
	seen := map[string]bool{}
	for i, f := range a.Fields {
		if !fieldKeyRe.MatchString(f.Key) || f.Key == "access_token" || f.Key == OAuthConnectionField {
			add("auth.fields[%d].key must be lowercase letters, digits and underscores, and not access_token", i)
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
}

// OAuthConnectionField names the connection in an OAuth key's stored
// secret. The stored secret of an OAuth key is a JSON object of this field
// and the key's extra fields (a Google Ads developer token, say). The tokens
// themselves are stored apart and never in the key.
const OAuthConnectionField = "oauth_connection"

// OAuthStoredSecret is the stored secret of an OAuth key: the connection id
// and the extra fields, in the spec's order so it always reads the same.
func (a Auth) OAuthStoredSecret(connection string, fields map[string]string) string {
	var b strings.Builder
	b.WriteByte('{')
	k, _ := json.Marshal(OAuthConnectionField)
	v, _ := json.Marshal(connection)
	b.Write(k)
	b.WriteByte(':')
	b.Write(v)
	for _, f := range a.Fields {
		val := strings.TrimSpace(fields[f.Key])
		if val == "" {
			continue
		}
		k, _ := json.Marshal(f.Key)
		v, _ := json.Marshal(val)
		b.WriteByte(',')
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.String()
}

// OAuthFields reads the extra fields out of an OAuth key's stored secret.
func OAuthFields(stored string) map[string]string {
	out := map[string]string{}
	var m map[string]any
	if json.Unmarshal([]byte(stored), &m) != nil {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok && k != OAuthConnectionField {
			out[k] = s
		}
	}
	return out
}

// OAuthFieldsProblem checks the extra fields given for a connection: known
// keys only, the required ones set, one line each.
func (a Auth) OAuthFieldsProblem(fields map[string]string) string {
	known := map[string]AuthField{}
	for _, f := range a.Fields {
		known[f.Key] = f
	}
	for k, v := range fields {
		if _, ok := known[k]; !ok {
			return fmt.Sprintf("%s is not a field of this provider", k)
		}
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Sprintf("%s cannot span lines", k)
		}
	}
	for _, f := range a.Fields {
		if !f.Optional && strings.TrimSpace(fields[f.Key]) == "" {
			l := f.Label
			if l == "" {
				l = f.Key
			}
			return l + " is required"
		}
	}
	return ""
}

// OAuthWireSecret is what the pool hands out for an OAuth key: the access
// token alone, or with extra fields a JSON object of access_token and the
// fields. A lease of an OAuth key returns it, never the refresh token.
func OAuthWireSecret(access string, fields map[string]string) string {
	if len(fields) == 0 {
		return access
	}
	m := map[string]string{"access_token": access}
	for k, v := range fields {
		if v != "" {
			m[k] = v
		}
	}
	if len(m) == 1 {
		return access
	}
	b, _ := json.Marshal(m) // sorted keys: stable
	return string(b)
}

// OAuthWireParts reads a wire secret back: the access token and the fields.
func OAuthWireParts(secret string) map[string]string {
	if !strings.HasPrefix(secret, "{") {
		return map[string]string{"access_token": secret}
	}
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

func (a Auth) oauthHeader() (string, string) {
	name, prefix := a.Name, a.Prefix
	if name == "" {
		name = "Authorization"
	}
	if prefix == "" {
		prefix = "Bearer "
	}
	return name, prefix
}
