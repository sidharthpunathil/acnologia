<?php

// Dependency free smoke tests: php tests/run.php
declare(strict_types=1);

spl_autoload_register(static function (string $class): void {
    if (str_starts_with($class, 'Acnologia\\')) {
        require __DIR__ . '/../src/' . str_replace('\\', '/', substr($class, strlen('Acnologia\\'))) . '.php';
    }
});

use Acnologia\Acnologia;
use Acnologia\Exception\AcnologiaException;
use Acnologia\Exception\AllKeysBusy;
use Acnologia\Exception\AppLimitReached;
use Acnologia\Exception\NoKeyAvailable;
use Acnologia\Exception\RateLimited;
use Acnologia\Exception\ServiceUnavailable;
use Acnologia\Exception\ShareUnavailable;
use Acnologia\Response;

$keys = [1, 2];
$bad = [];
$log = [];
$n = 0;
$lease = static function (array $exclude, bool $exclusive = false) use (&$keys, &$bad, &$n): ?array {
    foreach ($keys as $k) {
        if (!in_array($k, $exclude, true) && !in_array($k, $bad, true)) {
            $n++;
            return ['lease_id' => "l$n", 'key_id' => $k, 'service' => 'serpapi', 'secret' => "s$k",
                'auth' => ['type' => 'query', 'name' => 'api_key'], 'base_url' => 'https://serpapi.com',
                'expires_at' => '2026-11-01T00:00:00Z', 'exclusive' => $exclusive];
        }
    }
    return null;
};
$leases = [];
$busy = false;
$breaker = null;
$transport = static function (string $method, string $url, array $headers, ?string $body) use (&$log, &$bad, &$leases, &$busy, &$breaker, $lease): Response {
    $log[] = [$method, $url, $headers, $body];
    $path = parse_url($url, PHP_URL_PATH);
    $json = $body !== null ? json_decode($body, true) : null;
    if ($path === '/api/v1/lease') {
        if ($breaker !== null) {
            return new Response(503, ['retry-after' => '9'], $breaker);
        }
        if ($busy) {
            return new Response(503, [], '{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}');
        }
        $l = $lease($json['exclude_key_ids'] ?? [], ($json['exclusive'] ?? false) === true);
        if ($l === null) {
            return new Response(503, [], '{"error":{"code":"no_key_available","message":"drained"},"retry_after_sec":9}');
        }
        $leases[$l['lease_id']] = $l['key_id'];
        return new Response(200, [], json_encode($l));
    }
    if (preg_match('#^/api/v1/leases/([^/]+)/renew$#', $path, $m)) {
        if (!isset($leases[rawurldecode($m[1])])) {
            return new Response(404, [], '{"error":{"code":"lease_not_found","message":"lease is not open"}}');
        }
        return new Response(200, [], json_encode(['ok' => true, 'expires_at' => '2026-11-02T00:00:' . ($json['ttl_sec'] ?? 0) . 'Z']));
    }
    if (preg_match('#^/api/v1/leases/([^/]+)/report$#', $path, $m)) {
        $key = $leases[rawurldecode($m[1])];
        if ($json['outcome'] !== 'ok') {
            $bad[] = $key;
        }
        $next = $json['rotate'] ? $lease([$key]) : null;
        if ($next) {
            $leases[$next['lease_id']] = $next['key_id'];
        }
        return new Response(200, [], json_encode(['ok' => true, 'next' => $next]));
    }
    return new Response(200, ['x-acnologia-key-id' => '1'], json_encode(['url' => $url, 'body' => $body]));
};

$fails = 0;
$check = static function (bool $cond, string $name) use (&$fails): void {
    echo ($cond ? 'ok   ' : 'FAIL ') . $name . PHP_EOL;
    $fails += $cond ? 0 : 1;
};

$acn = new Acnologia('http://acnologia.test/', 'acn_x', 30, $transport);

$res = $acn->proxy('serpapi')->get('/search.json', ['q' => 'coffee']);
$check($res->json()['url'] === 'http://acnologia.test/proxy/serpapi/search.json?q=coffee', 'proxy get url');
$check($log[0][2]['Authorization'] === 'Bearer acn_x', 'proxy sends token');
$res = $acn->proxy('elevenlabs')->post('v1/text-to-speech/v', ['text' => 'hi']);
$check($res->json()['body'] === '{"text":"hi"}', 'proxy post json');

