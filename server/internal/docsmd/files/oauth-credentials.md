# OAuth credentials

Some APIs take no API key. You sign in to an account, approve what the app may do, and the API is called with a short lived access token that a refresh token renews. Google Ads, Search Console, Google Analytics 4, YouTube Data, Microsoft Graph, Slack, HubSpot and LinkedIn all work this way. Acnologia pools these accounts the way it pools keys: each connected account is one key in a service, so rotation, cooldowns, health checks, hidden keys and per app limits all apply.

This page covers OAuth for the APIs Acnologia calls. Signing MCP clients in to Acnologia itself is a different feature, described in [MCP sign in](mcp-oauth.md).

## How it works

1. You register an OAuth app with the provider (a Google Cloud OAuth client, a Microsoft Entra app registration and so on) and give it Acnologia's redirect URI.
2. You enter that app's client id and secret on the service. They are stored encrypted, like keys.
3. You press Connect account. Acnologia makes a single use sign in link, bound to you and the service, with a PKCE challenge when the provider supports it, and sends the browser to the provider.
4. The account's owner signs in and approves. The provider sends the browser back to Acnologia, which trades the code for an access token and a refresh token, stores both encrypted, and adds the account as a key labelled with its email when the provider names it.
5. Before each request Acnologia checks the access token. If it expires within 60 seconds it is refreshed first, once per key however many requests arrive together. A provider that rotates refresh tokens gets the new one stored at once.

Connect more accounts to the same service and requests rotate across them, as they would across keys.

## The redirect URI

Register this URI with the provider, exactly:

```
https://your-server.example/oauth/callback
```

It is your `ACNOLOGIA_PUBLIC_URL` plus `/oauth/callback`. The service page shows it, and so do `acn oauth status <service>` and the `oauth_settings` tool. Set `ACNOLOGIA_PUBLIC_URL` before you register it: without it the URI is built from the address you reached the server at, which changes behind a proxy.

The callback is open past the access password, because the person signing in may not be you. The link's state is the credential: it is random, works once, and expires after 15 minutes.

## Setting up a service

In the dashboard, add a service from an OAuth provider (Google APIs, Google Ads, Microsoft Graph, Slack, HubSpot and LinkedIn ship built in), open it, and fill in the OAuth client card: client id, client secret and, if you want other than the provider's defaults, the scopes. For Microsoft Graph the card also takes the tenant. Then press Connect account.

From a terminal:

```bash
acn oauth setup google --client-id 1234.apps.googleusercontent.com --scopes openid,email,https://www.googleapis.com/auth/webmasters.readonly
acn oauth connect google
```

For a provider whose sign in URLs take a tenant (Microsoft Graph), add `--tenant organizations`, `--tenant consumers`, or your tenant id or domain. Leave it out to keep the stored tenant, or pass `--tenant ""` to go back to the provider's default (`common`).

`acn oauth connect` prints the sign in link, opens it in your browser, and waits until the sign in finishes. Pass `--no-browser` on a server over SSH, or `--no-wait` to print the link and return. Repeat `--field key=value` for the provider's extra fields.

`acn oauth status <service>` lists the connected accounts, their state and when each access token expires.

## For agents

An agent cannot click through a consent screen, so the control MCP tools hand the link to a person:

| Tool | What it does |
|---|---|
| `oauth_settings` | The client id (never the secret), the redirect URI, scopes, the tenant (`takes_tenant`, `tenant`, `default_tenant`), extra fields and connected accounts. |
| `set_oauth_client` | Sets the client id, secret, scopes and, where the provider takes one, the tenant. |
| `oauth_connect_url` | Starts a connect. Returns `url` for a person to open and `connect_id`. |
| `oauth_connection_status` | `pending`, `exchanging`, `done` (with `key_id` and `account`), `failed` (with `error`) or `expired`. |
| `oauth_reconnect` | A link that signs a key's account in again, keeping the key. |

Give the user the `url`, then poll `oauth_connection_status` every few seconds until it is `done` or `failed`. The same routes are in the [HTTP API](api.md): `GET` and `PUT /api/admin/services/{id}/oauth`, `POST /api/admin/services/{id}/oauth/connect`, `GET /api/admin/oauth/connects/{connect_id}` and `POST /api/admin/keys/{id}/oauth/reconnect`.

## Extra fields

Some APIs want more than the token. Google Ads needs a developer token on every call, and a manager account id when the account reaches customers through a manager account. A provider lists these as extra fields; you give them when you connect, and they are stored encrypted with the key and sent as headers on every request. Required fields must be set; optional ones are sent only when set.

## When access is revoked

