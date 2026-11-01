<?php

declare(strict_types=1);

namespace Cogito;

use Cogito\Exception\CogitoException;
use Cogito\Exception\KeyProblem;
use Cogito\Exception\NoKeyAvailable;

/**
 * Client for the Cogito consumer API.
 *
 *     $cog = new Cogito\Cogito('http://localhost:8080', 'cog_...');
 *     $res = $cog->proxy('serpapi')->get('/search.json', ['q' => 'coffee']);
 */
final class Cogito
{
    public const VERSION = '0.1.0';

    public readonly string $baseUrl;

    /** @var callable(string, string, array<string, string>, ?string): Response */
    private $transport;

    /**
     * @param null|callable(string $method, string $url, array<string, string> $headers, ?string $body): Response $transport
     *        override the HTTP layer (tests, custom clients). Defaults to curl.
     */
    public function __construct(
        string $baseUrl,
        public readonly string $token,
        public readonly int $timeout = 30,
        ?callable $transport = null,
    ) {
        if ($baseUrl === '' || $token === '') {
            throw new \InvalidArgumentException('baseUrl and token are required');
        }
        $this->baseUrl = rtrim($baseUrl, '/');
        $this->transport = $transport ?? $this->curlTransport(...);
    }

    /** @return list<array{slug: string, name: string, provider: string, category: string, available: bool, base_url: string, auth: array{type: string, name: string}}> */
    public function services(): array
    {
        return $this->call('GET', '/api/v1/services')['services'] ?? [];
    }

    /** @param list<int> $excludeKeyIds */
    public function lease(string $service, array $excludeKeyIds = [], ?int $ttlSec = null): Lease
    {
        $body = ['service' => $service];
        if ($excludeKeyIds) {
            $body['exclude_key_ids'] = array_values($excludeKeyIds);
        }
        if ($ttlSec !== null) {
            $body['ttl_sec'] = $ttlSec;
        }
        return Lease::fromArray($this, $this->call('POST', '/api/v1/lease', $body));
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
     * Lease a key, call $fn($lease), report the result and return what $fn returned.
     * Throw RateLimited, QuotaExhausted or InvalidKey from $fn to rotate to the next key.
     * Any other exception is reported as error and rethrown.
     *
     * @template T
     * @param callable(Lease): T $fn
     * @return T
     */
    public function withKey(string $service, callable $fn, int $maxAttempts = 5, ?int $ttlSec = null): mixed
    {
        if ($maxAttempts < 1) {
            throw new \InvalidArgumentException('maxAttempts must be at least 1');
        }
        $tried = [];
        $lease = $this->lease($service, [], $ttlSec);
        for ($attempt = 1; ; $attempt++) {
            $tried[] = $lease->keyId;
            try {
                $result = $fn($lease);
            } catch (KeyProblem $e) {
                $last = $attempt >= $maxAttempts;
                $next = $lease->report($e->outcome(), $e->statusCode, $e->retryAfter, $e->getMessage(), !$last);
                if ($last) {
                    throw $e;
                }
                $lease = $next ?? $this->lease($service, $tried, $ttlSec);
                continue;
            } catch (\Throwable $e) {
                try {
                    $lease->report('error', null, null, substr(get_class($e) . ': ' . $e->getMessage(), 0, 500));
                } catch (\Throwable) {
                    // never mask the caller's exception
                }
                throw $e;
            }
            $lease->report('ok');
            return $result;
        }
    }

    public function proxy(string $service): Proxy
    {
        return new Proxy($this, $service);
    }

    /**
     * @param array<string, mixed>|null $json
     * @return array<string, mixed>
     */
    private function call(string $method, string $path, ?array $json = null): array
    {
        $headers = [
            'Authorization' => 'Bearer ' . $this->token,
            'Accept' => 'application/json',
        ];
        $body = null;
        if ($json !== null) {
            $headers['Content-Type'] = 'application/json';
            $body = json_encode($json, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
        }
        $res = $this->send($method, $this->baseUrl . $path, $headers, $body);
        $data = null;
        if ($res->body !== '') {
            $data = json_decode($res->body, true);
        }
        if (!$res->ok()) {
            $err = is_array($data) && is_array($data['error'] ?? null) ? $data['error'] : [];
            $code = (string) ($err['code'] ?? '');
            $message = (string) ($err['message'] ?? ('HTTP ' . $res->status));
            if ($code === 'no_key_available' || ($res->status === 503 && $code === '')) {
                $retry = is_array($data) && isset($data['retry_after_sec']) ? (int) $data['retry_after_sec'] : null;
                throw new NoKeyAvailable($message, $retry);
            }
            throw new CogitoException($message, $res->status, $code);
        }
        return is_array($data) ? $data : [];
    }

    /**
     * @internal
     * @param array<string, string> $headers
     */
    public function send(string $method, string $url, array $headers, ?string $body): Response
    {
        $headers['User-Agent'] = 'cogito-php/' . self::VERSION;
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
            CURLOPT_TIMEOUT => $this->timeout,
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
            throw new CogitoException('request failed: ' . $msg);
        }
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);
        return new Response($status, $respHeaders, (string) $out);
    }
}
