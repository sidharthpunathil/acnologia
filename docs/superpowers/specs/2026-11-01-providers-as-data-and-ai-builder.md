# Cogito v2: providers as data, AI provider builder, logos

## Why

Today a provider (SerpAPI, ElevenLabs) is Go code. Adding one means writing
Go, rebuilding and restarting, which takes the dashboard down. The goal: give
Cogito some context (docs URL, GitHub repo, notes) and have an AI agent research
the API, write the provider, test it, and publish it, with no restart and no
downtime.

## The decision: providers are declarative specs in SQLite

A provider is a JSON **spec** stored in SQLite and interpreted by one generic
engine. No generated code is compiled or executed.

- **Hot:** publishing swaps the spec in an in-memory registry (copy on write).
  Running requests finish on the old spec, new requests use the new one.
  Nothing restarts.
- **Safe:** the AI can only produce data that the engine interprets. A bad spec
  can misclassify responses but cannot run code, so auto publishing is
  acceptable.
- **Testable:** every spec carries fixture tests (recorded or documented
  responses with the expected outcome). The engine runs them before any publish.
- **Versioned:** every save is a new version; rollback is one click.
- **Same engine for built-ins:** SerpAPI, ElevenLabs and Custom API become seeded
  specs. The old Go classifier test cases run against the seeded specs to prove
  parity.

Rejected alternatives: AI-written Go plus rebuild and restart (downtime, runs
untrusted code); a plugin runtime such as Wasm or JS (heavy, a sandbox to
maintain). The rule DSL covers what provider adapters actually do: match an
error response, pick an outcome, read quota from a JSON endpoint.

## Provider spec

```jsonc
{
  "base_url": "https://serpapi.com",
  "auth": { "type": "query", "name": "api_key", "prefix": "" },   // type: query | header | bearer
  "defaults": { "strategy": "round_robin", "cooldown_sec": 3600, "max_attempts": 5,
                "max_concurrency": 0, "queue_timeout_sec": 0 },
  "quota": { "unit": "searches", "counts_per_request": true },      // decrement 1 per success
  "rules": [                                                        // first match wins
    { "name": "Out of searches",
      "match": { "status": [429], "body_contains": ["run out of searches"] },
      "outcome": "quota_exhausted" },
    { "name": "Hourly cap",
      "match": { "body_contains": ["past hour"] },
      "outcome": "rate_limited", "retry_after": { "source": "next_hour" } }
  ],
  "use_standard_rules": true,   // then: 429 rate_limited (Retry-After), 402 quota_exhausted, 401/403 invalid, 5xx error
  "health": {
    "method": "GET", "path": "/account.json", "query": {},
    "invalid": { "status": [401], "body_contains": ["invalid api key"] },
    "ok_anyway": { "body_contains": ["missing_permissions"] },     // key works, quota unknown
    "quota": { "limit": ["searches_per_month", "extra_credits"],     // summed
               "remaining": ["total_searches_left"],
               "used": [],                                           // remaining = limit - used when remaining is empty
               "reset_at": "", "reset_format": "unix",               // unix | unix_ms | iso8601
               "window": { "limit": "account_rate_limit_per_hour", "used": "last_hour_searches", "period": "hour" } }
  },
  "examples": [ { "name": "Google search", "method": "GET", "path": "/search.json",
                  "query": { "q": "coffee", "engine": "google" }, "body": null, "safe": true } ],
  "tests": [ { "name": "out of searches", "status": 429, "headers": {},
               "body": "{\"error\":\"Your account has run out of searches.\"}", "expect": "quota_exhausted" } ]
}
```

Rule `match` fields are ANDed; values inside a list are ORed. Case-insensitive
substring matching. `status` empty means any status >= 400; rules never run on
2xx/3xx unless `status` lists such a code. `json_path` + `json_equals` matches a
field (dot path, `$.` optional, numeric segments index arrays).
`retry_after.source`: `header` (value = header name, default `Retry-After`),
`fixed` (value = seconds), `next_hour`, `body_json` (value = path to seconds),
`none`. Outcomes: `ok`, `rate_limited`, `quota_exhausted`, `invalid`, `error`.

Provider metadata (columns, not spec): slug, name, category, description,
website, docs_url, color, logo, status (`active` | `disabled`), builtin, source
(`builtin` | `manual` | `ai`), version.

## AI provider builder (agentic harness)

A background job runner inside the Go server. One run = one goal: "create
provider" or "update provider X".

Loop (Claude API, manual tool loop, model from `COGITO_AI_MODEL`, default
`claude-opus-5-5`):

1. The prompt holds the user's context, URLs, GitHub repo, the spec format and
   the SerpAPI/ElevenLabs specs as worked examples.
2. Tools:
   - server side: `web_search`, `web_fetch` (Anthropic hosted, so no SSRF exposure for Cogito)
   - `github_read(repo, path, ref)`: lists a dir or reads a file through the GitHub API (`GITHUB_TOKEN` optional)
   - `validate_spec(provider)`: schema and sanity errors
   - `run_tests(provider)`: runs fixture tests through the real engine
   - `live_check(provider)`: only when the user gave a test key: health check plus the first `safe` example, against the real API
   - `submit_provider(provider, summary)`: ends the run
3. The harness never trusts the model: on submit it re-validates and re-runs
   tests itself. Failures go back to the model (max 3 submits).
