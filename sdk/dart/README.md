# Acnologia for Dart and Flutter

Call APIs with the keys your Acnologia server holds, using your app's own `acn_` token. In proxy mode the server attaches the key, moves to the next key on rate limits and quota errors, and the secret never reaches your app. In lease mode you borrow a key for code that needs it raw, and `withKey` reports and rotates for you.

Dart 3.0 or later, built on `package:http`, async throughout. It runs in Dart on the server and the command line, and in Flutter on mobile, desktop and the web.

## Quick start

```dart
import 'package:acnologia/acnologia.dart';

Future<void> main() async {
  final acn = Acnologia(baseUrl: 'http://localhost:8080', token: 'acn_your_app_token');

  final resp = await acn.proxy('weather').get('/v1/forecast', query: {'city': 'Lisbon'});
  print('${resp.statusCode} ${resp.body}');

  acn.close();
}
```

`weather` is the slug of a service on your server. The request goes to `http://localhost:8080/proxy/weather/v1/forecast?city=Lisbon`.

## Install

The package is not on pub.dev yet. Depend on it from git:

```yaml
dependencies:
  acnologia:
    git:
      url: https://github.com/sidharthpunathil/acnologia.git
      path: sdk/dart
      ref: main
```

Then run `dart pub get` (or `flutter pub get`).

## Connect

Create an app in the dashboard and copy its token (`acn_...`, shown once; older `cog_...` tokens still work).

```dart
final acn = Acnologia(baseUrl: 'http://localhost:8080', token: 'acn_...');

// or from ACNOLOGIA_URL and ACNOLOGIA_TOKEN
final fromEnv = Acnologia.fromEnvironment();
```

`Acnologia.fromEnvironment()` reads the process environment, then values given at compile time with `--dart-define=ACNOLOGIA_URL=...`, which is the only source in a Flutter web build. Pass `environment:` to read from a map instead. It throws `ArgumentError` when either value is missing.

Both constructors also take:

| Argument | Default | Meaning |
|---|---|---|
| `httpClient` | a new `http.Client` | Any `http.Client`. `close()` closes it only if the SDK created it |
| `userAgent` | `acnologia-dart/0.1.0` | Not sent in browsers, which forbid it |
| `timeout` | 75 seconds | For API calls (lease, report, renew, services). Longer than the 60 second maximum lease wait. Proxy calls have no SDK timeout |

`await acn.services()` returns the services this token may use, each with `slug`, `name`, `provider`, `category`, `available`, `baseUrl` and `auth`.

## Proxy mode

`acn.proxy(service)` sends requests to `/proxy/{service}/{path}` and returns the upstream `http.Response` whatever its status:

```dart
final weather = acn.proxy('weather');

final resp = await weather.get('/v1/forecast', query: {'city': 'Lisbon'});
print('key ${resp.acnologiaKeyId}, attempts ${resp.acnologiaAttempts}');

await weather.post('/v1/alerts', body: {'city': 'Lisbon', 'level': 'high'});
```

`get`, `delete`, `post`, `put`, `patch` and `request(method, path, ...)` all take `query` and `headers`; the body methods also take `body`. A `String` or `List<int>` body is sent as is. Anything else is JSON encoded with `Content-Type: application/json` unless you set one. `stream(...)` returns an `http.StreamedResponse` for large or streamed bodies, and `url(path, query)` returns the full proxy URL.

Redirects are not followed (browsers follow them on their own), so the app token is not sent to another host.

Extensions on the response read Acnologia's headers: `acnologiaKeyId`, `acnologiaAttempts`, `acnologiaService` and `acnologiaCache`. When Acnologia itself refused the request (no key, circuit breaker open, bad token), `resp.acnologiaError` holds the typed exception and `resp.throwIfAcnologiaError()` throws it. A provider answer carries `X-Acnologia-Key-Id` and is never treated as an Acnologia error.

## Lease mode

Use a lease when a library insists on holding the raw key.

```dart
import 'package:http/http.dart' as http;

final client = http.Client();
final lease = await acn.lease('weather');
try {
  final resp = await http.Response.fromStream(
      await client.send(lease.request('GET', '/v1/forecast', query: {'city': 'Lisbon'})));
  await lease.report(resp.statusCode == 429 ? Outcome.rateLimited(const Duration(minutes: 1)) : Outcome.ok,
      statusCode: resp.statusCode);
} catch (e) {
  await lease.report(Outcome.error);
  rethrow;
}
```

`acn.lease(service, {excludeKeyIds, ttl, wait, exclusive})` options:

- `excludeKeyIds`: keys that must not be returned.
- `ttl`: the lease lifetime. The server default is 120 seconds on services with a concurrency limit and 300 otherwise.
- `wait`: 0 to 60 seconds to wait for a free slot when every usable key is busy. More than 60 throws `RangeError`.
- `exclusive`: no other caller gets the key until you report.

