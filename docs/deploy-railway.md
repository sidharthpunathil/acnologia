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
   | `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD` | `true`: the server refuses to start without the password, so it can never come up unlocked. |
   | `ACNOLOGIA_PUBLIC_URL` | `https://key.sidharth.co` (your domain, no trailing slash). |
   | `ACNOLOGIA_SECURE_COOKIES` | `true`: Railway serves HTTPS, so every cookie is `Secure`. |
   | `ACNOLOGIA_TRUST_PROXY_HEADERS` | `true`: Railway's proxy sets `X-Forwarded-For`, which the sign in limiter uses. |
   | `ACNOLOGIA_DATA_DIR` | `/data` (the image sets it already; keep it matching the volume). |
   | `ACNOLOGIA_MASTER_KEY` | Recommended, set before the first deploy: the output of `acnologia gen-master-key` (or `docker run --rm acnologia gen-master-key`). Kept in Railway's variables and your password manager, it survives losing the volume. |
   | `ANTHROPIC_API_KEY` | Optional: turns on the provider builder, "Fill with AI" and the Assistant. |

   Leave `ACNOLOGIA_ADDR` and `PORT` alone: with `ACNOLOGIA_ADDR` unset the
   server listens on `0.0.0.0:$PORT`, which Railway injects.
4. **Deploy** and wait for the health check. The log says
   `access gate on: the dashboard needs the access password`.
5. **Add the custom domain.** Settings, Networking, Custom Domain,
   `key.sidharth.co`. Create the CNAME record Railway shows at your DNS
   provider and wait for the certificate. (Generate Domain gives a
   `*.up.railway.app` address meanwhile.)
6. **Open the site.** The lock screen asks for the access password, then the
   first visit creates the superadmin. The browser stays unlocked for 30
   days; "Lock this browser" in the account menu ends that early.

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
- **Dashboard API scripts** (admin tokens, `acnologia ctl`) also send
  `X-Acnologia-Access: <password>`. `acnologia ctl` does this when
  `ACNOLOGIA_ACCESS_PASSWORD` is set in its environment.

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
