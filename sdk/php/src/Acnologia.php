<?php

declare(strict_types=1);

namespace Acnologia;

use Acnologia\Exception\AllKeysBusy;
use Acnologia\Exception\AcnologiaException;
use Acnologia\Exception\KeyProblem;
use Acnologia\Exception\NoKeyAvailable;

/**
 * Client for the Acnologia consumer API.
 *
 *     $acn = new Acnologia\Acnologia('http://localhost:8080', 'acn_...');
 *     $res = $acn->proxy('serpapi')->get('/search.json', ['q' => 'coffee']);
 */
final class Acnologia
{
    public const VERSION = '0.1.0';

    /** Longest wait_sec the server accepts. */
    public const MAX_WAIT_SEC = 60;

    /** Cap on a report message built from an exception. */
    public const MAX_REPORT_MESSAGE = 500;

    public readonly string $baseUrl;

    /** @var callable(string, string, array<string, string>, ?string): Response */
    private $transport;

    /** Per call curl timeout override, used while a lease waits server side. */
    private ?int $callTimeout = null;

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

    /** @return list<array{slug: string, name: string, provider: string, category: string, available: bool, base_url: string, auth: array{type: string, name: string, prefix?: string}}> */
    public function services(): array
    {
        return $this->call('GET', '/api/v1/services')['services'] ?? [];
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
     * @param list<int> $excludeKeyIds
     */
    public function lease(string $service, array $excludeKeyIds = [], ?int $ttlSec = null, ?int $waitSec = null): Lease
    {
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
     * Lease a key, call $fn($lease), report the result and return what $fn returned.
     * Throw RateLimited, QuotaExhausted or InvalidKey from $fn to rotate to the next key.
     * Any other exception is reported as error and rethrown.
     *
     * Every lease is reported, whatever $fn does. On services with a concurrency limit an
     * unreported lease keeps one of the key's slots busy until its TTL expires. $waitSec is
     * passed to every lease call.
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
    ): mixed
    {
        if ($maxAttempts < 1) {
            throw new \InvalidArgumentException('maxAttempts must be at least 1');
        }
        $tried = [];
        $lease = $this->lease($service, [], $ttlSec, $waitSec);
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
                $lease = $next ?? $this->lease($service, $tried, $ttlSec, $waitSec);
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
            $retry = is_array($data) && isset($data['retry_after_sec']) ? (int) $data['retry_after_sec'] : null;
            if ($code === 'all_keys_busy') {
                throw new AllKeysBusy($message, $retry);
            }
            if ($code === 'no_key_available' || ($res->status === 503 && $code === '')) {
                throw new NoKeyAvailable($message, $retry);
            }
            throw new AcnologiaException($message, $res->status, $code);
        }
        return is_array($data) ? $data : [];
    }

    /**
     * @internal
     * @param array<string, string> $headers
     */
    public function send(string $method, string $url, array $headers, ?string $body): Response
    {
        $headers['User-Agent'] = 'acnologia-php/' . self::VERSION;
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
