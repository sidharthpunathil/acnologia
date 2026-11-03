# Command line (acn)

`acn` is the Acnologia command line client: sign in to a server through your
browser once, then list services, add and check keys, watch activity and
manage the pool from a terminal or a CI job. It is a single small binary with
no dependencies, and the same commands are built into the server as
`acnologia ctl`.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.sh | sh
```

The script picks the build for your OS and CPU from the newest `cli-v*`
GitHub release, checks it against the release's `SHA256SUMS`, and installs
`acn` to `/usr/local/bin` when that is writable, otherwise to `~/.local/bin`
(it prints a PATH hint if that is not on your PATH). It never calls `sudo`.
`ACN_VERSION=cli-v0.1.0` pins a release and `ACN_INSTALL_DIR=/some/bin`
picks the folder.

With Go 1.26 or newer:

```sh
go install github.com/sidharthpunathil/acnologia/server/cmd/acn@latest
```

Windows, in PowerShell (no administrator prompt):

```powershell
irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.ps1 | iex
```

It picks the amd64 or arm64 zip from the newest `cli-v*` release, checks it
against `SHA256SUMS`, installs `acn.exe` to
`%LocalAppData%\Programs\acn` and adds that folder to your user PATH (open a
new terminal to pick it up). `$env:ACN_VERSION = "cli-v0.1.0"` pins a
release and `$env:ACN_INSTALL_DIR = "D:\tools\acn"` picks the folder; set
them before running the line above. If script execution is blocked, run it
from `powershell -ExecutionPolicy Bypass`. You can also download
`acn_windows_amd64.zip` or `acn_windows_arm64.zip` from the
[releases](https://github.com/sidharthpunathil/acnologia/releases) yourself.

From a checkout, `make cli` builds `bin/acn`; on Windows without make,
`.\scripts\build.ps1 cli` builds `bin\acn.exe`.

Each `cli-v*` release carries builds for macOS, Linux and Windows on amd64
and arm64, with a `SHA256SUMS` file. CI tests the client on all three
operating systems on every push, and checks that `install.ps1` parses in
Windows PowerShell 5.1 and PowerShell 7.

## Sign in

```sh
acn login
```

1. `acn` asks for the server: `Acnologia URL (for example
   https://key.sidharth.co):`. A bare host such as `key.sidharth.co` means
   https. Plain http is accepted only for `localhost` and `127.0.0.1`. Skip
   the question with `--domain https://key.sidharth.co`.
2. It checks the server (`GET /healthz` and `GET /api/gate`) and asks it for
   a one-time code, such as `WDJB-MJHT`.
3. It opens `https://key.sidharth.co/cli/authorize?code=WDJB-MJHT` in your
   browser. On a machine without one (over ssh), or with `--no-browser`, it
   prints the link to open elsewhere.
4. In the browser, unlock the site password if the server has one, and sign
   in. The page shows the code, the machine's name and OS and when the
   request was made. Check the code matches your terminal, pick the access
   (`read`, `write` or `admin`, never above your role) and press
   **Approve**, or **Deny**.
5. The terminal prints `Signed in to key.sidharth.co as you@example.com
   (write)`.

Approving creates an admin API token named `acn on <hostname>` for you, with
the scope you picked and a 90 day expiry. It shows up under Account, API
tokens, where you can revoke it, and in the audit log. The CLI never sees a
password, neither the site password nor yours: a valid admin token passes the
site lock on its own.

The code lasts 10 minutes. If it runs out, or you press Deny, run
`acn login` again.

Already have a token from the dashboard? `acn login https://key.sidharth.co
--token acnadm_...` saves it after checking it.

### Where the token is kept

Servers are saved per host in `ctl.json` under your user config folder
(`~/.config/acnologia/` on Linux, `~/Library/Application Support/acnologia/`
on macOS, `%AppData%\acnologia\` on Windows, for example
`C:\Users\you\AppData\Roaming\acnologia\ctl.json`), with the file at mode
0600 in a 0700 folder. Windows has no Unix modes: the file inherits the
ACL of your profile folder, which only you and administrators can read.
Saves are atomic (written beside the file, then moved into place).
`ACNOLOGIA_CTL_CONFIG` points it elsewhere. You can be signed
in to several servers; the last `acn login` is the default.

```sh
acn contexts              # saved servers, * marks the default
acn use key.sidharth.co   # switch the default
acn logout                # revoke this CLI's token on the server and forget it
acn logout localhost:8080 # the same for another saved server
```

## Commands

| Command | What it does |
|---|---|
| `acn whoami` | The account and role the saved token acts as. |
| `acn status` | The server's version and a pool overview: services, keys by state, in flight requests, the last 24 hours. |
| `acn services` | Services with key counts and requests in the last 24 hours. |
| `acn keys <service>` | Keys of a service: label, hint, state, quota and notes. |
| `acn keys add <service> [file]` | Add keys, one per line: `secret` or `label,secret`. Reads stdin without a file. Duplicates are skipped. |
| `acn keys check <service>` | Health check every key of a service. |
| `acn events [--service s] [--limit n] [--kind k]` | Recent activity, for example `--kind rate_limited`. |
| `acn breaker <service> [--reset]` | The service's circuit breaker, or close it. |
| `acn cache purge <service>` | Drop the service's cached responses. |
| `acn usage <app-id>` | An app's requests, spend and limits today and this month. |
| `acn mcp` | A stdio MCP server for Claude and other MCP clients; see [Connect Claude](#connect-claude). |
| `acn mcp config` | Print the snippet that adds `acn mcp` to Claude Code, Claude Desktop or Cursor. |
| `acn version` | The client's version. |

`--json` prints the server's JSON instead of a table for `whoami`, `status`,
`services`, `keys`, `events` and `usage`:

```sh
acn services --json | jq -r '.services[] | select(.key_counts.available == 0) | .slug'
```

What a command may do depends on the token's scope: `read` acts as a
member, `write` as an admin (add keys, reset breakers, purge caches) and
`admin` as a superadmin.

## Connect Claude

`acn mcp` is a stdio MCP server for Claude Code, Claude Desktop, Cursor and
any other MCP client. It bridges to the server's control MCP
(`/mcp/control`) with the saved token, so Claude can do what you can do in
the dashboard: add services and keys, check health, create apps and tokens,
read usage and events, build providers. It acts as you, with the token's
scope, and every call lands in the audit log. (`/mcp` with an app token is
the other MCP: it hands out keys to one app.)

Sign in once, then add it to Claude Code:

```sh
acn login
claude mcp add acnologia -- "$(command -v acn)" mcp
```

`acn mcp config` prints that line for you, with the absolute path of the
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

`--client cursor` prints the same shape and `--client json` just the inner
server object. `--name` changes the name the client shows (default
`acnologia`), and `--context staging` pins a saved server other than the
default one (the snippet then runs `acn mcp --context staging`).

### Choose the tools

By default Claude sees every tool group except `admin`, `secrets` and `raw`,
minus the tools your token's scope cannot use. These flags work on both
`acn mcp` and `acn mcp config`:

| Flag | What it does |
|---|---|
| `--groups services,keys` | Only these groups: `overview`, `services`, `keys`, `apps`, `providers`, `ai`, `routing`, `sharing`, `refresh`, `admin`, `secrets`, `raw`, or `all`. |
| `--exclude delete_service,api_request` | Drop tools by name. |
| `--read-only` | List only read tools; the server refuses the rest. |
| `--confirm off` | Skip the `confirm: true` step on destructive tools, for clients that ask you before each call anyway. Default `on`. |

```sh
$ acn mcp config --groups services,keys --read-only
claude mcp add acnologia -- /usr/local/bin/acn mcp --groups services,keys --read-only
```

The bridge sends them as `X-Acnologia-MCP-Groups`, `X-Acnologia-MCP-Exclude`,
`X-Acnologia-MCP-Read-Only` and `X-Acnologia-MCP-Confirm` headers.

### Over HTTP, without acn

A client can also talk to `/mcp/control` directly with an admin token in a
header. `--http` prints that form, with the filters as query parameters:

```sh
$ acn mcp config --http --groups services,keys
claude mcp add --transport http acnologia 'https://key.sidharth.co/mcp/control?groups=services,keys' --header "Authorization: Bearer <your acnadm_ token>"
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
same on stderr. Without a saved server, `acn mcp` exits with `not logged in;
run acn login first`. `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` work here as
for every other command.

The server binary has the same bridge without a saved login: `acnologia mcp
--control --url https://key.sidharth.co --token acnadm_...` (an `acnadm_`
token implies `--control`), with the same filter flags.

## CI and scripts

Set two variables instead of signing in; they override any saved server for
that command:

```sh
export ACNOLOGIA_URL=https://key.sidharth.co
export ACNOLOGIA_TOKEN=acnadm_...   # from Account, API tokens, or acn login on your machine
acn keys add serpapi < new-keys.txt
acn keys check serpapi
```

In PowerShell, set them with `$env:` and pass the file by name, since
PowerShell has no `<` redirection (a `Get-Content` pipe works too). Files
saved on Windows with CRLF line endings or a byte order mark read the same:

```powershell
$env:ACNOLOGIA_URL = "https://key.sidharth.co"
$env:ACNOLOGIA_TOKEN = "acnadm_..."
acn keys add serpapi .\new-keys.txt
acn keys check serpapi
```

A valid admin token passes the site lock by itself, so CI needs no site
password. (`ACNOLOGIA_ACCESS_PASSWORD` in the environment is still sent as
`X-Acnologia-Access` if set, for older servers, but only to the server in
`ACNOLOGIA_ACCESS_URL`, or else `ACNOLOGIA_URL`.)

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
- Limits are global, like the site lock's: 30 new requests a minute, 600
  polls a minute, and 10 wrong user codes a minute (200 a day), after which
  lookups are refused unseen. A poll sooner than the interval gets
  `slow_down` and a longer interval.
- Approve and deny need a dashboard session past the site lock. An API
  token cannot approve a sign in, so one token cannot mint another.
- The CLI opens `verification_uri_complete` only when it is http or https
  on the host you typed. Any other link is not opened; the CLI prints
  `<server>/cli/authorize?code=...` for you to open instead.
- `acn logout` calls `DELETE /api/admin/tokens/{id}` with the token itself;
  a token may revoke itself and no other token.
