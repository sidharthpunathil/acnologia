# Acnologia: self-hosted API key pool

## Intent

You build products (websites, agents, scripts) that depend on third party APIs
such as SerpAPI and ElevenLabs. Each provider account has a rate limit or a
monthly quota. You register many accounts per provider and want one place that
holds all their keys and hands out a working one on demand. When a key hits a
rate limit, the caller gets the next available key without any manual work.

Acnologia is that place: a single Go binary with an embedded React dashboard,
self-hosted with Docker, backed by SQLite.

## Goals

- Store many API keys per service, encrypted at rest (AES-256-GCM).
- Hand out keys by availability, with pluggable rotation strategies.
- Detect rate limits and quota exhaustion, cool keys down, and rotate.
- Two ways to consume:
  - **Lease mode**: ask for a key, call the provider yourself, report the outcome.
  - **Proxy mode**: call Acnologia instead of the provider; Acnologia injects a key,
    retries on rate limits with the next key, and never exposes secrets.
- Roles: `superadmin`, `admin`, `member`.
- Key flags: `hidden` (only superadmins see it or count it), `tier` = `primary`
  or `backup` (backup keys are used only when no primary is available),
  `restricted` (only allow-listed apps may use it), `status` = `active`,
  `disabled`, `exhausted`, `invalid`.
- Built-in providers: SerpAPI and ElevenLabs, plus a `generic` provider for any
  API key based HTTP service.
- SDKs: Python, Go, TypeScript/JavaScript, PHP, Ruby. `llms.txt`, an MCP server
  endpoint, and an OpenAPI-ish reference in docs.
- Self-hostable: one Docker image, one volume.

## Non-goals (v1)

- Multi-tenant SaaS billing.
- Postgres (the store layer is isolated so it can be added later).
- OAuth or SSO logins.

## Anti-goals

- Leaking secrets: members and apps in proxy-only mode must never see raw keys.
- Showing the real pool size to anyone but superadmins when keys are hidden.

## Domain model

| Entity  | Notes |
|---------|-------|
| User    | email, name, bcrypt password, role, disabled |
| Session | random token (sha256 stored), cookie `acnologia_session`, 14 day expiry |
| Service | slug, name, provider, category, description, base_url, auth_type (`query`, `header`, `bearer`), auth_name, strategy, cooldown_sec, max_attempts, icon |
| Key     | service, label, encrypted secret, hint (last 4), tier, hidden, restricted, status, weight, priority, cooldown_until, consecutive_fails, quota_limit, quota_remaining, quota_reset_at, usage_total, usage_today, last_used_at, last_error, notes |
| App     | a consumer, e.g. "my website". Token `acn_...` (sha256 stored, shown once), allowed services (empty means all), mode `proxy`, `lease` or `both`, active |
| KeyApp  | allow-list rows for restricted keys |
| Lease   | key handed to an app, ttl, outcome |
| Event   | lease, proxy, rate_limited, quota_exhausted, invalid, error, health |
| Audit   | who did what in the dashboard |

## Selection algorithm

For `service S`, app `A`, exclusions `X`:

1. Candidates: keys of S where status is `active`, `cooldown_until <= now`,
   `quota_remaining` is null or above 0, not in X, and (not restricted or A is
   allow-listed).
2. Use primary tier candidates; fall back to backup tier only if no primary is
   available.
3. Pick with the service strategy:
   - `round_robin`: least recently used first.
   - `fill_first`: lowest `priority` number first, then least recently used.
   - `least_used`: lowest `usage_today`.
   - `most_quota`: highest `quota_remaining` (null treated as infinite).
   - `random`: weighted random by `weight`.
4. No candidate: HTTP 503 with `retry_after_sec` = seconds until the earliest
   cooldown ends (if any).

Selection runs under a per-service mutex so two concurrent callers do not get
the same "least recently used" key.

## Outcome classification

`Classify(status, headers, body) -> {kind, retry_after}` per provider.

- **SerpAPI**: 429 with "run out of searches" -> `quota_exhausted`; other 429 ->
  `rate_limited` (default 3600s, the hourly limit); 401 or "Invalid API key" ->
  `invalid`. Health check: `GET https://serpapi.com/account.json?api_key=` reads
  `total_searches_left`, `searches_per_month`.
- **ElevenLabs**: 429 `too_many_concurrent_requests` -> `rate_limited` 5s;
  429 `system_busy` -> `error` (provider side, key not penalized); 401
  `quota_exceeded` -> `quota_exhausted`; 401 `invalid_api_key` -> `invalid`.
  Health check: `GET https://api.elevenlabs.io/v1/user/subscription` reads
  `character_count`, `character_limit`, `next_character_count_reset_unix`.
