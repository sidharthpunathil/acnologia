# Control MCP: run the whole server from Claude

Builds on the app MCP (`/mcp`, `server/internal/api/mcp.go`, `mcp_v2.go`) and
admin API tokens (`acnadm_...`, `tokens_auth.go`).

## Why

`/mcp` is for apps: it hands out keys and proxies calls for one app token.
There is no way for Claude (Claude Code, Claude Desktop, claude.ai, Cursor) to
*operate* Acnologia: add services and keys, check health, create apps and
tokens, read usage and events, build providers with AI, share with servers
and people. The dashboard Assistant can do much of it, but only inside the
dashboard. The control MCP exposes every dashboard feature to any MCP client,
as the signed in user, with the same role checks and audit trail.

## Shape

- Endpoint: `POST /mcp/control` (also `DELETE` returns 204, other methods 405
  with `Allow: POST, DELETE`), streamable HTTP with JSON responses, protocol
  `2025-06-18`, the same JSON-RPC envelope as `/mcp` (reuse `rpcReq`,
  `rpcErr`, the decode limit and notification handling).
- Auth: `Authorization: Bearer acnadm_...` only (admin API token). The call
  acts as the token's owner with the lower of the owner's role and the token
  scope (exactly `tokenUser`). App tokens get 401 with
  `WWW-Authenticate: Bearer realm="acnologia"` and a message pointing to
  `/mcp`. It works behind the access password the same way `/api/admin` does
  with an admin token.
- Every tool runs as an **in process request** to `s.handler()` against the
  existing `/api/admin/...` route, carrying the same `Authorization` header
  and `RemoteAddr`, with `X-Requested-With: acnologia-mcp` (same technique as
  `chatSession.api` in `assistant.go`). So roles, `sessionOnly`,
  `notShareApp`, validation and audit are enforced by the routes that already
  exist. The tool layer never touches the store directly for writes.

## Tools

Declared in one table (`server/internal/api/mcp_control.go`): name, title,
description, group, kind (`read`, `write`, `destructive`), minimum role (for
listing only; the route decides), HTTP method, path template, input schema
and which inputs go to path, query and body. Service inputs accept an id,
slug or name (resolve like `chatSession.resolveService`).

Groups and the tools in them (names are the contract):

| Group | Tools |
|---|---|
| `overview` | `overview`, `usage_report`, `list_events`, `settings_summary`, `whoami` |
| `services` | `list_services`, `get_service`, `create_service`, `update_service`, `delete_service`, `service_breaker`, `reset_breaker`, `cache_stats`, `purge_cache`, `check_service_keys` |
| `keys` | `list_keys`, `key_details`, `add_keys`, `update_key`, `delete_key`, `check_key`, `check_all_keys`, `reset_key` |
| `apps` | `list_apps`, `create_app`, `update_app`, `delete_app`, `rotate_app_token`, `list_app_tokens`, `create_app_token`, `revoke_app_token`, `app_usage`, `get_app_mcp`, `update_app_mcp` |
| `providers` | `list_providers`, `get_provider`, `validate_provider`, `create_provider`, `update_provider`, `delete_provider`, `rollback_provider`, `live_check_provider`, `fetch_provider_logo`, `check_provider_offers` |
| `call` | `call_service`, `list_service_examples` |
| `ai` | `ai_status`, `ai_models`, `ai_assist`, `start_provider_build`, `list_ai_runs`, `get_ai_run`, `cancel_ai_run`, `publish_ai_run`, `delete_ai_run` |
| `routing` | `list_webhooks`, `create_webhook`, `update_webhook`, `delete_webhook`, `test_webhook`, `list_egress`, `create_egress`, `update_egress`, `delete_egress`, `test_egress`, `list_groups`, `create_group`, `update_group`, `delete_group` |
| `sharing` | `get_identity`, `update_identity`, `list_shares`, `get_share`, `create_share`, `update_share`, `pause_share`, `resume_share`, `revoke_share`, `extend_share`, `share_invite`, `share_claim_link`, `list_peers`, `preview_peer`, `connect_peer`, `sync_peer`, `disconnect_peer`, `list_peer_providers`, `pull_peer_provider` |
| `refresh` | `refresh_settings`, `update_refresh_settings`, `run_refresh`, `list_refresh_runs`, `list_refresh_reviews`, `apply_refresh_review`, `dismiss_refresh_review` |
| `admin` | `list_users`, `create_user`, `update_user`, `delete_user`, `list_audit` |
| `secrets` | `reveal_key` |
| `raw` | `api_request` `{method, path, query?, body?}` for any `/api/admin/...` path not covered above. Path must start with `/api/admin/`; `/api/admin/tokens` and `PATCH /api/admin/me` are refused (session only anyway). |

