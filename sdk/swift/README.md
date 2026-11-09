# Acnologia for Swift

Call the APIs your Acnologia server holds keys for, from Swift, with your app's own `acn_` token. Send requests through the proxy and the key never reaches your process. Or lease a key, call the provider yourself, and let `withKey` report each attempt and move to the next key on a rate limit, an empty quota or a rejected key.

Foundation only, async/await on `URLSession`, `Sendable` types. Requires Swift 5.9 or later and runs on macOS 13+, iOS 16+, tvOS 16+, watchOS 9+ and Linux (through `FoundationNetworking`).

## Quick start

```swift
import Acnologia

let acn = try Acnologia(baseURL: "http://localhost:8080", token: "acn_your_app_token")
let resp = try await acn.proxy("weather").get("/v1/forecast", query: [URLQueryItem(name: "city", value: "Lisbon")])
print(resp.statusCode, resp.text)
```

`weather` is the slug of a service on your server. Acnologia attaches the key, sends the call on, and returns the provider's response.

## Install

The repository root has a `Package.swift` that exposes this SDK, so depend on the repository URL directly:

```swift
// Package.swift
dependencies: [
    .package(url: "https://github.com/sidharthpunathil/acnologia", branch: "main"),
],
targets: [
    .target(name: "MyApp", dependencies: [.product(name: "Acnologia", package: "acnologia")]),
]
```

In Xcode, use File > Add Package Dependencies, paste `https://github.com/sidharthpunathil/acnologia`, pick the `main` branch, and add the `Acnologia` library to your target.

To work on the SDK itself, open `sdk/swift`: its own `Package.swift` builds the same sources.

## Connect to your server

Create an app on the Apps page of the dashboard and copy its token. Tokens with the older `cog_...` prefix still work.

`Acnologia.fromEnvironment()` reads the URL and token from the environment and throws `AcnologiaError.invalidArgument` when one is missing. Pass a dictionary to read from somewhere else.

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | Server URL, such as `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | App token, `acn_...` |
| `ACNOLOGIA_ACCESS_PASSWORD` | Optional. The server's access password, sent as `X-Acnologia-Access` |

The initializer and `fromEnvironment` take the rest as arguments:

| Argument | Default | Notes |
|---|---|---|
| `configuration` | `.default` | A `URLSessionConfiguration`. |
| `userAgent` | `acnologia-swift/0.2.0` | |
| `timeout` | 75 seconds | Bounds API calls. It is longer than the 60 second maximum lease wait. Proxy calls use the session's own timeouts. |
| `accessPassword` | `nil` | For a deploy behind the access password: sent as `X-Acnologia-Access` on every request. `fromEnvironment` falls back to `ACNOLOGIA_ACCESS_PASSWORD`. |

`Acnologia(baseURL:token:)` takes a `URL`, or a `String` (that form throws `.invalidArgument` for an empty or relative URL or an empty token).

## Call an API through the proxy

`acn.proxy(service)` returns a `Proxy` whose requests go to `/proxy/{service}/{path}`. The key stays on the server.

```swift
let weather = acn.proxy("weather")
let resp = try await weather.get("/v1/forecast", query: [URLQueryItem(name: "city", value: "Lisbon")])
print(resp.statusCode, resp.keyID ?? 0, resp.attempts ?? 0)
let forecast = try resp.decode(Forecast.self)   // Forecast is your own Decodable type

let alert = try await weather.postJSON("/v1/alerts", ["city": "Lisbon"])
```

- Upstream answers come back as a `ProxyResponse` whatever their status.
- Redirects are not followed, so the app token is never sent to another host.

Every request helper takes `options: ProxyOptions(idempotencyKey:noCache:)`. `idempotencyKey` sends `Idempotency-Key`, so a retry replays the first answer instead of calling the provider twice; `noCache` sends `Cache-Control: no-cache`.

```swift
let resp = try await acn.proxy("payments").postJSON("/v1/charges", charge,
                                                    options: ProxyOptions(idempotencyKey: "order-42"))
