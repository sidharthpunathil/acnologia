# Configuration

Cogito reads environment variables on start. It also loads a `.env` file from
the working directory if there is one; variables already set in the
environment win over the file. [`.env.example`](../.env.example) lists them all.

## Settings

| Variable | Default | What it does |
|---|---|---|
| `COGITO_ADDR` | `:8080` | Listen address. |
| `COGITO_DATA_DIR` | `./data` | Holds `cogito.db` (plus WAL files) and, when no env key is set, `master.key`. Created with mode 0700. |
| `COGITO_MASTER_KEY` | empty | 32 bytes, base64 or hex, that encrypt stored keys. Generate with `cogito gen-master-key`. When empty, `data/master.key` is used, or created on first boot. |
| `COGITO_PUBLIC_URL` | empty | Base URL written into `/llms.txt`. When empty it is derived from the request host. |
| `COGITO_SECURE_COOKIES` | `false` | Mark the session cookie `Secure`. Set `true` behind HTTPS. It is also set automatically for direct TLS. |
| `COGITO_TRUST_PROXY_HEADERS` | `false` | Take the client IP from `X-Forwarded-For` / `X-Real-IP`. Enable only behind a proxy that overwrites them; otherwise clients could spoof their address to dodge the login limiter. |
| `COGITO_BOOTSTRAP_EMAIL`, `COGITO_BOOTSTRAP_PASSWORD` | empty | Create the first superadmin on boot instead of the setup screen. Used only while there are no users. |
| `COGITO_HEALTH_INTERVAL` | `6h` | How often each key is re-checked with the provider. `0` or `off` disables background checks. Exhausted keys are re-checked hourly. |
| `COGITO_EVENT_RETENTION_DAYS` | `30` | How long request events are kept. `0` or less keeps them forever. |
| `COGITO_MAX_BODY_MB` | `50` | Largest proxied request body. |
| `COGITO_CACHE_MB` | `64` | Memory for cached GET responses and idempotent replays. `0` turns caching off. |
| `COGITO_WEBHOOK_ALLOW_PRIVATE` | `false` | Let webhooks reach private, loopback and link-local addresses, for a receiver on your own network. |
| `COGITO_EGRESS_ECHO_URL` | `https://www.cloudflare.com/cdn-cgi/trace` | URL the egress proxy test calls to learn the proxy's public IP. |
| `ANTHROPIC_API_KEY` | empty | Enables the AI provider builder and "Fill with AI". |
| `COGITO_AI_MODEL` | `claude-opus-5-5` | Model for AI features. |
| `GITHUB_TOKEN` | empty | Lets the AI builder read GitHub with a higher rate limit. Use a fine grained, public read only token. |

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
COGITO_DATA_DIR=./data ./bin/cogito

# Docker
cp .env.example .env
docker compose up -d --build
```

The container runs as a nonroot user with `/data` as a volume.

## Backups

Back up `cogito.db` (with its `-wal` file, or after a clean shutdown) **and**
the master key, separately. Without the master key the stored keys cannot be
decrypted, and Cogito refuses to start against a database with a different key.

## Upgrading

Migrations run automatically on start. Notable behavior changes:

- **Hidden keys now need a grant.** Existing apps cannot lease or proxy hidden
  keys until a superadmin turns on **Lease hidden keys** for them. Existing
  services keep their trust; services an admin edits afterwards pause hidden
  keys until a superadmin confirms them.
- **Behind a proxy**, set `COGITO_TRUST_PROXY_HEADERS=true`. The server logs a
  warning when it sees `X-Forwarded-For` while the setting is off.
