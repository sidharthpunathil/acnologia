# Acnologia for Java

Call the APIs your Acnologia server holds keys for, from Java, with your app's own `acn_` token. Send requests through the proxy and the key never reaches your process. Or lease a key, call the provider yourself, and let `withKey` report each attempt and move to the next key on a rate limit, an empty quota or a rejected key.

Java 17 or later, built on `java.net.http.HttpClient` with Jackson as its one dependency. The network calls have `Async` forms that return a `CompletableFuture`.

## Quick start

```java
import dev.acnologia.*;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.util.Map;

Acnologia acn = Acnologia.create("http://localhost:8080", "acn_your_app_token");
HttpResponse<byte[]> res = acn.proxy("weather").get("/v1/forecast", Map.of("city", "Lisbon"));
System.out.println(new String(res.body(), StandardCharsets.UTF_8));
```

`weather` is the slug of a service on your server. Acnologia attaches the key, sends the call on, and returns the provider's response.

## Install

The package is not on Maven Central yet. Install it into your local Maven repository from this folder:

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

With Gradle, add `mavenLocal()` to your repositories and `implementation("dev.acnologia:acnologia:0.1.0")`.

## Connect to your server

Create an app on the Apps page of the dashboard and copy its token. Tokens with the older `cog_` prefix still work.

`Acnologia.fromEnv()` reads the URL and token from the environment:

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | Server URL, such as `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | App token, `acn_...` |

For more options, use the builder. It falls back to the environment variables for any value you leave unset.

```java
Acnologia acn = Acnologia.builder()
    .baseUrl("http://localhost:8080").token("acn_...")
    .httpClient(myClient).userAgent("my-app/1.0").timeout(Duration.ofSeconds(90))
    .build();
```

| Builder method | Default | Notes |
|---|---|---|
| `timeout` | 75 seconds (`Acnologia.DEFAULT_TIMEOUT`) | Bounds API calls; zero disables it. Keep it longer than any lease wait (at most 60 seconds). |
| `userAgent` | `acnologia-java/0.1.0` | |
| `httpClient` | a client the SDK builds | The proxy uses your client only when it does not follow redirects. Otherwise it uses its own client that never follows them. |

`build()` throws `IllegalArgumentException` when no URL or token is set or found.

## Call an API through the proxy

`acn.proxy(service)` returns a `Proxy` whose requests go to `/proxy/{service}/{path}`. The key stays on the server.

```java
Proxy weather = acn.proxy("weather");
HttpResponse<byte[]> forecast = weather.get("/v1/forecast", Map.of("city", "Lisbon"));
HttpResponse<byte[]> alert = weather.postJson("/v1/alerts", Map.of("city", "Lisbon"));
```

- The upstream response comes back unchanged. A non-2xx status is not an exception.
- Each response carries `X-Acnologia-Key-Id`, `X-Acnologia-Attempts` and `X-Acnologia-Service`.
- Redirects are never followed, so the app token is not replayed to another host.
- Proxy calls have no default timeout because bodies may stream. Set one on the request when you need it.

Other methods:

- `get(path)`, `post(path, contentType, bytes)`, `url(path[, query])`
- `request(method, path, query, headers, bodyPublisher[, bodyHandler])`
- `send(HttpRequest, bodyHandler)`: only the path and query of the request are used, so a request built against the provider's URL works
- Async forms: `getAsync`, `postJsonAsync`, `requestAsync` and `sendAsync`

To turn an Acnologia error from the proxy (a response without `X-Acnologia-Key-Id`) into an exception, call `ApiException.fromResponse(status, body)`.

## Lease a key with `withKey`

`withKey` leases a key, runs your function, reports the outcome, and rotates when the function throws one of the rotation exceptions:

| The function | Reported outcome | Rotates |
|---|---|---|
| returns a value | `ok` | no, the value is returned |
| throws `RateLimitedException(retryAfter)` | `rate_limited` | yes |
| throws `QuotaExhaustedException` | `quota_exhausted` | yes |
| throws `InvalidKeyException` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown unchanged |

```java
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.time.Duration;

HttpClient http = HttpClient.newHttpClient();

String json = acn.withKey("weather", lease -> {
    HttpRequest req = lease.apply(HttpRequest.newBuilder(
        URI.create(lease.url("/v1/forecast", Map.of("city", "Lisbon")))).build()); // apply adds the key
    HttpResponse<String> res = http.send(req, HttpResponse.BodyHandlers.ofString());
    if (res.statusCode() == 429) throw new RateLimitedException(Duration.ofHours(1), 429, null);
    if (res.statusCode() == 402) throw new QuotaExhaustedException(402, null);
    if (res.statusCode() == 401) throw new InvalidKeyException(401, null);
    return res.body();
});
```

The function may throw a checked exception (here `IOException` and `InterruptedException` from `http.send`), which `withKey` declares and rethrows.

How it ends:

- A rotation exception is also recognized as the cause of another exception.
- After `maxAttempts` keys (default 5) the last rotation exception is thrown. The final attempt is reported without asking for a next key.
- When the pool runs dry, `NoKeyAvailableException` is thrown (`AllKeysBusyException` when every usable key is at its concurrency limit).
- Every lease is reported, even when the function throws an `Error`, and the secret is scrubbed from report messages.

Pass `WithKeyOptions` to change the attempts or the lease:

```java
String out = acn.withKey("weather",
    WithKeyOptions.builder().maxAttempts(3).waitFor(Duration.ofSeconds(20)).build(),
    lease -> callProvider(lease));
