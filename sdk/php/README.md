# acnologia/keys (PHP)

PHP client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. Acnologia
holds many keys per provider and hands out a working one, rotating automatically
when a key is rate limited or out of quota.

PHP 8.1+, uses the `curl` and `json` extensions, no Composer dependencies.

## Install

```bash
composer require acnologia/keys
```

```php
require 'vendor/autoload.php';

$acn = new Acnologia\Acnologia('http://localhost:8080', 'acn_your_app_token');
```

The token is an app token from the Acnologia dashboard, sent as `Authorization: Bearer acn_...`.

## Proxy mode

Send the provider request to Acnologia. It injects a key and retries with the next
key on rate limits. Your code never sees a secret. Responses come back as
`Acnologia\Response` (`status`, `headers`, `body`, `ok()`, `json()`, `header()`),
including non 2xx ones.

### SerpAPI search

```php
$res = $acn->proxy('serpapi')->get('/search.json', ['engine' => 'google', 'q' => 'best espresso grinder']);
foreach (array_slice($res->json()['organic_results'] ?? [], 0, 5) as $r) {
    echo $r['title'], ' ', $r['link'], PHP_EOL;
}
echo 'served by key ', $res->header('X-Acnologia-Key-Id'), PHP_EOL;
```

### ElevenLabs text to speech

```php
$res = $acn->proxy('elevenlabs')->post(
    '/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM',
    ['text' => 'Hello from Acnologia.', 'model_id' => 'eleven_multilingual_v2'],
    ['Accept' => 'audio/mpeg'],
);
if ($res->ok()) {
    file_put_contents('hello.mp3', $res->body);
}
```

Also available: `put()`, `delete()` and `request($method, $path, ['query' => [], 'headers' => [], 'body' => ...])`.
Array bodies are sent as JSON; strings are sent as is.

## Lease mode

`$acn->lease($service, $excludeKeyIds = [], $ttlSec = null, $waitSec = null, $exclusive = false)` returns a
`Acnologia\Lease` with `leaseId`, `keyId`, `service`, `secret`, `auth`
(`['type' => ..., 'name' => ..., 'prefix' => ...]`), `baseUrl`, `expiresAt` and `exclusive`. It throws
`Acnologia\Exception\NoKeyAvailable` (with `retryAfter`) when the pool is drained.
`exclusive: true` keeps the key from every other caller until you report or the
lease expires.

`$lease->apply($query, $headers)` returns `['query' => ..., 'headers' => ...]`
with the secret placed where `auth.type` says: `query` adds a query param
(SerpAPI `api_key`), `header` adds a header (ElevenLabs `xi-api-key`), `bearer`
sets `Authorization: Bearer ...`. A non empty `auth.prefix` goes in front of the
secret for every type; for `bearer` it replaces `Bearer ` (so `'Token '` gives
`Authorization: Token ...`). `$lease->url($path, $query)` builds a provider URL.

`$lease->report($outcome, $statusCode, $retryAfterSec, $message, $rotate)`
reports `ok`, `rate_limited`, `quota_exhausted`, `invalid` or `error`, and
returns the next `Lease` (or null) when `$rotate` is true.

`$lease->renew($ttlSec = null)` extends an open lease so a long job keeps its
key (null uses the server default of 120 seconds; the server caps it at 24
hours). It returns the new expiry and updates `$lease->expiresAt`.
`$acn->renew($lease, $ttlSec = null)` does the same without updating the lease.
A lease that is no longer open throws `AcnologiaException` with `errorCode`
`lease_not_found`.

### withKey

`$acn->withKey($service, $fn, $maxAttempts = 5, $ttlSec = null, $waitSec = null, $exclusive = false)` leases a key, calls `$fn($lease)`,
reports and returns the result. Throw `RateLimited`, `QuotaExhausted` or
`InvalidKey` from `$fn` to cool the key down and retry with the next one. Any
other exception is reported as `error` and rethrown.

SerpAPI with your own HTTP call:

