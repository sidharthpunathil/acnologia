<?php

declare(strict_types=1);

namespace Acnologia;

use Acnologia\Exception\AllKeysBusy;
use Acnologia\Exception\AcnologiaException;
use Acnologia\Exception\AppLimitReached;
use Acnologia\Exception\KeyProblem;
use Acnologia\Exception\NoKeyAvailable;
use Acnologia\Exception\ServiceUnavailable;
use Acnologia\Exception\ShareUnavailable;

/**
 * Client for the Acnologia consumer API.
 *
 *     $acn = new Acnologia\Acnologia('http://localhost:8080', 'acn_...');
 *     $res = $acn->proxy('serpapi')->get('/search.json', ['q' => 'coffee']);
 */
final class Acnologia
{
    public const VERSION = '0.2.0';

    /** Longest wait_sec the server accepts. */
    public const MAX_WAIT_SEC = 60;

    /** Cap on a report message built from an exception. */
    public const MAX_REPORT_MESSAGE = 500;

    /** Header that carries the access password of a locked deploy. */
    public const ACCESS_HEADER = 'X-Acnologia-Access';

    /** Where saveForwardProxyCA() and forwardProxyEnv() put the proxy CA by default. */
    public const DEFAULT_CA_PATH = 'acnologia-ca.pem';

    public readonly string $baseUrl;

    /** @var callable(string, string, array<string, string>, ?string): Response */
    private $transport;

    /** Per call curl timeout override, used while a lease waits server side. */
    private ?int $callTimeout = null;

    /** Sent as X-Acnologia-Access on every request when set. */
    private ?string $accessPassword;

    /**
     * @param null|callable(string $method, string $url, array<string, string> $headers, ?string $body): Response $transport
     *        override the HTTP layer (tests, custom clients). Defaults to curl.
     * @param ?string $accessPassword the access password of a locked deploy, sent as
     *        X-Acnologia-Access on every request. The consumer routes pass the lock with the
     *        app token alone, so this is rarely needed.
     */
    public function __construct(
        string $baseUrl,
        public readonly string $token,
        public readonly int $timeout = 30,
        ?callable $transport = null,
        ?string $accessPassword = null,
    ) {
        if ($baseUrl === '' || $token === '') {
            throw new \InvalidArgumentException('baseUrl and token are required');
        }
        $this->baseUrl = rtrim($baseUrl, '/');
        $this->transport = $transport ?? $this->curlTransport(...);
        $this->accessPassword = $accessPassword === '' ? null : $accessPassword;
    }

    /** Keep the token and the access password out of var_dump and print_r. */
    public function __debugInfo(): array
    {
        return [
            'baseUrl' => $this->baseUrl,
            'token' => '***',
            'timeout' => $this->timeout,
            'accessPassword' => $this->accessPassword === null ? null : '***',
        ];
    }

    /**
     * The token's own access, from GET /api/v1/me: app {id, name}, limits
     * {daily_requests, monthly_requests, daily_budget, monthly_budget} (each nullable),
     * usage {today_requests, today_cost, month_requests, month_cost, last_used_at,
     * limit_hit}, limit_hit, services [{slug, name, provider, category, available (an
     * int: free keys for a share's token, 1 or 0 for an app), logo_url}] and share (null for an ordinary app). A share token whose share stopped
     * still gets an answer, with share.status "paused", "expired" or "revoked".
     *
     * @return array<string, mixed>
     */
    public function me(): array
    {
        return $this->call('GET', '/api/v1/me');
    }

    /** @return list<array{slug: string, name: string, provider: string, category: string, available: bool, base_url: string, auth: array{type: string, name: string, prefix?: string}, guide_url?: string}> */
    public function services(): array
    {
        return $this->call('GET', '/api/v1/services')['services'] ?? [];
    }

