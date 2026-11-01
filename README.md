# Cogito

A self-hosted API key pool. Register many accounts with a provider (SerpAPI,
ElevenLabs, or any API key based HTTP service), put all their keys in Cogito,
and your apps get a working key on demand. When a key hits a rate limit or runs
out of quota, Cogito cools it down and hands out the next one.

One Go binary with an embedded React dashboard. SQLite storage, keys encrypted
at rest with AES-256-GCM.

![Catalog](docs/screenshots/catalog.png)

## Quick start

```sh
# Docker
cp .env.example .env
docker compose up -d --build

# or from source (Go 1.26, Node 22, pnpm 10)
make build
COGITO_DATA_DIR=./data ./bin/cogito
```

Open http://localhost:8080. The first visit creates the superadmin. Then:

1. **Add a service** (SerpAPI, ElevenLabs, or a custom API).
2. **Add keys**: paste one per line, or `label,secret` per line.
3. **Create an app** for each consumer (your website, an agent) and copy its
   `cog_...` token. It is shown once.
4. Point your app at `http://localhost:8080/proxy/<service>/...` with that token.

## Screenshots

| Service keys | Apps |
|---|---|
| ![Service](docs/screenshots/service.png) | ![Apps](docs/screenshots/apps.png) |

The service page shows each key's live state: available, cooling with a
countdown, exhausted until reset, or invalid. Badges mark hidden, backup and
restricted keys, and a bar shows quota left.

## Two ways to use it

**Proxy mode (recommended).** Call Cogito instead of the provider. Cogito picks a
key, injects it, retries with the next key on rate limits, and your app never
sees a secret.

```sh
curl "http://localhost:8080/proxy/serpapi/search.json?q=coffee" \
  -H "Authorization: Bearer cog_your_app_token"

curl -X POST "http://localhost:8080/proxy/elevenlabs/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM" \
  -H "Authorization: Bearer cog_your_app_token" \
  -H "Content-Type: application/json" \
  -d '{"text": "Hello", "model_id": "eleven_multilingual_v2"}' -o hello.mp3
```

**Lease mode.** Ask for a key, call the provider yourself, report the outcome.

```sh
curl -X POST localhost:8080/api/v1/lease -H "Authorization: Bearer cog_..." \
  -H "Content-Type: application/json" -d '{"service":"serpapi"}'
# {"lease_id":"lse_...","key_id":4,"secret":"...","auth":{"type":"query","name":"api_key"},...}

curl -X POST localhost:8080/api/v1/leases/lse_.../report -H "Authorization: Bearer cog_..." \
  -H "Content-Type: application/json" -d '{"outcome":"rate_limited","retry_after_sec":60,"rotate":true}'
# {"ok":true,"next":{...a different key...}}
```

## Features

- **Rotation strategies** per service: round robin, fill first (by priority),
  least used today, most quota left, weighted random.
- **Rate limit detection** per provider. SerpAPI: out of searches, hourly cap,
  invalid key. ElevenLabs: quota exceeded, concurrency limit, invalid key, and
  `system_busy` (provider side, so the key is not penalized). Generic: 429, 402,
  401/403 and `Retry-After`.
- **Cooldown with backoff**: `Retry-After` when given, otherwise the service
  cooldown doubled per consecutive failure, capped at one hour.
- **Quota tracking**: health checks read SerpAPI `account.json` and ElevenLabs
  `/v1/user/subscription` (neither spends quota). Exhausted keys come back
  automatically when the quota resets.
- **Key flags**:
  - `hidden`: only superadmins see the key or count it, and admins cannot
    infer it from events.
  - `backup` tier: used only when every primary key is unavailable.
  - `restricted`: only allow-listed apps can use it.
  - `disabled`: kept, but never handed out.
- **Roles**:
  - `member`: read only.
  - `admin`: manages services, keys and apps.
  - `superadmin`: everything above, plus hidden keys, revealing secrets,
    users and the audit log.
- **Apps**: each consumer gets its own token (shown once, stored as a hash),
  limited to `proxy`, `lease` or both, and to a list of services. Apps only ever
  learn whether a key is available, never how many exist.
- **MCP server** at `POST /mcp` (streamable HTTP) with tools `list_services`,
  `pool_status`, `get_key`, `report_key`, `proxy_request`.
- **llms.txt** at `/llms.txt`, full version in `docs/llms-full.txt`.
- **SDKs**:
  - Python: [`sdk/python`](sdk/python)
  - TypeScript and JavaScript: [`sdk/typescript`](sdk/typescript)
  - Go: [`sdk/go`](sdk/go)
  - PHP: [`sdk/php`](sdk/php)
  - Ruby: [`sdk/ruby`](sdk/ruby)
- **Audit log** of every dashboard change and every secret reveal.

## Self-hosting

```sh
cp .env.example .env
docker run --rm cogito:latest gen-master-key   # paste into COGITO_MASTER_KEY
docker compose up -d --build
open http://localhost:8080                      # first visit creates the superadmin
```

Back up the `cogito-data` volume **and** your master key. Without the master key,
stored API keys cannot be decrypted. If `COGITO_MASTER_KEY` is unset, one is
generated into `/data/master.key` on first boot.

Put it behind TLS (Caddy, Traefik, Cloudflare Tunnel) and set
`COGITO_SECURE_COOKIES=true`. All settings are in [.env.example](.env.example).

## MCP

Claude Code:

```sh
claude mcp add --transport http cogito http://localhost:8080/mcp \
  --header "Authorization: Bearer cog_your_app_token"
```

Any MCP client that supports streamable HTTP with a bearer header works the same way.

## Development

```sh
make dev    # Go on :8080, Vite on :5173 with hot reload
make test   # Go tests with the race detector
make build  # bin/cogito with the UI embedded
```

Layout:

| Path | What |
|---|---|
| `server/` | Go backend: `internal/api` (HTTP, proxy, MCP), `pool` (selection and outcomes), `providers` (SerpAPI, ElevenLabs, generic), `store` (SQLite), `worker` (health checks, resets) |
| `web/` | React, Vite, Zustand and Tailwind dashboard |
| `sdk/` | Client libraries |
| `docs/` | Design spec, llms.txt |
| `reference/` | OmniRoute source and treg.to screenshots used as references |

Adding a provider means implementing `Classify` and `Check` in
`server/internal/providers` and registering it.
