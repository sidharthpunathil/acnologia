## OpenQodex: only when asked, at the end of a project
- Never ask about an OpenQodex review and never run one, for any push to any branch, `main` included: just push.
- The developer runs one complete scan when the project ends. Run it only when they ask, as they say (the whole
  change, or `npx -y openqodex@0.19.0 review --all`), in the background (it can take a long time; OpenQodex stops the
  reviewer after 30 minutes). Show the receipt (verdict, one line per finding, the absolute path of `report.html`) and
  ask "Fix all, or tell me which?"
- No hooks: no push gate and no git pre-push hook (removed on 2026-11-22). Never install one.
- Never add a file to `review.instructions_files` on your own. Ask the developer which files the reviewer may use, then
  set only what they name. The report is in `.openqodex/reviews/`.

## Invariants

Security and design rules the code enforces. Do not break one without the developer's say so; each names where it
lives, so check there first.

- Proxy mode never hands the key out. The proxy puts the pool key on the upstream request and scrubs it (raw, query
  and path escaped) from relayed headers and bodies, streams included (`api/proxy.go`, `api/proxy_scrub.go`).
- App tokens never reach a provider: `X-Acnologia-Token`, `acn_token` query params and a Bearer app token are
  stripped before forwarding, with the hop headers (`buildUpstream` in `api/proxy.go`).
- A request that carries a key never follows a redirect (`CheckRedirect` returns `ErrUseLastResponse` in
  `api/proxy.go`, `pool/pool.go`, `egress/egress.go`).
- The SSRF checks run in the dialer's `Control` hook on the resolved address of every connection, never on the URL
  alone: `catalog.NoMetadata` for service calls, `catalog.PublicOnly` for peer services, passthrough tunnels and
  `SafeClient` (`catalog/netguard.go`, `catalog/logo.go`).
- Hidden keys go only to a superadmin or an app with `lease_hidden`, and only to a service whose target a superadmin
  confirmed (`target_trusted`) (`pool.MayUse`, `pool/pool.go`). A reveal share never shows one (`api/people.go`).
- Keys another server handed over, and peer share tokens, are proxy only: never leased, revealed or re-shared
  (`api/consumer.go`, `revealKey` in `api/admin.go`, `api/sharing.go`).
- Every key reveal is audited, and the secret is returned only after the audit row is stored (`revealKey` in
  `api/admin.go`, `Store.AddAuditErr`).
- Read-only app tokens never get a raw secret and never reach a write: no lease, renew or report
  (`api/consumer.go`), only GET, HEAD and OPTIONS through `/proxy` (`api/proxy.go`), only read-only MCP tools
  (`api/mcp.go`), and no forward proxy passthrough (`api/forward.go`).
- The forward proxy is never an open relay: plain HTTP to a host that is no service gets 403 `not_a_service`, and
  passthrough is a blind tunnel to public addresses on 443 and 8443, never offered to share or read-only tokens
  (`api/forward.go`).
- Secrets are stored only through `crypto.Box` (encrypted with the master key). Text that is stored or sent to Claude
  goes through `Store.Scrub` (`internal/redact`), and lease report messages are scrubbed of the key
  (`api/consumer.go`).
- Request logs never carry a query string (`requestLog` in `api/server.go`).
- Mutating dashboard calls need a JSON content type or `X-Requested-With`, so a cross site form cannot post with the
  session cookie (`jsonOnly` in `api/server.go`). Routes are locked by the access gate unless listed open
  (`api/gate.go`), and admin routes sit in the right `requireRole` group (`api/server.go`).
- The client IP comes from the socket, or with `ACNOLOGIA_TRUST_PROXY_HEADERS` from the right of `X-Forwarded-For`
  (or one named header), never from headers a client controls (`internal/clientip`).
- Schema changes go in a numbered migration step appended to `migrations`, never in the frozen baseline: `migrate`
  and the `schema` constant (`store/store.go`; see "Changing the schema" in `docs/development.md`).
