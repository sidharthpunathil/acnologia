# Provider specs

A **provider** describes how one API works: where the key goes, which
responses mean "this key is rate limited" or "out of quota", and how to check
a key without spending quota. Providers are data, stored in SQLite with
version history, so adding one needs no code change and no redeploy.

A **service** is a provider plus your settings and your keys. Several
services can share a provider, for example two pools of keys for the same API
kept apart for two teams.

This page covers how to add a provider, how to write its spec, and then the
reference: offers, usage guides, validation, the built in specs, providers
from another server, and testing.

## Adding a provider

There are three ways in:

1. **Dashboard.** Providers, New provider. Fill in the form or paste JSON.
   Save validates the spec and runs its tests, and a spec that fails is not
   published.
2. **AI builder.** Providers, Build with AI. Give it context, docs URLs and,
   optionally, a test key. Watch the run, then review the draft or let it
   publish. See [Building a provider with AI](#building-a-provider-with-ai)
   and [the run flow](flows.md#ai-provider-builder).
3. **API.** `POST /api/admin/providers` with the metadata and spec, or
   `POST /api/admin/providers/validate` to check a spec without saving it.

Then create a service from the catalog and add keys.

### Where providers come from

```mermaid
flowchart LR
    Built["built in specs<br/>(27, see below)"] -->|seeded on first boot| Store[("providers table<br/>+ provider_versions")]
    Editor["dashboard editor<br/>(admin)"] -->|validate + fixture tests| Store
    AI["AI provider builder<br/>(admin starts a run)"] -->|"harness re-validates<br/>and re-tests"| Store
    API["PUT /api/admin/providers/{slug}"] --> Store
    Store -->|"load into memory<br/>on boot and on publish"| Reg["provider registry"]
    Reg --> Proxy["proxy classify"]
    Reg --> Health["health checks"]
    Reg --> Svc["new service defaults"]
```

Every publish bumps `version` and stores a snapshot, so **Rollback** can
publish any earlier version again. Built in providers can be edited and
disabled but not deleted. Other providers can be deleted once no service uses
them.

## Building a provider with AI

Set `ANTHROPIC_API_KEY` to turn this on. Give the builder a name and a docs
URL or GitHub repo, plus a test key if you have one. An agent on the Claude
API then:

1. reads the official docs (web search, web fetch, GitHub),
2. writes the whole provider: metadata, auth, defaults, rules, health check,
   examples, tests and a usage guide,
3. validates it and runs the tests through the real engine, plus a live check
   with your test key,
4. finds the logo on the provider's website,
5. publishes it and adds a service to the catalog.

You watch every step live. Without auto publish, the draft waits for your
review. The guardrails live in code, outside the prompt:

- Specs are data and cannot execute.
- The harness re-validates and re-tests every submission itself, and a
  failing test blocks publishing.
- Logo fetches and live checks cannot reach private networks.
- The test key only goes to the domain of a URL you gave (or of the provider
  being updated). With a test key but no docs URL, the live check is skipped
  and the draft waits for your review, even with auto publish on.
- Test keys are encrypted, never shown to the model (see
  [security](security.md#what-the-model-can-and-cannot-see)), and wiped when
  the run ends.
- A run stops after 40 turns, and its web searches and fetches are capped.

Admins can delete a finished run, or remove every failed, cancelled and
published run at once with **Clear finished**. Runs still working are refused
with 409, Clear finished keeps runs waiting for review, and the spend they
recorded stays on the Usage page.

**AI rules** (Settings) is your own policy text that every AI action follows,
for example "always set max_concurrency for voice APIs".

## Spec at a glance

A shortened spec, modeled on the built in `serpapi` one:

```jsonc
{
  "base_url": "https://serpapi.com",
  "auth": { "type": "query", "name": "api_key", "prefix": "" },
  "defaults": { "strategy": "round_robin", "cooldown_sec": 3600, "max_attempts": 5,
                "max_concurrency": 0, "queue_timeout_sec": 0 },
  "quota": { "unit": "searches", "counts_per_request": true },
  "rules": [
    { "name": "Out of searches",
      "match": { "status": [429], "body_contains": ["run out of searches"] },
      "outcome": "quota_exhausted" }
  ],
  "use_standard_rules": true,
  "health": { "method": "GET", "path": "/account.json",
              "quota": { "limit": ["searches_per_month"], "remaining": ["total_searches_left"] } },
  "examples": [ { "name": "Search", "method": "GET", "path": "/search.json",
                  "query": { "q": "coffee" }, "safe": true } ],
  "tests": [ { "name": "out of searches", "status": 429,
               "body": "{\"error\":\"Your account has run out of searches.\"}",
               "expect": "quota_exhausted" } ]
}
```

| Part | What it controls |
|---|---|
| `base_url` | Default base URL for new services. Existing services keep their own. |
| `auth` | `query` (parameter `name`), `header` (header `name`) or `bearer` (`Authorization: Bearer`, or `name` with `prefix`). The proxy applies `prefix` when the service still uses the spec's placement, and leases return it in `auth.prefix`. |
| `defaults` | Strategy, cooldown, attempts and concurrency for new services. |
| `quota` | The unit shown in the dashboard, whether each successful request uses one unit, `headers` to read remaining quota from every response, and `cost_per_request` in USD for app budgets and the Usage page when no offer prices a request. |
| `offers` | Optional. Prices, free allowances and published limits per product or endpoint. See [Offers](#offers). |
| `rules` | Ordered matchers, first match wins. Fields in `match` are ANDed, values in a list are ORed, matching is case insensitive. `retry_after` reads a header, a fixed value, the next hour, or a JSON field (always capped at 24 hours). |
| `use_standard_rules` | Append 429 rate limited, 402 quota exhausted, 401/403 invalid. Anything else 5xx is `error`. |
| `health` | A free request that proves a key works and reads its quota. Without it, validity is learned from real traffic. |
| `examples` | Requests for the Integrate panel. Live checks only run `safe` GET or HEAD examples. |
| `tests` | Fixture responses with the expected outcome, run on every save. |
| `guide` | Optional. How to call the provider through Acnologia, for apps and AI agents. See [Usage guides](#usage-guides). |

When you write rules, start from what the provider's docs say each error
looks like, and add a `tests` entry for each rule so a later edit cannot
break it quietly. The full field reference is in the
[design spec](superpowers/specs/2026-11-01-providers-as-data-and-ai-builder.md#provider-spec).

## Offers

`offers` records what a provider charges and allows, per product or
endpoint, as its pricing page publishes it. It is optional, and specs without
it work as before.

```jsonc
"offers": [
  {
    "name": "TinySearch",
    "match": { "method": "GET", "path": "/$" },
    "free": true,
    "price": { "amount": 0, "currency": "USD", "unit": "request" },
    "limits": [ { "count": 30, "per": "minute", "unit": "requests" },
                { "count": 500, "per": "hour", "unit": "requests" } ],
    "notes": "Free up to 12,000 a day, then wallet funds.",
    "source_url": "https://www.tinyfish.ai/pricing",
    "checked_at": "2026-11-02"
  },
  { "name": "Agent runs", "match": { "method": "POST", "path": "/v1/automation/run" },
    "price": { "amount": 0.016, "unit": "step" }, "concurrency": 2 }
]
```

| Field | Meaning |
|---|---|
| `name` | The product name. Unique within the spec. |
| `match` | Which requests the offer covers. `method` (empty for any) and `path`, relative to the base URL: a prefix by default, `*` matches inside one segment, and a trailing `$` makes it exact. Or `examples`, a list of example names, which match that example's method and path exactly. Leave `match` out for a provider wide offer. |
| `price` | `amount` is USD (or `currency`) for one `unit`: request, search, query, url, page, step, minute, check, token, character, credit, image, second, session, run or gb. `$5 per 1,000` is `0.005`. `per_request` is how many units one request uses when that is fixed (1 credit). |
| `free` | Requests under this offer cost nothing. A free offer cannot have a price above 0. |
| `limits` | `{count, per, unit}` with `per` second, minute, hour, day or month (30 days). |
| `concurrency` | Requests one key may have in flight under this offer. |
| `notes`, `source_url`, `checked_at` | Plan differences and anything else worth knowing, the pricing page, and the date (YYYY-MM-DD) it was read. |

A request uses the first offer whose `match` selects it, plus every provider
wide offer. A lease never shows Acnologia the request, so it gets only the
provider wide offers.

How Acnologia uses offers:

- **Pacing.** Before calling upstream, the pool checks each key against the
  limits of every offer that applies. Windows are rolling and kept in memory
  per key. Each is a ring of ten sub buckets, so memory stays fixed however
  high the limit, and counting the whole oldest bucket keeps the count
  conservative: a key may wait up to a tenth of the window longer than
  strictly needed, never less. A key at a limit counts as busy, so the
  request tries another key, waits up to the service's `queue_timeout_sec`,
  or answers 503 `all_keys_busy` with `retry_after_sec` set to when a window
  frees. Each request or lease counts as one unit, so for limits counted in
  URLs or tokens, send one unit per request or treat the limit as
  approximate. Limits apply to every key, so list only limits that hold for
  every plan and put plan specific caps in `notes`.
- **Concurrency.** An endpoint offer's `concurrency` caps that endpoint's
  in flight requests per key. A provider wide offer's `concurrency` becomes
  the per key slot limit when neither the key nor the service sets one.
- **Cost.** A successful request costs the service's `cost_per_request`
  override if set, else the matching offer's price (free offers cost 0, a
  unit such as `request` or `url` counts once, `per_request` multiplies),
  else `quota.cost_per_request`. Prices in units that vary per request
  (tokens, minutes) without `per_request`, or in another currency, fall back
  to `quota.cost_per_request`. The Usage page and app budgets use this cost.
- **AI and people.** The AI builder reads the provider's pricing page and
  fills offers with `source_url` and `checked_at`. The Assistant's
  `provider_offers` tool answers questions such as which search provider is
  free. Catalog and provider cards show a Free badge, the service page lists
  "Offers and limits", Copy for LLM includes them, and MCP service tools
  name the free offer and its limits.

Built in offers come from each provider's pricing and rate limit pages,
checked 2026-11-02 (2026-11-03 for the LLM APIs). Where a page publishes
nothing for a value, the value is left out.

### Keeping offers current

Prices and free tiers change, so a superadmin can have Acnologia re-read
them (Settings, Provider refresh; needs `ANTHROPIC_API_KEY`). By default it
runs monthly, on the 1st at 03:00 UTC, over providers behind a service with a
usable key: at most 10 a run, skipping any checked in the last 25 days,
capped at $2 a run and $5 a month. Check now on a service's Offers and limits
card, or a provider's Offers tab, checks one provider at once.

A refresh only ever changes `offers`. A change publishes a new provider
version on its own only when it is small and sourced: existing offers whose
price moves within 4x either way or whose limits loosen, each citing a page
on the provider's own website, docs or API domain. Everything else waits for
review in Settings: adding or removing an offer, changing what it matches,
its notes or whether it is free, and any tighter limit or concurrency. So
does every change when apply is set to review. A change to a built in
provider makes it an edited version, so later releases stop backfilling its
offers; roll back from the Versions tab. When nothing changed, `checked_at`
just moves to today. The full flow is in
[Keeping provider offers fresh](flows.md#keeping-provider-offers-fresh).

## Usage guides

A spec may carry a `guide`: how an app or an AI agent calls the provider
through Acnologia. Acnologia renders it, together with what it already knows
about the provider, into one Markdown page per service.

```jsonc
"guide": {
  "summary": "Daily weather forecasts by city as JSON. Pick it for forecasts up to 7 days ahead.",
  "endpoints": [
    { "name": "Forecast", "method": "GET", "path": "/v1/forecast",
      "description": "The forecast for one city.",
      "params": [
        { "name": "city", "in": "query", "type": "string", "required": true, "description": "City name, such as Lisbon." },
        { "name": "days", "in": "query", "type": "integer", "required": false, "description": "Days ahead, 1 to 7." }
      ],
      "response": "`days[]` with `date`, `high`, `low` and `summary`." }
  ],
  "notes": ["Temperatures are in Celsius unless `units=imperial`.", "Cached forecasts are free and do not use quota."],
  "markdown": "Optional free form Markdown, appended after the endpoints."
}
```

| Field | What to write |
|---|---|
| `summary` | Required. What the API is for and when to pick it, in 1 to 3 sentences (up to 1,000 characters). |
| `endpoints` | The calls worth knowing, most useful first (up to 40). `path` is relative to `base_url`, like in `examples`. `params[].in` is `query`, `path`, `body` or `header`. `body` is an example JSON body; `response` names the fields that matter. |
| `notes` | Short facts an agent gets wrong without them: pagination, model names, streaming, error shapes, size limits, cost traps (up to 30). |
| `markdown` | Anything else, up to 20,000 characters. |

Never describe auth or keys in a guide. Acnologia adds the key, and callers
only send their app token. Write for a model that has never seen the API:
real parameter names, a realistic example body, and the response fields it
will need next.

**What the rendered guide contains:**

- a title and the summary;
- "How to call it through Acnologia": the proxy URL
  `<public url>/proxy/<service>`, the app token as
  `Authorization: Bearer acn_...`, a note not to send a provider key, and the
  MCP alternatives `proxy_request` on `/mcp` or `call_service` on
  `/mcp/control`;
- the endpoints, with a parameter table, example body and response notes;
- the spec's `examples` as curl commands through the proxy;
- limits and pricing from `quota` and `offers`;
- how Acnologia handles this provider's errors, built from its `rules`: rate
  limits, quota and rejected keys rotate to the next key, and what the caller
  sees when no key can serve;
- the notes and free Markdown, and links to the provider's docs and website.

**Without a guide.** A provider with no `guide` still gets a page, assembled
from its description, examples, offers and rules, and marked as generated
(`generated: true`). A written guide is better, because it says which
parameters matter and what comes back.

**How a provider gets its guide.** Every way a provider is added or changed
can write one:

- **AI builder.** A guide is required output. A submit with no guide, or one
  with no endpoints, goes back to the agent like any other spec problem. On
  the last allowed submit, a guide gap alone does not fail an otherwise
  working provider: the run accepts it and notes "Accepted without a full
  usage guide". Publishing a run saves the guide with the spec.
- **Editor.** The Guide tab holds the guide fields as JSON. **Start a guide**
  gives a template, and **Fill with AI** (`POST /api/admin/ai/assist` with
  `form: "guide"`) drafts the fields from the provider's base URL, docs URL
  and examples into the draft for you to review and save. A provider saved
  without a guide shows a quiet hint on its Overview tab.
- **Write guide with AI.** On a saved provider, admins can press **Write
  guide with AI** (or **Rewrite with AI**) on the Guide tab, which calls
  `POST /api/admin/providers/{slug}/guide/generate`. The model reads the docs
  URL (web search and fetch), the spec's examples, offers and current guide,
  and the server saves only the `guide` part of the spec as a new version
  labelled `ai`. It uses the builder's model, follows the AI rules, and its
  spend shows on the Usage page. When the model cannot read the docs, nothing
  changes. When AI is not set up, the button is disabled and says why.
- **Assistant.** `provider_guide` (members) reads a provider's guide, and
  `write_provider_guide` (admins) runs the same writer. The Assistant offers
  it after `create_service` or a published build when the provider has no
  written guide, and runs it only if you agree.
- **Offer refresh** reads pricing pages only and keeps an existing guide
  exactly as it is. Its reviews carry offers only.

**Where guides show up:**

- Dashboard: the provider page's **Guide** tab (with Copy for LLM, and the
  guide fields to edit) and a service's **Integrate** tab, with the real
  proxy URL.
- Apps: `GET /api/v1/services/{slug}/guide` (JSON, or Markdown with
  `Accept: text/markdown` or `?format=md`), linked from each entry of
  `GET /api/v1/services` as `guide_url`.
- MCP: the `service_guide` core tool and the
  `acnologia://services/{slug}/guide` resource on `/mcp`.
- Dashboard API: `GET /api/admin/providers/{slug}/guide` and
  `GET /api/admin/services/{id}/guide`.

Guides are part of the spec, so they are validated on save, versioned,
rolled back and copied to other servers with the rest of it. Built in
providers ship with guides. An install seeded by an older release gets each
built in guide once on boot (as a version noted "built-in guide added"), even
when the spec was edited since, because a guide never changes how requests
are handled. If you remove it afterwards, it stays removed.

## Providers with many APIs

Some providers are a catalog of APIs behind one key, each on its own host.
Omkar Cloud, for example, serves about 185 scrapers at
`<api>.omkar.cloud` and accepts the same `API-Key` on all of them. Such a
provider lists those hosts in `spec.hosts` instead of being split into one
provider per API:

```json
"base_url": "https://google-maps-scraper.omkar.cloud",
"hosts": [
  {"name": "tiktok-scraper", "url": "https://tiktok-scraper.omkar.cloud",
   "title": "TikTok Scraper", "group": "Social & Video",
   "description": "Videos, users, hashtags and ads.",
   "docs_url": "https://www.omkar.cloud/tools/tiktok-scraper",
   "logo_url": "https://www.omkar.cloud/images/tools/tiktok-scraper/logo.png"}
]
```

A caller picks a host with a first path segment of `~` plus its name:
`/proxy/omkar/~tiktok-scraper/videos/details?video=...` goes to
`https://tiktok-scraper.omkar.cloud/videos/details`. A path without the
prefix goes to the service's base URL as before, and a `~name` the spec does
not list is refused with 400. Offers, examples and guide endpoints keep the
`/~name` prefix in their paths, so each API can carry its own offer, limits,
examples and docs; guide endpoints take an optional `group` (the API's
title) and the guide lists every host grouped by `group`. The service page
shows them as a searchable **APIs** card.

Every host must be on the same site as `base_url` (its host without the
first label, `omkar.cloud` here), so a spec can never send a key to another
company's domain. Changing the set of hosts or their URLs counts as a key
target change, like `auth`. The live check runs only examples on the main
host. A spec with hosts may have up to 400 hosts, 400 guide endpoints and
400 offers.

The AI builder looks for a provider's full catalog and fills `hosts` (in
batches with its `stage_parts` tool when the list is long). From the
Assistant, `improve_provider` runs the builder on an existing provider with
your instruction, such as "add every Omkar API, grouped by category", and
`update_provider` edits a provider's name, description, category, website,
docs URL, color, status or any part of its spec (a JSON merge patch) as a
new version. Changes to `base_url`, `auth`, the health check or `hosts`, and
disabling a provider, wait for your Confirm.

The Assistant also runs a catalog entry by entry:

- `get_catalog` lists the entries by category, with each one's URL, docs,
  logo, how many endpoints it documents and its last check.
- `update_catalog` adds, changes or removes entries after your Confirm. A
  provider without a catalog gets one. Endpoints given with an entry
  (paths relative to the entry, such as `/search`) are stored as
  `/~name/search` guide endpoints, so they become MCP tools at once.
  Removing an entry also removes its endpoints, examples and offers.
- `find_missing_catalog` reads the provider's website, docs page, sitemap
  and any pages you name, and lists product hosts on the provider's site
  that the catalog lacks, plus entries those pages no longer mention.
- `verify_catalog` checks entries. Without `live` it checks each host
  answers over HTTPS. With `live` (after your Confirm, since it uses
  quota) it makes one GET per entry through the proxy with a pooled key,
  from an example or an endpoint that needs no parameters, through the
  Assistant app, and reports `ok`, `rate_limited`, `key_rejected`,
  `plan_or_quota`, `not_found`, `no_key`, `provider_error`, `unreachable`
  or `no_test_call`. Results are kept per entry and shown by `get_catalog`.

Ask in plain words: "what Omkar APIs are we missing?", "add them",
"verify the Omkar catalog live", "remove the ones that are dead".

A service on a host with a `logo_url` shows that logo (the site the API
reads, such as TikTok), with the provider's logo as a small badge on it.

When an account's APIs are separate services instead (one service per
Omkar scraper, say), they share keys: a service on the same site with the
same auth placement as a service that has keys is linked to it when it is
created, and uses those keys behind its own. The catalog offers to link any
older ones in one click, and keys stored twice say where their twin is. The
catalog shows such services as one family card, with each key counted once,
that opens to list them.

A key's state is still tracked per key, not per API. When quota or plans
are per API (as with Omkar), write the rules so an error about one API does
not park the key for all of them: a used up API quota as `busy` (rotate to
the next key without a long cooldown) and a missing or inactive plan as `ok`
(the error is relayed and the key kept). The AI builder writes them that way.

## Validation rules

- `slug`: lowercase letters, digits and hyphens.
- `base_url`: http(s) with a host. `website` and `docs_url`: empty or
  http(s).
- `health.path` and example paths start with `/`, so they cannot change
  host. `health.path` may not hold `.` or `..` segments (raw or percent
  encoded), and `health.from_origin` needs a `base_url` without a path, so a
  health check stays under the service's base URL.
- A spec edit by anyone but a superadmin that changes `auth` or the health
  check's method, path, query or `from_origin` pauses hidden keys on every
  service of the provider until a superadmin confirms each one
  ([security](security.md)).
- Offers: names are required and unique, a `match` needs a method, a path or
  examples, paths start with `/`, example names must exist, price units come
  from the list above, limits need a count of at least 1 and a known window,
  `checked_at` is a date and `source_url` is http(s).
- Hosts: names are lowercase letters, digits and hyphens and unique, URLs
  are http(s) on the site of `base_url` with no credentials, query or IP
  address.
- Guide: `summary` is required (at most 1,000 characters), at most 40
  endpoints (400 with hosts) each with a name, a path starting with `/` and a method of GET,
  POST, PUT, PATCH or DELETE, params `in` query, path, body or header, at
  most 30 notes and 20,000 characters of Markdown.
- Rule regexes are RE2, so they cannot backtrack catastrophically. JSON paths
  are depth limited.

## Built in providers

Twenty seven specs ship with the server. They are seeded on first boot and
edited like any other provider. Every health check below spends no quota.
Without one, a key's validity is learned from real traffic.

| Provider (slug) | Category | Base URL | Key | Health check | Live quota |
|---|---|---|---|---|---|
| SerpAPI (`serpapi`) | Search | `https://serpapi.com` | query `api_key` | `GET /account.json` | searches left this month |
| Serper (`serper`) | Search | `https://google.serper.dev` | `X-API-KEY` | none | no |
| Brave Search (`brave-search`) | Search | `https://api.search.brave.com` | `X-Subscription-Token` | none | from response headers |
| Tavily (`tavily`) | Search | `https://api.tavily.com` | `Authorization: Bearer` | `GET /usage` | credits |
| Exa (`exa`) | Search | `https://api.exa.ai` | `x-api-key` | none | no |
| Firecrawl (`firecrawl`) | Search | `https://api.firecrawl.dev` | `Authorization: Bearer` | none | no |
| Jina Search (`jina`) | Search | `https://s.jina.ai` | `Authorization: Bearer` | none | no |
| SearchApi (`searchapi`) | Search | `https://www.searchapi.io` | query `api_key` | none | no |
| Linkup (`linkup`) | Search | `https://api.linkup.so` | `Authorization: Bearer` | none | no |
| You.com Search (`youcom`) | Search | `https://ydc-index.io` | `X-API-Key` | none | no |
| TinyFish Search (`tinyfish-search`) | Search | `https://api.search.tinyfish.ai` | `X-API-Key` | `GET /usage` | no |
| TinyFish Fetch (`tinyfish-fetch`) | Search | `https://api.fetch.tinyfish.ai` | `X-API-Key` | `GET /usage` | no |
| ElevenLabs (`elevenlabs`) | Voice | `https://api.elevenlabs.io` | `xi-api-key` | `GET /v1/user/subscription` | characters left |
| Google Gemini (`gemini`) | LLM | `https://generativelanguage.googleapis.com` | `x-goog-api-key` | `GET /v1beta/models` | no |
| OpenAI (`openai`) | LLM | `https://api.openai.com` | `Authorization: Bearer` | `GET /v1/models` | no |
| Anthropic (`anthropic`) | LLM | `https://api.anthropic.com` | `x-api-key` | `GET /v1/models` | no |
| xAI (`xai`) | LLM | `https://api.x.ai` | `Authorization: Bearer` | `GET /v1/api-key` (also catches blocked or disabled keys) | no |
| Mistral AI (`mistral`) | LLM | `https://api.mistral.ai` | `Authorization: Bearer` | `GET /v1/models` | no |
| DeepSeek (`deepseek`) | LLM | `https://api.deepseek.com` | `Authorization: Bearer` | `GET /user/balance` | balance, USD or CNY |
| Cohere (`cohere`) | LLM | `https://api.cohere.com` | `Authorization: Bearer` | `POST /v1/check-api-key` (`{"valid":false}` is invalid) | no |
| Perplexity (`perplexity`) | LLM | `https://api.perplexity.ai` | `Authorization: Bearer` | `GET /v1/models` | no |
| Together AI (`together`) | LLM | `https://api.together.xyz` | `Authorization: Bearer` | `GET /v1/models` | no |
| Groq (`groq`) | LLM | `https://api.groq.com/openai` | `Authorization: Bearer` | `GET /v1/models` | no |
| Fireworks AI (`fireworks`) | LLM | `https://api.fireworks.ai/inference` | `Authorization: Bearer` | `GET /v1/models` | no |
| Cerebras (`cerebras`) | LLM | `https://api.cerebras.ai` | `Authorization: Bearer` | `GET /v1/models` | no |
| OpenRouter (`openrouter`) | LLM | `https://openrouter.ai/api` | `Authorization: Bearer` | `GET /v1/key` | credit limit and remaining, USD, for keys with a limit |
| Custom API (`generic`) | Other | your API's | a header, a bearer token or a query parameter, set per service | none | no |

Live quota comes from the health check's account endpoint. The exception is
Brave Search, whose quota headers on every response update it as traffic
flows (`quota.headers`).

TinyFish serves each product from its own host with one shared key
(`X-API-Key`), so Search and Fetch are two providers. Add the same key to
both services. Both are free within their limits (see [Offers](#offers)).

Built in providers seeded by an older release get the offers this release
ships on the next boot, as long as nobody has edited them (version 1).

One more spec, `acnologia-peer` ("Acnologia server"), is built in for
services that another Acnologia server shares with this one. Its one key is
the share token, and it counts only the owner's own refusals against it (see
[Providers from another server](#providers-from-another-server)). It is not
counted above.

### LLM APIs

Model API keys pool the same way, including your team's Claude keys. Every
one has a health check that spends nothing, and the invalid key responses
below were checked against the real APIs (see
[Testing against the real APIs](#testing-against-the-real-apis)).

Paths keep their version prefix, so an OpenAI compatible client uses
`http://<acnologia>/proxy/<service>/v1` as its base URL (DeepSeek and
Perplexity serve some routes without `/v1`).

All of them treat a 429 as a rate limit and honor `Retry-After`, a 401 as a
bad key, and a 5xx (including Anthropic's 529) as the provider's fault rather
than the key's. Validation errors (400, 404, 422) are the caller's and leave
the key alone. Notable rules:

- **Anthropic**: specs cannot add static headers, so callers send
  `anthropic-version` themselves, as the Anthropic SDKs do, and the proxy
  forwards it. The health check cannot send it, so a 400 that asks for it
  counts as a working key. The monthly spend cap (429
  `enforced_spend_limit_reached`), a Console usage limit, a low credit
  balance and `billing_error` are `quota_exhausted`. A 403
  `permission_error` is `ok`: the key works but may not use that resource.
- **OpenAI**: billing problems also answer 429, so `insufficient_quota`,
  `credit_balance_exhausted` and the spend limit codes are `quota_exhausted`
  before any other 429. A 403 for an unsupported region is `error`, since
  every key would fail the same way.
- **xAI** answers a wrong key with 400 `Incorrect API key provided`, which
  is `invalid`. Running out of prepaid credit is matched loosely (402 or 403
  mentioning credits or a spending limit) because the text is not
  documented.
- **DeepSeek**: 402 Insufficient Balance is `quota_exhausted`. A 429 means
  the account is at its concurrency limit, so it is `busy` for 2 seconds.
  503 Server Overloaded is `error`. The balance is rounded to whole units, so
  a key with less than 0.5 left reads as exhausted until a check finds more.
- **Groq**: a 429 naming a daily window (RPD, TPD) is `quota_exhausted`
  until `Retry-After`; a console spend limit (`blocked_api_access`) is
  `quota_exhausted`; 498 Flex capacity is `error`.
- **Cohere**: the trial key monthly cap is `quota_exhausted`, the per minute
  limit `rate_limited`, and a 498 deny listed key `banned`.
- **OpenRouter**: a 402 for credit held by requests in flight is `busy`;
  other 402s and 403 key or budget limits are `quota_exhausted`; 403
  moderation and guardrail blocks are `ok`; the free model daily cap is
  `quota_exhausted`; an error object inside a 200 is `error`.
- **Perplexity**: a 429 whose `error.type` is `overloaded` is `error`, since
  it is not the key's limit.
- **Fireworks AI**: 412 account suspended is `banned`; 503 while a
  deployment scales up is `error`.
- **Cerebras**: a 429 naming a daily window is `quota_exhausted`.

None of them use `quota.headers`. Their rate limit headers count per minute
windows (and, for Groq, requests per day) with resets written as durations
such as `6m0s`, which would read as a whole quota. Pacing comes from 429s and
`Retry-After` instead.

Offers list a few headline models per provider with input and output prices
per million tokens, free tiers and credits, and published rate limits per
tier in `notes`, each with its source page, checked 2026-11-03. The model is
chosen in the request body, so a provider's model offers share their
endpoint's match and only the first one selects a request. The rest are
there for people and the AI tools, and token prices never decide a request's
cost. Free offers (Groq's free plan, Mistral's free mode, Cohere trial keys,
OpenRouter's `:free` models) come last for the same reason, so their limits
never pace paid traffic. Only Perplexity's Search API has a per request price
and a limit that holds on every tier (50 a second).

## Providers from another server

When another Acnologia shares a service with this one (see
[sharing](flows.md#sharing-between-servers)), the local service runs on
`acnologia-peer` and calls the owner's proxy with the share token. The
manifest also carries the owner's spec for the service's provider and for any
provider shared on its own. When this server has no provider with that slug,
sync imports it once, with source `peer:<owner id>`. The shared service then
shows the provider's name, logo, docs, examples and offers, and you can add
your own keys to it. A provider you already have is never touched.

An imported provider never updates on its own. When the owner publishes a
newer version, the service page says so and a superadmin can pull it
(`POST /api/admin/peers/{id}/providers/{slug}/pull`). A pull that moves the
base URL needs an explicit yes, and a provider edited here is not offered
updates. The provider refresh skips imported providers unless a superadmin
turns on checking providers copied from another server. Imports stay after a
disconnect or revoke; delete one you do not use from the Providers page.

## Testing against the real APIs

Every built in spec's fixtures run in `go test ./...`. For the LLM APIs,
`server/internal/providers/testdata/observed_no_key.json` also records what
each real API answered on 2026-11-03 when called with no key and with an
invalid one, on its health endpoint and an inference endpoint. A test
replays those responses through the engine (each must classify as
`invalid`) and through the health check (each must report the key invalid
rather than fail).

To call the real APIs again with an invalid key (this needs the network and
no secrets):

```bash
cd server
ACNOLOGIA_LIVE_PROVIDERS=1 go test ./internal/providers -run TestInferenceLiveNoKey -v
```

In PowerShell:

```powershell
cd server
$env:ACNOLOGIA_LIVE_PROVIDERS = "1"
go test ./internal/providers -run TestInferenceLiveNoKey -v
Remove-Item Env:ACNOLOGIA_LIVE_PROVIDERS
```

The test is skipped unless that variable is set, so CI never calls the real
APIs. For each LLM API it runs the health check and the first POST example
with an invalid key: the check must report the key invalid and the response
must classify as `invalid`. When one fails, the provider changed its error,
so update the rule and the recorded response in `observed_no_key.json`
together.