    /**
     * The usage guide of a service this token may use, from
     * GET /api/v1/services/{slug}/guide: markdown, generated (true when the server built it
     * from the provider spec) and guide (the structured guide, decoded as arrays).
     *
     * @return array{markdown: string, generated: bool, guide: mixed}
     */
    public function guide(string $service): array
    {
        $data = $this->call('GET', '/api/v1/services/' . rawurlencode($service) . '/guide');
        return [
            'markdown' => (string) ($data['markdown'] ?? ''),
            'generated' => ($data['generated'] ?? false) === true,
            'guide' => $data['guide'] ?? null,
        ];
    }

    /** The usage guide of a service as Markdown (the same route with Accept: text/markdown). */
    public function guideMarkdown(string $service): string
    {
        $res = $this->raw('GET', '/api/v1/services/' . rawurlencode($service) . '/guide', null, 'text/markdown');
        if (!$res->ok()) {
            throw $this->errorFor($res);
        }
        return $res->body;
    }

    /**
     * Capabilities this token may call, from GET /api/v1/capabilities. A share's token
     * gets none.
     *
     * @return list<array{slug: string, name: string, description: string, strategy: string, params: mixed, routes: list<array{service: string, mapped: bool}>, url: string}>
     */
    public function capabilities(): array
    {
        return $this->call('GET', '/api/v1/capabilities')['capabilities'] ?? [];
    }

    /**
     * Call a capability: POST /proxy/{slug} with $params as the JSON body. $service uses
     * only that provider (?_service=), $raw returns the provider's own body instead of the
     * mapped output (?_raw=1). Like a proxy call it returns the Response and never throws
     * for a non 2xx; see Response::capability(), service() and tried().
     *
     * @param array<string, mixed>|object $params
     * @param array<string, string> $headers
     */
    public function capability(
        string $slug,
        array|object $params = [],
        ?string $service = null,
        bool $raw = false,
        array $headers = [],
        ?string $idempotencyKey = null,
        bool $noCache = false,
    ): Response {
        $query = [];
        if ($service !== null && $service !== '') {
            $query['_service'] = $service;
        }
        if ($raw) {
            $query['_raw'] = '1';
        }
        $url = $this->baseUrl . '/proxy/' . rawurlencode($slug) . ($query ? '?' . http_build_query($query) : '');
        // an empty params array is a JSON object, not []
        $body = $params === [] ? new \stdClass() : $params;
        return Proxy::sendTo($this, 'POST', $url, [
            'body' => $body,
            'headers' => $headers,
            'idempotencyKey' => $idempotencyKey,
            'noCache' => $noCache,
        ]);
    }

    /**
     * How to use the forward proxy, from GET /api/v1/proxy: proxy_url,
     * proxy_url_with_token, ca_url, ca_fingerprint, passthrough, hosts [{host, service,
     * name, path}], env and service_header. proxy_url_with_token and env carry a
     * "<app token>" placeholder, never the token, so the array is safe to log.
     *
     * @return array<string, mixed>
     */
    public function forwardProxy(): array
    {
        return $this->call('GET', '/api/v1/proxy');
    }

    /** The forward proxy's CA certificate as PEM text, from GET /api/v1/proxy/ca.pem. The route is public: no token is sent. */
    public function forwardProxyCA(): string
    {
        $res = $this->send('GET', $this->baseUrl . '/api/v1/proxy/ca.pem', ['Accept' => 'application/x-pem-file'], null);
        if (!$res->ok()) {
            throw $this->errorFor($res);
        }
        return $res->body;
    }

    /** Download the forward proxy's CA certificate, write it to $path and return $path. */
    public function saveForwardProxyCA(string $path = self::DEFAULT_CA_PATH): string
    {
        $pem = $this->forwardProxyCA();
        if (file_put_contents($path, $pem) === false) {
            throw new AcnologiaException('could not write ' . $path);
        }
        return $path;
    }

    /**
     * The forward proxy URL with user "app" and this client's token as the password,
     * percent escaped: http://app:acn_...@host:8888. It contains the token: keep it out
     * of logs.
     */
    public function forwardProxyURL(): string
    {
        $raw = (string) ($this->forwardProxy()['proxy_url'] ?? '');
        $u = parse_url($raw);
        if ($u === false || !isset($u['host'])) {
            throw new AcnologiaException('the server sent no usable proxy_url');
        }
        $out = ($u['scheme'] ?? 'http') . '://app:' . rawurlencode($this->token) . '@' . $u['host'];
        if (isset($u['port'])) {
            $out .= ':' . $u['port'];
        }
        return $out . ($u['path'] ?? '');
    }

