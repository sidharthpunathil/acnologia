# cogito/keys (PHP)

PHP client for [Cogito](../../docs/llms.txt), a self-hosted API key pool. Cogito
holds many keys per provider and hands out a working one, rotating automatically
when a key is rate limited or out of quota.

PHP 8.1+, uses the `curl` and `json` extensions, no Composer dependencies.

## Install

```bash
composer require cogito/keys
```

```php
require 'vendor/autoload.php';

$cog = new Cogito\Cogito('http://localhost:8080', 'cog_your_app_token');
```

The token is an app token from the Cogito dashboard, sent as `Authorization: Bearer cog_...`.

## Proxy mode

Send the provider request to Cogito. It injects a key and retries with the next
key on rate limits. Your code never sees a secret. Responses come back as
`Cogito\Response` (`status`, `headers`, `body`, `ok()`, `json()`, `header()`),
including non 2xx ones.

### SerpAPI search

```php
$res = $cog->proxy('serpapi')->get('/search.json', ['engine' => 'google', 'q' => 'best espresso grinder']);
foreach (array_slice($res->json()['organic_results'] ?? [], 0, 5) as $r) {
    echo $r['title'], ' ', $r['link'], PHP_EOL;
}
echo 'served by key ', $res->header('X-Cogito-Key-Id'), PHP_EOL;
```

### ElevenLabs text to speech

```php
$res = $cog->proxy('elevenlabs')->post(
    '/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM',
    ['text' => 'Hello from Cogito.', 'model_id' => 'eleven_multilingual_v2'],
    ['Accept' => 'audio/mpeg'],
);
if ($res->ok()) {
    file_put_contents('hello.mp3', $res->body);
}
```

Also available: `put()`, `delete()` and `request($method, $path, ['query' => [], 'headers' => [], 'body' => ...])`.
Array bodies are sent as JSON; strings are sent as is.

## Lease mode

`$cog->lease($service, $excludeKeyIds = [], $ttlSec = null)` returns a
`Cogito\Lease` with `leaseId`, `keyId`, `service`, `secret`, `auth`
(`['type' => ..., 'name' => ...]`), `baseUrl` and `expiresAt`. It throws
`Cogito\Exception\NoKeyAvailable` (with `retryAfter`) when the pool is drained.

`$lease->apply($query, $headers)` returns `['query' => ..., 'headers' => ...]`
with the secret placed where `auth.type` says: `query` adds a query param
(SerpAPI `api_key`), `header` adds a header (ElevenLabs `xi-api-key`), `bearer`
sets `Authorization: Bearer ...`. `$lease->url($path, $query)` builds a provider URL.

`$lease->report($outcome, $statusCode, $retryAfterSec, $message, $rotate)`
reports `ok`, `rate_limited`, `quota_exhausted`, `invalid` or `error`, and
returns the next `Lease` (or null) when `$rotate` is true.

### withKey

`$cog->withKey($service, $fn, $maxAttempts = 5)` leases a key, calls `$fn($lease)`,
reports and returns the result. Throw `RateLimited`, `QuotaExhausted` or
`InvalidKey` from `$fn` to cool the key down and retry with the next one. Any
other exception is reported as `error` and rethrown.

SerpAPI with your own HTTP call:

```php
use Cogito\Exception\{InvalidKey, QuotaExhausted, RateLimited};

$data = $cog->withKey('serpapi', function (Cogito\Lease $lease) {
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
$audio = $cog->withKey('elevenlabs', function (Cogito\Lease $lease) {
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

Manual flow:

```php
$lease = $cog->lease('serpapi');
// ... call the provider ...
$next = $lease->report('rate_limited', 429, 3600, 'hourly limit', rotate: true);
```

## Errors

| class | when |
|---|---|
| `Cogito\Exception\CogitoException` | non 2xx from Cogito (`statusCode`, `errorCode`), or curl failure |
| `Cogito\Exception\NoKeyAvailable` | 503 `no_key_available`, `retryAfter` in seconds |
| `RateLimited`, `QuotaExhausted`, `InvalidKey` | thrown by you inside `withKey` (all extend `KeyProblem`) |

## Tests

```bash
php tests/run.php
```

The constructor takes an optional fourth argument, a transport callable
`fn(string $method, string $url, array $headers, ?string $body): Cogito\Response`,
which the tests use instead of curl.
