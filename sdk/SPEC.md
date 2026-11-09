# SDK surface (version 0.2.0)

The contract every SDK in this folder follows. Names follow each language's
idiom (`forwardProxy` in TypeScript, `forward_proxy` in Python and Ruby,
`ForwardProxy` in Go and C#), but the calls, fields and behavior are the
same. The server side is in [docs/api.md](../docs/api.md#consumer-api) and
[docs/llms-full.txt](../docs/llms-full.txt).

## Client

`new Client(baseURL, token, options)`. Except where a language never read
the environment before (Go, PHP, TypeScript), a missing URL or token falls
back to `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`.

Options, all optional:

| Option | Effect |
|---|---|
| `accessPassword` | Sends `X-Acnologia-Access` on every request, for a deploy behind the access password. Falls back to `ACNOLOGIA_ACCESS_PASSWORD` where the SDK reads the environment. |
| `userAgent`, `timeout`, custom HTTP client | As each SDK already had. |

Every request sends `Authorization: Bearer <token>`, except
`forwardProxyCA`, which is public and sends no token.

## Calls

| Call | Request | Returns |
|---|---|---|
| `me()` | `GET /api/v1/me` | `Me`: `app {id, name}`, `limits {daily_requests, monthly_requests, daily_budget, monthly_budget}` (each nullable), `usage {today_requests, today_cost, month_requests, month_cost, last_used_at, limit_hit}`, `limit_hit`, `services [{slug, name, provider, category, available, logo_url}]` (`available` here is an integer: free keys for a share's token, 1 or 0 for an app; in `services()` it is a boolean), `share` (null for an ordinary app; for a share's token `{id, name, owner_name, recipient, label, status, mode, ends_at}`). A share token whose share stopped still gets 200 here, with `status`. |
| `services()` | `GET /api/v1/services` | `[Service]`: `slug, name, provider, category, available, base_url, auth {type, name, prefix}, guide_url`. |
| `guide(slug)` | `GET /api/v1/services/{slug}/guide` | `Guide`: `markdown` (string), `generated` (bool), `guide` (the structured guide, kept as the language's generic JSON value). |
| `guideMarkdown(slug)` | same, with `Accept: text/markdown` | the Markdown as a string. |
| `capabilities()` | `GET /api/v1/capabilities` | `[Capability]`: `slug, name, description, strategy, params` (generic JSON), `routes [{service, mapped}]`, `url`. |
| `capability(slug, params, opts)` | `POST /proxy/{slug}` with `params` as the JSON body; `opts.service` adds `?_service=<slug>`, `opts.raw` adds `?_raw=1` | the SDK's proxy response type (below). |
| `forwardProxy()` | `GET /api/v1/proxy` | `ForwardProxy`: `proxy_url, proxy_url_with_token, ca_url, ca_fingerprint, passthrough, hosts [{host, service, name, path}], env` (string map), `service_header`. |
| `forwardProxyCA()` | `GET /api/v1/proxy/ca.pem`, no token | the PEM text. Languages with a file system may add a variant that saves it to a path (default `acnologia-ca.pem`). |
| `forwardProxyURL()` | `forwardProxy()`, then local | `proxy_url` with user `app` and the client's token as the password, percent-escaped: `http://app:acn_...@host:8888`. |
| `forwardProxyEnv(caPath = "acnologia-ca.pem")` | `forwardProxy()`, then local | a string map: `HTTPS_PROXY` and `HTTP_PROXY` (the `forwardProxyURL`), `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` set to `caPath`. |

The existing calls stay as they are: proxy requests, `lease`, `report`
(with `rotate`), `renew`, and `withKey`.

`String`, debug and log forms of `ForwardProxy` never contain the token
(the server's `proxy_url_with_token` has a `<app token>` placeholder, so
it is safe). `forwardProxyURL` and `forwardProxyEnv` do contain it: say so
in their doc comments.

Where the language's standard HTTP client takes a proxy and extra CA roots
without a new dependency, add a helper that returns a configured client
that sends everything through the forward proxy, for example Go's
`ForwardProxyTransport(ctx) (*http.Transport, error)` (proxy URL with the
token, `RootCAs` = system pool plus the Acnologia CA). Optional elsewhere.

## Proxy requests

Per call options, added where missing:

| Option | Header |
|---|---|
| `idempotencyKey` | `Idempotency-Key: <value>` |
| `noCache` | `Cache-Control: no-cache` |

The proxy response type exposes, alongside status, headers and body:

| Accessor | From |
|---|---|
| `keyId` | `X-Acnologia-Key-Id` (integer, absent for a share's token) |
| `attempts` | `X-Acnologia-Attempts` (integer) |
| `service` | `X-Acnologia-Service` |
| `cache` | `X-Acnologia-Cache` (`hit`, `miss`, ...) |
| `replayed` | `Idempotent-Replayed: true` |
| `capability` | `X-Acnologia-Capability` |
| `tried` | `X-Acnologia-Tried` (`service=status` pairs, comma separated), parsed into a list of `{service, status}` |

A `{service}` in a proxy path may also be a service group or a capability
slug; nothing changes in the SDK for that.

## Errors

Every API error carries the HTTP status, the server's `error.code`, the
message and, when given, `retry_after_sec` (or the `Retry-After` header).
Add these typed errors where missing (subclasses, enum cases or error
kinds, as the SDK already models errors):

| Error | When |
|---|---|
| `AppLimitReached` | 429 `app_limit_reached`. Carries `limit` (`daily_requests`, `monthly_requests`, `daily_budget`, `monthly_budget`), `reset_at` and retry after. |
| `ShareUnavailable` | 403 `share_revoked`, `share_paused`, `share_expired`. Carries the status (`revoked`, `paused`, `expired`), also sent as `X-Acnologia-Share`. |

The existing no key available, all keys busy and service unavailable errors
stay. Other codes worth recognizing by `code` on the generic error:
`empty_group`, `bad_params`, `no_route`, `no_route_worked`,
`proxy_not_allowed`, `lease_not_allowed`, `loop_detected`.

## Tests and docs

Each new call has an offline test against the SDK's existing stub or fake
server: the request path, method and headers, and the parsed answer. The
SDK's README documents every new call with a short example, including the
forward proxy (`forwardProxyEnv`, then the app's own HTTP client).