```php
use Acnologia\Exception\{InvalidKey, QuotaExhausted, RateLimited};

$data = $acn->withKey('serpapi', function (Acnologia\Lease $lease) {
    ['query' => $q] = $lease->apply(['engine' => 'google', 'q' => 'coffee']);
    $ch = curl_init($lease->url('/search.json', $q));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    $body = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
    if ($status === 429) {
        throw str_contains($body, 'run out of searches')
            ? new QuotaExhausted('out of searches', 429)
            : RateLimited::after(3600);
    }
    if ($status === 401) {
        throw new InvalidKey('', 401);
    }
    return json_decode($body, true);
});
```

ElevenLabs:

```php
$audio = $acn->withKey('elevenlabs', function (Acnologia\Lease $lease) {
    ['headers' => $h] = $lease->apply([], ['Accept' => 'audio/mpeg', 'Content-Type' => 'application/json']);
    $ch = curl_init($lease->url('/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM'));
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_POSTFIELDS => json_encode(['text' => 'Hi', 'model_id' => 'eleven_multilingual_v2']),
        CURLOPT_HTTPHEADER => array_map(fn ($k, $v) => "$k: $v", array_keys($h), $h),
    ]);
    $body = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
    if ($status === 429) {
        throw RateLimited::after(5);
    }
    if ($status === 401) {
        throw str_contains($body, 'quota_exceeded') ? new QuotaExhausted('', 401) : new InvalidKey('', 401);
    }
    return $body;
});
file_put_contents('hi.mp3', $audio);
```

Manual flow (report on every path, see Concurrency limits):

```php
$lease = $acn->lease('serpapi');
try {
    // ... call the provider ...
    $next = $lease->report('rate_limited', 429, 3600, 'hourly limit', rotate: true);
} finally {
    if (!$lease->reported) {
        $lease->report('error');
    }
}
```

## Concurrency limits

Some providers cap how many requests one key may run at once (ElevenLabs
allows 2 per key by default). Acnologia enforces this per key, so a lease is
exclusive: on a service with a limit, each lease holds one of the key's slots
until you report it or its `ttlSec` runs out (default 120 seconds on limited
services, 300 otherwise).

- **Always report.** `withKey` reports every lease, even when `$fn` throws.
  With a manual `lease()`, report in a `finally` block, or the slot stays taken
  until the TTL expires.
- **Wait instead of failing.** Pass `waitSec` (0 to 60) and the server waits
  that long for a slot when every usable key is busy. The curl timeout is
  extended to cover the wait.

```php
use Acnologia\Exception\AllKeysBusy;

$audio = $acn->withKey('elevenlabs', $speak, waitSec: 20);

try {
    $lease = $acn->lease('elevenlabs', waitSec: 20);
} catch (AllKeysBusy $e) {
    sleep($e->retryAfter ?? 1); // $e->busy === true
}
```

`AllKeysBusy` (503 `all_keys_busy`) means the keys are fine but in use; it
clears as soon as another caller reports. It extends `NoKeyAvailable`, so
existing `catch (NoKeyAvailable $e)` blocks still catch it; check `$e->busy` to
tell them apart.

Proxy mode needs none of this: Acnologia queues proxied requests for up to the
service's `queue_timeout_sec` (30 seconds for ElevenLabs) and never sends more
than the limit in parallel on one key.

## Errors

| class | when |
|---|---|
| `Acnologia\Exception\AcnologiaException` | non 2xx from Acnologia (`statusCode`, `errorCode`), or curl failure |
| `Acnologia\Exception\NoKeyAvailable` | 503 `no_key_available`, `retryAfter` in seconds, `busy` |
| `Acnologia\Exception\AllKeysBusy` | 503 `all_keys_busy`: every usable key is at its concurrency limit (extends `NoKeyAvailable`, `busy` is true) |
| `Acnologia\Exception\ServiceUnavailable` | 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open; `retryAfter` in seconds (not a `NoKeyAvailable`) |
| `RateLimited`, `QuotaExhausted`, `InvalidKey` | thrown by you inside `withKey` (all extend `KeyProblem`) |

## Tests

```bash
php tests/run.php
```

The constructor takes an optional fourth argument, a transport callable
`fn(string $method, string $url, array $headers, ?string $body): Acnologia\Response`,
which the tests use instead of curl.
