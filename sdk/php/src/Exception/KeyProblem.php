<?php

declare(strict_types=1);

namespace Acnologia\Exception;

/**
 * Throw a subclass from inside withKey() to report the key and retry with the next one.
 * OUTCOME is the value sent to the report endpoint.
 */
abstract class KeyProblem extends \RuntimeException
{
    public const OUTCOME = 'error';

    public function __construct(
        string $message = '',
        public readonly ?int $statusCode = null,
        public readonly ?int $retryAfter = null,
    ) {
        parent::__construct($message !== '' ? $message : static::OUTCOME);
    }

    public function outcome(): string
    {
        return static::OUTCOME;
    }
}
