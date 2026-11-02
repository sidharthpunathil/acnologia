<?php

declare(strict_types=1);

namespace Acnologia\Exception;

/**
 * HTTP 503 service_unavailable: the provider keeps failing and its circuit breaker
 * is open, so Acnologia paused traffic to it. Wait $retryAfter seconds (null when the
 * server did not say). Not a NoKeyAvailable: waiting for a key will not help.
 */
final class ServiceUnavailable extends AcnologiaException
{
    public function __construct(
        string $message = 'service unavailable',
        public readonly ?int $retryAfter = null,
        int $statusCode = 503,
    ) {
        parent::__construct($message, $statusCode, 'service_unavailable');
    }
}
