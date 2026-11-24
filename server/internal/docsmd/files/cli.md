# Command line (acn)

`acn` runs an Acnologia server from a terminal or a CI job. Sign in through
your browser once, then list services, add and check keys, watch activity,
and connect Claude to the server over MCP. It is one small binary with no
dependencies. The server binary carries the same commands as
`acnologia ctl`.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.sh | sh
```

Windows, in PowerShell (no administrator prompt):

```powershell
irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.ps1 | iex
```

With Go 1.26 or newer:

```sh
go install github.com/sidharthpunathil/acnologia/server/cmd/acn@latest
```

What the installers do:

- **`install.sh`** picks the build for your OS and CPU from the newest
  `cli-v*` GitHub release, checks it against the release's `SHA256SUMS`, and
  installs `acn` to `/usr/local/bin` when that is writable, otherwise to
  `~/.local/bin` (it prints a PATH hint if that is not on your PATH). It
  never calls `sudo`. `ACN_VERSION=cli-v0.1.0` pins a release and
  `ACN_INSTALL_DIR=/some/bin` picks the folder.
- **`install.ps1`** picks the amd64 or arm64 zip from the newest `cli-v*`
  release, checks it against `SHA256SUMS`, installs `acn.exe` to
  `%LocalAppData%\Programs\acn` and adds that folder to your user PATH (open
  a new terminal to pick it up). Set `$env:ACN_VERSION = "cli-v0.1.0"` to
  pin a release and `$env:ACN_INSTALL_DIR = "D:\tools\acn"` to pick the
  folder, before running the line above. If script execution is blocked,
  run it from `powershell -ExecutionPolicy Bypass`.

You can also download `acn_windows_amd64.zip`, `acn_windows_arm64.zip` or
the macOS and Linux builds from the
[releases](https://github.com/sidharthpunathil/acnologia/releases) yourself.
Each `cli-v*` release carries builds for macOS, Linux and Windows on amd64
and arm64, with a `SHA256SUMS` file. CI tests the client on all three
operating systems on every push, and checks that `install.ps1` parses in
Windows PowerShell 5.1 and PowerShell 7.

From a checkout, `make cli` builds `bin/acn`. On Windows without make,
`.\scripts\build.ps1 cli` builds `bin\acn.exe`.

## Sign in

```sh
acn login
```

1. `acn` asks for the server: `Acnologia URL (for example
   https://key.example.com):`. A bare host such as `key.example.com` means
   https. Plain http is accepted only for `localhost`, `*.localhost` and
   loopback addresses such as `127.0.0.1`. Skip the question with
   `--domain https://key.example.com`.
2. It checks the server (`GET /healthz` and `GET /api/gate`) and asks it for
   a one-time code, such as `WDJB-MJHT`.
3. It opens `https://key.example.com/cli/authorize?code=WDJB-MJHT` in your
   browser. On a machine without one (over ssh), or with `--no-browser`, it
   prints the link to open elsewhere.
4. In the browser, enter the site password if the server has one, and sign
   in. The page shows the code, the machine's name and OS, and when the
   request was made. Check that the code matches your terminal, pick the
   access (`read`, `write` or `admin`, never above your role), and press
   **Approve** or **Deny**.
5. The terminal prints `Signed in to key.example.com as you@example.com
   (write)`.

Approving creates an admin API token named `acn on <hostname>` for you, with
the scope you picked and a 90 day expiry. It shows up under Account, API
tokens, where you can revoke it, and in the audit log. The CLI never sees a
password, neither the site password nor yours: a valid admin token passes
the site lock on its own.

The code lasts 10 minutes. If it runs out, or you press Deny, run
`acn login` again.

Already have a token from the dashboard? Save it after a check:

```sh
acn login https://key.example.com --token acnadm_... --name prod
```

`--name` is the name the server is saved under (default: its host).

### Where the token is kept

Servers are saved per host in `ctl.json` under your user config folder:

| OS | Folder |
|---|---|
| Linux | `~/.config/acnologia/` |
| macOS | `~/Library/Application Support/acnologia/` |
| Windows | `%AppData%\acnologia\`, for example `C:\Users\you\AppData\Roaming\acnologia\ctl.json` |

The file is mode 0600 in a 0700 folder. Windows has no Unix modes, so there
the file inherits the ACL of your profile folder, which only you and
administrators can read. Saves are atomic (written beside the file, then
moved into place). `ACNOLOGIA_CTL_CONFIG` points it elsewhere.

You can be signed in to several servers. The last `acn login` becomes the
default:

```sh
acn contexts              # saved servers, * marks the default
acn use key.example.com   # switch the default
acn logout                # revoke this CLI's token on the server and forget it
acn logout localhost:8080 # the same for another saved server
```

## Everyday use

```sh
acn status                      # version, keys by state, in flight requests, last 24 hours
acn keys weather                # the keys of the weather service and their state
acn keys add weather keys.txt   # one key per line: secret, or label,secret
acn keys check weather          # health check every key now
acn events --kind rate_limited  # recent rate limits
acn breaker weather --reset     # close a tripped circuit breaker
```

`--json` prints the server's JSON instead of a table, for scripts:

```sh
acn services --json | jq -r '.services[] | select(.key_counts.available == 0) | .slug'
```

## Commands

| Command | What it does |
|---|---|
| `acn login [--domain <url>] [--no-browser]` | Sign in through the browser. See [Sign in](#sign-in). |
| `acn login <url> --token acnadm_... [--name <name>]` | Save a token you already have, after checking it. |
| `acn logout [host]` | Revoke this CLI's token on the server and forget the server. |
| `acn whoami` | The account and role the saved token acts as. |
| `acn contexts` | Saved servers; `*` marks the default. |
| `acn use <host>` | Switch the default server. |
| `acn status` | The server's version and a pool overview: services, keys by state, in flight requests, the last 24 hours. |
| `acn services` | Services with key counts and requests in the last 24 hours. |
| `acn keys <service>` | Keys of a service: label, hint, state, quota and notes. |
| `acn keys add <service> [file]` | Add keys, one per line: `secret` or `label,secret`. Reads stdin without a file. Duplicates are skipped. |
| `acn keys check <service>` | Health check every key of a service. |
| `acn events [--service s] [--limit n] [--kind k]` | Recent activity, 30 events by default, for example `--kind rate_limited`. |
| `acn breaker <service> [--reset]` | The service's circuit breaker, or close it. |
| `acn cache purge <service>` | Drop the service's cached responses. |
| `acn usage <app-id>` | An app's requests, spend and limits today and this month. |
| `acn apps` | Apps with their mode, whether they are on, token count and allowed services. |
| `acn apps create <name> [--mode proxy\|lease\|both] [--services a,b] [--description d]` | Create an app and print its first token, which is shown only this once. Without `--services` the app may use every service. |
| `acn tokens list --app <id\|name>` | An app's tokens: name, prefix, read only, last use, expiry. Never their values. |
| `acn tokens create --app <id\|name> [--name ci] [--read-only] [--expires 30]` | Add a token to an app and print it once. The app's other tokens keep working. `--expires` takes days (`30` or `30d`). |
| `acn api <METHOD> <path> [--data json\|@file\|@-]` | Call any route with the saved admin token and print the JSON answer. See [Any route, any tool](#any-route-any-tool). |
| `acn tool <name> [key=value ...] [--args json\|@file] [--yes]` | Run any control MCP tool. |
| `acn tool --list` | The control MCP tools your role can use, with their kind (read, write or destructive). |
| `acn proxy` | The [forward proxy](api.md#forward-proxy): its URL, the CA fingerprint and the hosts that get keys. |
| `acn proxy ca [--out file\|-] [--bundle]` | Save the CA certificate apps must trust (`acnologia-ca.pem` by default; `-` prints it). `--bundle` saves the system roots plus the CA instead (`acnologia-ca-bundle.pem`), for `SSL_CERT_FILE` and `REQUESTS_CA_BUNDLE`. |
| `acn proxy env [--token acn_...] [--ca file] [--bundle file]` | Print `export` lines (`HTTPS_PROXY`, `NO_PROXY`, `NODE_EXTRA_CA_CERTS` and the like) for an app to use the forward proxy, with a placeholder where the app token goes unless you pass `--token`. It saves the CA and a bundle of the system roots plus the CA next to the CLI config: `NODE_EXTRA_CA_CERTS` gets the CA alone, `SSL_CERT_FILE` and `REQUESTS_CA_BUNDLE` get the bundle, because they replace the whole trust store. The system roots come from `SSL_CERT_FILE` or the usual Linux and macOS bundle paths; on Windows, or when none is found, it sets `NODE_EXTRA_CA_CERTS` only and says so on stderr. |
| `acn mcp` | A stdio MCP server for Claude and other MCP clients. See [Connect Claude](#connect-claude). |
| `acn mcp config` | Print the snippet that adds `acn mcp` to Claude Code, Claude Desktop, Cursor or Codex. |
| `acn mcp install [--client claude-code\|cursor\|codex\|all] [--dry-run]` | Add `acn mcp` to the agents on this machine. See [Set up your agents in one step](#set-up-your-agents-in-one-step). |
| `acn version` | The client's version. |
| `acn help` | The list of commands. |

`--json` works anywhere on the line. It prints the server's JSON instead of
a table, and when a command fails it prints the error as JSON on stdout
(see [Exit codes](#exit-codes)).

What a command may do depends on the token's scope: `read` acts as a
member, `write` as an admin (add keys, reset breakers, purge caches) and
`admin` as a superadmin.

### Any route, any tool

`acn api` sends one request with the current server's admin token. The
path may carry a query string, and `--data` takes inline JSON, `@file` or
`@-` for stdin:

```sh
acn api GET /api/admin/checklist
acn api POST /api/admin/apps --data '{"name":"ci-bot","mode":"proxy"}'
acn api PUT /api/admin/ai/rules --data @rules.json
```

`acn tool` runs a [control MCP](api.md#control-mcp-mcpcontrol) tool through
`/mcp/control`, so every tool there is also a command. Arguments are
`key=value` pairs. A value that parses as JSON is sent as JSON (`limit=5`,
`read_only=true`, `keys='["k1","k2"]'`); anything else is sent as text. To
send a number as text, quote it: `name='"123"'`. `--args` takes a whole
JSON object, inline, from `@file` or from `@-`. Destructive tools only say
what they would do until you add `--yes`:

```sh
acn tool --list
acn tool list_keys service=weather summary=true
acn tool set_rotation services='["weather","weather-backup"]' rotation=fallback
acn tool delete_capability capability=web-search --yes
```

Every tool group is on for `acn tool`, so the result depends only on your
role. Results print as indented JSON.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | It worked. |
| `1` | Any other failure. |
| `2` | The command was used the wrong way: a missing argument, an unknown flag or command, data that is not JSON, or a tool input the server called invalid. |
| `3` | Not signed in, or the token may not do this (HTTP 401 or 403). |
| `4` | Not found (HTTP 404), such as an unknown service, app or key. |
| `5` | Rate limited or unavailable (HTTP 429 or 503). Retry later. |

With `--json` the error goes to stdout as one object, with every field the
server sent (`hint` and `fix` too, when it has them), the HTTP `status` and
the `exit_code`:

```json
{"error": {"code": "not_found", "message": "not found", "status": 404, "exit_code": 4}}
```

Without `--json` the message goes to stderr, followed by `hint: ...` when
the server gave one.

## Connect Claude

`acn mcp` is a stdio MCP server for Claude Code, Claude Desktop, Cursor and
any other MCP client. It bridges to the server's control MCP
(`/mcp/control`) with the saved token, so Claude can do what you can do in
the dashboard: add services and keys, check health, create apps and tokens,
read usage and events, build providers. It acts as you, with the token's
scope, and every call lands in the audit log. The other MCP, `/mcp` with an
app token, hands out keys to one app.

Sign in once, then add it to every agent on this machine:

```sh
acn login
acn mcp install
```

### Set up your agents in one step

`acn mcp install` finds Claude Code, Cursor and Codex on this machine (the
binary on your PATH, or its config folder) and adds the `acn mcp` entry to
each one:

| Agent | Where it goes |
|---|---|
| Claude Code | `claude mcp add --scope user acnologia -- /path/to/acn mcp` when `claude` is on your PATH, otherwise `mcpServers` in `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`). User scope, so every project sees it. |
| Cursor | `mcpServers` in `~/.cursor/mcp.json`. |
| Codex | `[mcp_servers.acnologia]` in `~/.codex/config.toml` (or `$CODEX_HOME/config.toml`). |

```sh
$ acn mcp install
Claude Code: added acnologia with claude mcp add --scope user acnologia -- /usr/local/bin/acn mcp
Cursor: backed up /home/me/.cursor/mcp.json to /home/me/.cursor/mcp.json.bak-20261125-101500
Cursor: updated acnologia in /home/me/.cursor/mcp.json
Codex: added acnologia in /home/me/.codex/config.toml
Restart your agent, or start a new session, to load the Acnologia tools.
```

- The entry runs `acn mcp`, which reads the token from your saved login, so
  no token is written into the agent's files.
- Other servers and settings in each file stay as they were. A file is
  copied to `<file>.bak-<time>` before it changes. If the entry already
  exists with other settings, its command and arguments are replaced and
  extra fields such as `env` are kept.
- Running it again changes nothing when the entry is already right.
- `--client cursor` sets up one agent even if it was not detected, and
  `--client all` (the default) sets up every agent it finds.
- `--dry-run` prints the commands it would run and the entries it would
  write, and touches nothing.
- `--name`, `--context` and the [tool filters](#choose-the-tools) work as
  for `acn mcp config`.
- A config file that is not valid JSON, or a Codex server written as a
  dotted key or an inline table, is left alone with a message asking you to
  edit it by hand.

### Print a snippet instead

`acn mcp config` prints the Claude Code line, with the absolute path of the
`acn` you ran:

```sh
$ acn mcp config
claude mcp add acnologia -- /usr/local/bin/acn mcp
```

For Claude Desktop (`claude_desktop_config.json`) or Cursor
(`~/.cursor/mcp.json`), print the `mcpServers` entry to merge into the file:

```sh
$ acn mcp config --client claude-desktop
{
  "mcpServers": {
    "acnologia": {
      "command": "/usr/local/bin/acn",
      "args": [
        "mcp"
      ]
    }
  }
}
```

`--client cursor` prints the same shape, `--client codex` the
`[mcp_servers.acnologia]` table for `~/.codex/config.toml`, and
`--client json` just the inner server object. `--name` changes the name the client shows (default
`acnologia`), and `--context staging` pins a saved server other than the
default (the snippet then runs `acn mcp --context staging`).

### Choose the tools

By default Claude sees every tool group except `admin`, `secrets` and `raw`,
minus the tools your token's scope cannot use. These flags work on both
`acn mcp` and `acn mcp config`:

| Flag | What it does |
|---|---|
| `--groups services,keys` | Only these groups: `overview`, `power` (search, batch and routes), `call`, `services`, `keys`, `apps`, `providers`, `ai`, `routing`, `sharing`, `refresh`, `admin`, `secrets`, `raw`, or `all`. |
| `--exclude delete_service,api_request` | Drop tools by name. |
| `--read-only` | List only read tools; the server refuses the rest. |
| `--confirm off` | Skip the `confirm: true` step on destructive tools, for clients that ask you before each call anyway. Default `on`. |

```sh
$ acn mcp config --groups services,keys --read-only
claude mcp add acnologia -- /usr/local/bin/acn mcp --groups services,keys --read-only
```

The bridge sends them as the `X-Acnologia-MCP-Groups`,
`X-Acnologia-MCP-Exclude`, `X-Acnologia-MCP-Read-Only` and
`X-Acnologia-MCP-Confirm` headers.

### Over HTTP, without acn

A client can also talk to `/mcp/control` directly with an admin token in a
header. `--http` prints that form, with the filters as query parameters:

```sh
$ acn mcp config --http --groups services,keys
claude mcp add --transport http acnologia 'https://key.example.com/mcp/control?groups=services,keys' --header "Authorization: Bearer <your acnadm_ token>"
```

The token is left out unless you pass `--show-token`, so a snippet pasted
into a chat or a ticket leaks nothing; put in a token from Account, API
tokens. With `--client claude-desktop`, `cursor` or `json` it prints
`{"type": "http", "url": ..., "headers": {"Authorization": ...}}`. Claude
Desktop may only run local commands from its config file, so the stdio form
is the safer choice there.

### When it fails

If the token expired or was revoked, Claude shows `Acnologia refused the
token (401)` with a hint to run `acn login` again, and `acn mcp` prints the
same on stderr. Without a saved server, `acn mcp` exits with `not logged
in; run acn login first`. `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` work here as
for every other command. An app token (`acn_...`) is refused: the control
MCP needs an admin token.

The server binary has the same bridge without a saved login: `acnologia mcp
--control --url https://key.example.com --token acnadm_...` (an `acnadm_`
token implies `--control`), with the same filter flags. With an app token,
`acnologia mcp --token acn_...` bridges to the app MCP at `/mcp` instead.