The `call` group (on by default) calls the provider APIs themselves.
`call_service` `{service, method, path, query, body, headers}` runs the
request through the proxy handler in process as one app per server named
"Control MCP", made on first use through `POST /api/admin/apps` (mode
proxy, all services) and found again by its id in the `meta` table
(`control_mcp_app_id`). Key rotation, retries, the breaker, the cache,
usage and that app's limits apply, and an admin can limit or turn it off
on the Apps page. It needs the admin role (it spends quota), kind `write`,
`openWorldHint: true`. The result is the status, selected headers
(`Content-Type`, rate limit and `X-Acnologia-*` headers) and the body: JSON
parsed, text as text, binary as base64 under the cap or a size note.
`list_service_examples {service}` returns the provider spec's examples as
ready `call_service` arguments.

Kind drives MCP annotations: `read` tools set `readOnlyHint: true`;
`destructive` tools set `destructiveHint: true`; others
`readOnlyHint: false, destructiveHint: false`; `idempotentHint` for PUTs and
reads; `openWorldHint: true` for tools that reach the internet (checks,
tests, live checks, builds, logo fetch, offers, peers). Every tool has a
`title`.

Destructive tools (deletes, revokes, rotate, disconnect, reset of the
breaker excluded) take a `confirm` boolean. Without `confirm: true` they do
nothing and return a non error result that says exactly what would happen
(for example the service name and key count) and to call again with
`confirm: true`. This keeps clients that auto approve from deleting by
accident.

Results: the route's JSON as a text content block plus `structuredContent`
when the body is a JSON object. Errors from the route return
`isError: true` with the route's `error.message` and status. Results are
capped at 512 KB (trimmed with a note, like `/mcp`).

## Default tool set and flexibility

Default groups: everything except `admin`, `secrets` and `raw`. Tools the
caller's role cannot use are not listed at all (member sees reads; admin adds
writes; superadmin adds `sharing` writes, `refresh` writes, `admin`).

Clients choose with query parameters on the endpoint URL (so they work in
any client config):

- `groups=services,keys,apps` only these groups (`all` means every group,
  including `admin`, `secrets`, `raw`).
- `exclude=delete_service,api_request` drops tools by name.
- `read_only=1` lists only `read` tools and refuses others at call time.
- `confirm=off` turns the confirm step off for clients that ask the user
  themselves (Claude Code, Claude Desktop). Default `on`.

The same are accepted as headers `X-Acnologia-MCP-Groups`,
`X-Acnologia-MCP-Exclude`, `X-Acnologia-MCP-Read-Only`,
`X-Acnologia-MCP-Confirm`; the header wins when both are set.

## Resources and prompts

Resources: `acnologia://overview` (the overview JSON),
`acnologia://services` (services with key counts), `acnologia://docs/{slug}`
for each Markdown doc the server serves under `/docs/{slug}.md` (template in
`resources/templates/list`), and `acnologia://llms-full.txt`.

Prompts: `onboard_provider` (args `provider`, add a service and keys),
`health_report` (summarize failing keys, breakers, budget use),
`set_up_app` (args `name`, `services`: create an app, pick tools, hand back
the MCP URL), `share_with_person` (args `services`, `days`).

`initialize` returns `serverInfo {name: "acnologia-control", version}` and
instructions that explain: acting as the signed in user, the confirm step,
service lookup by slug, and that `/mcp` is the app side.

## Tool list for the dashboard

`GET /api/admin/mcp/control` (any signed in user, session or token) returns:

```json
{
  "url": "<public base>/mcp/control",
  "groups": [{"id": "services", "label": "Services", "default": true}],
  "tools": [{"name": "list_services", "title": "List services", "description": "...", "group": "services", "kind": "read", "available": true}]
}
```

`available` is false when the caller's role cannot use it. Base URL is
`ACNOLOGIA_PUBLIC_URL`, else the request host, like `mcpView`.

## Clients

- `acnologia mcp --control [--url ...] [--token acnadm_...] [--groups ...]
  [--read-only] [--exclude ...]` bridges stdio to `/mcp/control`; without
  `--control` it stays the app bridge. Token type picks the default: an
  `acnadm_` token implies `--control`.
- `acn mcp` runs the same bridge using the current `acn login` context (its
  saved URL and admin token), so `claude mcp add acnologia -- acn mcp` just
  works. `acn mcp config [--client claude-code|claude-desktop|cursor|json]
  [--http]` prints the snippet to paste (stdio via `acn mcp` by default, or
  the HTTP URL plus header with `--http`).
- Dashboard: Settings, "Connect Claude" section: endpoint URL, a button that
  creates an admin API token (name "Claude MCP", chosen scope) and shows it
  once, group toggles and read only switch that rewrite the URL, ready to
  copy snippets (Claude Code `claude mcp add --transport http ...`, Claude
  Desktop JSON using `acn mcp`, Cursor JSON, raw URL plus header), and the
  tool list from `GET /api/admin/mcp/control` grouped, with kind badges.

## Out of scope

OAuth for the MCP endpoint (admin tokens cover it today), SSE streaming,
sampling. Chat thread routes are not exposed (the client is the assistant).
