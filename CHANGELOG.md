# Changelog

## Unreleased

### Errors and discovery for agents

- Every API error keeps `code` and `message` and adds `hint`, `fix`
  (a tool, command or URL), `doc_url`, `request_id` and, when a retry
  helps, `retry_after_sec`. Every code is listed in `docs/errors.md`,
  generated from the same registry so the two can't drift. MCP tool errors
  carry the same fields.
- `GET /api/v1` and `/.well-known/acnologia` are an index for agents:
  version, setup state, every endpoint URL and where to start.
  `GET /api/openapi.json` is an OpenAPI 3.1 spec of every route.
- On a server behind the access password, the docs and `llms.txt` open to
  any valid app, OAuth or admin token. The dashboard stays locked.
- `/api/v1/me` and the new `acnologia_whoami` tool describe the token, its
  limits and what is left. Service logos only come with `?logos=1`.

### Control MCP and acn

- The control MCP has a tool for every dashboard feature: capabilities,
  rotation, shared keys, AI rules and prices, share analytics, logos, the
  Assistant, access controls, jobs, demo data and Try it. Results are capped
  at 64 KB, list tools take `summary: true`, and errors keep their code.
- `acn api`, `acn tool <name>` (every control tool), `acn apps`,
  `acn tokens`, `acn limits`, `acn deny`, `acn tags`, `acn ceiling`,
  `acn events --follow`, `acn audit`, `acn jobs`, `acn demo`, `acn try`,
  `acn trash`, `acn backup`, `acn oauth`, `acn run -- <cmd>` (one command
  through the forward proxy) and `acn scan` (find keys in env and agent
  config files, preview, then import). With `--json` an error prints as
  JSON and the exit code says what happened: 2 usage, 3 auth, 4 not found,
  5 retry later.
- `ACNOLOGIA_BOOTSTRAP_TOKEN` gives a fresh server an admin API token on
  first boot, so CI and agents can set it up without a browser.
  `GET /api/admin/checklist` and the `quickstart` tool set up a service,
  keys, an app and a token in one step.

### Observability

- Audit rows record the token, the channel (dashboard, api, cli,
  control_mcp, app_mcp, assistant, oauth) and the client (the MCP client
  name, or the User-Agent).
- Events and the audit log take `after_id`, `since`, `app_id` and other
  filters and return `next_cursor`. `GET /api/admin/events/stream` and
  `GET /api/v1/events/stream` send events as they happen.
- New notifications: `key.cooling`, `app.limit_near` and `share.expiring`.
- Check-all, service checks, peer sync and MCP sync take `?async=1` and
  return a job to poll at `GET /api/admin/jobs/{id}`.

### Keys and calls

- Idempotency answers are stored encrypted in the database for 24 hours and
  survive a restart. An answer over 4 MB or a stream can't be replayed
  (422 `idempotency_unreplayable`).
- Key affinity: a job or file created on one key is used with that key.
  Specs declare `affinity` rules; OpenAI files, batches and fine tuning
  jobs have them built in. `X-Acnologia-Key-Affinity` pins a run of
  requests to one key.
- A busy service shares freed slots fairly between apps, and a request
  waits for a key whose cooldown ends before the queue timeout.
- Measured evidence: success rate and latency per endpoint over 7 days,
  shown in search results and guides, and a `reliable` routing strategy.
- The built-in LLM offers carry per-model token prices, so budgets count
  their token spend. A verified example keeps a short sample of its answer
  for agents to read.

### Limits and access

- Limits per token, and a per-token default for each app.
- Caller tags: `X-Acnologia-Meta: customer=...` records use per tag value,
  tag budgets are enforced before the call, and a token can be pinned to
  a tag value.
- Deny rules per app by method, service or host, and path prefix, on the
  proxy, the forward proxy, capabilities, rotation and upstream MCP tools.
- `ACNOLOGIA_DAILY_SPEND_CEILING_USD` stops every app once the server's
  estimated spend for the day reaches it.

### Provider sign in

- Services can use OAuth accounts instead of API keys: Google (Search
  Console, GA4, YouTube, Sheets), Google Ads and Microsoft Graph are built
  in, and custom specs can use `auth: oauth2`. Each connected account is a
  pooled key whose access token refreshes itself; a lease hands out only
  the access token.

