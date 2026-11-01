<?php

// Dependency free smoke tests: php tests/run.php
declare(strict_types=1);

spl_autoload_register(static function (string $class): void {
    if (str_starts_with($class, 'Cogito\\')) {
        require __DIR__ . '/../src/' . str_replace('\\', '/', substr($class, 7)) . '.php';
    }
});

use Cogito\Cogito;
use Cogito\Exception\NoKeyAvailable;
use Cogito\Exception\RateLimited;
use Cogito\Response;

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
$transport = static function (string $method, string $url, array $headers, ?string $body) use (&$log, &$bad, &$leases, $lease): Response {
    $log[] = [$method, $url, $headers, $body];
    $path = parse_url($url, PHP_URL_PATH);
    $json = $body !== null ? json_decode($body, true) : null;
    if ($path === '/api/v1/lease') {
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
    return new Response(200, ['x-cogito-key-id' => '1'], json_encode(['url' => $url, 'body' => $body]));
};

$fails = 0;
$check = static function (bool $cond, string $name) use (&$fails): void {
    echo ($cond ? 'ok   ' : 'FAIL ') . $name . PHP_EOL;
    $fails += $cond ? 0 : 1;
};

$cog = new Cogito('http://cogito.test/', 'cog_x', 30, $transport);

$res = $cog->proxy('serpapi')->get('/search.json', ['q' => 'coffee']);
$check($res->json()['url'] === 'http://cogito.test/proxy/serpapi/search.json?q=coffee', 'proxy get url');
$check($log[0][2]['Authorization'] === 'Bearer cog_x', 'proxy sends token');
$res = $cog->proxy('elevenlabs')->post('v1/text-to-speech/v', ['text' => 'hi']);
$check($res->json()['body'] === '{"text":"hi"}', 'proxy post json');

$l = $cog->lease('serpapi');
$check($l->apply(['q' => 'a'])['query'] === ['q' => 'a', 'api_key' => 's1'], 'apply query');
$check($l->url('/search.json', ['q' => 'a']) === 'https://serpapi.com/search.json?q=a', 'lease url');
$l->report('ok');

$seen = [];
$out = $cog->withKey('serpapi', static function ($lease) use (&$seen) {
    $seen[] = $lease->keyId;
    if ($lease->keyId === 1) {
        throw RateLimited::after(60);
    }
    return 'done';
});
$check($out === 'done' && $seen === [1, 2], 'withKey rotates');

$keys = [];
try {
    $cog->lease('serpapi');
    $check(false, 'no key available');
} catch (NoKeyAvailable $e) {
    $check($e->retryAfter === 9, 'no key available');
}

exit($fails ? 1 : 0);
