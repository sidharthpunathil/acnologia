# Server architecture

Acnologia is one Go binary. It serves the React dashboard, the dashboard API, the
consumer API, an HTTP proxy, an MCP endpoint and the server to server sharing
routes from a single process, and keeps everything in one SQLite file. There
is no queue, cache or second service to run.

```mermaid
flowchart LR
    subgraph Clients
        UI["Dashboard<br/>(React SPA)"]
        App["Your apps<br/>(SDKs, curl)"]
        Agent["AI agents<br/>(MCP clients)"]
        Peer["Other Acnologia servers"]
    end

    subgraph Acnologia["acnologia (one process)"]
        Router["chi router<br/>+ middleware"]
        Gate["access lock<br/>/api/gate"]
        Auth["auth<br/>sessions, app tokens,<br/>share tokens"]
        Admin["dashboard API<br/>/api/admin, /api/auth"]
        Consumer["consumer API<br/>/api/v1"]
        Proxy["proxy<br/>/proxy/{service}/*"]
        MCP["MCP<br/>/mcp"]
        PeerAPI["sharing<br/>/.well-known/acnologia,<br/>/api/peer/v1"]
        PeerPkg["peer<br/>identity, invites, sync"]
        Pool["pool<br/>pick, slots, outcomes"]
        Engine["providers engine<br/>classify, health check"]
        Catalog["catalog<br/>provider specs, logos"]
        AIB["AI provider builder<br/>(agent runs)"]
        Chat["assistant chat<br/>tools call the dashboard API"]
        Refresh["provider refresh<br/>offers and Claude prices"]
        Worker["worker<br/>housekeeping, health,<br/>peer sync, refresh"]
        Store[("SQLite<br/>acnologia.db")]
        Box["crypto box<br/>AES-256-GCM"]
    end

    Upstream["Provider APIs<br/>(SerpAPI, ElevenLabs, ...)"]
    Claude["Claude API"]

    UI -->|session cookie| Router
    App -->|acn_ token| Router
    Agent -->|acn_ token| Router
    Peer -->|"invite or share token"| Router
    Router --> Gate
    Gate --> Auth
    Auth --> Admin & Consumer & Proxy & MCP & PeerAPI
    Admin --> Catalog & Pool & Store & AIB
    Consumer --> Pool
    Proxy --> Pool
    MCP --> Consumer & Proxy
    Pool --> Engine & Store & Box
    Pool -->|key injected| Upstream
    Proxy -->|"share token, shared service"| Peer
    PeerAPI --> Pool & Catalog
    Admin --> PeerPkg
    PeerPkg -->|"redeem, manifest"| Peer
    PeerPkg --> Catalog & Store
    Engine -->|health checks| Upstream
    Worker --> Pool & Store & PeerPkg & Refresh
    Admin --> Refresh
    Refresh --> Claude
    Refresh --> Catalog
    AIB --> Claude
    Admin --> Chat
    Chat -->|"in-process calls<br/>as the user"| Admin
    Chat --> Claude
    AIB --> Catalog
    Catalog --> Store
```

## Packages

All server code lives under `server/`.