- **Generic**: 429 -> `rate_limited` (honors `Retry-After`), 402 ->
  `quota_exhausted`, 401/403 -> `invalid`.

Effects: `rate_limited` sets `cooldown_until = now + retry_after` or
`cooldown_sec * 2^(consecutive_fails-1)` capped at 1h. `quota_exhausted` sets
status `exhausted` until `quota_reset_at` (or 24h); the background worker
reactivates it. `invalid` sets status `invalid`. `ok` resets consecutive_fails.

## Auth

- Dashboard: session cookie. First run with zero users shows a setup screen that
  creates the superadmin.
- Apps: `Authorization: Bearer acn_...` or `X-Acnologia-Token: acn_...` or
  query `acn_token=acn_...` (proxy only, for tools that only take a URL). All
  three are stripped before forwarding upstream.

## Permissions

| Action | member | admin | superadmin |
|---|---|---|---|
| View catalog, services, visible keys metadata, events | yes | yes | yes |
| See hidden keys, true counts | no | no | yes |
| Create/edit services, keys, apps | no | yes (not hidden keys) | yes |
| Reveal a key secret | no | no | yes (audited) |
| Manage users, view audit log | no | no | yes |

## HTTP API

All JSON. Errors: `{"error": {"code": "string", "message": "string"}}`.

### Public
- `GET /healthz` -> `{"ok": true, "version": "..."}`
- `GET /llms.txt`
- `POST /mcp` MCP streamable HTTP (JSON responses), app token auth.

### Consumer API (app token)
- `GET /api/v1/services` -> `{"services": [{"slug","name","provider","category","available": bool,"base_url","auth": {"type","name"}}]}`
- `POST /api/v1/lease` body `{"service": "serpapi", "exclude_key_ids": [], "ttl_sec": 300}`
  -> 200 `{"lease_id","key_id","service","secret","auth": {"type","name"},"base_url","expires_at"}`
  -> 503 `{"error": {"code":"no_key_available","message"}, "retry_after_sec": n}`
- `POST /api/v1/leases/{lease_id}/report` body `{"outcome": "ok|rate_limited|quota_exhausted|invalid|error", "status_code": 429, "retry_after_sec": 60, "message": "", "rotate": true}`
  -> `{"ok": true, "next": <lease or null>}`
- `ANY /proxy/{service_slug}/{path...}` forwards to `base_url/{path}` with query
  string and body. Response headers add `X-Acnologia-Key-Id`, `X-Acnologia-Attempts`.

### Dashboard API (session cookie)
- `GET /api/auth/state` -> `{"setup_required": bool, "user": User|null}`
- `POST /api/auth/setup` `{"email","name","password"}` (only when no users)
- `POST /api/auth/login` `{"email","password"}`; `POST /api/auth/logout`
- `GET /api/admin/overview` -> `{"services": n, "keys": {"total","active","cooling","exhausted","invalid","disabled","backup"}, "requests_24h", "rate_limits_24h", "recent_events": [Event]}`
- `GET/POST /api/admin/services`, `GET/PATCH/DELETE /api/admin/services/{id}`
  - Service list items include `key_counts` `{"total","available","cooling","exhausted","invalid","disabled","backup"}` (hidden keys excluded unless superadmin).
- `GET /api/admin/providers` -> built-in provider templates (defaults for base_url, auth, category, icon).
- `GET /api/admin/services/{id}/keys`, `POST /api/admin/services/{id}/keys` `{"keys": [{"label","secret","tier","hidden","restricted","weight","priority","notes","app_ids": []}]}` (bulk)
- `PATCH/DELETE /api/admin/keys/{id}`, `POST /api/admin/keys/{id}/reveal` (superadmin), `POST /api/admin/keys/{id}/check` (health check now), `POST /api/admin/keys/{id}/reset` (clear cooldown, set active)
- `GET/POST /api/admin/apps`, `PATCH/DELETE /api/admin/apps/{id}`, `POST /api/admin/apps/{id}/rotate-token`
- `GET /api/admin/events?service_id=&key_id=&limit=`
- `GET/POST /api/admin/users`, `PATCH/DELETE /api/admin/users/{id}` (superadmin)
- `GET /api/admin/audit` (superadmin)
- `GET /api/admin/me`, `PATCH /api/admin/me` (name, password)

## Stack (builder's calls)

- Go 1.26, chi router, `modernc.org/sqlite` (pure Go, no CGO), bcrypt.
- Master key from `ACNOLOGIA_MASTER_KEY` (base64, 32 bytes). If unset, generated
  into `$ACNOLOGIA_DATA_DIR/master.key` on first boot with a loud log line.
