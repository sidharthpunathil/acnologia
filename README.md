<p align="center"><img src="docs/brand/logo.png" alt="Acnologia" width="140"></p>

# Acnologia

**I think, therefore I am.**

A self-hosted pool for the API keys your apps burn through. Put every key you
have for SerpAPI, ElevenLabs, Brave Search, Tavily or any key based HTTP API in
one place. Your apps call Acnologia instead of the provider. Acnologia sends
each request with a key that has quota left, and when the provider answers
rate limited, out of quota or suspended, it parks that key and retries with the
next one.

One Go binary with the dashboard built in. SQLite storage, keys encrypted at
rest with AES-256-GCM.

![Catalog](docs/screenshots/catalog.png)

## Quick start

```sh
git clone https://github.com/sidharthpunathil/acnologia && cd acnologia

# Docker
cp .env.example .env
docker compose up -d --build

# or from source (Go 1.26, Node 22, pnpm 10)
make build
COGITO_DATA_DIR=./data ./bin/cogito
```

Open http://localhost:8080. The first visit creates the superadmin. Then:

1. **Add a service.** Pick a provider from the catalog, or describe any HTTP API.
2. **Add keys.** Paste one per line, or `label,secret` per line.
3. **Create an app** for each consumer (your website, a worker, an agent) and
   copy its `cog_...` token. It is shown once.
4. **Point your app at** `http://localhost:8080/proxy/<service>/...` with that token.

| Service keys | Apps |
|---|---|
| ![Service](docs/screenshots/service.png) | ![Apps](docs/screenshots/apps.png) |

The service page shows each key's live state: available, cooling with a
countdown, exhausted until reset, invalid or banned. Badges mark hidden, backup
and restricted keys, and a bar shows quota left.

## Calling it

**Proxy mode (recommended).** Send the request to Acnologia exactly as you would
send it to the provider, minus the provider's key. Acnologia attaches a key,
retries with the next one on a rate limit, and your app never holds a secret.

```sh
curl "http://localhost:8080/proxy/serpapi/search.json?q=coffee" \
  -H "Authorization: Bearer cog_your_app_token"

curl -X POST "http://localhost:8080/proxy/elevenlabs/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM" \
  -H "Authorization: Bearer cog_your_app_token" \
  -H "Content-Type: application/json" \
  -d '{"text": "Hello", "model_id": "eleven_multilingual_v2"}' -o hello.mp3
```

Responses carry `X-Cogito-Key-Id` and `X-Cogito-Attempts`, so you can see which
key answered and how many it took.

**Lease mode.** Use it when a provider SDK insists on holding the raw key. Borrow
a key, call the provider yourself, then report what happened. With
`rotate: true` the report hands you the next key in the same response.

```sh
curl -X POST localhost:8080/api/v1/lease -H "Authorization: Bearer cog_..." \
  -H "Content-Type: application/json" -d '{"service":"serpapi"}'
# {"lease_id":"lse_...","key_id":4,"secret":"...","auth":{"type":"query","name":"api_key"},...}

curl -X POST localhost:8080/api/v1/leases/lse_.../report -H "Authorization: Bearer cog_..." \
  -H "Content-Type: application/json" -d '{"outcome":"rate_limited","retry_after_sec":60,"rotate":true}'
# {"ok":true,"next":{...a different key...}}
```

## When a key fails

Each provider's rules sort its error responses into an outcome, and each
outcome has its own recovery. A key that hit SerpAPI's hourly cap is back at the
top of the next hour. A provider outage is never blamed on your keys.

| Outcome | Typical cause | What Acnologia does |
|---|---|---|
| `rate_limited` | 429, an hourly cap, `Retry-After` | Rests the key for as long as the provider asked, or doubles its cooldown per failure up to an hour. The next key takes the request. |
| `busy` | the account is at its concurrency cap | Steps the key aside for about a second, without backoff. |
| `quota_exhausted` | out of searches, characters or credits | Parks the key until its quota resets, then brings it back on its own. |
| `invalid` | the provider rejects the key | Takes it out of rotation until a health check passes. |
| `banned` | the provider suspended the account | Takes it out until you reset it. Health checks skip it. |
| `error` | 5xx, timeouts, refused connections | Leaves the key alone. Ten of these in a minute open the service's circuit breaker: requests get a 503 at once, and one probe goes through every 30 seconds until the provider recovers. |

