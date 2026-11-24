# Development

From a fresh clone to the tests and a running dashboard, then the repository
layout, every test suite, CI, Windows builds, screenshot tests and the
conventions a change should follow.

## Quick start

You need Go 1.26, Node 22 and pnpm 10 (the repo pins `pnpm@10.24.0`). On
Windows, see [Windows](#windows) for the PowerShell equivalents.

```sh
git clone https://github.com/sidharthpunathil/acnologia.git
cd acnologia
make test                            # server tests with the race detector, plus the Go SDK
(cd web && pnpm install)             # dashboard dependencies, once
make dev                             # Go on :8080, Vite on :5173 with hot reload
```

Open http://localhost:5173 and create the first account. Vite forwards `/api`,
`/proxy`, `/mcp`, `/healthz`, `/llms.txt`, `/llms-full.txt` and the raw docs to
the server on :8080; set `ACNOLOGIA_BACKEND` to point it at another instance.

Other targets:

```sh
make build   # bin/acnologia with the dashboard, docs and llms.txt embedded
make cli     # bin/acn alone, no web build
make run     # make build, then run bin/acnologia with ./data
make docker  # local image tagged acnologia:<version> and acnologia:latest
make clean   # remove bin/ and the built dashboard assets
```

`make dev` runs `make llms` first, then starts the server from `server/` with
`ACNOLOGIA_DATA_DIR=../data`, so local data lands in `data/` at the repo root.
The server reads a `.env` from the directory it starts in, so for `make dev`
put one in `server/` (copy `.env.example`) or export the variables. With no
master key set, the server generates `data/master.key`. Start it later with a
different key and it refuses to boot rather than mix keys.

## Repository layout

| Path | What |
|---|---|
| `server/cmd/acnologia` | Entry point and boot sequence. |
| `server/cmd/acn` | The `acn` command line client. |
| `server/internal/api` | HTTP handlers, middleware, proxy, MCP, sharing routes, the Assistant and its compaction. |
| `server/internal/pool` | Key selection, pacing, concurrency slots, outcomes, health checks. |
| `server/internal/providers` | Provider spec format and engine, built in specs. |
| `server/internal/catalog` | Provider publishing, versions, logos, live checks, SSRF safe client. |
| `server/internal/agent` | The shared Claude client, the AI provider builder, Fill with AI, model choice per feature, model prices and the offers check. |
| `server/internal/refresh` | Scheduled provider offers and Claude price refresh: settings, runs, diffs, reviews. |
| `server/internal/peer` | Server identity, invites, the manifest client, sync with other servers and key handover. |
| `server/internal/store` | SQLite schema, migrations, queries. |
| `server/internal/crypto` | Master key, encryption, hashing, scrubbing. |
| `server/internal/redact` | Keeps secrets out of requests to Claude, stored text and logs. |
| `server/internal/ctl` | The `acn` and `acnologia ctl` client. |
| `server/internal/mcpbridge` | The stdio MCP bridge shared by `acnologia mcp` and `acn mcp`. |
| `server/internal/worker` | Housekeeping, background health checks, peer sync, sharing timers and the refresh schedule. |
| `server/internal/breaker`, `egress`, `respcache`, `notify`, `keyparse` | Circuit breaker, egress proxies, response cache, webhooks, the key paste parser. |
| `server/internal/config` | Environment settings and the default data directory per OS. |
| `server/internal/docsmd`, `llmstxt`, `webui` | Embedded copies of the docs, `llms.txt` and the built dashboard. |
| `web/` | React, Vite, Zustand and Tailwind dashboard. Built into `server/internal/webui/dist`. Each page is its own chunk: a new page goes in with `lazyPage` and in the `PAGES` list in `App.tsx` (or `PublicRoutes.tsx`), so its code is fetched before the first render. |
| `sdk/` | The seventeen client libraries, see [SDKs](sdks.md). |
| `e2e/` | Playwright screenshot suite for the dashboard. |
| `scripts/build.ps1` | The Makefile's build steps for Windows PowerShell. |
| `install.sh`, `install.ps1` | `acn` installers for macOS and Linux, and for Windows. |
| `docs/` | These docs, `llms.txt` (embedded and served at `/llms.txt`), design specs. |
| `reference/` | Third party code kept for reference only. Not built. |

See [architecture](architecture.md) for how the packages fit together.

## Tests

| Suite | Run | Covers |
|---|---|---|
| Server | `cd server && go test ./...` | The API end to end against a fake upstream (`internal/api/api_test.go`), concurrency, security regressions (`security_test.go`, `mcp_security_test.go`), provider engine, catalog, agent harness, embedded docs (`docs_test.go`). |
| Go SDK | `cd sdk/go && go test ./...` | Client, leases, `WithKey`, secret hygiene. |
| TypeScript SDK | `cd sdk/typescript && pnpm install && pnpm test && pnpm typecheck` | The same for TypeScript. |
| Python SDK | `cd sdk/python && pip install -e '.[test]' && pytest` | The same for Python. |
| Dashboard | `cd web && pnpm typecheck && pnpm test` | Type check, then the Vitest unit tests (`web/src/**/*.test.ts(x)`): the key paste parser against the shared cases in `server/internal/keyparse/testdata/cases.json`, the MCP paste parser, the format and URL helpers, and `StatusPill` rendered with Testing Library in jsdom. `pnpm build` type checks and builds. |
| Lint | `make lint` | `gofmt -l`, `go vet` and golangci-lint on the server, set up in `server/.golangci.yml`. |
| Secret scan | `make secrets` | gitleaks over the commits not on `origin/main` yet (`SECRETS_RANGE=<from>..<to>` for others), with `.github/gitleaks.toml`. |
| Dashboard screens | `make test-screens` | Full page screenshots of every dashboard page, see [Screenshot tests](#screenshot-tests). |
| Live provider checks | `cd server && ACNOLOGIA_LIVE_PROVIDERS=1 go test ./internal/providers -run TestInferenceLiveNoKey -v` | Calls the real LLM APIs with an invalid key. Needs the network but no secrets; skipped without the variable. See [providers](providers.md#testing-against-the-real-apis). |

The API tests start the real router on `httptest` with a temporary SQLite
database, so they exercise middleware, auth and the proxy the way production
does.

A Vitest file runs in Node unless it starts with `// @vitest-environment
jsdom`, which component tests need. Keep tests to pure logic in `web/src/lib`
and small components; the screenshot suite covers whole pages.

### Before pushing

`make check` runs what the go and dashboard CI jobs run on a push: the
`gofmt` check, `go vet`, golangci-lint, the server tests (without `-race`),
then the dashboard type check and unit tests. It needs `web/node_modules`, so
run `pnpm install` in `web/` once. golangci-lint and gitleaks are pinned to the
versions CI uses and run through `go run`, which builds each once and caches
it, so there is nothing else to install.

The linters are staticcheck, govet, errcheck (not in tests), ineffassign and
unused. Fix a finding rather than silencing it. When a finding is wrong, add
a narrow rule under `exclusions` in `server/.golangci.yml` (a path and the
finding's text) with a comment saying why.

### CI

`.github/workflows/ci.yml` runs only on a push to `main`, and on demand
(`gh workflow run ci.yml --ref main`). Day to day work goes to the `stage`
branch, which runs nothing; merging `stage` (or a pull request) into `main`
tests the whole batch in one run. Pull requests themselves run nothing, and
a push to `main` that only touches Markdown, `docs/`, the embedded doc copies
in `server/internal/docsmd` and `llmstxt`, `video/` or issue templates runs
nothing either. A newer push to `main` cancels the older run, and every job
has a timeout.

Every job runs offline against fakes. A `changes` job first works out which
areas a push touched, by diffing it against the last commit a `ci` run passed
on, and only those jobs run. A manual run, or a push it cannot diff (no
earlier passing run, or one that is no ancestor after a force push), runs
them all. Editing `ci.yml` does not by itself run anything. A skipped job
counts as passed.

A push runs on Linux only, without `-race`. A manual run is the full check:
the macOS and Windows legs, `-race` (except on Windows), and the windows
build job. Run one before a release or after an OS specific change.

The secret scan starts from the same last passing commit as the `changes`
job, so commits from a run that was cancelled or failed, and docs only
pushes that ran nothing, are scanned by the next run. `.github/gitleaks.toml`
keeps the default gitleaks rules and allows only the fake keys the repo uses
on purpose: Go test files under `server/internal`, the key paste cases,
`web/src` test files, and the `acn_your_app_token` style placeholders in the
docs and dashboard examples. A real looking key anywhere else fails the run;
replace it with an obvious placeholder rather than widening the allowlist.

Dependabot (`.github/dependabot.yml`) checks Go modules in `server/`, npm in
`web/` and the GitHub Actions every Monday. Version updates are grouped into
one pull request per ecosystem (npm majors come on their own) and target
`stage`, so they reach CI with the next merge into `main`. Security updates
open against `main` as soon as an advisory lands.

| Job | On a push | Manual run adds | What |
|---|---|---|---|
| go | Linux | macOS, Windows, `-race` | `go vet`, server and CLI tests, and on Linux a `gofmt -l` check, golangci-lint (`server/.golangci.yml`, cached by the action) and a Windows arm64 cross compile and vet |
| embedded docs | Linux | | The docs tests alone, when the copies in `server/internal/docsmd` or `llmstxt` changed but no server Go code did (the go job runs them anyway) |
| go, python, ruby, c, cpp, haskell, zig, typescript sdk | Linux | macOS, Windows | Each SDK's own tests, when its folder under `sdk/` changed |
| elixir sdk | Linux (two Elixir and OTP versions) | Windows | `mix test` |
| lua sdk | Linux (Lua 5.1 and 5.4) | macOS | `busted` |
| dashboard | Linux | | `pnpm test`, then `pnpm build`, when `web/` or the shared key paste cases changed |
| secret scan | Linux, every run | | gitleaks with `.github/gitleaks.toml` over the commits since the last passing run, or the whole history on a manual run or a push it cannot diff |
| windows build | (manual only) | Windows | Parses `install.ps1`, `setup.ps1` and `scripts/build.ps1` in PowerShell 5.1 and 7, builds both binaries with `build.ps1` and runs them, when the build tooling changed |

### Releases

Releases are tag driven. A `v*` tag runs `release-server.yml`, which builds the
server for Linux, macOS and Windows on amd64 and arm64 (`.tar.gz`, or `.zip`
for Windows, plus `SHA256SUMS`). A `cli-v*` tag runs `release-cli.yml`, which
builds `acn` for the same six targets; `install.sh` and `install.ps1`
download from those releases.

## Windows

Everything builds natively on Windows with Go 1.26, Node 22 and pnpm 10
(`npm install -g pnpm@10.24.0`). CGO is not needed because the SQLite driver
is pure Go. The Makefile needs `make` and `sh`, so use `scripts/build.ps1`
instead, from Windows PowerShell 5.1 or PowerShell 7:

| make | PowerShell | Result |
|---|---|---|
| `make build` | `.\scripts\build.ps1` | dashboard, docs and `bin\acnologia.exe` |
| `make cli` | `.\scripts\build.ps1 cli` | `bin\acn.exe` |
| `make llms` | `.\scripts\build.ps1 llms` | copies `docs/` into the server's embedded copies |
| `make web` | `.\scripts\build.ps1 web` | `pnpm install` and `pnpm build` in `web/` |
| `make test` | `.\scripts\build.ps1 test` | server and Go SDK tests, without `-race` |

If scripts are blocked, run `Set-ExecutionPolicy -Scope CurrentUser
RemoteSigned` once, or call the script as `powershell -ExecutionPolicy Bypass
-File .\scripts\build.ps1`. The same steps by hand:

```powershell
cd web; pnpm install --frozen-lockfile; pnpm build; cd ..
.\scripts\build.ps1 llms
cd server
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=dev" -o ..\bin\acnologia.exe .\cmd\acnologia
go build -trimpath -ldflags "-s -w -X main.version=dev" -o ..\bin\acn.exe .\cmd\acn
```

For `make dev`, run `.\scripts\build.ps1 llms` once, then the two halves in two
terminals:

```powershell
cd server; $env:ACNOLOGIA_DATA_DIR = "..\data"; go run .\cmd\acnologia
cd web; pnpm dev
```

Notes:

- `.gitattributes` checks text files out with LF on every platform
  (PowerShell scripts with CRLF), so `core.autocrlf` does not change the
  embedded docs or test fixtures.
- `go test -race` needs cgo and a C compiler, so on Windows run the tests
  without it.
- Without `ACNOLOGIA_DATA_DIR`, the data directory on Windows is
  `%LocalAppData%\Acnologia`.
- Cross compile for Windows from any OS with `GOOS=windows GOARCH=amd64` (or
  `arm64`) and `CGO_ENABLED=0`.

## Screenshot tests

`e2e/` holds a Playwright suite that screenshots every dashboard page and
compares it with the committed baselines in `e2e/__screenshots__`. It has its
own `package.json`, so it never touches the dashboard's dependencies, and it
uses the installed Google Chrome instead of downloading a browser.

```sh
make build                   # the suite tests bin/acnologia as built
make test-screens            # compare against the baselines
make test-screens UPDATE=1   # write new baselines after an intended change
cd e2e && npm run report     # open the HTML report with diffs
```

Global setup starts `bin/acnologia` on a free port with an empty temp data
dir, a fresh master key and health checks off, then seeds a fixed demo
workspace through the API: five services with keys (one invalid, one out of
quota, two backups), a group, a webhook, an egress proxy, two apps, a second
admin and an API token. The services point at a fake provider API on
127.0.0.1, so key checks and the seeded proxy traffic never leave the
machine. Teardown stops the server and deletes the data dir.

Each page runs in four projects: desktop (1280x900) and phone (390x844), each
in light and dark. A test fails on a console error, an uncaught page error or
horizontal overflow. Before each capture the suite waits for network idle and
for every `[aria-busy="true"]` skeleton to clear. The page clock is frozen a
minute after seeding, and anything that changes from run to run (relative
times, dates, local ports, the animated demos on `/docs`) is painted over.

Baselines are rendered on macOS. Other platforms render fonts differently, so
regenerate the baselines on the machine that runs the suite, and review the
diff images before committing them.

## Changing the schema

The database records its schema version in SQLite's `PRAGMA user_version`.
Version 1 is the baseline: the `schema` constant and `store.migrate`, the
idempotent steps (`CREATE TABLE IF NOT EXISTS`, `addColumn`) that brought
every database up to date before versioning. A database from before then
has version 0, gets the baseline and is set to 1. The baseline is frozen, so
don't add to it.

A schema change is one entry appended to `migrations` in
`server/internal/store/store.go`, with its function next to the code that
uses the table:

```go
var migrations = []migration{
	{"oauth device codes", migrateOAuthDeviceCodes}, // version 2
}
```

On start, every step the database has not had runs once, in order, inside
one transaction with the rest of the upgrade, and the version goes up after
each. A failure anywhere rolls all of it back. The step runs on the single
writer connection inside that transaction, so it uses `db` directly and
never begins another transaction. It does not need to be idempotent. Never
edit, reorder or remove a step that has shipped: fix a mistake with a new
step.

A binary refuses to start on a database with a newer version than it knows,
and says which versions and what to do. Before a step changes an existing
database, the server copies it to `acnologia.db.pre-v<from>-to-v<to>.bak`
next to it and keeps the last three (`ACNOLOGIA_MIGRATION_BACKUP=false`
turns this off). `store.Open` never copies; the server opens the store with
`store.OpenWith` and the setting. Tests that need their own steps call
`open` with a list, as `schema_version_test.go` does.

## Conventions

- Store secrets only through `crypto.Box`. Never log a request query string,
  a secret, or an unscrubbed upstream error (`crypto.Scrub`). Text that is
  stored or sent to Claude goes through `Store.Scrub` or the agent client,
  which both use `internal/redact`.
- New dashboard routes go under `/api/admin` in the right `requireRole`
  group. Anything that can reveal or route a hidden key checks `isSuper` or
  the `lease_hidden` / `target_trusted` flags.
- A new route that must work while the access lock is on goes in
  `gateOpenPrefixes` or `gateOpenExact` (`internal/api/gate.go`). Everything
  else is locked by default.
- Schema changes go in a numbered migration step, see
  [Changing the schema](#changing-the-schema).
- Links built from server data use `safeHref` in the dashboard.
- Update [`llms.txt`](llms.txt) and [`llms-full.txt`](llms-full.txt) when the
  consumer API changes, then run `make llms`. That target also copies
  `README.md` (as the Overview page), the docs and `docs/screenshots/*.png`
  into the binary. A new doc must be added to the `llms` target, the
  Dockerfile, `scripts/build.ps1` and the page list in
  `server/internal/docsmd/docsmd.go`.
- Refresh `docs/screenshots/` from a seeded server, not from the e2e
  baselines, which carry mask bars over dynamic values.