- React 18 + Vite + TypeScript + Zustand + Tailwind, built and embedded in the
  Go binary with `embed`.
- UI follows treg.to/catalog: warm off-white canvas, white rounded cards,
  monospace meta lines, category tabs, top search.

## JSON shapes

IDs are integers except `lease_id` (string). Timestamps are RFC 3339 strings or null.

```ts
type Role = "superadmin" | "admin" | "member";
interface User { id: number; email: string; name: string; role: Role; disabled: boolean; created_at: string; last_login_at: string | null }
interface KeyCounts { total: number; available: number; cooling: number; exhausted: number; invalid: number; disabled: number; backup: number }
interface Service {
  id: number; slug: string; name: string; provider: "serpapi" | "elevenlabs" | "generic";
  category: string; description: string; base_url: string;
  auth_type: "query" | "header" | "bearer"; auth_name: string;
  strategy: "round_robin" | "fill_first" | "least_used" | "most_quota" | "random";
  cooldown_sec: number; max_attempts: number; icon: string; created_at: string;
  key_counts: KeyCounts; requests_24h: number;
}
type KeyStatus = "active" | "disabled" | "exhausted" | "invalid";
interface Key {
  id: number; service_id: number; label: string; hint: string;          // hint like "...a9F2"
  tier: "primary" | "backup"; hidden: boolean; restricted: boolean; app_ids: number[];
  status: KeyStatus; effective_status: KeyStatus | "cooling";          // cooling = active but cooldown_until in future
  weight: number; priority: number; cooldown_until: string | null; consecutive_fails: number;
  quota_limit: number | null; quota_remaining: number | null; quota_reset_at: string | null;
  usage_total: number; usage_today: number; last_used_at: string | null; last_error: string;
  last_checked_at: string | null; notes: string; created_at: string;
}
interface App {
  id: number; name: string; description: string; token_prefix: string;   // "acn_ab12"
  allowed_services: string[]; mode: "proxy" | "lease" | "both"; active: boolean;
  created_at: string; last_used_at: string | null;
}
// POST /api/admin/apps and POST /api/admin/apps/{id}/rotate-token return { app: App, token: string } (token shown once)
interface Event {
  id: number; ts: string; kind: "lease" | "proxy" | "ok" | "rate_limited" | "quota_exhausted" | "invalid" | "error" | "health";
  service_id: number | null; service_slug: string; key_id: number | null; key_label: string;
  app_id: number | null; app_name: string; status_code: number; latency_ms: number; detail: string;
}
interface AuditEntry { id: number; ts: string; user_email: string; action: string; target: string; detail: string }
interface ProviderTemplate { provider: string; name: string; category: string; description: string; base_url: string; auth_type: string; auth_name: string; icon: string; default_cooldown_sec: number; docs_url: string }
```

List endpoints wrap results: `{"services": [...]}`, `{"keys": [...]}`, `{"apps": [...]}`,
`{"events": [...]}`, `{"users": [...]}`, `{"audit": [...]}`, `{"providers": [...]}`.
Single resources return the object directly. `POST /api/admin/keys/{id}/reveal` returns `{"secret": "..."}`.
`POST /api/admin/keys/{id}/check` returns the updated Key.
`PATCH /api/admin/keys/{id}` accepts any of: label, secret, tier, hidden, restricted, app_ids, status, weight, priority, notes, quota_limit.

Dev ports: Go server on `:8080`; Vite dev server on `:5173` proxying `/api`, `/proxy`, `/mcp`, `/healthz`, `/llms.txt` to `:8080`.
The production build is emitted to `server/internal/webui/dist` and embedded.

## Calls made during the build

- The user skipped the question round, so every choice here is the builder's call.
- CSRF guard: mutating `/api/*` calls need `Content-Type: application/json` or `X-Requested-With`; the session cookie is also `SameSite=Lax`.
- Hidden keys still serve traffic. For non superadmins they are excluded from lists, counts and events, and admins cannot delete a service that holds them.
- Proxy retries only on key-specific outcomes (rate_limited, quota_exhausted, invalid). It does not retry 5xx, because a retried TTS call could be billed twice.
- Accept-Encoding is dropped upstream so the Go transport decompresses error bodies for classification.
- SerpAPI quota is decremented locally per successful proxied search; ElevenLabs quota is refreshed by health checks (character cost is not known per request).
- Health checks run every 6h (`ACNOLOGIA_HEALTH_INTERVAL`), hourly for exhausted keys, and right after keys are added.
- A failing SerpAPI account check marks a key invalid only on 401 or "Invalid API key"; other errors are treated as transient.
