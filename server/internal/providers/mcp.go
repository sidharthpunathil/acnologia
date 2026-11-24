package providers

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MCPSlug is the built-in provider for connecting any Streamable HTTP MCP
// server as a pooled service.
const MCPSlug = "mcp"

// MCPConfig describes an upstream MCP server offered by a provider. When set,
// Acnologia connects to it using the service's pooled keys, discovers its
// tools, and exposes them through Acnologia's /mcp server with key pooling,
// rotation, limits, budgets and access control.
type MCPConfig struct {
	// URL is the upstream MCP endpoint: a path resolved against the service's
	// base_url (such as "/mcp/" or "/" to use base_url as-is), or an absolute
	// http(s) URL on the same site as base_url so keys never leave the provider.
	URL string `json:"url,omitempty"`
	// Transport is "streamable_http" (default).
	Transport string `json:"transport,omitempty"`
	// Headers are optional static non-credential headers sent to the upstream
	// MCP server (e.g. feature flags). Auth headers always come from the key pool.
	Headers map[string]string `json:"headers,omitempty"`
}

// EffectiveTransport returns the transport name, defaulting to streamable_http.
func (m *MCPConfig) EffectiveTransport() string {
	if m == nil || strings.TrimSpace(m.Transport) == "" {
		return "streamable_http"
	}
	return strings.TrimSpace(m.Transport)
}

var forbiddenMCPHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"host":                true,
	"content-length":      true,
}

func (s *Spec) validateMCP(add func(string, ...any)) {
	m := s.MCP
	if m == nil {
		return
	}
	if m.Transport != "" && m.Transport != "streamable_http" {
		add("mcp.transport must be streamable_http")
	}
	if m.URL != "" {
		if strings.HasPrefix(m.URL, "/") {
			if m.URL != "/" && (!hostPathRe.MatchString(m.URL) || strings.Contains(m.URL, "//") || hasDotSegment(m.URL) || len(m.URL) > 300) {
				add("mcp.url path must be a plain path such as /mcp/ without a query, . or .. segments")
			}
		} else {
			u, err := url.Parse(m.URL)
			site := HostSite(s.BaseURL)
			switch {
			case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
				add("mcp.url must be a path starting with / or an http(s) URL")
			case u.User != nil || u.Fragment != "":
				add("mcp.url must not carry credentials or a fragment")
			case net.ParseIP(u.Hostname()) != nil:
				add("mcp.url must use a domain name, not an IP address")
			case !onSite(u.Hostname(), site):
				add("mcp.url must be on %s, the site of base_url, so keys stay with this provider", site)
			}
		}
	}
	if len(m.Headers) > 10 {
		add("mcp.headers may have at most 10 entries")
	}
	for k, v := range m.Headers {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "\r\n:") {
			add("mcp.headers has an invalid header name %q", k)
		}
		if forbiddenMCPHeaders[strings.ToLower(strings.TrimSpace(k))] {
			add("mcp.headers must not set %q (credentials come from the key pool)", k)
		}
		if strings.ContainsAny(v, "\r\n") || len(v) > 512 {
			add("mcp.headers[%q] value is invalid or too long", k)
		}
	}
}

// ValidateServiceMCPURL checks a service-level mcp_url override. Empty is valid
// (clears override). Relative paths and full http(s) URLs are allowed (trailing
// slash preserved verbatim because some MCP servers require it).
func ValidateServiceMCPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") {
		if raw == "/" {
			return ""
		}
		if !hostPathRe.MatchString(raw) || strings.Contains(raw, "//") || hasDotSegment(raw) || len(raw) > 300 {
			return "mcp_url path must be a plain path such as /mcp/ without . or .. segments"
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "mcp_url must be a path starting with / or an http(s) URL"
	}
	if u.User != nil || u.Fragment != "" {
		return "mcp_url must not carry credentials or a fragment"
	}
	return ""
}

// ResolveMCPURL returns the upstream MCP endpoint URL for a service, or "" if
// neither the service nor its provider spec configures upstream MCP.
// Trailing slashes are preserved verbatim so servers that require /mcp/ work
// without redirects.
func ResolveMCPURL(baseURL, serviceMCPURL string, specMCP *MCPConfig) string {
	raw := strings.TrimSpace(serviceMCPURL)
	if raw == "" && specMCP != nil {
		raw = strings.TrimSpace(specMCP.URL)
	}
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		if raw == "/" {
			return strings.TrimSpace(baseURL)
		}
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + raw
	}
	return fmt.Sprintf("%s/%s", strings.TrimRight(strings.TrimSpace(baseURL), "/"), strings.TrimLeft(raw, "/"))
}

func mcpBuiltin() Builtin {
	return Builtin{
		Slug:        MCPSlug,
		Name:        "MCP server",
		Category:    "MCP",
		Color:       "#5b5bd6",
		Description: "Any upstream Streamable HTTP MCP server. Acnologia pools and rotates its tokens, syncs its tools, and exposes them through /mcp with per-app access, limits and read-only controls.",
		Spec: Spec{
			BaseURL:          "https://mcp.example.com/mcp/",
			Auth:             Auth{Type: "bearer", Name: "Authorization"},
			Defaults:         Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
			UseStandardRules: true,
			MCP:              &MCPConfig{URL: "/", Transport: "streamable_http"},
			Guide: &Guide{
				Summary: "Connects Acnologia to any remote Streamable HTTP MCP server. Add one or more upstream tokens to the service's key pool, sync its tools, and every upstream tool becomes available through Acnologia's /mcp endpoint with key pooling, rotation, limits and per-token read-only controls.",
				Notes: []string{
					"Sync tools from the service's MCP tab (or POST /api/admin/services/{id}/mcp/sync) whenever the upstream server adds or changes tools.",
					"Apps and teammates call the synced tools through Acnologia's /mcp endpoint with their own acn_ tokens; upstream tokens never leave Acnologia.",
					"Create read-only app tokens or enable Read-only mode in App MCP settings to expose only tools annotated with readOnlyHint: true.",
				},
			},
			Tests: []Fixture{
				{Name: "200 ok", Status: 200, Body: `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`, Expect: OK},
				{Name: "429 rate limited", Status: 429, Expect: RateLimited},
				{Name: "402 quota exhausted", Status: 402, Expect: QuotaExhausted},
				{Name: "401 invalid token", Status: 401, Expect: Invalid},
				{Name: "403 forbidden token", Status: 403, Expect: Invalid},
				{Name: "503 server error", Status: 503, Expect: Error},
			},
		},
	}
}
