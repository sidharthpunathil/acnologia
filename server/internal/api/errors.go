package api

// The error registry. Every error code the API writes has an entry here: what
// it means, whether a retry can help, and what to do next. writeErr adds the
// entry's hint, fix and doc link to the error envelope, MCP tool errors carry
// the same facts, and docs/errors.md is generated from it (errors_test.go
// fails when a code has no entry or the doc drifts from the registry).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// errFix is the next step for an error: an MCP tool to call, a CLI command to
// run, or a URL (a path on this server, or a doc) to read or call.
type errFix struct {
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
	URL     string `json:"url,omitempty"`
}

func (f errFix) empty() bool { return f.Tool == "" && f.Command == "" && f.URL == "" }

// errHelp is one code's entry.
type errHelp struct {
	// Status is the HTTP status the code usually comes with.
	Status int
	// Retry says whether the same request can succeed later unchanged.
	Retry bool
	// Area groups the code in docs/errors.md.
	Area string
	// Meaning says what happened, for the docs.
	Meaning string
	// Hint says what to do, for the envelope and the docs.
	Hint string
	Fix  errFix
	// Doc overrides the code's own anchor in docs/errors.md.
	Doc string
}

// Areas, in the order docs/errors.md shows them.
const (
	areaAccess   = "Access and tokens"
	areaConsumer = "Services, proxy and leases"
	areaMCP      = "MCP tools"
	areaRequest  = "Request shape"
	areaSharing  = "Sharing"
	areaAdmin    = "Dashboard and admin API"
	areaAI       = "AI features"
)

var errAreas = []string{areaAccess, areaConsumer, areaMCP, areaRequest, areaSharing, areaAdmin, areaAI}

// errorsDoc is where the registry is published.
const errorsDoc = "/docs/errors.md"

func eh(status int, retry bool, area, meaning, hint string) errHelp {
	return errHelp{Status: status, Retry: retry, Area: area, Meaning: meaning, Hint: hint}
}

func (h errHelp) tool(t string) errHelp { h.Fix.Tool = t; return h }
func (h errHelp) cmd(c string) errHelp  { h.Fix.Command = c; return h }
func (h errHelp) url(u string) errHelp  { h.Fix.URL = u; return h }
func (h errHelp) doc(d string) errHelp  { h.Doc = d; return h }
func (h errHelp) docURL(code string) string {
	if h.Doc != "" {
		return h.Doc
	}
	return errorsDoc + "#" + code
}