$l = $acn->lease('serpapi');
$check($l->apply(['q' => 'a'])['query'] === ['q' => 'a', 'api_key' => 's1'], 'apply query');
$check($l->url('/search.json', ['q' => 'a']) === 'https://serpapi.com/search.json?q=a', 'lease url');
$l->report('ok');

$seen = [];
$out = $acn->withKey('serpapi', static function ($lease) use (&$seen) {
    $seen[] = $lease->keyId;
    if ($lease->keyId === 1) {
        throw RateLimited::after(60);
    }
    return 'done';
});
$check($out === 'done' && $seen === [1, 2], 'withKey rotates');

$lastBody = static function () use (&$log): array {
    for ($i = count($log) - 1; $i >= 0; $i--) {
        if (parse_url($log[$i][1], PHP_URL_PATH) === '/api/v1/lease') {
            return json_decode($log[$i][3], true);
        }
    }
    return [];
};

$bad = [];
$acn->lease('elevenlabs', [], 60, 20)->report('ok');
$check($lastBody() === ['service' => 'elevenlabs', 'ttl_sec' => 60, 'wait_sec' => 20], 'lease sends wait_sec');
$acn->lease('elevenlabs', [], null, 0)->report('ok');
$check(!array_key_exists('wait_sec', $lastBody()), 'wait_sec 0 omitted');
try {
    $acn->lease('elevenlabs', [], null, 61);
    $check(false, 'wait_sec above 60 rejected');
} catch (\InvalidArgumentException) {
    $check(true, 'wait_sec above 60 rejected');
}

$bad = [];
$acn->withKey('elevenlabs', static function ($lease) {
    if ($lease->keyId === 1) {
        throw RateLimited::after(5);
    }
    return 'ok';
}, waitSec: 15);
$leaseBodies = array_values(array_filter($log, static fn ($r) => parse_url($r[1], PHP_URL_PATH) === '/api/v1/lease'));
$check(json_decode(end($leaseBodies)[3], true)['wait_sec'] === 15, 'withKey sends wait_sec');

$bad = [];
$reportsBefore = count(array_filter($log, static fn ($r) => str_ends_with((string) parse_url($r[1], PHP_URL_PATH), '/report')));
try {
    $acn->withKey('serpapi', static function () {
        throw new \LogicException('boom');
    });
} catch (\LogicException) {
}
$reportsAfter = count(array_filter($log, static fn ($r) => str_ends_with((string) parse_url($r[1], PHP_URL_PATH), '/report')));
$lastReport = json_decode(end($log)[3], true);
$check($reportsAfter === $reportsBefore + 1 && $lastReport['outcome'] === 'error', 'withKey reports on other exceptions');

$mk = static fn (array $auth): \Acnologia\Lease => \Acnologia\Lease::fromArray($acn, ['lease_id' => 'p', 'key_id' => 1, 'secret' => 'sk', 'auth' => $auth]);
$check($mk(['type' => 'query', 'name' => 'api_key'])->auth['prefix'] === '', 'prefix defaults to empty');
$check($mk(['type' => 'query', 'name' => 'key', 'prefix' => 'pk_'])->apply(['q' => 'a'])['query'] === ['q' => 'a', 'key' => 'pk_sk'], 'prefix query');
$check($mk(['type' => 'header', 'name' => 'Authorization', 'prefix' => 'Token '])->apply()['headers'] === ['Authorization' => 'Token sk'], 'prefix header');
$check($mk(['type' => 'header', 'name' => 'xi-api-key'])->apply()['headers'] === ['xi-api-key' => 'sk'], 'header without prefix');
$check($mk(['type' => 'bearer', 'name' => '', 'prefix' => 'Key '])->apply()['headers'] === ['Authorization' => 'Key sk'], 'prefix replaces Bearer');
$check($mk(['type' => 'bearer', 'name' => 'X-Token', 'prefix' => 'Token '])->apply()['headers'] === ['X-Token' => 'Token sk'], 'prefix bearer custom name');
$check($mk(['type' => 'bearer', 'name' => ''])->apply()['headers'] === ['Authorization' => 'Bearer sk'], 'bearer default');

