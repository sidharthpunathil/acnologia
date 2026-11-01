<?php

declare(strict_types=1);

namespace Cogito\Exception;

/** The provider rate limited this key. Pass retryAfter (seconds) when the provider says. */
final class RateLimited extends KeyProblem
{
    public const OUTCOME = 'rate_limited';

    public static function after(?int $retryAfter, ?int $statusCode = 429, string $message = ''): self
    {
        return new self($message, $statusCode, $retryAfter);
    }
}