// errRegistry maps every error code to its help. Tool names are the public
// ones agents see on /mcp.
var errRegistry = map[string]errHelp{
	// Access and tokens.
	"unauthenticated": eh(401, false, areaAccess, "The request has no credential, or one that is wrong, revoked, expired, or belongs to a disabled app (403 then).",
		"Send a valid token: Authorization: Bearer acn_... for /api/v1, /proxy and /mcp; acnadm_... for /api/admin. GET /api/v1/me says what an app token can do.").url("/api/v1/me"),
	"forbidden": eh(403, false, areaAccess, "The caller is signed in, but its role (or its API token's scope) is below what the route needs.",
		"Ask a superadmin to raise your role, or create an API token with a wider scope (read, write or admin), never above your own role.").cmd("acn whoami"),
	"locked": eh(401, false, areaAccess, "The server is behind its access password and the request did not pass the gate.",
		"Send the access password in the X-Acnologia-Access header, or an admin API token (Authorization: Bearer acnadm_...). The consumer routes (/api/v1, /proxy, /mcp) and the docs (/llms.txt, /llms-full.txt, /docs/*.md, /api/docs) also take a valid app token or MCP access token.").url("/api/v1"),
	"wrong_password": eh(401, false, areaAccess, "The access password typed on the lock screen is wrong.",
		"Check the password with the server's operator (ACNOLOGIA_ACCESS_PASSWORD). Wrong guesses are limited per network."),
	"too_many_attempts": eh(429, true, areaAccess, "Too many wrong passwords, sign in attempts or device codes from this client.",
		"Wait retry_after_sec (or the Retry-After header) before trying again. Do not loop on guesses."),
	"bad_credentials": eh(401, false, areaAccess, "The email and password do not match an account.",
		"Check both. In a script, sign in with acn login instead of a password.").cmd("acn login"),
	"bad_password": eh(400, false, areaAccess, "The current password given to change a password is wrong.",
		"Send the account's current password."),
	"weak_password": eh(400, false, areaAccess, "A new password is shorter than 10 or longer than 72 characters.",
		"Pick a password of 10 to 72 characters."),
	"already_setup": eh(409, false, areaAccess, "First run setup is already done.",
		"Sign in with the existing superadmin, or ask them to create your account.").url("/api/auth/state"),
	"session_required": eh(403, false, areaAccess, "The route needs a dashboard sign in; an API token cannot do it (managing API tokens, approving sign ins).",
		"Do it in the dashboard in a browser. An API token may only revoke itself (acn logout)."),
	"csrf": eh(403, false, areaAccess, "A dashboard write came without a JSON content type or the X-Requested-With header.",
		"Send Content-Type: application/json, or the X-Requested-With header."),
	"expired_token": eh(400, false, areaAccess, "The CLI sign in request expired or does not exist.",
		"Start again with acn login.").cmd("acn login"),
	"authorization_pending": eh(400, true, areaAccess, "The CLI sign in has not been approved in the browser yet.",
		"Keep polling at the interval the create call gave."),
	"slow_down": eh(400, true, areaAccess, "The CLI sign in is polled faster than the interval allows.",
		"Poll less often: add 5 seconds to the interval."),
	"access_denied": eh(400, false, areaAccess, "Someone denied the CLI sign in in the browser.",
		"Run acn login again if the denial was a mistake.").cmd("acn login"),
	"already_decided": eh(409, false, areaAccess, "The CLI sign in was already approved or denied.",
		"Nothing to do; start a new acn login for another sign in."),
	"too_many_requests": eh(429, true, areaAccess, "Too many CLI sign ins are waiting, or this client started too many.",
		"Wait a minute and try again.").cmd("acn login"),
	"request_expired": eh(400, false, areaAccess, "The MCP sign in (OAuth consent) request expired or was already used.",
		"Start the connection again from the MCP client.").doc("/docs/mcp-oauth.md"),
	"read_only_token": eh(403, false, areaAccess, "A read-only app token tried to lease, renew or report a key, or to write.",
		"Use read-only calls (GET or HEAD through /proxy, read-only MCP tools), or ask an admin for a token that is not read-only.").tool("acnologia_whoami").url("/api/v1/me"),

	// Services, proxy and leases.
	"service_not_allowed": eh(403, false, areaConsumer, "The app may not use this service (or capability): it is outside the app's allowed services or its share's scope.",
		"Pick a service from the app's list, or ask an admin to add this one to the app.").tool("acnologia_list_services").url("/api/v1/services"),
	"unknown_service": eh(404, false, areaConsumer, "No service, service group or capability has this slug.",
		"Check the slug against the app's services; the message suggests close ones.").tool("acnologia_list_services").url("/api/v1/services"),
	"proxy_not_allowed": eh(403, false, areaConsumer, "The app is in lease mode and may not use the proxy or the forward proxy.",
		"Lease a key with POST /api/v1/lease instead, or ask an admin to set the app's mode to proxy or both.").tool("acnologia_get_key").url("/api/v1/lease"),
	"lease_not_allowed": eh(403, false, areaConsumer, "The app is proxy only, or the service is shared by another server or holds handed over keys, which are proxy only.",
		"Call the provider through /proxy/{service}/... or the acnologia_proxy_request tool instead.").tool("acnologia_proxy_request"),
	"no_key_available": eh(503, true, areaConsumer, "Every usable key for the service is cooling down, out of quota or disabled.",
		"Wait retry_after_sec, then retry. Check the service's state with acnologia_pool_status; if it stays empty, an admin has to add or fix keys.").tool("acnologia_pool_status"),
	"all_keys_busy": eh(503, true, areaConsumer, "Usable keys exist, but each is at its concurrency limit or at a rate limit its provider publishes.",
		"Retry after retry_after_sec (often 1 second), or lease with wait_sec so the server waits for a free key.").tool("acnologia_pool_status"),
	"service_unavailable": eh(503, true, areaConsumer, "The provider keeps failing, so the circuit breaker paused traffic to it.",
		"Wait retry_after_sec before trying again. An admin can reset the breaker.").cmd("acn breaker <service> --reset"),
	"app_limit_reached": eh(429, true, areaConsumer, "The app used up a daily or monthly request or budget limit.",
		"Wait until reset_at, or ask an admin to raise the limit. GET /api/v1/me shows what is left of each limit.").tool("acnologia_whoami").url("/api/v1/me"),
	"limits_unavailable": eh(503, true, areaConsumer, "The server could not read the app's limits, so it held the request back.",
		"Retry after retry_after_sec."),
	"token_limit_reached": eh(429, true, areaConsumer, "The token used up a request or budget limit of its own.",
		"Wait until reset_at, or ask an admin to raise the token's limits or the app's per-token default.").tool("acnologia_whoami").url("/api/v1/me"),
	"tag_limit_reached": eh(429, true, areaConsumer, "A caller tag value (X-Acnologia-Meta) used up its tag budget; tag names the value.",
		"Wait until reset_at, or ask an admin to raise the app's tag budget. GET /api/v1/usage/tags shows use per tag value.").url("/api/v1/usage/tags"),
	"server_spend_ceiling": eh(503, true, areaConsumer, "The server reached its daily spend ceiling, so every app waits until midnight UTC.",
		"Wait until reset_at, or ask a superadmin to raise the ceiling (PUT /api/admin/settings/spend-ceiling)."),
	"bad_meta": eh(400, false, areaConsumer, "X-Acnologia-Meta is malformed: more than 5 pairs, a repeated key, or characters outside the allowed set.",
		"Send at most 5 key=value pairs, comma separated, with short ASCII keys and values."),
	"tag_pinned": eh(400, false, areaConsumer, "The token is pinned to a value of this tag key, and the request sent a different one.",
		"Drop the tag from X-Acnologia-Meta, or send the pinned value. GET /api/v1/me shows the token."),
	"denied_by_rule": eh(403, false, areaConsumer, "One of the app's deny rules covers this request; rule names it.",
		"Call a different method, service or path, or ask an admin to change the app's deny rules."),
	"too_many_streams": eh(503, true, areaConsumer, "Too many event streams are open on this server, or for this person or app.",
		"Close a stream you no longer read, then retry after retry_after_sec."),
	"empty_group": eh(503, false, areaConsumer, "The service group has no services left.",
		"Call one of the services directly, or ask an admin to add services to the group.").tool("acnologia_list_services"),
	"bad_params": eh(400, false, areaConsumer, "A capability call is missing a required param, or one has the wrong type.",
		"Send the params GET /api/v1/capabilities lists for the capability.").url("/api/v1/capabilities"),
	"no_route": eh(503, false, areaConsumer, "No route of the capability can take these params, or _service names none of its routes.",
		"Change the params or drop _service. GET /api/v1/capabilities lists each route.").url("/api/v1/capabilities"),
	"no_route_worked": eh(503, true, areaConsumer, "Every route of the capability failed before reaching a provider; tried lists each.",
		"Read tried, then retry later or call one service directly."),
	"lease_not_found": eh(404, false, areaConsumer, "The lease is not this app's, was already reported, or has expired.",
		"Lease a new key. Renew a lease before expires_at.").tool("acnologia_get_key").url("/api/v1/lease"),
	"lease_revoked": eh(403, false, areaConsumer, "The app may no longer hold this key (its mode, services, share scope or hidden key grant changed, or the key was disabled), so the lease ended.",
		"Lease a new key, or use the proxy.").tool("acnologia_get_key"),
	"report_failed": eh(409, false, areaConsumer, "The report could not apply: unknown lease (404), already reported (409), reported over 10 minutes after expiry (410), or the app lost the service (403).",
		"Do not retry the same report. Report each lease once, within 10 minutes of expires_at."),
	"upstream_unreachable": eh(502, true, areaConsumer, "The provider could not be reached.",
		"Retry later. If it keeps failing, check the service's base URL in its guide.").tool("acnologia_service_guide"),
	"upstream_idle": eh(504, true, areaConsumer, "The provider stopped sending its answer partway.",
		"Retry; for long jobs ask for a smaller answer."),
	"upstream_blocked": eh(502, false, areaConsumer, "The provider's address is one the server refuses to call (a cloud metadata or private address).",
		"Ask an admin to fix the service's base URL or MCP URL."),
	"loop_detected": eh(508, false, areaConsumer, "The request already passed through more than 3 Acnologia servers.",
		"Call the provider from a server that holds the keys, or break the sharing loop."),
	"idempotency_in_progress": eh(409, true, areaConsumer, "A request with the same Idempotency-Key is still running.",
		"Wait a moment, then retry with the same key to get its answer."),
	"idempotency_key_reused": eh(422, false, areaConsumer, "This Idempotency-Key was already used with a different request body.",
		"Use a new Idempotency-Key for a different request."),
	"not_a_service": eh(403, false, areaConsumer, "The forward proxy was asked for a host that is no service here (or for passthrough this token may not use).",
		"Call that host directly and list it in NO_PROXY. GET /api/v1/proxy lists the hosts the proxy serves.").url("/api/v1/proxy").doc("/docs/api.md#forward-proxy"),
	"port_not_allowed": eh(403, false, areaConsumer, "A forward proxy passthrough tunnel asked for a port other than 443 or 8443.",
		"Call that host directly, outside the proxy.").doc("/docs/api.md#forward-proxy"),
	"proxy_auth_required": eh(407, false, areaConsumer, "The forward proxy URL carries no app token, or a wrong one.",
		"Put the app token in the proxy URL: http://app:acn_...@host:port. GET /api/v1/proxy returns the URL.").url("/api/v1/proxy").cmd("acn proxy env"),
	"connect_unsupported": eh(400, false, areaConsumer, "A CONNECT reached the proxy over HTTP/2 or later.",
		"Speak HTTP/1.1 to the forward proxy."),
	"proxy_ca": eh(503, true, areaConsumer, "The forward proxy's certificate authority is not available.",
		"Retry later; an admin can check the forward proxy settings.").url("/api/v1/proxy/ca.pem"),
	"no_stream": eh(500, false, areaConsumer, "The connection cannot stream, so GET /mcp cannot open its notification stream.",
		"Use POST /mcp; poll tools/list instead of streaming."),

	// MCP tools.
	"invalid_arguments": eh(0, false, areaMCP, "A tool's arguments are not valid JSON, miss a required field, or name a field the tool does not take.",
		"Read the tool's inputSchema in tools/list and call it again with the fields it lists.").tool("acnologia_search_tools"),
	"unknown_tool": eh(0, false, areaMCP, "No tool this app may call has that name.",
		"Call tools/list, or acnologia_search_tools to find a service tool, and use a name it returns.").tool("acnologia_search_tools"),
	"tool_error": eh(0, false, areaMCP, "A tool failed for a reason without a more precise code; the message says why.",
		"Read the message. acnologia_whoami shows what this connection may do."),

	// Request shape.
	"bad_json": eh(400, false, areaRequest, "The body is not valid JSON, or a field has the wrong type.",
		"Send a JSON object with Content-Type: application/json, with the fields the route documents.").doc("/docs/api.md"),
	"invalid_json": eh(400, false, areaRequest, "The body is not valid JSON.",
		"Send a valid JSON object."),
	"bad_content_type": eh(415, false, areaRequest, "A dashboard write was not sent as JSON.",
		"Send Content-Type: application/json."),
	"bad_id": eh(400, false, areaRequest, "An id in the path is not a number.",
		"Use the numeric id from the matching list route."),
	"bad_path": eh(400, false, areaRequest, "The proxy path has a literal ?, #, or a . or .. segment.",
		"Send the query string as query parameters and drop dot segments."),
	"bad_body": eh(400, false, areaRequest, "The proxy could not read the request body.",
		"Resend the request with a complete body."),
	"bad_request": eh(400, false, areaRequest, "The proxy could not build the upstream request.",
		"Check the path and headers you sent."),
	"body_too_large": eh(413, false, areaRequest, "The proxy body is over ACNOLOGIA_MAX_BODY_MB.",
		"Send a smaller body, or ask an admin to raise the limit."),
	"method_not_allowed": eh(405, false, areaRequest, "The method is not one this route takes (the proxy takes GET, HEAD, POST, PUT, PATCH, DELETE and OPTIONS).",
		"Use a method the route lists; GET /mcp needs Accept: text/event-stream."),
	"invalid": eh(400, false, areaRequest, "A field failed validation; the message names it.",
		"Fix the field the message names and send again."),
	"not_found": eh(404, false, areaRequest, "No such endpoint, or the id or slug in the path does not exist.",
		"List the collection to get a valid id. GET /api/v1 lists the endpoints for agents.").url("/api/v1"),
	"duplicate": eh(409, false, areaRequest, "Something with that name, slug or secret already exists.",
		"Use another name or slug, or update the existing one."),
	"too_large": eh(413, false, areaRequest, "An uploaded logo is over 512 KB.",
		"Upload a smaller image."),
	"bad_image": eh(400, false, areaRequest, "An uploaded image is not base64, or not a png, jpeg, webp, gif, svg or ico.",
		"Upload a supported image type, base64 encoded."),
	"too_many_rules": eh(400, false, areaAdmin, "The app already has the most deny rules it can hold.",
		"Delete a rule you no longer need, or merge rules with a shorter path prefix."),
	"bad_query": eh(400, false, areaRequest, "The query string could not be parsed.",
		"Send the query as key=value pairs joined with &, URL encoded."),
	"bad_app": eh(400, false, areaAdmin, "Try it named an app that does not exist or may not use this service.",
		"Pick an app from GET /api/admin/apps that may use the service, or leave app_id out to use the Try it app."),
	"app_off": eh(403, false, areaAdmin, "The internal Try it app is turned off.",
		"Turn the Try it app back on in Apps, or send app_id to try as another app."),
	"demo_loaded": eh(409, false, areaAdmin, "Demo data is already loaded.",
		"Remove it first with DELETE /api/admin/demo, then load it again."),
	"has_services": eh(409, false, areaAdmin, "The server already has services, so demo data is not added by default.",
		"Send {\"force\": true} to POST /api/admin/demo to add it anyway."),
	"no_demo": eh(404, false, areaAdmin, "No demo data is loaded.",
		"Load it with POST /api/admin/demo."),
	"invalid_key": eh(401, false, areaConsumer, "The demo provider was called without a demo key.",
		"Call the demo service through /proxy/demo so Acnologia adds a demo key."),
	"internal": eh(500, true, areaRequest, "The server failed unexpectedly.",
		"Retry once. If it repeats, report the request_id to the server's operator; it is in the server log."),
	"rate_limited": eh(429, true, areaRequest, "Too many invite, claim or revoke attempts from this client.",
		"Wait retry_after_sec (a minute) before trying again."),

	// Sharing.
	"share_revoked": eh(403, false, areaSharing, "The owner revoked the share this token belongs to.",
		"Ask the owner for a new share or claim link."),
	"share_paused": eh(403, true, areaSharing, "The owner paused the share this token belongs to.",
		"Wait for the owner to resume it, or ask them."),
	"share_expired": eh(403, false, areaSharing, "The share this token belongs to expired.",
		"Ask the owner to extend it or for a new link."),
	"share_ended": eh(409, false, areaSharing, "The share is paused or has ended, so no new link can be issued.",
		"Resume the share first."),
	"share_token": eh(403, false, areaSharing, "The key is a share token from another server and is never revealed.",
		"Use the service through the proxy."),
	"share_app": eh(409, false, areaSharing, "This app backs a share to another server.",
		"Manage it on the Sharing page, or pick another app."),
	"invite_invalid": eh(404, false, areaSharing, "The invite is not valid.",
		"Ask the owner for a new invite."),
	"invite_used": eh(409, false, areaSharing, "The invite was already used.",
		"Ask the owner for a new invite."),
	"invite_expired": eh(410, false, areaSharing, "The invite expired.",
		"Ask the owner for a new invite."),
	"bad_invite": eh(400, false, areaSharing, "The invite text could not be read.",
		"Paste the whole invite the owner sent."),
	"self_invite": eh(400, false, areaSharing, "The invite is from this same server.",
		"Redeem it on another server."),
	"self_redeem": eh(400, false, areaSharing, "A server tried to redeem its own invite.",
		"Redeem it on another server."),
	"bad_identity": eh(400, false, areaSharing, "The other server's id does not match its public key.",
		"Check the server URL; the other server may need an upgrade."),
	"bad_signature": eh(401, false, areaSharing, "A server to server request is not signed by the server it names.",
		"Check that both servers run current versions and the URL is right."),
	"bad_box_key": eh(400, false, areaSharing, "The other server's sealing key is unreadable.",
		"Upgrade the other server and try again."),
	"box_key_required": eh(400, false, areaSharing, "The share hands keys over, and the other server cannot take them.",
		"Upgrade the other server to a version that takes handed over keys."),
	"no_box_key": eh(400, false, areaSharing, "The other server gave no key to seal handed over keys to.",
		"Upgrade the other server and try again."),
	"not_a_share": eh(403, false, areaSharing, "This token is not a server share token.",
		"Use the share token the other server received."),
	"not_handover": eh(403, false, areaSharing, "This share is proxy only; its keys never leave the owner.",
		"Use the shared services through the proxy."),
	"handed_over": eh(409, false, areaSharing, "This key was handed over by another server; its secret and settings cannot change here and it is never revealed.",
		"Change it on the server that owns it."),
	"handover_service": eh(409, false, areaSharing, "This service holds keys another server handed over; its base URL, auth, provider and egress follow the owner.",
		"Change it on the server that owns it."),
	"unknown_share": eh(404, false, areaSharing, "No share matches this revoke.",
		"Nothing to do on this server."),
	"stale": eh(409, false, areaSharing, "The request is out of date: an old revoke push, or a provider review that changed since it was read.",
		"Read the current state again and retry with it."),
	"replay": eh(409, false, areaSharing, "This revoke push was already applied.",
		"Nothing to do."),
	"not_active": eh(409, false, areaSharing, "Only an active share can be paused.",
		"Check the share's status first."),
	"not_paused": eh(409, false, areaSharing, "Only a paused share can be resumed.",
		"Check the share's status first."),
	"not_pending": eh(409, false, areaSharing, "The share was already redeemed, or the review is no longer pending.",
		"Read its current state."),
	"person_share": eh(409, false, areaSharing, "This share is for a person.",
		"Issue a new claim link instead of an invite."),
	"not_person_share": eh(409, false, areaSharing, "This share is for another server.",
		"Issue a new invite instead of a claim link."),
	"claim_invalid": eh(404, false, areaSharing, "The claim link is not valid.",
		"Ask the owner for a new link."),
	"claim_expired": eh(410, false, areaSharing, "The claim link expired or its share ended.",
		"Ask the owner for a new link."),
	"claim_full": eh(409, false, areaSharing, "The link was claimed as many times as the owner allowed.",
		"Ask the owner for a new link."),
	"already_claimed": eh(409, false, areaSharing, "The share was already claimed or has ended.",
		"Use the token you got when you claimed it, or ask for a new link."),
	"nothing_to_reveal": eh(409, false, areaSharing, "No key is in this share's scope right now.",
		"Ask the owner to add keys to the share."),
	"no_revealable_keys": eh(400, false, areaSharing, "A reveal share needs keys that can be revealed, and none are in its scope.",
		"Pick services with keys that are not hidden or handed over."),
	"peer_unreachable": eh(502, true, areaSharing, "The other server could not be reached.",
		"Check its URL and that it is up, then retry."),
	"peer_error": eh(502, true, areaSharing, "The other server answered with an error.",
		"Retry later; check the other server's logs."),
	"preview_failed": eh(502, true, areaSharing, "Previewing the invite on the other server failed.",
		"Check the invite and the other server, then retry."),
	"connect_failed": eh(502, true, areaSharing, "Connecting to the other server failed.",
		"Check the invite and the other server, then retry."),
	"pull_failed": eh(502, true, areaSharing, "Pulling a provider from the other server failed.",
		"Retry later."),
	"edited_here": eh(409, false, areaSharing, "The provider was edited on this server, so a pull would overwrite those edits.",
		"Keep the local edits, or roll them back before pulling."),
	"base_url_changes": eh(409, false, areaSharing, "Pulling the provider would change the base URL of services that use it.",
		"Confirm the change, or keep the current provider."),
	"cannot_share_keys": eh(400, false, areaSharing, "These services cannot share one key pool; the message says why.",
		"Link services of the same provider and auth."),

	// Dashboard and admin API.
	"last_superadmin": eh(409, false, areaAdmin, "The change would leave no active superadmin.",
		"Make another superadmin first."),
	"self_delete": eh(400, false, areaAdmin, "A user tried to delete their own account.",
		"Ask another superadmin to do it."),
	"unknown_provider": eh(400, false, areaAdmin, "No provider has that slug.",
		"List the providers and use a slug from there.").url("/api/admin/providers"),
	"unknown_egress": eh(400, false, areaAdmin, "No egress proxy has that id.",
		"List the egress proxies and use an id from there.").url("/api/admin/egress"),
	"bad_provider": eh(400, false, areaAdmin, "The provider spec is unreadable.",
		"Fix the spec; see the provider spec format.").doc("/docs/providers.md"),
	"bad_spec": eh(400, false, areaAdmin, "The provider spec does not decode or fails validation.",
		"Fix the spec; see the provider spec format.").doc("/docs/providers.md"),
	"invalid_provider": eh(422, false, areaAdmin, "The provider spec fails validation; the message names the field.",
		"Fix that field of the spec.").doc("/docs/providers.md"),
	"invalid_spec": eh(422, false, areaAdmin, "The generated or edited usage guide fails validation.",
		"Fix the field the message names.").doc("/docs/providers.md"),
	"in_use": eh(409, false, areaAdmin, "The provider is still used by a service.",
		"Delete or move those services first."),
	"logo_not_found": eh(404, false, areaAdmin, "No logo could be found for that image or website.",
		"Upload a logo, or pass a different URL."),
	"no_url": eh(400, false, areaAdmin, "A logo fetch got neither an image nor a website.",
		"Pass the image or website to take the logo from."),
	"no_website": eh(400, false, areaAdmin, "The provider has no website to take a logo from.",
		"Set the provider website first, or pass a url."),
	"decrypt_failed": eh(500, false, areaAdmin, "A secret could not be decrypted.",
		"Check that ACNOLOGIA_MASTER_KEY is the key the data was written with.").doc("/docs/configuration.md"),
	"audit_unavailable": eh(503, true, areaAdmin, "A key reveal could not be written to the audit log, so the key was not shown.",
		"Try again."),
	"check_failed": eh(502, true, areaAdmin, "A key health check failed to run.",
		"Retry later."),
	"no_health_check": eh(422, false, areaAdmin, "The provider has no health check to run.",
		"Add a health check to the provider spec, or skip the check.").doc("/docs/providers.md"),
	"untrusted_target": eh(409, false, areaAdmin, "The service's target is not confirmed by a superadmin, so hidden keys are not sent there.",
		"Ask a superadmin to confirm the service's target."),
	"key_banned": eh(409, false, areaAdmin, "The key is suspended (banned) by its provider.",
		"Reset the key after the provider lifts the suspension."),
	"mcp_sync_failed": eh(502, true, areaAdmin, "Syncing tools from the service's upstream MCP server failed.",
		"Check the MCP URL and its auth, then sync again."),
	"no_mcp_url": eh(400, false, areaAdmin, "The service has no upstream MCP URL to sync from.",
		"Set the service's MCP URL first."),
	"nothing_due": eh(409, false, areaAdmin, "No provider is due for an offers refresh.",
		"Nothing to do, or refresh one provider by hand."),
	"refresh_busy": eh(409, true, areaAdmin, "A provider refresh is already running.",
		"Wait for it to finish."),
	"refresh_budget": eh(409, false, areaAdmin, "The provider refresh budget for this period is spent.",
		"Wait for the next period, or raise the budget in the refresh settings."),
	"refresh_error": eh(502, true, areaAdmin, "The provider refresh failed.",
		"Retry later; the run's log says why."),

	// AI features.
	"ai_disabled": eh(503, false, areaAI, "No AI model is configured, so AI features are off.",
		"Set an AI provider key in the server's configuration.").doc("/docs/configuration.md"),
	"ai_error": eh(502, true, areaAI, "The AI model failed.",
		"Retry later."),
	"invalid_model": eh(400, false, areaAI, "The model name is not one the server knows.",
		"Pick a model from GET /api/admin/ai/models.").url("/api/admin/ai/models"),
	"invalid_price": eh(400, false, areaAI, "A model price is not a valid number.",
		"Send prices as non negative numbers."),
	"run_busy": eh(409, true, areaAI, "The AI build is still running.",
		"Cancel it or wait for it to finish."),
	"busy": eh(409, true, areaAI, "This chat is still answering the previous message.",
		"Wait for the answer, or stop it first."),
	"not_running": eh(409, false, areaAI, "The chat is not working on anything.",
		"Send the message normally."),
	"nothing_to_compact": eh(400, false, areaAI, "The chat is too short to compact.",
		"Nothing to do."),
	"compact_failed": eh(502, true, areaAI, "Summarizing the chat failed, so nothing changed.",
		"Retry later."),
}