if resp.replayed { print("answered from the first try") }
```

Other helpers: `request(method, path, query:headers:body:options:)`, `post`, `put`, `patch`, `delete`, `url(path, query:)`, and `send(URLRequest)`, which accepts a relative URL such as `/v1/forecast?city=Lisbon`.

`ProxyResponse` has `statusCode`, `isSuccess`, `headers`, `data`, `text`, `header(_:)` and `decode(_:)`, and Acnologia's headers:

| Property | Header |
|---|---|
| `keyID` | `X-Acnologia-Key-Id` (absent for a share's token) |
| `attempts` | `X-Acnologia-Attempts` |
| `service` (also `servedBy`) | `X-Acnologia-Service` |
| `cache` | `X-Acnologia-Cache` (`hit`, `miss`, ...) |
| `replayed` | `Idempotent-Replayed: true` |
| `capability` | `X-Acnologia-Capability` |
| `tried` | `X-Acnologia-Tried`, parsed into `[TriedRoute]` (`service`, `status`) |

When Acnologia itself refused the request (no key, circuit breaker open, bad token), `resp.acnologiaError` holds the typed error and `try resp.throwingAcnologiaError()` throws it. Provider answers carry `X-Acnologia-Key-Id` and are never treated as Acnologia errors.

## Lease a key with `withKey`

`withKey` leases a key, runs the body, reports the outcome, and rotates when the body throws one of the rotation errors:

| Body | Reported outcome | Rotates |
|---|---|---|
| returns | `ok` | no |
| throws `AcnologiaError.rateLimited(retryAfter:)` | `rate_limited` | yes |
| throws `AcnologiaError.quotaExhausted()` | `quota_exhausted` | yes |
| throws `AcnologiaError.invalidKey()` | `invalid` | yes |
| throws anything else | `error` | no, the error is rethrown as is |

```swift
let forecast: Forecast = try await acn.withKey("weather", maxAttempts: 3) { lease in
    var req = URLRequest(url: try lease.url("/v1/forecast", query: [URLQueryItem(name: "city", value: "Lisbon")]))
    lease.apply(to: &req)   // adds the key where the service expects it
    let (data, resp) = try await URLSession.shared.data(for: req)
    switch (resp as? HTTPURLResponse)?.statusCode ?? 0 {
    case 429:
        throw AcnologiaError.rateLimited(retryAfter: 3600, statusCode: 429)
    case 402:
        throw AcnologiaError.quotaExhausted(statusCode: 402)
    case 401:
        throw AcnologiaError.invalidKey(statusCode: 401)
    case 400...:
        throw URLError(.badServerResponse)
    default:
        return try JSONDecoder().decode(Forecast.self, from: data)
    }
}
```

Providers differ (some send 429 for an empty quota), so check the body when it matters and throw the error that fits.

How it ends:

- After `maxAttempts` keys (default 5) the last rotation error is thrown. The final attempt is reported without asking for a next key.
- When the pool runs dry, `.noKeyAvailable` or `.allKeysBusy` is thrown.
- Every lease is reported, also when the surrounding task is cancelled (the report goes out from a detached task).
- Messages sent with a report have the secret replaced by `[key]` and are cut to 500 characters.
- `withKey` takes `options: LeaseOptions(...)`. Its `ttl`, `wait` and `exclusive` apply to every lease it takes, and `excludeKeyIDs` to the first.

## Lease and report by hand

```swift
let lease = try await acn.lease("weather", options: LeaseOptions(ttl: 300, wait: 10))
// ... call the provider with lease.apply(to: &request) ...
let next = try await lease.report(.rateLimited(retryAfter: 3600), statusCode: 429, rotate: true)
if let next { /* retry with next */ }

let expires = try await lease.renew(ttl: 600)   // for long jobs
```

Report on every path. On services with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out. With `rotate: true` the report returns the next lease when another key is available.

`lease.renew(ttl:)` extends an open lease and returns the new expiry. `nil` uses the server default of 120 seconds, and the server caps it at 24 hours. It throws `.api` with code `lease_not_found` when the lease is no longer open.

A lease fails with `.forbidden`, code `lease_not_allowed`, when the app is proxy only or the service is shared to you by another Acnologia server. Call those services through the proxy.

**`LeaseOptions`**: `excludeKeyIDs`, `ttl` (seconds; server default 120 on services with a concurrency limit, 300 otherwise), `wait` (0 to 60 seconds, for a free slot when every usable key is busy) and `exclusive` (no other caller gets the key until you report).

**`Lease` fields**: `leaseID`, `keyID`, `service`, `secret`, `auth` (`type`, `name`, `prefix`), `baseURL`, `expiresAt` and `exclusive`.

**Placing the key**: `apply(to:)` puts the secret where `auth.type` says:

| `auth.type` | Where the secret goes |
|---|---|
| `query` | query parameter `auth.name` |
| `header` | header `auth.name` |
| `bearer` | header `auth.name` (default `Authorization`) as `Bearer <secret>` |

A non-empty `auth.prefix` goes in front of the secret for every type and replaces `Bearer ` for bearer. `applied(to:)` returns a copy instead of changing the request, and `url(_:query:)` joins the base URL and path without the secret.

**Outcomes**: `.ok`, `.rateLimited(retryAfter:)`, `.quotaExhausted`, `.invalid`, `.error`, `.busy` and `.banned`.

## List services and read their guides

```swift
for s in try await acn.services() {
    print(s.slug, s.available, s.auth.type, s.guideURL ?? "")
}

