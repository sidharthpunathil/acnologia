<?php

declare(strict_types=1);

namespace Acnologia\Exception;

/**
 * HTTP 403 share_revoked, share_paused or share_expired: the token belongs to a
 * share its owner revoked or paused, or that reached its end date. $shareStatus is
 * "revoked", "paused" or "expired" (the server also sends it as X-Acnologia-Share).
 * A paused share may resume; a revoked one will not come back.
 */
final class ShareUnavailable extends AcnologiaException
{
    public function __construct(
        string $message = 'share unavailable',
        public readonly string $shareStatus = '',
    ) {
        parent::__construct($message, 403, 'share_' . $shareStatus);
    }
}
