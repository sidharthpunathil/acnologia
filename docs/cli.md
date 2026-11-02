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

Windows: download `acn_windows_amd64.zip` from the
[releases](https://github.com/sidharthpunathil/acnologia/releases), unzip
`acn.exe` and put it on your PATH. From a checkout, `make cli` builds
`bin/acn`.

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
on macOS, `%AppData%\acnologia\` on Windows), with the file at mode 0600 in
a 0700 folder. `ACNOLOGIA_CTL_CONFIG` points it elsewhere. You can be signed
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
| `acn version` | The client's version. |

`--json` prints the server's JSON instead of a table for `whoami`, `status`,
`services`, `keys`, `events` and `usage`:

```sh
acn services --json | jq -r '.services[] | select(.key_counts.available == 0) | .slug'
```

What a command may do depends on the token's scope: `read` acts as a
member, `write` as an admin (add keys, reset breakers, purge caches) and
`admin` as a superadmin.

## CI and scripts

Set two variables instead of signing in; they override any saved server for
that command:

```sh
export ACNOLOGIA_URL=https://key.sidharth.co
export ACNOLOGIA_TOKEN=acnadm_...   # from Account, API tokens, or acn login on your machine
acn keys add serpapi < new-keys.txt
acn keys check serpapi
```

A valid admin token passes the site lock by itself, so CI needs no site
password. (`ACNOLOGIA_ACCESS_PASSWORD` in the environment is still sent as
`X-Acnologia-Access` if set, for older servers.)

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
- `acn logout` calls `DELETE /api/admin/tokens/{id}` with the token itself;
  a token may revoke itself and no other token.
