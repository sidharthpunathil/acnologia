# Instructions for the OpenQodex reviewer

Write here what a reviewer of this repo must know. The agent reads this file at the start of every review and follows it for what to flag and what not to flag. Commit it so the whole team shares it.

## What this repo is

Acnologia is one Go binary backed by one SQLite file. It holds a pool of provider API keys, encrypted at rest with AES-256-GCM, and hands them out or injects them into requests, rotating keys on rate limits. The one process serves the React dashboard (`web/`, embedded in the binary), the dashboard API (`/api/admin`, `/api/auth`), the consumer API (`/api/v1`), the HTTP proxy (`/proxy/{service}/*`), the app and control MCP endpoints (`/mcp`, `/mcp/control`) and the sharing routes between Acnologia servers. `docs/architecture.md` has the package map. SDKs for many languages live under `sdk/`.

## Conventions the code follows

- Go: `gofmt`, standard library first, small packages with tests next to the code. Server changes come with a Go test.
- Providers are data: a provider is a spec (base URL, where the key goes, error classification, health check) with fixture tests, not custom code.
- Dashboard: follows `web/AGENTS.md`. BoardUI components, semantic color tokens and composite type utilities only, `cx()` for class merging, icons from `@remixicon/react`.
- npm dependencies are pinned to exact versions.
- Docs in `docs/` are copied into the binary by `make llms`; a change to behaviour, routes or settings updates `docs/` too.
- Writing (docs, UI text, commit messages): plain, short sentences and no em dashes.

## Never flag

- Generated copies: `server/internal/llmstxt/` and `server/internal/docsmd/` mirror `docs/` and `README.md`. Review the source in `docs/` instead.
- Screenshot baselines under `e2e/__screenshots__/` and images under `docs/screenshots/`.
- Legacy dashboard class aliases (`canvas`, `surface`, `ink`, `muted`, `line` and the rest listed in `web/AGENTS.md`) on pages not yet migrated.

## Always check

- Secrets: a provider key, app token (`acn_`), admin token (`acnadm_`), share token or master key must never be logged, returned in an error, sent to the dashboard in plain text or reach an MCP client. Keys stay inside the pool and proxy.
- Auth on every new route: which of session cookie, app token, admin token or share token it accepts, and that a share token cannot reach a service that was not shared.
- Proxy and sharing code: no SSRF through a user supplied upstream URL or host, and request headers from the client do not override the injected key.
- SQLite: parameterised queries only, and migrations that keep existing databases working.
- Concurrency in the pool and worker (slots, outcomes, refresh): races and locks held across network calls.
- User facing text in the dashboard, apps and MCP tool output keeps Acnologia out of the copy of apps built on its proxy.
