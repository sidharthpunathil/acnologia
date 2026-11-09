# Acnologia for Kotlin

Call the APIs your Acnologia server holds keys for, from Kotlin, with your app's own `acn_` token. Send requests through the proxy and the key never reaches your process. Or lease a key, call the provider yourself, and let `withKey` report each attempt and move to the next key on a rate limit, an empty quota or a rejected key.

Every network call is a `suspend` function, built on OkHttp and kotlinx.serialization. This is its own artifact and does not wrap the Java SDK.

## Quick start

```kotlin
import dev.acnologia.kotlin.*

suspend fun main() {
    val acn = Acnologia("http://localhost:8080", "acn_your_app_token")
    acn.proxy("weather").get("/v1/forecast", mapOf("city" to "Lisbon")).use { res ->
        println(res.body!!.string())
    }
}
```

`weather` is the slug of a service on your server. Acnologia attaches the key, sends the call on, and returns the provider's response.

## Install

The package is not on Maven Central yet. Publish it to your local Maven repository from this folder (Gradle 8 and JDK 17 or later):

```sh
cd sdk/kotlin
gradle publishToMavenLocal
```

Then in your build:

```kotlin
repositories { mavenLocal(); mavenCentral() }
dependencies { implementation("dev.acnologia:acnologia-kotlin:0.2.0") }
```

It brings in OkHttp 4.12, kotlinx-coroutines 1.9 and kotlinx-serialization-json 1.7.

## Connect to your server

Create an app on the Apps page of the dashboard and copy its token. Tokens with the older `cog_` prefix still work.

`Acnologia.fromEnv()` reads the URL and token from the environment and throws `IllegalArgumentException` when one is missing:

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | Server URL, such as `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | App token, `acn_...` |
| `ACNOLOGIA_ACCESS_PASSWORD` | Optional: the server's access password |

The constructor takes the rest as named arguments:

```kotlin
val acn = Acnologia("http://localhost:8080", "acn_...", httpClient = myOkHttp, userAgent = "my-app/1.0")
```

| Argument | Default | Notes |
|---|---|---|
| `httpClient` | an OkHttp client with a 75 second call and read timeout | That is longer than the 60 second maximum lease wait. If you pass your own, keep its timeouts longer than any wait. `fromEnv(httpClient)` takes one too. |
| `userAgent` | `acnologia-kotlin/0.2.0` | |
| `accessPassword` | none, or `ACNOLOGIA_ACCESS_PASSWORD` with `fromEnv` | Sent as `X-Acnologia-Access` on every request, for a server behind the access password. |

`Call.await()` is exported so you can run your own OkHttp calls without blocking a thread.

## Call an API through the proxy

`acn.proxy(service)` returns a `Proxy` whose requests go to `/proxy/{service}/{path}`. The key stays on the server. Each call returns the OkHttp `Response`; close it.

```kotlin
@Serializable data class Alert(val city: String)

acn.proxy("weather").postJson("/v1/alerts", Alert("Lisbon")).use { res ->
    println("${res.code} via key ${res.header("X-Acnologia-Key-Id")}")
}
```

- The upstream response comes back unchanged. A non-2xx status is not an exception.
- `res.acnologia` reads Acnologia's answer headers: `keyId` (null for a share's token), `attempts`, `service`, `cache` (`hit`, `miss`, ...), `replayed`, `capability` and `tried` (a list of `TriedRoute(service, status)`).
- Redirects are never followed, so the app token is not replayed to another host.
- With the default client, proxy calls drop the call timeout because bodies may stream.

Other methods:

- `get(path, query, headers)`, `delete(path, query, headers)`, `url(path, query)`
- `post(path, requestBody)`, `postJson(path, jsonElement)`, and `postJson(path, value)` for any value kotlinx.serialization can encode (a `@Serializable` class needs the serialization plugin in your build)
- `request(method, path, query, headers, body)`
- `send(request)`: uses only the path and query, so a request built against the provider's URL works

Every one takes `options = ProxyOptions(idempotencyKey = ..., noCache = ...)`. `idempotencyKey` sends `Idempotency-Key`, so a retry with the same key replays the first answer instead of calling the provider again. `noCache` sends `Cache-Control: no-cache` to skip Acnologia's cache.

```kotlin
acn.proxy("serpapi").get("/search.json", mapOf("q" to "coffee"), options = ProxyOptions(noCache = true)).use { res ->
    println("key ${res.acnologia.keyId}, cache ${res.acnologia.cache}")
}
```

To turn an Acnologia error from the proxy (a response without `X-Acnologia-Key-Id`) into an exception, call `ApiException.fromResponse(code, bodyText)`.

## Lease a key with `withKey`

`withKey(service, maxAttempts = 5, options = LeaseOptions()) { lease -> ... }` leases a key, runs the suspend block, reports the outcome, and rotates when the block throws one of the rotation exceptions:

| The block | Reported outcome | Rotates |
|---|---|---|
| returns a value | `ok` | no, the value is returned |
| throws `RateLimitedException(retryAfter)` | `rate_limited` | yes |
| throws `QuotaExhaustedException()` | `quota_exhausted` | yes |
| throws `InvalidKeyException()` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown |

```kotlin
import kotlin.time.Duration.Companion.hours
import kotlin.time.Duration.Companion.seconds