4. Passing drafts are published automatically when `auto_publish` is on
   (default), else wait in `needs_review`. Publishing can also create a service
   (default on for new providers) so it appears in the catalog immediately.
5. Logo: the harness fetches the provider's logo from its website
   (apple-touch-icon, `<link rel=icon>`, favicon) and stores it in SQLite.

Every step (search, fetch, tool call, result, error) is written to
`agent_steps` and polled by the UI, so you watch the agent work live. Usage is
recorded per run. Test keys are encrypted and wiped when the run ends.

Requires `ANTHROPIC_API_KEY`. Without it the AI builder is disabled; manual
provider editing still works.

## Logos

Providers and services each have an optional logo stored in SQLite (BLOB +
mime, max 512 KB, png/jpeg/webp/svg/ico). A service without its own logo shows
its provider's. A missing logo falls back to a lettermark in the provider color.
SVGs are served with a locked-down CSP and `nosniff`.

## API contract (additions)

Dashboard (session):

- `GET /api/admin/providers` -> `{providers: Provider[], strategies: string[]}`
- `GET /api/admin/providers/{slug}` -> `Provider & {versions: ProviderVersion[]}`
- `POST /api/admin/providers` (admin) body `ProviderInput` -> `{provider, report}`; 400 `{error, errors: string[], report}` when invalid or tests fail
- `PUT /api/admin/providers/{slug}` (admin) body `ProviderInput` + `note` -> `{provider, report}`
- `DELETE /api/admin/providers/{slug}` (admin; not builtin; no services use it)
- `POST /api/admin/providers/validate` body `ProviderInput` -> `{errors: string[], report: TestReport}` (live editor feedback)
- `POST /api/admin/providers/{slug}/live-check` (admin) body `{secret}` -> `LiveReport`
- `POST /api/admin/providers/{slug}/rollback` (admin) body `{version}` -> `Provider`
- `PUT /api/admin/providers/{slug}/logo` (admin) body `{data_base64, mime}`; `DELETE` same path; `POST .../logo/fetch` body `{url?}` fetches from website
- `GET /api/logos/providers/{slug}`, `GET /api/logos/services/{id}` (session) -> image
- `PUT /api/admin/services/{id}/logo`, `DELETE /api/admin/services/{id}/logo` (admin)
- `GET /api/admin/ai/status` -> `{enabled, model, reason}`
- `GET /api/admin/ai/runs` -> `{runs: AgentRun[]}`
- `POST /api/admin/ai/runs` (admin) body `{kind: "create_provider"|"update_provider", provider_slug?, context, urls: string[], github, test_key?, auto_publish (default true), create_service (default true)}` -> `AgentRun`
- `GET /api/admin/ai/runs/{id}?after=<seq>` -> `{run: AgentRun, steps: AgentStep[]}`
- `POST /api/admin/ai/runs/{id}/cancel`, `POST /api/admin/ai/runs/{id}/publish`

```ts
interface ProviderSpec { /* the JSON above */ }
interface Provider {
  slug: string; name: string; category: string; description: string; website: string; docs_url: string;
  color: string; logo_url: string | null; status: "active" | "disabled"; builtin: boolean;
  source: "builtin" | "manual" | "ai"; version: number; updated_at: string; created_at: string;
  services_count: number; spec: ProviderSpec;
  // flattened for convenience, derived from spec:
  base_url: string; auth_type: string; auth_name: string; default_cooldown_sec: number;
  default_max_concurrency: number; default_queue_timeout_sec: number;
}
type ProviderInput = Omit<Provider, "logo_url"|"builtin"|"source"|"version"|"updated_at"|"created_at"|"services_count"|"base_url"|"auth_type"|"auth_name"|"default_cooldown_sec"|"default_max_concurrency"|"default_queue_timeout_sec">;
interface ProviderVersion { version: number; created_at: string; created_by: string; note: string; source: string }
interface TestResult { name: string; expect: string; got: string; ok: boolean; detail: string }
interface TestReport { passed: number; failed: number; results: TestResult[] }
interface LiveReport { ok: boolean; health: { ok: boolean; status: number; detail: string; quota_limit: number|null; quota_remaining: number|null };
                       example: { ran: boolean; ok: boolean; status: number; outcome: string; detail: string } }
interface AgentRun {
  id: number; kind: string; status: "queued"|"running"|"testing"|"needs_review"|"published"|"failed"|"cancelled";
  title: string; provider_slug: string;
  input: { context: string; urls: string[]; github: string; auto_publish: boolean; create_service: boolean; has_test_key: boolean };
  draft: ProviderInput | null; report: TestReport | null; live: LiveReport | null;
  error: string; summary: string; service_id: number | null;
  usage: { input_tokens: number; output_tokens: number; web_searches: number; turns: number; cost_usd: number };
  created_by: string; created_at: string; finished_at: string | null;
}
interface AgentStep { seq: number; ts: string; type: "info"|"thinking"|"search"|"fetch"|"tool"|"result"|"error"|"done"; title: string; detail: string }
```

Changes to existing shapes: `Service` gains `logo_url: string | null`,
`max_concurrency`, `queue_timeout_sec`, `in_flight`, `key_counts.busy`.
`Key` gains `max_concurrency: number | null` (PATCH `-1` clears it) and
`in_flight`. Lease body gains `wait_sec`; 503 code `all_keys_busy`.
