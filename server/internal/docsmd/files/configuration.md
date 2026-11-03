# Configuration

Acnologia reads environment variables on start. It also loads a `.env` file from
the working directory if there is one; variables already set in the
environment win over the file. [`.env.example`](../.env.example) lists them all.

## Settings

| Variable | Default | What it does |
|---|---|---|
| `ACNOLOGIA_ADDR` | `:8080` | Listen address. When unset and the host sets `PORT` (Railway, Render, Fly), the server listens on `0.0.0.0:$PORT`. |
| `ACNOLOGIA_DATA_DIR` | `./data`; `%LocalAppData%\Acnologia` on Windows | Holds `acnologia.db` (plus WAL files) and, when no env key is set, `master.key`. Created with mode 0700 (on Windows it relies on the folder's ACL instead, see [Windows](#windows)). |
| `ACNOLOGIA_MASTER_KEY` | empty | 32 bytes, base64 or hex, that encrypt stored keys. Generate with `acnologia gen-master-key`. When empty, `data/master.key` is used, or created on first boot. |
| `ACNOLOGIA_PUBLIC_URL` | empty | Base URL for links the server writes: the MCP endpoint shown on the Apps page and the `acn login` approval link. It is also the URL this server announces to other servers: in `/.well-known/acnologia` and in every share invite it makes. When empty, the URL a superadmin saved on the Sharing page is used for sharing, else the request host. Set it on any server that shares, so invites never carry an internal address. |
| `ACNOLOGIA_SECURE_COOKIES` | `false` | Mark the session cookie `Secure`. Set `true` behind HTTPS. It is also set automatically for direct TLS. |
| `ACNOLOGIA_TRUST_PROXY_HEADERS` | `false` | Take the client IP from `X-Forwarded-For` / `X-Real-IP`. Enable only behind a proxy that overwrites them; otherwise clients could spoof their address to dodge the login limiter. |
| `ACNOLOGIA_ACCESS_PASSWORD` | empty | Locks the whole site behind one shared password, in front of the user accounts. At least 12 characters. Unset, nothing changes. See [Access password](#access-password). |
| `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD` | `false` | When `true`, a public deploy can never come up unlocked: a set password must be at least 12 characters, and a missing one is generated into `access-password` in the data dir. |
| `ACNOLOGIA_ACCESS_SIGNS_IN` | `true` | With an access password, that password is the whole login: unlocking signs the browser in as the owner (the first superadmin), with no email form, and signing out locks it again. Set `false` to keep separate accounts behind the lock. |
| `ACNOLOGIA_BOOTSTRAP_EMAIL`, `ACNOLOGIA_BOOTSTRAP_PASSWORD` | empty | Create the first superadmin on boot instead of the setup screen. Used only while there are no users. |
| `ACNOLOGIA_HEALTH_INTERVAL` | `6h` | How often each key is re-checked with the provider. `0` or `off` disables background checks. Exhausted keys are re-checked hourly. |
| `ACNOLOGIA_EVENT_RETENTION_DAYS` | `30` | How long request events are kept. `0` or less keeps them forever. |
| `ACNOLOGIA_MAX_BODY_MB` | `50` | Largest proxied request body. |
| `ACNOLOGIA_CACHE_MB` | `64` | Memory for cached GET responses and idempotent replays. `0` turns caching off. |
| `ACNOLOGIA_WEBHOOK_ALLOW_PRIVATE` | `false` | Let webhooks reach private, loopback and link-local addresses, for a receiver on your own network. |
| `ACNOLOGIA_EGRESS_ECHO_URL` | `https://www.cloudflare.com/cdn-cgi/trace` | URL the egress proxy test calls to learn the proxy's public IP. |
| `ANTHROPIC_API_KEY` | empty | Enables the AI features: the provider builder, "Fill with AI" and the Assistant. Everything else works without it. |
| `ACNOLOGIA_AI_MODEL` | `claude-opus-5-5` | Fallback model for AI features. The model a superadmin picks under Settings, AI (a default and optional per feature models for the Assistant, the provider builder, Fill with AI, chat summaries and offer refresh) wins over it and needs no restart. With neither set, Acnologia uses `claude-opus-5-5`. |
| `ACNOLOGIA_CHAT_COMPACT_TOKENS` | `0` | Prompt size, in tokens, at which an Assistant chat summarizes its older messages. `0` means 60% of the Assistant's model's context window (600,000 tokens for current models, 120,000 for 200K models), so it moves when the model changes. |
| `GITHUB_TOKEN` | empty | Lets the AI builder read GitHub with a higher rate limit. Use a fine grained, public read only token. |

The command line tools, the installers and one test read a few more. The
server ignores them.

| Variable | Default | What it does |
|---|---|---|
| `ACNOLOGIA_URL`, `ACNOLOGIA_TOKEN` | empty | `acn` and `acnologia ctl`: use this server and admin token for one command instead of the saved one (for CI). `acnologia mcp` reads them too, as the base URL (default `http://localhost:8080`) and the app token. See [Command line](cli.md). |
| `ACNOLOGIA_CTL_CONFIG` | `acnologia/ctl.json` in the user config folder | `acn`: where the saved servers live. |
| `ACNOLOGIA_ACCESS_PASSWORD` | empty | `acn` and `acnologia ctl` send it as `X-Acnologia-Access` on every call. Rarely needed, since an admin token passes the lock on its own. |
| `ACN_VERSION`, `ACN_INSTALL_DIR` | newest `cli-v*` release; `/usr/local/bin` or `~/.local/bin` (`%LocalAppData%\Programs\acn` on Windows) | `install.sh` and `install.ps1`: pin a release and pick the install folder. |
| `ACNOLOGIA_LIVE_PROVIDERS` | empty | Set to `1` to let `go test ./internal/providers` call the real LLM provider APIs with no key (see [providers](providers.md)). |

## Per service settings

Set in the dashboard (service settings) or through `PATCH
/api/admin/services/{id}`. New services take their defaults from the provider
spec.

| Field | Range | Meaning |
|---|---|---|
| `base_url` | http(s) URL | Where the proxy and health checks send requests. Changing it as a non-superadmin pauses hidden keys (see [security](security.md#hidden-keys-admins-operate-but-never-read-them)). |
| `auth_type`, `auth_name` | `query`, `header`, `bearer` | Where the key goes: query parameter, header, or `Authorization: Bearer`. |
| `strategy` | `round_robin`, `fill_first`, `least_used`, `most_quota`, `random` | How the next key is chosen. |
| `cooldown_sec` | 1 to 86400 | Base cooldown after a rate limit, doubled per consecutive failure, capped at one hour. |
| `max_attempts` | 1 to 20 | Keys the proxy tries before giving up. |
| `max_concurrency` | 0 to 1000 | Default in-flight requests per key, 0 is unlimited. Keys can override it. |
| `queue_timeout_sec` | 0 to 300 | How long a proxied request waits for a free slot. |

## Running

```sh
# from source
make build
ACNOLOGIA_DATA_DIR=./data ./bin/acnologia

# Docker
cp .env.example .env
docker compose build
docker run --rm acnologia:latest gen-master-key   # paste into ACNOLOGIA_MASTER_KEY in .env
docker compose up -d
```

Open http://localhost:8080; the first visit creates the superadmin. The
container runs as a nonroot user and keeps its data in `/data` (Compose mounts
a named volume there; the image declares none, so hosts such as Railway can
mount their own). If
`ACNOLOGIA_MASTER_KEY` is unset, a key is generated into `/data/master.key` on
first boot.

Put it behind TLS (Caddy, Traefik, Cloudflare Tunnel) and set
`ACNOLOGIA_SECURE_COOKIES=true`. Behind a reverse proxy also set
`ACNOLOGIA_TRUST_PROXY_HEADERS=true`, or every client shares one login rate
limit.

Acnologia keeps itself out of search results: every response carries
`X-Robots-Tag: noindex, nofollow, noarchive, nosnippet`, `/robots.txt`
disallows everything, the page carries a robots meta tag, and pages cannot be
framed. This holds whether or not the access password is set.

## Windows

The server is a single `acnologia.exe` with no runtime dependencies (the
SQLite driver is pure Go). Download `acnologia_windows_amd64.zip` or
`acnologia_windows_arm64.zip` from the
[releases](https://github.com/sidharthpunathil/acnologia/releases), or build
it with `.\scripts\build.ps1` (see [development](development.md#windows)).
Docker Desktop runs the Linux image as on any other host.

**Data dir.** Without `ACNOLOGIA_DATA_DIR`, the data lives in
`%LocalAppData%\Acnologia` (for example
`C:\Users\you\AppData\Local\Acnologia`), not in `.\data`, because a service
or a Scheduled Task starts in `C:\Windows\System32`. Windows ignores Unix
file modes, so the 0700 and 0600 the server asks for do nothing there: the
database, `master.key` and `access-password` are protected by the folder's
ACL. Keep the data dir inside the profile of the account that runs the
server (its `AppData` is readable only by that account, SYSTEM and
administrators), or lock a custom folder down yourself:

```powershell
icacls D:\acnologia /inheritance:r /grant:r "${env:USERNAME}:(OI)(CI)F" "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F"
```

Paths take backslashes as they are, in the environment or in `.env`, with
or without quotes: `ACNOLOGIA_DATA_DIR=D:\acnologia\data`. A `.env`,
`access-password` or `master.key` saved by Notepad (CRLF line endings, or a
byte order mark) reads the same as one written on Linux. Set variables for
one PowerShell session with `$env:ACNOLOGIA_ADDR = "127.0.0.1:8080"`, or for
your account with `setx ACNOLOGIA_ADDR 127.0.0.1:8080`.

**Stopping.** Ctrl+C stops the server cleanly: it gives in flight requests
up to 15 seconds and closes the database. Closing the console window,
signing out and shutting down start the same shutdown, but Windows ends the
process after about 5 seconds.

**Running in the background.** `acnologia.exe` is a console program, not a
native Windows service, so `sc.exe create` on its own fails with error 1053.
Use one of these:

- **NSSM** ([nssm.cc](https://nssm.cc)) wraps it as a service and stops it
  with Ctrl+C:

  ```powershell
  nssm install Acnologia C:\Acnologia\acnologia.exe
  nssm set Acnologia AppDirectory C:\Acnologia
  nssm set Acnologia AppEnvironmentExtra ACNOLOGIA_DATA_DIR=C:\ProgramData\Acnologia ACNOLOGIA_ADDR=127.0.0.1:8080
  nssm start Acnologia
  ```

  A service runs as LocalSystem by default, whose `%LocalAppData%` is under
  `C:\Windows\System32\config\systemprofile`, so set `ACNOLOGIA_DATA_DIR`
  explicitly and restrict that folder with `icacls` as above (or run the
  service as a dedicated user with `nssm set Acnologia ObjectName`).

- **A Scheduled Task** that starts at logon, as your own user, with no extra
  tools:

  ```powershell
  $action = New-ScheduledTaskAction -Execute "C:\Acnologia\acnologia.exe" -WorkingDirectory "C:\Acnologia"
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
  $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit 0 -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
  Register-ScheduledTask -TaskName Acnologia -Action $action -Trigger $trigger -Settings $settings
  ```

  Put settings in `C:\Acnologia\.env` (it is read from the working
  directory). The data goes to your `%LocalAppData%\Acnologia`.

- **sc.exe** works only through a wrapper that speaks the service protocol,
  such as NSSM or WinSW; point `sc.exe create ... binPath=` at the wrapper,
  not at `acnologia.exe`.

Allow the port through Windows Defender Firewall only if other machines
need to reach it; for local use bind to `127.0.0.1:8080`.

## Access password

An instance reachable from the internet should set `ACNOLOGIA_ACCESS_PASSWORD`
(and `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD=true`). Every visitor then sees a lock
screen before setup, sign in or any data. By default that password is the
whole login: unlocking signs you in as the owner. Set
`ACNOLOGIA_ACCESS_SIGNS_IN=false` to keep separate user accounts behind it, so
each person still signs in with an email and password after unlocking.

- **Locked:** the dashboard API (`/api/*`, including `/api/auth/*` setup and
  sign in), the docs markdown and images (`/docs/*.md`, `/docs/assets/*`),
  `/llms.txt`, `/llms-full.txt`, and any request that is not a GET or HEAD.
  A locked API call gets `401 {"error":{"code":"locked"}}`; a page navigation
  gets the app, which shows the lock screen.
- **Open:** the app shell and its static files (they carry no data), `/healthz`,
  `/robots.txt`, `/api/gate*`, `/.well-known/acnologia` and the server to
  server routes under `/api/peer/v1/` (they need an invite or a share token,
  see [sharing](api.md#sharing-between-servers)), the CLI sign in's `POST /api/cli/device` and
  `POST /api/cli/device/token` (useless until a signed in person approves),
  and the consumer routes `/proxy/*`, `/api/v1/*` and `/mcp`, which need an
  app token of their own. Apps and MCP clients keep working unchanged.
- **Admin API tokens:** a request with a valid `Authorization: Bearer
  acnadm_...` passes the lock without the password, so `acn` and CI need
  only their token. A wrong, expired or revoked token does not, and an app
  token (`acn_`) never opens a dashboard route.
- **Browsers:** the lock screen posts the password to `POST /api/gate/unlock`
  and gets an `acnologia_gate` cookie (HttpOnly, SameSite=Lax, Secure under
  `ACNOLOGIA_SECURE_COOKIES` or TLS) that lasts 30 days. It holds an issue time
  and an HMAC keyed by the master key and the password, never the password.
  Changing the password signs every browser out. "Lock this browser" in the
  account menu calls `POST /api/gate/lock`. `GET /api/gate` answers
  `{"locked": bool, "required": bool, "password_only": bool}`.
- **Wrong guesses:** 5 a minute and 50 a day across all clients, then `429`
  with `retry_after_sec`. The limit is global, not per IP, because a client
  behind a proxy can claim any `X-Forwarded-For`.
- **Scripts:** send the password in the `X-Acnologia-Access` header on every
  dashboard call (it is compared in constant time and never rate limited):

  ```sh
  curl -H "X-Acnologia-Access: $ACNOLOGIA_ACCESS_PASSWORD" \
       -H "Authorization: Bearer acnadm_..." https://key.example.com/api/admin/services
  ```

  `acnologia ctl` sends it for you when `ACNOLOGIA_ACCESS_PASSWORD` is set in
  its environment. The proxy strips the header, so it never reaches a provider.

The password and the gate cookie are never logged and are scrubbed by the
redactor if they turn up in a log line, event or prompt.

## Backups

With Docker Compose, back up the data volume. Otherwise back up `acnologia.db` (with its `-wal` file, or after a clean shutdown) **and**
the master key, separately. Without the master key the stored keys cannot be
decrypted, and Acnologia refuses to start against a database with a different key.

## Upgrading

Migrations run automatically on start. Notable behavior changes:

- **Hidden keys now need a grant.** Existing apps cannot lease or proxy hidden
  keys until a superadmin turns on **Lease hidden keys** for them. Existing
  services keep their trust; services an admin edits afterwards pause hidden
  keys until a superadmin confirms them.
- **Behind a proxy**, set `ACNOLOGIA_TRUST_PROXY_HEADERS=true`. The server logs a
  warning when it sees `X-Forwarded-For` while the setting is off.

## Upgrading from Cogito

Acnologia used to be called Cogito. An existing install keeps working without
changes:

- `COGITO_*` environment variables are read when the `ACNOLOGIA_*` name is
  not set, with a warning at startup.
- An existing `cogito.db` in the data directory is opened as is, and the
  Compose file keeps the `cogito-data` volume name so it finds your data.
- App tokens starting with `cog_` and admin tokens starting with `cogadm_`
  keep working. New tokens start with `acn_` and `acnadm_`.
- Requests may still send `X-Cogito-Token` or `?cogito_token=`. Responses and
  webhooks send each `X-Acnologia-*` header together with its `X-Cogito-*`
  twin for now.
- The `cogito_session` cookie, the `~/.config/cogito` ctl config and MCP
  resource URIs under `cogito://` are still accepted.
