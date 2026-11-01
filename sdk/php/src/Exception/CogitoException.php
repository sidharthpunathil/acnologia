<?php

declare(strict_types=1);

namespace Cogito\Exception;

/** A non 2xx response from the Cogito consumer API, or a transport failure (status 0). */
class CogitoException extends \RuntimeException
{
    public function __construct(
        string $message,
        public readonly int $statusCode = 0,
        public readonly string $errorCode = '',
        ?\Throwable $previous = null,
    ) {
        parent::__construct($message, $statusCode, $previous);
    }
}
