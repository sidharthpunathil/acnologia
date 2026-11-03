# Sharing with people who do not run Acnologia

Builds on `2026-11-03-server-identity-and-sharing-design.md` and
`2026-11-03-share-modes-and-timers-design.md`.

## Why

Most people the owner wants to give access to (a contractor, a friend, a
teammate with a laptop script or an AI agent) do not run their own
Acnologia. They should be able to use shared services with nothing but a
link, under the same limits, key scopes, timers and revoke as a server share.

## Recipient types

`shares.recipient`: `server` (today) or `person`. A person share has a label
(name, optional email used only as a label; no email is sent) instead of a
peer identity. Everything else on the share applies: key scopes, request and
budget limits (proxy only), timers (`ends_at`, `end_action`, extend), pause,
resume, revoke, audit, webhooks, usage.

## Modes for a person

- `proxy` (default): the person gets a personal app token bound to the share's
  backing app (mode proxy, allowed services = the share's services). Calls go
  through this server; keys never leave; revoke and pause refuse the next
  call. MCP access: the token also works at `/mcp` with the share's services
  (check how app MCP tool sets are configured and give share apps a sensible
  default, core plus service tools, no manage tools).
- `reveal`: the claim page shows the scoped raw keys once. The UI and docs say
  plainly that revoke cannot take them back. When a reveal share ends (timer,
  revoke) the owner gets the rotate checklist (same component as handover)
  and an audit entry; keys stay marked "Revealed to <label>" on the owner's
  key list and key page. Hidden keys are never revealable here, even with
  include_hidden. Superadmin only, like all sharing writes.

## Claim link

- Creating a person share returns a one time claim URL
  `<public url>/claim/<code>` (code shown once, stored hashed, validity 1h,
  24h default, 7d; a new link can be issued while unclaimed).
- `GET /claim/<code>` serves the SPA page; the page calls
  `POST /api/claim/v1/preview` (what is shared, by whom, ends when; no
  secrets) and, on the person pressing "Get my access",
  `POST /api/claim/v1/redeem`, which burns the code and returns the token (or,
  for reveal, the keys) exactly once. Rate limited like the peer routes.
- The claim routes and `/claim/*` must work on a deploy locked by the access
  password, exposing nothing else (check server/internal/api/gate.go and how
  app tokens on /proxy, /api/v1 and /mcp already pass the lock).
- After redeem the page shows, once: the token (SecretBox with copy), the base
  URL, per service snippets (curl, Python, TypeScript; reuse the Integrate
  snippet builders where possible), an MCP config block for Claude Code and
  Cursor, the limits and the end time, and a warning that the token is shown
  once. For reveal: each key with service and label, copy buttons, and the
  warning that the owner can ask them to stop using the keys at any time.

## My access page

- `/me` SPA page for a person: they paste their token (kept in localStorage
  only if they tick "remember on this device", wrapped in try/catch). It calls
  `GET /api/v1/me` with the token: share name, owner server name, services
  (with available counts), limits with usage today and this month, ends_at,
  status (active, paused, expired, revoked). Proxy shares only; a reveal
  share has no token after claim.
- `GET /api/v1/me` works for any app token (useful for every app), returning
  the app's own limits and usage; share fields only for share apps.

## Owner UI

Sharing page: "Shared by you" lists servers and people (filter chips: All,
Servers, People). New share drawer starts with "Share with: Another server /
A person". Person rows show label, mode, claimed or waiting (with "New link"),
usage, limits meters, timer countdown, pause, resume, revoke, extend. Reveal
shares show the rotate checklist on end.

## API

- `POST /api/admin/shares` accepts `recipient` (`server` | `person`),
  `label`, `email`, and for person `mode` (`proxy` | `reveal`); returns
  `claim_url` for person shares (instead of `invite`).
- `POST /api/admin/shares/{id}/claim-link` `{valid}` for a new link while
  unclaimed.
- Share responses add `recipient`, `label`, `email`, `claimed_at`,
  `claim_expires_at`, `revealed_keys` (ids, service, label; no secrets).
- Public: `POST /api/claim/v1/preview` `{code}`, `POST /api/claim/v1/redeem`
  `{code}`. Consumer: `GET /api/v1/me`.

## Non goals

Accounts or passwords for people; sending email; letting a person create
their own tokens; handing keys to a person without the reveal warning.

## Tests

Claim preview has no secrets; redeem once (second redeem 409, expired 410,
wrong code 404, rate limit); token works on /proxy and /mcp only for the
shared services and respects key scopes, limits and timers; pause and revoke
refuse the next call; `/api/v1/me` for share and non share apps; reveal shows
scoped non hidden keys once and records exposure; rotate checklist on end;
claim routes and the person's calls work behind the access password lock and
nothing else opens; no code, token or key in logs, events, audit, webhooks or
Claude input.
