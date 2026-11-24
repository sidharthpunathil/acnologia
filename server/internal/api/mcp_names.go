package api

// What an agent sees of the app MCP's built in tools. Internally they keep
// their short names (get_key, list_services, ...), which settings, audits
// and the dashboard use. Agents see them with the acnologia_ prefix, so they
// cannot clash with another server's tools, and calling a tool by its old
// name still works. Each built in tool also states the shape of its result.

import (
	"regexp"
	"strings"
)

const toolPrefix = "acnologia_"

// canonicalTool maps a name an agent sent to the internal name: the prefixed
// form of a built in tool loses its prefix, anything else stays as it is.
func canonicalTool(name string) string {
	if rest, ok := strings.CutPrefix(name, toolPrefix); ok && isStaticTool(rest) {
		return rest
	}
	return name
}

// publicTool is the name agents see for a tool.
func publicTool(name string) string {
	if isStaticTool(name) {
		return toolPrefix + name
	}
	return name
}

var wordRe = regexp.MustCompile(`[A-Za-z0-9_]+`)

// publicText rewrites built in tool names in text an agent reads
// (instructions, descriptions, prompts, errors, guides) to their public form.
func publicText(s string) string {
	return wordRe.ReplaceAllStringFunc(s, publicTool)
}

// disabledSet is the app's hidden tools by internal name, whichever form
// the setting was saved in.
func disabledSet(names []string) map[string]bool {
	off := make(map[string]bool, len(names))
	for _, n := range names {
		off[canonicalTool(n)] = true
	}
	return off
}

// presentTools is tools as tools/list shows them: built in tools renamed,
// their descriptions rewritten, and their output schema attached.
func presentTools(ts []mcpTool) []mcpTool {
	out := make([]mcpTool, len(ts))
	for i, t := range ts {
		if isStaticTool(t.Name) {
			t.OutputSchema = toolOutputs[t.Name]
			t.Name = publicTool(t.Name)
			t.Description = publicText(t.Description)
		} else if t.Set == setService && !t.upstreamMCP {
			t.OutputSchema = proxyOutput
		}
		out[i] = t
	}
	return out
}

// ---- output schemas ----
//
// Clients check structuredContent against outputSchema, so a schema names
// only fields every result has with that type. Fields that may be null or
// absent are described without a type.

func outObj(props map[string]any, required ...string) map[string]any {
	return obj(props, required...)
}

func typed(t, desc string) map[string]any { return map[string]any{"type": t, "description": desc} }

func anyOf(desc string) map[string]any { return map[string]any{"description": desc} }

func listOf(item map[string]any, desc string) map[string]any {
	return map[string]any{"type": "array", "items": item, "description": desc}
}

// withPage adds the paging fields of a list tool's result.
func withPage(props map[string]any) map[string]any {
	props["total"] = typed("integer", "How many items match")
	props["count"] = typed("integer", "How many items this page has")
	props["offset"] = typed("integer", "Where this page starts")
	props["has_more"] = typed("boolean", "Whether another page follows")
	props["next_offset"] = typed("integer", "The offset of the next page, when has_more")
	return props
}

var proxyOutput = outObj(map[string]any{
	"status":               typed("integer", "The provider's HTTP status"),
	"content_type":         typed("string", "The provider's Content-Type"),
	"key_id":               typed("string", "Which pool key served the call"),
	"body":                 anyOf("The response body: JSON as is, text as a string. A cut body is always a string"),
	"body_base64":          typed("string", "A binary body, base64 encoded"),
	"bytes":                typed("integer", "Size of a binary body"),
	"note":                 typed("string", "Why a body was cut or left out"),
	"truncated":            typed("boolean", "The body was longer than the app's max result size and was cut"),
	"bytes_total":          typed("integer", "The whole body's size, when it was cut"),
	"bytes_total_at_least": typed("boolean", "The body was too long to measure: bytes_total is a lower bound"),
	"retry_after_sec":      typed("integer", "Seconds the provider or Acnologia asked to wait before trying again"),
	"attempts":             typed("integer", "How many keys were tried"),
	"served_by":            typed("string", "The service that answered"),
	"tried":                typed("string", "For a capability: each service tried, with its status"),
	"cache":                typed("string", "hit or miss in the response cache, replay for a repeated idempotency key, or bypass"),
}, "status")

