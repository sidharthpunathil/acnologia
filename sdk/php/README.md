# Acnologia for PHP

Call APIs with the keys your Acnologia server holds, using your app's own `acn_` token. In proxy mode the server attaches the key, moves to the next key on rate limits and quota errors, and the secret never reaches your process. In lease mode you borrow a key for code that needs it raw, and `withKey` reports and rotates for you.

PHP 8.1 or later with the `curl` and `json` extensions. No Composer dependencies.

## Quick start

```php
require 'vendor/autoload.php';

$acn = new Acnologia\Acnologia('http://localhost:8080', 'acn_your_app_token');

$res = $acn->proxy('weather')->get('/v1/forecast', ['city' => 'Lisbon']);
if ($res->ok()) {
    print_r($res->json());
}
```

`weather` is the slug of a service on your server. The request goes to `http://localhost:8080/proxy/weather/v1/forecast?city=Lisbon`.

## Install

The package is not on Packagist yet. Point Composer at a checkout of this repository with a path repository in your `composer.json`:

```json
"repositories": [{ "type": "path", "url": "path/to/acnologia/sdk/php" }]
```

Then:

```bash
composer require acnologia/keys:@dev
```

## Connect

Create an app in the dashboard and copy its token (`acn_...`, shown once). The token is sent as `Authorization: Bearer acn_...`.

```php
$acn = new Acnologia\Acnologia(
    'http://localhost:8080',
    (string) getenv('ACNOLOGIA_TOKEN'),
    timeout: 30,
);
```