## Features

**Keep requests flowing**
- **Rotation strategies** per service: round robin, fill first by priority,
  least used today, most quota left, or weighted random.
- **Live quota.** Health checks read remaining quota from account endpoints
  (SerpAPI `account.json`, ElevenLabs `/v1/user/subscription`) without spending
  any. Providers that send quota headers, such as Brave Search, update it on
  every response.
- **Concurrency limits** per key. ElevenLabs allows 2 parallel requests on its
  free plan, so extra requests wait in a queue for a free slot.
- **Backup keys** serve only when every primary key is cooling, busy or out of quota.
- **Service groups** put several services behind one slug, for example two
  SerpAPI accounts owned by different teams. A request the first member cannot
  serve moves to the next.

**Spend less quota**
- **Response cache.** Identical GET requests to search APIs come back from
  memory (`X-Cogito-Cache: hit`) and cost nothing upstream.
- **Idempotency keys.** Send `Idempotency-Key` and a retried POST within 10
  minutes replays the first answer instead of paying twice.
- **App limits and spend.** Each provider has a price per request. Give an app
  a daily or monthly request cap or USD budget and it gets a 429 when it reaches it.

**Control who uses what**
- **Apps** each get their own token (stored as a hash), a list of services and
  a mode: `proxy`, `lease` or both. An app learns whether a key is available,
  never how many exist.
- **Key flags.** `hidden` keys are visible only to superadmins and go only to
  apps a superadmin cleared for them. `restricted` keys serve only the apps you
  name. `disabled` keys stay stored but are never handed out. Each key can also
  record the account it belongs to (name, email, phone).
- **Roles.** Members read. Admins manage services, keys and apps; since they
  can create lease apps, treat non-hidden keys as readable by admins. Superadmins
  also see hidden keys, reveal secrets, manage users and read the audit log.
  See [security](docs/security.md).
- **Egress proxies.** Route a service or a single key through an HTTP, HTTPS or
  SOCKS5 proxy.

**Know what happened**
- **Webhooks** to Slack, Discord, Telegram or any URL (signed JSON) when a key
  goes bad, quota drops under 10%, a pool runs dry, a provider goes down or an
  app reaches its limit.
- **Activity** for every proxied request, lease and health check, with its key,
  app, status and latency. Each key shows whether it works, when it last worked,
  and 14 days of requests and failures.
- **Audit log** of every dashboard change and every secret reveal.

## Providers are data, and AI builds them

A provider is a JSON spec stored in SQLite: where the key goes, which error
responses mean rate limited, busy, out of quota, bad key or banned, how to read
remaining quota, and fixture tests that prove the rules. One engine interprets
every spec, and publishing one swaps it in memory, so **adding or fixing a
provider never restarts Acnologia**. Every provider keeps its version history
with one-click rollback, and you can edit it as a form or as raw JSON.

Built in: SerpAPI, Serper, Brave Search, Tavily, Exa, Firecrawl, Jina,
SearchApi, Linkup, You.com, ElevenLabs, and a generic spec for any API that
takes a key in a header, a bearer token or a query parameter.

**Build one with AI.** Give the builder a name and a docs URL or GitHub repo,
plus a test key if you have one. An agent on the Claude API then:

1. reads the official docs (web search, web fetch, GitHub),
2. writes the whole provider: metadata, auth, defaults, rules, health check,
   examples and tests,
3. validates it and runs the tests through the real engine, plus a live check
   with your test key,
4. finds the logo on the provider's website,
5. publishes it and adds a service to the catalog.

You watch every step live. Without auto publish, the draft waits for your review.

The guardrails live in code, where a prompt cannot talk its way past them:
- Specs are data and cannot execute.
- The harness re-validates and re-tests every submission itself.
- A failing test blocks publishing.
- Logo fetches and live checks cannot reach private networks.
- Test keys are encrypted and wiped after the run.
- Runs are capped in turns and searches.

