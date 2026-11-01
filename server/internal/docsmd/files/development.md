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
| Dashboard screens | `make test-screens` | Full page screenshots of every dashboard page, see below. |

The API tests spin up the real router on `httptest` with a temporary SQLite
database, so they exercise middleware, auth and the proxy the way production
does.

## Screenshot tests

`e2e/` holds a Playwright suite that screenshots every dashboard page and
compares it with the committed baselines in `e2e/__screenshots__`. It has its
own `package.json`, so it never touches the dashboard's dependencies, and it
uses the installed Google Chrome (no browser download).

```sh
make build                   # the suite tests bin/cogito as built
make test-screens            # compare against the baselines
make test-screens UPDATE=1   # write new baselines after an intended change
cd e2e && npm run report     # open the HTML report with diffs
```

Global setup starts `bin/cogito` on a free port with an empty temp data dir,
a fresh master key and health checks off, then seeds a fixed demo workspace
through the API: five services with keys (one invalid, one out of quota, two
backups), a group, a webhook, an egress proxy, two apps, a second admin and an
API token. The services point at a fake provider API on 127.0.0.1, so key
checks and the seeded proxy traffic never leave the machine. Teardown stops
the server and deletes the data dir.

Each page runs in four projects: desktop (1280x900) and phone (390x844), in
light and dark. Every test fails on a console error, an uncaught page error
or horizontal overflow. Before each capture the suite waits for network idle
and for every `[aria-busy="true"]` skeleton to go, the page clock is frozen a
minute after seeding, and anything that changes from run to run (relative
times, dates, local ports, the animated demos on `/docs`) is painted over.

Baselines are rendered on macOS; other platforms differ in font rendering, so
regenerate them on the machine that runs the suite. Review the diff images
before committing new baselines.

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