The constructor takes the base URL, the token, a curl timeout in seconds (default 30), an optional transport (see [Tests](#tests)) and an optional `accessPassword`. It throws `InvalidArgumentException` when the URL or the token is empty. It does not read `ACNOLOGIA_URL` or `ACNOLOGIA_TOKEN` itself, so pass them in with `getenv` as above.

`accessPassword` is the access password of a locked deploy. When set, every request carries it as `X-Acnologia-Access`. The consumer routes pass the lock with the app token alone, so most clients never need it:

```php
$acn = new Acnologia\Acnologia($url, $token, accessPassword: (string) getenv('MY_ACCESS_PASSWORD'));
```

`var_dump` and `print_r` show the token and the access password as `***`.

## What this token can do

```php
$me = $acn->me();                       // GET /api/v1/me
$me['app']['name'];                     // "billing-bot"
$me['limits']['daily_requests'];        // 1000, or null when unset
$me['usage']['today_requests'];         // 12
$me['limit_hit'];                       // null, or ['limit' => ..., 'reset_at' => ...]
$me['share'];                           // null for an ordinary app; for a share's token its status and more
```

`me()` returns the decoded JSON: `app`, `limits`, `usage`, `limit_hit`, `services` and `share`. A share's token still gets an answer when its share is paused, expired or revoked, with `share['status']` saying which.

`$acn->services()` returns the services this token may use, from `GET /api/v1/services`: `slug`, `name`, `provider`, `category`, `available`, `base_url`, `auth` and `guide_url`.

```php
$md = $acn->guideMarkdown('weather');   // the usage guide as Markdown
$g = $acn->guide('weather');            // ['markdown' => ..., 'generated' => bool, 'guide' => [...]]
```

`generated` is true when the server built the guide from the provider spec because nobody wrote one.

## Capabilities

A capability is one call (say `web-search`) that the server sends to whichever provider can serve it.

```php
foreach ($acn->capabilities() as $cap) {
    echo $cap['slug'], ': ', $cap['description'], PHP_EOL;   // also name, strategy, params, routes, url
}

$res = $acn->capability('web-search', ['query' => 'coffee']);
$res->json();          // the mapped answer
$res->service();       // the provider that answered
$res->tried();         // [['service' => 'serpapi', 'status' => 429], ['service' => 'tavily', 'status' => 200]]

$acn->capability('web-search', ['query' => 'coffee'], service: 'tavily', raw: true);
```

`$acn->capability($slug, $params = [], $service = null, $raw = false, $headers = [], $idempotencyKey = null, $noCache = false)` POSTs `$params` as JSON to `/proxy/{slug}`. `$service` adds `?_service=` to use only that provider, and `$raw` adds `?_raw=1` for the provider's own body. Like a proxy call, it returns the `Response` and does not throw for a non 2xx.

## Forward proxy

Apps can also call a provider's own URL with Acnologia as their `HTTPS_PROXY`. The proxy puts a key on the request. The app trusts the proxy's CA and uses its token as the proxy password.

```php
$acn->saveForwardProxyCA('acnologia-ca.pem');        // public route, sends no token
$env = $acn->forwardProxyEnv('acnologia-ca.pem');
// ['HTTPS_PROXY' => 'http://app:acn_...@keys.example.com:8888', 'HTTP_PROXY' => ..., 'NODE_USE_ENV_PROXY' => '1',
//  'NODE_EXTRA_CA_CERTS' => 'acnologia-ca.pem', 'REQUESTS_CA_BUNDLE' => 'acnologia-ca.pem', 'SSL_CERT_FILE' => 'acnologia-ca.pem']

// a child process uses its own HTTP client through the proxy
$proc = proc_open(['python3', 'my_script.py'], [], $pipes, null, $env + getenv());

// or curl in this process: no key in your code
$ch = curl_init('https://api.openai.com/v1/models');
curl_setopt_array($ch, [
    CURLOPT_PROXY => $env['HTTPS_PROXY'],
    CURLOPT_CAINFO => 'acnologia-ca.pem',
    CURLOPT_RETURNTRANSFER => true,
]);
```

| Call | Returns |
|---|---|
| `forwardProxy()` | `GET /api/v1/proxy` as an array: `proxy_url`, `proxy_url_with_token`, `ca_url`, `ca_fingerprint`, `passthrough`, `hosts` (`host`, `service`, `name`, `path`), `env`, `service_header` |
| `forwardProxyCA()` | The CA certificate as PEM text, from `GET /api/v1/proxy/ca.pem`, sent without a token |
| `saveForwardProxyCA($path = 'acnologia-ca.pem')` | Writes the PEM to `$path` and returns `$path` |
| `forwardProxyURL()` | `proxy_url` with user `app` and your token as the password, percent escaped |
| `forwardProxyEnv($caPath = 'acnologia-ca.pem')` | `HTTPS_PROXY`, `HTTP_PROXY`, `NODE_USE_ENV_PROXY`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` |

`forwardProxy()` holds a `<app token>` placeholder, never the token, so it is safe to log. `forwardProxyURL()` and `forwardProxyEnv()` contain the real token: keep them out of logs. With `CURLOPT_CAINFO` curl trusts only that file, which is enough for hosts the proxy opens. For a passthrough host, put the system bundle and the Acnologia CA in one file.

## Proxy mode

`$acn->proxy($service)` returns an `Acnologia\Proxy` with `get`, `post`, `put`, `delete` and `request`:

```php
$weather = $acn->proxy('weather');

$weather->get('/v1/forecast', ['city' => 'Lisbon'], ['Accept' => 'application/json']);
$weather->post('/v1/alerts', ['city' => 'Lisbon', 'level' => 'high']);
$weather->request('PATCH', '/v1/alerts/42', ['query' => [], 'headers' => [], 'body' => ['level' => 'low']]);
```

Array bodies are sent as JSON with `Content-Type: application/json`; strings are sent as is. `$weather->url($path, $query)` returns the full proxy URL. A service slug may also be a service group or a capability slug.

`post` and `put` take `$idempotencyKey`, sent as `Idempotency-Key`: a repeat of the same request with the same key within 10 minutes gets the stored answer and the provider is not called again. Every method takes `$noCache`, which sends `Cache-Control: no-cache` so a service with a cache TTL calls the provider. With `request()`, pass them as the `idempotencyKey` and `noCache` options.

```php
$res = $weather->post('/v1/alerts', ['level' => 'high'], idempotencyKey: 'alert-1234');
$res->replayed();                                   // true on a replay
$weather->get('/v1/forecast', ['city' => 'Lisbon'], noCache: true);
```

Every call returns an `Acnologia\Response`, including non 2xx responses: proxy calls never throw for a provider status. It has `status`, `headers` (lower cased names), `body`, `ok()`, `json()` and `header($name)`, plus:

| Method | From |
|---|---|
| `keyId()` | `X-Acnologia-Key-Id`, the key that served the request (`null` for a share's token) |
| `attempts()` | `X-Acnologia-Attempts`, how many keys the proxy tried |
| `service()` | `X-Acnologia-Service`, the service that answered |
| `cache()` | `X-Acnologia-Cache`: `hit`, `miss` and so on |
| `replayed()` | `Idempotent-Replayed: true` |
| `capability()` | `X-Acnologia-Capability` |
| `tried()` | `X-Acnologia-Tried` as a list of `['service' => ..., 'status' => ...]` |

## Lease mode

Use a lease when a library insists on holding the raw key.

```php
$lease = $acn->lease('weather');
['query' => $q, 'headers' => $h] = $lease->apply(['city' => 'Lisbon']);
$url = $lease->url('/v1/forecast', $q);
// call the provider with $url and $h, then:
$lease->report('ok');
```

`$acn->lease($service, $excludeKeyIds = [], $ttlSec = null, $waitSec = null, $exclusive = false)` returns an `Acnologia\Lease`:

| Property | Meaning |
|---|---|
| `leaseId`, `keyId`, `service` | Which lease and key this is |
| `secret` | The provider key. Never log it |
| `auth` | `['type' => ..., 'name' => ..., 'prefix' => ...]` |
| `baseUrl` | The provider's base URL |
| `expiresAt` | When the lease ends. `renew()` updates it |
| `exclusive` | True when no other caller can get this key |
| `reported` | True once the lease has been reported |

`var_dump` and `print_r` show `secret` as `***`.

### Put the key on the request

`$lease->apply($query, $headers)` returns `['query' => ..., 'headers' => ...]` with the secret placed where `auth['type']` says:

- `query`: the query parameter named by `auth['name']`
- `header`: the header named by `auth['name']` (or `Authorization`)
- `bearer`: `Authorization: Bearer <secret>`, or the header named by `auth['name']`

A non empty `auth['prefix']` goes in front of the secret for every type. For `bearer` it replaces `Bearer `, so `'Token '` gives `Authorization: Token ...`.

`$lease->url($path, $query)` joins `baseUrl`, the path and an optional query array.

### Report the outcome

```php
$next = $lease->report('rate_limited', 429, 60, 'hourly limit', rotate: true);
```

`$lease->report($outcome, $statusCode = null, $retryAfterSec = null, $message = null, $rotate = false)` accepts `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned` or `error`. With `$rotate` it returns the next `Lease`, or `null` when no other key is free.

Report on every path, including errors. With a manual lease, use `finally`:

```php
$lease = $acn->lease('weather');
try {
    // call the provider
    $lease->report('ok');
} finally {
    if (!$lease->reported) {
        $lease->report('error');
    }
}
```

### Renew for long jobs

`$lease->renew($ttlSec = null)` extends an open lease, returns the new expiry and updates `$lease->expiresAt`. `null` uses the server default of 120 seconds, and the server caps it at 24 hours. `$acn->renew($lease, $ttlSec = null)` does the same without updating the lease. A lease that is no longer open throws `AcnologiaException` with `errorCode` `lease_not_found`.

## withKey

`withKey` leases a key, calls your function with it, reports the outcome and returns the result:

```php
use Acnologia\Exception\{InvalidKey, QuotaExhausted, RateLimited};

$forecast = $acn->withKey('weather', function (Acnologia\Lease $lease) {
    ['query' => $q, 'headers' => $h] = $lease->apply(['city' => 'Lisbon']);
    $ch = curl_init($lease->url('/v1/forecast', $q));
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_HTTPHEADER => array_map(fn ($k, $v) => "$k: $v", array_keys($h), $h),
    ]);
    $body = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
    if ($status === 429) {
        throw RateLimited::after(60);
    }
    if ($status === 402) {
        throw new QuotaExhausted('', 402);
    }
    if ($status === 401 || $status === 403) {
        throw new InvalidKey('', $status);
    }
    return json_decode($body, true);
});
```

Map the provider's responses to these exceptions to rotate:

| Throw from `$fn` | Reported as | Then |
|---|---|---|
| nothing | `ok` | returns what `$fn` returned |
| `RateLimited` | `rate_limited` | retries with the next key |
| `QuotaExhausted` | `quota_exhausted` | retries with the next key |
| `InvalidKey` | `invalid` | retries with the next key |
| anything else | `error` | rethrows it |

All three live in `Acnologia\Exception` and extend `KeyProblem`. Their constructor is `($message = '', $statusCode = null, $retryAfter = null)`, and `RateLimited::after($retryAfter, $statusCode = 429, $message = '')` is a shorthand.

`$acn->withKey($service, $fn, $maxAttempts = 5, $ttlSec = null, $waitSec = null, $exclusive = false)` passes `$ttlSec`, `$waitSec` and `$exclusive` to every lease it takes. After `$maxAttempts` keys it rethrows the last exception. It reports every lease, even when `$fn` throws, and replaces the secret with `[key]` in any message it reports.

## Concurrency limits

Some services cap how many requests one key may run at once. Acnologia enforces the cap per key, so on such a service each lease holds one of the key's slots until you report it or its TTL runs out. The default TTL is 120 seconds on limited services and 300 otherwise.

- **Always report.** `withKey` does it for you. With a manual `lease()`, report in `finally`, or the slot stays taken until the TTL expires.
- **Wait instead of failing.** Pass `waitSec` (0 to 60) and the server waits that long for a free slot. The curl timeout is extended to cover the wait.

```php
use Acnologia\Exception\AllKeysBusy;