**AI rules** (Settings) is your own policy text that every AI action follows,
for example "always set max_concurrency for voice APIs". **Fill with AI** fills
the app, service and provider forms from one sentence. The **Assistant** page is
a chat where Claude does the work through tool calls, as you and with your
permissions: say "add these SerpAPI keys", paste them, and it finds or creates
the service. Deletes and token rotations wait for your Confirm.
See [the flow](docs/flows.md#assistant-chat).

Set `ANTHROPIC_API_KEY` in `.env` to turn the AI features on. Everything else
works without it.

## For agents and scripts

**MCP.** `POST /mcp` speaks the MCP streamable HTTP transport with the same app
token. Each app gets its own tool sets, chosen in the dashboard: core leasing
(`get_key`, `renew_key`, `report_key`, `proxy_request`), ready to call tools
generated from each provider's examples, read-only key insights, and key
management. Leases can be exclusive, so no other caller gets the key until you
report it.

```sh
# Claude Code, over HTTP
claude mcp add --transport http cogito http://localhost:8080/mcp \
  --header "Authorization: Bearer cog_your_app_token"

# Clients that only speak stdio, such as Claude Desktop
cogito mcp --url http://localhost:8080 --token cog_your_app_token
```

**llms.txt.** Agents can read `/llms.txt`, and the full reference at `/llms-full.txt`.

**SDKs** for [Python](sdk/python), [TypeScript and JavaScript](sdk/typescript),
[Go](sdk/go), [PHP](sdk/php) and [Ruby](sdk/ruby). Their lease helpers always
report, and they raise a distinct error when every key is busy, so you know to
retry shortly.

**`cogito ctl`.** Create an API token under Account (`cogadm_...`, scoped read,
write or admin, never above your own role) and script the server from CI or
your laptop. The server binary is also the client.

```sh
cogito ctl login https://keys.example.com --token cogadm_...
cogito ctl status
cogito ctl keys add serpapi < keys.txt
cogito ctl events --service serpapi --kind rate_limited
```

## Self-hosting

```sh
cp .env.example .env
docker compose build
docker run --rm cogito:latest gen-master-key   # paste into COGITO_MASTER_KEY in .env
docker compose up -d
open http://localhost:8080                      # the first visit creates the superadmin
```

Back up the `cogito-data` volume **and** your master key. Without the master key,
stored API keys cannot be decrypted. If `COGITO_MASTER_KEY` is unset, one is
generated into `/data/master.key` on first boot.

Put it behind TLS (Caddy, Traefik, Cloudflare Tunnel) and set
`COGITO_SECURE_COOKIES=true`. Behind a reverse proxy also set
`COGITO_TRUST_PROXY_HEADERS=true`, or every client shares one login rate
limit. All settings are in [.env.example](.env.example).

Acnologia keeps itself out of search results: every response carries
`X-Robots-Tag: noindex, nofollow`, `/robots.txt` disallows everything, and pages
cannot be framed.

Upgrading: existing apps can no longer lease or proxy hidden keys until a
superadmin turns on **Lease hidden keys** for them, because a lease or an
upstream that echoes the key reveals it.

## Development

```sh
make dev    # Go on :8080, Vite on :5173 with hot reload
make test   # Go tests with the race detector
make build  # bin/cogito with the UI embedded
```

| Path | What |
|---|---|
| `server/` | Go backend: `internal/api` (HTTP, proxy, MCP), `pool` (selection, slots, outcomes), `providers` (spec engine), `catalog` (provider publishing, logos), `agent` (AI builder), `store` (SQLite), `worker` (health checks, housekeeping) |
| `web/` | React, Vite, Zustand and Tailwind dashboard |
| `sdk/` | Client libraries |
| `docs/` | Architecture, flows, security, configuration, API, llms.txt |
| `reference/` | Third party code used as reference only |

## Documentation

- [Architecture](docs/architecture.md): components, data model, boot and background work
- [Request flows](docs/flows.md): proxy, leases, key states, health checks, sign in, MCP, AI builder
- [Security model](docs/security.md): roles, hidden keys, secrets, limits, operator checklist
- [Configuration](docs/configuration.md): every setting, backups, upgrading
- [HTTP API](docs/api.md): consumer error codes and the dashboard API
- [Provider specs](docs/providers.md) and [Development](docs/development.md)
