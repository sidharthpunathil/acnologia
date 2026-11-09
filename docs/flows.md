# Request flows

Each section below walks through one path in the server: who starts it, what
happens in order, and what the caller gets back. The diagrams use Mermaid and
render on GitHub.

- [Dashboard sign in](#dashboard-sign-in)
- [Adding keys](#adding-keys)
- [Proxy request](#proxy-request) and the [forward proxy](#forward-proxy-request)
- [Picking a key](#picking-a-key)
- [Lease and report](#lease-and-report)
- [Key states](#key-states)
- [Classifying an upstream response](#classifying-an-upstream-response)
- [Health checks](#health-checks)
- [MCP](#mcp) and the [control MCP](#control-mcp)
- [Choosing the AI model](#choosing-the-ai-model)
- [AI provider builder](#ai-provider-builder)
- [Keeping provider offers fresh](#keeping-provider-offers-fresh)
- [Assistant chat](#assistant-chat) and its [compaction](#compaction)
- [Sharing between servers](#sharing-between-servers) and [with people](#sharing-with-people)

The examples use a made up `weather` service. Swap in any service slug you
have.

## Dashboard sign in

Who: a person in a browser. The first visit creates the superadmin; every
later visit signs in.

1. The browser asks `GET /api/auth/state` whether setup is still needed.
2. With no users yet, `POST /api/auth/setup` creates the superadmin. The
   server hashes the password, then counts and inserts under a lock, so two
   setup calls cannot both win.
3. Otherwise `POST /api/auth/login` checks the password with bcrypt. An
   unknown email is compared against a dummy hash, so timing does not reveal
   which accounts exist. After 10 failures in 10 minutes, per client IP and
   per account, the limiter refuses further tries.
4. The server stores a session (the SHA-256 of a 32 byte token, valid 14
   days) and sets the `acnologia_session` cookie.
5. Every later change sends `Content-Type: application/json` and
   `X-Requested-With`. The server runs `jsonOnly` (the CSRF check), then
   `requireUser`, then `requireRole`.

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
    S-->>B: Set-Cookie acnologia_session (HttpOnly, SameSite=Lax, Secure on TLS)
    B->>S: later: POST /api/admin/... with Content-Type: application/json<br/>and X-Requested-With
    S->>S: jsonOnly (CSRF) then requireUser then requireRole
```

A session ends on logout, after 14 days, or when the user is disabled.
Changing your own password ends your other sessions; a superadmin resetting
it ends all of them.

## Adding keys

Who: an admin, on a service page, through the Assistant, or with
`POST /api/admin/services/{id}/keys`.

1. The admin sends up to 5000 keys, either as a list (`keys`, each with its
   own label, tier, limits and account fields) or as pasted `text`, which the
   server parses into keys and labels.
2. The server validates every key before it stores any. Only a superadmin
   may mark a key `hidden`.
3. Each secret is encrypted and fingerprinted. A key already in the service
   is skipped and reported in `skipped` by line, so pasting the same list
   twice adds nothing.
4. The server writes the audit entry `key.create` and answers 201 with the
   new keys.
5. In the background it health checks the new keys, so their validity and
   quota show up without a manual click.

If the service's base URL was set by someone other than a superadmin, new
hidden keys stay unused until a superadmin saves the service's settings, and
the answer carries a `warning` saying so.

## Proxy request

Who: an app, script or agent holding an `acn_` token. It calls
`ANY /proxy/{service}/{path}`, and Acnologia attaches a key and forwards the
request. The app never sees the key. When a response says the key itself is
the problem, Acnologia retries on the next key.

1. The proxy looks up the app by its token hash and checks that it is
   active, that its mode allows proxying, and that the service and method
   are allowed.
2. It reads the body (up to `ACNOLOGIA_MAX_BODY_MB`) and validates the path.
3. It asks the pool for a key, excluding keys already tried. When every
   usable key is at its concurrency cap, it waits up to `queue_timeout_sec`
   for a slot. With no key at all, the app gets 503 `no_key_available` or
   `all_keys_busy` with `retry_after_sec`.
4. It builds the upstream URL, strips the `acn_` token, cookies and hop
   headers, injects the key (as a query parameter, a header or a bearer
   token) and forwards the request without following redirects.
5. A status below 400 streams back to the app with the key scrubbed from the
   headers, a sandbox CSP, `X-Acnologia-Key-Id` and `X-Acnologia-Attempts`.
6. A 4xx or 5xx is classified with the provider spec. Outcomes that blame the
   key (`rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned`) move
   on to the next key, up to `max_attempts`. Any other answer is relayed as
   it is.

```mermaid
sequenceDiagram
    autonumber
    participant A as App
    participant P as Proxy handler
    participant Pool as Pool
    participant U as Provider API
    participant DB as SQLite

    A->>P: GET /proxy/weather/v1/forecast?city=Lisbon<br/>Authorization: Bearer acn_...
    P->>DB: look up app by token hash, check active
    P->>P: mode allows proxy? service allowed? method allowed?
    P->>P: read body (max ACNOLOGIA_MAX_BODY_MB), validate path
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
        P->>P: build URL from parsed base_url + path,<br/>strip acn_ token, cookies, hop headers,<br/>inject key (query / header / bearer)
        P->>U: forward request (redirects not followed)
        U-->>P: response
        P->>DB: event (proxy, status, latency)
        alt status < 400
            P->>Pool: Apply(ok)
            P-->>A: stream response, key scrubbed from headers,<br/>sandbox CSP, X-Acnologia-Key-Id, X-Acnologia-Attempts
            Note over P: slot released when the body ends<br/>or the upstream is idle for 5 minutes
        else 4xx or 5xx
            P->>P: classify with the provider spec
            P->>Pool: Apply(outcome), release slot
            alt outcome rotates (rate_limited, busy, quota_exhausted, invalid, banned)
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
- `TRACE` is refused with 405. `CONNECT` never reaches this route: the
  [forward proxy](#forward-proxy-request) answers it first.
- Redirects go back to the app and are never followed with the key. Any copy
  of the key in a response header (a `Location` that kept the query string,
  for example) is replaced with `[key]`.
- A 5xx is not retried on another key, so a non-idempotent request is never
  sent twice after the provider may have acted on it.
- If the network fails on a later attempt, the app gets the earlier
  provider answer instead of a bare 502.

## Forward proxy request

Who: an app that sets Acnologia as its `HTTPS_PROXY` (or `HTTP_PROXY`), with
its `acn_` token in the proxy URL (`http://app:acn_...@host:8888`). The app
calls provider URLs as they are, such as
`https://serpapi.com/search.json?q=coffee`, and needs no SDK.

1. The client sends `CONNECT serpapi.com:443` with
   `Proxy-Authorization`. Acnologia checks the token as `/proxy` does: no
   token or a wrong one gets 407 `proxy_auth_required`, a lease only app 403
   `proxy_not_allowed`.
2. When the host belongs to a service (its base URL, or a provider host from
   the spec's `hosts` list), Acnologia answers `200 Connection Established`
   and completes the TLS handshake itself, with a certificate for that host
   signed by the server's own CA. The app trusts that CA from
   `GET /api/v1/proxy/ca.pem`.
3. Each request inside the tunnel is matched to a service by host and path:
   the longest matching base path wins, `X-Acnologia-Service` overrides, and
   a provider host becomes the `/~name` path.
4. The request is handed to `/proxy/{service}/{path}` with the app's token,
   and runs the [proxy request](#proxy-request) flow unchanged: key picked
   and injected (replacing any key the app sent), rotation, app limits,
   cache, events and `X-Acnologia-*` headers.
5. For a host that is no service, Acnologia opens a blind tunnel to it
   instead: no TLS interception, no key, public addresses only, port 443 or
   8443, and only when `ACNOLOGIA_FORWARD_PASSTHROUGH` is on and the token is
   not a share's. Otherwise the app gets 403 `not_a_service`.

A plain `http://` URL skips `CONNECT`: the absolute URL in the request line
is routed the same way, and plain HTTP to a host that is no service is
always refused, so the proxy is never an open relay.

```mermaid
sequenceDiagram
    autonumber
    participant A as App
    participant F as Forward proxy
    participant CA as Proxy CA
    participant P as /proxy pipeline
    participant U as Provider API
    participant O as Other host

    A->>F: CONNECT serpapi.com:443<br/>Proxy-Authorization: Basic app:acn_...
    F->>F: token valid? mode allows proxy?
    alt no token or wrong token
        F-->>A: 407 proxy_auth_required + Proxy-Authenticate
    end
    alt host belongs to a service
        F-->>A: 200 Connection Established
        A->>F: TLS ClientHello (SNI serpapi.com)
        F->>CA: leaf certificate for serpapi.com (minted once, cached)
        CA-->>F: cert signed by the Acnologia CA
        F-->>A: TLS handshake (app trusts acnologia-ca.pem)
        A->>F: GET /search.json?q=coffee&api_key=dummy
        F->>F: route by host and path (longest base path,<br/>X-Acnologia-Service overrides)
        F->>P: GET /proxy/serpapi/search.json?q=coffee&api_key=dummy<br/>X-Acnologia-Token: acn_...
        P->>P: pick key, replace api_key with the pool key
        P->>U: GET https://serpapi.com/search.json?q=coffee&api_key=<pool key>
        U-->>P: response
        P-->>F: response + X-Acnologia-* headers
        F-->>A: response over the tunnel
    else other host, passthrough on, port 443 or 8443
        F->>O: TCP connect (public addresses only)
        F-->>A: 200 Connection Established
        A->>O: end to end TLS, no key, not opened
    else other host, passthrough off or share token
        F-->>A: 403 not_a_service
    end
```

## Picking a key

Who: the pool, inside the proxy, a lease or an MCP call. The code is
`internal/pool/pool.go` `Pick`, run under a per service lock.

1. Load the service's keys with status `active`.
2. Skip a key that is excluded, restricted to other apps, or hidden when the
   app lacks `lease_hidden` or the service target is untrusted.
3. Skip a key that is cooling down or has zero quota left, remembering the
   earliest cooldown end for `retry_after_sec`.
4. Skip a key whose in-flight count is at its concurrency limit, and note
   that the pool is busy.
5. Sort the rest into primary and backup keys. Backup keys are used only
   when no primary key is left.
6. Choose by the service's strategy, decrypt the secret, mark the key used
   and take a slot. With nothing left, return `NoKeyError` with `Busy` and
   `RetryAfter`.

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

Who: an app in mode `lease` or `both` that needs the plaintext key, for
example for a client library that must hold it.

1. The app calls `POST /api/v1/lease` with the service, how long to wait for
   a key and how long to hold it.
2. The pool picks a key (hidden keys only if the app has `lease_hidden`) and
   the slot it holds now belongs to the lease.
3. The app gets the lease id, the secret, how to attach it (`auth`), the base
   URL and `expires_at`, and calls the provider itself.
4. The app reports the outcome on `POST /api/v1/leases/{id}/report`. The
   server checks the lease is the app's, is no more than 10 minutes past
   expiry, and that the app is still allowed on the service.
5. The server claims the lease (only an empty outcome can be set), releases
   the slot, scrubs the secret from the message, caps `retry_after_sec` at 24
   hours and applies the outcome to the key.
6. With `rotate` and an outcome other than `ok`, the answer carries a new
   lease on another key in `next`.

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

A lease that is never reported keeps its slot until `expires_at`, when the
worker frees it. A report more than 10 minutes after expiry gets 410; a
second report gets 409.

## Key states

`status` is stored. `cooling` is derived from `cooldown_until`.

```mermaid
stateDiagram-v2
    [*] --> active: key added
    active --> cooling: rate_limited<br/>(Retry-After, or cooldown x 2^fails, max 1h,<br/>any cooldown capped at 24h)
    cooling --> active: cooldown_until passes
    active --> exhausted: quota_exhausted, or health check<br/>shows zero remaining
    exhausted --> active: quota_reset_at passes (worker),<br/>or a good health check
    active --> invalid: invalid (401/403 class)
    invalid --> active: good health check,<br/>admin reset, or secret replaced
    active --> banned: banned (account suspended)
    banned --> active: admin reset
    active --> disabled: admin disables
    disabled --> active: admin enables
    exhausted --> disabled: admin disables
    invalid --> disabled: admin disables
```

The provider's rules sort each error response into an outcome, and each
outcome has its own recovery:

| Outcome | Typical cause | What happens to the key |
|---|---|---|
| `rate_limited` | 429, an hourly cap, `Retry-After` | Rests for as long as the provider asked, or the service cooldown doubled per consecutive failure up to an hour. The next key takes the request. |
| `busy` | the account is at its concurrency cap | Steps aside for about a second, without backoff or a failure count. |
| `quota_exhausted` | out of searches, characters or credits | Parked as `exhausted` until its quota resets (or 24 hours), then back on its own. |
| `invalid` | the provider rejects the key | Out of rotation until a health check passes. |
| `banned` | the provider suspended the account | Out until an admin resets it. Health checks skip it. |
| `error` | 5xx, timeouts, refused connections | Not penalized. Errors count toward the service's circuit breaker: 10 in 60 seconds open it, requests then get 503 `service_unavailable` at once, and one probe goes through every 30 seconds until the provider recovers. |

An `error` outcome records the error and leaves the key's state alone.
Separately, each key reports its **validity**: `invalid`, `valid` (it worked
at `last_ok_at`) or `unknown`.

## Classifying an upstream response

Who: the engine, for every upstream answer of 400 or more that the proxy
gets. The code is `internal/providers/engine.go` `Classify`; the rules come
from the [provider spec](providers.md).

1. The provider's own rules run in order and the first match wins. A rule
   can set `retry_after` from a header, a fixed value, the next hour or a
   JSON body field, capped at 24 hours.
2. With no match and `use_standard_rules` on: 429 is `rate_limited` (honoring
   `Retry-After`), 402 is `quota_exhausted`, 401 and 403 are `invalid`.
3. Anything still unmatched is `error` at 500 or above, else `ok`.

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

Who: the worker on an interval, the server right after keys are added, or an
admin from the dashboard (one key, one service, or all keys).

1. The pool skips the check when the provider has none, or when the key is
   hidden and the service target is untrusted.
2. The engine sends the spec's health request (for example
   `GET /account.json`) without following redirects.
3. On a network error, the key's `last_error` is set to the error with the
   key scrubbed out.
4. On a response, the engine reads validity, the quota limit, remaining and
   reset, and any window cooldown. The pool saves the quota, revives the key
   or marks it invalid or exhausted, and sets `last_ok_at`.

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

## MCP

Who: an AI agent or MCP client holding an app token. `POST /mcp` speaks
JSON-RPC over the MCP streamable HTTP transport with JSON responses. Its
tools map onto the consumer API and the proxy, with the same token rules.

An admin picks each app's tool sets in the dashboard (`GET`/`PUT
/api/admin/apps/{id}/mcp`):

- **core**: `list_services`, `pool_status`, `service_guide`, `get_key`,
  `renew_key`, `report_key`, `proxy_request`.
- **service tools**: generated from each provider's examples and named
  `<slug>_<example>`.
- **insights** (read only): `list_keys`, `key_details`, `service_stats`.
- **manage**: `check_keys`, `set_key_status`, `add_keys`. `check_keys` needs a
  service and checks only that service's keys.

Core, service tools, resources and prompts are on by default. When two
services would produce the same tool name, this server's own services keep it
(oldest first) and the later one gets a short suffix. A service shared by
another server therefore never takes a local tool's name or a built in one.
Clients that only speak stdio can bridge with `acnologia mcp --url ...
--token acn_...`.

A typical session:

1. The client initializes with `Authorization: Bearer acn_...`. The
   instructions tell the agent to read `service_guide` before calling a
   service it has not used.
2. `tools/list` returns the tools this app's settings allow.
3. `service_guide` returns the usage guide as Markdown: endpoints, params,
   limits and errors.
4. `proxy_request` runs the proxy handler in process, with the response
   capped at `max_result_kb` (default 200). A JSON answer within the cap is
   embedded as sent, so large integers keep every digit. `get_key` issues a
   lease with the same checks as `/api/v1/lease`.

The guide is also the resource `acnologia://services/{slug}/guide` (one per
allowed service, `text/markdown`) and, over HTTP,
`GET /api/v1/services/{slug}/guide`. All three apply the proxy's allow check,
so an app, or a share's token, reads guides only for services it may call.

```mermaid
sequenceDiagram
    participant Ag as MCP client
    participant M as /mcp
    participant C as Consumer API
    participant P as Proxy

    Ag->>M: initialize (Authorization: Bearer acn_...)
    M-->>Ag: server info, capabilities
    Ag->>M: tools/list
    M-->>Ag: the tools this app's settings allow
    Ag->>M: tools/call service_guide {service}
    M-->>Ag: the usage guide as Markdown<br/>(endpoints, params, limits, errors)
    Ag->>M: tools/call get_key {service}
    M->>C: issue lease (same checks as /api/v1/lease)
    M-->>Ag: lease JSON
    Ag->>M: tools/call proxy_request {service, method, path}
    M->>P: run the proxy handler in process<br/>(response capped at max_result_kb, default 200)
    M-->>Ag: status, headers, body
```

## Control MCP

Who: a signed in user's MCP client, operating the server. `POST /mcp/control`
takes an admin API token (`acnadm_...`) and nothing else.

1. On initialize, the server finds the token's owner and caps their role at
   the token's scope.
2. `tools/list` returns the tools that role and the endpoint options
   (`groups`, `read_only`, `exclude`) allow.
3. Each tool call becomes an in process request to an existing `/api/admin`
   route with the same token, so the route checks the role, validates the
   input and writes the audit entry.
4. A destructive tool first answers with what it would do (for example the
   service and how many keys it holds) and acts only when called again with
   `confirm: true`.
5. `call_service` goes through the proxy as the `Control MCP` app instead,
   so the key never leaves the server.

See [Control MCP](api.md#control-mcp-mcpcontrol) for the tools and options.

```mermaid
sequenceDiagram
    participant Cl as MCP client (Claude)
    participant M as /mcp/control
    participant R as /api/admin route
    participant P as Proxy

    Cl->>M: initialize (Authorization: Bearer acnadm_...)
    M->>M: token owner, role capped at the token scope
    M-->>Cl: acnologia-control, instructions
    Cl->>M: tools/list (?groups=, read_only=, exclude=)
    M-->>Cl: tools this role and these options allow
    Cl->>M: tools/call delete_service {service: "weather"}
    M->>R: GET /api/admin/services/{id} (same token)
    M-->>Cl: would delete weather and its 12 keys, call again with confirm
    Cl->>M: tools/call delete_service {service, confirm: true}
    M->>R: DELETE /api/admin/services/{id} (same token, X-Requested-With)
    R->>R: role check, audit
    R-->>M: {"ok": true}
    M-->>Cl: result (structuredContent, capped at 512 KB)
    Cl->>M: tools/call call_service {service, path, query}
    M->>P: run the proxy as the Control MCP app
    P-->>M: provider answer (key injected, rotated on 429)
    M-->>Cl: status, headers, body
```

## Choosing the AI model

The AI features are the Assistant, the provider builder, Fill with AI, chat
summaries (used by [compaction](#compaction)) and offer refresh. Each picks
its model at call time, so a change in Settings, AI applies to the next call
without a restart. The first of these that is set wins:

1. the feature's own model, saved under Settings, AI, "Use a different model
   for...",
2. the saved default model from the same card,
3. `ACNOLOGIA_AI_MODEL`,
4. the built in default, `claude-opus-5-5`.

The card shows each feature's effective model and where it came from. Only a
superadmin saves models (`PUT /api/admin/ai/models`, audited as `ai.models`);
admins can see them.

The picker merges Anthropic's Models API (cached for an hour) with
Acnologia's built in table and shows each model's price per million tokens
and its context window. A model id is accepted when it is in either list or
already in use. A model with no known price is accepted with a warning, and
its cost is estimated at `claude-opus-5-5` prices.

Smaller models such as Haiku suit Fill with AI, chat summaries and offer
refresh. The builder writes better specs on a larger one. The Assistant's
model also decides when a chat compacts, since the threshold is a share of
its context window.

**Model prices.** The Usage page prices every Claude call from a built in
table. Under Settings, AI, Model prices, a superadmin can override a model's
input, output and cache prices and its context window
(`PUT /api/admin/ai/prices/{model}`), or reset it to the built in values
(`DELETE`). Each change is audited as `ai.prices`. The
[provider refresh](#keeping-provider-offers-fresh) can propose new prices
too. An override changes how cost is counted from then on and leaves past
usage as it was. A context window override moves the compaction threshold.

## AI provider builder

Who: an admin. Needs `ANTHROPIC_API_KEY`.

1. The admin starts a run with `POST /api/admin/ai/runs`: the kind, context,
   URLs, an optional test key, and whether to publish on success.
2. The run is queued. Two runs go at a time.
3. For up to 40 turns, Claude reads the docs (web search and web fetch run
   on Anthropic's side) and calls the builder's tools: `github_read`,
   `validate_spec`, `run_tests` and `live_check`. `live_check` runs GET
   examples only, 5 per run, and sends the test key only to the provider's
   own domain. Every step is recorded for the live view.
4. The agent may call `submit_provider` up to 3 times. The harness itself
   re-validates the spec, re-runs the fixture tests and requires a usage
   guide with endpoints.
5. An accepted spec with auto publish is published as a new version (a
   create run refuses an existing slug), optionally with a new service and
   its logo. Without auto publish the run waits as `needs_review` until an
   admin publishes it from the dashboard.

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
            R->>R: re-validate and re-run fixture tests itself<br/>and require a usage guide with endpoints
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

Auto publish defaults to on for new providers and off for updates of
existing ones. The test key is encrypted while the run is active and wiped
when it ends. Every request to Claude passes the redactor first, so the model
sees the test key only as a placeholder such as `[key ...c21e]` (see
[security](security.md#what-the-model-can-and-cannot-see)).

Every submit must carry a `guide` (summary, endpoints with params, notes)
written from the docs the agent read; see
[usage guides](providers.md#usage-guides). A missing guide, or one without
endpoints, goes back to the agent like a failing test. On the last allowed
submit the run is accepted with a note instead, so a provider that otherwise
works does not fail over its guide.

**Writing just the guide.** `POST /api/admin/providers/{slug}/guide/generate`
(admin; also the Assistant's `write_provider_guide`) runs a short synchronous
loop on the builder's model: up to 12 turns, 4 web searches and 10 fetches,
within 5 minutes. It sends the provider's public metadata, examples, offers
and current guide through the redactor, asks for a guide, validates it, and
saves only the `guide` key of the spec as a new provider version (source
`ai`, audit `provider.guide.generate`). The answer is `{provider, updated,
verified, summary, guide, markdown, model, cost_usd}`; `updated: false`
means the model could not read the docs and nothing changed.

An admin can delete finished runs: `DELETE /api/admin/ai/runs/{id}` for one
(409 `run_busy` while it is queued, running, testing or publishing), or
`DELETE /api/admin/ai/runs` for every failed, cancelled and published run.
The bulk clear keeps runs waiting for review.

## Keeping provider offers fresh

Offers (free tiers, prices, published rate limits) feed the pacer, cost
accounting and every place that shows what is free. Prices change, so a
superadmin can have Acnologia re-read them. Settings, Provider refresh holds
the controls, the history and the changes waiting for review. It needs
`ANTHROPIC_API_KEY`; with AI off nothing runs.

Who: the worker on a schedule, or a superadmin by hand.

**1. A run starts** in one of three ways:

- On the schedule: off, weekly, every 2 weeks, monthly (on the 1st, the
  default) or every N days (1–365), at a chosen UTC hour (03:00 by default).
  The worker asks once a minute whether a run is due. The first run comes the
  first time the clock reaches the hour after the schedule was saved. Weekly
  and every N days count from the day of the last scheduled run; monthly runs
  on the 1st of the next month. A run missed while the server was off starts
  as soon as it is back.
- Run refresh now in Settings (superadmin): the whole scope, same rules.
- Check now on the service page's Offers and limits card or the provider's
  Offers tab (superadmin): one provider, whatever the scope and staleness
  say.

**2. The run picks providers.** The scope is one of: providers behind at
least one service with a usable key (active, cooling or out of quota; the
default), providers behind any service, or every provider. Always check adds
providers whatever the scope; Never check removes them and wins over Always
check. Then:

- Providers copied from another server (source `peer:...`) are skipped unless
  you turn them on. Their owner keeps them current and your copy follows
  theirs.
- Disabled providers are skipped unless listed under Always check.
- A provider whose offers were read within the last N days (default 25,
  counted from its last refresh check or the newest `checked_at` on its
  offers) is skipped.
- The rest go oldest checked first, at most N per run (default 10).

**3. The model checks each provider.** The offer refresh model (see
[Choosing the AI model](#choosing-the-ai-model); until you set one it is the
default model, so pick a small one such as Haiku) gets the provider's name,
base URL, website, docs URL, each offer's `source_url`, its example names and
its current offers. Keys never leave the server, and every request passes the
same redaction as the other AI features. The model may run 3 web searches and
5 page fetches within 8 turns, then must call `submit_offers` with the full
list and `verified`. The offers are validated like any spec edit, and a
failure goes back to the model once. Each call is recorded in the AI usage
ledger under the `refresh` feature, with the run as its ref.

**4. The result is compared** with the current offers by name, ignoring
`checked_at`:

- **No change.** `checked_at` moves to today on the current version, in place,
  with no new version, so a built in provider stays unedited and keeps
  getting the offers new releases ship. The stored order and names stay as
  they are, since order decides which offer prices a request.
- **A small, sourced change** with apply set to Apply sourced changes (the
  default) publishes on its own. That means every change only moves existing
  offers' prices within 4x either way (same unit and `per_request`) or
  loosens their limits and concurrency, and every changed offer cites an
  http(s) `source_url` on the provider's own website, docs or API domain. A
  new provider version is published with source `refresh` and a note listing
  what changed. Only `offers` change: auth, the base URL, rules, health
  checks and tests are never touched. The webhook `provider.offers_changed`
  fires and the audit log records it. A built in provider changed this way
  becomes an edited version, like any edit, so later releases no longer
  backfill its offers. Roll back from the provider's Versions tab.
- **Anything else waits for review** in Settings, under Waiting for review,
  with a readable diff. That covers a missing source or one on another site,
  an added or removed offer, a changed match, notes or free flag, a tighter
  limit, a bigger price move, apply set to Always review, and a failed
  publish. Apply publishes it as above; Dismiss drops it. A proposal made
  against a version that has since changed cannot be applied: dismiss it and
  check again. A newer proposal for the same provider replaces an older
  pending one.
- **A failure** (the model could not read the pages, the call failed, or the
  offers did not validate) changes nothing. The provider is not counted as
  checked, so the next run tries again. One provider failing never stops the
  others.

**Claude's own prices.** With Claude model prices on (the default), every run
also re-reads Anthropic's pricing page for the models Acnologia prices and
proposes new input, output and cache prices. Its web search and fetch may
only read anthropic.com and claude.com. A change saves as price overrides on
its own only when every changed price cites an https page on those sites and
moves within 4x either way; otherwise it waits for review. The model picker
shows each price's source and the date it was checked. Superadmins can also
edit a model's price and context window by hand under Settings, AI, Model
prices, and reset it to the built in table (see
[Model prices](#choosing-the-ai-model)).

**Budget and safety.** Before each provider, a run stops if it has spent its
per run cap (default $2) or the refresh feature has spent its monthly cap
(default $5) since the 1st, UTC. A check that crosses the cap partway through
stops there. Providers it did not reach are marked Not reached. One run goes
at a time: Run now and Check now answer 409 while one is running. A run's
state lives in the database, so after a restart it resumes on the next tick,
and the provider that was being checked starts over.

**Where to see it.** The service page's Offers and limits card says when the
offers were checked and by what (scheduled refresh, a refresh by hand, Check
now, or when the provider was written), when a refresh last changed them
(with a link to that version), and whether a change waits for review.
Settings, Provider refresh shows the model, the last and next run, the
month's spend against its cap, recent runs with each provider's outcome and
cost, and the reviews. Settings changes, runs started by hand, applies and
dismissals are audited (`provider.refresh.*`, `ai.prices`).

## Assistant chat

Who: any signed in user, on the Assistant page. Claude manages Acnologia for
you there. Every action is a tool call that the server turns into a request
to its own dashboard API, **made as you** (with your session). It gets exactly
your role, the same validation and the same audit entries as a click in the
UI.

1. You send a message with `POST /threads/{id}/messages`. The server loads
   the thread (yours only) and decrypts it.
2. Before anything reaches Claude, the server finds pasted keys (the paste
   parser first, then patterns), encrypts them into the thread's vault and
   puts placeholders such as `[key k1 ...c21e]` in the text.
3. For up to 24 turns, Claude answers with text and tool calls, limited to
   the tools your role allows. A read or change tool calls the dashboard API
   with your session; the result is trimmed and redacted for the model, and
   app tokens are kept for you only. A delete or rotate tool records a
   pending action and shows a Confirm card instead of acting.
4. The thread is saved encrypted. Your message is stored as prose plus
   `key_list` (each key's label and last four characters).
5. You get the new transcript items, and a new app token once, if one was
   made.

```mermaid
sequenceDiagram
    autonumber
    participant U as You
    participant C as /api/admin/chat
    participant Cl as Claude API
    participant D as Dashboard API (in process)
    participant DB as SQLite

    U->>C: POST /threads/{id}/messages<br/>"add these weather keys: sk_1 (alice@x.io), sk_2"
    C->>DB: load thread (yours only), decrypt
    C->>C: find the keys (paste parser, then patterns),<br/>encrypt them into the thread vault,<br/>put [key k1 ...c21e] placeholders in the text
    loop until Claude answers (max 24 turns)
        C->>Cl: history + tools allowed for your role
        Cl-->>C: text and tool calls
        alt read or change tool
            C->>D: e.g. GET /services, POST /services/1/keys<br/>with your session cookie
            D-->>C: result (role checks, validation, audit)
            C->>C: trim and redact the result for the model,<br/>keep app tokens for you only
        else delete or rotate tool
            C->>C: record a pending action,<br/>show a Confirm card, do nothing yet
        end
        C->>Cl: tool results (every request passes the redactor)
    end
    C->>DB: save thread encrypted (your message stored as prose<br/>plus key_list: label and last four characters)
    C-->>U: new transcript items (and a new app token once, if any)
```

The path the Assistant usually takes to add keys:

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
| member and up | ask_user, search_providers, provider_offers, provider_guide, get_provider_spec, list_capabilities, wait_for_build, overview, list_providers, get_provider, list_services, list_keys, key_details, list_apps, app_usage, list_events, list_webhooks, list_egress_proxies, list_groups, service_breaker, cache_stats, app_mcp_settings, get_ai_run |
| admin and up | create_service, update_service, add_keys, update_key, check_keys, reset_key, create_app, update_app (including limits), test_webhook, test_egress_proxy, create_group, update_group, reset_breaker, purge_cache, update_app_mcp, start_provider_build, write_provider_guide, update_provider (base URL, auth, health, hosts or disabling after Confirm), improve_provider, find_logo, set_provider_logo, set_service_logo, save_capability, try_capability |
| admin and up, after Confirm | delete_key, delete_service, delete_app, rotate_app_token, delete_webhook, delete_egress_proxy, delete_group, delete_capability, enable_app_mcp_manage, create_webhook, update_webhook, create_egress_proxy, update_egress_proxy |
| admin and up, after Confirm for some inputs | update_app with mode or allowed_services, update_service with base_url, auth, provider or egress_id, update_key with egress_id |

`ask_user` shows a small form in the chat (name the pasted keys, pick one
of 2 to 8 choices, or pick a logo); the answer goes to `POST /threads/{id}/answer`.

**Logos.** `find_logo` looks on the websites or image links it is given
(the site a scraper reads, or the provider's own site) and keeps up to 6
candidates in the thread, graded vector, clear (128px or more), fair or
small. Claude sees each raster candidate as a picture in the tool result.
When one is clearly the right brand and sharp it sets it with
`set_service_logo` or `set_provider_logo`; when it is not sure it asks
with `ask_user` kind `logo`, a form of the pictures and "None of these".
The user can also paste a link, which the set tools download (a link with
no image extension is tried as an image before it is treated as a
website), or send an image, which they set by its chat image id.

The Assistant has no tool to reveal a stored key, manage users or read the
audit log. New app tokens and webhook signing secrets are shown to you once
and never sent to Claude. Egress proxy URLs are redacted from the stored chat
like keys.

In the dashboard, a message with pasted keys shows its prose first, then a
"keys secured" strip of chips (label and last four characters, never a
secret). The chat state lives in the page's store: the last open chat comes
back when you return from another tab, unsent drafts are kept per chat while
the page is open, and a turn still running is picked up again.

### Compaction

A long chat would eventually overflow the model's context window, and it
gets slower and more expensive on every turn. Before each model call the
server estimates the prompt: the input tokens Claude reported for the last
call plus about one token per four characters added since, or a character
estimate of the whole prompt, whichever is larger. Past 60% of the context
window of the Assistant's model (or `ACNOLOGIA_CHAT_COMPACT_TOKENS`), it
compacts:

1. Keep the last 6 messages verbatim, moving the cut back so a `tool_use` is
   never split from its `tool_result`.
2. One Claude call summarizes the older part (and any earlier summary) from
   the stored, already redacted history. It runs on the chat summaries model
   from Settings, AI, which can be smaller and cheaper than the Assistant's.
3. The summary is redacted and stored on the thread (text, messages covered,
   time, tokens saved), and the transcript gets an "Earlier messages
   summarized" item.
4. Claude is sent the summary plus the kept messages.

If the summary fails and the prompt is within 90% of the window, the history
is sent as it is. Past 90%, only the latest messages are sent, with a note
that older ones were left out.

```mermaid
flowchart TD
    Est["estimate the next prompt"] --> Over{"past the threshold?"}
    Over -->|no| Send["send the history"]
    Over -->|yes| Split["keep the last 6 messages verbatim;<br/>move the cut back so a tool_use<br/>is never split from its tool_result"]
    Split --> Sum["one Claude call summarizes the older part<br/>(and any earlier summary) from the stored,<br/>already redacted history"]
    Sum -->|ok| Store["redact the summary, store it on the thread<br/>(text, messages covered, time, tokens saved),<br/>add an 'Earlier messages summarized' item"]
    Store --> SendS["send summary + kept messages"]
    Sum -->|fails| Fall{"near the window?<br/>(90%)"}
    Fall -->|no| Send
    Fall -->|yes| Trim["send only the latest messages,<br/>with a note that older ones were left out"]
```

- Nothing is deleted. The stored messages and the transcript stay whole, so
  you still see every message above the divider. Only what Claude is sent
  changes.
- Repeated compaction folds the previous summary into the new one.
- The summary keeps goals, decisions, the names and ids of services, keys,
  apps and providers touched, pending tasks and open questions. Key
  placeholders stay as they are, and the summary passes the same redactor as
  tool results, so it never holds a secret.
- The summary call is billed as Assistant usage on the Usage page.
- A failed summary is logged and not retried in that turn; the chat carries
  on.
- The thread's menu (the dots at the top right of the chat) has **Compact
  now**, which calls `POST /chat/threads/{id}/compact` whatever the chat's
  size. Open the divider to read the summary.

## Sharing between servers

One Acnologia (the **owner**) can let another (the **receiver**) use some of
its services and give it provider specs. In the default proxy mode keys never
leave the owner: the receiver calls the owner's proxy with a share token, and
the owner picks and injects its own key. A share can instead hand the keys
over (see [Share modes](#share-modes-proxy-or-key-handover)). Pairing is by
invite only.

**Server identity.** Every server has an Ed25519 keypair made on first start
(the private key encrypted with the master key, in `meta`), an id `acn_srv_`
plus 20 characters derived from the public key, a name (the hostname until a
superadmin renames it) and a public URL (`ACNOLOGIA_PUBLIC_URL`, else the one
saved on the Sharing page, else the request origin). It is published at
`/.well-known/acnologia`. Set `ACNOLOGIA_PUBLIC_URL` on any server that
shares, so invites never carry an internal address.

Both sides work from the Sharing page. Only a superadmin changes anything
there; admins can look.

**Pairing, step by step:**

1. **Owner superadmin:** New share. Pick the services, any provider specs to
   give away, a key scope per service, limits, an optional expiry, whether
   hidden keys may serve it, and how long the invite lasts (1 hour, 24 hours
   by default, or 7 days).
2. **Owner server:** stores the share as pending, with a backing app in proxy
   mode, and keeps only the SHA-256 of the invite secret. The invite
   (`acninv_...`) is shown once. Until someone redeems it, New invite replaces
   it with a fresh one (409 `not_pending` after a redeem).
3. **Owner superadmin:** sends the invite to the other side privately.
4. **Receiver superadmin:** pastes the invite into Connect. The receiver
   fetches `/.well-known/acnologia` and `POST /api/peer/v1/preview`, and shows
   the owner's id, name, key fingerprint and what is shared.
5. **Receiver superadmin:** picks a slug prefix and says whether the owner is
   on a private network (which lets the calls reach a private address), then
   connects.
6. **Receiver server:** sends `POST /api/peer/v1/redeem` with its identity, a
   nonce and an Ed25519 signature.
7. **Owner server:** checks that the id matches the key, the signature is
   valid, and the invite is pending, unexpired and matches the stored hash.
   It binds the share to the receiver, burns the invite and mints a share
   token (stored as a hash), then returns the token, its identity and the
   manifest.
8. **Receiver server:** stores the token encrypted, imports the shared
   provider specs and the providers of shared services it lacks, and creates
   one local service per shared service (`prefix-slug`, pointing at the
   owner's `/proxy/slug`) with the token as a hidden key.

Editing a redeemed share reaches the receiver at its next sync.

```mermaid
sequenceDiagram
    autonumber
    participant SA as Owner superadmin
    participant A as Owner (A)
    participant B as Receiver (B)
    participant SB as Receiver superadmin

    SA->>A: New share: services, provider specs, limits, expiry
    A->>A: store share (pending) + backing app (proxy only),<br/>keep SHA-256 of the invite secret
    A-->>SA: invite acninv_... (shown once, 1h / 24h / 7d)
    SA-->>SB: send the invite privately
    SB->>B: paste invite
    B->>A: GET /.well-known/acnologia
    B->>A: POST /api/peer/v1/preview (invite)
    B-->>SB: owner id, name, fingerprint, what is shared
    SB->>B: Connect (prefix, private network?)
    B->>A: POST /api/peer/v1/redeem<br/>B's identity, nonce, Ed25519 signature
    A->>A: id matches key, signature valid,<br/>invite pending, unexpired, hash matches
    A->>A: bind share to B, burn invite,<br/>mint share token (stored as a hash)
    A-->>B: token, owner identity, manifest
    B->>B: store token encrypted, import shared provider specs<br/>and the providers of shared services B lacks,<br/>then create services prefix-slug -> A/proxy/slug<br/>with the token as a hidden key
```

**Using a shared service.** A local app on B calls `/proxy/a-weather/...` like
any other service.

1. B injects the share token as a bearer token and adds
   `X-Acnologia-Hop: n+1`.
2. A authenticates the token, checks that the share is active and unexpired,
   applies its limits and proxies with its own keys.
3. B classifies A's answer with the `acnologia-peer` provider, whose rules
   count only A's own refusals against the token:
   - 403 `share_revoked`, `share_paused` or `share_expired`, and 401, mark it
     invalid.
   - 429 `app_limit_reached` makes it exhausted until the reset.
   - 503 `no_key_available`, `all_keys_busy` or `service_unavailable` cool it
     for `retry_after_sec`.
   - Anything A relays from the upstream passes through untouched, since A
     already rotated.

Shared services are proxy only. A lease would hand out the token, so
`/api/v1/lease` answers 403 `lease_not_allowed` and MCP `get_key` is refused
the same way.

**Which keys.** Each shared service can be limited to all its keys, up to N
keys, or a picked list (`key_scopes`, see
[the API](api.md#sharing-between-servers)). A's pool applies the scope on
every pick for the share's backing app, using a per app cache that any share
change clears, so ordinary apps pay nothing for it. Up to N takes the first N
keys still in rotation by priority. When one turns invalid, banned or
disabled, the next one takes its place; a key that is only cooling down or
out of quota keeps its slot. The Sharing page shows each service as
`weather · 3 of 10 keys` and each set limit as a meter of today's or this
month's use against its cap.

**Limits.** A share's limits are its backing app's: requests a day, requests
a month, USD a day and USD a month, counted from the cost of A's own
requests. Past one, A answers 429 `app_limit_reached` until the window
resets, and B treats the token as out of quota until then. Once an expiry
date passes, A answers 403 `share_expired` and B's services stop. The
manifest tells B the limits, the expiry and how many keys can take a request
now (counted within the key scope). It never carries the keys.

**Sync.** B reads each link's manifest every 10 minutes and on Sync now.

- New services are created.
- A service no longer shared keeps its row (events and app settings survive)
  but its key is disabled.
- A status change applies at once.
- Providers sync before services, so a new service already has its provider
  when it is made.

**Providers of shared services.** The manifest carries the spec of every
provider a shared service uses (`shared: false`) next to the ones shared on
purpose (`shared: true`). When B has no provider with that slug, sync imports
it as a dependency of the services that need it. The service then keeps the
provider's name, logo, color, category, docs and website links, Integrate
examples, offers and MCP tools. The local service keeps provider
`acnologia-peer`, which carries the token and classifies its answers, and
records the owner's provider slug in `peer_provider`. The dashboard, the
Integrate tab, Copy for LLM, the catalog and MCP use `peer_provider` to
describe the service, with a "via <server>" marker. When B already has a
provider with that slug, B's own spec describes the service and is never
touched.

```mermaid
flowchart TD
    M([manifest from A]) --> P{"for each provider spec:<br/>shared, or used by a shared service?"}
    P -->|no| Skip[skip]
    P -->|yes| Known{"imported through<br/>this link before?"}
    Known -->|yes| Note["note A's newest version;<br/>never change the spec"]
    Known -->|no| Own{"B has that slug?"}
    Own -->|yes| Keep["keep B's own provider<br/>(reported only if shared on purpose)"]
    Own -->|no| Import["publish with source peer:A,<br/>record shared or dependency,<br/>copy A's logo"]
    Import -->|spec invalid| Err["last_error: provider ... (for service)"]
    Note --> S
    Keep --> S
    Import --> S
    Err --> S
    S([create or update services,<br/>peer_provider = A's provider slug])
```

Each import happens once, with source `peer:<owner id>`. Later edits on the
owner are never applied on their own, so an owner cannot redirect keys that B
adds to an imported provider. When A's version is newer than the one
imported, the service page says so and a superadmin can pull it (`POST
/api/admin/peers/{id}/providers/{slug}/pull`). A pull that moves the base URL
needs an explicit yes, and a provider edited on B since the import is not
offered updates. If an import fails (an invalid spec, for example), the
service is still created and works through the proxy, the reason goes in the
link's `last_error`, and the next sync tries again. Re-syncing an unchanged
manifest changes nothing.

**Logos.** After importing a provider that has no logo, B asks A for it
(`GET /api/peer/v1/providers/{slug}/logo` with the share token, sent only to
A's URL). A serves logos only for providers in that share's manifest. B
accepts an allowed image type no larger than 512 KB whose bytes match the
declared type, stores it like any provider logo, and retries at each sync
while the provider has none. When A has no logo, B tries the catalog's logo
discovery on the provider's website once (public addresses only, no token).

**Offers.** A peer service shows the owner provider's offers for reference,
but they do not pace the share token or price its requests. B's one key is
the token, which stands for all of A's keys, and A already paces its own keys
under those limits and enforces the share's request and budget caps.

**Revoke, pause, expire.**

- **Revoke** deactivates the backing app and revokes its token in the same
  transaction, so the very next request through A gets 403 `share_revoked`
  with `X-Acnologia-Share: revoked`. B marks the link revoked as soon as it
  sees that answer (from the proxy or the manifest), deletes the token and
  the services' key, and the services stop. Revoking is final: a revoked
  share cannot be edited or resumed, so share again with a new invite.
- **Pause** answers `share_paused` and **expiry** `share_expired`, checked on
  every request. B disables the services' key but keeps the token, and a
  resume turns the services back on at the next sync.

**Disconnect.** B tells A (`POST /api/peer/v1/leave`, best effort), deletes
the token and the keys holding it, and leaves the services in place without a
key. A shows the share as left by the peer. Providers imported from A stay on
B after a disconnect or revoke, whether or not B added keys to them: they are
plain specs with no secret, B's own services may use them, and a superadmin
can delete one B does not use from the Providers page.

**Loops.** A service whose provider is `acnologia-peer` can never be shared
onward, and a request that already passed through more than 3 servers gets
508 `loop_detected`.

### Share modes: proxy or key handover

A share's `mode` is chosen at creation and fixed once redeemed (create a new
share to change it):

- **Proxy** (the default) is everything above: the other server never sees
  your keys, and revoke is instant.
- **Handover** gives the keys themselves to the other server, which calls the
  provider directly. Latency is lower, and the other server keeps working
  while the owner is down.

**The honest constraint.** A handed over key cannot be taken back. Once the
other server holds it, it can copy it, and revoke can only make a server that
cooperates delete it. Everything below narrows that window without closing
it. After revoking a handover share, rotate every key on its rotate checklist
at the provider.

**What is handed over.** The share's key scopes decide (all, up to N, or a
picked list), as in proxy mode. Hidden keys go only with `include_hidden`.
Keys of a service whose provider is `acnologia-peer`, and keys another server
handed over to this one, never go. Request and budget limits cannot apply,
because the other server makes the requests and this server cannot count
them, so a handover share refuses limits with 400. Timers and the lease still
apply.

**Sealing.** Every server has an X25519 key pair next to its Ed25519 identity
(the private part encrypted with the master key, made on first use) and
publishes the public part as `box_public_key` at `/.well-known/acnologia`.
The receiver signs it into its redeem request and the owner records it on the
share. Each secret is sealed with HPKE (RFC 9180) base mode: DHKEM(X25519,
HKDF-SHA256), HKDF-SHA256 and AES-256-GCM, with the info string
`acnologia-handover-v1\n<share id>\n<key id>\n<receiver id>`, so a sealed
secret opens only on that server, for that key and that share. The envelope
is `acnseal1_` + base64url(enc and ciphertext); the prefix names the format
version, and the redactor knows it.

**Handover, step by step:**

1. B reads the manifest with the share token: status active, mode handover,
   and the lease length.
2. B calls `GET /api/peer/v1/keys`. A resolves the key scope, seals each key
   to B's box key, sets `valid_until = now + lease` and records the exposure.
3. A returns the services (base URL and auth) and the sealed keys.
4. B opens each key and stores it encrypted as a hidden key of a local
   service on A's provider, valid until `valid_until`.
5. B reports back on `POST /api/peer/v1/report`: the keys it holds, requests
   made with each, and each key's status.
6. A local app on B calls `/proxy/a-weather/...`, and B calls the provider
   with the handed over key. A is not involved.

```mermaid
sequenceDiagram
    autonumber
    participant A as Owner (A)
    participant B as Receiver (B)
    participant P as Provider

    B->>A: GET /api/peer/v1/manifest (share token)
    A-->>B: status active, mode handover, lease
    B->>A: GET /api/peer/v1/keys (share token)
    A->>A: resolve the key scope, seal each key to B's box key,<br/>valid_until = now + lease, record the exposure
    A-->>B: services (base URL, auth) and sealed keys
    B->>B: open each key, store it encrypted as a hidden key<br/>of a local service on A's provider, lease = valid_until
    B->>A: POST /api/peer/v1/report (keys held, requests, status)
    Note over B,P: a local app on B calls /proxy/a-weather/...
    B->>P: request with the handed over key (A is not involved)
```

**On the receiver.** Handed over keys live in a local service per shared
service, slug `<prefix>-<slug>`, whose provider is the owner's (imported as a
dependency when B lacks it) and whose base URL and auth come from the owner.
Unless the link is on a private network, that base URL must be http(s) and
resolve to public addresses. Each key is hidden, labeled "Handed over by
<server>", and carries `peer_link_id` and `peer_valid_until`. Local apps use
it through B's proxy like any key. B never reveals it, leases it (403
`lease_not_allowed`, also for apps with `lease_hidden` and for MCP
`get_key`), shares it onward, changes its secret, hidden flag or egress, or
points its service anywhere else (409 `handover_service`).

**The lease.** Every handed over key is valid until `now + lease` (1h, 6h,
24h by default, 72h or 7d). B renews on each sync, every 10 minutes. The
owner stops renewing when the share is paused, expired or revoked, or when
the key leaves the scope. B's worker checks every minute and deletes every
key whose lease ended, row and ciphertext (zeroed first), with no network
needed. A receiver cut off from the owner lets go of the keys on its own.

**Wipes.** B deletes handed over keys when:

- the lease ends,
- the manifest or a refusal says paused, expired or revoked,
- a key is no longer sent,
- B disconnects (before telling the owner),
- a signed revoke arrives.

Pause wipes too; resume hands the keys over again at the next sync. B audits
and announces arrivals and wipes (`peer.handover_received`,
`peer.handover_wiped` with the reason), never each renewal.

**Revoke now.** Revoking a handover share (by hand, or by a timer whose end
action is revoke) also pushes `POST <receiver url>/api/peer/v1/revoke`,
signed with the owner's identity key over the share id, the receiver id, a
timestamp and a nonce.

1. B checks the signature against the owner key it pinned at connect,
   refuses timestamps more than 5 minutes off and nonces it has seen, deletes
   every key of that link and marks it revoked.
2. The push is best effort, and it uses the public only client unless the
   share is marked `private_network`.
3. The owner records the outcome on the share: "wiped 1 key(s) at ...", or
   "not reached (...); the keys are deleted by <time> when their lease ends".

```mermaid
flowchart TD
    R([revoke handover share]) --> T[token and app off at once]
    T --> Push{signed push reaches B?}
    Push -->|yes| W1[B deletes the keys now<br/>owner records wiped]
    Push -->|no| L[recorded as not reached]
    L --> Sync{B syncs?}
    Sync -->|yes| W2[manifest says revoked: B deletes]
    Sync -->|no| W3[lease ends: B's worker deletes,<br/>no network needed]
    W1 --> C[rotate checklist: rotate each key at the provider]
    W2 --> C
    W3 --> C
```

**What the owner sees.** Each handover share lists its keys: when each was
first handed over, last renewed, valid until, and deleted (with why), plus
the request count and status the receiver reported (cooperative, shown as
reported by the other server). The key list marks each key as handed over
while live, and as was handed over afterwards (`handed_over` on the key). A
revoked handover share carries `rotate_checklist`, every key the receiver
ever held. Audit entries: `share.handover` once per delivery (not per
renewal), `share.handover_wiped` for each wipe the owner learns of (push
confirmed, lease lapsed, reported deleted, peer left), and
`share.revoke_push` for each push.

### Share timers

Both modes have a timer: `ends_at` (RFC 3339, or none; `expires_at` is the
same field under its old name) and an `end_action`:

- `expire` (default): the share stops working (403 `share_expired`) and can
  be extended back to active.
- `revoke`: the share is revoked for good. The invite and token die, and
  handed over keys are wiped by the push or, failing that, by the lease.

The worker checks every minute and every request checks too, so a timer is
never late by more than the next request. Ends are audited as
`share.expired` or `share.revoked` (detail "timer ended") and sent to
webhooks with `reason: timer`. `POST /api/admin/shares/{id}/extend` moves
`ends_at` later on an active or expired share. An expired one turns active,
and handover keys are handed over again at the receiver's next sync. Invite
expiry is separate and does not change.

## Sharing with people

Most people an owner wants to give access to (a contractor, a friend, a
teammate with a script or an AI agent) do not run Acnologia. A share whose
recipient is a person gives them access with nothing but a link. Key scopes,
timers, pause, resume, extend, revoke, audit, webhooks and usage work as for
a share with a server, and so do request and budget limits in proxy mode.

**Step by step:**

1. **Owner (superadmin):** creates a share with `recipient: person`, a
   `label` (who it is for) and an optional `email`, which is only a label:
   Acnologia never sends mail. The mode is `proxy` (default) or `reveal`.
2. **Server:** answers with `claim_url`, `<public url>/claim/acnclaim_...`,
   shown once and valid 1h, 24h (default) or 7d. Only its hash is stored.
   While the link is unclaimed, `POST /api/admin/shares/{id}/claim-link`
   issues a new one and kills the old one.
3. **Owner:** sends the link however they like (chat, mail, in person).
4. **Person:** opens the link. The page calls `POST /api/claim/v1/preview`,
   which shows what is shared, by which server, for whom, until when and with
   which limits. It carries no secret.
5. **Person:** presses "Get my access", and the page calls
   `POST /api/claim/v1/redeem`. Redeem burns the link in one transaction, so
   it works once; a second redeem gets 409 `already_claimed`.
6. **Server:** answers once with a token (proxy) or the keys (reveal).

```mermaid
sequenceDiagram
    participant O as Owner (dashboard)
    participant S as Acnologia
    participant P as Person (browser)
    O->>S: POST /api/admin/shares {recipient: person, label, mode}
    S-->>O: claim_url (shown once; hash stored)
    O-->>P: sends the link (chat, mail, in person)
    P->>S: GET /claim/acnclaim_... (SPA page)
    P->>S: POST /api/claim/v1/preview {code}
    S-->>P: share, owner, services, limits, ends_at (no secret)
    P->>S: POST /api/claim/v1/redeem {code}
    S->>S: burn the link, mint the token or record the reveal
    S-->>P: token (proxy) or keys (reveal), once
```

**Proxy mode.** Redeem mints an app token on the share's backing app: proxy
only, limited to the shared services, with the key scopes and limits
applied. The answer has the token, the base URL, and per service the proxy
URL, upstream auth and the provider's examples (for curl, Python and
TypeScript snippets), plus the MCP URL, the limits and `ends_at`. The person
calls `/proxy/<service>/...`, `/api/v1/services`, `/api/v1/me` and `/mcp`
with `Authorization: Bearer acn_...`. Keys never leave the server. Pause,
revoke and an ended timer refuse the very next call with 403 `share_paused`,
`share_revoked` or `share_expired`. At `/mcp` the token sees the core tools
that do not hand out keys (`list_services`, `pool_status`, `proxy_request`)
and the tools generated for the shared services. It never sees `get_key`,
key insights or key management.

**Reveal mode.** Redeem returns the keys the scopes resolve to, exactly once,
each with its service, label, secret, provider, base URL and auth. Hidden,
restricted, disabled, invalid, banned and handed over keys are never shown,
even with `include_hidden`, and a share whose scopes resolve to no showable
key is refused at creation. Each key is recorded as revealed
(`share.revealed`, one audit entry and webhook per key, ids only) and marked
"Revealed to <label>" on the owner's key list and key page (`revealed_to`).
Revoke cannot take a revealed key back. When a claimed reveal share ends, by
revoke or by its timer, its response carries `rotate_checklist`, the audit
gets `share.rotate_keys`, and the owner should rotate each key at its
provider.

**My access.** `/me` is a page where the person pastes their token. It calls
`GET /api/v1/me` and shows the share's name, the owner server's name, the
share's status (active, paused, expired, revoked), mode and end, the services
with how many keys are free, and the limits with today's and this month's
use. The token stays in the browser only if they tick "remember on this
device". `/api/v1/me` works for any app token; ordinary apps get no share
fields.

**Locked deploys.** On a deploy behind the access password, `/claim/<code>`
and `/me` load the dashboard shell like any page, and the shell skips the
lock screen on those two pages. `/api/claim/v1/*` and the token's own calls
(`/proxy`, `/api/v1`, `/mcp`) pass the gate. Every other route stays locked.
