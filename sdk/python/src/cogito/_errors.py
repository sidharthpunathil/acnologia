from __future__ import annotations

from typing import Optional


class CogitoError(Exception):
    """An error response from the Cogito server (`{"error": {"code", "message"}}`)."""

    def __init__(self, message: str, *, status_code: int = 0, code: str = "") -> None:
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.code = code

    def __repr__(self) -> str:
        return f"{type(self).__name__}(status_code={self.status_code!r}, code={self.code!r}, message={self.message!r})"


class NoKeyAvailable(CogitoError):
    """Every key for the service is cooling down, exhausted or excluded (HTTP 503)."""

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


class KeyProblem(Exception):
    """Base class for errors you raise from inside `with_key` to make Cogito rotate the key.

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
