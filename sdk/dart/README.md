# Acnologia for Dart and Flutter

Dart client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. Built on
`package:http`, null safe, async throughout. Works in Dart on the server and command line, and
in Flutter on mobile, desktop and the web.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `withKey` runs that loop and rotates keys for you.

Requires Dart 3.0+.

## Install

Until the package is published on pub.dev, depend on it from git:

```yaml
dependencies:
  acnologia:
    git:
      url: https://github.com/sidharthpunathil/acnologia.git
      path: sdk/dart
      ref: main
```

Then `dart pub get` (or `flutter pub get`).

## Quick start

```dart
import 'package:acnologia/acnologia.dart';
import 'package:http/http.dart' as http;

final acn = Acnologia(baseUrl: 'http://localhost:8080', token: 'acn_...');
// or: final acn = Acnologia.fromEnvironment();

final lease = await acn.lease('serpapi');
final resp = await http.Response.fromStream(
    await http.Client().send(lease.request('GET', '/search.json', query: {'q': 'coffee'})));
await lease.report(resp.statusCode == 429 ? Outcome.rateLimited(const Duration(hours: 1)) : Outcome.ok);
```

## Environment variables

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | The Acnologia server, for example `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | The app token, `acn_...` (older `cog_...` tokens still work) |

`Acnologia.fromEnvironment()` reads the process environment, then values given at compile time
with `--dart-define=ACNOLOGIA_URL=...`, which is the only source in a Flutter web build. It throws
`ArgumentError` when either is missing. Pass `environment:` to read from a map instead.

Other constructor arguments: `httpClient` (any `http.Client`; the SDK creates one otherwise and
`close()` closes it), `userAgent` (default `acnologia-dart/0.1.0`, not sent in browsers, which
forbid it) and `timeout` for API calls (default 75 seconds, longer than the 60 second maximum
lease wait). Proxy calls have no SDK timeout.

## Lease mode

### withKey (recommended)

Throw one of the rotation exceptions from the callback to rotate to the next key:

| Callback | Reported outcome | Rotates |
|---|---|---|
| completes | `ok` | no |
| throws `RateLimitedException(retryAfter: ...)` | `rate_limited` | yes |
| throws `QuotaExhaustedException()` | `quota_exhausted` | yes |
| throws `InvalidKeyException()` | `invalid` | yes |
| throws anything else | `error` | no, the error is rethrown as is |

After `maxAttempts` keys (default 5) the last rotation exception is thrown; the final attempt is
reported without asking for a next key. When the pool runs dry, `NoKeyAvailableException` (or
`AllKeysBusyException`) is thrown. Every lease is reported. Messages sent with a report have the
secret replaced by `[key]` and are cut to 500 characters.

```dart
final results = await acn.withKey('serpapi', (lease) async {
  final req = lease.request('GET', '/search.json', query: {'engine': 'google', 'q': 'coffee'});
  final resp = await http.Response.fromStream(await client.send(req));
  if (resp.statusCode == 429 && resp.body.contains('run out of searches')) {
    throw const QuotaExhaustedException(statusCode: 429);
  }
  if (resp.statusCode == 429) {
    throw const RateLimitedException(retryAfter: Duration(hours: 1), statusCode: 429);
  }
  if (resp.statusCode == 401) throw const InvalidKeyException(statusCode: 401);
  if (resp.statusCode >= 400) throw StateError('serpapi: http ${resp.statusCode}');
  return jsonDecode(resp.body) as Map<String, dynamic>;
}, maxAttempts: 3);
```

`withKey` also takes `excludeKeyIds` (first lease only), and `ttl`, `wait` and `exclusive`, which
apply to every lease it takes.

### Lease, report and renew by hand

```dart
final lease = await acn.lease('serpapi', ttl: const Duration(minutes: 5), wait: const Duration(seconds: 10));
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
final next = await lease.report(Outcome.rateLimited(const Duration(hours: 1)), statusCode: 429, rotate: true);
if (next != null) { /* retry with next */ }

