<?php

declare(strict_types=1);

namespace Cogito;

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
}