$secret = 'a b+c/d';
$scrubbed = Acnologia::scrubMessage("GET /x?key=$secret&k2=" . urlencode($secret) . '&k3=' . rawurlencode($secret), $secret);
$check($scrubbed === 'GET /x?key=[key]&k2=[key]&k3=[key]', 'scrub raw and encoded secret');
$check(strlen(Acnologia::scrubMessage(str_repeat('x', 600), 'sk')) === 500, 'scrub truncates to 500');
$check(Acnologia::scrubMessage('no secret here', '') === 'no secret here', 'scrub with empty secret');
$cut = Acnologia::scrubMessage(str_repeat('x', 499) . "\u{00e9}tail", 'sk');
$check($cut === str_repeat('x', 499) && preg_match('//u', $cut) === 1, 'scrub keeps valid utf8');

$bad = [];
$keys = [1, 2];
try {
    $acn->withKey('serpapi', static function ($lease) {
        throw new \RuntimeException('GET https://serpapi.com/search?api_key=' . $lease->secret . ' failed');
    });
} catch (\RuntimeException) {
}
$lastReport = json_decode(end($log)[3], true);
$check($lastReport['outcome'] === 'error' && !str_contains($lastReport['message'], 's1') && str_contains($lastReport['message'], 'api_key=[key]'), 'withKey scrubs error report');

$bad = [];
$keys = [1, 2];
$acn->withKey('serpapi', static function ($lease) {
    if ($lease->keyId === 1) {
        throw new RateLimited('429 for key ' . $lease->secret, 429, 5);
    }
    return 'ok';
});
$rlReport = null;
foreach ($log as $r) {
    if (str_ends_with((string) parse_url($r[1], PHP_URL_PATH), '/report')) {
        $b = json_decode($r[3], true);
        if ($b['outcome'] === 'rate_limited') {
            $rlReport = $b;
        }
    }
}
$check($rlReport !== null && $rlReport['message'] === '429 for key [key]', 'withKey scrubs key problem report');

$busy = true;
try {
    $acn->lease('elevenlabs', [], null, 5);
    $check(false, 'all keys busy');
} catch (NoKeyAvailable $e) {
    $check($e instanceof AllKeysBusy, 'all keys busy throws AllKeysBusy');
    $check($e->busy && $e->errorCode === 'all_keys_busy' && $e->retryAfter === 1 && $e->statusCode === 503, 'all keys busy fields');
}
$busy = false;

$keys = [];
try {
    $acn->lease('serpapi');
    $check(false, 'no key available');
} catch (NoKeyAvailable $e) {
    $check($e->retryAfter === 9, 'no key available');
    $check(!$e->busy && !($e instanceof AllKeysBusy), 'no key available is not busy');
}

$keys = [1, 2];
$bad = [];
$leases['l/x'] = 1;
$rl = new \Acnologia\Lease($acn, 'l/x', 1, 'serpapi', 's1', ['type' => 'query', 'name' => 'api_key', 'prefix' => ''], 'https://serpapi.com', null);
$check($rl->exclusive === false, 'lease constructor stays compatible');
$exp = $rl->renew(90);
$check($exp === '2026-11-02T00:00:90Z' && $rl->expiresAt === $exp, 'renew updates expiresAt');
$check(str_ends_with($log[count($log) - 1][1], '/api/v1/leases/l%2Fx/renew'), 'renew escapes lease id');
$check(json_decode(end($log)[3], true) === ['ttl_sec' => 90], 'renew sends ttl_sec');
$rl->renew();
$check(end($log)[3] === '{}', 'renew without ttl sends {}');
$check($acn->renew('l/x', 5) === '2026-11-02T00:00:5Z', 'client renew by id');
try {
    $acn->renew('gone', 60);
    $check(false, 'renew not found');
} catch (AcnologiaException $e) {
    $check($e->statusCode === 404 && $e->errorCode === 'lease_not_found', 'renew not found');
}

$ex = $acn->lease('serpapi', exclusive: true);
$check($lastBody() === ['service' => 'serpapi', 'exclusive' => true] && $ex->exclusive, 'lease exclusive');
$ex->report('ok');
$plain = $acn->lease('serpapi');
$check(!array_key_exists('exclusive', $lastBody()) && !$plain->exclusive, 'exclusive omitted by default');
$plain->report('ok');

$bad = [];
$keys = [1];
try {
    $acn->withKey('serpapi', static function () {
        throw RateLimited::after(5);
    }, ttlSec: 30, exclusive: true);
} catch (NoKeyAvailable) {
}
$bodies = array_map(static fn ($r) => json_decode($r[3], true), array_slice(array_values(array_filter($log, static fn ($r) => parse_url($r[1], PHP_URL_PATH) === '/api/v1/lease')), -2));
$check(count($bodies) === 2 && $bodies[0]['exclusive'] === true && $bodies[1]['exclusive'] === true && $bodies[1]['ttl_sec'] === 30, 'withKey forwards exclusive');