// errHelpFor returns the help for code.
func errHelpFor(code string) (errHelp, bool) {
	h, ok := errRegistry[code]
	return h, ok
}

// apiError is the error envelope's inner object. Code and Message are the
// original fields; the rest were added for agents and are left out when empty.
type apiError struct {
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	Hint          string  `json:"hint,omitempty"`
	Fix           *errFix `json:"fix,omitempty"`
	DocURL        string  `json:"doc_url,omitempty"`
	RequestID     string  `json:"request_id,omitempty"`
	RetryAfterSec *int    `json:"retry_after_sec,omitempty"`
}

// withHelp fills the hint, fix and doc link from the registry.
func (e apiError) withHelp() apiError {
	h, ok := errHelpFor(e.Code)
	if !ok {
		if e.DocURL == "" {
			e.DocURL = errorsDoc
		}
		return e
	}
	if e.Hint == "" {
		e.Hint = h.Hint
	}
	if e.Fix == nil && !h.Fix.empty() {
		f := h.Fix
		e.Fix = &f
	}
	if e.DocURL == "" {
		e.DocURL = h.docURL(e.Code)
	}
	return e
}

// MarshalJSON adds the registry's help, so an apiError written anywhere,
// not only through writeErr, carries it.
func (e apiError) MarshalJSON() ([]byte, error) {
	type plain apiError
	return json.Marshal(plain(e.withHelp()))
}