If the provider refuses the refresh token (`invalid_grant`: the owner revoked access, changed their password, or the refresh token expired), Acnologia marks the key invalid. It leaves rotation, the other accounts keep serving, and the service page shows Reconnect next to it. Reconnecting signs the same account in again and puts the key back as it was, with its id, label, settings and fields.

A provider may also answer 401 to an access token before it expires. Acnologia then refreshes once and retries the request on the same key. Only if that fails too does the response's rule mark the key. The same goes for a service's upstream MCP server: a tool call or a tool sync refused with 401 refreshes the token once and calls again. An MCP server that answers HTTP 200 with a JSON-RPC error plainly about the credential (such as "Unauthorized" or "invalid token", but not a missing scope or permission) is treated like a 401.

If the token endpoint is down, the key steps out of rotation for 30 seconds and the request moves to the next account.

## Leases and reveals

- Proxy mode is the way to use OAuth accounts: the token never reaches the caller, and it is scrubbed from responses like a key.
- A lease of an OAuth key returns the current access token, never the refresh token. With extra fields the lease's `secret` is a JSON object of `access_token` and the fields. An access token usually lasts an hour, so lease again when it stops working rather than reporting the key invalid.
- Revealing an OAuth key shows its connection id and its extra fields, never a token. The refresh token never leaves the server.
- A handover share (keys sealed to another server, which then calls the provider itself) cannot include an OAuth service. The key is only a pointer to tokens stored here, so handing it over would mean sending the refresh token, which lets the other server mint access tokens until the account owner revokes it, and a lease or a revoke could never take it back. Such a share is refused with `oauth_handover_refused`. Share the service in proxy mode instead: the other server's calls go through this one and the tokens stay here.
- Every token and the client secret are known to the redactor, so they are scrubbed from events, logs, the audit log and anything sent to Claude.

## Provider notes

- **Google.** Google returns a refresh token only with `access_type=offline` and, on a later sign in, only with `prompt=consent`; the built in specs send both. While your OAuth app's publishing status is Testing, Google expires its refresh tokens after 7 days, so publish the app (or reconnect weekly). Sensitive scopes need Google's verification before people outside your organization can approve them.
- **Google APIs** pick the API with a path segment: `/proxy/google/~searchconsole/webmasters/v3/sites`, `/~analyticsdata/...`, `/~youtube/...`. Add each API's scope on the service and reconnect.
- **Google Ads** paths start with the API version. The built in examples use v26, the newest when this release shipped (Google's sunset dates page, read 2026-11-25, lists v23 as the oldest still served). Google sunsets each version about a year after it ships, so call a version it still supports. The version is one constant, `GoogleAdsAPIVersion` in `server/internal/providers/builtin_oauth.go`; change it there to move the examples, or edit the provider's spec on your server.
- **Microsoft Graph** signs in through the service's tenant, `common` (work, school and personal accounts) unless you set another: `organizations`, `consumers`, or your tenant id or domain for an app registered for one organization only. Set it on the OAuth client card, with `acn oauth setup --tenant`, or with `tenant` in `set_oauth_client`. Microsoft rotates refresh tokens; Acnologia keeps the newest.
- **Slack** installs your Slack app in a workspace and uses its bot token. The default scopes (`channels:read`, `users:read`, `team:read`) only read; add `chat:write` and the others your calls need, then reconnect. Each workspace is one key, named after the team. Slack answers most errors with HTTP 200 and `{"ok": false, "error": ...}`; `invalid_auth`, `not_authed`, `token_revoked` and `account_inactive` mark the key invalid. Tokens last until revoked unless token rotation is on in the Slack app, in which case they last 12 hours and are refreshed for you.
- **HubSpot** asks for the `oauth` scope only. HubSpot refuses a sign in whose scopes differ from the required scopes of your HubSpot app, so set the same scopes on the service (for example `oauth crm.objects.contacts.read`). Each portal is one key, named after its portal id. Access tokens last 30 minutes and are refreshed for you; the token endpoint is v3, as HubSpot's v1 endpoint stops working in February 2027.
- **LinkedIn** signs members in with OpenID Connect (`openid profile email`), and each member is one key named after their email. LinkedIn gives refresh tokens only to some approved partners, so most accounts need a reconnect when their access token ends after 60 days. Calls under `/rest` need the `LinkedIn-Version` (YYYYMM) and `X-Restli-Protocol-Version: 2.0.0` headers, which the caller sends.

## Adding another OAuth provider

Any provider whose spec has `auth.type` `oauth2` works. See [Provider specs](providers.md#oauth-providers) for the `oauth` block. Most need only the authorize and token URLs and their scopes. `token_path` reads a token the provider nests in its answer (Slack's user token is `authed_user.access_token`), and `{tenant}` in the URLs with a default `tenant` lets each service sign in through its own tenant.