### Safer admin changes

- Admin writes take `Idempotency-Key` and `?dry_run=1`.
- Deleting a key, service or app moves it to the trash for 7 days, from
  where it can be restored (Settings, `acn trash`, control MCP).
- `GET /api/admin/version` and `POST /api/admin/backup`.

### Getting started

- Load demo data from the Getting started card for a first call with no
  real key, and try any service from its Integrate tab.
- Every SDK's forward proxy env points `SSL_CERT_FILE` at a bundle with the
  system roots and sets `NO_PROXY`.

### Development

- CI adds golangci-lint, a gofmt check, web unit tests and a secret scan;
  dependabot opens weekly updates against `stage`. `make check` runs the
  same checks locally.

### Sign in to MCP with OAuth

- `/mcp` is an OAuth 2.1 authorization server for MCP clients: claude.ai and
  ChatGPT connectors, Claude Code and Cursor can sign in instead of taking a
  pasted token. PKCE S256 is required, clients register themselves or use a
  client ID metadata document, and the server needs a public HTTPS URL.
- Approving shows a consent page where you pick the app the connection acts
  as, or make a new one, and can make it read only. Access tokens last an
  hour and refresh tokens rotate on every use; a spent refresh token coming
  back revokes the whole connection. Connections are listed under the app's
  tokens and can be revoked there.

### Agents

- `acn mcp install` adds Acnologia to Claude Code, Cursor and Codex in one
  step, through the `acn mcp` bridge so no token is written into their files.
  It backs up each file first, and `--dry-run` shows the change.
- The repository is a Claude Code plugin marketplace: `/plugin marketplace
  add sidharthpunathil/acnologia`, then `/plugin install acnologia@acnologia`
  for the MCP server and a skill that teaches agents how to use it.
- A Getting started card on the catalog walks a new admin through adding a
  key, making an app token, connecting an agent and making a first call.
- MCP tools refuse argument names they don't know and list the accepted
  ones. An unknown tool name gets up to three close names.
- Proxied results carry `served_by`, `attempts`, `tried`, `cache` and
  `retry_after_sec`, and a cut body says `truncated` with `bytes_total`.
  `idempotency_key` is an argument on proxy and service tools.
- `search_tools` results say whether each tool's keys are free, resting or
  down, and rank available tools first. A capability shows up next to the
  provider tools it rotates across, and capabilities whose routes are all
  GET are read only.
- Each tool synced from an upstream MCP server can be set to follow the
  server's read-only hint, always read, or always write, on the service's
  MCP tab.

### Safer calls and budgets

- A POST, PUT, PATCH or DELETE without an Idempotency-Key is no longer sent
  to another provider after a 5xx or a lost connection, since the first one
  may have done the work. Refusals (429, 401, 402, 403, no free key) still
  move on.
- Idempotency keys are scoped to the token, reject a different body with
  422, answer a retry in flight with 409, never replay a 408, 425, 429 or
  5xx, replay before limits are checked, and send the retry to the same key.
- Budgets count token spend for offers that carry a token price
  (`model`, `price.amount`, `price.output_amount`). The `cheapest` strategy
  puts routes with no known price last.
- Limits refuse calls (503 `limits_unavailable`) when they can't be read,
  and a call that would pass a budget with its own estimate is refused.
- A key whose own egress proxy fails rests for 30 seconds and the next key is
  tried, without counting against the service. Buffered answers get the
  same idle timeout as streamed ones, and event streams are never buffered.
  Breaker failures less than a second apart count once.

### Access and security

- Read-only tokens can no longer renew or report a lease, which could hand
  them a raw key, and can't use the forward proxy's passthrough.
- A key reveal is refused (503 `audit_unavailable`) when its audit row
  can't be stored.
- `acn proxy env` writes a CA bundle with the system roots, so tools that
  read `SSL_CERT_FILE` still trust other hosts, and sets `NO_PROXY`.
  `acn proxy ca --bundle` writes the same file.
