# Security model

Acnologia holds other people's API keys, so most of its design is about who can
make a key leave the server, and where it can go.

## Trust boundaries

```mermaid
flowchart LR
    subgraph Untrusted
        Net["anyone on the network"]
        Up["provider responses"]
        Web["web pages read by the AI builder"]
    end
    subgraph Authenticated
        AppT["app token holders"]
        Mem["member"]
        Adm["admin"]
    end
    Sup["superadmin"]
    subgraph Server["Acnologia server (one binary)"]
        Keys[("encrypted keys")]
    end

    Net -->|"login, setup (rate limited)"| Server
    AppT -->|"proxy, lease (scoped by app)"| Server
    Mem -->|"read the dashboard"| Server
    Adm -->|"operate services, keys, apps"| Server
    Sup -->|"hidden keys, reveal, users, audit"| Server
    Up -->|"classified, scrubbed, sandboxed"| Server
    Web -->|"text read by the model"| Server
```

## Roles

| Can | member | admin | superadmin |
|---|:-:|:-:|:-:|
| See services, non-hidden keys, events, apps, providers | yes | yes | yes |
| Add, edit, delete services, keys, apps, providers; run checks; AI runs | no | yes | yes |
| See, create, hide or delete **hidden** keys | no | no | yes |
| Let an app use hidden keys (`lease_hidden`) | no | no | yes |
| Reveal a key's secret in the dashboard | no | no | yes |
| See Settings, AI models and prices, provider refresh runs and reviews, shares and peers | no | yes | yes |
| Manage users, read the audit log, set AI rules | no | no | yes |
| Switch AI models, edit model prices | no | no | yes |
| Change provider refresh settings, run it, Check now, apply or dismiss a review | no | no | yes |
| Create, edit, pause, resume and revoke shares; connect, sync and disconnect peers | no | no | yes |

Roles are re-read from the database on every request, so a demotion or a
disable takes effect immediately. At least one active superadmin always
remains.

## Apps and key flags

Each app has its own token (stored as a hash), a list of services it may use
(empty means all) and a mode: `proxy`, `lease` or `both`. An app learns
whether a service has a key available, never how many keys exist.

Each key can carry flags:

- `hidden`: visible only to superadmins, and handed only to apps a superadmin
  cleared with `lease_hidden` (below).
- `restricted`: serves only the apps you name on the key.
- `disabled`: stays stored but is never handed out.
- `backup` tier: serves only when every primary key is cooling, busy or out
  of quota.

## Hidden keys: admins operate, but never read them

An admin can create apps and edit services. Leasing a key returns the plain
secret, and a proxied provider can echo the key back, so **any key an admin can
route to an app or a host they control is effectively readable by that admin**.
Non-hidden keys are therefore readable by admins. Use `hidden` for keys they
must never see. Two flags enforce this:

- **`apps.lease_hidden`**: only apps a superadmin marked may use hidden keys,
  through a lease or the proxy. Admins cannot set it. When anyone other than a
  superadmin rotates the app's tokens or adds a token, the flag is cleared,
  because that person now holds a working token.
- **`services.target_trusted`**: when anyone other than a superadmin changes a
  service's base URL, auth placement or provider, the service becomes
  untrusted, and hidden keys are not sent to it by the proxy, leases or health
  checks. A superadmin confirms the target by saving those fields again. The
  dashboard shows a banner while hidden keys are paused.

```mermaid
flowchart TD
    Q{"may this hidden key<br/>be used here?"} --> T{"service.target_trusted?"}
    T -->|no| N1[no]
    T -->|yes| Path{"path"}
    Path -->|lease or proxy| L{"app.lease_hidden?"}
    L -->|no| N2[no]
    L -->|yes| Y1[yes]
    Path -->|health check| Y2[yes]
    Path -->|dashboard reveal| S{"caller is superadmin?"}
    S -->|no| N3["404, as if it did not exist"]
    S -->|yes| Y3["yes, audited"]
```

