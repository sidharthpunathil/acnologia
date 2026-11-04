# Deploying on Railway

A public Acnologia deploy on Railway, step by step: a service built from this
repo, a volume for the database, an access password in front of the whole
site, and a custom domain. Search engines are told to stay out either way.

Other platforms are compared in [Deploying Acnologia](deploy.md).

The repo has three files for Railway:

- The `Dockerfile`, which Railway builds.
- [railway.json](../railway.json): Dockerfile build, one replica, health
  check on `/healthz` with a 120 second timeout, restart on failure up to 10
  times. Railway has deprecated config as code: `railway.json` keeps working
  for existing services until 2026-12-01
  ([Railway docs](https://docs.railway.com/reference/config-as-code)).
- [.railway/railway.ts](../.railway/railway.ts): the same service as
  Infrastructure as Code (one replica, the health check, a volume at
  `/data`, the variables). Railway does not read it on deploy; you apply it
  with `railway config plan` and `railway config apply`. See
  [Infrastructure as Code](#infrastructure-as-code).

Three ways in: the [one click template](#one-click-template) once it is
published, the dashboard steps below, or the Infrastructure as Code file.

## Before you start

- A Railway account, and this repo on GitHub (yours or a fork).
- A domain you can add a DNS record to, if you want a custom address. Railway
  gives a `*.up.railway.app` address otherwise.
- Optional: the Railway CLI (`railway`), for reading files on the volume
  and for Infrastructure as Code. Run `railway link` in the repo once so it
  knows the project.
- Optional: a master key made in advance with `acnologia gen-master-key` (or
  `docker run --rm acnologia gen-master-key` after `make docker`). Setting it
  before the first deploy is the recommended path.

## Steps

1. **Create the service.** In the Railway dashboard: New Project, Deploy from
   GitHub repo, pick this repo. Railway builds the `Dockerfile` and, until
   2026-12-01, reads `railway.json`. After that date, set the replica count
   to 1 and the health check path to `/healthz` in the service's Settings
   yourself, or apply the [Infrastructure as Code](#infrastructure-as-code)
   file.
2. **Add a volume** mounted at **`/data`**. The database lives there, and so
   does the generated `master.key` unless you set `ACNOLOGIA_MASTER_KEY`.
   Without a volume every redeploy starts empty and the stored keys are lost.
3. **Set the variables** in the Variables tab. Type the secrets in yourself
   and never commit them.

   | Variable | Value |
   |---|---|
   | `ACNOLOGIA_ACCESS_PASSWORD` | A long random password, at least 12 characters. `openssl rand -base64 24` makes one. |
   | `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD` | `true`, so the site can never come up unlocked. If `ACNOLOGIA_ACCESS_PASSWORD` is missing, the server generates a random one into `/data/access-password` and uses it (see [If you did not set the access password](#if-you-did-not-set-the-access-password)). |
   | `ACNOLOGIA_PUBLIC_URL` | `https://key.example.com` (your address, no trailing slash). It is written into `/llms.txt`, the MCP endpoint shown on the Apps page, the `acn login` link and every share invite, so other servers reach this one at the right address instead of the `*.up.railway.app` one. |
   | `ACNOLOGIA_SECURE_COOKIES` | `true`. Railway serves HTTPS, so every cookie should be `Secure`. |
   | `ACNOLOGIA_TRUST_PROXY_HEADERS` | `true`. Railway's proxy sets `X-Forwarded-For`, which the sign in and access password limiters use. |
   | `ACNOLOGIA_MASTER_KEY` | Recommended: the output of `acnologia gen-master-key`. Kept in Railway's variables and your password manager, it survives losing the volume. |
   | `RAILWAY_RUN_UID` | `0`. The image runs as a non-root user and Railway mounts a new volume owned by root, so without this the server stops with `mkdir /data: permission denied`. |
   | `ACNOLOGIA_ACCESS_SIGNS_IN` | Optional. Unset means `true`: the access password is the only login. `false` keeps email accounts behind the lock. |
   | `ACNOLOGIA_ACCESS_TTL` | Optional. How long a browser stays unlocked: `30d` by default, `7d` for a week, `12h` for half a day. |
   | `ANTHROPIC_API_KEY` | Optional. Turns on the provider builder, Fill with AI, the Assistant and the provider refresh. See [AI on the deploy](#ai-on-the-deploy). |
   | `ACNOLOGIA_DATA_DIR` | Leave it: the image sets `/data`, matching the volume. |

   Leave `ACNOLOGIA_ADDR` and `PORT` unset too. With `ACNOLOGIA_ADDR` unset,
   the server listens on `0.0.0.0:$PORT`, which Railway injects.
4. **Deploy** and wait for the health check to pass. The log says
   `access gate on: the dashboard needs the access password`.
5. **Add the custom domain.** Settings, Networking, Custom Domain, then
   `key.example.com`. Create the CNAME record Railway shows at your DNS
   provider and wait for the certificate. Generate Domain gives a
   `*.up.railway.app` address in the meantime.
6. **Open the site.** The lock screen asks for the access password, and with
   `ACNOLOGIA_ACCESS_SIGNS_IN` left on that is the whole login: you land in
   the dashboard as the owner. The browser stays unlocked for 30 days (or
   `ACNOLOGIA_ACCESS_TTL`); "Sign out" in the account menu locks it early.
7. **Back up the master key** now, before you add any provider keys. See
   [Back up the master key](#back-up-the-master-key).

## Infrastructure as Code

[.railway/railway.ts](../.railway/railway.ts) describes the project: one
service built from this repo's `main` branch, one replica, the `/healthz`
health check with a 120 second timeout, a 1 GB volume at `/data`, and the
variables from the table above. The secrets use `preserve()`, so the file
never holds them and applying it keeps the values you set. Deploying from a
fork? Change the repository in the file first.

1. `railway login`, then `railway link` to an existing project, or
   `railway init` for a new one.
2. Set the secrets:

   ```sh
   railway variables --set "ACNOLOGIA_ACCESS_PASSWORD=$(openssl rand -base64 24)" \
     --set "ACNOLOGIA_MASTER_KEY=$(openssl rand -base64 32)"
   ```

   Copy both into your password manager (`railway variables` lists them).
   Set `ACNOLOGIA_PUBLIC_URL` and, if you want the AI features,
   `ANTHROPIC_API_KEY` the same way.
3. `railway config plan`, and read it: nothing unexpected should be deleted.
4. `railway config apply`.

Then add the custom domain and open the site, as in steps 5 and 6 above.

### Migrating an existing service

A service set up from `railway.json` moves over like this:

1. `railway config pull --force` writes out what the project looks like now.
2. Compare it with `.railway/railway.ts`: the service and volume names, the
   mount path, the variables.
3. `railway config plan`. Check that nothing unexpected is deleted, above
   all the volume.
4. `railway config apply`.
5. Remove `railway.json`, or let `railway config migrate` do the move.

The restart policy (on failure, up to 10 times) and the watch patterns in
`railway.json` are not in the Infrastructure as Code file, so Railway's
defaults apply to both after the move.

## One click template

A Deploy on Railway button needs a template, which the maintainer creates
once:

1. Railway dashboard, Workspace, Templates, New Template. (Or, from a working
   project, Settings, Generate Template from Project.)
2. Add a service from the GitHub repo `sidharthpunathil/acnologia`.
3. Attach a volume at `/data`.
4. Set the variables:

   | Variable | Template value |
   |---|---|
   | `RAILWAY_RUN_UID` | `0` |
   | `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD` | `true` |
   | `ACNOLOGIA_SECURE_COOKIES` | `true` |
   | `ACNOLOGIA_TRUST_PROXY_HEADERS` | `true` |
   | `ACNOLOGIA_ACCESS_PASSWORD` | `${{secret(32)}}` |
   | `ACNOLOGIA_MASTER_KEY` | `${{secret(64, "0123456789abcdef")}}`: 64 hex characters, which is 32 bytes. Acnologia accepts a hex master key. |
   | `ACNOLOGIA_PUBLIC_URL` | `https://${{RAILWAY_PUBLIC_DOMAIN}}` |
   | `ANTHROPIC_API_KEY` | Empty, marked optional. |

   Verify in the template editor that the `${{RAILWAY_PUBLIC_DOMAIN}}`
   reference resolves to the generated domain. Whoever adds a custom domain
   later changes `ACNOLOGIA_PUBLIC_URL` to match.
5. Publish. The button markdown, with the template's code in place of
   `<CODE>`:

   ```
   [![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template/<CODE>?utm_medium=integration&utm_source=button&utm_campaign=acnologia)
   ```

Whoever deploys from the template copies `ACNOLOGIA_MASTER_KEY` and
`ACNOLOGIA_ACCESS_PASSWORD` from the Variables tab into a password manager
right after the first deploy.

## AI on the deploy

With `ANTHROPIC_API_KEY` set, open Settings, AI and pick the models (see
[Choosing the AI model](flows.md#choosing-the-ai-model)). Until you do, every
feature runs on `claude-opus-5-5`, or on `ACNOLOGIA_AI_MODEL` if you set it.
A switch applies on the next call, with no redeploy. A smaller model such as
Haiku is enough for Fill with AI, chat summaries and the offers refresh.

The provider refresh is on by default:

- **When:** monthly, on the 1st at 03:00 UTC.
- **What:** providers behind a service with a usable key, at most 10 a run,
  skipping any checked in the last 25 days. A new deploy with no keys checks
  nothing.
- **Cost:** capped at $2 a run and $5 a month. Recent runs in Settings shows
  what each run cost.

Change the schedule, the scope and the caps under Settings, Provider refresh,
or set the schedule to Off. See
[Keeping provider offers fresh](flows.md#keeping-provider-offers-fresh).

## If you did not set the access password

With `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD=true` and no
`ACNOLOGIA_ACCESS_PASSWORD`, the first start writes a random password to
`/data/access-password` and the site comes up locked with it. The log names
the file, never the password. Read it with the Railway CLI:

```sh
railway volume files --volume <volume-name> download /access-password access-password.txt
```

`railway volume list` shows the volume's name. Setting
`ACNOLOGIA_ACCESS_PASSWORD` later replaces the generated password. Delete the
file to get a new random one on the next start.

## Move an existing install onto Railway

To bring a local database along, upload a consistent copy and its master key
to the volume root before the server opens them:

1. On your machine, take a snapshot and check it:
   `sqlite3 data/acnologia.db ".backup snapshot.db"` (the file is `cogito.db`
   on an older install), then `sqlite3 snapshot.db "pragma integrity_check"`.
2. Railway's file tools need a running container, so start the service once
   with `ACNOLOGIA_DATA_DIR=/data/boot` (a throwaway folder) and no public
   domain.
3. Upload both files:
   `railway volume files upload snapshot.db /acnologia.db` and
   `railway volume files upload data/master.key /master.key`. Download them
   again and compare `shasum -a 256` with the originals.
4. Set `ACNOLOGIA_DATA_DIR=/data` again. The next deploy opens your data, and
   you sign in with the accounts you already had.

## Back up the master key

The master key encrypts every stored API key. Without it the database is
useless, and Acnologia refuses to start against a database written with a
different key.

- If you set `ACNOLOGIA_MASTER_KEY`, keep a copy in your password manager.
- If you did not, the key was generated into `/data/master.key` on the
  volume. The image is distroless (no shell), so copy it off with
  `railway volume files download /master.key master.key` or a backup of the
  volume, and store it apart from any database backup. Setting
  `ACNOLOGIA_MASTER_KEY` later only works with that same key.

Back up the database by copying `/data/acnologia.db` together with its
`-wal` file, or after stopping the service.

## Clients and scripts

The access password covers the dashboard. These callers have their own
credentials and get through without it:

- **Apps, the proxy and the app MCP** (`/proxy/*`, `/api/v1/*`, `/mcp`) need
  an app token.
- **Dashboard API scripts and the control MCP** (`acn`, `acnologia ctl`,
  `/api/admin/*`, `/mcp/control`): a valid admin API token passes the lock on
  its own. Sign in with `acn login` (see [Command line](cli.md)).
- **Other Acnologia servers** you share with reach `/.well-known/acnologia`
  and `/api/peer/v1/*`, which need an invite or a share token.
- **People you share with** open a claim link (`/claim/...`), whose page
  calls `/api/claim/v1/*` with the claim code.

See [sharing between servers](flows.md#sharing-between-servers) and
[sharing with people](flows.md#sharing-with-people) for how each kind works.

## Keep it at one replica

SQLite is one file on one volume, and concurrency slots live in the server's
memory, so `railway.json` and `.railway/railway.ts` both set one replica. A service with a volume also has
a short downtime on each redeploy, because Railway never mounts one volume
into two deployments at once.

Railway stops the old deployment with SIGTERM and, by default, kills it
right away. The server drains requests in flight for up to 15 seconds, so
`railway.json` sets `drainingSeconds` to 20 and `.railway/railway.ts` sets the
service variable `RAILWAY_DEPLOYMENT_DRAINING_SECONDS` to `20`. If you deploy
without either file, set that variable (or the draining time under the
service's Settings) yourself, or a redeploy can drop the usage of requests in
flight.

## What a stranger sees

`/robots.txt` disallows everything, every response carries
`X-Robots-Tag: noindex, nofollow, noarchive, nosnippet, noimageindex, noai, noimageai`,
and the page has a robots meta tag. Without the password, a visitor gets the
lock screen and the API answers `401 locked`. Wrong passwords are limited to
5 a minute and 50 a day from one address, plus 20 a minute across everyone;
past that the answer is `429`. Changing `ACNOLOGIA_ACCESS_PASSWORD` and
redeploying signs every browser out.
