# Development

Go 1.26, Node 22 and pnpm 10.

```sh
make dev    # Go on :8080 and Vite on :5173 with hot reload
make test   # server tests with the race detector, plus the Go SDK
make build  # bin/acnologia with the dashboard and llms.txt embedded
make docker # local image
```

`make dev` runs the server from `server/`, so put a `.env` there or export the
variables. A missing master key makes the server generate `data/master.key`;
if you later start it with a different key it refuses to boot rather than
mixing keys.

## Repository layout

| Path | What |
|---|---|
| `server/cmd/acnologia` | Entry point and boot sequence. |
| `server/internal/api` | HTTP handlers, middleware, proxy, MCP. |
| `server/internal/pool` | Key selection, concurrency slots, outcomes, health checks. |
| `server/internal/providers` | Provider spec format and engine, built in specs. |
| `server/internal/catalog` | Provider publishing, versions, logos, live checks, SSRF safe client. |
| `server/internal/agent` | AI provider builder and the shared Claude client. |
| `server/internal/store` | SQLite schema, migrations, queries. |
| `server/internal/crypto` | Master key, encryption, hashing, scrubbing. |
| `server/internal/redact` | Keeps secrets out of requests to Claude, stored text and logs. |
| `server/internal/ctl` | The `acnologia ctl` client. |
| `server/internal/worker` | Housekeeping and background health checks. |
| `web/` | React, Vite, Zustand and Tailwind dashboard. Built into `server/internal/webui/dist`. |
| `sdk/` | Go, Python, TypeScript, PHP and Ruby clients. |
| `e2e/` | Playwright screenshot suite for the dashboard. |
| `scripts/build.ps1` | The Makefile's build steps for Windows PowerShell. |
| `install.sh`, `install.ps1` | `acn` installers for macOS and Linux, and for Windows. |
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

CI (`.github/workflows/ci.yml`) runs the server, the Go, Python and
TypeScript SDKs on Linux, macOS and Windows for every push and pull request,
plus a Windows job that builds both binaries with `scripts/build.ps1`.

## Windows

Everything builds natively on Windows with Go 1.26, Node 22 and pnpm 10
(`npm install -g pnpm@10.24.0`); CGO is not needed because the SQLite driver
is pure Go. The Makefile needs `make` and `sh`, so use
`scripts/build.ps1` instead, from Windows PowerShell 5.1 or PowerShell 7:

| make | PowerShell | Result |
|---|---|---|
| `make build` | `.\scripts\build.ps1` | dashboard, docs and `bin\acnologia.exe` |
| `make cli` | `.\scripts\build.ps1 cli` | `bin\acn.exe` |
| `make llms` | `.\scripts\build.ps1 llms` | copies `docs/` into the server's embedded copies |
| `make web` | `.\scripts\build.ps1 web` | `pnpm install` and `pnpm build` in `web/` |
| `make test` | `.\scripts\build.ps1 test` | server and Go SDK tests, without `-race` |

If scripts are blocked, run `Set-ExecutionPolicy -Scope CurrentUser
RemoteSigned` once, or call it as `powershell -ExecutionPolicy Bypass -File
.\scripts\build.ps1`. Without the script, the same steps by hand:

```powershell
cd web; pnpm install --frozen-lockfile; pnpm build; cd ..
.\scripts\build.ps1 llms
cd server
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=dev" -o ..\bin\acnologia.exe .\cmd\acnologia
go build -trimpath -ldflags "-s -w -X main.version=dev" -o ..\bin\acn.exe .\cmd\acn
```

For `make dev`, run the two halves in two terminals:

```powershell
cd server; $env:ACNOLOGIA_DATA_DIR = "..\data"; go run .\cmd\acnologia
cd web; pnpm dev
```

The repository's `.gitattributes` checks text files out with LF on every
platform (PowerShell scripts with CRLF), so `core.autocrlf` does not change
the embedded docs or test fixtures. `go test -race` needs cgo and a C
compiler; on Windows run the tests without it. Cross compile from any OS with
`GOOS=windows GOARCH=amd64` (or `arm64`) and `CGO_ENABLED=0`.

## Screenshot tests

`e2e/` holds a Playwright suite that screenshots every dashboard page and
compares it with the committed baselines in `e2e/__screenshots__`. It has its
own `package.json`, so it never touches the dashboard's dependencies, and it
uses the installed Google Chrome (no browser download).

```sh
make build                   # the suite tests bin/acnologia as built
make test-screens            # compare against the baselines
make test-screens UPDATE=1   # write new baselines after an intended change
cd e2e && npm run report     # open the HTML report with diffs
```

Global setup starts `bin/acnologia` on a free port with an empty temp data dir,
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
  a secret, or an unscrubbed upstream error (`crypto.Scrub`). Text that is
  stored or sent to Claude goes through `Store.Scrub` or the agent client,
  which both use `internal/redact`.
- New dashboard routes go under `/api/admin` with the right `requireRole`
  group; anything that can reveal or route a hidden key checks `isSuper` or the
  `lease_hidden` / `target_trusted` flags.
- Schema changes go in `store.migrate` with `addColumn`, so existing databases
  upgrade on start.
- Links built from server data use `safeHref` in the dashboard.
- Update [`llms.txt`](llms.txt) and [`llms-full.txt`](llms-full.txt) when the
  consumer API changes, then `make llms`. That target also copies `README.md`
  (as the Overview page), `docs/*.md` and `docs/screenshots/*.png` into the
  binary.
- Refresh `docs/screenshots/` from a seeded server, not from the e2e
  baselines, which carry mask bars over dynamic values.