var serviceItem = outObj(map[string]any{
	"slug":      typed("string", "Service slug, for the service argument of other tools"),
	"name":      typed("string", "Display name"),
	"category":  typed("string", "Category"),
	"available": typed("boolean", "Whether a key is free right now"),
	"base_url":  typed("string", "The provider's base URL"),
}, "slug", "available")

var keyItem = outObj(map[string]any{
	"key_id":          typed("integer", "Key id"),
	"label":           typed("string", "Key label"),
	"hint":            typed("string", "The secret's last characters"),
	"service":         typed("string", "Service slug"),
	"validity":        typed("string", "valid, invalid or unknown"),
	"status":          typed("string", "The key's effective status"),
	"quota_remaining": anyOf("Quota left, when the provider reports it"),
	"quota_limit":     anyOf("Quota size, when the provider reports it"),
	"cooldown_until":  anyOf("When a cooling key is usable again"),
	"last_error":      anyOf("The last error the provider gave"),
}, "key_id", "service")

var leaseOutput = outObj(map[string]any{
	"lease_id":   typed("string", "Pass to renew_key and report_key"),
	"key_id":     typed("integer", "Key id"),
	"service":    typed("string", "Service slug"),
	"secret":     typed("string", "The API key. Never print or store it"),
	"auth":       outObj(map[string]any{"type": typed("string", "header, query or bearer"), "name": typed("string", "Header or query parameter name"), "prefix": typed("string", "Goes before the secret")}),
	"base_url":   typed("string", "The provider's base URL"),
	"expires_at": typed("string", "When the lease ends unless renewed"),
	"exclusive":  typed("boolean", "Whether the key is reserved for you"),
}, "lease_id", "secret")

var toolOutputs = map[string]map[string]any{
	"list_services": outObj(withPage(map[string]any{"services": listOf(serviceItem, "Services on this page")}), "services"),
	"pool_status":   serviceItem,
	"get_key":       leaseOutput,
	"renew_key":     outObj(map[string]any{"ok": typed("boolean", "Renewed"), "expires_at": typed("string", "The new end of the lease")}, "ok"),
	"report_key":    outObj(map[string]any{"ok": typed("boolean", "Reported"), "next": anyOf("A new lease when rotate was set and a key was free, else null")}, "ok"),
	"service_guide": outObj(map[string]any{"service": typed("string", "Service slug"), "generated": typed("boolean", "Whether the guide was built from examples only"), "markdown": typed("string", "The guide")}, "markdown"),
	"proxy_request": proxyOutput,
	"search_tools":  outObj(map[string]any{}),
	// call_tool has none: it answers in the called tool's shape, and an
	// upstream MCP tool may return no structuredContent at all.
	"list_keys":     outObj(withPage(map[string]any{"keys": listOf(keyItem, "Keys on this page")}), "keys"),
	"key_details":   keyItem,
	"service_stats": outObj(map[string]any{"service": typed("string", "Service slug"), "name": typed("string", "Display name"), "keys": anyOf("Key counts by state"), "requests_24h": anyOf("Requests in the last 24 hours")}, "service"),
	"check_keys": outObj(map[string]any{"message": typed("string", "Summary"), "checked": typed("integer", "Keys checked"), "working": typed("integer", "Keys that work"),
		"out_of_quota": typed("integer", "Keys out of quota"), "invalid": typed("integer", "Keys the provider rejected"), "unreachable": typed("integer", "Checks that could not reach the provider"), "skipped": typed("integer", "Keys not checked")}, "message"),
	"set_key_status": keyItem,
	"add_keys":       outObj(map[string]any{"added": listOf(outObj(map[string]any{"key_id": typed("integer", "Key id"), "label": typed("string", "Label"), "hint": typed("string", "The secret's last characters")}), "Keys added"), "skipped": typed("integer", "Duplicates and empty secrets")}, "added", "skipped"),
}
