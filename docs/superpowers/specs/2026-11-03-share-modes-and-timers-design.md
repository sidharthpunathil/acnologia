# Share modes (proxy or key handover) and share timers

Builds on `2026-11-03-server-identity-and-sharing-design.md`.

## Why

Today every share is proxy only: the other server sends requests through the
owner and never sees a key. The owner also wants a second mode where the keys
themselves go to the other server, so it can call the provider directly
(lower latency, keeps working while the owner is down). Both modes need
timers: a share that ends on its own, can be extended, and can end by
expiring or by being revoked for good.

## The honest constraint

A handed over key cannot be taken back. Revoke can only make a cooperating
server delete it. Everything below narrows that window and says so plainly;
nothing may claim otherwise in UI copy or docs.

## Modes

`shares.mode`: `proxy` (default, today's behavior) or `handover`. Chosen at
creation, shown everywhere, not changeable after redeem (create a new share
instead). Superadmin only, like all sharing writes.

### Proxy

Unchanged. Copy: "The other server never sees your keys. Revoke is instant."

### Handover

- Which keys: the share's key scopes decide (all, up to N, pick), same as
  proxy. Hidden keys only with include_hidden. Keys whose service provider is
  `acnologia-peer` can never be handed over.
- Encryption to the receiver: each server gets an X25519 key pair next to its
  Ed25519 identity (private part stored encrypted in meta like the identity
  seed), published as `box_public_key` in `/.well-known/acnologia` and
  recorded on the share at redeem (`peer_box_public_key`). Secrets are sealed
  to that key: ephemeral X25519, HKDF-SHA256 with a context string binding
  share id, key id and receiver id, AES-256-GCM. Use the standard library
  (`crypto/ecdh`, `crypto/hkdf`; `crypto/hpke` if this Go version ships it,
  check with `go doc crypto/hpke`). Never send a secret in clear inside the
  manifest, never log it, redact it everywhere.
- Delivery: a token authenticated `GET /api/peer/v1/keys` on the owner returns
  the sealed keys for the share (id, service slug, label, tier, sealed secret,
  `valid_until`). Only for handover shares that are active and not expired.
- Renewal window (lease): every handed over key carries `valid_until = now +
  lease`. Default lease 24h, choices 1h, 6h, 24h, 72h, 7d. The receiver renews
  on its regular sync (every 10 minutes today). The owner stops renewing when
  the share is paused, revoked, expired or the key leaves the scope.
- Receiver storage: keys go into a local service whose provider is the
  owner's provider (import it as a dependency like today), base URL from that
  provider, encrypted with the receiver's master key, marked hidden and
  restricted, labeled "Handed over by <peer>", with `peer_link_id` and
  `peer_valid_until` columns. Local apps use them like any key. The receiver
  cannot reveal, export, re share or lease them (lease hands out the raw key;
  block it, same as peer services).
- Wipe: the receiver deletes a handed over key (row and ciphertext) when:
  `valid_until` passes without renewal (worker check every minute), the
  manifest says paused, revoked or expired, the key left the scope, the link
  is disconnected, or a signed revoke arrives. Pause wipes too; resume hands
  the keys over again.
- Revoke now (push): the owner sends `POST <receiver url>/api/peer/v1/revoke`
  signed with the owner's Ed25519 key over (share id, receiver id, timestamp,
  nonce); the receiver verifies against the pinned owner key on the link,
  rejects stale timestamps (more than 5 minutes) and replays, and wipes at
  once. Best effort: if it fails, the lease still ends it. Record the outcome
  ("wiped at 10:42" or "not reached, wipes by 10:42 tomorrow") on the share.
- Owner side exposure record: table of (key id, share id, peer id, first
  handed at, last renewed at, wiped confirmed at). The owner's key list and
  key page show "Handed over to <peer>" while live and "Was handed over to
  <peer>" after. Revoking a handover share shows a checklist of exactly
  which keys to rotate at the provider, with a link to each provider's key
  page when the spec knows it.
- Usage: requests run on the receiver, so the owner cannot enforce request or
  budget limits. The receiver reports per key request counts on sync
  (cooperative); the owner shows them as "reported by the other server".
  Request and budget limits are disabled for handover shares in the UI with
  the reason; timers and lease still apply.
- Key health: when the receiver's pool marks a handed over key invalid or
  banned, it reports that on sync; the owner records the report on its key
  (does not change status automatically, shows "reported invalid by <peer>").

## Timers (both modes)

- `ends_at` (datetime, optional) replaces the day granularity `expires_at`
  in the UI (keep the column and API field for compatibility; `ends_at` maps
  to it). Quick choices: 1h, 24h, 7d, 30d, custom date and time, never.
- `end_action`: `expire` (default; the share stops working and can be
  extended back to active) or `revoke` (revoked for good, invite and token
  dead, handover keys wiped and push revoke sent).
- Extend: superadmin can move `ends_at` later on an active or expired (not
  revoked) share; an expired share becomes active again; for handover the
  keys are handed over again on the next sync.
- The worker checks ends_at every minute and applies end_action, with audit
  and webhook events (`share.expired`, `share.revoked` with reason timer).
- Invite expiry is unchanged.
- UI: a live countdown on each share row ("ends in 5h 12m"), the end action,
  and Extend.

## API changes

- `POST/PATCH /api/admin/shares`: `mode`, `lease` (handover), `ends_at`
  (RFC 3339 or ""), `end_action`.
- `POST /api/admin/shares/{id}/extend` `{ends_at}`.
- Share responses: `mode`, `lease`, `ends_at`, `end_action`, `handover:
  {keys: [{key_id, service, label, handed_at, renewed_at, valid_until,
  wiped_at, reported_requests, reported_status}], last_revoke_push: {at, ok,
  detail}}`, and `rotate_checklist` after revoke.
- `GET /api/peer/v1/keys` (share token), `POST /api/peer/v1/revoke`
  (signed by owner), sync payload carries usage and health reports from the
  receiver.
- `/.well-known/acnologia` adds `box_public_key`.
- Peer links: `mode`, handed over keys with `valid_until` (no secrets).

## Non goals

Handing keys to a server that has not redeemed an invite. Re sharing handed
over keys. Changing a share's mode after redeem.

## Tests

Seal and open round trip; tampered ciphertext and wrong receiver fail; the
context binding (a sealed secret for key A or share X does not open as key B
or share Y). Delivery only for active handover shares. Lease expiry wipes on
the receiver with no network. Pause, revoke, scope change and disconnect
wipe. Signed revoke: valid wipes, bad signature, stale timestamp and replay
rejected. Timer end with expire and with revoke, extend reactivates. Proxy
mode unchanged. Handover keys cannot be revealed, leased or re shared on the
receiver. No secret in logs, events, audit, webhooks or Claude input. Two
server end to end: handover, direct call to a fake provider from B, revoke
push wipes, and a second run where the push fails and the lease wipes.
