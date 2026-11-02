# acnologia-java

Java client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool.
Java 17+, built on `java.net.http.HttpClient` with Jackson as its one dependency.
Every blocking call has an `Async` twin that returns a `CompletableFuture`.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `withKey` runs that loop and rotates keys for you.

## Install

The package is not on Maven Central yet. Install it into your local Maven
repository from this folder:

```sh
cd sdk/java
mvn install
```

Then depend on it:

```xml
<dependency>
  <groupId>dev.acnologia</groupId>
  <artifactId>acnologia</artifactId>
  <version>0.1.0</version>
</dependency>
```

With Gradle, add `mavenLocal()` to your repositories and
`implementation("dev.acnologia:acnologia:0.1.0")`.

## Quick start

```java
import dev.acnologia.*;

Acnologia acn = Acnologia.fromEnv(); // ACNOLOGIA_URL and ACNOLOGIA_TOKEN
HttpClient http = HttpClient.newHttpClient();

String json = acn.withKey("serpapi", lease -> {
    HttpRequest req = lease.apply(HttpRequest.newBuilder(
        URI.create(lease.url("/search.json", Map.of("q", "coffee")))).build()); // adds ?api_key=<secret>
    HttpResponse<String> res = http.send(req, HttpResponse.BodyHandlers.ofString());
    if (res.statusCode() == 429) throw new RateLimitedException(Duration.ofHours(1), 429, null);
    if (res.statusCode() == 401) throw new InvalidKeyException(401, null);
    return res.body();
});
```

Or set the URL and token yourself:

```java
Acnologia acn = Acnologia.create("http://localhost:8080", "acn_...");
Acnologia acn = Acnologia.builder()
    .baseUrl("http://localhost:8080").token("acn_...")
    .httpClient(myClient).userAgent("my-app/1.0").timeout(Duration.ofSeconds(90))
    .build();
```

The default `User-Agent` is `acnologia-java/0.1.0`. API calls time out after 75
seconds, longer than the 60 second maximum lease wait. Tokens with the older
`cog_` prefix still work.

## Environment variables

| Variable | Used for |
|---|---|
| `ACNOLOGIA_URL` | the server, for example `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | the app token, `acn_...` |

`Acnologia.fromEnv()` reads both, and the builder falls back to them for any
value you leave unset.

## withKey

`withKey(service, fn)` leases a key, runs `fn`, reports the outcome, and on a
rotation exception reports it with `rotate` and runs `fn` again with the next
key.

| `fn` does | Reported outcome | Rotates |
|---|---|---|
| returns a value | `ok` | no, the value is returned |
| throws `RateLimitedException(retryAfter)` | `rate_limited` | yes |
| throws `QuotaExhaustedException` | `quota_exhausted` | yes |
| throws `InvalidKeyException` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown unchanged |

A rotation exception is also recognized as the cause of another exception.
After `maxAttempts` keys (default 5) the last rotation exception is thrown; the
final attempt is reported without asking for a next key. When the pool runs
dry, `NoKeyAvailableException` is thrown. Every lease is reported, even when
`fn` throws an `Error`, and the secret is scrubbed from report messages.

`fn` may throw a checked exception, which `withKey` declares and rethrows:

```java
String out = acn.withKey("elevenlabs",
    WithKeyOptions.builder().maxAttempts(3).waitFor(Duration.ofSeconds(20)).build(),
    lease -> callProvider(lease)); // callProvider throws IOException
```

The async form takes a function that returns a `CompletionStage`:

```java
CompletableFuture<String> f = acn.withKeyAsync("serpapi", lease ->
    http.sendAsync(lease.apply(request), BodyHandlers.ofString()).thenApply(res -> {
        if (res.statusCode() == 429) throw new RateLimitedException(Duration.ofMinutes(1));
        return res.body();
    }));