Hidden keys and their traffic are also left out of everything a non-superadmin
sees: key lists, counts, events, request totals, and the events of a deleted
hidden key.

## Secrets at rest

| Item | Stored as |
|---|---|
| Provider keys | AES-256-GCM with a random nonce per write, plus a keyed HMAC fingerprint for duplicate detection |
| AI run test keys | Same encryption, wiped when the run ends |
| Passwords | bcrypt (10 to 72 characters) |
| Session and app tokens | SHA-256 of 32 random bytes. An app can hold several tokens in `app_tokens`, each revocable and optionally expiring; a revoked or expired token gets 401. |
| Share tokens | On the owner, the SHA-256 like any app token. On the receiver, encrypted like a provider key. |
| Server identity | Ed25519 private key, encrypted with the master key in `meta` |
| Master key | `ACNOLOGIA_MASTER_KEY`, else `data/master.key` (0600, generated on first boot) |

The server refuses to start when the master key does not match the database
(checked through `meta.master_key_check`, or by decrypting a stored key on
databases from before that check). It never generates a new key over an
existing file it failed to read. The data directory is chmod 0700 and the
database files 0600.

Windows ignores those modes, so there the folder's ACL does the job:

- The default data directory, `%LocalAppData%\Acnologia`, inherits the ACL
  of your profile, readable only by you, SYSTEM and administrators.
