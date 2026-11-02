# Acnologia for Swift

Swift client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. Foundation only,
async/await on `URLSession`, `Sendable` types.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `withKey` runs that loop and rotates keys for you.

Requires Swift 5.9+. Runs on macOS 13+, iOS 16+, tvOS 16+, watchOS 9+ and Linux (through
`FoundationNetworking`).

## Install

The repository root has a `Package.swift` that exposes this SDK, so depend on the repository URL
directly:

```swift
// Package.swift
dependencies: [
    .package(url: "https://github.com/sidharthpunathil/acnologia", branch: "main"),
],
targets: [
    .target(name: "MyApp", dependencies: [.product(name: "Acnologia", package: "acnologia")]),
]
```

In Xcode, use File > Add Package Dependencies, paste `https://github.com/sidharthpunathil/acnologia`,
pick the `main` branch, and add the `Acnologia` library to your target.

To work on the SDK itself, open `sdk/swift`: its own `Package.swift` builds the same sources.

## Quick start

```swift
import Acnologia

let acn = try Acnologia(baseURL: "http://localhost:8080", token: "acn_...")
// or: let acn = try Acnologia.fromEnvironment()

let lease = try await acn.lease("serpapi")
var req = URLRequest(url: try lease.url("/search.json", query: [URLQueryItem(name: "q", value: "coffee")]))
lease.apply(to: &req)                       // adds ?api_key=<secret>
let (data, resp) = try await URLSession.shared.data(for: req)
try await lease.report((resp as? HTTPURLResponse)?.statusCode == 429 ? .rateLimited(retryAfter: 3600) : .ok)
```

## Environment variables

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | The Acnologia server, for example `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | The app token, `acn_...` (older `cog_...` tokens still work) |

`Acnologia.fromEnvironment()` reads both and throws `AcnologiaError.invalidArgument` when one is
missing. Pass a dictionary to read from somewhere else.

Other initializer arguments: `configuration` (a `URLSessionConfiguration`), `userAgent` (default
`acnologia-swift/0.1.0`) and `timeout` for API calls (default 75 seconds, longer than the 60
second maximum lease wait). Proxy calls use the session's own timeouts.

## Lease mode

### withKey (recommended)

Throw one of the rotation errors from the body to rotate to the next key:

| Body | Reported outcome | Rotates |
|---|---|---|
| returns | `ok` | no |
| throws `AcnologiaError.rateLimited(retryAfter:)` | `rate_limited` | yes |
| throws `AcnologiaError.quotaExhausted()` | `quota_exhausted` | yes |
| throws `AcnologiaError.invalidKey()` | `invalid` | yes |
| throws anything else | `error` | no, the error is rethrown as is |

After `maxAttempts` keys (default 5) the last rotation error is thrown; the final attempt is
reported without asking for a next key. When the pool runs dry, `.noKeyAvailable` or
`.allKeysBusy` is thrown. Every lease is reported, also when the surrounding task is cancelled
(the report goes out from a detached task). Messages sent with a report have the secret
replaced by `[key]` and are cut to 500 characters.

```swift
let results: SearchResults = try await acn.withKey("serpapi", maxAttempts: 3) { lease in
    var req = URLRequest(url: try lease.url("/search.json", query: [
        URLQueryItem(name: "engine", value: "google"),
        URLQueryItem(name: "q", value: "coffee"),
    ]))
    lease.apply(to: &req)
    let (data, resp) = try await URLSession.shared.data(for: req)
    let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
    switch status {
    case 429 where String(decoding: data, as: UTF8.self).contains("run out of searches"):
        throw AcnologiaError.quotaExhausted(statusCode: 429)
    case 429:
        throw AcnologiaError.rateLimited(retryAfter: 3600, statusCode: 429)
    case 401:
        throw AcnologiaError.invalidKey(statusCode: 401)
    case 400...:
        throw URLError(.badServerResponse)
    default:
        return try JSONDecoder().decode(SearchResults.self, from: data)
    }
}
```

`SearchResults` is your own `Decodable` type.

`withKey` takes `options: LeaseOptions(...)`; its `ttl`, `wait` and `exclusive` apply to every
lease it takes, and `excludeKeyIDs` to the first.

### Lease, report and renew by hand

```swift
let lease = try await acn.lease("serpapi", options: LeaseOptions(ttl: 300, wait: 10))
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
let next = try await lease.report(.rateLimited(retryAfter: 3600), statusCode: 429, rotate: true)
if let next { /* retry with next */ }

