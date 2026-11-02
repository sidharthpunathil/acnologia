from __future__ import annotations

from typing import Optional


class AcnologiaError(Exception):
    """An error response from the Acnologia server (`{"error": {"code", "message"}}`)."""

    def __init__(self, message: str, *, status_code: int = 0, code: str = "") -> None:
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.code = code

    def __repr__(self) -> str:
        return f"{type(self).__name__}(status_code={self.status_code!r}, code={self.code!r}, message={self.message!r})"


class NoKeyAvailable(AcnologiaError):
    """No key could be leased right now (HTTP 503).

    `code` is `no_key_available` when every key is cooling down, exhausted or
    excluded, and `all_keys_busy` when usable keys exist but every one is at its
    concurrency limit (see `busy` and the `AllKeysBusy` subclass).
    """

    def __init__(
        self,
        message: str = "no key available",
        *,
        retry_after: Optional[float] = None,
        status_code: int = 503,
        code: str = "no_key_available",
    ) -> None:
        super().__init__(message, status_code=status_code, code=code)
        self.retry_after = retry_after

    @property
    def busy(self) -> bool:
        """True when every usable key is at its concurrency limit (`all_keys_busy`).

        Busy is short lived: slots free up as soon as other callers report, so a
        retry after `retry_after` seconds (usually 1) or a lease with `wait_sec`
        normally succeeds.
        """
        return self.code == "all_keys_busy"


class AllKeysBusy(NoKeyAvailable):
    """Every usable key is at its concurrency limit (HTTP 503 `all_keys_busy`).

    Subclasses NoKeyAvailable, so existing `except NoKeyAvailable` handlers still
    catch it.
    """

    def __init__(
        self,
        message: str = "all keys busy",
        *,
        retry_after: Optional[float] = None,
        status_code: int = 503,
        code: str = "all_keys_busy",
    ) -> None:
        super().__init__(message, retry_after=retry_after, status_code=status_code, code=code)


class ServiceUnavailable(AcnologiaError):
    """The provider keeps failing and its circuit breaker is open (HTTP 503 `service_unavailable`).

    Acnologia paused traffic to the service. Wait `retry_after` seconds (when
    known) before trying again. This is not a NoKeyAvailable.
    """

    def __init__(
        self,
        message: str = "service unavailable",
        *,
        retry_after: Optional[float] = None,
        status_code: int = 503,
        code: str = "service_unavailable",
    ) -> None:
        super().__init__(message, status_code=status_code, code=code)
        self.retry_after = retry_after


class KeyProblem(Exception):
    """Base class for errors you raise from inside `with_key` to make Acnologia rotate the key.

    `outcome` is the value sent to the report endpoint.
    """

    outcome: str = "error"

    def __init__(
        self,
        message: Optional[str] = None,
        *,
        status_code: Optional[int] = None,
    ) -> None:
        super().__init__(message or self.outcome)
        self.message = message
        self.status_code = status_code


class RateLimited(KeyProblem):
    """The provider rate limited this key. `retry_after` is in seconds, if known."""

    outcome = "rate_limited"

    def __init__(
        self,
        retry_after: Optional[float] = None,
        message: Optional[str] = None,
        *,
        status_code: Optional[int] = None,
    ) -> None:
        super().__init__(message, status_code=status_code)
        self.retry_after = retry_after


class QuotaExhausted(KeyProblem):
    """The key used up its monthly or daily quota."""

    outcome = "quota_exhausted"


class InvalidKey(KeyProblem):
    """The provider rejected the key as invalid or revoked."""

    outcome = "invalid"