| Package | Responsibility |
|---|---|
| `cmd/acnologia` | Entry point. Loads config, opens the store, verifies the master key, seeds providers, restores lease slots, starts the worker and the HTTP server. Also `acnologia version`, `acnologia gen-master-key`, `acnologia mcp` (a stdio bridge to `/mcp`) and `acnologia ctl`. |
| `internal/config` | Reads `ACNOLOGIA_*` environment variables and an optional `.env` file. See [configuration](configuration.md). |
| `internal/api` | Every HTTP handler: routing and middleware (`server.go`), the access lock (`gate.go`, `solo.go`), sessions, login and admin API tokens (`auth.go`, `tokens_auth.go`), the CLI sign in (`clidevice.go`), dashboard API (`admin.go`, `admin_v2.go`, `providers.go`, `insights.go`, `usage.go`, `app_tokens.go`, `settings.go`), AI routes and model switching (`ai.go`), the provider refresh routes (`refresh.go`), the Assistant chat, its tools, key vault and compaction (`assistant*.go`), sharing between servers (`sharing.go`, `peer_public.go`, `peer_providers.go`), consumer API (`consumer.go`), proxy (`proxy.go`, `proxy_extras.go`), MCP (`mcp.go`, `mcp_v2.go`, `mcp_admin.go`), the control MCP for admin tokens (`mcp_control.go`, its tool table in `mcp_control_tools.go`), the `COGITO_*` era compatibility (`compat.go`) and the Markdown docs (`docs.go`). |
| `internal/pool` | Picks a key for a request, holds per key concurrency slots, applies outcomes (cooldown, exhausted, invalid) and runs health checks. |
| `internal/providers` | The provider spec format and engine: classifying upstream responses into outcomes, running health checks, auth placement, fixture tests, and the built in specs (`builtin*.go`). |
| `internal/catalog` | Provider records on top of the store: validation, publishing with version history, service defaults, live checks, logo discovery and the SSRF safe HTTP client. |
| `internal/agent` | The AI provider builder: a background run that drives Claude with tools to research a provider and produce a spec. Also the shared Claude client used by "Fill with AI", the Assistant and its compaction summaries, and the provider refresh; the model per feature and price overrides (`models.go`, `pricing.go`, `prices.go`); and the offers and Claude price research the refresh calls (`refresh.go`). |
| `internal/store` | SQLite persistence. Schema, migrations and one file per entity. |
| `internal/crypto` | The master key, AES-256-GCM encryption of secrets, keyed fingerprints for duplicate detection, token hashing, secret scrubbing. |
| `internal/redact` | Keeps secrets out of everything that is not the key store: requests to Claude, agent steps, events, the audit log, webhook payloads and log lines. See [security](security.md#what-the-model-can-and-cannot-see). |
| `internal/keyparse` | Pulls keys out of pasted or dropped text: JSON arrays, CSV with a header row, `.env` files and free form lists. |
| `internal/breaker` | The per service circuit breaker that pauses traffic to a provider that keeps failing. |
| `internal/respcache` | The in-memory response cache and idempotent replays, a byte bounded LRU with per entry expiry. |
| `internal/egress` | Routes upstream traffic through HTTP, HTTPS or SOCKS5 proxies, per key or per service. |
| `internal/notify` | Delivers pool events to webhooks: Slack, Discord, Telegram or signed JSON. |
| `internal/peer` | Sharing between servers: the server identity (Ed25519), invites and the signed redeem message, the server to server client, and the receiver's sync that turns a share's manifest into local services and imports the provider specs they need (`providers.go`). The owner side handlers are `internal/api/peer_public.go` and `sharing.go`. See [flows](flows.md#sharing-between-servers). |
| `internal/refresh` | The provider offers refresh: its settings and schedule (`settings.go`, `schedule.go`), runs that re-check each provider's offers and Claude's prices within a budget (`refresher.go`), and the offers diff that decides between publishing and a review (`diff.go`). See [flows](flows.md#keeping-provider-offers-fresh). |
| `internal/worker` | Periodic housekeeping, background health checks, the peer sync loop and the refresh schedule tick. |
| `internal/ctl` | The admin API command line client, built as `acn` (`cmd/acn`) and as `acnologia ctl`. |
| `internal/webui`, `internal/llmstxt`, `internal/docsmd` | Embedded assets: the built dashboard, `llms.txt` and `llms-full.txt`, and the Markdown docs with their images. |

## Request pipeline

Every request passes the same middleware before reaching a handler.

```mermaid
flowchart TD
    Req([HTTP request]) --> RealIP{"ACNOLOGIA_TRUST_PROXY_HEADERS?"}
    RealIP -->|yes| SetIP["RealIP: client IP from<br/>X-Forwarded-For / X-Real-IP"]
    RealIP -->|no| Keep["keep socket address"]
    SetIP --> Rec[Recoverer]
    Keep --> Rec
    Rec --> Log["requestLog<br/>(path only, never the query)"]
    Log --> Priv["privateSite<br/>noindex, nosniff, DENY framing,<br/>dashboard CSP"]
    Priv --> GateMW{"gateRequests<br/>access password set?"}
    GateMW -->|"no, or unlocked,<br/>or an open route"| Route{route}
    GateMW -->|locked| Locked["401 locked,<br/>or the lock screen"]
    Route -->|/api/auth, /api/admin| JSON["jsonOnly<br/>(CSRF guard)"]
    JSON --> User["requireUser<br/>session cookie"]
    User --> Role["requireRole<br/>admin / superadmin"]
    Role --> H1[dashboard handler]
    Route -->|/api/v1, /proxy| App["requireApp<br/>acn_ token"]
    App --> H2[consumer or proxy handler]
    Route -->|/mcp| H3["MCP handler<br/>(authenticates the token itself)"]
    Route -->|/api/logos| User2[requireUser] --> H4[logo handler]
    Route -->|"/api/peer/v1"| H5["peer handlers<br/>(invite, or requireApp<br/>with a share token)"]
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
    apps ||--o{ app_tokens : "authenticates with"
    keys ||--o{ leases : "lent as"
    apps ||--o{ leases : "borrows"
    services ||--o{ events : logs
    agent_runs ||--o{ agent_steps : records
    users ||--o{ chat_threads : owns
    shares |o--o| apps : "served through"
    peer_links ||--o{ services : "creates"
    peer_links ||--o{ peer_link_providers : "imported"
    refresh_runs ||--o{ refresh_items : "checks"
    refresh_runs ||--o{ refresh_reviews : "proposes"
    providers ||--o| provider_refresh : "last checked"

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
        int peer_link_id
        string peer_remote_slug
        string peer_provider
    }
    keys {
        int id
        int service_id
        blob secret_enc
        string secret_fp
        bool hidden
        bool restricted
        string status
        string account_email
    }
    apps {
        int id
        string mode
        json allowed_services
        bool lease_hidden
        string share_id
    }
    app_tokens {
        int id
        int app_id
        string hash
        int expires_at
        int revoked_at
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
    chat_threads {
        int id
        int user_id
        string title
        blob data
    }
    agent_runs {
        int id
        string kind
        string status
        string model
        blob test_key_enc
    }
    shares {
        string id
        string name
        string status
        json services
        json providers
        json key_scopes
        bool include_hidden
        int app_id
        string peer_id
        string invite_hash
        int expires_at
    }
    peer_links {
        int id
        string peer_id
        string peer_url
        string peer_public_key
        string remote_share_id
        blob token_enc
        string status
        json manifest
        string prefix
        bool trusted
    }
    peer_link_providers {
        int link_id
        string provider
        string kind
        int imported_version
        int remote_version
        int local_version
    }
    refresh_runs {
        int id
        string trigger
        string status
        string model
        json skipped
    }
    refresh_items {
        int id
        int run_id
        string slug
        string status
        string diff
        float cost_usd
    }
    refresh_reviews {
        int id
        string slug
        int run_id
        string status
        int base_version
        json offers
        json diff
    }
    provider_refresh {
        string slug
        int checked_at
        string outcome
        int changed_version
    }
```

Notes:

- **Secrets** are stored only as `secret_enc` (AES-256-GCM, random nonce) plus
  `secret_fp`, a keyed HMAC used to reject duplicates without decrypting.
  Session and app tokens are stored as SHA-256 hashes.
- **Key ids are never reused.** A high water mark in `meta.keys_seq` makes sure
  a new key cannot inherit a deleted key's events, leases or slots.
- **`chat_threads`** holds assistant conversations, one owner each, with the
  transcript encrypted by the same master key as the stored keys.
- **Key account fields** (`account_name`, `account_email`, `account_phone`,
  `account_ref`) record who each key belongs to.
- **`app_tokens`** holds each app's named tokens as SHA-256 hashes; requests,
  spend and limits count per app (`app_usage`) whichever token made the call.
- **Sharing between servers.** `shares` are what this server gives: each is
  backed by a hidden app (`apps.share_id`) whose token the receiver holds, with
  `key_scopes` limiting which keys each shared service may use. `peer_links`
  are what this server received: the share token (encrypted), the last
  manifest and the local services made from it (`services.peer_link_id`,
  `peer_remote_slug`, and `peer_provider`, the owner's provider slug).
  `peer_link_providers` records the provider specs imported through a link and
  their versions on both sides.
- **Provider refresh.** `refresh_runs` and their `refresh_items` record each
  run, `refresh_reviews` the proposals waiting for a superadmin, and
  `provider_refresh` when each provider was last checked and last changed.
- **`agent_runs.model`** keeps the model a builder run was queued with, so a
  model switch never changes a run in flight. **`ai_usage`** is the ledger of
  Claude calls the Usage page prices.
- **`audit`** records every dashboard change and sign in. **`meta`** holds
  small settings such as `master_key_check`, `usage_day`, the server identity
  (`server_identity_key`, encrypted, plus `server_name` and `server_url`), the
  model choice (`ai_models`), price overrides (`ai_prices`) and the refresh
  settings (`provider_refresh`).
- Foreign keys are on, so deleting a service removes its keys and leases, and
  deleting a user removes their sessions.
- The connection pool has one connection, with WAL mode and a 5 second busy
  timeout, which keeps SQLite simple under concurrent requests.

## In memory state

Almost all state is in SQLite. The exception is **concurrency slots**
(`internal/pool/slots.go`): how many requests are in flight on each key, which
leases hold a slot, and a wake channel per service for requests queued on a
busy service. Acnologia runs as a single instance, and on boot it rebuilds lease
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
    M->>M: access password from env, or access-password file when required
    M->>K: master key from ACNOLOGIA_MASTER_KEY or data/master.key
    Note over K: generated only if the file does not exist
    M->>S: open acnologia.db, apply schema and migrations, chmod 0600
    M->>S: compare meta.master_key_check
    alt key does not match the database
        M-->>M: refuse to start
    end
    M->>S: bootstrap superadmin from env (only if no users)
    M->>Cat: seed built in providers, load all specs
    M->>M: start AI builder (fail runs left over from a crash)
    M->>P: restore slots for open leases
    M->>W: start housekeeping, health, peer sync and refresh loops
    M->>H: listen on ACNOLOGIA_ADDR
```

## Background work

`internal/worker` runs four loops.

| Loop | Interval | Does |
|---|---|---|
| Housekeeping | 30 s | Resets daily usage at UTC midnight, frees slots of expired leases, reactivates exhausted keys whose quota reset time passed, and every 10 minutes prunes expired sessions, leases older than 7 days and events past retention. |
| Peers | 10 min | Reads the manifest of every share another server gave this one, importing the providers of shared services this server lacks, adding new services and disabling removed ones (`internal/peer`). |
| Refresh | 1 min tick | Asks the provider refresh whether a scheduled run is due and starts it in the background (`internal/refresh`). A run checks providers one by one and stops at its budget. |
| Health | 1 min tick | Re-checks keys whose last check is older than `ACNOLOGIA_HEALTH_INTERVAL` (exhausted keys hourly). Skips disabled keys, keys at their concurrency cap, providers without a health check, and hidden keys on services whose target is not trusted. |

Health checks run in their own loop so a slow provider never delays freeing
lease slots.

## Build and packaging

```mermaid
flowchart LR
    Web["web/ (Vite build)"] -->|"server/internal/webui/dist"| Embed["go:embed"]
    LLMS["docs/llms.txt,<br/>llms-full.txt"] -->|"make llms"| Embed2["internal/llmstxt"]
    MD["README.md, docs/*.md,<br/>screenshots"] -->|"make llms"| Embed3["internal/docsmd"]
    Embed --> Bin["bin/acnologia<br/>(static, CGO off)"]
    Embed2 --> Bin
    Embed3 --> Bin
    Bin --> Img["Docker image<br/>distroless, nonroot"]
```

The Dockerfile builds the UI with Node, the binary with Go (`CGO_ENABLED=0`,
pure Go SQLite), and copies the result into a distroless nonroot image with
`ACNOLOGIA_DATA_DIR=/data`. The image declares no volume: Compose and hosts
such as Railway mount their own at `/data`. On Windows, `scripts/build.ps1`
does the same build without make (see [development](development.md#windows)).

## Deployment shape

```mermaid
flowchart LR
    Internet((clients)) --> TLS["reverse proxy<br/>TLS (Caddy, Traefik,<br/>Cloudflare Tunnel)"]
    TLS --> C["acnologia :8080"]
    C --> V[("/data volume<br/>acnologia.db, master.key")]
    C --> Up["provider APIs"]
```

One instance per data directory. Put it behind TLS, set
`ACNOLOGIA_SECURE_COOKIES=true` and `ACNOLOGIA_TRUST_PROXY_HEADERS=true`, and keep the
master key out of the data volume (set `ACNOLOGIA_MASTER_KEY` from a secret store)
so a copy of the volume alone does not reveal stored keys.

See also: [request flows](flows.md), [security model](security.md),
[provider specs](providers.md).