    /**
     * Environment for a process that should reach providers through the forward proxy:
     * HTTPS_PROXY and HTTP_PROXY (forwardProxyURL()), NODE_USE_ENV_PROXY=1, and
     * NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE and SSL_CERT_FILE set to $caPath. The proxy
     * values contain the token: keep them out of logs. Save the CA there with
     * saveForwardProxyCA().
     *
     * @return array<string, string>
     */
    public function forwardProxyEnv(string $caPath = self::DEFAULT_CA_PATH): array
    {
        $url = $this->forwardProxyURL();
        return [
            'HTTPS_PROXY' => $url,
            'HTTP_PROXY' => $url,
            'NODE_USE_ENV_PROXY' => '1',
            'NODE_EXTRA_CA_CERTS' => $caPath,
            'REQUESTS_CA_BUNDLE' => $caPath,
            'SSL_CERT_FILE' => $caPath,
        ];
    }

    /**
     * Borrow a key.
     *
     * $waitSec (0 to 60) lets the server wait that long for a free slot when every
     * usable key is at its concurrency limit. Throws AllKeysBusy (a NoKeyAvailable)
     * if none frees up in time, and NoKeyAvailable when the pool is drained.
     *
     * Always report the lease: on services with a concurrency limit it holds one of
     * the key's slots until it is reported or its TTL expires.
     *
     * $exclusive = true keeps the key from every other caller until the lease is
     * reported or expires.
     *
     * @param list<int> $excludeKeyIds
     */
    public function lease(
        string $service,
        array $excludeKeyIds = [],
        ?int $ttlSec = null,
        ?int $waitSec = null,
        bool $exclusive = false,
    ): Lease {
        $body = ['service' => $service];
        if ($excludeKeyIds) {
            $body['exclude_key_ids'] = array_values($excludeKeyIds);
        }
        if ($ttlSec !== null) {
            $body['ttl_sec'] = $ttlSec;
        }
        if ($waitSec !== null && $waitSec !== 0) {
            if ($waitSec < 0 || $waitSec > self::MAX_WAIT_SEC) {
                throw new \InvalidArgumentException('waitSec must be between 0 and ' . self::MAX_WAIT_SEC);
            }
            $body['wait_sec'] = $waitSec;
            $this->callTimeout = max($this->timeout, $waitSec + 10);
        }
        if ($exclusive) {
            $body['exclusive'] = true;
        }
        try {
            return Lease::fromArray($this, $this->call('POST', '/api/v1/lease', $body));
        } finally {
            $this->callTimeout = null;
        }
    }

    /** @internal used by Lease::report() */
    public function reportLease(
        string $leaseId,
        string $outcome,
        ?int $statusCode,
        ?int $retryAfterSec,
        ?string $message,
        bool $rotate,
    ): ?Lease {
        $body = ['outcome' => $outcome, 'rotate' => $rotate];
        if ($statusCode !== null) {
            $body['status_code'] = $statusCode;
        }
        if ($retryAfterSec !== null) {
            $body['retry_after_sec'] = $retryAfterSec;
        }
        if ($message !== null && $message !== '') {
            $body['message'] = $message;
        }
        $data = $this->call('POST', '/api/v1/leases/' . rawurlencode($leaseId) . '/report', $body);
        return is_array($data['next'] ?? null) ? Lease::fromArray($this, $data['next']) : null;
    }

