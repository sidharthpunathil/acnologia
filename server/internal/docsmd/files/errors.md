# Error codes

Every error code the Acnologia API answers with: what it means, whether trying again can help, and what to do next. The hint in an error is the "What to do" line of its code here.

## The error envelope

Every JSON error the API returns has this shape, with a matching HTTP status:

```json
{
  "error": {
    "code": "no_key_available",
    "message": "every key for this service is cooling down, exhausted or disabled",
    "hint": "Wait retry_after_sec, then retry. ...",
    "fix": {"tool": "acnologia_pool_status"},
    "doc_url": "/docs/errors.md#no_key_available",
    "request_id": "req_3kTq9wZx1bQf",
    "retry_after_sec": 30
  }
}
```

- `code` and `message` are as they always were. Branch on `code`; the message is for people and may change.
- `hint` says what to do next.
- `fix` names the next step when there is one: an MCP `tool` to call, a CLI `command` to run, or a `url` (a path on the server) to call or read. Each part is optional, and `fix` is left out when there is none.
- `doc_url` points at the code's entry on this page, or at the doc that covers it.
- `request_id` is also sent as the `X-Request-Id` header. Quote it to the operator: server errors log it.
- `retry_after_sec` is there when the response carries a `Retry-After` header. Some answers (`no_key_available`, `all_keys_busy`, `service_unavailable`, `too_many_attempts`) also repeat it at the top level, next to `error`, as they did before.

Fields may be added later; ignore the ones you do not know.

## MCP tool errors

A tool call on `/mcp` that fails returns a result with `isError: true`. Its text is the message followed by the hint, for clients that read only text. The same facts come as an object, `{code, message, hint, fix, doc_url, retry_after_sec}`, under `_meta["acnologia/error"]`. When the tool declares no output schema with required fields (`acnologia_call_tool`, `acnologia_search_tools` and upstream MCP tools), the object is also the result's `structuredContent`. Tools with a stricter schema leave it out there, because clients check `structuredContent` against the schema even on an error. The codes are the ones below; `invalid_arguments`, `unknown_tool` and `tool_error` occur only on MCP.

When a call is refused and the reason is unclear, call `acnologia_whoami` (or `GET /api/v1/me`): it shows the token, the app's mode, tool sets, limits with what is left, and the services it may use.

## Access and tokens

### `access_denied`

HTTP 400. Retry: no, not without a change.

Someone denied the CLI sign in in the browser.

What to do: Run acn login again if the denial was a mistake.

Next step: command `acn login`.

### `already_decided`

HTTP 409. Retry: no, not without a change.

The CLI sign in was already approved or denied.

What to do: Nothing to do; start a new acn login for another sign in.

### `already_setup`

HTTP 409. Retry: no, not without a change.

First run setup is already done.

What to do: Sign in with the existing superadmin, or ask them to create your account.

Next step: `/api/auth/state`.

### `authorization_pending`

HTTP 400. Retry: yes, after `retry_after_sec` when the error gives one.

The CLI sign in has not been approved in the browser yet.

What to do: Keep polling at the interval the create call gave.

### `bad_credentials`

HTTP 401. Retry: no, not without a change.

The email and password do not match an account.

What to do: Check both. In a script, sign in with acn login instead of a password.

Next step: command `acn login`.

### `bad_password`

HTTP 400. Retry: no, not without a change.

The current password given to change a password is wrong.

What to do: Send the account's current password.

### `csrf`

HTTP 403. Retry: no, not without a change.

A dashboard write came without a JSON content type or the X-Requested-With header.

What to do: Send Content-Type: application/json, or the X-Requested-With header.

### `expired_token`

HTTP 400. Retry: no, not without a change.

The CLI sign in request expired or does not exist.

What to do: Start again with acn login.

Next step: command `acn login`.

### `forbidden`

HTTP 403. Retry: no, not without a change.