$keys = [1, 2];
$bad = [];
$breaker = '{"error":{"code":"service_unavailable","message":"circuit open"},"retry_after_sec":42}';
try {
    $acn->lease('serpapi');
    $check(false, 'service unavailable');
} catch (AcnologiaException $e) {
    $check($e instanceof ServiceUnavailable && !($e instanceof NoKeyAvailable), 'service unavailable type');
    $check($e->retryAfter === 42 && $e->statusCode === 503 && $e->errorCode === 'service_unavailable' && $e->getMessage() === 'circuit open', 'service unavailable fields');
}
$breaker = '{"error":{"code":"service_unavailable","message":"circuit open"}}';
try {
    $acn->lease('serpapi');
} catch (ServiceUnavailable $e) {
    $check($e->retryAfter === 9, 'service unavailable falls back to Retry-After');
}
$breaker = '';
try {
    $acn->lease('serpapi');
} catch (AcnologiaException $e) {
    $check($e instanceof NoKeyAvailable, 'empty 503 is still NoKeyAvailable');
}
$breaker = null;

// 0.2.0 surface: each call against a scripted transport that records the request.
$reqs = [];
$next = null;
$script = static function (string $method, string $url, array $headers, ?string $body) use (&$reqs, &$next): Response {
    $reqs[] = ['method' => $method, 'url' => $url, 'path' => parse_url($url, PHP_URL_PATH), 'query' => (string) parse_url($url, PHP_URL_QUERY), 'headers' => $headers, 'body' => $body];
    return $next;
};
$last = static function () use (&$reqs): array {
    return $reqs[count($reqs) - 1];
};
$jsonRes = static fn (array $data, int $status = 200, array $headers = []): Response => new Response($status, $headers + ['content-type' => 'application/json'], json_encode($data));
$c = new Acnologia('http://acn.test', 'acn_x', 30, $script);

$check(Acnologia::VERSION === '0.2.0', 'version is 0.2.0');

$next = $jsonRes(['app' => ['id' => 7, 'name' => 'bot'], 'limits' => ['daily_requests' => 100], 'share' => null, 'services' => [['slug' => 'serpapi', 'available' => 2]]]);
$me = $c->me();
$check($last()['method'] === 'GET' && $last()['path'] === '/api/v1/me' && $last()['headers']['Authorization'] === 'Bearer acn_x', 'me request');
$check($me['app']['name'] === 'bot' && $me['limits']['daily_requests'] === 100 && $me['share'] === null && $me['services'][0]['slug'] === 'serpapi' && $me['services'][0]['available'] === 2, 'me parsed, available is a number');

$next = $jsonRes(['services' => [['slug' => 'serpapi', 'guide_url' => 'http://acn.test/api/v1/services/serpapi/guide']]]);
$check($c->services()[0]['guide_url'] === 'http://acn.test/api/v1/services/serpapi/guide', 'services carry guide_url');

$next = $jsonRes(['markdown' => "# SerpApi\n", 'generated' => true, 'guide' => ['endpoints' => [['path' => '/search.json']]]]);
$g = $c->guide('serp api');
$check($last()['path'] === '/api/v1/services/serp%20api/guide' && $last()['headers']['Accept'] === 'application/json', 'guide request');
$check($g['markdown'] === "# SerpApi\n" && $g['generated'] === true && $g['guide']['endpoints'][0]['path'] === '/search.json', 'guide parsed');

$next = new Response(200, ['content-type' => 'text/markdown'], "# SerpApi\n");
$check($c->guideMarkdown('serpapi') === "# SerpApi\n" && $last()['headers']['Accept'] === 'text/markdown', 'guideMarkdown');
$next = $jsonRes(['error' => ['code' => 'service_not_allowed', 'message' => 'no']], 403);
try {
    $c->guideMarkdown('maps');
    $check(false, 'guideMarkdown error');
} catch (AcnologiaException $e) {
    $check($e->statusCode === 403 && $e->errorCode === 'service_not_allowed', 'guideMarkdown error');
}

$next = $jsonRes(['capabilities' => [['slug' => 'web-search', 'name' => 'Web search', 'description' => 'Search', 'strategy' => 'fallback',
    'params' => ['query' => ['type' => 'string']], 'routes' => [['service' => 'serpapi', 'mapped' => true]], 'url' => 'http://acn.test/proxy/web-search']]]);