```

## Lease, report and renew by hand

```java
Lease lease = acn.lease("serpapi", LeaseOptions.builder()
    .ttl(Duration.ofMinutes(5)).waitFor(Duration.ofSeconds(10)).build());
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
Optional<Lease> next = lease.report(Report.builder(Outcome.RATE_LIMITED)
    .statusCode(429).retryAfter(Duration.ofHours(1)).rotate(true).build());
```

`LeaseOptions` takes `excludeKeyIds`, `ttl`, `waitFor` (at most 60 seconds) and
`exclusive`. Outcomes are `OK`, `RATE_LIMITED`, `QUOTA_EXHAUSTED`, `INVALID`,
`ERROR`, `BUSY` and `BANNED`. `lease.renew(Duration.ofMinutes(10))` extends an
open lease and returns the new expiry.

`lease.apply(HttpRequest)` returns a copy of the request with the secret where
the provider expects it: query parameter `auth.name` for `query`, header
`auth.name` for `header`, and `Authorization: Bearer <secret>` for `bearer`.
A non empty `auth.prefix` goes in front of the secret, and for `bearer` it
replaces `Bearer `. For other HTTP clients, `lease.apply(headers, query)` fills
two mutable maps instead. `lease.url(path, query)` joins the provider base URL
without the secret.

`Lease.toString()` and `Acnologia.toString()` never include the secret or the
token, and the SDK logs nothing.

## Proxy

```java
Proxy serp = acn.proxy("serpapi");
HttpResponse<byte[]> res = serp.get("/search.json", Map.of("engine", "google", "q", "coffee"));

HttpResponse<byte[]> audio = acn.proxy("elevenlabs").postJson(
    "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
    Map.of("text", "Hello from Acnologia", "model_id", "eleven_multilingual_v2"));
```

Requests go to `/proxy/{service}/{path}`. Upstream responses come back
unchanged, so a non-2xx status is not an exception. Each response carries
`X-Acnologia-Key-Id` and `X-Acnologia-Attempts`. Redirects are never followed,
and proxy calls have no default timeout because bodies may stream.

Other methods: `post(path, contentType, bytes)`,
`request(method, path, query, headers, bodyPublisher[, bodyHandler])`,
`send(HttpRequest, bodyHandler)` (only the path and query of the request are
used, so a request built against the provider's URL works), and the async forms
`getAsync`, `postJsonAsync`, `requestAsync` and `sendAsync`. To turn an
Acnologia error from the proxy (one without `X-Acnologia-Key-Id`) into an
exception, call `ApiException.fromResponse(status, body)`.

## Errors

Every exception extends the unchecked `AcnologiaException`. Network failures
are a plain `AcnologiaException` with the cause attached.

| Exception | When |
|---|---|
| `ApiException` (`statusCode()`, `code()`, `errorMessage()`) | any other non-2xx from the consumer API |
| `UnauthenticatedException` | 401, the token is missing or wrong |
| `ForbiddenException` | 403, the app is disabled or not allowed this service or mode |
| `NoKeyAvailableException` (`retryAfter()`, `isBusy()`) | 503 `no_key_available` |
| `AllKeysBusyException` (extends `NoKeyAvailableException`) | 503 `all_keys_busy`: every usable key is at its concurrency limit |
| `ServiceUnavailableException` (`retryAfter()`) | 503 `service_unavailable`: the provider's circuit breaker is open |
| `RateLimitedException` (`retryAfter()`) | you throw it from `withKey` |
| `QuotaExhaustedException` | you throw it from `withKey` |
| `InvalidKeyException` | you throw it from `withKey` |

The three rotation exceptions extend `KeyRotationException` and take an
optional status code, message and cause.

## Tests

The tests run against a local mock server built on the JDK's
`com.sun.net.httpserver`.

```sh
mvn test
# or, without Maven installed:
docker run --rm -v "$PWD":/w -w /w maven:3-eclipse-temurin-17 mvn -q test
```
