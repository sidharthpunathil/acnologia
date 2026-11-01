# Request flows

How the main paths through the server work, step by step. Diagrams use
Mermaid and render on GitHub.

- [Proxy request](#proxy-request)
- [Picking a key](#picking-a-key)
- [Lease and report](#lease-and-report)
- [Key states](#key-states)
- [Classifying an upstream response](#classifying-an-upstream-response)
- [Health checks](#health-checks)
- [Dashboard sign in](#dashboard-sign-in)
- [MCP](#mcp)
- [AI provider builder](#ai-provider-builder)
- [Assistant chat](#assistant-chat)

## Proxy request

`ANY /proxy/{service}/{path}`. The app never sees the key. Acnologia retries with
the next key when a response says the key itself is the problem.

```mermaid
sequenceDiagram
    autonumber
    participant A as App
    participant P as Proxy handler
    participant Pool as Pool
    participant U as Provider API
    participant DB as SQLite

    A->>P: GET /proxy/serpapi/search.json?q=x<br/>Authorization: Bearer cog_...
    P->>DB: look up app by token hash, check active
    P->>P: mode allows proxy? service allowed? method allowed?
    P->>P: read body (max COGITO_MAX_BODY_MB), validate path
    loop up to max_attempts keys
        P->>Pool: PickWait(service, app, exclude tried)
        alt every usable key at its concurrency cap
            Pool-->>P: wait up to queue_timeout_sec for a slot
        end
        alt no key at all
            Pool-->>P: NoKeyError
            P-->>A: 503 no_key_available / all_keys_busy + retry_after_sec
        end
        Pool-->>P: key + decrypted secret, slot held
        P->>P: build URL from parsed base_url + path,<br/>strip cog_ token, cookies, hop headers,<br/>inject key (query / header / bearer)
        P->>U: forward request (redirects not followed)
        U-->>P: response
        P->>DB: event (proxy, status, latency)
        alt status < 400
            P->>Pool: Apply(ok)
            P-->>A: stream response, key scrubbed from headers,<br/>sandbox CSP, X-Cogito-Key-Id, X-Cogito-Attempts
            Note over P: slot released when the body ends<br/>or the upstream is idle for 5 minutes
        else 4xx or 5xx
            P->>P: classify with the provider spec
            P->>Pool: Apply(outcome), release slot
            alt outcome rotates (rate_limited, quota_exhausted, invalid)
                Note over P: try the next key
            else ok or error
                P-->>A: relay this response
            end
        end
    end
    P-->>A: relay the last upstream response
```

Details worth knowing:

- The upstream URL is built from the parsed `base_url`, so an encoded `?` or
  `#` in the path stays in the path. Literal dot segments (`.`, `..`) are
  refused with 400 `bad_path`.
- `TRACE` and `CONNECT` are refused with 405.
- Redirects are passed back to the app, never followed with the key, and any
  copy of the key in a response header (for example a `Location` that kept the
  query string) is replaced with `[key]`.
- A 5xx is not retried on another key, so a non-idempotent request is never
  sent twice after the provider may have acted on it.
- If the network fails on a later attempt, the earlier provider answer is
  relayed instead of a bare 502.

## Picking a key

`internal/pool/pool.go` `Pick`, run under a per service lock.

```mermaid
flowchart TD
    Start([Pick for app on service]) --> Load["load keys with status active"]
    Load --> Each{for each key}
    Each --> Ex{"excluded, or restricted<br/>and app not listed?"}
    Ex -->|yes| Each
    Ex -->|no| Hid{"hidden key?"}
    Hid -->|"yes, and the app lacks lease_hidden<br/>or the service target is untrusted"| Each
    Hid -->|no or allowed| Avail{"cooling down or<br/>quota at zero?"}
    Avail -->|yes| Note1["remember earliest cooldown end"] --> Each
    Avail -->|no| Cap{"in-flight count at the<br/>key's concurrency limit?"}
    Cap -->|yes| Busy["busy = true"] --> Each
    Cap -->|no| Tier{"tier"}
    Tier -->|primary| Prim[primary set]
    Tier -->|backup| Back[backup set]
    Prim --> Each
    Back --> Each
    Each -->|done| Set{"primary set empty?"}
    Set -->|no| Choose
    Set -->|yes, backup has keys| Choose
    Set -->|both empty| NoKey["NoKeyError<br/>(Busy, RetryAfter)"]
    Choose["choose by strategy:<br/>round_robin, fill_first, least_used,<br/>most_quota, random (weighted)"] --> Dec["decrypt secret, mark used,<br/>acquire slot"]
    Dec --> Done([Picked])
```

## Lease and report

`POST /api/v1/lease` hands the plaintext key to the app; the app calls the
provider itself and reports what happened.

```mermaid
sequenceDiagram
    autonumber
    participant A as App
    participant C as Consumer API
    participant Pool as Pool
    participant DB as SQLite
    participant U as Provider API

    A->>C: POST /api/v1/lease {service, wait_sec, ttl_sec}
    C->>Pool: PickWait (hidden keys only if app.lease_hidden)
    Pool-->>C: key + secret, slot held
    C->>DB: insert lease (lse_..., expires_at)
    C->>Pool: slot now owned by the lease
    C-->>A: {lease_id, secret, auth {type, name, prefix}, base_url, expires_at}
    A->>U: call the provider with the key
    U-->>A: response
    A->>C: POST /api/v1/leases/{id}/report {outcome, retry_after_sec, message, rotate}
    C->>DB: lease belongs to app? within 10 min of expiry?<br/>app still allowed on the service?
    C->>DB: claim the lease (outcome set only if empty)
    C->>Pool: release the lease's slot
    C->>C: scrub the secret from message, cap retry_after at 24h
    C->>Pool: Apply(outcome)
    opt rotate and outcome is not ok
        C->>Pool: issue a new lease on another key
    end
    C-->>A: {ok: true, next: lease or null}
```

Unreported leases keep their slot until `expires_at`, when the worker frees it.
A report later than 10 minutes after expiry gets 410; a second report gets 409.

## Key states

`status` is stored; `cooling` is derived from `cooldown_until`.

```mermaid
stateDiagram-v2
    [*] --> active: key added
    active --> cooling: rate_limited<br/>(Retry-After, or cooldown x 2^fails, max 1h;<br/>any cooldown capped at 24h)
    cooling --> active: cooldown_until passes
    active --> exhausted: quota_exhausted, or health check<br/>shows zero remaining
    exhausted --> active: quota_reset_at passes (worker),<br/>or a good health check
    active --> invalid: invalid (401/403 class)
    invalid --> active: good health check,<br/>admin reset, or secret replaced
    active --> disabled: admin disables
    disabled --> active: admin enables
    exhausted --> disabled: admin disables
    invalid --> disabled: admin disables
```

An `error` outcome (5xx, network) records the error but does not change state.
Separately, each key reports **validity**: `invalid`, `valid` (it worked at
`last_ok_at`) or `unknown`.

## Classifying an upstream response

`internal/providers/engine.go` `Classify`. Provider rules come from the spec;
see [provider specs](providers.md).

```mermaid
flowchart TD
    R([status, headers, body]) --> Rules{"provider rules,<br/>first match wins"}
    Rules -->|match| Out["rule outcome<br/>+ retry_after from header, fixed,<br/>next_hour or body_json (max 24h)"]
    Rules -->|no match, use_standard_rules| Std{"standard rules"}
    Std -->|429| RL[rate_limited, honors Retry-After]
    Std -->|402| QE[quota_exhausted]
    Std -->|401, 403| INV[invalid]
    Std -->|none| Five{"status >= 500?"}
    Rules -->|"no match, no standard rules"| Five
    Five -->|yes| ERR[error]
    Five -->|no| OK[ok]
```

## Health checks

Run by the worker on an interval, after adding keys, or on demand from the
dashboard (one key, one service, or all keys).

```mermaid
sequenceDiagram
    autonumber
    participant W as Worker / dashboard
    participant Pool as Pool
    participant E as Engine
    participant U as Provider API
    participant DB as SQLite

    W->>Pool: Check(service, key)
    Pool->>Pool: provider has a health check?<br/>hidden key on an untrusted target? skip
    Pool->>E: spec.Check(base_url, secret)
    E->>U: health request (e.g. GET /account.json)<br/>redirects not followed
    alt network error
        U--xE: error
        Pool->>DB: last_error = scrubbed error (no key in it)
    else response
        U-->>E: status + body
        E-->>Pool: valid?, quota limit / remaining / reset, window cooldown
        Pool->>DB: update quota, status (revive or mark invalid / exhausted), last_ok_at
    end
```

## Dashboard sign in

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser
    participant S as Server
    participant DB as SQLite

    B->>S: GET /api/auth/state
    S-->>B: {setup_required, user}
    alt no users yet
        B->>S: POST /api/auth/setup {email, password}
        S->>S: hash password, then count and insert under a lock
        S->>DB: create superadmin
    else
        B->>S: POST /api/auth/login {email, password}
        S->>S: limiter: 10 failures / 10 min per client IP and per account
        S->>DB: load user, bcrypt compare<br/>(dummy hash when the user does not exist)
    end
    S->>DB: insert session (SHA-256 of a 32 byte token, 14 day TTL)
    S-->>B: Set-Cookie cogito_session (HttpOnly, SameSite=Lax, Secure on TLS)
    B->>S: later: POST /api/admin/... with Content-Type: application/json<br/>and X-Requested-With
    S->>S: jsonOnly (CSRF) then requireUser then requireRole
```

Sessions end on logout, after 14 days, when the user is disabled, when their
password is changed (other sessions) or reset by a superadmin (all sessions).

## MCP

`POST /mcp` speaks JSON-RPC over the MCP streamable HTTP transport with JSON
responses. Tools map onto the consumer API and the proxy, with the same app
token rules.

```mermaid
sequenceDiagram
    participant Ag as MCP client
    participant M as /mcp
    participant C as Consumer API
    participant P as Proxy

    Ag->>M: initialize (Authorization: Bearer cog_...)
    M-->>Ag: server info, capabilities
    Ag->>M: tools/list
    M-->>Ag: list_services, pool_status, get_key, report_key, proxy_request
    Ag->>M: tools/call get_key {service}
    M->>C: issue lease (same checks as /api/v1/lease)
    M-->>Ag: lease JSON
    Ag->>M: tools/call proxy_request {service, method, path}
    M->>P: run the proxy handler in process<br/>(response capped at 200 KB)
    M-->>Ag: status, headers, body
```

## AI provider builder

An admin starts a run with context, URLs and optionally a test key. The run
uses Claude with tools and ends in a published spec, a draft for review, or a
failure. Requires `ANTHROPIC_API_KEY`.

```mermaid
sequenceDiagram
    autonumber
    participant Ad as Admin
    participant API as /api/admin/ai
    participant R as Agent run (goroutine)
    participant Cl as Claude API
    participant T as Tools
    participant Cat as Catalog

    Ad->>API: POST /ai/runs {kind, context, urls, test_key, auto_publish}
    API->>R: queue (2 runs at a time)
    loop up to 40 turns
        R->>Cl: messages + tools
        Cl-->>R: thinking, text, tool calls<br/>(web_search and web_fetch run on Anthropic's side)
        R->>T: github_read / validate_spec / run_tests / live_check
        Note over T: live_check: GET examples only, 5 per run,<br/>test key only sent to the provider's own domain
        T-->>R: results
        R->>R: record every step for the live UI
        opt submit_provider (max 3)
            R->>R: re-validate and re-run fixture tests itself
        end
    end
    alt accepted and auto publish
        R->>Cat: publish new version (create run refuses an existing slug)
        R->>Cat: optionally create a service, fetch the logo
    else accepted, review needed
        R-->>Ad: status needs_review, publish from the dashboard
    end
```

Run states:

```mermaid
stateDiagram-v2
    [*] --> queued
    queued --> running
    running --> testing: live_check
    testing --> running
    running --> published: accepted, auto publish
    running --> needs_review: accepted, review required
    needs_review --> publishing: publish clicked (claimed atomically)
    publishing --> published
    publishing --> needs_review: publish failed
    queued --> cancelled
    running --> cancelled
    testing --> cancelled
    running --> failed: error, 40 turns, or 30 min timeout
```

Auto publish defaults to on for new providers and off for updates of existing
ones. The test key is encrypted while the run is active and wiped when it ends.

## Assistant chat

The Assistant page is a chat where Claude manages Acnologia for you. Every action
is a tool call that the server turns into a request to its own dashboard API,
**made as you** (your session), so it gets exactly your role, the same
validation, and the same audit log entries as clicking in the UI.

```mermaid
sequenceDiagram
    autonumber
    participant U as You
    participant C as /api/admin/chat
    participant Cl as Claude API
    participant D as Dashboard API (in process)
    participant DB as SQLite

    U->>C: POST /threads/{id}/messages<br/>"add these serpapi keys: sk_1 (alice@x.io), sk_2"
    C->>DB: load thread (yours only), decrypt
    loop until Claude answers (max 12 steps)
        C->>Cl: history + tools allowed for your role
        Cl-->>C: text and tool calls
        alt read or change tool
            C->>D: e.g. GET /services, POST /services/1/keys<br/>with your session cookie
            D-->>C: result (role checks, validation, audit)
            C->>C: trim the result for the model,<br/>keep app tokens for you only
        else delete or rotate tool
            C->>C: record a pending action,<br/>show a Confirm card, do nothing yet
        end
        C->>Cl: tool results
    end
    C->>C: replace pasted key secrets with their last<br/>four characters everywhere in the thread
    C->>DB: save thread encrypted
    C-->>U: new transcript items (and a new app token once, if any)
```

Typical "add keys" path the assistant follows:

```mermaid
flowchart TD
    Ask(["provider name + keys"]) --> LS[list_services]
    LS --> One{"services for<br/>that provider?"}
    One -->|one| Add[add_keys with account fields]
    One -->|several| Which[ask which service]
    One -->|none| LP[list_providers]
    LP --> Known{"provider known?"}
    Known -->|yes| CS[create_service] --> Add
    Known -->|no| Build["offer start_provider_build<br/>or a generic service"]
    Add --> Check["keys are health checked<br/>in the background"]
```

Confirmations:

```mermaid
stateDiagram-v2
    [*] --> waiting: a delete or rotate_app_token<br/>tool is called
    waiting --> approved: you press Confirm<br/>(the action runs now)
    waiting --> cancelled: you press Cancel
    waiting --> expired: you send another message
```

Tools by role:

| Role | Tools |
|---|---|
| member and up | overview, list_providers, get_provider, list_services, list_keys, key_details, list_apps, app_usage, list_events, list_webhooks, list_egress_proxies, list_groups, service_breaker, cache_stats, app_mcp_settings, get_ai_run |
| admin and up | create_service, update_service, add_keys, update_key, check_keys, reset_key, create_app, update_app (including limits), create_webhook, update_webhook, test_webhook, create_egress_proxy, update_egress_proxy, test_egress_proxy, create_group, update_group, reset_breaker, purge_cache, update_app_mcp, start_provider_build |
| admin and up, after Confirm | delete_key, delete_service, delete_app, rotate_app_token, delete_webhook, delete_egress_proxy, delete_group, enable_app_mcp_manage |

There is no tool to reveal a stored key, manage users or read the audit log.
New app tokens and webhook signing secrets are shown to you once and never
sent to Claude; egress proxy URLs are redacted from the stored chat like keys.