- A data directory you choose elsewhere (`C:\ProgramData\Acnologia` for a
  service, say) inherits that folder's ACL, which often lets every local
  user read it. Restrict it with `icacls` (see
  [configuration](configuration.md#windows)), or `master.key` and the
  database are readable by anyone signed in to the machine.
- `acn` keeps its token in `%AppData%\acnologia\ctl.json`, under the same
  profile ACL.

## Keeping keys out of places people can read

- Upstream error text, health check errors and lease report messages are
  scrubbed of the key (raw, query escaped and path escaped forms) before they
  are stored in `last_error` or events, or logged.
- Request logs record the path only, never the query string.
- Proxied response headers are scrubbed of the key, so a redirect that keeps
  the query string cannot hand a query-auth key to the app.
- Requests that carry a key never follow redirects (Go would forward custom
  auth headers like `xi-api-key` to the new host).
- The SDKs scrub the secret from failure reports and redact it when a lease or
  client is printed.

## Assistant chat

- Tools call the dashboard API in process with your own session, so the
  assistant can never do more than you can, and its changes appear in the
  audit log under your name. Members are offered read tools only.
- Deleting keys, services or apps, rotating app tokens and revoking an app
  token run only after you press Confirm; a confirmation expires when you send another message.
- There is no tool that reveals a stored key secret. Tool results sent to
  Claude never contain secrets, and new app tokens are shown to you once and
  never sent to Claude or stored.
- Keys you paste never reach Claude. Before a message goes to the model,
  Acnologia finds the keys in it (the paste parser, then the pattern detector
  for keys inside URLs, after `Bearer`, in JSON fields or glued to text),
  encrypts each one into the thread's vault and puts a placeholder such as
  `[key k3 ...c21e]` in its place. Tools resolve the placeholders on the
  server. Your own message is stored as the prose without the keys, plus a
  list of label and last four characters. Threads are encrypted at rest with
  the master key and visible only to their owner.
- Key account fields (email, phone, name) are personal data: members see them
  empty, and they are not written to events or the audit log.

## What the model can and cannot see

Every request to Anthropic, from the Assistant and its compaction summaries,
the provider builder, Fill with AI, run naming and the provider refresh,
goes through one HTTP middleware on the shared client
that rewrites the request body before it leaves the server:

- Known secrets are replaced by `[key ...c21e]`: every pool key, webhook URL
  and secret, and egress proxy URL (decrypted once into a multi pattern
  matcher and reloaded when one changes), plus `GITHUB_TOKEN`, the builder
  run's test keys and the keys a chat turn handled. Raw, query escaped and path escaped forms
  are matched. Values shorter than 8 characters are not matched exactly.
- Unknown secrets are replaced by `[secret ...c21e]` by pattern: common
  prefixes (`sk-ant-`, `sk-`, `AIza`, `ghp_`, `xox`, `tvly-`, `AKIA`,
  `sk_live_`, JWTs, Acnologia's own `acn_` and `acnadm_` tokens and the
  older `cog_` and `cogadm_` ones), `Bearer` and `Basic`
  credentials, auth headers, `key=`, `api_key=`, `token=`, `secret=` and
  `password=` in URLs, env lines and JSON, and long random looking tokens.
  Hex hashes, UUIDs, words, identifiers and paths are left alone.
- The system prompt, your messages and every tool result get both passes.
  Replayed model output gets the known pass only; thinking blocks and web
  search and fetch results are not touched, since the API checks their
  signatures. Tool definitions are not changed.

So Claude sees key hints, labels, statuses and quotas, but not a secret,
including a key an upstream echoes in an error or a live check. The builder's
live checks use the test key on the server; the model gets the result with
the key replaced.

The same redactor runs where text is stored or sent: agent steps and run
errors, run notes, events, the audit log, key `last_error`, webhook payloads
and delivery errors, and every log line (a wrapping `slog` handler).

Limits: a short key with no prefix and no `key=` in front of it (a bare 12
character token in a sentence) is caught only if it is already a pool key or
the paste parser finds it. Leasing a key, through the API or the MCP
`get_key` tool, returns the secret to the app by design; use proxy mode for
any client you would not hand a key to.

## AI models, prices and provider refresh

- **Switching models** is a superadmin action, audited as `ai.models`. Only
  model ids from Anthropic's list or the built in table are accepted, and
  every model is reached through the same client and redactor, so picking a
  model changes cost and quality, never what leaves the server.
- **Model price overrides** are superadmin only and audited as `ai.prices`
  with the old and new values. They change how Acnologia counts the cost of
  its own Claude calls (the Usage page) and, for a context window override,
  when an Assistant chat compacts. They never touch provider keys, offers or
  app budgets.
- **The provider refresh never sends a key.** The refresh model gets the
  provider's name, URLs, example names and current offers, and reads public
  pricing pages through `web_search` and `web_fetch`, which run on
  Anthropic's side, so no page is fetched from Acnologia's network. The
  Claude price refresh reads Anthropic's pricing page the same way. Every
  request passes the redactor above.
- **A refresh can only change offers.** Never auth, the base URL, rules,
  health checks or tests, so a misread or hostile page cannot redirect keys.
  The pages it reads are untrusted, so the bounds are in code: a change
  applies on its own only when it moves existing offers' prices within 4x or
  loosens their limits, and every changed offer cites a page on the
  provider's own website, docs or API domain. New or removed offers, a
  changed match, notes or free flag, and any tighter limit or concurrency
  wait for a superadmin, so a hostile page cannot throttle every key or zero
  a price unseen. A no change check only moves `checked_at` and never
  reorders the stored offers. Names and notes are stripped of control and
  invisible characters, a `source_url` with user info is dropped, and later
  checks start only from cited pages on the provider's own domains. Claude
  price changes apply on their own only from an https page on anthropic.com
  or claude.com (the only sites that refresh may read) and within 4x of the
  current price. Each applied change is a provider version you can roll
  back, with a webhook and an audit entry.
- **Spend is capped**: per run (default $2) and per month (default $5) for
  the refresh feature, checked before each provider. Only one run goes at a
  time.
- **Providers copied from a peer are skipped** unless a superadmin turns
  them on.

## Web protections

- **Access password** (optional, for an instance on the internet): with
  `ACNOLOGIA_ACCESS_PASSWORD` set, a lock screen comes before setup, sign in
  and every dashboard route, and only the app shell, `/healthz`,
  `/robots.txt`, the app token routes and the CLI sign in's create and poll
  calls stay open. A valid admin API token passes the lock on its own. The `acnologia_gate`
  cookie is an issue time plus an HMAC keyed by the master key and the
  password, refused after 30 days. Wrong guesses, on the lock screen or in
  `X-Acnologia-Access`, are limited to 5 a minute and 50 a day per client
  address, under a global ceiling of 20 a minute, so a flood makes the owner
  wait a minute at most unless it comes from the owner's own address. See
  [configuration](configuration.md#access-password).
- **No indexing**: every response carries `X-Robots-Tag: noindex, nofollow,
  noarchive, nosnippet` and `/robots.txt` disallows everything.
- **CSRF**: every mutating dashboard call must send `Content-Type:
  application/json` or `X-Requested-With`, both of which force a CORS
  preflight. This holds for bodyless calls too. The session cookie is
  `HttpOnly` and `SameSite=Lax`.
- **Content Security Policy**: the dashboard allows scripts only from itself
  plus the exact inline scripts in `index.html`, by SHA-256 hash computed at
  startup. Framing is denied.
- **Proxied responses** are served from the dashboard origin, so they get
  `Content-Security-Policy: sandbox; default-src 'none'`, `nosniff` and
  `X-Frame-Options: DENY`, and upstream `Set-Cookie`, CORS and security
  headers are dropped.
- **Logos** are served with their sniffed type, `nosniff` and a sandbox CSP,
  and the UI only ever shows them through `<img>`.
- **Links** from provider data are rendered only when they are `http(s)`, and
  the server rejects other schemes for provider `website` and `docs_url`.

## Abuse limits

| Limit | Value |
|---|---|
| Failed logins | 10 per 10 minutes per client IP and per existing account |
| Dashboard JSON body | 4 MB |
| Proxy request body | `ACNOLOGIA_MAX_BODY_MB` (default 50) |
| Proxy methods | GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS |
| Proxy idle stream | 5 minutes without bytes ends the response and frees the slot |
| Cooldown from a report or `Retry-After` | 24 hours |
| Lease wait / TTL | 60 s / 24 h |
| Late lease report | refused 10 minutes after expiry |
| MCP tool result | `max_result_kb` per app, default 200 KB, at most 2048 |
| AI runs | 2 at a time, 40 turns, 30 minutes, 5 live checks, 3 submits |
| Assistant | 24 model steps and 15 minutes per message, messages up to 1 MB (a dropped file of keys), one message at a time per chat |

The client IP comes from the socket unless `ACNOLOGIA_TRUST_PROXY_HEADERS=true`.
Behind a reverse proxy, enable it, or every client shares one login limit.

## Outbound requests

```mermaid
flowchart LR
    subgraph With["carries a key"]
        P["proxy"]
        H["health checks"]
        L["AI live checks"]
    end
    subgraph Token["carries an invite or a share token"]
        Peer["preview, redeem, sync,<br/>leave, peer logos"]
    end
    subgraph Without["no key"]
        Logo["logo discovery"]
        AI["AI features,<br/>provider refresh"]
    end
    P -->|"service base_url only,<br/>no redirects,<br/>optional egress proxy"| Prov[provider]
    H -->|"service base_url only,<br/>no redirects,<br/>optional egress proxy"| Prov
    L -->|"provider's own domain only,<br/>GET examples, SafeClient"| Prov
    Logo -->|"SafeClient"| Site[provider website]
    Peer -->|"owner's URL only,<br/>SafeClient unless the link<br/>is marked private network"| Owner[owner Acnologia]
    AI -->|"redacted,<br/>web search and fetch<br/>run on Anthropic's side"| Claude[Claude API]
```

Webhook deliveries carry no key and go through `SafeClient` unless
`ACNOLOGIA_WEBHOOK_ALLOW_PRIVATE=true`. Webhook URLs are encrypted at rest (Slack,
Discord and Telegram URLs are secrets) and only a host hint is ever returned.
Hidden keys never produce webhook events.

An egress proxy sees which hosts keys travel to, and the keys themselves when
a service's base URL is plain `http`. Assigning one is therefore part of a
service's target: a non-superadmin assigning or changing it clears
`target_trusted`, so hidden keys stop flowing until a superadmin confirms.
Proxy URLs are encrypted at rest and shown without credentials.

The response cache is shared by the apps of a service, so Acnologia never caches
a response made with a hidden or restricted key, a response the upstream marks
`private` or `no-store`, or a body that contains the key. Idempotent replays
are stored per app.

Admin API tokens (`acnadm_`) are stored as SHA-256 hashes, carry a scope that
never exceeds their owner's role, stop working when the owner is disabled, and
cannot create tokens or change the account (a token may revoke itself, which
is how `acn logout` works).

`acn login` gets its token through a device flow: the CLI's device code is 32
random bytes kept only as a SHA-256 hash, in memory, for 10 minutes; the
person approves the 8 letter user code on `/cli/authorize` with a dashboard
session past the site lock, picking a scope no higher than their role, and the
token (90 days, named `acn on <hostname>`) is audit logged as `token.create`.
Creating, polling and wrong user codes are rate limited globally (30, 600 and
10 a minute), and an API token cannot approve a sign in. See
[Command line](cli.md#how-the-sign-in-works).

The CLI treats the server it talks to as untrusted input:

- `acn login` opens the sign in link only when it is http or https on the
  host that was typed (never a downgrade from https, never with user info).
  Any other link (another host, `file://`, a local or UNC path, a custom
  scheme) is not handed to the OS launcher; the CLI prints
  `<server>/cli/authorize?code=...` instead.
- `ACNOLOGIA_ACCESS_PASSWORD` is sent only to the server named by
  `ACNOLOGIA_ACCESS_URL`, or else `ACNOLOGIA_URL`, never to a server being
  signed in to or another saved one, and it is dropped from a redirect to
  another origin.
- Text from the server (labels, last errors, event details, error messages)
  is printed with control characters, bidi overrides and invalid UTF-8
  escaped, so a lease report or a peer's key label cannot drive the terminal.
  `--json` output escapes them inside strings.

`SafeClient` checks the resolved address at dial time, which also defeats DNS
rebinding, and refuses loopback, private, link local (including cloud metadata
at 169.254.169.254), CGNAT, benchmark, documentation and reserved ranges, plus
NAT64 and 6to4 addresses that embed one of those.

The AI builder's `web_search` and `web_fetch` run on Anthropic's side, so
fetched pages never reach Acnologia's network. Its `github_read` tool validates
owner, repo and path. Text from fetched pages can steer the model, so its
output is checked rather than trusted: the harness re-validates and re-tests
every submitted spec, a create run cannot overwrite an existing provider,
update runs wait for review by default, and the test key only goes to the
provider's own domain.

How the test key stays on that domain:

- **Pinned to what the operator vouched for.** The key goes only to the
  registrable domain of a URL the operator gave, or of the provider being
  updated. A host on a shared hosting platform (`vercel.app`, `github.io`,
  `supabase.co`, `workers.dev`, `amazonaws.com` and similar) pins only that
  tenant's own host, and an IP address only itself.
- **Nothing to pin to, no key sent.** A create run with no docs URL (or only
  a GitHub link) never sends the test key. The draft is accepted without a
  live check and waits for review even with auto publish on, because
  publishing creates a service at `base_url` holding the test keys; the
  review step names the host to confirm.
- **Only a valid spec runs live.** `live_check` refuses a draft with schema
  problems, so every health and example path starts with `/` and the health
  method is GET or POST. The live check client also refuses any request whose
  host, scheme or user info differs from `base_url`'s, and the engine's health
  check does the same, so a path such as `@attacker.example/x` cannot move
  the key to another host.

## Sharing between servers

A share lets another Acnologia use some services through this server's proxy.
In the default proxy mode the other server is a client with a token, never a
holder of keys. A handover share is the exception, covered at the end of this
section.

- **No secret leaves the owner (proxy mode).** The receiver gets a share token, a manifest
  and answers to its proxied requests. The manifest holds the share's name,
  status, limits and expiry, each shared service's name, description,
  provider and how many keys in its key scope can take a request now, and
  the provider specs (which hold no secret). Never a key, its hint, its
  label, its count by status, or which key served a request. Hidden keys
  serve a share only when a superadmin turned on `include_hidden`, which
  sets `lease_hidden` on the backing app, and only while the service's target
  is trusted.
- **Key scopes hold on every pick.** A share limited to up to N keys or a
  picked list never gets a key outside it, through any path: the pool
  applies the scope for the backing app before choosing.
- **The backing app never grants everything.** An app with an empty service
  list may use every service, so a share's app always lists the shared
  services, or an entry that matches nothing for a provider only share. It is
  proxy only, hidden from the Apps page, and the app routes refuse to change it
  (409 `share_app`).
- **Invites are one time and short lived.** `acninv_` codes carry the owner URL,
  the share id and 32 random bytes; only the SHA-256 of the secret is stored and
  it is compared in constant time. Redeeming burns it in the same transaction
  that binds the peer. Preview and redeem are limited globally (20 a minute,
  500 a day).
- **An invite binds to the server that redeems it.** Redeem carries the
  receiver's public key and an Ed25519 signature over the share id, the
  invite hash, a nonce and its id; the owner checks the id is derived from
  the key and records that server on the share. The invite is then spent, so
  a leaked code is worthless after the first redeem, and a share serves one
  receiver. The receiver checks that the owner's identity at redeem matches
  the one previewed.
- **Revoke is immediate.** Revoke deactivates the app and revokes its tokens in
  one transaction; the share check runs before the token check, so the next
  request is refused with `share_revoked`. Pause and expiry are checked on every
  request.
- **Tokens are kept like keys.** The owner stores only the token's hash. The
  receiver stores it encrypted with the master key (in the link and as the
  services' hidden key), it is in the redactor's list, `/keys/{id}/reveal`
  refuses it, and revoke or disconnect deletes it. `acninv_` codes are a
  redactor pattern too, so neither reaches logs, events, audit detail, webhooks
  or Claude.
- **Outbound calls.** Preview, redeem, sync and leave use `SafeClient` unless the
  superadmin ticked "this server is on my private network" for that link.
  Proxied calls go to the service's base URL like any other service.
- **Imported specs never change on their own.** A provider spec from a peer,
  whether shared on purpose or pulled in because a shared service uses it, is
  imported once with source `peer:<owner id>` and only when this server has no
  provider with that slug; an existing one is never overwritten. A newer owner
  version is only reported. Pulling it is a superadmin action, refused when the
  spec was edited here and confirmed separately when the base URL moves, so an
  owner can never point keys this server added to its provider somewhere else.
  The local service keeps provider `acnologia-peer` for classification and the
  token; the owner's provider only describes it.
- **Logos from a peer.** The share token is sent only to the owner's URL. The
  owner serves logos only for providers in that share's manifest. The receiver
  accepts an allowed image type up to 512 KB whose bytes match the declared
  type and serves it with the same sandboxing CSP as every logo.
- **No chains.** Services from a peer (provider `acnologia-peer`) cannot be
  shared onward and cannot be leased. As a second guard, each server adds one
  to `X-Acnologia-Hop` when it forwards to a peer (the proxy strips the
  header on every other request); a request that arrives with more than 3,
  or would leave for a fourth server, gets 508 `loop_detected`.
- **Roles.** Only superadmins create, edit, pause, resume, revoke, connect,
  sync and disconnect. Admins can view. Server to server routes sit outside
  the access password lock; they need an invite, a share token or (for a
  revoke push) the owner's signature.

### Key handover: what changes

A handover share is the one place a key secret leaves the server that owns
it, on purpose. The model there is different, and the dashboard and these
docs say so plainly.

- **What the receiver can do with a key.** Anything the provider lets the key
  do, for as long as the provider honors it. A receiver that does not
  cooperate (modified code, a copy of its database and master key, a
  debugger, a proxy that logs its own outbound calls) can keep the secret
  forever. Nothing in Acnologia can take it back. Request and budget limits
  cannot be enforced either, because the owner never sees the requests; the
  receiver's usage reports are cooperative.
- **Why the lease exists.** A cooperating receiver deletes every handed over
  key whose lease (1h to 7d) ended without renewal, on its own clock and
  with no network: the worker checks every minute and zeroes the ciphertext
  before deleting the row. So a receiver the owner can no longer reach, or
  that loses the owner, keeps a key at most one lease past the last renewal.
  The owner stops renewing on pause, expiry, revoke and scope changes.
- **Why the push exists.** Revoke (and a timer that revokes) sends a signed
  revoke to the receiver so a cooperating receiver deletes the keys now
  rather than at the next sync or the end of the lease. It is Ed25519 signed
  by the owner's identity key over the share id, the receiver id, a
  timestamp and a nonce; the receiver verifies it against the owner key it
  pinned when it connected, refuses timestamps more than 5 minutes off and
  remembers nonces until they would be stale anyway, so it cannot be forged
  or replayed. It is best effort; its outcome is recorded on the share.
- **Rotate after revoke.** Because the push and the lease only bind a
  cooperating receiver, the owner should rotate every handed over key at its
  provider after revoking. The revoked share carries a rotate checklist of
  every key the receiver ever held.
- **Sealing.** Secrets travel sealed with HPKE (X25519, HKDF-SHA256,
  AES-256-GCM) to the receiver's X25519 key, which the receiver signed into
  its redeem request. The HPKE info binds the share id, the key id and the
  receiver id, so a sealed key replayed to another server, another share or
  as another key does not open. Sealed blobs (`acnseal1_...`) are a redactor
  pattern and never appear in a manifest, log, event, audit detail, webhook
  or anything sent to Claude; handed over secrets on the receiver are in its
  redactor's list like any stored key.
- **On the receiver the key stays inside the proxy.** A handed over key is a
  hidden key of a service whose target the owner chose. Local apps use it
  only through the proxy; `/keys/{id}/reveal` refuses it, leases (REST and
  MCP `get_key`) refuse it even for apps with `lease_hidden`, it cannot be
  shared onward (its service is a peer service), and its secret, hidden flag
  and egress, and its service's base URL, auth, provider and egress, cannot
  be changed to send it elsewhere. The assistant reaches keys only through
  these routes. A base URL from an owner that is not on a private network
  must resolve to public addresses, so an owner cannot aim the receiver's
  apps at its internal network.
- **Who decides.** Only a superadmin creates a handover share, and only a
  superadmin connects a receiver to one. A receiver from before this
  feature cannot redeem a handover share (it sends no box key).

## Sharing with people

A share with a person gives someone who does not run Acnologia access
through a one time claim link. In proxy mode the person is a client with a
token, like a receiving server; in reveal mode they get the raw keys.

- **Claim links.** A code is `acnclaim_` and 24 random bytes. Only its
  SHA-256 is stored, it is shown once (in the create or new link answer),
  and it is valid 1h, 24h or 7d. Redeem burns it in the same transaction
  that mints the token or records the reveal, so it works once even under
  parallel redeems; a new link kills the old one. Preview and redeem are
  limited globally (20 a minute, 500 a day), answer with
  `Cache-Control: no-store`, and say only which of unknown (404), used (409)
  or expired (410) applies. A share revoked or past its timer before the
  claim refuses the link. `acnclaim_` is a redactor pattern, and the request
  log writes `/claim/[code]` for the page, so a code reaches no log, event,
  audit detail, webhook or Claude input. Anyone holding an unclaimed link can
  claim it: send it over a private channel and pick the shortest validity
  that works. The email on a share is a label; nothing is ever sent.
- **What a person's token can do.** It is an app token on the share's
  backing app, kept as a hash like any app token. It can call
  `/proxy/<service>` for the shared services only, within the key scopes,
  limits and timer; read `/api/v1/services` and `/api/v1/me`; and use `/mcp`
  with the core tools that do not hand out keys and the generated service
  tools.
- **What it cannot do.** Lease a key (the app is proxy only, so neither
  `/api/v1/lease` nor MCP `get_key` works) or see any key secret, hint or
  count by status. Use key insights or key management over MCP: a share's
  token never gets those tool sets, whatever the app's MCP settings say, and
  the Apps page cannot change a share's app. Reach any dashboard route (app
  tokens never pass `requireUser`, which takes a session or an `acnadm_`
  token) or the server to server routes (403 `not_a_share`). Outlive a
  pause, revoke or ended timer: the share check runs before the token check
  on every request.
- **Reveal: the threat model.** A revealed key has left the server for good.
  The person can use it for anything the provider allows, outside every
  limit, for as long as the provider honors it, and revoke cannot take it
  back; the dashboard and these docs say so plainly. What Acnologia does:
  reveal only the keys the scopes resolve to, and never hidden, restricted,
  disabled, invalid, banned or handed over keys (even with
  `include_hidden`); show them in exactly one answer, after the link is
  burned; record each key as revealed (`revealed_to` on the key,
  `revealed_keys` on the share, `share.revealed` audit and webhook per key
  with ids only); and when the share ends, list every revealed key in
  `rotate_checklist` and the `share.rotate_keys` audit entry. Rotate them at
  the provider. Only a superadmin creates a reveal share; prefer proxy mode.
- **Access password lock.** On a locked deploy the `/claim/<code>` and `/me`
  pages are the dashboard shell (which carries no data), `/api/claim/v1/*`
  is open and needs a claim code, and the person's own calls go through
  `/proxy`, `/api/v1` and `/mcp`, which were already open and need an app
  token. Nothing else opens: other `/api/*` routes, writes to the pages and
  dot segment tricks on the open prefixes stay locked.

## Operator checklist

1. Serve over TLS and set `ACNOLOGIA_SECURE_COOKIES=true`.
2. Behind a reverse proxy, set `ACNOLOGIA_TRUST_PROXY_HEADERS=true`.
3. Set `ACNOLOGIA_MASTER_KEY` from a secret store instead of keeping
   `master.key` on the data volume, and back it up. Losing it makes stored keys
   unreadable.
4. Do the first run setup right away, or set `ACNOLOGIA_BOOTSTRAP_EMAIL` and
   `ACNOLOGIA_BOOTSTRAP_PASSWORD`, so nobody else can claim the superadmin.
5. Mark keys admins must never see as `hidden`, and grant `lease_hidden` only
   to apps that need them.
6. Use a fine grained, public read only `GITHUB_TOKEN` if you set one.
7. On a server that shares, set `ACNOLOGIA_PUBLIC_URL`, send invites over a
   private channel with the shortest validity that works, and give each share
   the narrowest key scope and limits it needs. Prefer proxy shares; use a
   handover share only with a server you trust to delete keys, with the
   shortest lease that works, and rotate its keys at the provider after you
   revoke it.
8. Share with a person in proxy mode unless they truly need the raw keys;
   send claim links privately with the shortest validity, and rotate every
   key a reveal share showed once it ends.
9. On Windows with a data directory outside your profile, restrict it with
   `icacls`.

## Known limitations

- Docker base images are pinned by tag, not digest.
- Ciphertexts are not bound to their row, so someone with write access to the
  database could swap secrets between keys.
- Proxy request bodies are buffered in memory up to the body limit.
- The per account login limit means ten wrong passwords lock that account for
  10 minutes.