`Lease` fields: `leaseId`, `keyId`, `service`, `secret`, `auth` (`type`, `name`, `prefix`), `baseUrl`, `expiresAt` and `exclusive`. `toString()` never shows the secret or the app token. Read `lease.secret` only where you need it, and do not log it.

### Put the key on the request

- `lease.request(method, path, query:, headers:)` builds an `http.Request` to the provider with the secret applied. Send it with any `http.Client`.
- `lease.apply(url, headers)` returns a record `(url:, headers:)` with the secret added. The inputs are not changed.
- `lease.url(path, query)` joins the base URL and path without the secret.

Where the secret goes depends on `auth.type`: `query` sets the query parameter `auth.name` (default `api_key`), `header` sets the header `auth.name`, and `bearer` sets `auth.name` (default `Authorization`) to `Bearer <secret>`. A non empty `auth.prefix` goes in front of the secret for every type and replaces `Bearer ` for bearer.

### Report the outcome

```dart
final next = await lease.report(Outcome.rateLimited(const Duration(minutes: 1)), statusCode: 429, rotate: true);
if (next != null) {
  // retry with next
}
```

`lease.report(outcome, {statusCode, message, rotate})` takes one of `Outcome.ok`, `Outcome.rateLimited([retryAfter])`, `Outcome.quotaExhausted`, `Outcome.invalid`, `Outcome.error`, `Outcome.busy` or `Outcome.banned`. With `rotate: true` it returns the next `Lease`, or null when no other key is free.

Report on every path. On a service with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out.

### Renew for long jobs

```dart
final expires = await lease.renew(ttl: const Duration(minutes: 10));
```

`renew` returns the new expiry; `lease.expiresAt` keeps the old value. Without `ttl` the server uses 120 seconds, and it caps the value at 24 hours. A lease that is not yours or no longer open throws `AcnologiaApiException` with code `lease_not_found`.

## withKey

`withKey` leases a key, runs your callback with it, reports the outcome and returns the callback's value:

```dart
final forecast = await acn.withKey('weather', (lease) async {
  final resp = await http.Response.fromStream(
      await client.send(lease.request('GET', '/v1/forecast', query: {'city': 'Lisbon'})));
  if (resp.statusCode == 429) {
    throw const RateLimitedException(retryAfter: Duration(minutes: 1), statusCode: 429);
  }
  if (resp.statusCode == 402) throw const QuotaExhaustedException(statusCode: 402);
  if (resp.statusCode == 401) throw const InvalidKeyException(statusCode: 401);
  if (resp.statusCode >= 400) throw StateError('weather: http ${resp.statusCode}');
  return jsonDecode(resp.body) as Map<String, dynamic>;
}, maxAttempts: 3);
```

Map the provider's responses to these exceptions to rotate:

| Callback | Reported as | Then |
|---|---|---|
| completes | `ok` | returns its value |
| throws `RateLimitedException(retryAfter: ...)` | `rate_limited` | retries with the next key |
| throws `QuotaExhaustedException()` | `quota_exhausted` | retries with the next key |
| throws `InvalidKeyException()` | `invalid` | retries with the next key |
| throws anything else | `error` | rethrows it unchanged |

All three take an optional `statusCode` and `message` and are `KeyRotationException`s.

`withKey(service, body, {maxAttempts = 5, excludeKeyIds, ttl, wait, exclusive})` applies `excludeKeyIds` to the first lease only, and `ttl`, `wait` and `exclusive` to every lease it takes. After `maxAttempts` keys it throws the last rotation exception; the final attempt is reported without asking for a next key. When the pool runs dry it throws `NoKeyAvailableException` (or `AllKeysBusyException`).

Every lease is reported. A message sent with a report has the secret replaced by `[key]` and is cut to 500 characters.

## Errors

Every exception extends `AcnologiaException`, which has `message`, `statusCode`, `code` and `retryAfter`.

| Exception | When |
|---|---|
| `NoKeyAvailableException` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusyException` | 503 `all_keys_busy`: every usable key is at its concurrency limit. A `NoKeyAvailableException` with `busy` true. Retry after `retryAfter` (usually 1 second) or lease with `wait` |
| `ServiceUnavailableException` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `UnauthenticatedException` | 401: token missing, wrong, revoked or expired |
| `ForbiddenException` | 403: app disabled, or `service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed` |
| `AcnologiaApiException` | Any other non 2xx, for example 429 `app_limit_reached` or 404 `lease_not_found` |
| `RateLimitedException`, `QuotaExhaustedException`, `InvalidKeyException` | Thrown by you from `withKey` |

Bad arguments throw `ArgumentError` or `RangeError`. An API call that runs past `timeout` throws `TimeoutException`.

```dart
try {
  await acn.withKey('weather', fetchForecast, wait: const Duration(seconds: 20));
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

The tests replay the server's real response shapes through `MockClient` from `package:http/testing`.

## More

- [All SDKs](../README.md)
- [HTTP API and error codes](../../docs/api.md)
- [Request flows](../../docs/flows.md): what the proxy and leases do on the server