$forecast = $acn->withKey('weather', $fetchForecast, waitSec: 20);

try {
    $lease = $acn->lease('weather', waitSec: 20);
} catch (AllKeysBusy $e) {
    sleep($e->retryAfter ?? 1);
}
```

Proxy mode handles this on the server: it queues proxied requests for up to the service's `queue_timeout_sec` and never sends more than the limit in parallel on one key.

## Errors

All classes are in `Acnologia\Exception`.

| Class | When |
|---|---|
| `AcnologiaException` | Any non 2xx from Acnologia (`statusCode`, `errorCode`), or a curl failure (`statusCode` 0) |
| `NoKeyAvailable` | 503 `no_key_available`: every key is cooling down, exhausted or excluded. `retryAfter` in seconds |
| `AllKeysBusy` | 503 `all_keys_busy`: every usable key is at its concurrency limit. Extends `NoKeyAvailable`, `busy` is true |
| `ServiceUnavailable` | 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open. `retryAfter` in seconds. Not a `NoKeyAvailable` |
| `AppLimitReached` | 429 `app_limit_reached`: the app used up a request or budget limit. `limit` (`daily_requests`, `monthly_requests`, `daily_budget`, `monthly_budget`), `resetAt` and `retryAfter` |
| `ShareUnavailable` | 403 `share_revoked`, `share_paused` or `share_expired`. `shareStatus` is `revoked`, `paused` or `expired` |
| `RateLimited`, `QuotaExhausted`, `InvalidKey` | Thrown by you inside `withKey` |

`AllKeysBusy` clears as soon as another caller reports, so a short sleep or `waitSec` usually fixes it. Because it extends `NoKeyAvailable`, a `catch (NoKeyAvailable $e)` block catches both; check `$e->busy` to tell them apart.

A 401 (bad token) or 403 (for example `lease_not_allowed` on a proxy only service) is an `AcnologiaException`: check `statusCode` and `errorCode`. Other codes worth checking there: `empty_group`, `bad_params`, `no_route`, `no_route_worked`, `proxy_not_allowed`, `loop_detected`. The token and the access password are replaced with `[key]` in exception messages.

## Tests

```bash
php tests/run.php
```

Without a local PHP:

```bash
docker run --rm -v "$PWD":/app -w /app php:8.1-cli php tests/run.php
```

The tests replace curl with a transport, the constructor's fourth argument:

```php
$acn = new Acnologia\Acnologia('http://acn.test', 'acn_test', 30,
    fn (string $method, string $url, array $headers, ?string $body) => new Acnologia\Response(200, [], '{}'));
```

## More

- [All SDKs](../README.md)
- [HTTP API and error codes](../../docs/api.md)
- [Request flows](../../docs/flows.md): what the proxy and leases do on the server
