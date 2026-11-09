<?php

declare(strict_types=1);

namespace Acnologia\Exception;

/**
 * HTTP 429 app_limit_reached: the app used up a daily or monthly request or budget
 * limit. $limit names it (daily_requests, monthly_requests, daily_budget,
 * monthly_budget), $resetAt is when it resets (ISO 8601) and $retryAfter is the
 * seconds until then, or null when the server did not say.
 */
final class AppLimitReached extends AcnologiaException
{
    public function __construct(
        string $message = 'app limit reached',
        public readonly ?string $limit = null,
        public readonly ?string $resetAt = null,
        public readonly ?int $retryAfter = null,
    ) {
        parent::__construct($message, 429, 'app_limit_reached');
    }
}
