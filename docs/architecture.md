# Server architecture

Cogito is one Go binary. It serves the React dashboard, the dashboard API, the
consumer API, an HTTP proxy and an MCP endpoint from a single process, and
keeps everything in one SQLite file. There is no queue, cache or second
service to run.

```mermaid
flowchart LR
    subgraph Clients
        UI["Dashboard<br/>(React SPA)"]
        App["Your apps<br/>(SDKs, curl)"]
        Agent["AI agents<br/>(MCP clients)"]
    end

    subgraph Cogito["cogito (one process)"]
        Router["chi router<br/>+ middleware"]
        Auth["auth<br/>sessions, app tokens"]
        Admin["dashboard API<br/>/api/admin, /api/auth"]
        Consumer["consumer API<br/>/api/v1"]
        Proxy["proxy<br/>/proxy/{service}/*"]
        MCP["MCP<br/>/mcp"]
        Pool["pool<br/>pick, slots, outcomes"]
        Engine["providers engine<br/>classify, health check"]
        Catalog["catalog<br/>provider specs, logos"]
        AIB["AI provider builder<br/>(agent runs)"]
        Worker["worker<br/>housekeeping, health"]
        Store[("SQLite<br/>cogito.db")]
        Box["crypto box<br/>AES-256-GCM"]
    end

    Upstream["Provider APIs<br/>(SerpAPI, ElevenLabs, ...)"]
    Claude["Claude API"]

    UI -->|session cookie| Router
    App -->|cog_ token| Router
    Agent -->|cog_ token| Router
    Router --> Auth
    Auth --> Admin & Consumer & Proxy & MCP
    Admin --> Catalog & Pool & Store & AIB
    Consumer --> Pool
    Proxy --> Pool
    MCP --> Consumer & Proxy
    Pool --> Engine & Store & Box
    Pool -->|key injected| Upstream
    Engine -->|health checks| Upstream
    Worker --> Pool & Store
    AIB --> Claude
    AIB --> Catalog
    Catalog --> Store
```

## Packages

All server code lives under `server/`.

| Package | Responsibility |
|---|---|
| `cmd/cogito` | Entry point. Loads config, opens the store, verifies the master key, seeds providers, restores lease slots, starts the worker and the HTTP server. Also `cogito version` and `cogito gen-master-key`. |
| `internal/config` | Reads `COGITO_*` environment variables and an optional `.env` file. See [configuration](configuration.md). |
| `internal/api` | Every HTTP handler: routing and middleware (`server.go`), sessions and login (`auth.go`), dashboard API (`admin.go`, `providers.go`, `insights.go`, `ai.go`), consumer API (`consumer.go`), proxy (`proxy.go`) and MCP (`mcp.go`). |
| `internal/pool` | Picks a key for a request, holds per key concurrency slots, applies outcomes (cooldown, exhausted, invalid) and runs health checks. |
| `internal/providers` | The provider spec format and engine: classifying upstream responses into outcomes, running health checks, auth placement, fixture tests, and the built in SerpAPI, ElevenLabs and generic specs. |
| `internal/catalog` | Provider records on top of the store: validation, publishing with version history, service defaults, live checks, logo discovery and the SSRF safe HTTP client. |
| `internal/agent` | The AI provider builder: a background run that drives Claude with tools to research a provider and produce a spec. |
| `internal/store` | SQLite persistence. Schema, migrations and one file per entity. |
| `internal/crypto` | The master key, AES-256-GCM encryption of secrets, keyed fingerprints for duplicate detection, token hashing, secret scrubbing. |
| `internal/worker` | Periodic housekeeping and background health checks. |
| `internal/webui`, `internal/llmstxt` | Embedded assets: the built dashboard and `llms.txt`. |

## Request pipeline

Every request passes the same middleware before reaching a handler.

```mermaid
flowchart TD
    Req([HTTP request]) --> RealIP{"COGITO_TRUST_PROXY_HEADERS?"}
    RealIP -->|yes| SetIP["RealIP: client IP from<br/>X-Forwarded-For / X-Real-IP"]
    RealIP -->|no| Keep["keep socket address"]
    SetIP --> Rec[Recoverer]
    Keep --> Rec
    Rec --> Log["requestLog<br/>(path only, never the query)"]
    Log --> Priv["privateSite<br/>noindex, nosniff, DENY framing,<br/>dashboard CSP"]
    Priv --> Route{route}
    Route -->|/api/auth, /api/admin| JSON["jsonOnly<br/>(CSRF guard)"]
    JSON --> User["requireUser<br/>session cookie"]
    User --> Role["requireRole<br/>admin / superadmin"]
    Role --> H1[dashboard handler]
    Route -->|/api/v1, /proxy| App["requireApp<br/>cog_ token"]
    App --> H2[consumer or proxy handler]
    Route -->|/mcp| H3["MCP handler<br/>(authenticates the token itself)"]
    Route -->|/api/logos| User2[requireUser] --> H4[logo handler]
    Route -->|anything else| SPA["embedded SPA<br/>or static asset"]
```

## Data model

