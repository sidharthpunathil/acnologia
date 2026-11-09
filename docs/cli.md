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
| `acn proxy` | The [forward proxy](api.md#forward-proxy): its URL, the CA fingerprint and the hosts that get keys. |
| `acn proxy ca [--out file\|-]` | Save the CA certificate apps must trust (`acnologia-ca.pem` by default; `-` prints it). |
| `acn proxy env [--token acn_...] [--ca file]` | Print `export` lines (`HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS` and the like) for an app to use the forward proxy, with a placeholder where the app token goes unless you pass `--token`. |
| `acn mcp` | A stdio MCP server for Claude and other MCP clients. See [Connect Claude](#connect-claude). |
| `acn mcp config` | Print the snippet that adds `acn mcp` to Claude Code, Claude Desktop or Cursor. |
| `acn version` | The client's version. |
| `acn help` | The list of commands. |

`--json` works anywhere on the line for `whoami`, `status`, `services`,
`keys`, `events` and `usage`.

What a command may do depends on the token's scope: `read` acts as a
member, `write` as an admin (add keys, reset breakers, purge caches) and
`admin` as a superadmin.

## Connect Claude

`acn mcp` is a stdio MCP server for Claude Code, Claude Desktop, Cursor and
any other MCP client. It bridges to the server's control MCP
(`/mcp/control`) with the saved token, so Claude can do what you can do in
the dashboard: add services and keys, check health, create apps and tokens,
read usage and events, build providers. It acts as you, with the token's
scope, and every call lands in the audit log. The other MCP, `/mcp` with an
app token, hands out keys to one app.

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
