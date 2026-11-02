# HTTP API

All bodies are JSON. Errors look like
`{"error": {"code": "string", "message": "string"}}` with a matching status.

There are three ways in:

| Surface | Prefix | Auth |
|---|---|---|
| Public | `/healthz`, `/llms.txt`, `/llms-full.txt`, `/docs/{slug}.md`, `/docs/assets/{name}`, `/api/docs`, `/robots.txt` | none |
| Consumer | `/api/v1`, `/proxy`, `/mcp` | app token: `Authorization: Bearer acn_...`, `X-Acnologia-Token`, or `?acn_token=` (proxy only) |
| Dashboard | `/api/auth`, `/api/admin`, `/api/logos` | `acnologia_session` cookie, or an admin API token `Authorization: Bearer acnadm_...` for `/api/admin` |

The consumer API is documented for app and agent authors in
[llms-full.txt](llms-full.txt) (served at `/llms-full.txt`, with a summary at `/llms.txt`). This page covers
the rules that apply to it, then lists the dashboard API.

## Consumer API

| Method and path | Purpose |
|---|---|
| `GET /api/v1/services` | Services the token may use, with `available`, `base_url` and `auth` `{type, name, prefix}`. |
| `POST /api/v1/lease` | Borrow a key: `{service, exclude_key_ids, ttl_sec, wait_sec, exclusive}`. Apps in mode `lease` or `both`. `exclusive: true` keeps the key from every other caller until you report or the lease expires. |
| `POST /api/v1/leases/{id}/report` | Report the outcome: `{outcome, status_code, retry_after_sec, message, rotate}`. |
| `POST /api/v1/leases/{id}/renew` | Extend an open lease: `{ttl_sec}`, returns `{ok, expires_at}`. 404 `lease_not_found` when it is not yours or no longer open. |
| `ANY /proxy/{service}/{path}` | Call the provider through Acnologia with a key injected. Apps in mode `proxy` or `both`. `{service}` may also be a service group. Honors `Cache-Control: no-cache` and `Idempotency-Key`; answers carry `X-Acnologia-Key-Id`, `X-Acnologia-Attempts`, `X-Acnologia-Service` and, when caching applies, `X-Acnologia-Cache`, plus `Idempotent-Replayed: true` on a replay. |
| `POST /mcp` | MCP over JSON-RPC (streamable HTTP, JSON responses). Tools depend on the app's tool sets: core (`list_services`, `pool_status`, `get_key`, `renew_key`, `report_key`, `proxy_request`), service tools generated from provider examples, insights (`list_keys`, `key_details`, `service_stats`) and manage (`check_keys`, `set_key_status`, `add_keys`). Also resources and prompts. |

Status codes an app should handle:

| Status | Code | Meaning |
|---|---|---|
| 401 | `unauthenticated` | Token missing or wrong. |
| 403 | `unauthenticated`, `service_not_allowed`, `proxy_not_allowed`, `lease_not_allowed` | App disabled, or not allowed this service or mode. |
| 400 | `bad_path` | Proxy path with a literal `?`, `#`, or a `.` / `..` segment. |
| 405 | `method_not_allowed` | Proxy method other than GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS. |
| 413 | `body_too_large` | Proxy body over `ACNOLOGIA_MAX_BODY_MB`. |
| 503 | `no_key_available` | Every usable key is cooling down, exhausted or disabled. Wait `retry_after_sec`. |
| 503 | `all_keys_busy` | Usable keys exist but all are at their concurrency limit. Retry shortly or lease with `wait_sec`. |
| 503 | `service_unavailable` | The provider keeps failing and its circuit breaker is open. Wait `retry_after_sec`. |
| 503 | `empty_group` | The group has no services left. |
| 429 | `app_limit_reached` | The app used up a daily or monthly request or budget limit; `limit` and `reset_at` say which and until when. |
| 404 | `lease_not_found` | Renewing a lease that is not open. |
| 409 | `report_failed` | Lease already reported. |
| 410 | `report_failed` | Lease expired more than 10 minutes ago. |
| 502 | `upstream_unreachable` | The provider could not be reached on the first attempt. |

Hidden keys are only used for apps a superadmin marked `lease_hidden`. Other
apps simply never see them; if a service has only hidden keys, those apps get
503 `no_key_available`.

## Dashboard API

Mutating calls (anything but GET, HEAD, OPTIONS) must send `Content-Type:
application/json` or `X-Requested-With`. The dashboard sends both.

### Auth (`/api/auth`)

| Route | Role | Purpose |
|---|---|---|
| `GET /state` | public | `{setup_required, user}` |
| `POST /setup` | public, once | Create the first superadmin `{email, name, password}` and sign in. |
| `POST /login` | public | `{email, password}`. 429 after 10 failures in 10 minutes. |
| `POST /logout` | any | End the session. |

### Admin (`/api/admin`)

Role column: **m** member, **a** admin, **s** superadmin (minimum role).