let expires = try await lease.renew(ttl: 600)   // for long jobs
```

`LeaseOptions`: `excludeKeyIDs`, `ttl` (seconds; server default 120 on services with a
concurrency limit, 300 otherwise), `wait` (0 to 60 seconds to wait for a free slot when every
usable key is busy) and `exclusive` (no other caller gets the key until you report).

`Lease` fields: `leaseID`, `keyID`, `service`, `secret`, `auth` (`type`, `name`, `prefix`),
`baseURL`, `expiresAt`, `exclusive`. `apply(to:)` places the secret by `auth.type`: `query` sets
the query parameter `auth.name`, `header` sets header `auth.name`, `bearer` sets `auth.name`
(default `Authorization`) to `Bearer <secret>`. A non empty `auth.prefix` goes in front of the
secret for every type and replaces `Bearer ` for bearer. `url(_:query:)` joins the base URL and
path without the secret.

Outcomes: `.ok`, `.rateLimited(retryAfter:)`, `.quotaExhausted`, `.invalid`, `.error`, `.busy`,
`.banned`.

The secret and the app token never appear in `description`, `debugDescription`, `dump` or a
`Mirror`. Read `lease.secret` only where you need it, and do not log it.

## Proxy mode

`acn.proxy(service)` sends requests to `/proxy/{service}/{path}`. Upstream answers come back as
`ProxyResponse` whatever their status. Redirects are not followed, so the app token is never
sent to another host.

```swift
let resp = try await acn.proxy("serpapi").get("/search.json", query: [URLQueryItem(name: "q", value: "coffee")])
print(resp.statusCode, resp.keyID ?? 0, resp.attempts ?? 0)
let results = try resp.decode(SearchResults.self)

let audio = try await acn.proxy("elevenlabs").postJSON(
    "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
    ["text": "Hello from Acnologia", "model_id": "eleven_multilingual_v2"])
try audio.data.write(to: URL(fileURLWithPath: "hello.mp3"))
```

Other helpers: `request(method, path, query:headers:body:)`, `post`, `put`, `patch`, `delete`,
`url(path, query:)`, and `send(URLRequest)`, which accepts a relative URL such as
`/v1/voices?page=2`.

`ProxyResponse` has `statusCode`, `headers`, `data`, `text`, `header(_:)`, `decode(_:)`, and
Acnologia's headers as `keyID`, `attempts`, `servedBy` and `cache`. When Acnologia itself refused
the request (no key, circuit breaker open, bad token), `resp.acnologiaError` holds the typed
error and `try resp.throwingAcnologiaError()` throws it. Provider answers carry
`X-Acnologia-Key-Id` and are never treated as Acnologia errors.

## Services

```swift
for s in try await acn.services() {
    print(s.slug, s.available, s.auth.type)
}
```

## Errors

Everything is an `AcnologiaError`:

| Case | When |
|---|---|
| `.noKeyAvailable(retryAfter:message:)` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `.allKeysBusy(retryAfter:message:)` | 503 `all_keys_busy`: every usable key is at its concurrency limit; retry after `retryAfter` (usually 1s) or lease with `wait` |
| `.serviceUnavailable(retryAfter:message:)` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `.unauthenticated(message:)` | 401: token missing, wrong, revoked or expired |
| `.forbidden(code:message:)` | 403: app disabled, or `service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed` |
| `.api(statusCode:code:message:)` | any other non-2xx, for example 429 `app_limit_reached` or 404 `lease_not_found` |
| `.rateLimited(retryAfter:statusCode:message:)` | you throw it from `withKey` |
| `.quotaExhausted(statusCode:message:)` | you throw it from `withKey` |
| `.invalidKey(statusCode:message:)` | you throw it from `withKey` |
| `.invalidArgument(_)`, `.decoding(_)` | bad input, or a body the SDK could not decode |

Each error also has `retryAfter`, `statusCode`, `code`, `message`, `isNoKeyAvailable` (true for
both no key cases) and `rotatesKey`.

```swift
do {
    try await acn.withKey("elevenlabs", options: LeaseOptions(wait: 20)) { lease in try await speak(lease) }
} catch let e as AcnologiaError where e.isNoKeyAvailable {
    try await Task.sleep(nanoseconds: UInt64((e.retryAfter ?? 1) * 1_000_000_000))
}
```

## Tests

```sh
cd sdk/swift && swift test     # or `swift test` at the repository root
```

The tests replay the server's real response shapes through a `URLProtocol` stub.