// envelopeError is code and msg with everything the response knows: its
// request id (set on the response when it has none) and how long to wait
// when a Retry-After header is set.
func envelopeError(w http.ResponseWriter, code, msg string) apiError {
	return withResponse(w, apiError{Code: code, Message: msg})
}

func withResponse(w http.ResponseWriter, e apiError) apiError {
	if e.RequestID == "" {
		e.RequestID = requestID(w)
	}
	if e.RetryAfterSec == nil {
		if sec, ok := retryAfterSec(w.Header().Get("Retry-After")); ok {
			e.RetryAfterSec = &sec
		}
	}
	return e
}

const requestIDHeader = "X-Request-Id"

// requestID is the response's X-Request-Id, set to a new one when there is
// none yet. Errors carry it, so a caller can quote it to the operator.
func requestID(w http.ResponseWriter) string {
	if v := w.Header().Get(requestIDHeader); v != "" {
		return v
	}
	id := crypto.RandomToken("req_", 9)
	w.Header().Set(requestIDHeader, id)
	return id
}

// ---- MCP tool errors ----

// toolErr is an MCP tool error with a code from the registry.
type toolErr struct {
	code       string
	msg        string
	retryAfter int
}

func (e *toolErr) Error() string { return e.msg }

func toolErrf(code, format string, args ...any) error {
	return &toolErr{code: code, msg: fmt.Sprintf(format, args...)}
}

