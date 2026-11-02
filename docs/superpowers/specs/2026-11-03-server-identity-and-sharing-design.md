# Server identity and sharing between Acnologia servers

## Why

Teams and friends run more than one Acnologia: a laptop, a Railway deploy, a
teammate's box. Today the only way to give another server access to your
SerpAPI pool is to copy keys over, which leaks the secrets for good and makes
revoking impossible. This adds a server identity and a share that lets one
server use another server's providers and pools, with the keys never leaving
home, so access can be limited, watched, paused and revoked at any time.

## Goals

- Every server has a stable, verifiable identity: an id, a name, a public URL.
- A server can share a **bundle** with one other server: any mix of
  - **services** (the pool behind them, used through the owner's proxy; keys
    never leave the owner), and
  - **provider specs** (definitions only, no secrets), so the receiver can add
    its own keys to a provider someone else built.
- The owner can set limits, pause, resume and revoke a share, and see its use.
- The receiver sees incoming shares, uses shared services like local ones and
  can disconnect.

## Non goals (v1)

- Copying or exporting key secrets to another server. Revoke would be a lie.
  Explicitly out.
- Re sharing a service that came from a peer (no chains, no loops).
- Lease mode on shared services: proxy only, because a lease hands out the
  raw key.
- A directory or discovery of servers. Pairing is by invite code only.
- Sharing apps, users, webhooks or settings.

## Anti goals

- A shared secret, invite or token ever showing up in a log, event, audit
  detail, webhook payload or anything sent to Claude (run it through the
  existing redactor and token hashing like app tokens).
- Revoking that only takes effect "eventually". Revoke must refuse the very
  next request.

## Identity

- On first start the server makes an Ed25519 keypair. The private key is
  stored in `meta`, encrypted with the master key box like other secrets. The
  **server id** is `acn_srv_` + the first 20 chars of lowercase base32 of
  SHA-256(public key). It never changes unless the data dir is reset.
- **Name** defaults to the hostname; superadmin can rename. **Public URL**
  comes from `ACNOLOGIA_PUBLIC_URL` if set, else what the superadmin saved in
  settings, else the request origin when generating an invite.
- `GET /.well-known/acnologia` (public, no auth): `{id, name, version,
  public_key (base64), url}`.
- Dashboard: a "This server" card (id with copy, name editable, URL,
  public key fingerprint) on a new **Sharing** page (and linked from Settings).

## Owner side: shares

New table `shares`: id, name, note, status (`pending`, `active`, `paused`,
`revoked`, `expired`), services json (slugs), providers json (slugs),
include_hidden bool (superadmin only, default false), app_id (the backing
app), peer_id, peer_name, peer_url, peer_public_key, invite_hash,
invite_expires_at, expires_at (optional), created_by, created_at,
redeemed_at, revoked_at.