```mermaid
erDiagram
    users ||--o{ sessions : "signs in with"
    services ||--o{ keys : holds
    providers ||--o{ services : "configures"
    providers ||--o{ provider_versions : "has history"
    keys ||--o{ key_apps : "restricted to"
    apps ||--o{ key_apps : "allowed on"
    keys ||--o{ leases : "lent as"
    apps ||--o{ leases : "borrows"
    services ||--o{ events : logs
    agent_runs ||--o{ agent_steps : records

    users {
        int id
        string email
        string role
        bool disabled
    }
    sessions {
        string token_hash
        int user_id
        int expires_at
    }
    providers {
        string slug
        string name
        json spec
        int version
        string source
    }
    services {
        int id
        string slug
        string provider
        string base_url
        string auth_type
        bool target_trusted
    }
    keys {
        int id
        int service_id
        blob secret_enc
        string secret_fp
        bool hidden
        bool restricted
        string status
    }
    apps {
        int id
        string token_hash
        string mode
        json allowed_services
        bool lease_hidden
    }
    leases {
        string id
        int key_id
        int app_id
        int expires_at
        string outcome
    }
    events {
        int id
        string kind
        int key_id
        int app_id
        string detail
    }
    agent_runs {
        int id
        string kind
        string status
        blob test_key_enc
    }
```

Notes:

- **Secrets** are stored only as `secret_enc` (AES-256-GCM, random nonce) plus
  `secret_fp`, a keyed HMAC used to reject duplicates without decrypting.
  Session and app tokens are stored as SHA-256 hashes.
- **Key ids are never reused.** A high water mark in `meta.keys_seq` makes sure
  a new key cannot inherit a deleted key's events, leases or slots.
- **`audit`** records every dashboard change and sign in. **`meta`** holds
  small settings such as `master_key_check` and `usage_day`.
- Foreign keys are on, so deleting a service removes its keys and leases, and
  deleting a user removes their sessions.
- The connection pool has one connection, with WAL mode and a 5 second busy
  timeout, which keeps SQLite simple under concurrent requests.

## In memory state

Almost all state is in SQLite. The exception is **concurrency slots**
(`internal/pool/slots.go`): how many requests are in flight on each key, which
leases hold a slot, and a wake channel per service for requests queued on a
busy service. Cogito runs as a single instance, and on boot it rebuilds lease
slots from open leases in the database (`RestoreLeases`).

## Boot sequence

```mermaid
sequenceDiagram
    autonumber
    participant M as main
    participant C as config
    participant K as crypto
    participant S as store
    participant Cat as catalog
    participant P as pool
    participant W as worker
    participant H as HTTP server

    M->>C: load env (+ .env)
    M->>M: create data dir, chmod 0700
    M->>K: master key from COGITO_MASTER_KEY or data/master.key
    Note over K: generated only if the file does not exist
    M->>S: open cogito.db, apply schema and migrations, chmod 0600
    M->>S: compare meta.master_key_check
    alt key does not match the database
        M-->>M: refuse to start
    end
    M->>S: bootstrap superadmin from env (only if no users)
    M->>Cat: seed built in providers, load all specs
    M->>M: start AI builder (fail runs left over from a crash)
    M->>P: restore slots for open leases
    M->>W: start housekeeping and health loops
    M->>H: listen on COGITO_ADDR
```

## Background work

`internal/worker` runs two loops.

| Loop | Interval | Does |
|---|---|---|
| Housekeeping | 30 s | Resets daily usage at UTC midnight, frees slots of expired leases, reactivates exhausted keys whose quota reset time passed, and every 10 minutes prunes expired sessions, leases older than 7 days and events past retention. |
| Health | 1 min tick | Re-checks keys whose last check is older than `COGITO_HEALTH_INTERVAL` (exhausted keys hourly). Skips disabled keys, keys at their concurrency cap, providers without a health check, and hidden keys on services whose target is not trusted. |

Health checks run in their own loop so a slow provider never delays freeing
lease slots.

## Build and packaging

```mermaid
flowchart LR
    Web["web/ (Vite build)"] -->|"server/internal/webui/dist"| Embed["go:embed"]
    LLMS["docs/llms.txt"] -->|"make llms"| Embed2["internal/llmstxt"]
    Embed --> Bin["bin/cogito<br/>(static, CGO off)"]
    Embed2 --> Bin
    Bin --> Img["Docker image<br/>distroless, nonroot"]
```

The Dockerfile builds the UI with Node, the binary with Go (`CGO_ENABLED=0`,
pure Go SQLite), and copies the result into a distroless nonroot image with a
`/data` volume.

## Deployment shape

```mermaid
flowchart LR
    Internet((clients)) --> TLS["reverse proxy<br/>TLS (Caddy, Traefik,<br/>Cloudflare Tunnel)"]
    TLS --> C["cogito :8080"]
    C --> V[("/data volume<br/>cogito.db, master.key")]
    C --> Up["provider APIs"]
```

One instance per data directory. Put it behind TLS, set
`COGITO_SECURE_COOKIES=true` and `COGITO_TRUST_PROXY_HEADERS=true`, and keep the
master key out of the data volume (set `COGITO_MASTER_KEY` from a secret store)
so a copy of the volume alone does not reveal stored keys.

See also: [request flows](flows.md), [security model](security.md),
[provider specs](providers.md).
