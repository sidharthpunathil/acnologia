<?php

// Dependency free smoke tests: php tests/run.php
declare(strict_types=1);

spl_autoload_register(static function (string $class): void {
    if (str_starts_with($class, 'Acnologia\\')) {
        require __DIR__ . '/../src/' . str_replace('\\', '/', substr($class, 7)) . '.php';
    }
});

use Acnologia\Acnologia;
use Acnologia\Exception\AllKeysBusy;
use Acnologia\Exception\NoKeyAvailable;
use Acnologia\Exception\RateLimited;
use Acnologia\Response;

$keys = [1, 2];
$bad = [];
$log = [];
$n = 0;
$lease = static function (array $exclude) use (&$keys, &$bad, &$n): ?array {
    foreach ($keys as $k) {
        if (!in_array($k, $exclude, true) && !in_array($k, $bad, true)) {
            $n++;
            return ['lease_id' => "l$n", 'key_id' => $k, 'service' => 'serpapi', 'secret' => "s$k",
                'auth' => ['type' => 'query', 'name' => 'api_key'], 'base_url' => 'https://serpapi.com',
                'expires_at' => '2026-11-01T00:00:00Z'];
        }
    }
    return null;
};
$leases = [];
$busy = false;
$transport = static function (string $method, string $url, array $headers, ?string $body) use (&$log, &$bad, &$leases, &$busy, $lease): Response {
    $log[] = [$method, $url, $headers, $body];
    $path = parse_url($url, PHP_URL_PATH);
    $json = $body !== null ? json_decode($body, true) : null;
    if ($path === '/api/v1/lease') {
        if ($busy) {
            return new Response(503, [], '{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}');
        }
        $l = $lease($json['exclude_key_ids'] ?? []);
        if ($l === null) {
            return new Response(503, [], '{"error":{"code":"no_key_available","message":"drained"},"retry_after_sec":9}');
        }
        $leases[$l['lease_id']] = $l['key_id'];
        return new Response(200, [], json_encode($l));
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

exit($fails ? 1 : 0);