```

`WithKeyOptions.builder()` takes `maxAttempts`, `waitFor`, `ttl` and `leaseOptions`.

The async form takes a function that returns a `CompletionStage`. A stage that fails with a rotation exception rotates the same way:

```java
CompletableFuture<String> f = acn.withKeyAsync("weather", lease ->
    http.sendAsync(lease.apply(request), HttpResponse.BodyHandlers.ofString()).thenApply(res -> {
        if (res.statusCode() == 429) throw new RateLimitedException(Duration.ofMinutes(1));
        return res.body();
    }));
```

## Lease and report by hand

```java
Lease lease = acn.lease("weather", LeaseOptions.builder()
    .ttl(Duration.ofMinutes(5)).waitFor(Duration.ofSeconds(10)).build());
// ... call the provider with lease.apply(request) ...
Optional<Lease> next = lease.report(Report.builder(Outcome.RATE_LIMITED)
    .statusCode(429).retryAfter(Duration.ofHours(1)).rotate(true).build());
```

Report on every path. On services with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out. With `rotate(true)` the report returns the next lease when another key is available. Shortcuts: `Report.ok()`, `Report.rateLimited(retryAfter)`, `Report.quotaExhausted()`, `Report.invalid()` and `Report.error(message)`.

`lease.renew(Duration.ofMinutes(10))` extends an open lease for a long job and returns the new expiry (`Duration.ZERO` uses the server default of two minutes). It throws `ApiException` 404 `lease_not_found` when the lease is no longer open. `reportAsync` and `renewAsync` are the async forms.

A lease fails with `ForbiddenException`, code `lease_not_allowed`, when the app is proxy only or the service is shared to you by another Acnologia server. Call those services through the proxy.

**`LeaseOptions`**: `excludeKeyIds`, `ttl`, `waitFor` (at most 60 seconds, for a free slot when every key is busy) and `exclusive` (keep the key from every other caller until you report).

**`Lease` fields**: `leaseId()`, `keyId()`, `service()`, `secret()`, `auth()` (`type`, `name`, `prefix`), `baseUrl()`, `expiresAt()` and `exclusive()`.

**Placing the key**: `lease.apply(HttpRequest)` returns a copy of the request with the secret where `auth.type` says:

| `auth.type` | Where the secret goes |
|---|---|
| `query` | query parameter `auth.name` |
| `header` | header `auth.name` |
| `bearer` | header `auth.name` (default `Authorization`) as `Bearer <secret>` |

A non-empty `auth.prefix` goes in front of the secret, and for `bearer` it replaces `Bearer `. For other HTTP clients, `lease.apply(headers, query)` fills two mutable maps instead. `lease.url(path, query)` joins the provider base URL without the secret.

**Outcomes**: `OK`, `RATE_LIMITED`, `QUOTA_EXHAUSTED`, `INVALID`, `ERROR`, `BUSY` and `BANNED`.

## List services

```java
for (Service s : acn.services()) {
    System.out.println(s.slug() + " available=" + s.available() + " auth=" + s.auth().type());
}
```

## Handle errors

Every exception extends the unchecked `AcnologiaException`. Network failures are a plain `AcnologiaException` with the cause attached.

| Exception | When |
|---|---|
| `ApiException` (`statusCode()`, `code()`, `errorMessage()`) | any other non-2xx from the consumer API, such as 404 `lease_not_found` |
| `UnauthenticatedException` | 401: the token is missing or wrong |
| `ForbiddenException` | 403: the app is disabled or not allowed this service or mode |
| `NoKeyAvailableException` (`retryAfter()`, `isBusy()`) | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusyException` (extends `NoKeyAvailableException`) | 503 `all_keys_busy`: every usable key is at its concurrency limit |
| `ServiceUnavailableException` (`retryAfter()`) | 503 `service_unavailable`: the provider's circuit breaker is open |
| `RateLimitedException` (`retryAfter()`) | you throw it from `withKey` |
| `QuotaExhaustedException` | you throw it from `withKey` |
| `InvalidKeyException` | you throw it from `withKey` |

The API exceptions extend `ApiException`. The three rotation exceptions extend `KeyRotationException` and take an optional status code, message and cause.

```java
try {
    acn.lease("weather");
} catch (NoKeyAvailableException e) {
    Thread.sleep(e.retryAfter().isZero() ? 1000 : e.retryAfter().toMillis());
}
```

## Keep secrets out of logs

`Lease.toString()` and `Acnologia.toString()` never include the secret or the token, and the SDK logs nothing.

## Run the tests

The tests run against a local mock server built on the JDK's `com.sun.net.httpserver`. From `sdk/java`:

```sh
mvn test
# or, without Maven installed:
docker run --rm -v "$PWD":/w -w /w maven:3-eclipse-temurin-17 mvn -q test
```

## More

- [SDK overview](../README.md): every language, install commands, test commands
- [Consumer API reference](../../docs/api.md)
- [Acnologia README](../../README.md)
