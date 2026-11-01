# Development

Go 1.26, Node 22 and pnpm 10.

```sh
make dev    # Go on :8080 and Vite on :5173 with hot reload
make test   # server tests with the race detector, plus the Go SDK
make build  # bin/cogito with the dashboard and llms.txt embedded
make docker # local image
```

`make dev` runs the server from `server/`, so put a `.env` there or export the
variables. A missing master key makes the server generate `data/master.key`;
if you later start it with a different key it refuses to boot rather than
mixing keys.

## Repository layout

| Path | What |
|---|---|
| `server/cmd/cogito` | Entry point and boot sequence. |
| `server/internal/api` | HTTP handlers, middleware, proxy, MCP. |
| `server/internal/pool` | Key selection, concurrency slots, outcomes, health checks. |
| `server/internal/providers` | Provider spec format and engine, built in specs. |
| `server/internal/catalog` | Provider publishing, versions, logos, live checks, SSRF safe client. |
| `server/internal/agent` | AI provider builder. |
| `server/internal/store` | SQLite schema, migrations, queries. |
| `server/internal/crypto` | Master key, encryption, hashing, scrubbing. |
| `server/internal/worker` | Housekeeping and background health checks. |
| `web/` | React, Vite, Zustand and Tailwind dashboard. Built into `server/internal/webui/dist`. |
| `sdk/` | Go, Python, TypeScript, PHP and Ruby clients. |
| `docs/` | These docs, `llms.txt` (embedded and served at `/llms.txt`), design specs. |
| `reference/` | Third party code used as reference only. Not built. |

See [architecture](architecture.md) for how the packages fit together.

## Tests

| Suite | Run | Covers |
|---|---|---|
| Server | `cd server && go test ./...` | API end to end against a fake SerpAPI (`internal/api/api_test.go`), concurrency, security regressions (`security_test.go`, `mcp_security_test.go`), provider engine, catalog, agent harness. |
| Go SDK | `cd sdk/go && go test ./...` | Client, leases, `WithKey`, secret hygiene. |
| TypeScript SDK | `cd sdk/typescript && npx vitest run` | Same for TS. |
| Python SDK | `cd sdk/python && pytest` | Same for Python (needs `httpx`, `pytest`). |
| Dashboard | `cd web && npx tsc --noEmit -p .` | Type check. |

The API tests spin up the real router on `httptest` with a temporary SQLite
database, so they exercise middleware, auth and the proxy the way production
does.

## Conventions

- Store secrets only through `crypto.Box`; never log a request query string,
  a secret, or an unscrubbed upstream error (`crypto.Scrub`).
- New dashboard routes go under `/api/admin` with the right `requireRole`
  group; anything that can reveal or route a hidden key checks `isSuper` or the
  `lease_hidden` / `target_trusted` flags.
- Schema changes go in `store.migrate` with `addColumn`, so existing databases
  upgrade on start.
- Links built from server data use `safeHref` in the dashboard.
- Update [`llms.txt`](llms.txt) and [`llms-full.txt`](llms-full.txt) when the
  consumer API changes, then `make llms`.