| Route | Role | Purpose |
|---|:-:|---|
| `GET /me`, `PATCH /me` | m | Own profile; changing the password needs `current_password` and ends other sessions. `PATCH` needs a dashboard session, not an API token. |
| `GET /overview` | m | Key totals (including `banned`), in-flight count, 24 h requests, rate limits, cache hits and spend, cache stats, recent events. |
| `GET /tokens`, `POST /tokens`, `DELETE /tokens/{id}` | m | Your admin API tokens: `{name, scope: read\|write\|admin, expires_in_days}`, scope capped at your role. Create returns the token once. Session only. Superadmins can list everyone's with `?all=1`. |
| `GET /webhooks` | m | Webhooks (URL shown only as a host hint), event types and formats. |
| `POST /webhooks`, `PATCH /webhooks/{id}`, `DELETE /webhooks/{id}` | a | `{name, url, format: json\|slack\|discord\|telegram, events, active}`. Create returns the JSON signing secret once. |
| `POST /webhooks/{id}/test` | a | Send a test event now and report the receiver's answer. |
| `GET /egress` | m | Egress proxies with how many services and keys use each. |
| `POST /egress`, `PATCH /egress/{id}`, `DELETE /egress/{id}` | a | `{name, url, region, notes}`; `http`, `https`, `socks5` or `socks5h` URLs. Changing or deleting a proxy used by a trusted service or a hidden key needs a superadmin. |
| `POST /egress/{id}/test` | a | Request the echo URL through the proxy and report the public IP. |
| `GET /groups`, `POST /groups`, `PATCH /groups/{id}`, `DELETE /groups/{id}` | m / a | Service groups `{slug, name, description, members}` with 2 to 20 member slugs in fallback order. Slugs share the namespace with services. |
| `GET /services/{id}/breaker`, `POST /services/{id}/breaker/reset` | m / a | Circuit breaker state and settings; close it by hand. |
| `POST /services/{id}/cache/purge`, `GET /cache` | a / m | Drop a service's cached responses; cache totals. |
| `GET /usage?days=30` | m | The Usage page: requests, provider spend and cache hits, plus Claude calls, tokens, web searches and cost, for today, 7 and 30 days and the requested range (1 to 366 days). Hidden key traffic only for superadmins. Also `ai_enabled`, `ai_model`, `ai_price` and `event_retention_days`. |
| `GET /apps/{id}/usage?days=30` | m | Today's and this month's requests, spend and cache hits, limits, the limit currently hit, and daily history. |
| `GET /services`, `GET /services/{id}` | m | Services with key counts, request totals, `target_trusted`. |
| `POST /services` | a | Create from a provider's defaults. |
| `PATCH /services/{id}` | a | Edit. Also takes `egress_id` (0 for direct), `cache_ttl_sec` and `cost_per_request` (-1 for the provider default). A non-superadmin changing `base_url`, auth, provider or egress proxy sets `target_trusted=false`; a superadmin submitting those fields sets it true. |
| `DELETE /services/{id}` | a | Delete with its keys. Admins cannot delete a service holding hidden keys. |
| `POST /services/{id}/check` | a | Health check every key in the service. |
| `GET /services/{id}/keys` | m | Keys (hidden ones only for superadmins). Never includes secrets. |
| `POST /services/{id}/keys` | a | Add up to 1000 keys: `{keys: [{secret, label, tier, hidden, restricted, app_ids, weight, priority, notes, quota_limit, max_concurrency, account_name, account_email, account_phone, account_ref}]}`. `hidden` needs a superadmin. |
| `GET /keys` | m | All keys across services, with filters `q`, `service_id`, `validity` and `sort`. |
| `GET /keys/{id}/insights` | m | One key with recent events and 14 days of activity. |
| `PATCH /keys/{id}` | a | Edit a key (including its account fields) or replace its secret. `egress_id` routes it through a proxy (0 falls back to the service's); only a superadmin can set it on hidden keys. |
| `DELETE /keys/{id}` | a | Delete a key. |
| `POST /keys/{id}/check`, `POST /keys/{id}/reset` | a | Health check now; reset to active and clear errors. |
| `POST /keys/check-all` | a | Health check every key. |
| `POST /keys/{id}/reveal` | s | Return the secret. Audited. |
| `GET /events` | m | Request and health events, filters `service_id`, `key_id`, `kind`, `limit` (max 500). |
| `GET /apps` | m | Apps (token prefix only). |
| `POST /apps`, `PATCH /apps/{id}` | a | `{name, description, mode, allowed_services, active, lease_hidden, limits}`. `limits` is `{daily_requests, monthly_requests, daily_budget, monthly_budget}`, null meaning none. Only a superadmin may set `lease_hidden: true`. Create returns the token once. |
| `GET /apps/{id}/mcp`, `PUT /apps/{id}/mcp` | m / a | An app's MCP settings: `{core, service_tools, insights, manage, resources, prompts, exclusive_default, max_result_kb, instructions, disabled_tools}`, with the tool list. `max_result_kb` defaults to 200, at most 2048; `instructions` under 8,000 characters. |
| `POST /apps/{id}/rotate-token` | a | New token, shown once. Clears `lease_hidden` unless a superadmin rotates. |
| `DELETE /apps/{id}` | a | Delete the app and its leases. |
| `GET /providers`, `GET /providers/{slug}` | m | Provider specs, versions and metadata. |
| `POST /providers/validate` | m | Validate a spec and run its fixture tests without saving. |
| `POST /providers`, `PUT /providers/{slug}` | a | Create or publish a new version of a provider. |
| `DELETE /providers/{slug}` | a | Delete an unused, non built-in provider. |
| `POST /providers/{slug}/rollback` | a | Publish an earlier version again. |
| `POST /providers/{slug}/live-check` | a | Run the health check and safe GET examples with a key you supply. |
| `PUT`, `DELETE /providers/{slug}/logo`, `POST /providers/{slug}/logo/fetch` | a | Upload, remove, or discover a logo from the website. |
| `PUT`, `DELETE /services/{id}/logo` | a | Service logo. |
| `GET /ai/status`, `GET /ai/rules` | m | Whether AI is enabled; operator rules for the AI builder. |
| `PUT /ai/rules` | s | Set the rules (max 20,000 characters). |
| `GET /ai/runs`, `GET /ai/runs/{id}?after=n` | m | Runs and their live steps. |
| `POST /ai/runs` | a | Start a run: `{kind, provider_slug, context, urls, test_key, auto_publish, create_service}`. |
| `POST /ai/runs/{id}/cancel`, `POST /ai/runs/{id}/publish` | a | Cancel; publish a run waiting for review. |
| `DELETE /ai/runs/{id}` | a | Delete a finished run and its steps. 409 `run_busy` while it is queued, running, testing or publishing. Audited. Spend already recorded stays in Usage. |
| `DELETE /ai/runs` | a | Clear finished runs: deletes every failed, cancelled and published run and returns `{deleted}`. Runs waiting for review are kept. |
| `POST /ai/assist` | a | "Fill with AI" for dashboard forms. |
| `GET /chat/threads`, `POST /chat/threads` | m | Your assistant conversations (never other users'), and whether AI is enabled. |
| `GET /chat/threads/{id}`, `DELETE /chat/threads/{id}` | m | One conversation's transcript; delete it. |
| `POST /chat/threads/{id}/messages` | m | `{text}`, up to 1 MB. Keys in the text go to the thread's vault before anything reaches Claude. Runs the assistant until it answers; returns the new transcript items. 409 while a previous message is still running, 503 when AI is off. |
| `POST /chat/threads/{id}/confirm` | m | `{pending_id, approve}`. Runs or cancels an action waiting for confirmation, then lets the assistant reply. |
| `POST /chat/threads/{id}/answer` | m | `{ask_id, names, choice, auto}`. Answers an `ask` item (label pasted keys, pick a choice, or let the assistant pick names) and continues the turn. |
| `GET /users`, `POST /users`, `PATCH /users/{id}`, `DELETE /users/{id}` | s | User management. A password reset ends that user's sessions; the last active superadmin cannot be removed. |
| `GET /audit` | s | Audit log (max 1000 rows). |

### Logos (`/api/logos`)

| Route | Role | Purpose |
|---|:-:|---|
| `GET /providers/{slug}`, `GET /services/{id}` | m | Logo bytes, served with `nosniff` and a sandbox CSP. A service falls back to its provider's logo. |

### Key account fields

Every key can record who it belongs to: `account_name`, `account_email`,
`account_phone` and `account_ref` (any other identifier, such as a username or
account id). All are optional strings (max 200, 254, 40 and 200 characters;
the email must contain `@`). They count as personal data: members receive them
empty, and only admins and superadmins can search by them on the Keys page.

### Assistant transcript items

`messages` and `confirm` return `{thread, items, all}`: `items` are the
entries this request created or changed, `all` the full transcript. Each item:

| Field | Meaning |
|---|---|
| `kind` | `user`, `assistant`, `tool`, `confirm`, `ask`, `build` or `error` |
| `text` | message text, or what a confirmation will do. For a user message with keys, the prose without the keys |
| `keys`, `key_list` | for user messages with keys: how many, and `[{label, hint}]` in paste order (hint is the last four characters, never the secret) |
| `tool`, `input`, `ok`, `summary`, `detail` | for tool calls: name, input (secrets redacted), success, one line summary, result JSON |
| `pending` | for confirmations: `{id, status}` with status `waiting`, `approved`, `cancelled` or `expired` |
| `secret` | a new app token, present only in the response that created it |
| `link` | an in-app path to what the tool touched |
| `ask` | for `ask` items: `{id, kind: names\|choice, question, rows, choices, status, answer}` |
| `build` | for `build` items: a provider build the turn follows, `{run_id, title, status, phase, step, ...}` |
| `card`, `entities` | a rich result (keys added, a service, a provider) and the services, keys, apps and providers a tool named, with ids |
| `suggestions` | short follow ups proposed at the end of a turn |
