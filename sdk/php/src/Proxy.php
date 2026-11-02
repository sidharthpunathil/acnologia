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

    /** @param array<string, string> $headers */
    public function get(string $path, array $query = [], array $headers = []): Response
    {
        return $this->request('GET', $path, ['query' => $query, 'headers' => $headers]);
    }

    /**
     * @param mixed $body array/object is sent as JSON, string is sent as is
     * @param array<string, string> $headers
     */
    public function post(string $path, mixed $body = null, array $headers = []): Response
    {
        return $this->request('POST', $path, ['body' => $body, 'headers' => $headers]);
    }

    public function put(string $path, mixed $body = null, array $headers = []): Response
    {
        return $this->request('PUT', $path, ['body' => $body, 'headers' => $headers]);
    }

    public function delete(string $path, array $query = [], array $headers = []): Response
    {
        return $this->request('DELETE', $path, ['query' => $query, 'headers' => $headers]);
    }

    /**
     * @param array{query?: array<string, mixed>, headers?: array<string, string>, body?: mixed} $options
     */
    public function request(string $method, string $path, array $options = []): Response
    {
        $headers = $options['headers'] ?? [];
        $body = $options['body'] ?? null;
        if ($body !== null && !is_string($body)) {
            $body = json_encode($body, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
            if (!self::hasHeader($headers, 'content-type')) {
                $headers['Content-Type'] = 'application/json';
            }
        }
        $headers['Authorization'] = 'Bearer ' . $this->client->token;
        return $this->client->send(strtoupper($method), $this->url($path, $options['query'] ?? []), $headers, $body);
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
