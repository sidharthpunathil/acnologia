<?php

declare(strict_types=1);

namespace Cogito\Exception;

/**
 * HTTP 503 all_keys_busy: usable keys exist but every one is at its concurrency
 * limit. Clears as soon as another caller reports a lease, so retry after
 * $retryAfter seconds (usually 1) or lease with $waitSec. Extends NoKeyAvailable,
 * so existing `catch (NoKeyAvailable $e)` blocks still catch it.
 */
final class AllKeysBusy extends NoKeyAvailable
{
    public function __construct(string $message = 'all keys busy', ?int $retryAfter = null)
    {
        parent::__construct($message, $retryAfter, 'all_keys_busy');
    }
}
