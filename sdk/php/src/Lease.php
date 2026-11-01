<?php

declare(strict_types=1);

namespace Cogito;

/** A key handed out by Cogito. Report how it went with report(). */
final class Lease
{
    public bool $reported = false;

    /**
     * @param array{type: string, name: string} $auth
     */
    public function __construct(
        private readonly Cogito $client,
        public readonly string $leaseId,
        public readonly int $keyId,
        public readonly string $service,
        public readonly string $secret,
        public readonly array $auth,
        public readonly string $baseUrl,
        public readonly ?string $expiresAt,
    ) {
    }

    /** @param array<string, mixed> $data */
    public static function fromArray(Cogito $client, array $data): self
    {
        $auth = is_array($data['auth'] ?? null) ? $data['auth'] : [];
        return new self(
            $client,
            (string) ($data['lease_id'] ?? ''),
            (int) ($data['key_id'] ?? 0),
            (string) ($data['service'] ?? ''),
            (string) ($data['secret'] ?? ''),
            ['type' => (string) ($auth['type'] ?? ''), 'name' => (string) ($auth['name'] ?? '')],
            (string) ($data['base_url'] ?? ''),
            isset($data['expires_at']) ? (string) $data['expires_at'] : null,
        );
    }

    /**
     * Report the outcome: ok, rate_limited, quota_exhausted, invalid or error.
     * Returns the next Lease when $rotate is true and another key is free.
     */
    public function report(
        string $outcome,
        ?int $statusCode = null,
        ?int $retryAfterSec = null,
        ?string $message = null,
        bool $rotate = false,
    ): ?Lease {
        $next = $this->client->reportLease($this->leaseId, $outcome, $statusCode, $retryAfterSec, $message, $rotate);
        $this->reported = true;
        return $next;
    }

    /**
     * Add the secret where the provider expects it and return both arrays.
     * query: $query[auth.name]; header: $headers[auth.name]; bearer: $headers[auth.name or Authorization] = "Bearer ...".
     *
     * @param array<string, mixed> $query
     * @param array<string, string> $headers
     * @return array{query: array<string, mixed>, headers: array<string, string>}
     */
    public function apply(array $query = [], array $headers = []): array
    {
        $name = $this->auth['name'];
        switch ($this->auth['type']) {
            case 'query':
                $query[$name] = $this->secret;
                break;
            case 'header':
                $headers[$name] = $this->secret;
                break;
            case 'bearer':
                $headers[$name !== '' ? $name : 'Authorization'] = 'Bearer ' . $this->secret;
                break;
            default:
                throw new \InvalidArgumentException('unknown auth type ' . $this->auth['type']);
        }
        return ['query' => $query, 'headers' => $headers];
    }

    /** Join the service base_url and a path, with an optional query string. */
    public function url(string $path = '', array $query = []): string
    {
        $url = rtrim($this->baseUrl, '/') . '/' . ltrim($path, '/');
        return $query ? $url . '?' . http_build_query($query) : $url;
    }

    public function __debugInfo(): array
    {
        return ['leaseId' => $this->leaseId, 'keyId' => $this->keyId, 'service' => $this->service, 'secret' => '***'];
    }
}
