# 002: Features borrowed from OmniRoute

Source: github.com/cheaper-inference/OmniRoute v3.8.53 (MIT). We take the
ideas that fit a generic key pool and leave the LLM gateway parts behind.

Branch: `worktree-omniroute-features`. Other sessions own `web/` and a security
pass over `server/` right now, so new logic lives in new packages first and is
wired into `pool`, `api/proxy.go` and the engine after rebasing on their work.
Store changes go through `migrate()` / `addColumn`, never by editing existing
`CREATE TABLE` statements.

## Build order

Each step ships with Go tests and keeps `go test -race ./...` green.

1. **New outcomes: `busy` and `banned`.**
   - `busy`: the key is healthy but at the provider's concurrency cap. Rotate
     now, cool for the retry hint or 1s, never raise `consecutive_fails`.
   - `banned`: the account is suspended. New key status `banned`. It is
     terminal: health checks never revive it, only an operator reset does.
   - The ElevenLabs concurrency rule becomes `busy`, and its suspension
     strings become `banned`.
2. **Quota from response headers.** `quota.headers` in the spec names the
   limit, remaining and reset headers. Every response updates the key's quota,
   so no health check is needed. Comma lists such as Brave's `1, 1999` take a
   chosen index.
3. **Circuit breaker per service.** `error` outcomes and network failures
   count toward it. After N inside a window it opens, and the proxy fails fast
   with 503 and `Retry-After`. After the reset time it lets one probe through
   (half open). Settings live in spec `defaults`.
4. **Webhooks.** Event types: `key.invalid`, `key.banned`, `key.exhausted`,
   `key.recovered`, `quota.low`, `pool.empty`, `service.down`, `service.up`,
   `app.limit`. Formats: JSON (HMAC-SHA256 signed), Slack, Discord and
   Telegram. Delivery is async with retries. Bursts are deduplicated per
   subject. URLs pass the same SSRF guard as logo fetching.
5. **Response cache and idempotency.** `cache_ttl_sec` is set per service
   (default from the spec). Only 2xx GET responses up to 1MB are cached, in an
   in-memory LRU with a global byte cap. `Cache-Control: no-cache` bypasses
   it. `Idempotency-Key` replays any method's response per app for 10 minutes.
   Responses carry `X-Acnologia-Cache: hit|miss|bypass`, and hits are logged as
   events so the savings show up.
6. **Egress proxies.** A registry of HTTP, HTTPS and SOCKS5 proxies with the
   URL encrypted at rest. They are assigned per key, else per service, else
   none. They are used for proxied traffic, leases' health checks and key
   checks. A test endpoint reports the egress IP.
7. **Cost and app limits.** The spec gains `quota.cost_per_request` (USD) and
   services can override it. Each event records its cost. Apps get optional
   daily and monthly request caps and USD budgets, enforced on proxy and lease
   with 429 `app_limit_reached` and `Retry-After`. Apps also get a usage
   endpoint.
8. **Search providers in the catalog.** Built-in specs for Serper, Brave,
   Tavily, Exa, Firecrawl, Jina, SearchAPI.io, Linkup and You.com, with free
   tier notes (`quota.free_tier`). Health checks are written only where the
   endpoint is documented and free.
9. **Service groups.** An ordered list of services behind one slug. When one
   member has no usable key, is open-circuited, or runs out of rotations, the
   same request moves to the next member. Meant for services with the same
   request shape, such as two SerpAPI accounts in different orgs or two
   regions of one API.
10. **Admin API tokens and `acnologia ctl`.** `acnadm_` tokens with `read`,
    `write` or `admin` scope, never above the creator's role, stored hashed.
    The CLI runs `acnologia ctl login|status|services|keys add|keys check|events`
    against a remote instance.
11. **AI builder, llms.txt, README and SDK types** learn the new spec fields
    and outcomes.
12. **Dashboard.** Pages for webhooks, proxies, groups and tokens. App limit
    and usage fields, service cache and proxy settings, the banned status, and
    the busy and banned outcomes in the spec editor.
13. **Docs page.** A public `/docs` page listing everything Acnologia does,
    written with the copywriting skill.

## Left out on purpose

- **Unified `/v1/search` with request translation.** Translation is code, not
  data, and it breaks the "providers are data" rule. Groups cover same-shape
  fallback.
- **LLM-only features:** token compression, fusion and pipeline routing,
  prompt caching, OAuth subscription tricks.
- **Fingerprint stealth, MITM, A2A, memory, desktop app.**
