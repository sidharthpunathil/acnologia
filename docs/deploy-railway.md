# Deploying on Railway

Acnologia holds your API keys, so a public deploy is locked twice: an access
password in front of the whole site, then the user accounts behind it. Search
engines are told to stay out either way.

The repo has a `Dockerfile` and a `railway.json` (Dockerfile build, one
replica, health check on `/healthz` with a 120 second timeout, restart on
failure up to 10 times).

## Steps

1. **Create the service.** Railway dashboard, New Project, Deploy from GitHub
   repo, pick this repo. Railway reads `railway.json` and builds the
   `Dockerfile`.
2. **Add a volume** mounted at **`/data`**. The database and, unless you set
   `ACNOLOGIA_MASTER_KEY`, the generated `master.key` live there. Without a
   volume every redeploy starts empty and the stored keys are lost.
3. **Set the variables** (Variables tab). Type the secrets in yourself; never
   commit them.

   | Variable | Value |
   |---|---|
   | `ACNOLOGIA_ACCESS_PASSWORD` | A long random password, 12 characters at least. `openssl rand -base64 24` makes one. |
   | `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD` | `true`: the site can never come up unlocked. If `ACNOLOGIA_ACCESS_PASSWORD` is missing, the server generates a random one into `/data/access-password` and uses it (see below). |
   | `ACNOLOGIA_ACCESS_SIGNS_IN` | Leave unset (`true`): the access password is the only login. `false` keeps email accounts behind the lock. |
   | `ACNOLOGIA_ACCESS_TTL` | Optional. How long a browser stays unlocked: `30d` by default, `7d` for a week, `12h` for half a day. |
   | `ACNOLOGIA_PUBLIC_URL` | `https://key.sidharth.co` (your domain, no trailing slash). Written into `/llms.txt` and into every share invite, so another server reaches this one at the right address instead of the `*.up.railway.app` one. |
   | `ACNOLOGIA_SECURE_COOKIES` | `true`: Railway serves HTTPS, so every cookie is `Secure`. |
   | `ACNOLOGIA_TRUST_PROXY_HEADERS` | `true`: Railway's proxy sets `X-Forwarded-For`, which the sign in limiter uses. |
   | `ACNOLOGIA_DATA_DIR` | `/data` (the image sets it already; keep it matching the volume). |
   | `ACNOLOGIA_MASTER_KEY` | Recommended, set before the first deploy: the output of `acnologia gen-master-key` (or `docker run --rm acnologia gen-master-key`). Kept in Railway's variables and your password manager, it survives losing the volume. |
   | `ANTHROPIC_API_KEY` | Optional: turns on the provider builder, Fill with AI, the Assistant and the provider refresh. See [AI on the deploy](#ai-on-the-deploy). |
   | `RAILWAY_RUN_UID` | `0`. The image runs as a non-root user, and Railway mounts a new volume owned by root, so without this the server stops with `mkdir /data: permission denied`. |

   Leave `ACNOLOGIA_ADDR` and `PORT` alone: with `ACNOLOGIA_ADDR` unset the
   server listens on `0.0.0.0:$PORT`, which Railway injects.
4. **Deploy** and wait for the health check. The log says
   `access gate on: the dashboard needs the access password`.
5. **Add the custom domain.** Settings, Networking, Custom Domain,
   `key.sidharth.co`. Create the CNAME record Railway shows at your DNS
   provider and wait for the certificate. (Generate Domain gives a
   `*.up.railway.app` address meanwhile.)
6. **Open the site.** The lock screen asks for the access password, and that
   is the whole login: you land in the dashboard as the owner. The browser
   stays unlocked for 30 days; "Sign out" in the account menu locks it early.

## AI on the deploy

With `ANTHROPIC_API_KEY` set, open Settings, AI and pick the models (see
[Choosing the AI model](flows.md#choosing-the-ai-model)). Every feature runs
on `claude-opus-5-5` until you do, or on `ACNOLOGIA_AI_MODEL` if you set it.
A switch applies on the next call, with no redeploy. A smaller model such as
Haiku is enough for Fill with AI, chat summaries and offer refresh.

The provider refresh is on by default: monthly, on the 1st at 03:00 UTC,
over providers behind a service with a usable key, at most 10 a run, and
skipping any checked in the last 25 days. A new deploy with no keys checks
nothing. Its spend is capped at $2 a run and $5 a month, so the worst case is
$5 a month, and Recent runs in Settings shows what each run cost. Change the
schedule, the scope and the caps under Settings, Provider refresh, or set the
schedule to Off. See
[Keeping provider offers fresh](flows.md#keeping-provider-offers-fresh).

## If you did not set the access password

With `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD=true` and no `ACNOLOGIA_ACCESS_PASSWORD`,
the first start writes a random password to `/data/access-password` and the
site comes up locked with it. The log names the file, never the password.
Read it with:

```
railway volume files --volume acnologia-data download /access-password access-password.txt
```

Setting `ACNOLOGIA_ACCESS_PASSWORD` later replaces it. Delete the file to get
a new random one on the next start.

## Move an existing install onto Railway

To bring a local database along, upload a consistent copy and its master key
to the volume root before the server opens them:

1. On your machine: `sqlite3 data/acnologia.db ".backup snapshot.db"` (or
   `cogito.db` on an older install), then
   `sqlite3 snapshot.db "pragma integrity_check"`.
2. Railway's file tools need a running container, so start the service once
   with `ACNOLOGIA_DATA_DIR=/data/boot` (a throwaway folder) and no public
   domain.
3. `railway volume files upload snapshot.db /acnologia.db` and
   `railway volume files upload data/master.key /master.key`, then download
   both again and compare `shasum -a 256` with the originals.
4. Set `ACNOLOGIA_DATA_DIR=/data` again. The next deploy opens your data, and
   you sign in with the accounts you already had.

## Back up the master key

The master key encrypts every stored API key. Lose it and the database is
useless; Acnologia also refuses to start against a database written with a
different key.

- If you set `ACNOLOGIA_MASTER_KEY`, keep a copy in your password manager.
- If you did not, the key was generated into `/data/master.key` on the
  volume. The image is distroless (no shell), so copy it off with Railway's
  volume tools or a backup of the volume, and store it apart from any
  database backup. Setting `ACNOLOGIA_MASTER_KEY` later only works with that
  same key.

Back up the database by copying `/data/acnologia.db` with its `-wal` file, or
after stopping the service.

## Clients and scripts

- **Apps, the proxy and MCP** (`/proxy/*`, `/api/v1/*`, `/mcp`) are not behind
  the access password: they need an app token, as before.
- **Dashboard API scripts** (admin tokens, `acn`, `acnologia ctl`): a valid
  admin token passes the lock on its own. Sign in with `acn login` (see
  [Command line](cli.md)).
- **Other Acnologia servers** you share with reach `/.well-known/acnologia`
  and `/api/peer/v1/*` without the password: those need an invite or a share
  token. See [sharing](flows.md#sharing-between-servers).

## Keep it at one replica

SQLite is one file on one volume. A service with a volume also has a short
downtime on redeploy, because Railway never mounts one volume into two
deployments.

## What a stranger sees

`/robots.txt` disallows everything, every response carries
`X-Robots-Tag: noindex, nofollow, noarchive, nosnippet`, and the page has a
robots meta tag. Without the password, a visitor gets the lock screen and the
API answers `401 locked`. Five wrong passwords a minute (fifty a day, counted
across everyone) earn a `429`. Changing `ACNOLOGIA_ACCESS_PASSWORD` and
redeploying signs every browser out.
