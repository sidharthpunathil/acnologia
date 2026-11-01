<?php

declare(strict_types=1);

namespace Cogito\Exception;

/** HTTP 503 no_key_available: every key is cooling down, exhausted or excluded. */
final class NoKeyAvailable extends CogitoException
{
    public function __construct(
        string $message = 'no key available',
        public readonly ?int $retryAfter = null,
    ) {
        parent::__construct($message, 503, 'no_key_available');
    }
}
