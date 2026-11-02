# acnologia-kotlin

Kotlin client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool.
Coroutines first: every network call is a `suspend` function, built on OkHttp
and kotlinx.serialization. It is its own artifact and does not wrap the Java SDK.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `withKey` runs that loop and rotates keys for you.

## Install

The package is not on Maven Central yet. Publish it to your local Maven
repository from this folder (Gradle 8 and JDK 17+):

```sh
cd sdk/kotlin
gradle publishToMavenLocal
```

Then in your build:

```kotlin
repositories { mavenLocal(); mavenCentral() }
dependencies { implementation("dev.acnologia:acnologia-kotlin:0.1.0") }
```

It brings in OkHttp 4.12, kotlinx-coroutines 1.9 and kotlinx-serialization-json 1.7.

## Quick start

```kotlin
import dev.acnologia.kotlin.*

val acn = Acnologia.fromEnv() // ACNOLOGIA_URL and ACNOLOGIA_TOKEN
val http = OkHttpClient()

val json: String = acn.withKey("serpapi") { lease ->
    http.newCall(lease.request("/search.json", mapOf("q" to "coffee"))).await().use { res -> // adds ?api_key=<secret>
        if (res.code == 429) throw RateLimitedException(1.hours, 429)
        if (res.code == 401) throw InvalidKeyException(401)
        res.body!!.string()
    }
}
```

Or set the URL and token yourself:

```kotlin
val acn = Acnologia("http://localhost:8080", "acn_...", httpClient = myOkHttp, userAgent = "my-app/1.0")
```

The default `User-Agent` is `acnologia-kotlin/0.1.0`. The default OkHttp client
has a 75 second call and read timeout, longer than the 60 second maximum lease
wait; if you pass your own, keep its timeouts longer than any wait. Tokens with
the older `cog_` prefix still work. `Call.await()` is exported so you can run
your own OkHttp calls without blocking a thread.

## Environment variables

| Variable | Used for |
|---|---|
| `ACNOLOGIA_URL` | the server, for example `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | the app token, `acn_...` |

`Acnologia.fromEnv()` reads both and throws `IllegalArgumentException` when one
is missing.

## withKey

`withKey(service, maxAttempts = 5, options = LeaseOptions()) { lease -> ... }`
takes a suspend block. It leases a key, runs the block, reports the outcome,
and on a rotation exception reports it with `rotate` and runs the block again
with the next key.

| The block | Reported outcome | Rotates |
|---|---|---|
| returns a value | `ok` | no, the value is returned |
| throws `RateLimitedException(retryAfter)` | `rate_limited` | yes |
| throws `QuotaExhaustedException()` | `quota_exhausted` | yes |
| throws `InvalidKeyException()` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown |

A rotation exception is also recognized as the cause of another exception.
After `maxAttempts` keys the last rotation exception is thrown; the final
attempt is reported without asking for a next key. When the pool runs dry,
`NoKeyAvailableException` is thrown. Every lease is reported, including when
the coroutine is cancelled while the block runs (the report is sent in
`NonCancellable`), and the secret is scrubbed from report messages.

```kotlin
val audio = acn.withKey("elevenlabs", maxAttempts = 3, options = LeaseOptions(wait = 20.seconds)) { lease ->
    speak(lease)
}
```

## Lease, report and renew by hand

```kotlin
val lease = acn.lease("serpapi", LeaseOptions(ttl = 5.minutes, wait = 10.seconds))
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
val next: Lease? = lease.report(Outcome.RATE_LIMITED, statusCode = 429, retryAfter = 1.hours, rotate = true)
```

`LeaseOptions` takes `excludeKeyIds`, `ttl`, `wait` (at most 60 seconds) and
`exclusive`. Outcomes are `OK`, `RATE_LIMITED`, `QUOTA_EXHAUSTED`, `INVALID`,
`ERROR`, `BUSY` and `BANNED`; `lease.report(Report(...))` takes the same fields
as a data class. `lease.renew(10.minutes)` extends an open lease and returns
the new expiry.

`lease.apply(request)` returns a copy of an OkHttp `Request` with the secret
where the provider expects it: query parameter `auth.name` for `query`, header
`auth.name` for `header`, and `Authorization: Bearer <secret>` for `bearer`. A
non empty `auth.prefix` goes in front of the secret, and for `bearer` it
replaces `Bearer `. `lease.request(path, query)` builds a GET with the key
applied, `lease.url(path, query)` joins the provider base URL without the
secret, and `lease.apply(headers, query)` fills two mutable maps for other
clients.

`Lease.toString()` and `Acnologia.toString()` never include the secret or the
token, and the SDK logs nothing.

## Proxy

```kotlin
acn.proxy("serpapi").get("/search.json", mapOf("engine" to "google", "q" to "coffee")).use { res ->
    println(res.body!!.string())
}

@Serializable data class Tts(val text: String, val model_id: String)
acn.proxy("elevenlabs").postJson("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
    Tts("Hello from Acnologia", "eleven_multilingual_v2")).use { res -> File("hello.mp3").writeBytes(res.body!!.bytes()) }
```

Requests go to `/proxy/{service}/{path}` and return the OkHttp `Response`;
close it. Upstream responses come back unchanged, so a non-2xx status is not an
exception. Each response carries `X-Acnologia-Key-Id` and
`X-Acnologia-Attempts`. Redirects are never followed, and the default client's
call timeout is dropped for proxy calls because bodies may stream.

Other methods: `delete`, `post(path, requestBody)`, `postJson(path, jsonElement)`,
`request(method, path, query, headers, body)`, and `send(request)`, which uses
only the path and query of a request built against the provider's URL. To turn
an Acnologia error from the proxy (one without `X-Acnologia-Key-Id`) into an
exception, call `ApiException.fromResponse(code, bodyText)`.

## Errors

Every exception extends `AcnologiaException`. Network failures are a plain
`AcnologiaException` with the cause attached.

| Exception | When |
|---|---|
| `ApiException` (`statusCode`, `code`, `errorMessage`) | any other non-2xx from the consumer API |
| `UnauthenticatedException` | 401, the token is missing or wrong |
| `ForbiddenException` | 403, the app is disabled or not allowed this service or mode |
| `NoKeyAvailableException` (`retryAfter`, `isBusy`) | 503 `no_key_available` |
| `AllKeysBusyException` (extends `NoKeyAvailableException`) | 503 `all_keys_busy`: every usable key is at its concurrency limit |
| `ServiceUnavailableException` (`retryAfter`) | 503 `service_unavailable`: the provider's circuit breaker is open |
| `RateLimitedException` (`retryAfter`) | you throw it from `withKey` |
| `QuotaExhaustedException` | you throw it from `withKey` |
| `InvalidKeyException` | you throw it from `withKey` |

The three rotation exceptions extend the sealed `KeyRotationException` and take
an optional status code, message and cause.

## Tests

The tests use OkHttp's MockWebServer.

```sh
gradle test
# or, without Gradle installed:
docker run --rm -v "$PWD":/w -w /w gradle:8-jdk17 gradle test --no-daemon
```
