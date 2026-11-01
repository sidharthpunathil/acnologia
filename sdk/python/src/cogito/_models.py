from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional
from urllib.parse import quote

from ._errors import KeyProblem

if TYPE_CHECKING:  # pragma: no cover
    from ._client import AsyncCogito, Cogito

OUTCOMES = ("ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error")


@dataclass(frozen=True)
class Auth:
    """Where the provider expects the key: `type` is `query`, `header` or `bearer`.

    `prefix`, when set, goes in front of the secret for every type (for
    `bearer` it replaces the default `Bearer ` prefix)."""

    type: str
    name: str
    prefix: str = ""

    @classmethod
    def from_json(cls, data: Optional[Mapping[str, Any]]) -> "Auth":
        data = data or {}
        return cls(
            type=str(data.get("type") or ""),
            name=str(data.get("name") or ""),
            prefix=str(data.get("prefix") or ""),
        )


@dataclass(frozen=True)
class Service:
    slug: str
    name: str
    provider: str
    category: str
    available: bool
    base_url: str
    auth: Auth

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "Service":
        return cls(
            slug=str(data.get("slug", "")),
            name=str(data.get("name", "")),
            provider=str(data.get("provider", "")),
            category=str(data.get("category", "")),
            available=bool(data.get("available", False)),
            base_url=str(data.get("base_url", "")),
            auth=Auth.from_json(data.get("auth")),
        )


def _parse_time(value: Any) -> Optional[datetime]:
    if not value or not isinstance(value, str):
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


MAX_REPORT_MESSAGE = 500


def scrub_message(message: Optional[str], secret: Optional[str]) -> Optional[str]:
    """Replace the lease secret (raw and URL encoded) with `[key]` and truncate.

    Errors from query auth services often carry the full request URL, key
    included, and a report must not copy it to the server."""
    if message is None:
        return None
    if secret:
        message = message.replace(secret, "[key]").replace(quote(secret, safe=""), "[key]")
    return message[:MAX_REPORT_MESSAGE]


def outcome_for(exc: Optional[BaseException], secret: Optional[str] = None) -> Dict[str, Any]:
    """Map an exception (or None for success) to report keyword arguments.

    `secret` is scrubbed from the message built from the exception."""
    if exc is None:
        return {"outcome": "ok"}
    if isinstance(exc, KeyProblem):
        kwargs: Dict[str, Any] = {
            "outcome": exc.outcome,
            "status_code": exc.status_code,
            "message": scrub_message(exc.message, secret),
            "rotate": True,
        }
        retry_after = getattr(exc, "retry_after", None)
        if retry_after is not None:
            kwargs["retry_after_sec"] = retry_after
        return kwargs
    return {"outcome": "error", "message": scrub_message(f"{type(exc).__name__}: {exc}", secret)}


@dataclass
class _LeaseBase:
    lease_id: str
    key_id: int
    service: str
    secret: str = field(repr=False)
    auth: Auth
    base_url: str
    expires_at: Optional[datetime]
    reported: bool = field(default=False, compare=False)

    @classmethod
    def _fields_from_json(cls, data: Mapping[str, Any]) -> Dict[str, Any]:
        return {
            "lease_id": str(data.get("lease_id", "")),
            "key_id": int(data.get("key_id") or 0),
            "service": str(data.get("service", "")),
            "secret": str(data.get("secret", "")),
            "auth": Auth.from_json(data.get("auth")),
            "base_url": str(data.get("base_url", "")),
            "expires_at": _parse_time(data.get("expires_at")),
        }

    def apply(self, params_or_headers: Optional[Mapping[str, Any]] = None) -> Dict[str, Any]:
        """Return a copy of `params_or_headers` with the secret added.

        For auth type `query` the dict is treated as query params and the secret
        goes under `auth.name` (for example `api_key`). For `header` it is
        treated as headers (for example `xi-api-key`). For `bearer` the header
        `auth.name` (default `Authorization`) is set to `Bearer <secret>`.

        A non empty `auth.prefix` goes in front of the secret for every type;
        for `bearer` it replaces the default `Bearer ` prefix.
        """
        out: Dict[str, Any] = dict(params_or_headers or {})
        kind = self.auth.type
        prefix = self.auth.prefix
        if kind == "bearer":
            out[self.auth.name or "Authorization"] = (prefix or "Bearer ") + self.secret
        elif kind == "header":
            out[self.auth.name or "Authorization"] = prefix + self.secret
        elif kind == "query":
            out[self.auth.name] = prefix + self.secret
        else:
            raise ValueError(f"unknown auth type {kind!r}")
        return out

    def request_kwargs(
        self,
        params: Optional[Mapping[str, Any]] = None,
        headers: Optional[Mapping[str, Any]] = None,
    ) -> Dict[str, Dict[str, Any]]:
        """Return `{"params": ..., "headers": ...}` with the secret placed correctly,
        ready to splat into an httpx or requests call."""
        p = dict(params or {})
        h = dict(headers or {})
        if self.auth.type == "query":
            p = self.apply(p)
        else:
            h = self.apply(h)
        return {"params": p, "headers": h}

    def url(self, path: str = "") -> str:
        """Join the service base_url with `path`."""
        if not path:
            return self.base_url
        return self.base_url.rstrip("/") + "/" + path.lstrip("/")


