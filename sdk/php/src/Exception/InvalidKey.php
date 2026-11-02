<?php

declare(strict_types=1);

namespace Acnologia\Exception;

/** The provider rejected the key as invalid or revoked. */
final class InvalidKey extends KeyProblem
{
    public const OUTCOME = 'invalid';
}