    /**
     * Extend an open lease so a long job keeps its key. A null (or non positive)
     * $ttlSec uses the server default (120 seconds); the server caps it at 24 hours.
     * Returns the new expires_at. Prefer Lease::renew(), which also updates
     * $lease->expiresAt. Throws AcnologiaException with errorCode lease_not_found
     * when the lease is no longer open.
     */
    public function renew(Lease|string $lease, ?int $ttlSec = null): string
    {
        $id = $lease instanceof Lease ? $lease->leaseId : $lease;
        $body = [];
        if ($ttlSec !== null && $ttlSec > 0) {
            $body['ttl_sec'] = $ttlSec;
        }
        $data = $this->call('POST', '/api/v1/leases/' . rawurlencode($id) . '/renew', $body);
        $exp = $data['expires_at'] ?? null;
        if (!is_string($exp) || $exp === '') {
            throw new AcnologiaException('renew response has no expires_at');
        }
        return $exp;
    }

    /**
     * Lease a key, call $fn($lease), report the result and return what $fn returned.
     * Throw RateLimited, QuotaExhausted or InvalidKey from $fn to rotate to the next key.
     * Any other exception is reported as error and rethrown.
     *
     * Every lease is reported, whatever $fn does. On services with a concurrency limit an
     * unreported lease keeps one of the key's slots busy until its TTL expires. $ttlSec,
     * $waitSec and $exclusive are passed to every lease call.
     *
     * @template T
     * @param callable(Lease): T $fn
     * @return T
     */
    public function withKey(
        string $service,
        callable $fn,
        int $maxAttempts = 5,
        ?int $ttlSec = null,
        ?int $waitSec = null,
        bool $exclusive = false,
    ): mixed
    {
        if ($maxAttempts < 1) {
            throw new \InvalidArgumentException('maxAttempts must be at least 1');
        }
        $tried = [];
        $lease = $this->lease($service, [], $ttlSec, $waitSec, $exclusive);
        for ($attempt = 1; ; $attempt++) {
            $tried[] = $lease->keyId;
            try {
                $result = $fn($lease);
            } catch (KeyProblem $e) {
                $last = $attempt >= $maxAttempts;
                $message = self::scrubMessage($e->getMessage(), $lease->secret);
                $next = $lease->report($e->outcome(), $e->statusCode, $e->retryAfter, $message, !$last);
                if ($last) {
                    throw $e;
                }
                $lease = $next ?? $this->lease($service, $tried, $ttlSec, $waitSec, $exclusive);
                continue;
            } catch (\Throwable $e) {
                try {
                    $lease->report('error', null, null, self::scrubMessage(get_class($e) . ': ' . $e->getMessage(), $lease->secret));
                } catch (\Throwable) {
                    // never mask the caller's exception
                }
                throw $e;
            }
            $lease->report('ok');
            return $result;
        }
    }

    /**
     * Replace the lease secret (raw and URL encoded) with "[key]" and truncate to
     * MAX_REPORT_MESSAGE bytes. Errors from query auth services often carry the full
     * request URL, key included, and a report must not copy it to the server.
     */
    public static function scrubMessage(string $message, string $secret): string
    {
        if ($secret !== '') {
            $message = str_replace([$secret, urlencode($secret), rawurlencode($secret)], '[key]', $message);
        }
        if (strlen($message) > self::MAX_REPORT_MESSAGE) {
            $message = substr($message, 0, self::MAX_REPORT_MESSAGE);
            if (preg_match('//u', $message) !== 1) {
                // the cut split a multibyte character; drop the partial tail
                $message = (string) preg_replace('/[\xC0-\xFF][\x80-\xBF]*$/', '', $message);
            }
        }
        return $message;
    }

    public function proxy(string $service): Proxy
    {
        return new Proxy($this, $service);
    }