@dataclass
class Lease(_LeaseBase):
    """A key handed out by Cogito. Use it as a context manager to report automatically:
    a clean exit reports `ok`, an exception reports the matching outcome."""

    _client: Optional["Cogito"] = field(default=None, repr=False, compare=False)

    @classmethod
    def from_json(cls, data: Mapping[str, Any], client: Optional["Cogito"] = None) -> "Lease":
        return cls(**cls._fields_from_json(data), _client=client)

    def report(
        self,
        outcome: str,
        status_code: Optional[int] = None,
        retry_after_sec: Optional[float] = None,
        message: Optional[str] = None,
        rotate: bool = False,
    ) -> Optional["Lease"]:
        """Report how the key did. Returns the next Lease when `rotate` is true
        and another key is available, otherwise None."""
        if self._client is None:
            raise RuntimeError("lease is not bound to a client")
        nxt = self._client._report(
            self.lease_id,
            outcome=outcome,
            status_code=status_code,
            retry_after_sec=retry_after_sec,
            message=message,
            rotate=rotate,
        )
        self.reported = True
        return nxt

    def __enter__(self) -> "Lease":
        return self

    def __exit__(self, exc_type: Any, exc: Optional[BaseException], tb: Any) -> bool:
        if self.reported:
            return False
        kwargs = outcome_for(exc, self.secret)
        kwargs["rotate"] = False
        if exc is None:
            self.report(**kwargs)
        else:
            try:
                self.report(**kwargs)
            except Exception:
                pass  # never mask the caller's exception
        return False


@dataclass
class AsyncLease(_LeaseBase):
    """Async variant of Lease. Use with `async with`."""

    _client: Optional["AsyncCogito"] = field(default=None, repr=False, compare=False)

    @classmethod
    def from_json(cls, data: Mapping[str, Any], client: Optional["AsyncCogito"] = None) -> "AsyncLease":
        return cls(**cls._fields_from_json(data), _client=client)

    async def report(
        self,
        outcome: str,
        status_code: Optional[int] = None,
        retry_after_sec: Optional[float] = None,
        message: Optional[str] = None,
        rotate: bool = False,
    ) -> Optional["AsyncLease"]:
        if self._client is None:
            raise RuntimeError("lease is not bound to a client")
        nxt = await self._client._report(
            self.lease_id,
            outcome=outcome,
            status_code=status_code,
            retry_after_sec=retry_after_sec,
            message=message,
            rotate=rotate,
        )
        self.reported = True
        return nxt

    async def __aenter__(self) -> "AsyncLease":
        return self

    async def __aexit__(self, exc_type: Any, exc: Optional[BaseException], tb: Any) -> bool:
        if self.reported:
            return False
        kwargs = outcome_for(exc, self.secret)
        kwargs["rotate"] = False
        if exc is None:
            await self.report(**kwargs)
        else:
            try:
                await self.report(**kwargs)
            except Exception:
                pass
        return False


ServiceList = List[Service]
