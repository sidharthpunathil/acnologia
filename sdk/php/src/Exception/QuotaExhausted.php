<?php

declare(strict_types=1);

namespace Cogito\Exception;

/** The key used up its quota. */
final class QuotaExhausted extends KeyProblem
{
    public const OUTCOME = 'quota_exhausted';
}