- With `ACNOLOGIA_TRUST_PROXY_HEADERS=true` the client IP comes from
  `X-Forwarded-For` counted from the right (`ACNOLOGIA_TRUSTED_PROXY_HOPS`,
  default 1), or from one header named in `ACNOLOGIA_CLIENT_IP_HEADER`.
  `True-Client-IP` and `X-Real-IP` are no longer trusted.

### Provider rotation

- Requests can move between providers as they move between keys. A service
  with rotation on shares its requests with the other services, also with
  rotation on, that serve the same endpoint: services of the same provider,
  or of providers whose catalog lists an entry of the same name (Omkar
  Cloud's `amazon-scraper` and a self-hosted server's). Each service gets
  the path in its own form.
- Two modes: `fallback` tries the service the request names first, and
  `round_robin` starts each request at the next service.
- Settings on the service page has a Provider rotation card that lists the
  matching services and turns rotation on for all of them in one click.
  New routes: `GET /api/admin/services/{id}/rotation` and
  `POST /api/admin/services/rotation`; `PATCH /services/{id}` takes
  `rotation`.

### Catalogs on self-hosted servers

- A catalog entry's `url` can be a path on the service's base URL, such as
  `/api/scrapers/amazon-scraper/call`, so a server that runs many scrapers
  under one host gets the same `/~name/...` calls as Omkar Cloud. Path
  entries send keys nowhere new and need no named domain.
- A spec with a catalog may document up to 1500 guide endpoints.

### Assistant

- Every service page has an Ask AI button that opens the Assistant in a
  drawer, with the service as the chat's context.
- `call_service` calls a service's endpoint through the proxy and pages a
  long JSON answer with `offset`, `limit`, `select` and `where`, so the
  Assistant can read what a self-hosted server serves and write its catalog
  from it.
- `update_catalog` takes an `offer` per entry and asks for Confirm only
  when an entry gets a host of its own.
- `service_rotation` and `set_rotation` read and set provider rotation;
  `update_service` takes `rotation`.
- A turn may take 40 steps and each reply up to 16,000 tokens, enough to
  write a long catalog in batches.

### Action needed

- Clients that saved per tool permission rules for the app MCP (`POST /mcp`)
  must update them to the new names. A Claude Code allow rule for `get_key`,
  for example, now needs `acnologia_get_key`. Until the rule is updated, the
  client asks again before each call.

### App MCP

- Built in tools are listed as `acnologia_<name>`: `acnologia_get_key`,
  `acnologia_list_services`, `acnologia_search_tools` and so on, so they no
  longer clash with another server's tools. Generated service tools such as
  `serpapi_google_search` keep their names.
- The old names still work when called, and `disabled_tools` accepts either
  form.
- The dashboard's MCP panel on the Apps page shows the names agents see.
- Every built in tool and every service tool declares an `outputSchema` that
  matches its `structuredContent`.
- `initialize` answers with the client's protocol version when the server
  speaks it (`2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05`), else
  `2025-06-18`.
- Every built in tool has a `title` and states `readOnlyHint`,
  `destructiveHint` and `openWorldHint`.
- `acnologia_list_services` and `acnologia_list_keys` page their results:
  pass `limit` and `offset`, and read `has_more` and `next_offset` (pass it as
  `offset` for the next page).
- Errors name the tool to call next, such as `acnologia_list_services` for an
  unknown service.
- The agent instructions say that apps built by agents call the provider's
  own URL with Acnologia as their `HTTPS_PROXY`, and keep Acnologia out of
  their UI, copy, page titles and user facing errors.

### Control MCP

- Tools are listed as `acnologia_<name>` (`acnologia_list_keys`,
  `acnologia_delete_service`). The old names still work when called, and
  `exclude=` and `batch` accept either form. Instructions, descriptions,
  prompts and errors use the new names, and per tool permission rules saved
  in a client need the new names too.
- The list tools (`list_keys`, `list_services`, `list_events`, `list_apps`,
  `list_providers`, `search` and the other lists) return one page, 100 items
  by default, with `total`, `count`, `offset`, `has_more` and `next_offset`.
  Pass `limit` and `offset` for more. `list_events` and `list_audit` page
  through the newest 500 and 1,000 entries, and their `limit` is now the page
  size.
- `whoami`, `search` and most list tools declare an `outputSchema` that
  matches their `structuredContent`, and every tool states all four hints.
