<?php

declare(strict_types=1);

namespace Acnologia;

/** A raw HTTP response. Header names are lower cased. */
final class Response
{
    /**
     * @param array<string, string> $headers
     */
    public function __construct(
        public readonly int $status,
        public readonly array $headers,
        public readonly string $body,
    ) {
    }

    public function ok(): bool
    {
        return $this->status >= 200 && $this->status < 300;
    }

    public function header(string $name): ?string
    {
        return $this->headers[strtolower($name)] ?? null;
    }

    /** @return mixed */
    public function json()
    {
        return json_decode($this->body, true, 512, JSON_THROW_ON_ERROR);
    }

    /** Id of the key that served a proxied request (X-Acnologia-Key-Id), or null (always null for a share's token). */
    public function keyId(): ?int
    {
        return $this->intHeader('X-Acnologia-Key-Id');
    }

    /** How many keys the proxy tried (X-Acnologia-Attempts), or null. */
    public function attempts(): ?int
    {
        return $this->intHeader('X-Acnologia-Attempts');
    }

    /** Slug of the service that answered (X-Acnologia-Service), or null. For a group or capability, the member that served it. */
    public function service(): ?string
    {
        return $this->stringHeader('X-Acnologia-Service');
    }

    /** Cache result (X-Acnologia-Cache: hit, miss, bypass...), or null. */
    public function cache(): ?string
    {
        return $this->stringHeader('X-Acnologia-Cache');
    }

    /** True when the proxy replayed a stored answer for a repeated Idempotency-Key. */
    public function replayed(): bool
    {
        return strtolower((string) $this->header('Idempotent-Replayed')) === 'true';
    }

    /** Slug of the capability that served the call (X-Acnologia-Capability), or null. */
    public function capability(): ?string
    {
        return $this->stringHeader('X-Acnologia-Capability');
    }

    /**
     * The providers a capability or group call tried, in order, from X-Acnologia-Tried.
     *
     * @return list<array{service: string, status: int}>
     */
    public function tried(): array
    {
        $out = [];
        foreach (explode(',', (string) $this->header('X-Acnologia-Tried')) as $pair) {
            $pair = trim($pair);
            if ($pair === '') {
                continue;
            }
            $at = strrpos($pair, '=');
            $out[] = $at === false
                ? ['service' => $pair, 'status' => 0]
                : ['service' => substr($pair, 0, $at), 'status' => (int) substr($pair, $at + 1)];
        }
        return $out;
    }

    private function stringHeader(string $name): ?string
    {
        $v = $this->header($name);
        return $v === null || $v === '' ? null : $v;
    }

    private function intHeader(string $name): ?int
    {
        $v = trim((string) $this->header($name));
        return $v !== '' && ctype_digit($v) ? (int) $v : null;
    }
}