## CI and scripts

Set two variables instead of signing in. Together they override any saved
server for that command:

```sh
export ACNOLOGIA_URL=https://key.example.com
export ACNOLOGIA_TOKEN=acnadm_...   # from Account, API tokens, or acn login on your machine
acn keys add weather < new-keys.txt
acn keys check weather
```

PowerShell has no `<` redirection, so set the variables with `$env:` and
pass the file by name (a `Get-Content` pipe works too). Files saved on
Windows with CRLF line endings or a byte order mark read the same:

```powershell
$env:ACNOLOGIA_URL = "https://key.example.com"
$env:ACNOLOGIA_TOKEN = "acnadm_..."
acn keys add weather .\new-keys.txt
acn keys check weather
```

### A fresh server with no browser

A CI job or an agent can set up a new server without anyone signing in.
Make a token yourself and start the server with it and the first user:

```sh
export ACNOLOGIA_BOOTSTRAP_EMAIL=ci@example.com
export ACNOLOGIA_BOOTSTRAP_PASSWORD='a long password'
export ACNOLOGIA_BOOTSTRAP_TOKEN=acnadm_$(openssl rand -hex 24)
acnologia
```

On first boot the server creates that superadmin and stores the token's
hash as their admin API token (scope `admin`, named `bootstrap`). Then:

