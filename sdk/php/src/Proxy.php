<?php

declare(strict_types=1);

namespace Acnologia;

/**
 * Sends requests through /proxy/{service}/. Acnologia injects a key and rotates on
 * rate limits. Non 2xx responses are returned, never thrown.
 */
final class Proxy
{
    public function __construct(private readonly Acnologia $client, public readonly string $service)
    {
    }

    public function url(string $path = '', array $query = []): string
    {
        $url = $this->client->baseUrl . '/proxy/' . rawurlencode($this->service) . '/' . ltrim($path, '/');
        return $query ? $url . (str_contains($url, '?') ? '&' : '?') . http_build_query($query) : $url;
    }

    /**
     * $noCache sends Cache-Control: no-cache, so a service with a cache TTL calls the
     * provider instead of answering from its cache.
     *
     * @param array<string, string> $headers
     */
    public function get(string $path, array $query = [], array $headers = [], bool $noCache = false): Response
    {
        return $this->request('GET', $path, ['query' => $query, 'headers' => $headers, 'noCache' => $noCache]);
    }

    /**
     * $idempotencyKey is sent as Idempotency-Key: the proxy answers a repeat of the
     * same request with the stored response for 10 minutes (Response::replayed()).
     *
     * @param mixed $body array/object is sent as JSON, string is sent as is
     * @param array<string, string> $headers
     */
    public function post(
        string $path,
        mixed $body = null,
        array $headers = [],
        ?string $idempotencyKey = null,
        bool $noCache = false,
    ): Response {
        return $this->request('POST', $path, ['body' => $body, 'headers' => $headers, 'idempotencyKey' => $idempotencyKey, 'noCache' => $noCache]);
    }

    /** @param array<string, string> $headers */
    public function put(
        string $path,
        mixed $body = null,
        array $headers = [],
        ?string $idempotencyKey = null,
        bool $noCache = false,
    ): Response {
        return $this->request('PUT', $path, ['body' => $body, 'headers' => $headers, 'idempotencyKey' => $idempotencyKey, 'noCache' => $noCache]);
    }

    /** @param array<string, string> $headers */
    public function delete(string $path, array $query = [], array $headers = [], bool $noCache = false): Response
    {
        return $this->request('DELETE', $path, ['query' => $query, 'headers' => $headers, 'noCache' => $noCache]);
    }

    /**
     * @param array{query?: array<string, mixed>, headers?: array<string, string>, body?: mixed, idempotencyKey?: ?string, noCache?: bool} $options
     */
    public function request(string $method, string $path, array $options = []): Response
    {
        return self::sendTo($this->client, $method, $this->url($path, $options['query'] ?? []), $options);
    }

    /**
     * @internal Send to a full proxy URL with the same body and header rules as request().
     * @param array{headers?: array<string, string>, body?: mixed, idempotencyKey?: ?string, noCache?: bool} $options
     */
    public static function sendTo(Acnologia $client, string $method, string $url, array $options = []): Response
    {
        $headers = $options['headers'] ?? [];
        $body = $options['body'] ?? null;
        if ($body !== null && !is_string($body)) {
            $body = json_encode($body, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
            if (!self::hasHeader($headers, 'content-type')) {
                $headers['Content-Type'] = 'application/json';
            }
        }
        $key = $options['idempotencyKey'] ?? null;
        if ($key !== null && $key !== '' && !self::hasHeader($headers, 'idempotency-key')) {
            $headers['Idempotency-Key'] = $key;
        }
        if (($options['noCache'] ?? false) && !self::hasHeader($headers, 'cache-control')) {
            $headers['Cache-Control'] = 'no-cache';
        }
        $headers['Authorization'] = 'Bearer ' . $client->token;
        return $client->send(strtoupper($method), $url, $headers, $body);
    }

    private static function hasHeader(array $headers, string $name): bool
    {
        foreach ($headers as $k => $_) {
            if (strtolower((string) $k) === $name) {
                return true;
            }
        }
        return false;
    }
}