// toolErrCode classifies an error from a tool: its own code when it has
// one, else a code read from the message, else tool_error.
func toolErrCode(err error) (code string, retry int) {
	var te *toolErr
	if errors.As(err, &te) {
		return te.code, te.retryAfter
	}
	switch {
	case errors.Is(err, errReadOnlyLease):
		return "read_only_token", 0
	case errors.Is(err, errLeaseNotFound):
		return "lease_not_found", 0
	case errors.Is(err, errLeaseRevoked):
		return "lease_revoked", 0
	case errors.Is(err, errPeerLease), errors.Is(err, errHandoverLease):
		return "lease_not_allowed", 0
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "invalid arguments"), strings.HasSuffix(m, " is required"), strings.Contains(m, "does not take"):
		return "invalid_arguments", 0
	case strings.Contains(m, "read-only"):
		return "read_only_token", 0
	case strings.Contains(m, "cannot use service"), strings.Contains(m, "cannot use capability"):
		return "service_not_allowed", 0
	case strings.Contains(m, "no service named"):
		return "unknown_service", 0
	case strings.Contains(m, "unknown tool"), strings.Contains(m, "unknown or disabled tool"):
		return "unknown_tool", 0
	case strings.Contains(m, "reached its") && strings.Contains(m, "limit"):
		return "app_limit_reached", 0
	}
	return "tool_error", 0
}