- A share is backed by a regular app row (mode `proxy`, `allowed_services` =
  the shared services, never empty: a provider only share has no app token
  power on the proxy, so give it an allowed list that matches nothing or no
  app at all, builder's call) with a new `apps.share_id` column. Limits
  (daily/monthly request and USD caps) are the app's existing `AppLimits`.
  Share apps are hidden from the Apps page list and managed on Sharing, but
  usage and limits reuse app machinery. `lease_hidden` mirrors
  `include_hidden`.
- **Invite**: creating a share returns a one time code, shown once:
  `acninv_` + base64url(json{u: owner url, s: share id, k: 32 random bytes}).
  Only its hash is stored. Default validity 24h (choices 1h, 24h, 7d).
- **Redeem** (server to server): `POST /api/peer/v1/redeem` (public, rate
  limited like the CLI device routes) with `{invite secret, share id, id, name,
  url, public_key, nonce, signature}`; the signature is Ed25519 over a
  canonical string of share id, invite secret hash, nonce, receiver id. The
  owner verifies the id matches the key, binds the share to that peer,
  burns the invite, mints the app token, and answers `{token, owner identity,
  manifest}`. A second redeem of the same invite is refused.
- **Manifest**: `GET /api/peer/v1/manifest` with the share token: status,
  limits, expires_at, services (slug, name, provider, category, description,
  logo url or none, `available` count of usable keys, no key data), and
  provider specs (full spec JSON, version) for shared providers plus the
  providers of shared services.
- **Revoke**: sets status revoked, deactivates the app and its tokens in the
  same transaction. The next proxied request gets 403 with code
  `share_revoked` and header `X-Acnologia-Share: revoked`. Pause is the same
  with `share_paused` and is reversible. Expiry is checked on every request.
- Editing a share: rename, change services and providers, limits, expiry.
  Changes show up on the receiver at the next manifest sync.
- Usage: requests and cost per day from `app_usage`, last used, per service
  counts from events. Show a 14 day sparkline style bar row on each share.
- Audit entries and webhook events: `share.created`, `share.redeemed`,
  `share.paused`, `share.resumed`, `share.revoked`, `share.updated`.
- Only superadmins create, edit, pause, resume, revoke. Admins can view.
  Services whose provider is `acnologia-peer` cannot be added to a share.

## Receiver side: connections

New table `peer_links`: id, peer_id, peer_name, peer_url, peer_public_key,
remote_share_id, token_enc (box encrypted), status, manifest json, prefix (the
slug prefix for local services), trusted bool, last_sync_at, last_error,
created_by, created_at.

- **Connect**: superadmin pastes an invite on the Sharing page. The server
  fetches `/.well-known/acnologia` from the URL in the invite, shows the owner
  id, name and fingerprint for confirmation, then redeems. Outbound calls use
  `catalog.SafeClient`; a "this is on my private network" checkbox (superadmin,
  like `target_trusted`) allows private addresses so two local servers can pair.
- For each shared **service** the receiver creates a local service: slug
  `<prefix>-<remote slug>` (prefix defaults to a slug of the peer name, editable
  at connect), provider `acnologia-peer` (a new built in spec, see below),
  base_url `<peer url>/proxy/<remote slug>`, bearer auth, and one hidden key
  whose secret is the share token. It shows "from <peer name>" in the
  catalog and service page. Local apps use it like any service.
- For each shared **provider spec** the receiver imports it as a local
  provider with source `peer:<peer id>` unless a provider with that slug
  exists (then skip and report the conflict). It can add its own keys.
- **Sync**: the worker refreshes every connected link's manifest every 10
  minutes and on demand ("Sync now"). New services are added, removed ones are
  disabled (not deleted, so events and app config survive), status changes
  are applied. A 403 `share_revoked` from the proxy or the manifest marks the
  link revoked and disables its services at once.
- **Disconnect**: deletes the stored token, disables the local services, and
  tells the owner (`POST /api/peer/v1/leave` with the token) so the owner sees
  "left by peer". Best effort.

## The `acnologia-peer` provider spec

A built in provider in `internal/providers` describing another Acnologia
proxy: bearer auth on `Authorization`, classification rules for the owner's
consumer errors: 403 `share_revoked` or `share_paused` → key invalid
(stops rotation), 401 → invalid, 429 `app_limit_reached` → exhausted until
`reset_at`, 503 `no_key_available` / `all_keys_busy` / `service_unavailable`
→ cooldown using `retry_after_sec`, anything else relayed from the upstream
provider → ok (the owner already rotated keys; do not punish the token).
Health check: `GET /api/peer/v1/manifest`.

Loop guard: the proxy adds `X-Acnologia-Hop: n+1` on requests to an
`acnologia-peer` upstream and refuses with 508 `loop_detected` past 3.

## API surface (dashboard, under `/api/admin`)

- `GET /identity`, `PATCH /identity` (name, url)
- `GET /shares`, `POST /shares` → `{share, invite}`, `GET /shares/{id}`
  (with usage), `PATCH /shares/{id}`, `POST /shares/{id}/pause`,
  `/resume`, `/revoke`, `POST /shares/{id}/invite` (new invite while pending)
- `GET /peers`, `POST /peers/preview` `{invite}` → owner identity,
  `POST /peers` `{invite, prefix, trusted}`, `POST /peers/{id}/sync`,
  `DELETE /peers/{id}`

Add these to `docs/api.md`, a "Sharing between servers" section in
`docs/flows.md`, the security model in `docs/security.md`, and
`docs/llms-full.txt`. Add MCP/assistant tools only if trivial; not required.

## UI

A **Sharing** page in the nav (BoardUI rules in `web/AGENTS.md`):

1. "This server" card: id (mono, copy), name (inline edit), URL, fingerprint.
2. **Shared by you**: rows with name, peer (or "waiting for invite"), service
   and provider chips, status chip, requests today, last used, actions (pause,
   resume, revoke with ConfirmButton, edit). "New share" opens a sheet: name,
   pick services (multi select, peer services excluded), pick providers, limits,
   expiry, invite validity, include hidden keys (superadmin). After create, the
   invite code is shown once with copy and a warning that it is shown once.
3. **Shared with you**: rows per peer link: peer name and id, status, services
   created locally (links), providers imported, last sync, Sync now,
   Disconnect. "Connect a server" opens: paste invite → preview owner identity
   and what is shared → choose prefix and private network → Connect.
4. Empty states explain the promise in one line: keys never leave the server
   that owns them.

## Calls left to the builder

Table and column names, how the provider only share's app is represented,
exact rate limits on the public peer routes, file layout, sync interval
constant, the exact icons.

## Verification

Go tests: identity stability across restarts, redeem happy path, wrong
signature, reused invite, expired invite, revoke blocks the next proxy call,
pause and resume, limits, manifest content has no secrets, the peer spec
classification, the hop loop guard, receiver sync adding and disabling
services. An end to end test that runs two servers in process (httptest) and
proxies a request from B through A to a fake upstream. `go test ./...` and the
web typecheck pass.