val http = OkHttpClient()

val json: String = acn.withKey("weather", maxAttempts = 3, options = LeaseOptions(wait = 10.seconds)) { lease ->
    http.newCall(lease.request("/v1/forecast", mapOf("city" to "Lisbon"))).await().use { res -> // request adds the key
        if (res.code == 429) throw RateLimitedException(1.hours, 429)
        if (res.code == 402) throw QuotaExhaustedException(402)
        if (res.code == 401) throw InvalidKeyException(401)
        res.body!!.string()
    }
}
```

How it ends:

- A rotation exception is also recognized as the cause of another exception.
- After `maxAttempts` keys the last rotation exception is thrown. The final attempt is reported without asking for a next key.
- When the pool runs dry, `NoKeyAvailableException` is thrown (`AllKeysBusyException` when every usable key is at its concurrency limit).
- Every lease is reported, including when the coroutine is cancelled while the block runs (the report is sent in `NonCancellable`). The secret is scrubbed from report messages.
- `options` sets the first lease. Its `ttl`, `wait` and `exclusive` apply to every later lease.

## Lease and report by hand

```kotlin
val lease = acn.lease("weather", LeaseOptions(ttl = 5.minutes, wait = 10.seconds))
// ... call the provider with lease.apply(request) ...
val next: Lease? = lease.report(Outcome.RATE_LIMITED, statusCode = 429, retryAfter = 1.hours, rotate = true)
```

Report on every path. On services with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out. With `rotate = true` the report returns the next lease when another key is available. `lease.report(Report(...))` takes the same fields as a data class.

`lease.renew(10.minutes)` extends an open lease for a long job and returns the new expiry (no argument uses the server default of two minutes). It throws `ApiException` 404 `lease_not_found` when the lease is no longer open.

A lease fails with `ForbiddenException`, code `lease_not_allowed`, when the app is proxy only or the service is shared to you by another Acnologia server. Call those services through the proxy.

**`LeaseOptions`**: `excludeKeyIds`, `ttl`, `wait` (at most 60 seconds, for a free slot when every key is busy) and `exclusive` (keep the key from every other caller until you report).

**`Lease` fields**: `leaseId`, `keyId`, `service`, `secret`, `auth` (`type`, `name`, `prefix`), `baseUrl`, `expiresAt` and `exclusive`.

**Placing the key**: `lease.apply(request)` returns a copy of an OkHttp `Request` with the secret where `auth.type` says:

| `auth.type` | Where the secret goes |
|---|---|
| `query` | query parameter `auth.name` |
| `header` | header `auth.name` |
| `bearer` | header `auth.name` (default `Authorization`) as `Bearer <secret>` |

A non-empty `auth.prefix` goes in front of the secret, and for `bearer` it replaces `Bearer `. `lease.request(path, query)` builds a GET with the key applied, `lease.url(path, query)` joins the provider base URL without the secret, and `lease.apply(headers, query)` fills two mutable maps for other clients.

**Outcomes**: `OK`, `RATE_LIMITED`, `QUOTA_EXHAUSTED`, `INVALID`, `ERROR`, `BUSY` and `BANNED`.

## List services and read guides

```kotlin
for (s in acn.services()) println("${s.slug} available=${s.available} auth=${s.auth.type} guide=${s.guideUrl}")