$caps = $c->capabilities();
$check($last()['method'] === 'GET' && $last()['path'] === '/api/v1/capabilities' && $last()['headers']['Authorization'] === 'Bearer acn_x', 'capabilities request');
$check($caps[0]['slug'] === 'web-search' && $caps[0]['routes'][0]['service'] === 'serpapi' && $caps[0]['routes'][0]['mapped'] === true && $caps[0]['params']['query']['type'] === 'string', 'capabilities parsed');

$next = new Response(200, ['x-acnologia-capability' => 'web-search', 'x-acnologia-service' => 'tavily', 'x-acnologia-tried' => 'serpapi=429, tavily=200', 'x-acnologia-attempts' => '2'], '{"items":[]}');
$res = $c->capability('web-search', ['query' => 'coffee'], service: 'tavily', raw: true, idempotencyKey: 'k1', noCache: true);
$r = $last();
$check($r['method'] === 'POST' && $r['path'] === '/proxy/web-search' && $r['query'] === '_service=tavily&_raw=1', 'capability request line');
$check($r['body'] === '{"query":"coffee"}' && $r['headers']['Content-Type'] === 'application/json' && $r['headers']['Authorization'] === 'Bearer acn_x', 'capability body');
$check($r['headers']['Idempotency-Key'] === 'k1' && $r['headers']['Cache-Control'] === 'no-cache', 'capability idempotency and no-cache');
$check($res->capability() === 'web-search' && $res->service() === 'tavily' && $res->attempts() === 2, 'capability response headers');
$check($res->tried() === [['service' => 'serpapi', 'status' => 429], ['service' => 'tavily', 'status' => 200]], 'capability tried');
$c->capability('web-search');
$check($last()['body'] === '{}' && $last()['query'] === '' && !isset($last()['headers']['Cache-Control']), 'capability without params sends {}');

$next = new Response(200, ['x-acnologia-key-id' => '4', 'x-acnologia-attempts' => '1', 'x-acnologia-service' => 'serpapi', 'x-acnologia-cache' => 'hit', 'idempotent-replayed' => 'true'], '{}');
$res = $c->proxy('serpapi')->get('/search.json', ['q' => 'a'], [], noCache: true);
$check($last()['headers']['Cache-Control'] === 'no-cache', 'proxy get noCache');
$check($res->keyId() === 4 && $res->attempts() === 1 && $res->service() === 'serpapi' && $res->cache() === 'hit' && $res->replayed() && $res->capability() === null && $res->tried() === [], 'response accessors');
$c->proxy('serpapi')->post('/x', ['a' => 1], [], 'order-1');
$check($last()['headers']['Idempotency-Key'] === 'order-1' && !isset($last()['headers']['Cache-Control']), 'proxy post idempotencyKey');
$c->proxy('serpapi')->request('PATCH', '/x', ['body' => ['a' => 1], 'idempotencyKey' => 'p1', 'noCache' => true]);
$check($last()['headers']['Idempotency-Key'] === 'p1' && $last()['headers']['Cache-Control'] === 'no-cache', 'proxy request options');
$c->proxy('serpapi')->post('/x', null, ['Idempotency-Key' => 'mine'], 'other');
$check($last()['headers']['Idempotency-Key'] === 'mine' && !isset($last()['headers']['idempotency-key']), 'caller Idempotency-Key wins');
$noKey = (new Response(200, [], ''));
$check($noKey->keyId() === null && $noKey->replayed() === false && $noKey->service() === null, 'accessors absent');

$forward = ['proxy_url' => 'http://acn.test:8888', 'proxy_url_with_token' => 'http://app:%3Capp%20token%3E@acn.test:8888',
    'ca_url' => 'http://acn.test/api/v1/proxy/ca.pem', 'ca_fingerprint' => 'ab:cd', 'passthrough' => true,
    'hosts' => [['host' => 'api.openai.com', 'service' => 'openai', 'name' => 'OpenAI', 'path' => '/v1']],
    'env' => ['NODE_USE_ENV_PROXY' => '1'], 'service_header' => 'X-Acnologia-Service'];