let guide = try await acn.guide("serpapi")          // Guide: markdown, generated, guide (JSONValue)
let md = try await acn.guideMarkdown("serpapi")     // the same guide as Markdown text
```

## See your own access

`me()` returns the token's app, limits, use, services and, for a share's token, the share. A share whose owner paused, revoked or let it expire still answers here, with `share.status` saying so.

```swift
let me = try await acn.me()
print(me.app.name, me.usage.todayRequests, me.limits.dailyRequests ?? 0)
if let hit = me.limitHit { print("limit \(hit.limit) resets at \(hit.resetAt!)") }
if let share = me.share { print(share.ownerName, share.status) }
```

## Capabilities

A capability is one job (such as finding places) that several providers can do. Acnologia picks a provider, translates the params and moves on when one fails.

```swift
for c in try await acn.capabilities() {
    print(c.slug, c.routes.map(\.service))
}

let resp = try await acn.capability("places", params: ["query": "coffee in Lisbon", "limit": 5])
print(resp.service ?? "", resp.tried)     // who answered, and who was tried first

// Pin one provider, or get its own body back instead of the mapped shape:
_ = try await acn.capability("places", params: ["query": "tea"],
                             options: CapabilityOptions(service: "serpapi", raw: true))
```

`params` is `[String: JSONValue]` (literals work) or any `Encodable` value. The answer is a `ProxyResponse`, returned whatever its status; a missing param comes back with `acnologiaError` code `bad_params`.

## Forward proxy

An app can send its own HTTP calls through Acnologia as an `HTTPS_PROXY` and call provider URLs as they are. The SDK hands you the settings:

```swift
let setup = try await acn.forwardProxy()        // proxy_url, CA, hosts, env (no token inside)
try await acn.forwardProxyCA(saveTo: "acnologia-ca.pem")   // or forwardProxyCA() for the PEM text
let env = try await acn.forwardProxyEnv(caPath: "acnologia-ca.pem")

// Run a child process (any language) through the proxy:
let p = Process()
p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
p.arguments = ["node", "--use-env-proxy", "app.js"]
p.environment = ProcessInfo.processInfo.environment.merging(env) { _, new in new }
try p.run()
```

`forwardProxyEnv` returns `HTTPS_PROXY` and `HTTP_PROXY` (from `forwardProxyURL()`, `http://app:acn_...@host:8888`), `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to `caPath`. `forwardProxyURL()` and `forwardProxyEnv()` contain the app token, so do not log them. `ForwardProxy` itself never does. `forwardProxyCA()` sends no token: the CA is public.

`URLSession` has no portable way to take a proxy with credentials and an extra CA root without a delegate, so the SDK does not build a proxied session for you. In Swift, `acn.proxy(service)` is the simpler route.

## Handle errors

Everything is an `AcnologiaError`:

| Case | When |
|---|---|
| `.noKeyAvailable(retryAfter:message:)` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `.allKeysBusy(retryAfter:message:)` | 503 `all_keys_busy`: every usable key is at its concurrency limit. Retry after `retryAfter` (usually 1s) or lease with `wait` |
| `.serviceUnavailable(retryAfter:message:)` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `.appLimitReached(limit:resetAt:retryAfter:message:)` | 429 `app_limit_reached`: the app used up a limit. `limit` is `daily_requests`, `monthly_requests`, `daily_budget` or `monthly_budget` |
| `.shareUnavailable(status:message:)` | 403 `share_revoked`, `share_paused`, `share_expired`: the share behind the token stopped. `status` is `revoked`, `paused` or `expired` |
| `.unauthenticated(message:)` | 401: token missing, wrong, revoked or expired |
| `.forbidden(code:message:)` | 403: app disabled, or `service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed` |
| `.api(statusCode:code:message:)` | any other non-2xx, for example 404 `lease_not_found`, 400 `bad_params`, 503 `empty_group`, `no_route`, `no_route_worked`, 508 `loop_detected` |
| `.rateLimited(retryAfter:statusCode:message:)` | you throw it from `withKey` |
| `.quotaExhausted(statusCode:message:)` | you throw it from `withKey` |
| `.invalidKey(statusCode:message:)` | you throw it from `withKey` |
| `.invalidArgument(_)`, `.decoding(_)` | bad input, or a body the SDK could not decode |

Each error also has `retryAfter`, `statusCode`, `code`, `message`, `isNoKeyAvailable` (true for both no key cases) and `rotatesKey`.

```swift
do {
    _ = try await acn.lease("weather")
} catch let e as AcnologiaError where e.isNoKeyAvailable {
    try await Task.sleep(nanoseconds: UInt64((e.retryAfter ?? 1) * 1_000_000_000))
}
```

## Keep secrets out of logs

The secret, the app token and the access password never appear in `description`, `debugDescription`, `dump` or a `Mirror`. Read `lease.secret` only where you need it, and do not log it.

## Run the tests

The tests replay the server's real response shapes through a `URLProtocol` stub.

```sh
cd sdk/swift && swift test     # or `swift test` at the repository root
```

## More

- [SDK overview](../README.md): every language, install commands, test commands
- [Consumer API reference](../../docs/api.md)
- [Acnologia README](../../README.md)