final expires = await lease.renew(ttl: const Duration(minutes: 10)); // for long jobs
```

`lease` options: `excludeKeyIds`, `ttl` (server default 120 seconds on services with a
concurrency limit, 300 otherwise), `wait` (0 to 60 seconds to wait for a free slot when every
usable key is busy) and `exclusive` (no other caller gets the key until you report).

`Lease` fields: `leaseId`, `keyId`, `service`, `secret`, `auth` (`type`, `name`, `prefix`),
`baseUrl`, `expiresAt`, `exclusive`. `apply(url, headers)` returns a record `(url:, headers:)`
with the secret placed by `auth.type`: `query` sets the query parameter `auth.name`, `header` sets
header `auth.name`, `bearer` sets `auth.name` (default `Authorization`) to `Bearer <secret>`. A
non empty `auth.prefix` goes in front of the secret for every type and replaces `Bearer ` for
bearer. `url(path, query)` joins the base URL and path without the secret, and
`request(method, path, query:, headers:)` builds an `http.Request` with the secret applied.

Outcomes: `Outcome.ok`, `Outcome.rateLimited([retryAfter])`, `Outcome.quotaExhausted`,
`Outcome.invalid`, `Outcome.error`, `Outcome.busy`, `Outcome.banned`.

The secret and the app token never appear in `toString()`. Read `lease.secret` only where you
need it, and do not log it.

## Proxy mode

`acn.proxy(service)` sends requests to `/proxy/{service}/{path}` and returns the upstream
`http.Response` whatever its status. Redirects are not followed (browsers follow them on their
own), so the app token is not sent to another host.

```dart
final resp = await acn.proxy('serpapi').get('/search.json', query: {'engine': 'google', 'q': 'coffee'});
print('${resp.statusCode} key ${resp.acnologiaKeyId} attempts ${resp.acnologiaAttempts}');

final audio = await acn.proxy('elevenlabs').post('/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM',
    body: {'text': 'Hello from Acnologia', 'model_id': 'eleven_multilingual_v2'});
```

Bodies: a `String` or `List<int>` is sent as is; anything else is JSON encoded with
`Content-Type: application/json` unless you set one. Other helpers: `request`, `put`, `patch`,
`delete`, `url(path, query)`, and `stream(...)`, which returns an `http.StreamedResponse` for
large or streamed bodies.

Extensions on the response: `acnologiaKeyId`, `acnologiaAttempts`, `acnologiaService` and
`acnologiaCache` read Acnologia's headers. When Acnologia itself refused the request (no key,
circuit breaker open, bad token), `resp.acnologiaError` holds the typed exception and
`resp.throwIfAcnologiaError()` throws it. Provider answers carry `X-Acnologia-Key-Id` and are
never treated as Acnologia errors.

## Services

```dart
for (final s in await acn.services()) {
  print('${s.slug} ${s.available} ${s.auth.type}');
}
```

## Errors

Every exception extends `AcnologiaException`, which has `message`, `statusCode`, `code` and
`retryAfter`.

| Exception | When |
|---|---|
| `NoKeyAvailableException` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusyException` (a `NoKeyAvailableException`, `busy` is true) | 503 `all_keys_busy`: every usable key is at its concurrency limit; retry after `retryAfter` (usually 1s) or lease with `wait` |
| `ServiceUnavailableException` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `UnauthenticatedException` | 401: token missing, wrong, revoked or expired |
| `ForbiddenException` | 403: app disabled, or `service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed` |
| `AcnologiaApiException` | any other non-2xx, for example 429 `app_limit_reached` or 404 `lease_not_found` |
| `RateLimitedException`, `QuotaExhaustedException`, `InvalidKeyException` | you throw them from `withKey` (all are `KeyRotationException`) |

Bad arguments throw `ArgumentError` or `RangeError`; an API call that runs past `timeout` throws
`TimeoutException`.

```dart
try {
  await acn.withKey('elevenlabs', speak, wait: const Duration(seconds: 20));
} on NoKeyAvailableException catch (e) {
  await Future<void>.delayed(e.retryAfter ?? const Duration(seconds: 1));
}
```

## Tests

```sh
dart pub get
dart analyze
dart test
```

Without a local Dart SDK:

```sh
docker run --rm -v "$PWD":/w -w /w dart:stable sh -c "dart pub get && dart analyze && dart test"
```

The tests replay the server's real response shapes through `MockClient` from
`package:http/testing`.
