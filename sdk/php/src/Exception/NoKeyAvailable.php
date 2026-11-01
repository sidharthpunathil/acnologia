<?php

declare(strict_types=1);

namespace Cogito\Exception;

/**
 * HTTP 503: no key can be leased right now.
 *
 * errorCode is `no_key_available` when every key is cooling down, exhausted or
 * excluded, and `all_keys_busy` when every usable key is at its concurrency
 * limit. In the second case $busy is true and the subclass AllKeysBusy is thrown.
 */
class NoKeyAvailable extends CogitoException
{
    /** True when every usable key is at its concurrency limit (`all_keys_busy`). */
    public readonly bool $busy;

    public function __construct(
        string $message = 'no key available',
        public readonly ?int $retryAfter = null,
        string $errorCode = 'no_key_available',
    ) {
        parent::__construct($message, 503, $errorCode);
        $this->busy = $errorCode === 'all_keys_busy';
    }
}