$next = $jsonRes($forward);
$fp = $c->forwardProxy();
$check($last()['method'] === 'GET' && $last()['path'] === '/api/v1/proxy' && $last()['headers']['Authorization'] === 'Bearer acn_x', 'forwardProxy request');
$check($fp['proxy_url'] === 'http://acn.test:8888' && $fp['passthrough'] === true && $fp['hosts'][0]['host'] === 'api.openai.com' && $fp['service_header'] === 'X-Acnologia-Service', 'forwardProxy parsed');
$check(!str_contains(json_encode($fp), 'acn_x'), 'forwardProxy has no token');

$enc = new Acnologia('http://acn.test', 'acn_a/b+c', 30, $script);
$check($enc->forwardProxyURL() === 'http://app:acn_a%2Fb%2Bc@acn.test:8888', 'forwardProxyURL escapes the token');
$env = $c->forwardProxyEnv();
$url = 'http://app:acn_x@acn.test:8888';
$check($env === ['HTTPS_PROXY' => $url, 'HTTP_PROXY' => $url, 'NODE_USE_ENV_PROXY' => '1', 'NODE_EXTRA_CA_CERTS' => 'acnologia-ca.pem',
    'REQUESTS_CA_BUNDLE' => 'acnologia-ca.pem', 'SSL_CERT_FILE' => 'acnologia-ca.pem'], 'forwardProxyEnv');
$check($c->forwardProxyEnv('/etc/acn.pem')['SSL_CERT_FILE'] === '/etc/acn.pem', 'forwardProxyEnv caPath');

$pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";
$next = new Response(200, ['content-type' => 'application/x-pem-file'], $pem);
$check($c->forwardProxyCA() === $pem, 'forwardProxyCA body');
$check($last()['method'] === 'GET' && $last()['path'] === '/api/v1/proxy/ca.pem' && !isset($last()['headers']['Authorization']), 'forwardProxyCA sends no token');
$tmp = tempnam(sys_get_temp_dir(), 'acn');
$check($c->saveForwardProxyCA($tmp) === $tmp && file_get_contents($tmp) === $pem, 'saveForwardProxyCA writes the file');
unlink($tmp);

$locked = new Acnologia('http://acn.test', 'acn_x', 30, $script, 'gatepw');
$next = $jsonRes([]);
$locked->me();
$check($last()['headers']['X-Acnologia-Access'] === 'gatepw', 'access password on API calls');
$locked->proxy('serpapi')->get('/x');
$check($last()['headers']['X-Acnologia-Access'] === 'gatepw', 'access password on proxy calls');
$c->me();
$check(!isset($last()['headers']['X-Acnologia-Access']), 'no access header by default');
$check(!str_contains(print_r($locked, true), 'gatepw') && !str_contains(print_r($locked, true), 'acn_x'), 'print_r hides secrets');

$next = $jsonRes(['error' => ['code' => 'app_limit_reached', 'message' => 'daily limit'], 'limit' => 'daily_requests', 'reset_at' => '2026-11-05T00:00:00Z', 'retry_after_sec' => 30], 429);
try {
    $c->capabilities();
    $check(false, 'app limit reached');
} catch (AppLimitReached $e) {
    $check($e->statusCode === 429 && $e->errorCode === 'app_limit_reached' && $e->limit === 'daily_requests' && $e->resetAt === '2026-11-05T00:00:00Z' && $e->retryAfter === 30, 'app limit reached fields');
}
$next = $jsonRes(['error' => ['code' => 'app_limit_reached', 'message' => 'monthly']], 429, ['retry-after' => '12']);
try {
    $c->me();
} catch (AppLimitReached $e) {
    $check($e->retryAfter === 12, 'app limit reached falls back to Retry-After');
}
$next = $jsonRes(['error' => ['code' => 'share_paused', 'message' => 'paused']], 403, ['x-acnologia-share' => 'paused']);
try {
    $c->forwardProxy();
    $check(false, 'share unavailable');
} catch (ShareUnavailable $e) {
    $check($e->statusCode === 403 && $e->errorCode === 'share_paused' && $e->shareStatus === 'paused', 'share unavailable fields');
}
$next = new Response(403, ['x-acnologia-share' => 'revoked'], '');
try {
    $c->services();
} catch (ShareUnavailable $e) {
    $check($e->shareStatus === 'revoked', 'share status from the header alone');
}
$next = $jsonRes(['error' => ['code' => 'no_route', 'message' => 'no provider for acn_x']], 404);
try {
    $c->guide('x');
} catch (AcnologiaException $e) {
    $check($e->errorCode === 'no_route' && !str_contains($e->getMessage(), 'acn_x'), 'other codes stay generic and scrub the token');
}

exit($fails ? 1 : 0);