val guide = acn.guide("serpapi")          // Guide(markdown, generated, guide as a JsonElement)
val md: String = acn.guideMarkdown("serpapi")
```

## Check your own access

`me()` returns the app, its limits (`dailyRequests`, `monthlyRequests`, `dailyBudget`, `monthlyBudget`, each null when unset), usage, `limitHit`, the services it may call (with `logoUrl`) and, for a share's token, `share` with its `status`. A share that stopped still answers here.

```kotlin
val me = acn.me()
println("${me.app.name}: ${me.usage.todayRequests} requests today, limit hit: ${me.limitHit?.limit}")
```

## Call a capability

A capability is one call that any of several services can serve. `capabilities()` lists them with their `params` and `routes`. `capability(slug, params, service, raw, options)` POSTs the params to `/proxy/{slug}`; `service` picks one route and `raw = true` skips the output mapping.

```kotlin
val params = buildJsonObject { put("query", "bakery in Lisbon") }
acn.capability("maps-search", params).use { res ->
    println("${res.acnologia.service} answered after ${res.acnologia.tried}")
    println(res.body!!.string())
}
```

## Use the forward proxy

Your app can keep calling provider URLs as they are, with Acnologia as its HTTPS proxy. Save the CA, then hand the environment to the process that makes the calls:

```kotlin
acn.saveForwardProxyCA()                  // writes acnologia-ca.pem
val env = acn.forwardProxyEnv()           // HTTPS_PROXY, HTTP_PROXY, NODE_USE_ENV_PROXY, NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE, SSL_CERT_FILE
ProcessBuilder("node", "app.js").apply { environment().putAll(env) }.start()
```

For OkHttp in this process, `forwardProxyClient()` returns a client set up with the proxy, the token as `Proxy-Authorization` and trust in the system roots plus the Acnologia CA:

```kotlin
val http = acn.forwardProxyClient()
http.newCall(Request.Builder().url("https://serpapi.com/search.json?q=coffee").build()).await().use { res ->
    println(res.body!!.string())          // Acnologia injected the key
}
```

- `forwardProxy()` returns the setup: `proxyUrl`, `proxyUrlWithToken` (with a `<app token>` placeholder), `caUrl`, `caFingerprint`, `passthrough`, `hosts`, `env` and `serviceHeader`. It holds no token, so it is safe to log.
- `forwardProxyUrl()` is `proxyUrl` with user `app` and your token as the password. It and `forwardProxyEnv(caPath)` contain the token: do not log them.
- `forwardProxyCA()` returns the PEM. It is public and sent without the token.

## Handle errors

Every exception extends `AcnologiaException`. Network failures are a plain `AcnologiaException` with the cause attached.

| Exception | When |
|---|---|
| `ApiException` (`statusCode`, `code`, `errorMessage`) | any other non-2xx from the consumer API, such as 404 `lease_not_found` |
| `UnauthenticatedException` | 401: the token is missing or wrong |
| `ForbiddenException` | 403: the app is disabled or not allowed this service or mode |
| `ShareUnavailableException` (`share`, extends `ForbiddenException`) | 403 `share_revoked`, `share_paused`, `share_expired`: `share` is `revoked`, `paused` or `expired` |
| `AppLimitReachedException` (`limit`, `resetAt`, `retryAfter`) | 429 `app_limit_reached`: the app used up a daily or monthly request or budget limit |
| `NoKeyAvailableException` (`retryAfter`, `isBusy`) | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusyException` (extends `NoKeyAvailableException`) | 503 `all_keys_busy`: every usable key is at its concurrency limit |
| `ServiceUnavailableException` (`retryAfter`) | 503 `service_unavailable`: the provider's circuit breaker is open |
| `RateLimitedException` (`retryAfter`) | you throw it from `withKey` |
| `QuotaExhaustedException` | you throw it from `withKey` |
| `InvalidKeyException` | you throw it from `withKey` |

Other codes arrive as a plain `ApiException` with that `code`, among them `empty_group`, `bad_params`, `no_route`, `no_route_worked`, `proxy_not_allowed`, `lease_not_allowed` and `loop_detected`. `ApiException.fromResponse(code, body, headers)` also reads `Retry-After` and `X-Acnologia-Share`.

The API exceptions extend `ApiException`. The three rotation exceptions extend the sealed `KeyRotationException` and take an optional status code, message and cause.

```kotlin
try {
    acn.lease("weather")
} catch (e: NoKeyAvailableException) {
    delay(if (e.retryAfter.isPositive()) e.retryAfter else 1.seconds)
}
```

## Keep secrets out of logs

`Lease.toString()`, `Acnologia.toString()` and `ForwardProxy.toString()` never include the secret or the token, and the SDK logs nothing. `forwardProxyUrl()` and `forwardProxyEnv()` return the token on purpose.

## Run the tests

The tests use OkHttp's MockWebServer. From `sdk/kotlin`:

```sh
gradle test
# or, without Gradle installed:
docker run --rm -v "$PWD":/w -w /w gradle:8-jdk17 gradle test --no-daemon
```

## More

- [SDK overview](../README.md): every language, install commands, test commands
- [Consumer API reference](../../docs/api.md)
- [Acnologia README](../../README.md)
