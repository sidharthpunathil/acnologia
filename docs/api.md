# HTTP API

All bodies are JSON. Errors look like
`{"error": {"code": "string", "message": "string"}}` with a matching status.

There are six ways in:

| Surface | Prefix | Auth |
|---|---|---|
| Public | `/healthz`, `/llms.txt`, `/llms-full.txt`, `/docs/{slug}.md`, `/docs/assets/{name}`, `/api/docs`, `/robots.txt`, `/.well-known/acnologia`, `/api/gate` | none (behind the [access password](configuration.md#access-password) only `/healthz`, `/robots.txt`, `/.well-known/acnologia` and `/api/gate` stay open) |
| Server to server | `/api/peer/v1` | an invite (`preview`, `redeem`), a share token (`manifest`, `leave`, `providers/{slug}/logo`, `keys`, `report`) or the owner's signature (`revoke`); see [Sharing between servers](#sharing-between-servers) |
| Claim links | `/api/claim/v1` | a claim code (`acnclaim_...`) from a share with a person; see [Sharing with people](#sharing-with-people) |
| Consumer | `/api/v1`, `/proxy`, `/mcp` | app token: `Authorization: Bearer acn_...`, `X-Acnologia-Token`, or `?acn_token=` (proxy only) |
| Dashboard | `/api/auth`, `/api/admin`, `/api/logos`, `/api/cli/device` | `acnologia_session` cookie, or an admin API token `Authorization: Bearer acnadm_...` for `/api/admin` |
| Control MCP | `/mcp/control` | admin API token only: `Authorization: Bearer acnadm_...`; see [Control MCP](#control-mcp-mcpcontrol) |

The consumer API is documented for app and agent authors in
[llms-full.txt](llms-full.txt) (served at `/llms-full.txt`, with a summary at `/llms.txt`). This page covers
the rules that apply to it, then lists the dashboard API.

## Consumer API

| Method and path | Purpose |
|---|---|
| `GET /api/v1/services` | Services the token may use, with `available`, `base_url`, `auth` `{type, name, prefix}` and `guide_url`. |
| `GET /api/v1/services/{slug}/guide` | The service's usage guide, only for a service the token may use (403 `service_not_allowed` otherwise). `Accept: text/markdown` or `?format=md` returns raw Markdown; otherwise `{markdown, guide, generated}` (see [usage guides](providers.md#usage-guides)). |
| `GET /api/v1/me` | The token's own access: `{app: {id, name}, limits: {daily_requests, monthly_requests, daily_budget, monthly_budget}, usage: {today_requests, today_cost, month_requests, month_cost, last_used_at, limit_hit}, limit_hit, services: [{slug, name, provider, category, available, logo_url}], share}` (`logo_url` is always null). `share` is null for an ordinary app; for a share's token it is `{id, name, owner: {name, id}, owner_name, owner_id, recipient, label, status, mode, ends_at}`. A share token whose share is paused, expired or revoked still gets 200 here with that `status` and no services. `available` counts keys free right now for a share's token; for an ordinary app it is 1 or 0, since apps never learn pool sizes. |
| `POST /api/v1/lease` | Borrow a key: `{service, exclude_key_ids, ttl_sec, wait_sec, exclusive}`. Apps in mode `lease` or `both`. `exclusive: true` keeps the key from every other caller until you report or the lease expires. |
| `POST /api/v1/leases/{id}/report` | Report the outcome: `{outcome, status_code, retry_after_sec, message, rotate}`. |
| `POST /api/v1/leases/{id}/renew` | Extend an open lease: `{ttl_sec}`, returns `{ok, expires_at}`. 404 `lease_not_found` when it is not yours or no longer open. |
| `ANY /proxy/{service}/{path}` | Call the provider through Acnologia with a key injected. Apps in mode `proxy` or `both`. `{service}` may also be a service group. Honors `Cache-Control: no-cache` and `Idempotency-Key`; answers carry `X-Acnologia-Key-Id` and `X-Acnologia-Attempts` (not for a share's or a person's token), `X-Acnologia-Service` and, when caching applies, `X-Acnologia-Cache`, plus `Idempotent-Replayed: true` on a replay. A caller that may not lease the key gets bodies with the key replaced by `[key]`. A share's token reaches only the services it was made with: a slug that now names another service or a group answers 403 `service_not_allowed`. |
| `POST /mcp` | MCP over JSON-RPC (streamable HTTP, JSON responses). Tools depend on the app's tool sets: core (`list_services`, `pool_status`, `service_guide`, `get_key`, `renew_key`, `report_key`, `proxy_request`), service tools generated from provider examples, insights (`list_keys`, `key_details`, `service_stats`) and manage (`check_keys`, `set_key_status`, `add_keys`). Also resources (including `acnologia://services/{slug}/guide`, the usage guide as Markdown) and prompts. |

Status codes an app should handle:

| Status | Code | Meaning |
|---|---|---|
| 401 | `unauthenticated` | Token missing or wrong. |
| 403 | `unauthenticated`, `service_not_allowed`, `proxy_not_allowed`, `lease_not_allowed` | App disabled, or not allowed this service or mode. |
| 400 | `bad_path` | Proxy path with a literal `?`, `#`, or a `.` / `..` segment. |
| 400 | `bad_body` | The proxy could not read the request body. |
| 405 | `method_not_allowed` | Proxy method other than GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS. |
| 413 | `body_too_large` | Proxy body over `ACNOLOGIA_MAX_BODY_MB`. |
| 503 | `no_key_available` | Every usable key is cooling down, exhausted or disabled. Wait `retry_after_sec`. |
| 503 | `all_keys_busy` | Usable keys exist but all are at their concurrency limit or at a rate limit their provider publishes in `offers`. Retry after `retry_after_sec` or lease with `wait_sec`. |
| 503 | `service_unavailable` | The provider keeps failing and its circuit breaker is open. Wait `retry_after_sec`. |
| 503 | `empty_group` | The group has no services left. |
| 429 | `app_limit_reached` | The app used up a daily or monthly request or budget limit; `limit` and `reset_at` say which and until when. |
| 404 | `unknown_service` | No service (or, for the proxy, service group) has that slug. |
| 404 | `lease_not_found` | Renewing a lease that is not open. |
| 409 | `report_failed` | Lease already reported. |
| 410 | `report_failed` | Lease expired more than 10 minutes ago. |
| 502 | `upstream_unreachable` | The provider could not be reached on the first attempt. |
| 403 | `share_revoked`, `share_paused`, `share_expired` | Only for a share token (another server's, or a person's from a claim link): the owner revoked, paused or let the share expire. The answer also carries `X-Acnologia-Share: revoked`, `paused` or `expired`. |
| 403 | `lease_not_allowed` | Leasing a service another server shares with this one, or one holding keys another server handed over. Those are proxy only. |
| 508 | `loop_detected` | The request already passed through more than 3 Acnologia servers (`X-Acnologia-Hop`). |

Hidden keys are only used for apps a superadmin marked `lease_hidden`. Other
apps simply never see them; if a service has only hidden keys, those apps get
503 `no_key_available`.

## Dashboard API

Mutating calls (anything but GET, HEAD, OPTIONS) must send `Content-Type:
application/json` or `X-Requested-With`. The dashboard sends both.

### Access lock (`/api/gate`)

Open while the site is locked, so the lock screen can reach them. See
[Access password](configuration.md#access-password).

| Route | Role | Purpose |
|---|---|---|
| `GET /api/gate` | public | `{required, locked, password_only}`. `password_only` is true when the access password is the whole login (`ACNOLOGIA_ACCESS_SIGNS_IN`). |
| `POST /api/gate/unlock` | public | `{password}`. Sets the `acnologia_gate` cookie and returns `{required, locked: false}`. 401 `wrong_password`; 429 `too_many_attempts` with `retry_after_sec` after 5 wrong guesses a minute or 50 a day. |
| `POST /api/gate/lock` | public | Drops this browser's gate cookie. |

### Auth (`/api/auth`)

| Route | Role | Purpose |
|---|---|---|
| `GET /state` | public | `{setup_required, user}` |
| `POST /setup` | public, once | Create the first superadmin `{email, name, password}` and sign in. |
| `POST /login` | public | `{email, password}`. 429 after 10 failures in 10 minutes. |
| `POST /logout` | any | End the session. |

### CLI sign in (`/api/cli/device`)

The device flow behind `acn login` ([Command line](cli.md#how-the-sign-in-works)).
Create and poll are open in the site lock and need no account; the rest need a
dashboard session (not an API token).

| Route | Role | Purpose |
|---|---|---|
| `POST /api/cli/device` | public | `{client, hostname, os}` returns `{device_code, user_code, verification_uri, verification_uri_complete, expires_in, interval}`. 30 a minute across all clients. |
| `POST /api/cli/device/token` | public | `{device_code}`. 400 `authorization_pending`, `slow_down` (with `interval`), `access_denied` or `expired_token`; 200 `{token, token_id, user, scope, expires_at}` once after approval. |
| `GET /api/cli/device/lookup?user_code=` | m, session | `{request: {user_code, client, hostname, os, created_at, expires_at, state}}`. Wrong codes: 10 a minute, 200 a day, then 429. |
| `POST /api/cli/device/approve` | m, session | `{user_code, scope, label}`. Creates a 90 day admin token named `acn on <hostname>` (or `label`), scope capped at your role. |
| `POST /api/cli/device/deny` | m, session | `{user_code}`. The CLI's next poll gets `access_denied`. |

### Admin (`/api/admin`)

Role column: **m** member, **a** admin, **s** superadmin (minimum role).

| Route | Role | Purpose |
|---|:-:|---|
| `GET /me`, `PATCH /me` | m | Own profile; changing the password needs `current_password` and ends other sessions. `PATCH` needs a dashboard session, not an API token. |
| `GET /overview` | m | Key totals (including `banned`), in-flight count, 24 h requests, rate limits, cache hits and spend, cache stats, recent events. |
| `GET /tokens`, `POST /tokens`, `DELETE /tokens/{id}` | m | Your admin API tokens: `{name, scope: read\|write\|admin, expires_in_days}`, scope capped at your role. Create returns the token once. Session only, except that a token may `DELETE` itself (`acn logout`). Superadmins can list everyone's with `?all=1`. |
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
| `GET /services`, `GET /services/{id}` | m | Services with key counts, request totals, `target_trusted`. A service another server shares also has `peer_link_id`, `peer_name`, `peer_remote_slug` and `peer_provider` (the owner's provider slug, which names, describes and gives the logo of the service while `provider` stays `acnologia-peer`). |
| `POST /services` | a | Create from a provider's defaults. |
| `PATCH /services/{id}` | a | Edit. Also takes `egress_id` (0 for direct), `cache_ttl_sec` and `cost_per_request` (-1 for the provider default). A non-superadmin changing `base_url`, auth, provider or egress proxy sets `target_trusted=false`; a superadmin submitting those fields sets it true. On a service holding keys another server handed over, those fields follow the owner and a change is refused (409 `handover_service`). |
| `DELETE /services/{id}` | a | Delete with its keys. Admins cannot delete a service holding hidden keys. |
| `POST /services/{id}/check` | a | Health check every key in the service. |
| `GET /services/{id}/keys` | m | Keys (hidden ones only for superadmins). Never includes secrets. A key this server handed over to another server has `handed_over: [{share_id, share_name, peer_id, peer_name, live, handed_at, valid_until, wiped_at, reported_status}]`; a key a reveal share showed to a person has `revealed_to: [{share_id, share_name, label, revealed_at}]` (for good: revoke cannot take it back); a key another server handed over to this one has `peer_link_id`, `peer_valid_until` (deleted then unless renewed) and `peer_remote_key_id`. |
| `POST /services/{id}/keys` | a | Add up to 1000 keys: `{keys: [{secret, label, tier, hidden, restricted, app_ids, weight, priority, notes, quota_limit, max_concurrency, account_name, account_email, account_phone, account_ref}]}`. `hidden` needs a superadmin. |
| `GET /keys` | m | All keys across services, with filters `q`, `service_id`, `validity` and `sort`. |
| `GET /keys/{id}/insights` | m | One key with recent events and 14 days of activity. |
| `PATCH /keys/{id}` | a | Edit a key (including its account fields) or replace its secret. `egress_id` routes it through a proxy (0 falls back to the service's); only a superadmin can set it on hidden keys. On a key another server handed over, `secret`, `hidden` and `egress_id` are refused (403 `handed_over`). |
| `DELETE /keys/{id}` | a | Delete a key. |
| `POST /keys/{id}/check`, `POST /keys/{id}/reset` | a | Health check now; reset to active and clear errors. |
| `POST /keys/check-all` | a | Health check every key. |
| `POST /keys/{id}/reveal` | s | Return the secret. Audited. Refused (403) for a share token (`share_token`) and for a key another server handed over (`handed_over`). |
| `GET /events` | m | Request and health events, filters `service_id`, `key_id`, `kind`, `limit` (max 500). |
| `GET /apps` | m | Apps (not those backing a share; their changes answer 409 `share_app`). Each carries `token_count` (tokens neither revoked nor expired) and `last_used_at` (the latest use by any of its tokens). Token values are never returned. |
| `POST /apps`, `PATCH /apps/{id}` | a | `{name, description, mode, allowed_services, active, lease_hidden, limits}`. `limits` is `{daily_requests, monthly_requests, daily_budget, monthly_budget}`, null meaning none. Only a superadmin may set `lease_hidden: true`. Create returns the first token once as `token`, with its record (named `Default`) as `app_token`. |
| `GET /mcp/control` | m | The control MCP for the Connect Claude card: `{url, groups: [{id, label, default}], tools: [{name, title, description, group, kind, available}]}`. `kind` is `read`, `write` or `destructive`; `available` is false when your role cannot use the tool. `url` uses `ACNOLOGIA_PUBLIC_URL`, else the request host. See [Control MCP](#control-mcp-mcpcontrol). |
| `GET /apps/{id}/mcp`, `PUT /apps/{id}/mcp` | m / a | An app's MCP settings: `{core, service_tools, insights, manage, resources, prompts, exclusive_default, max_result_kb, instructions, disabled_tools}`, with the tool list. `max_result_kb` defaults to 200, at most 2048; `instructions` under 8,000 characters. |
| `GET /apps/{id}/tokens` | m | The app's tokens, revoked ones included: `{id, name, prefix, created_at, created_by, created_by_email, last_used_at, expires_at, revoked_at}`. `prefix` is the first 8 characters plus `...`. `last_used_at` is written at most once a minute per token. |
| `POST /apps/{id}/tokens` | a | Add a token: `{name, expires_in_days?}` (name up to 64 characters, expiry 1 to 3650 days, omitted means never). Returns `{token, app_token}`; the token is shown once. The app's other tokens keep working. Clears `lease_hidden` unless a superadmin creates it. |
| `PATCH /apps/{id}/tokens/{tokenId}` | a | Rename: `{name}`. |
| `DELETE /apps/{id}/tokens/{tokenId}` | a | Revoke the token at once. It stays listed with `revoked_at` set. Token changes are audited as `app.token_create`, `app.token_rename`, `app.token_revoke` and `app.rotate_token`. |
| `POST /apps/{id}/rotate-token` | a | Kept for compatibility: creates one new token and revokes all the app's other tokens. Returns `{app, token, app_token}`; the token is shown once. Clears `lease_hidden` unless a superadmin rotates. |
| `DELETE /apps/{id}` | a | Delete the app and its leases. |
| `GET /providers`, `GET /providers/{slug}` | m | Provider specs, versions and metadata. Specs may carry `offers` (prices, free allowances and limits per endpoint; see [providers.md](providers.md#offers)). `refresh` (absent when never checked) is `{checked_at, trigger, outcome, run_id, changed_at, changed_version, pending_review}` from the provider refresh. |
| `GET /providers/{slug}/guide` | m | The provider's usage guide: `{markdown, guide, generated}`. `guide` is the JSON form `{provider, name, service, summary, generated, proxy_base, docs_url, website, endpoints, examples: [{name, method, path, query, body, curl}], limits, errors, notes, markdown}`; `generated` is true when the spec has no `guide` and one was assembled from its examples and offers. `?service=<slug>` fills the proxy URL for a service built on this provider (400 otherwise). `?format=md` or `Accept: text/markdown` returns the Markdown alone. |
| `GET /services/{id}/guide` | m | The same for one service, with its real proxy URL (for a service another server shares, the owner's provider describes it). |
| `POST /providers/validate` | m | Validate a spec and run its fixture tests without saving. |
| `POST /providers`, `PUT /providers/{slug}` | a | Create or publish a new version of a provider. |
| `DELETE /providers/{slug}` | a | Delete an unused, non built-in provider. |
| `POST /providers/{slug}/rollback` | a | Publish an earlier version again. |
| `POST /providers/{slug}/guide/generate` | a | Write or rewrite the provider's usage guide with AI from its docs URL, examples and offers, and save it as a new version (source `ai`). Optional body `{instruction}` (up to 4,000 characters). Synchronous, up to 5 minutes. Answers `{provider, updated, verified, summary, guide, markdown, model, cost_usd}`; `updated: false` when nothing changed. 503 `ai_disabled` without AI, 422 `invalid_spec`, 400 `ai_error`. |
| `POST /providers/{slug}/live-check` | a | Run the health check and safe GET examples with a key you supply. |
| `PUT`, `DELETE /providers/{slug}/logo`, `POST /providers/{slug}/logo/fetch` | a | Upload, remove, or discover a logo from the website. |
| `PUT`, `DELETE /services/{id}/logo` | a | Service logo. |
| `GET /ai/status`, `GET /ai/rules` | m | Whether AI is enabled, the effective default `model` and each feature's model in `models`; operator rules for the AI builder. |
| `PUT /ai/rules` | s | Set the rules (max 20,000 characters). |
| `GET /ai/models` | a | Models to pick from: `{models: [{id, display_name, context_window, input_price, output_price, pricing_known, builtin}], source, note, settings, effective, env_model, builtin_model, compact_tokens, compact_from_env, ai_enabled, can_edit}`. Prices are USD per million tokens. `source` is `api` when the list comes from Anthropic's Models API (cached for an hour and merged with Acnologia's built in table for prices and context windows; a model only the API knows has `pricing_known: false`), or `builtin` when there is no API key or the call failed, with `note` saying which. `effective` maps `default`, `assistant`, `builder`, `fill`, `summary` and `refresh` to `{model, source, context_window}`, where `source` is `override`, `saved`, `env` or `builtin`. `compact_tokens` is the chat compaction threshold for the Assistant's model. |
| `PUT /ai/models` | s | Save the model choice: `{default, assistant, builder, fill, summary, refresh}`, each a model id or `""` for "use the default". Per call, a feature's own model wins, then the saved default, then `ACNOLOGIA_AI_MODEL`, then `claude-opus-5-5`. Ids must be in the list above (or already in use); one the Models API lists but Acnologia has no price for saves with a `warnings` entry. 400 `invalid_model` otherwise. Takes effect on the next AI call with no restart; a builder run keeps the model it was queued with, an Assistant turn the model it started with. Returns the `GET` fields without `models`, plus `warnings`. Audited as `ai.models` with old and new values. |
| `PUT /ai/prices/{model}` | s | Override a model's price: `{input, output, cache_write, cache_read, context_window, source_url, checked_at}`, USD per million tokens (cache prices 0 mean 1.25x and 0.1x of input; `context_window` 0 keeps the built in one). Applies to cost accounting on the next call. Returns the `GET /ai/models` body, whose models now also carry `cache_write_price`, `cache_read_price`, `price_source` (`builtin`, `manual` or `refresh`), `price_url`, `price_checked` and `override`. 400 `invalid_price`. Audited as `ai.prices` with old and new values. |
| `DELETE /ai/prices/{model}` | s | Reset a model to the built in price. Audited as `ai.prices`. |
| `GET /providers/refresh/settings` | a | The provider refresh: `{settings, defaults, ai_enabled, model: {model, source, input_price, output_price, pricing_known}, next_run, last_run, running, month_spend_usd, pending_reviews, can_edit}`. `settings` is `{schedule: off\|weekly\|biweekly\|monthly\|days, every_days, hour, scope: with_keys\|with_services\|all, include, exclude, include_peer, claude_prices, apply: auto\|review, max_run_usd, max_month_usd, max_providers, stale_days}`. See [Keeping provider offers fresh](flows.md#keeping-provider-offers-fresh). |
| `PUT /providers/refresh/settings` | s | Save those settings (400 `invalid` on a bad value). Audited as `provider.refresh.settings` with old and new values. |
| `POST /providers/refresh/run` | s | Start a run over the scope now. 202 `{run}`; 409 `refresh_busy` while a run goes, `refresh_budget` when the month's cap is spent, `nothing_due` when every provider was skipped; 503 `ai_disabled`. Audited as `provider.refresh.run`. |
| `POST /providers/{slug}/refresh` | s | Check now: re-check one provider whatever the scope and staleness. 202 `{run}`, same errors. Audited as `provider.refresh.check`. |
| `GET /providers/refresh/runs`, `GET /providers/refresh/runs/{id}` | a | Recent runs `{id, trigger: schedule\|manual\|provider, status: running\|done\|stopped\|failed, started_by, model, skipped: [{slug, reason}], stop_reason, providers, checked, changed, reviews, failed, pending, cost_usd}`; one run adds `items: [{slug, status: pending\|running\|unchanged\|applied\|review\|failed\|skipped, progress, detail, diff, version, review_id, cost_usd}]`. The slug `_claude_prices` is Anthropic's model prices. |
| `GET /providers/refresh/reviews?status=pending\|all` | a | Proposals waiting for review: `{reviews: [{id, slug, run_id, status, base_version, offers, diff: {added, removed, changed: [{name, fields: [{field, from, to}], source_url}]}, summary, reason}]}`. |
| `POST /providers/refresh/reviews/{id}/apply`, `.../dismiss` | s | Apply (publishes a version with source `refresh`, or saves Claude prices as overrides) or drop a proposal. 409 `stale` when the provider changed since, `not_pending` when already decided. Audited as `provider.refresh.apply` and `provider.refresh.dismiss`. |
| `GET /settings` | a | The configuration only the environment sets, for the Settings page: `{version, access_gate_on, env: [{name, set, secret, value}]}`. `value` is the effective value (the default when the variable is unset). Secrets such as `ACNOLOGIA_MASTER_KEY` and `ANTHROPIC_API_KEY` report only `set`. |
| `GET /ai/runs`, `GET /ai/runs/{id}?after=n` | m | Runs and their live steps. |
| `POST /ai/runs` | a | Start a run: `{kind, provider_slug, context, urls, test_key, auto_publish, create_service}`. |
| `POST /ai/runs/{id}/cancel`, `POST /ai/runs/{id}/publish` | a | Cancel; publish a run waiting for review. |
| `DELETE /ai/runs/{id}` | a | Delete a finished run and its steps. 409 `run_busy` while it is queued, running, testing or publishing. Audited. Spend already recorded stays in Usage. |
| `DELETE /ai/runs` | a | Clear finished runs: deletes every failed, cancelled and published run and returns `{deleted}`. Runs waiting for review are kept. |
| `POST /ai/assist` | a | "Fill with AI" for dashboard forms. `form: "guide"` with `current: {provider: <slug>, ...guide}` drafts a provider's usage guide fields. |
| `GET /chat/threads`, `POST /chat/threads` | m | Your assistant conversations (never other users'), and whether AI is enabled. |
| `GET /chat/threads/{id}`, `DELETE /chat/threads/{id}` | m | One conversation's transcript; delete it. |
| `POST /chat/threads/{id}/messages` | m | `{text}`, up to 1 MB. Keys in the text go to the thread's vault before anything reaches Claude. Runs the assistant until it answers; returns the new transcript items. 409 while a previous message is still running, 503 when AI is off. |
| `POST /chat/threads/{id}/confirm` | m | `{pending_id, approve}`. Runs or cancels an action waiting for confirmation, then lets the assistant reply. |
| `POST /chat/threads/{id}/answer` | m | `{ask_id, names, choice, auto}`. Answers an `ask` item (label pasted keys, pick a choice, or let the assistant pick names) and continues the turn. |
| `POST /chat/threads/{id}/compact` | m | Compact now: Claude summarizes the older messages and the summary replaces them in what the model is sent; the last 6 messages stay verbatim and the transcript keeps everything. Returns the new `compact` item. 400 `nothing_to_compact` when the chat is too short, 409 while a turn runs, 502 `compact_failed` when the summary call fails (nothing changes), 503 when AI is off. |
| `GET /identity` | a | This server's identity: `{id, name, url, url_source: env\|saved\|request, version, public_key, fingerprint}`. |
| `PATCH /identity` | s | `{name, url}`. `url` is ignored when `ACNOLOGIA_PUBLIC_URL` is set; `""` clears the saved one. |
| `GET /shares`, `GET /shares/{id}` | a | Shares this server gives, each with `mode`, `lease` (`1h`, `6h`, `24h`, `72h` or `7d`) and `lease_sec`, `ends_at` (the timer, the same value as `expires_at`), `end_action`, `private_network`, `key_scopes`, `service_keys {slug: {keys_shared, keys_total}}` (the keys each shared service's scope resolves to now, of the keys the share could use without a scope) and `usage {today_requests, today_cost, month_requests, month_cost, last_used_at, history (14 days), limit_hit}`; one share adds `per_service` request counts. A handover share adds `handover {keys: [{key_id, service, label, provider, live, handed_at, renewed_at, valid_until, wiped_at, wipe_reason, reported_requests, reported_status, reported_at}], last_revoke_push: {at, ok, detail} or null}` (`reported_*` is what the other server says, not measured here), and once revoked `rotate_checklist: [{key_id, service, label, provider, provider_name, provider_url, handed_at, wiped_at, key_exists}]`, every key the other server ever held. Every share also has `recipient` (`server` or `person`), `label`, `email`, `claimed_at`, `claim_expires_at` and `revealed_keys: [{key_id, service, label, provider, revealed_at}]` (empty unless a reveal share was claimed); a reveal share that ended (revoked or expired) after its claim has `rotate_checklist` in the same shape, `handed_at` being when the key was revealed. Never carries the invite, the claim code, the token, a key or a sealed key. |
| `POST /shares` | s | `{name, note, recipient: server\|person, label, email, mode: proxy\|handover\|reveal, lease, services, providers, key_scopes, limits, ends_at, end_action: expire\|revoke, include_hidden, private_network, invite_valid: 1h\|24h\|7d, claim_valid: 1h\|24h\|7d}`. For `recipient: person` see [Sharing with people](#sharing-with-people): it needs a `label`, takes `mode` proxy or reveal, and returns `{share, claim_url}` instead of an invite. `ends_at` is RFC 3339 or `""` for no end; `expires_at` is still accepted as its alias (`ends_at` wins when both are sent). `lease` (default `24h`) is how long a handed over key stays valid without renewal. A handover share refuses any request or budget limit with 400 (the other server calls the provider itself, so this server cannot count); send the limits as null or leave them out. `private_network` lets the revoke push reach a receiver on a private address (otherwise the push uses the public only client). Returns `{share, invite}`; the invite is shown once. Services from another server (provider `acnologia-peer`, or holding keys handed over to this server) are refused. Audited as `share.created` with the services, providers and key scope modes. |
| `PATCH /shares/{id}` | s | Same fields but `invite_valid`. `mode` changes only while the share is pending (400 after redeem: create a new share). Changes reach the receiver at its next sync; a new key scope applies to the very next request. `ends_at: ""` clears the timer; an expired share moved into the future turns active again. Audited as `share.updated`. |
| `POST /shares/{id}/extend` | s | `{ends_at}`: move the timer later (RFC 3339, in the future and after the current end) or `""` for no end. An expired share turns active again; a handover share's keys are handed over again at the receiver's next sync. 409 on a revoked share. Audited as `share.extended`. |
| `POST /shares/{id}/pause`, `/resume`, `/revoke` | s | Pause (reversible) or revoke (final). Both refuse the very next request. Revoking a handover share also sends the signed revoke push and records the result in `handover.last_revoke_push` (`wiped 1 key(s) at ...`, or `not reached (...); the keys are deleted by <time> when their lease ends`). Audited as `share.paused`, `share.resumed`, `share.revoked` and `share.revoke_push`. |
| `POST /shares/{id}/invite` | s | A new invite for a share not redeemed yet: `{invite_valid}`. The old invite stops working. 409 `person_share` for a share with a person. |
| `POST /shares/{id}/claim-link` | s | A new claim link for a share with a person not claimed yet: `{valid: 1h\|24h\|7d}` (default 24h). Returns `{share, claim_url, claim_expires_at}`, shown once; the old link stops working. 409 `already_claimed` once claimed or ended, 409 `not_person_share` for a share with a server. Audited as `share.claim_link`. |
| `GET /peers` | a | Shares other servers gave this one: status, `mode`, `local_services`, imported `providers`, `last_sync_at`, `last_error`, the owner's `fingerprint`. A handover link adds `handover {keys: [{key_id, remote_key_id, service, label, valid_until, status}]}`: the keys held here and when each is deleted unless renewed. Never carries the token or a key. |
| `POST /peers/preview` | s | `{invite, trusted}`: fetch the owner's identity and what the invite shares, without redeeming it. |
| `POST /peers` | s | `{invite, prefix, trusted, owner_id}`: redeem the invite and create the local services. `owner_id` makes the call fail if the server at the URL is not the one previewed. |
| `POST /peers/{id}/sync` | s | Read the owner's manifest now. |
| `GET /peers/{id}/providers` | a | Provider specs imported through the link: `{providers: [{provider, name, kind: shared\|dependency, for_services, services (remote slug to local id), imported_version, remote_version, local_version, imported_at, edited_here, update_available, base_url, remote_base_url, missing}]}`. Versions named `imported` and `remote` are the owner's; `local_version` is this server's right after the import. |
| `POST /peers/{id}/providers/{slug}/pull` | s | Update an imported provider to the owner's current spec: `{allow_base_url_change}`. 409 `base_url_changes` (with `from` and `to`) when the new spec moves the base URL and `allow_base_url_change` is not set; 409 `edited_here` when the provider was edited on this server since the import. Audited as `provider.pulled`. Returns the same list as the GET. |
| `DELETE /peers/{id}` | s | Disconnect: delete any handed over keys, tell the owner, delete the token and the local services' key. On a link already disconnected or revoked, remove it from the list. |
| `GET /users`, `POST /users`, `PATCH /users/{id}`, `DELETE /users/{id}` | s | User management. A password reset ends that user's sessions; the last active superadmin cannot be removed. |
| `GET /audit` | s | Audit log (max 1000 rows). |

### Control MCP (`/mcp/control`)

`POST /mcp/control` is a second MCP server, for operating Acnologia from an
MCP client (Claude Code, Claude Desktop, claude.ai, Cursor) rather than for
apps. It speaks the same JSON-RPC as `/mcp` (streamable HTTP, JSON
responses, protocol `2025-06-18`; `DELETE` answers 204, other methods 405).

- **Auth:** an admin API token only, `Authorization: Bearer acnadm_...`. The
  call acts as the token's owner with the lower of the owner's role and the
  token scope, like `/api/admin`. An app token, a dashboard session or no
  token gets 401 with `WWW-Authenticate: Bearer realm="acnologia"`; the
  message points app tokens to `/mcp`. Behind the access password the admin
  token opens it, as it does `/api/admin`.
- **How tools run:** each tool is an in process request to the matching
  `/api/admin` route with the same token and client address (and
  `X-Requested-With: acnologia-mcp`), so the route's role check, session
  only rule, share app rule, validation and audit all apply. Service inputs
  take an id, slug or name.
- **Listing:** tools the caller's role cannot use are not listed. Members
  see reads, admins add writes and `call`, superadmins add sharing and
  refresh writes and the `admin` group.
- **Confirm step:** destructive tools (deletes, revokes, rotate, disconnect,
  a raw `DELETE`) take `confirm`. Without `confirm: true` nothing changes and
  the result says what would happen (for example the service and its key
  count). `confirm=off` turns the step off for clients that ask the user
  themselves.
- **Results:** the route's JSON as text, plus `structuredContent` when it is
  an object. A route error is `isError: true` with its status and message.
  Results are capped at 512 KB.
- **Calling providers:** `call_service` `{service, method, path, query,
  body, headers}` runs the request through the proxy as an app named
  `Control MCP`, made on first use (mode proxy, all services). Key rotation,
  retries, the breaker, the cache, usage and that app's limits all apply,
  and the key never reaches the client. It needs the admin role. Turn the
  app off or limit it on the Apps page. `list_service_examples` gives a
  service's documented calls as ready `call_service` arguments.

Choices go in the endpoint URL's query or in headers (the header wins):

| Query | Header | Effect |
|---|---|---|
| `groups=services,keys` | `X-Acnologia-MCP-Groups` | Only these groups; `all` is every group. Default: every group except `admin`, `secrets` and `raw`. |
| `exclude=delete_service` | `X-Acnologia-MCP-Exclude` | Drop tools by name. |
| `read_only=1` | `X-Acnologia-MCP-Read-Only` | List only read tools and refuse the others. |
| `confirm=off` | `X-Acnologia-MCP-Confirm` | No confirm step. Default `on`. |

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
| `admin` (off by default) | `list_users`, `create_user`, `update_user`, `delete_user`, `list_audit` |
| `secrets` (off by default) | `reveal_key` |
| `raw` (off by default) | `api_request` `{method, path, query, body}` for any `/api/admin/...` route not covered above |

`api_request` refuses paths outside `/api/admin/`, `/api/admin/tokens`,
`PATCH /api/admin/me` and the Assistant's chat routes.

Resources: `acnologia://overview`, `acnologia://services`,
`acnologia://llms-full.txt` and `acnologia://docs/{slug}` for each page under
`/docs/{slug}.md` (also a resource template). Prompts: `onboard_provider`,
`health_report`, `set_up_app` and `share_with_person`.

### Sharing between servers

One Acnologia can let another use some of its services through its proxy,
or give it provider specs. In proxy mode (the default) keys never leave the
server that owns them; in handover mode they are sealed to the other server,
which can then keep them, so revoke only makes a cooperating server delete
them. The
flow is in [flows.md](flows.md#sharing-between-servers) and the security
model in [security.md](security.md#sharing-between-servers).

| Route | Auth | Purpose |
|---|---|---|
| `GET /.well-known/acnologia` | none | `{id, name, version, public_key (base64 Ed25519), url, box_public_key (base64 X25519, what handed over keys are sealed to)}`. The id is `acn_srv_` and the first 20 characters of the lowercase base32 SHA-256 of the key. |
| `POST /api/peer/v1/preview` | invite | `{invite, share_id}`: the share's name, services and providers. Does not use the invite up. |
| `POST /api/peer/v1/redeem` | invite and signature | `{invite, share_id, id, name, url, public_key, nonce, signature, box_public_key}`. The signature is Ed25519 over `acnologia-redeem-v1\n<share id>\n<sha256 hex of the invite secret>\n<nonce>\n<receiver id>`, or, when `box_public_key` is sent, over `acnologia-redeem-v2\n...\n<receiver id>\n<box public key>`. A handover share needs `box_public_key` (400 `box_key_required`). Returns `{token, owner, manifest}` once. 409 `invite_used`, 410 `invite_expired`, 404 `invite_invalid`, 401 `bad_signature`, 400 `bad_identity`, 400 `self_redeem` when a server redeems its own invite. |
| `GET /api/peer/v1/manifest` | share token | `{share_id, name, status, mode, lease_sec, end_action, limits, expires_at, services: [{slug, name, provider, category, description, logo_url, available}], providers: [{slug, name, version, shared, spec, ...}]}`. No key data. |
| `POST /api/peer/v1/leave` | share token | The receiver disconnected; the share is revoked and shows as left by the peer. |
| `GET /api/peer/v1/providers/{slug}/logo` | share token | The logo of a provider in this share's manifest (shared, or used by a shared service). 404 for any other provider or when it has none. |
| `GET /api/peer/v1/keys` | share token | Handover shares only (409 `not_handover` for a proxy share; 403 while paused, expired or revoked): `{share_id, lease_sec, services: [{slug, name, provider, base_url, auth_type, auth_name}], keys: [{id, service, label, tier, sealed_secret, valid_until}]}`. Each `sealed_secret` is `acnseal1_` + base64url(HPKE enc and ciphertext), sealed to the receiver's box key. Every call renews the lease (`valid_until = now + lease`). |
| `POST /api/peer/v1/report` | share token | `{keys: [{id, requests, status}]}`: the receiver says which handed over keys it holds, requests made with each and its pool status. The owner stores it as reported; a key missing from the list is recorded as deleted there. |
| `POST /api/peer/v1/revoke` | owner signature | `{share_id, owner_id, receiver_id, ts, nonce, signature}`, sent by the owner to the receiver. The signature is Ed25519 over `acnologia-revoke-v1\n<share id>\n<receiver id>\n<ts>\n<nonce>`, checked against the owner key pinned on the link. Deletes every key handed over through that link and marks it revoked: `{ok, wiped}`. 401 `bad_signature`, 400 `stale` (more than 5 minutes off), 409 `replay`, 404 `unknown_share`, 429 `rate_limited`. |

**Key scopes.** `key_scopes` maps a shared service's slug to which of its keys
the share may use. A service without an entry uses every key, as before.

| Mode | Shape | Meaning |
|---|---|---|
| `all` | `{"mode": "all"}` | Every key the share could use. Sending it removes the scope. |
| `count` | `{"mode": "count", "count": 3}` | At most N keys (N 1 or more): the first N still in rotation, by priority then id. Invalid, banned and disabled keys are out of rotation and the next key takes their place; a key cooling down or out of quota still counts, so the share does not spread onto every key. |
| `pick` | `{"mode": "pick", "key_ids": [4, 9]}` | Only the listed keys. Each must be a key of that service (400 otherwise), and a hidden key needs `include_hidden` (400 otherwise). A deleted key drops out. |

Restricted and hidden keys still apply in every mode. Sending `key_scopes`
replaces all of them; a scope for a service the share does not share is a
400, and a service removed from the share loses its scope. The pool enforces
the scope on every pick, so a scope that leaves no usable key answers 503
`no_key_available`, and the manifest's `available` counts only keys in the
scope.

Preview and redeem share a global limit of 20 calls a minute and 500 a day
(429 `rate_limited`). A share token route called with an ordinary app token gets 403
`not_a_share`; with the token of a revoked, paused or expired share, 403
`share_revoked`, `share_paused` or `share_expired`. All these routes, and the well-known identity, are open
through the access password lock. Revoke pushes share their own limit of
20 a minute and 500 a day.

### Sharing with people

A share whose `recipient` is `person` is for someone who does not run
Acnologia. Creating one returns `claim_url`, `<public url>/claim/acnclaim_...`,
shown once; only its hash is stored. The person opens it, the page previews
the share and, on "Get my access", redeems the code, which works once. The
flow is in [flows.md](flows.md#sharing-with-people) and the threat model in
[security.md](security.md#sharing-with-people).

Rules on create and edit (400 `invalid` otherwise): `label` is 1 to 80
characters; `email` is optional and only a label (loosely checked, never
mailed); `mode` is `proxy` (default) or `reveal` (`handover` is for servers
only, `reveal` for people only); `recipient` cannot change. A reveal share
takes no request or budget limits, and its key scopes must resolve to at
least one key it can show (400 `no_revealable_keys`): hidden keys are never
revealed, even with `include_hidden`, and nor are restricted, disabled,
invalid, banned or handed over keys. Timers, pause, resume, extend, revoke,
audit and webhooks work as for a server share.

| Route | Auth | Purpose |
|---|---|---|
| `POST /api/claim/v1/preview` | claim code | `{code}` (the code, or the whole link). `{owner: {name, id, url}, share: {name, label, mode, ends_at, limits, services: [{slug, name, provider, category, description, logo_url}], providers: [{slug, name}], key_count}, claim_expires_at}`. `key_count` is how many keys a reveal share would show (0 for proxy); `logo_url` is always null (logos need a dashboard session). No secret; does not use the link up. |
| `POST /api/claim/v1/redeem` | claim code | `{code}`. Burns the link and answers once. Proxy: `{token, base_url (the origin plus /proxy), mcp_url, limits, ends_at, services: [{slug, name, provider, category, proxy_url, auth: {type, name, prefix}, examples: [{name, method, path, query, body}]}]}`; the token is an app token on the share's backing app, proxy only, for the shared services. Reveal: `{keys: [{service, label, secret, provider, base_url, auth: {type, name, prefix}}], ends_at}`; each key is recorded as revealed. 409 `nothing_to_reveal` (the link is not used up) when the scope has no key to show right now. |

Both answer 404 `claim_invalid` for an unknown code, 409 `already_claimed`
once redeemed, 410 `claim_expired` when the link ran out or the share ended
(revoked, or its timer passed) before the claim, and 429 `rate_limited` past
a global 20 calls a minute or 500 a day (their own bucket, sized like the
invite routes). They send `Cache-Control: no-store`. Both, the `/claim/<code>`
and `/me` pages, and the person's own calls with the token (`/proxy`,
`/api/v1`, `/mcp`) work through the access password lock; nothing else does.

A person's token: works on `/proxy/{service}`, `/api/v1/services`,
`/api/v1/me` and `/mcp`, only for the shared services and within the key
scopes, limits and timer; cannot lease (403 `lease_not_allowed`), sees only
the core and service MCP tools (never `get_key`, insights or manage tools,
whatever the app's MCP settings say), gets 401 on every dashboard route and
403 `not_a_share` on the server to server routes. Pause, revoke and an ended
timer refuse the very next call. Audit and webhook events: `share.created`,
`share.claim_link`, `share.claimed`, `share.revealed` (one per key, key id
and service only), `share.paused`, `share.resumed`, `share.revoked`,
`share.expired`, and the audit entry `share.rotate_keys` when a claimed
reveal share ends.

### Logos (`/api/logos`)

| Route | Role | Purpose |
|---|:-:|---|
| `GET /providers/{slug}`, `GET /services/{id}` | m | Logo bytes, served with `nosniff` and a sandbox CSP. A service falls back to its provider's logo (for a shared service, the owner's provider's logo when this server has it). |

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