The caller is signed in, but its role (or its API token's scope) is below what the route needs.

What to do: Ask a superadmin to raise your role, or create an API token with a wider scope (read, write or admin), never above your own role.

Next step: command `acn whoami`.

### `locked`

HTTP 401. Retry: no, not without a change.

The server is behind its access password and the request did not pass the gate.

What to do: Send the access password in the X-Acnologia-Access header, or an admin API token (Authorization: Bearer acnadm_...). The consumer routes (/api/v1, /proxy, /mcp) and the docs (/llms.txt, /llms-full.txt, /docs/*.md, /api/docs) also take a valid app token or MCP access token.

Next step: `/api/v1`.

### `read_only_token`

HTTP 403. Retry: no, not without a change.

A read-only app token tried to lease, renew or report a key, or to write.

What to do: Use read-only calls (GET or HEAD through /proxy, read-only MCP tools), or ask an admin for a token that is not read-only.

Next step: tool `acnologia_whoami`, `/api/v1/me`.

### `request_expired`

HTTP 400. Retry: no, not without a change.

The MCP sign in (OAuth consent) request expired or was already used.

What to do: Start the connection again from the MCP client.

More in [/docs/mcp-oauth.md](/docs/mcp-oauth.md).

### `session_required`

HTTP 403. Retry: no, not without a change.

The route needs a dashboard sign in; an API token cannot do it (managing API tokens, approving sign ins).

What to do: Do it in the dashboard in a browser. An API token may only revoke itself (acn logout).

### `slow_down`

HTTP 400. Retry: yes, after `retry_after_sec` when the error gives one.

The CLI sign in is polled faster than the interval allows.

What to do: Poll less often: add 5 seconds to the interval.

### `too_many_attempts`

HTTP 429. Retry: yes, after `retry_after_sec` when the error gives one.

Too many wrong passwords, sign in attempts or device codes from this client.

What to do: Wait retry_after_sec (or the Retry-After header) before trying again. Do not loop on guesses.

### `too_many_requests`

HTTP 429. Retry: yes, after `retry_after_sec` when the error gives one.

Too many CLI sign ins are waiting, or this client started too many.

What to do: Wait a minute and try again.

Next step: command `acn login`.

### `unauthenticated`

HTTP 401. Retry: no, not without a change.

The request has no credential, or one that is wrong, revoked, expired, or belongs to a disabled app (403 then).

What to do: Send a valid token: Authorization: Bearer acn_... for /api/v1, /proxy and /mcp; acnadm_... for /api/admin. GET /api/v1/me says what an app token can do.

Next step: `/api/v1/me`.

### `weak_password`

HTTP 400. Retry: no, not without a change.

A new password is shorter than 10 or longer than 72 characters.

What to do: Pick a password of 10 to 72 characters.

### `wrong_password`

HTTP 401. Retry: no, not without a change.

The access password typed on the lock screen is wrong.

What to do: Check the password with the server's operator (ACNOLOGIA_ACCESS_PASSWORD). Wrong guesses are limited per network.

## Services, proxy and leases

### `all_keys_busy`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

Usable keys exist, but each is at its concurrency limit or at a rate limit its provider publishes.

What to do: Retry after retry_after_sec (often 1 second), or lease with wait_sec so the server waits for a free key.

Next step: tool `acnologia_pool_status`.

### `app_limit_reached`

HTTP 429. Retry: yes, after `retry_after_sec` when the error gives one.

The app used up a daily or monthly request or budget limit.

What to do: Wait until reset_at, or ask an admin to raise the limit. GET /api/v1/me shows what is left of each limit.

Next step: tool `acnologia_whoami`, `/api/v1/me`.

### `bad_meta`

HTTP 400. Retry: no, not without a change.

X-Acnologia-Meta is malformed: more than 5 pairs, a repeated key, or characters outside the allowed set.

What to do: Send at most 5 key=value pairs, comma separated, with short ASCII keys and values.

### `bad_params`

HTTP 400. Retry: no, not without a change.

A capability call is missing a required param, or one has the wrong type.

What to do: Send the params GET /api/v1/capabilities lists for the capability.

Next step: `/api/v1/capabilities`.

### `connect_unsupported`

HTTP 400. Retry: no, not without a change.

A CONNECT reached the proxy over HTTP/2 or later.

What to do: Speak HTTP/1.1 to the forward proxy.

### `denied_by_rule`

HTTP 403. Retry: no, not without a change.

One of the app's deny rules covers this request; rule names it.

What to do: Call a different method, service or path, or ask an admin to change the app's deny rules.

### `empty_group`

HTTP 503. Retry: no, not without a change.

The service group has no services left.

What to do: Call one of the services directly, or ask an admin to add services to the group.

Next step: tool `acnologia_list_services`.

### `idempotency_in_progress`

HTTP 409. Retry: yes, after `retry_after_sec` when the error gives one.

A request with the same Idempotency-Key is still running.

What to do: Wait a moment, then retry with the same key to get its answer.

### `idempotency_key_reused`

HTTP 422. Retry: no, not without a change.

This Idempotency-Key was already used with a different request body.

What to do: Use a new Idempotency-Key for a different request.

### `idempotency_unreplayable`

HTTP 422. Retry: no, not without a change.

The first answer for this Idempotency-Key was over 4 MB or a stream, so it was delivered once and cannot be replayed.

What to do: Treat the first call as done. Use a new Idempotency-Key only for a new logical request.

### `invalid_key`

HTTP 401. Retry: no, not without a change.

The demo provider was called without a demo key.

What to do: Call the demo service through /proxy/demo so Acnologia adds a demo key.

### `key_unavailable_for_resource`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

The job or file was created on one key, and that key cannot serve right now. Other keys would answer 404 for it.

What to do: Retry after retry_after_sec. If the key was removed, the resource has to be created again.

### `lease_not_allowed`

HTTP 403. Retry: no, not without a change.

The app is proxy only, or the service is shared by another server or holds handed over keys, which are proxy only.

What to do: Call the provider through /proxy/{service}/... or the acnologia_proxy_request tool instead.

Next step: tool `acnologia_proxy_request`.

### `lease_not_found`

HTTP 404. Retry: no, not without a change.

The lease is not this app's, was already reported, or has expired.

What to do: Lease a new key. Renew a lease before expires_at.

Next step: tool `acnologia_get_key`, `/api/v1/lease`.

### `lease_revoked`

HTTP 403. Retry: no, not without a change.

The app may no longer hold this key (its mode, services, share scope or hidden key grant changed, or the key was disabled), so the lease ended.

What to do: Lease a new key, or use the proxy.

Next step: tool `acnologia_get_key`.

### `limits_unavailable`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

The server could not read the app's limits, so it held the request back.

What to do: Retry after retry_after_sec.

### `loop_detected`

HTTP 508. Retry: no, not without a change.

The request already passed through more than 3 Acnologia servers.

What to do: Call the provider from a server that holds the keys, or break the sharing loop.

### `no_key_available`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

Every usable key for the service is cooling down, out of quota or disabled.

What to do: Wait retry_after_sec, then retry. Check the service's state with acnologia_pool_status; if it stays empty, an admin has to add or fix keys.

Next step: tool `acnologia_pool_status`.

### `no_route`

HTTP 503. Retry: no, not without a change.

No route of the capability can take these params, or _service names none of its routes.

What to do: Change the params or drop _service. GET /api/v1/capabilities lists each route.

Next step: `/api/v1/capabilities`.

### `no_route_worked`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

Every route of the capability failed before reaching a provider; tried lists each.

What to do: Read tried, then retry later or call one service directly.

### `no_stream`

HTTP 500. Retry: no, not without a change.

The connection cannot stream, so GET /mcp cannot open its notification stream.

What to do: Use POST /mcp; poll tools/list instead of streaming.

### `not_a_service`

HTTP 403. Retry: no, not without a change.

The forward proxy was asked for a host that is no service here (or for passthrough this token may not use).

What to do: Call that host directly and list it in NO_PROXY. GET /api/v1/proxy lists the hosts the proxy serves.

Next step: `/api/v1/proxy`.

More in [/docs/api.md#forward-proxy](/docs/api.md#forward-proxy).

### `port_not_allowed`

HTTP 403. Retry: no, not without a change.

A forward proxy passthrough tunnel asked for a port other than 443 or 8443.

What to do: Call that host directly, outside the proxy.

More in [/docs/api.md#forward-proxy](/docs/api.md#forward-proxy).

### `proxy_auth_required`

HTTP 407. Retry: no, not without a change.

The forward proxy URL carries no app token, or a wrong one.

What to do: Put the app token in the proxy URL: http://app:acn_...@host:port. GET /api/v1/proxy returns the URL.

Next step: command `acn proxy env`, `/api/v1/proxy`.

### `proxy_ca`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

The forward proxy's certificate authority is not available.

What to do: Retry later; an admin can check the forward proxy settings.

Next step: `/api/v1/proxy/ca.pem`.

### `proxy_not_allowed`

HTTP 403. Retry: no, not without a change.

The app is in lease mode and may not use the proxy or the forward proxy.

What to do: Lease a key with POST /api/v1/lease instead, or ask an admin to set the app's mode to proxy or both.

Next step: tool `acnologia_get_key`, `/api/v1/lease`.

### `report_failed`

HTTP 409. Retry: no, not without a change.

The report could not apply: unknown lease (404), already reported (409), reported over 10 minutes after expiry (410), or the app lost the service (403).

What to do: Do not retry the same report. Report each lease once, within 10 minutes of expires_at.

### `server_spend_ceiling`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

The server reached its daily spend ceiling, so every app waits until midnight UTC.

What to do: Wait until reset_at, or ask a superadmin to raise the ceiling (PUT /api/admin/settings/spend-ceiling).

### `service_not_allowed`

HTTP 403. Retry: no, not without a change.

The app may not use this service (or capability): it is outside the app's allowed services or its share's scope.

What to do: Pick a service from the app's list, or ask an admin to add this one to the app.

Next step: tool `acnologia_list_services`, `/api/v1/services`.

### `service_unavailable`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

The provider keeps failing, so the circuit breaker paused traffic to it.

What to do: Wait retry_after_sec before trying again. An admin can reset the breaker.

Next step: command `acn breaker <service> --reset`.

### `tag_limit_reached`

HTTP 429. Retry: yes, after `retry_after_sec` when the error gives one.

A caller tag value (X-Acnologia-Meta) used up its tag budget; tag names the value.

What to do: Wait until reset_at, or ask an admin to raise the app's tag budget. GET /api/v1/usage/tags shows use per tag value.

Next step: `/api/v1/usage/tags`.

### `tag_pinned`

HTTP 400. Retry: no, not without a change.

The token is pinned to a value of this tag key, and the request sent a different one.

What to do: Drop the tag from X-Acnologia-Meta, or send the pinned value. GET /api/v1/me shows the token.

### `token_limit_reached`

HTTP 429. Retry: yes, after `retry_after_sec` when the error gives one.

The token used up a request or budget limit of its own.

What to do: Wait until reset_at, or ask an admin to raise the token's limits or the app's per-token default.

Next step: tool `acnologia_whoami`, `/api/v1/me`.

### `too_many_streams`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

Too many event streams are open on this server, or for this person or app.

What to do: Close a stream you no longer read, then retry after retry_after_sec.

### `unknown_service`

HTTP 404. Retry: no, not without a change.

No service, service group or capability has this slug.

What to do: Check the slug against the app's services; the message suggests close ones.

Next step: tool `acnologia_list_services`, `/api/v1/services`.

### `upstream_blocked`

HTTP 502. Retry: no, not without a change.

The provider's address is one the server refuses to call (a cloud metadata or private address).

What to do: Ask an admin to fix the service's base URL or MCP URL.

### `upstream_idle`

HTTP 504. Retry: yes, after `retry_after_sec` when the error gives one.

The provider stopped sending its answer partway.

What to do: Retry; for long jobs ask for a smaller answer.

### `upstream_unreachable`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

The provider could not be reached.

What to do: Retry later. If it keeps failing, check the service's base URL in its guide.

Next step: tool `acnologia_service_guide`.

## MCP tools

### `invalid_arguments`

MCP tool error. Retry: no, not without a change.

A tool's arguments are not valid JSON, miss a required field, or name a field the tool does not take.

What to do: Read the tool's inputSchema in tools/list and call it again with the fields it lists.

Next step: tool `acnologia_search_tools`.

### `tool_error`

MCP tool error. Retry: no, not without a change.

A tool failed for a reason without a more precise code; the message says why.

What to do: Read the message. acnologia_whoami shows what this connection may do.

### `unknown_tool`

MCP tool error. Retry: no, not without a change.

No tool this app may call has that name.

What to do: Call tools/list, or acnologia_search_tools to find a service tool, and use a name it returns.

Next step: tool `acnologia_search_tools`.

## Request shape

### `bad_body`

HTTP 400. Retry: no, not without a change.

The proxy could not read the request body.

What to do: Resend the request with a complete body.

### `bad_content_type`

HTTP 415. Retry: no, not without a change.

A dashboard write was not sent as JSON.

What to do: Send Content-Type: application/json.

### `bad_id`

HTTP 400. Retry: no, not without a change.

An id in the path is not a number.

What to do: Use the numeric id from the matching list route.

### `bad_image`

HTTP 400. Retry: no, not without a change.

An uploaded image is not base64, or not a png, jpeg, webp, gif, svg or ico.

What to do: Upload a supported image type, base64 encoded.

### `bad_json`

HTTP 400. Retry: no, not without a change.

The body is not valid JSON, or a field has the wrong type.

What to do: Send a JSON object with Content-Type: application/json, with the fields the route documents.

More in [/docs/api.md](/docs/api.md).

### `bad_path`

HTTP 400. Retry: no, not without a change.

The proxy path has a literal ?, #, or a . or .. segment.

What to do: Send the query string as query parameters and drop dot segments.

### `bad_query`

HTTP 400. Retry: no, not without a change.

The query string could not be parsed.

What to do: Send the query as key=value pairs joined with &, URL encoded.

### `bad_request`

HTTP 400. Retry: no, not without a change.

The proxy could not build the upstream request.

What to do: Check the path and headers you sent.

### `body_too_large`

HTTP 413. Retry: no, not without a change.

The proxy body is over ACNOLOGIA_MAX_BODY_MB.

What to do: Send a smaller body, or ask an admin to raise the limit.

### `duplicate`

HTTP 409. Retry: no, not without a change.

Something with that name, slug or secret already exists.

What to do: Use another name or slug, or update the existing one.

### `internal`

HTTP 500. Retry: yes, after `retry_after_sec` when the error gives one.

The server failed unexpectedly.

What to do: Retry once. If it repeats, report the request_id to the server's operator; it is in the server log.

### `invalid`

HTTP 400. Retry: no, not without a change.

A field failed validation; the message names it.

What to do: Fix the field the message names and send again.

### `invalid_json`

HTTP 400. Retry: no, not without a change.

The body is not valid JSON.

What to do: Send a valid JSON object.

### `method_not_allowed`

HTTP 405. Retry: no, not without a change.

The method is not one this route takes (the proxy takes GET, HEAD, POST, PUT, PATCH, DELETE and OPTIONS).

What to do: Use a method the route lists; GET /mcp needs Accept: text/event-stream.

### `not_found`

HTTP 404. Retry: no, not without a change.

No such endpoint, or the id or slug in the path does not exist.

What to do: List the collection to get a valid id. GET /api/v1 lists the endpoints for agents.

Next step: `/api/v1`.

### `rate_limited`

HTTP 429. Retry: yes, after `retry_after_sec` when the error gives one.

Too many invite, claim or revoke attempts from this client.

What to do: Wait retry_after_sec (a minute) before trying again.

### `too_large`

HTTP 413. Retry: no, not without a change.

An uploaded logo is over 512 KB.

What to do: Upload a smaller image.

## Sharing

### `already_claimed`

HTTP 409. Retry: no, not without a change.

The share was already claimed or has ended.

What to do: Use the token you got when you claimed it, or ask for a new link.

### `bad_box_key`

HTTP 400. Retry: no, not without a change.

The other server's sealing key is unreadable.

What to do: Upgrade the other server and try again.

### `bad_identity`

HTTP 400. Retry: no, not without a change.

The other server's id does not match its public key.

What to do: Check the server URL; the other server may need an upgrade.

### `bad_invite`

HTTP 400. Retry: no, not without a change.

The invite text could not be read.

What to do: Paste the whole invite the owner sent.

### `bad_signature`

HTTP 401. Retry: no, not without a change.

A server to server request is not signed by the server it names.

What to do: Check that both servers run current versions and the URL is right.

### `base_url_changes`

HTTP 409. Retry: no, not without a change.

Pulling the provider would change the base URL of services that use it.

What to do: Confirm the change, or keep the current provider.

### `box_key_required`

HTTP 400. Retry: no, not without a change.

The share hands keys over, and the other server cannot take them.

What to do: Upgrade the other server to a version that takes handed over keys.

### `cannot_share_keys`

HTTP 400. Retry: no, not without a change.

These services cannot share one key pool; the message says why.

What to do: Link services of the same provider and auth.

### `claim_expired`

HTTP 410. Retry: no, not without a change.

The claim link expired or its share ended.

What to do: Ask the owner for a new link.

### `claim_full`

HTTP 409. Retry: no, not without a change.

The link was claimed as many times as the owner allowed.

What to do: Ask the owner for a new link.

### `claim_invalid`

HTTP 404. Retry: no, not without a change.

The claim link is not valid.

What to do: Ask the owner for a new link.

### `connect_failed`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

Connecting to the other server failed.

What to do: Check the invite and the other server, then retry.

### `edited_here`

HTTP 409. Retry: no, not without a change.

The provider was edited on this server, so a pull would overwrite those edits.

What to do: Keep the local edits, or roll them back before pulling.

### `handed_over`

HTTP 409. Retry: no, not without a change.

This key was handed over by another server; its secret and settings cannot change here and it is never revealed.

What to do: Change it on the server that owns it.

### `handover_service`

HTTP 409. Retry: no, not without a change.

This service holds keys another server handed over; its base URL, auth, provider and egress follow the owner.

What to do: Change it on the server that owns it.

### `invite_expired`

HTTP 410. Retry: no, not without a change.

The invite expired.

What to do: Ask the owner for a new invite.

### `invite_invalid`

HTTP 404. Retry: no, not without a change.

The invite is not valid.

What to do: Ask the owner for a new invite.

### `invite_used`

HTTP 409. Retry: no, not without a change.

The invite was already used.

What to do: Ask the owner for a new invite.

### `no_box_key`

HTTP 400. Retry: no, not without a change.

The other server gave no key to seal handed over keys to.

What to do: Upgrade the other server and try again.

### `no_revealable_keys`

HTTP 400. Retry: no, not without a change.

A reveal share needs keys that can be revealed, and none are in its scope.

What to do: Pick services with keys that are not hidden or handed over.

### `not_a_share`

HTTP 403. Retry: no, not without a change.

This token is not a server share token.

What to do: Use the share token the other server received.

### `not_active`

HTTP 409. Retry: no, not without a change.

Only an active share can be paused.

What to do: Check the share's status first.

### `not_handover`

HTTP 403. Retry: no, not without a change.

This share is proxy only; its keys never leave the owner.

What to do: Use the shared services through the proxy.

### `not_paused`

HTTP 409. Retry: no, not without a change.

Only a paused share can be resumed.

What to do: Check the share's status first.

### `not_pending`

HTTP 409. Retry: no, not without a change.

The share was already redeemed, or the review is no longer pending.

What to do: Read its current state.

### `not_person_share`

HTTP 409. Retry: no, not without a change.

This share is for another server.

What to do: Issue a new invite instead of a claim link.

### `nothing_to_reveal`

HTTP 409. Retry: no, not without a change.

No key is in this share's scope right now.

What to do: Ask the owner to add keys to the share.

### `oauth_handover_refused`

HTTP 400. Retry: no, not without a change.

A handover share cannot include a service whose keys are signed in OAuth accounts: handing them over would send their refresh tokens to the other server.

What to do: Share that service in proxy mode, where the tokens stay on this server, or leave it out of the handover share.

More in [/docs/oauth-credentials.md](/docs/oauth-credentials.md).

### `peer_error`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

The other server answered with an error.

What to do: Retry later; check the other server's logs.

### `peer_unreachable`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

The other server could not be reached.

What to do: Check its URL and that it is up, then retry.

### `person_share`

HTTP 409. Retry: no, not without a change.

This share is for a person.

What to do: Issue a new claim link instead of an invite.

### `preview_failed`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

Previewing the invite on the other server failed.

What to do: Check the invite and the other server, then retry.

### `pull_failed`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

Pulling a provider from the other server failed.

What to do: Retry later.

### `replay`

HTTP 409. Retry: no, not without a change.

This revoke push was already applied.

What to do: Nothing to do.

### `self_invite`

HTTP 400. Retry: no, not without a change.

The invite is from this same server.

What to do: Redeem it on another server.

### `self_redeem`

HTTP 400. Retry: no, not without a change.

A server tried to redeem its own invite.

What to do: Redeem it on another server.

### `share_app`

HTTP 409. Retry: no, not without a change.

This app backs a share to another server.

What to do: Manage it on the Sharing page, or pick another app.

### `share_ended`

HTTP 409. Retry: no, not without a change.

The share is paused or has ended, so no new link can be issued.

What to do: Resume the share first.

### `share_expired`

HTTP 403. Retry: no, not without a change.

The share this token belongs to expired.

What to do: Ask the owner to extend it or for a new link.

### `share_paused`

HTTP 403. Retry: yes, after `retry_after_sec` when the error gives one.

The owner paused the share this token belongs to.

What to do: Wait for the owner to resume it, or ask them.

### `share_revoked`

HTTP 403. Retry: no, not without a change.

The owner revoked the share this token belongs to.

What to do: Ask the owner for a new share or claim link.

### `share_token`

HTTP 403. Retry: no, not without a change.

The key is a share token from another server and is never revealed.

What to do: Use the service through the proxy.

### `stale`

HTTP 409. Retry: no, not without a change.

The request is out of date: an old revoke push, or a provider review that changed since it was read.

What to do: Read the current state again and retry with it.

### `unknown_share`

HTTP 404. Retry: no, not without a change.

No share matches this revoke.

What to do: Nothing to do on this server.

## Dashboard and admin API

### `app_off`

HTTP 403. Retry: no, not without a change.

The internal Try it app is turned off.

What to do: Turn the Try it app back on in Apps, or send app_id to try as another app.

### `audit_unavailable`

HTTP 503. Retry: yes, after `retry_after_sec` when the error gives one.

A key reveal could not be written to the audit log, so the key was not shown.

What to do: Try again.

### `backup_failed`

HTTP 500. Retry: yes, after `retry_after_sec` when the error gives one.

The server could not copy its database, usually for lack of disk space in the data directory.

What to do: Free some space in the data directory and try again. The server log has the cause.

### `bad_app`

HTTP 400. Retry: no, not without a change.

Try it named an app that does not exist or may not use this service.

What to do: Pick an app from GET /api/admin/apps that may use the service, or leave app_id out to use the Try it app.

### `bad_provider`

HTTP 400. Retry: no, not without a change.

The provider spec is unreadable.

What to do: Fix the spec; see the provider spec format.

More in [/docs/providers.md](/docs/providers.md).

### `bad_spec`

HTTP 400. Retry: no, not without a change.

The provider spec does not decode or fails validation.

What to do: Fix the spec; see the provider spec format.

More in [/docs/providers.md](/docs/providers.md).

### `check_failed`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

A key health check failed to run.

What to do: Retry later.

### `decrypt_failed`

HTTP 500. Retry: no, not without a change.

A secret could not be decrypted.

What to do: Check that ACNOLOGIA_MASTER_KEY is the key the data was written with.

More in [/docs/configuration.md](/docs/configuration.md).

### `demo_loaded`

HTTP 409. Retry: no, not without a change.

Demo data is already loaded.

What to do: Remove it first with DELETE /api/admin/demo, then load it again.

### `has_services`

HTTP 409. Retry: no, not without a change.

The server already has services, so demo data is not added by default.

What to do: Send {"force": true} to POST /api/admin/demo to add it anyway.

### `in_use`

HTTP 409. Retry: no, not without a change.

The provider is still used by a service.

What to do: Delete or move those services first.

### `invalid_oauth_client`

HTTP 400. Retry: no, not without a change.

The OAuth client id, secret or scopes given are not usable.

What to do: Send a one line client_id, and scopes the provider allows (oauth_settings lists allowed_scopes when it limits them).

Next step: tool `acnologia_oauth_settings`.

More in [/docs/oauth-credentials.md](/docs/oauth-credentials.md).

### `invalid_oauth_fields`

HTTP 400. Retry: no, not without a change.

The extra fields for an OAuth connection are missing one the provider needs, or name one it does not have.

What to do: oauth_settings lists the fields; send the required ones, by key.

Next step: tool `acnologia_oauth_settings`.

More in [/docs/oauth-credentials.md](/docs/oauth-credentials.md).

### `invalid_provider`

HTTP 422. Retry: no, not without a change.

The provider spec fails validation; the message names the field.

What to do: Fix that field of the spec.

More in [/docs/providers.md](/docs/providers.md).

### `invalid_spec`

HTTP 422. Retry: no, not without a change.

The generated or edited usage guide fails validation.

What to do: Fix the field the message names.

More in [/docs/providers.md](/docs/providers.md).

### `key_banned`

HTTP 409. Retry: no, not without a change.

The key is suspended (banned) by its provider.

What to do: Reset the key after the provider lifts the suspension.

### `last_superadmin`

HTTP 409. Retry: no, not without a change.

The change would leave no active superadmin.

What to do: Make another superadmin first.

### `logo_not_found`

HTTP 404. Retry: no, not without a change.

No logo could be found for that image or website.

What to do: Upload a logo, or pass a different URL.

### `mcp_sync_failed`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

Syncing tools from the service's upstream MCP server failed.

What to do: Check the MCP URL and its auth, then sync again.

### `no_demo`

HTTP 404. Retry: no, not without a change.

No demo data is loaded.

What to do: Load it with POST /api/admin/demo.

### `no_health_check`

HTTP 422. Retry: no, not without a change.

The provider has no health check to run.

What to do: Add a health check to the provider spec, or skip the check.

More in [/docs/providers.md](/docs/providers.md).

### `no_mcp_url`

HTTP 400. Retry: no, not without a change.

The service has no upstream MCP URL to sync from.

What to do: Set the service's MCP URL first.

### `no_url`

HTTP 400. Retry: no, not without a change.

A logo fetch got neither an image nor a website.

What to do: Pass the image or website to take the logo from.

### `no_website`

HTTP 400. Retry: no, not without a change.

The provider has no website to take a logo from.

What to do: Set the provider website first, or pass a url.

### `not_oauth_service`

HTTP 400. Retry: no, not without a change.

An OAuth route was called for a service that takes API keys, not OAuth sign in.

What to do: Add keys to that service instead. OAuth routes are for services of an oauth2 provider.

More in [/docs/oauth-credentials.md](/docs/oauth-credentials.md).

### `nothing_due`

HTTP 409. Retry: no, not without a change.

No provider is due for an offers refresh.

What to do: Nothing to do, or refresh one provider by hand.

### `oauth_not_configured`

HTTP 409. Retry: no, not without a change.

The service has no OAuth client id yet, so there is nothing to sign in with.

What to do: Register an OAuth app with the provider using the redirect URI oauth_settings shows, then set its client id and secret.

Next step: tool `acnologia_set_oauth_client`.

More in [/docs/oauth-credentials.md](/docs/oauth-credentials.md).

### `refresh_budget`

HTTP 409. Retry: no, not without a change.

The provider refresh budget for this period is spent.

What to do: Wait for the next period, or raise the budget in the refresh settings.

### `refresh_busy`

HTTP 409. Retry: yes, after `retry_after_sec` when the error gives one.

A provider refresh is already running.

What to do: Wait for it to finish.

### `refresh_error`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

The provider refresh failed.

What to do: Retry later; the run's log says why.

### `restore_blocked`

HTTP 409. Retry: no, not without a change.

A trashed item needs something else first: a key whose service is in the trash too, or a service whose provider was deleted.

What to do: Restore the service first (it is in the trash list), or add the provider again, then restore.

Next step: tool `acnologia_list_trash`, command `acn trash`.

### `restore_conflict`

HTTP 409. Retry: no, not without a change.

A trashed item cannot come back because its place was taken: another service now has its slug, or its service has another key with the same secret.

What to do: Rename or delete the item that took its place, then restore again.

Next step: tool `acnologia_list_trash`, command `acn trash`.

### `self_delete`

HTTP 400. Retry: no, not without a change.

A user tried to delete their own account.

What to do: Ask another superadmin to do it.

### `too_many_rules`

HTTP 400. Retry: no, not without a change.

The app already has the most deny rules it can hold.

What to do: Delete a rule you no longer need, or merge rules with a shorter path prefix.

### `unknown_egress`

HTTP 400. Retry: no, not without a change.

No egress proxy has that id.

What to do: List the egress proxies and use an id from there.

Next step: `/api/admin/egress`.

### `unknown_provider`

HTTP 400. Retry: no, not without a change.

No provider has that slug.

What to do: List the providers and use a slug from there.

Next step: `/api/admin/providers`.

### `untrusted_target`

HTTP 409. Retry: no, not without a change.

The service's target is not confirmed by a superadmin, so hidden keys are not sent there.

What to do: Ask a superadmin to confirm the service's target.

## AI features

### `ai_disabled`

HTTP 503. Retry: no, not without a change.

No AI model is configured, so AI features are off.

What to do: Set an AI provider key in the server's configuration.

More in [/docs/configuration.md](/docs/configuration.md).

### `ai_error`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

The AI model failed.

What to do: Retry later.

### `busy`

HTTP 409. Retry: yes, after `retry_after_sec` when the error gives one.

This chat is still answering the previous message.

What to do: Wait for the answer, or stop it first.

### `compact_failed`

HTTP 502. Retry: yes, after `retry_after_sec` when the error gives one.

Summarizing the chat failed, so nothing changed.

What to do: Retry later.

### `invalid_model`

HTTP 400. Retry: no, not without a change.

The model name is not one the server knows.

What to do: Pick a model from GET /api/admin/ai/models.

Next step: `/api/admin/ai/models`.

### `invalid_price`

HTTP 400. Retry: no, not without a change.

A model price is not a valid number.

What to do: Send prices as non negative numbers.

### `not_running`

HTTP 409. Retry: no, not without a change.

The chat is not working on anything.

What to do: Send the message normally.

### `nothing_to_compact`

HTTP 400. Retry: no, not without a change.

The chat is too short to compact.

What to do: Nothing to do.

### `run_busy`

HTTP 409. Retry: yes, after `retry_after_sec` when the error gives one.

The AI build is still running.

What to do: Cancel it or wait for it to finish.
