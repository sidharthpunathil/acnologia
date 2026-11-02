<?php

declare(strict_types=1);

namespace Acnologia\Exception;

/** A non 2xx response from the Acnologia consumer API, or a transport failure (status 0). */
class AcnologiaException extends \RuntimeException
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
