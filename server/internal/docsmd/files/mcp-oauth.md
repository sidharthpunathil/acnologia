# Connecting MCP clients with OAuth

Hosted MCP clients such as claude.ai custom connectors, ChatGPT connectors, Claude Code and Cursor can connect to Acnologia's `/mcp` endpoint by signing in. You approve the connection in the dashboard, pick the app it acts as, and the client receives short lived tokens that renew on their own. App tokens (`acn_...`) keep working as before, so clients that take a header can still use one.

## What the server needs

- A public HTTPS URL. claude.ai and ChatGPT connect from their own servers, so they cannot reach `localhost` or a private network. Claude Code and Cursor run on your machine and can use a local URL.
- `ACNOLOGIA_PUBLIC_URL` set to that URL (see [Configuration](configuration.md)). The issuer, the endpoints in the metadata and the resource every token is bound to all come from it. Without it the server uses the host of each request, which breaks behind a proxy that rewrites the host.
- An admin account for `/mcp`. Only admins and superadmins can approve an app connection, the same people who can create an app's tokens. The control MCP has its own rule, below.

If the access password is on, the discovery, registration, token and revocation endpoints stay reachable. The consent page sits behind the lock screen like the rest of the dashboard.

## Adding Acnologia to claude.ai

1. In claude.ai, open Settings, Connectors, and add a custom connector.
2. Enter `https://your-server.example/mcp` as the URL and leave the OAuth client fields empty.
3. Claude opens your Acnologia dashboard. Sign in if asked, choose the app the connector acts as (or create one), decide whether it is read only, and press Approve.

The browser returns to claude.ai and the connector lists the app's MCP tools.

## Adding Acnologia to ChatGPT

In ChatGPT, open Settings, Apps and Connectors (developer mode must be on), and create a connector with `https://your-server.example/mcp` as its URL and OAuth as its authentication. ChatGPT registers itself and sends you to the same consent page.

## Claude Code and Cursor

Add the server with its URL and no token, then sign in when the client asks:

```sh
claude mcp add --transport http acnologia https://your-server.example/mcp
```

In Claude Code, run `/mcp` and pick the server to start the sign in. Cursor shows a Login button next to the server in its MCP settings.

## What a connection can do

A connection acts as the app you chose, exactly like one of the app's tokens. The app's allowed services, mode, limits, MCP settings and budget all apply. Each connection also gets the app's per-token default limits as a cap of its own, so one client cannot spend the whole app's budget. With read only ticked, the client sees only read only tools: no raw keys and no tool that changes anything.

A connection stops working when:

- you revoke it from the app's tokens panel, under MCP sign ins;
- the app is paused or deleted;
- the person who approved it is disabled, deleted or no longer an admin.

Access tokens last one hour. Refresh tokens last 30 days and change on every refresh. If a refresh token that was already used comes back, Acnologia revokes the whole connection, because it cannot tell the client from someone who copied the token. The client then has to sign in again. Every approval, revocation and reuse lands in the audit log.

## Endpoints

| Endpoint | Purpose |
| --- | --- |
| `GET /.well-known/oauth-protected-resource/mcp` | Protected resource metadata (RFC 9728). Also served without the `/mcp` suffix. |
| `GET /.well-known/oauth-protected-resource/mcp/control` | Protected resource metadata for the control MCP. |
| `GET /.well-known/oauth-authorization-server` | Authorization server metadata (RFC 8414). |
| `POST /oauth/register` | Dynamic client registration (RFC 7591), public clients only. Rate limited per address. |
| `GET /oauth/authorize` | Starts a sign in and sends the browser to `/oauth/consent`. |
| `POST /oauth/token` | `authorization_code` and `refresh_token` grants. |
| `POST /oauth/revoke` | Token revocation (RFC 7009). Revoking either token ends the connection. |

A 401 from `/mcp` carries `WWW-Authenticate: Bearer resource_metadata="<base>/.well-known/oauth-protected-resource/mcp"` (and from `/mcp/control`, the control MCP's metadata URL), and `error="invalid_token"` when the request sent a token, so a client with an expired access token refreshes without asking you.

The rules the server enforces:

- PKCE with `S256` on every authorization request.
- `resource`, when sent, must be `<base>/mcp` or `<base>/mcp/control`. A token works only on the resource it was issued for, never on `/proxy`, `/api/admin` or the consumer API.
- The scope is `mcp` for `/mcp` and `mcp:control` for `/mcp/control`. Without a `resource`, asking for `mcp:control` picks the control MCP.
- Redirect URIs must be https, `http://localhost`, `http://127.0.0.1` or `http://[::1]` (any port), or a native app scheme such as `cursor://`. They must match what the client registered, and fragments are refused.
- A `client_id` that is an https URL is read as a client ID metadata document. Acnologia fetches it from a public address only, without following redirects, and its `client_id` must equal its URL.
- Codes last 60 seconds and work once. Using a code a second time revokes the tokens the first use produced.
- Codes and tokens are stored as SHA-256 hashes, like app tokens.

## Connecting the control MCP

The control MCP at `/mcp/control` runs the server itself: services, keys, apps, providers, sharing, users and the audit log. Hosted clients sign in to it the same way, so claude.ai and Claude Code do not need an admin API token pasted in. Admin API tokens keep working there too.

Warning: a control MCP connection can change your server. With write or admin access the client can add and delete services, keys and apps, and with admin it can also reveal secrets and manage users. Approve only clients you trust, and pick the lowest access that does the job.

To add it to claude.ai, add a custom connector with `https://your-server.example/mcp/control` as the URL. In Claude Code:

```sh
claude mcp add --transport http acnologia-control https://your-server.example/mcp/control
```

The consent page is different from the app one. There is no app to pick, because the connection acts as you. You choose its access (read, write or admin), which works like an admin API token's scope: read acts as a member, write as an admin and admin as a superadmin. It starts at read and can never go above your own role. Anyone who can create an admin API token can approve a connection, at the same scopes.

What a control connection can do, and when it stops:

- It works on `/mcp/control` only. It is refused on `/mcp`, `/api/admin`, `/proxy` and the consumer API, and an `/mcp` token is refused on `/mcp/control`.
- Inside the control MCP it behaves like an admin API token of its scope: the same tools, role checks and read only option. Like a token, it cannot manage tokens or change your account.
- The audit log records its changes as you, with via `oauth` and the grant in the client (`oauth-grant/<id>`).
- It is revoked when you are disabled or deleted, or when your role drops below its access. The server checks this on every call and every refresh.
- You can revoke it under Control MCP sign ins on your account page, below your API tokens. A superadmin sees everyone's and can revoke any of them.
- Its tokens last and rotate as for `/mcp`, and a reused refresh token revokes the connection.

If the access password is on, `/mcp/control` stays locked, but its 401 still points at the metadata, and the access token opens it like an admin API token does.