// toolErrorInfo is what an MCP error result carries next to its text.
func toolErrorInfo(err error) map[string]any {
	code, retry := toolErrCode(err)
	info := map[string]any{"code": code, "message": publicText(err.Error())}
	if h, ok := errHelpFor(code); ok {
		info["hint"] = publicText(h.Hint)
		if !h.Fix.empty() {
			info["fix"] = h.Fix
		}
		info["doc_url"] = h.docURL(code)
	}
	if retry > 0 {
		info["retry_after_sec"] = retry
	}
	return info
}

// ---- suggestions ----

// closeSlugs are the slugs a few edits from name, or containing it (or
// contained in it), closest first, at most n.
func closeSlugs(name string, slugs []string, n int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 128 {
		return nil
	}
	limit := max(2, len(name)/3)
	type cand struct {
		slug string
		d    int
	}
	var cs []cand
	for _, s := range slugs {
		ls := strings.ToLower(s)
		d := levenshtein(name, ls)
		if strings.Contains(ls, name) || strings.Contains(name, ls) {
			d = min(d, 1)
		}
		if d <= limit {
			cs = append(cs, cand{s, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].slug < cs[j].slug
	})
	out := []string{}
	for _, c := range cs {
		if len(out) == n {
			break
		}
		out = append(out, c.slug)
	}
	return out
}

// usableSlugs are the slugs of the services a may call, sorted. Only those:
// an error never names a service the app cannot use.
func (s *Server) usableSlugs(ctx context.Context, a *store.App) []string {
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, sv := range svcs {
		if a.CanUse(sv.Slug) && s.sharedAs(ctx, a, sv) {
			out = append(out, sv.Slug)
		}
	}
	sort.Strings(out)
	return out
}

// slugList names up to n slugs, and how many more there are.
func slugList(slugs []string, n int) string {
	if len(slugs) == 0 {
		return "none"
	}
	if len(slugs) <= n {
		return strings.Join(slugs, ", ")
	}
	return strings.Join(slugs[:n], ", ") + " and " + strconv.Itoa(len(slugs)-n) + " more"
}

// unknownServiceMsg says no service has slug, with close slugs the app may
// use.
func (s *Server) unknownServiceMsg(ctx context.Context, a *store.App, slug string) string {
	msg := "no service named " + slug
	if a != nil {
		if near := closeSlugs(slug, s.usableSlugs(ctx, a), 3); len(near) > 0 {
			msg += "; did you mean " + strings.Join(near, ", ") + "?"
		}
	}
	return msg
}

// notAllowedMsg says the app may not use slug, and which services it may.
func (s *Server) notAllowedMsg(ctx context.Context, a *store.App, slug string) string {
	return "this app cannot use service " + slug + "; it may use: " + slugList(s.usableSlugs(ctx, a), 20)
}