    /** @param array<string, mixed>|null $json */
    private function raw(string $method, string $path, ?array $json, string $accept = 'application/json'): Response
    {
        $headers = [
            'Authorization' => 'Bearer ' . $this->token,
            'Accept' => $accept,
        ];
        $body = null;
        if ($json !== null) {
            $headers['Content-Type'] = 'application/json';
            // an empty body is a JSON object, not []
            $body = $json === [] ? '{}' : json_encode($json, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
        }
        return $this->send($method, $this->baseUrl . $path, $headers, $body);
    }

    /**
     * @param array<string, mixed>|null $json
     * @return array<string, mixed>
     */
    private function call(string $method, string $path, ?array $json = null): array
    {
        $res = $this->raw($method, $path, $json);
        if (!$res->ok()) {
            throw $this->errorFor($res);
        }
        $data = $res->body !== '' ? json_decode($res->body, true) : null;
        return is_array($data) ? $data : [];
    }

    /** The typed exception for a non 2xx answer from the consumer API. */
    private function errorFor(Response $res): AcnologiaException
    {
        $data = $res->body !== '' ? json_decode($res->body, true) : null;
        $err = is_array($data) && is_array($data['error'] ?? null) ? $data['error'] : [];
        $code = (string) ($err['code'] ?? '');
        $message = (string) ($err['message'] ?? ('HTTP ' . $res->status));
        foreach ([$this->token, $this->accessPassword] as $secret) {
            if ($secret !== null && $secret !== '') {
                $message = str_replace([$secret, rawurlencode($secret)], '[key]', $message);
            }
        }
        $retry = is_array($data) && isset($data['retry_after_sec']) ? (int) $data['retry_after_sec'] : null;
        $share = strtolower(trim((string) $res->header('X-Acnologia-Share')));
        if ($code === '' && $res->status === 403 && in_array($share, ['revoked', 'paused', 'expired'], true)) {
            $code = 'share_' . $share;
        }
        if ($code === 'service_unavailable') {
            return new ServiceUnavailable($message, $retry ?? self::headerRetryAfter($res), $res->status);
        }
        if ($code === 'app_limit_reached') {
            return new AppLimitReached(
                $message,
                is_string($data['limit'] ?? null) ? $data['limit'] : null,
                is_string($data['reset_at'] ?? null) ? $data['reset_at'] : null,
                $retry ?? self::headerRetryAfter($res),
            );
        }
        if (in_array($code, ['share_revoked', 'share_paused', 'share_expired'], true)) {
            return new ShareUnavailable($message, substr($code, strlen('share_')));
        }
        if ($code === 'all_keys_busy') {
            return new AllKeysBusy($message, $retry);
        }
        if ($code === 'no_key_available' || ($res->status === 503 && $code === '')) {
            return new NoKeyAvailable($message, $retry);
        }
        return new AcnologiaException($message, $res->status, $code);
    }

    private static function headerRetryAfter(Response $res): ?int
    {
        $h = trim((string) $res->header('Retry-After'));
        return ctype_digit($h) && (int) $h > 0 ? (int) $h : null;
    }

    /**
     * @internal
     * @param array<string, string> $headers
     */
    public function send(string $method, string $url, array $headers, ?string $body): Response
    {
        $headers['User-Agent'] = 'acnologia-php/' . self::VERSION;
        if ($this->accessPassword !== null) {
            $headers[self::ACCESS_HEADER] = $this->accessPassword;
        }
        return ($this->transport)($method, $url, $headers, $body);
    }

    /** @param array<string, string> $headers */
    private function curlTransport(string $method, string $url, array $headers, ?string $body): Response
    {
        $ch = curl_init($url);
        $lines = [];
        foreach ($headers as $k => $v) {
            $lines[] = $k . ': ' . $v;
        }
        $respHeaders = [];
        curl_setopt_array($ch, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_HTTPHEADER => $lines,
            CURLOPT_TIMEOUT => $this->callTimeout ?? $this->timeout,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_HEADERFUNCTION => static function ($ch, string $line) use (&$respHeaders): int {
                $parts = explode(':', $line, 2);
                if (count($parts) === 2) {
                    $respHeaders[strtolower(trim($parts[0]))] = trim($parts[1]);
                }
                return strlen($line);
            },
        ]);
        if ($body !== null) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, $body);
        }
        $out = curl_exec($ch);
        if ($out === false) {
            $msg = curl_error($ch);
            curl_close($ch);
            throw new AcnologiaException('request failed: ' . $msg);
        }
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);
        return new Response($status, $respHeaders, (string) $out);
    }
}