```sh
export ACNOLOGIA_URL=http://localhost:8080
export ACNOLOGIA_TOKEN=$ACNOLOGIA_BOOTSTRAP_TOKEN
acn api GET /api/admin/checklist
acn tool quickstart provider=serpapi keys='["your-key"]' --yes
```

`quickstart` creates the service, adds the keys, creates an app with a
token and prints the app's MCP config. The checklist lists the four
Getting started steps with `done` and the next tool, command and dashboard
URL for each. The token works only on a server that has no users yet; on
any other the server ignores it and logs a notice. It is never logged.
Revoke it in Account, API tokens once the job is done.

A valid admin token passes the site lock by itself, so CI needs no site
password. If `ACNOLOGIA_ACCESS_PASSWORD` is set in the environment, it is
still sent as `X-Acnologia-Access` (for older servers), but only to the
server in `ACNOLOGIA_ACCESS_URL`, or else `ACNOLOGIA_URL`.

## How the sign in works

The flow follows the OAuth device authorization grant
([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)):

| Call | Gate | Purpose |
|---|---|---|
| `POST /api/cli/device` `{client, hostname, os}` | open | Returns `{device_code, user_code, verification_uri, verification_uri_complete, expires_in: 600, interval: 5}`. |
| `POST /api/cli/device/token` `{device_code}` | open | The CLI's poll: `400` with code `authorization_pending`, `slow_down` (with a longer `interval`), `access_denied` or `expired_token`, or `200 {token, token_id, user, scope, expires_at}` once. |
| `GET /api/cli/device/lookup?user_code=` | locked, session | What the authorize page shows. |
| `POST /api/cli/device/approve` `{user_code, scope, label}` | locked, session | Creates the token. |
| `POST /api/cli/device/deny` `{user_code}` | locked, session | Refuses the request. |

- The device code is 32 random bytes; the server keeps only its SHA-256.
  Pending requests live in memory for 10 minutes, so a restart drops them.
- The user code is 8 letters from an alphabet without vowels or look alike
  characters (20^8 combinations).
- Limits are per client address under a global ceiling, so one source
  cannot block everyone's sign in:
  - New requests: 5 a minute and 50 a day from one address, 30 a minute
    from all, and at most 10 waiting at once from one address.
  - Polls with an unknown device code: 60 a minute from one address, 600
    from all. A poll for a live request never counts; one sooner than the
    interval gets `slow_down` and a longer interval.
  - Wrong user codes: 10 a minute and 200 a day per signed in user, after
    which that user's lookups are refused unseen. Other users' misses never
    stop a right code.
- Approve and deny need a dashboard session past the site lock. An API
  token cannot approve a sign in, so one token cannot mint another.
- The CLI opens `verification_uri_complete` only when it is http or https
  on the host you typed. Any other link is not opened; the CLI prints
  `<server>/cli/authorize?code=...` for you to open instead.
- `acn logout` calls `DELETE /api/admin/tokens/{id}` with the token itself;
  a token may revoke itself and no other token.
