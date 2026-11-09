from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional
from urllib.parse import quote

from ._errors import KeyProblem

if TYPE_CHECKING:  # pragma: no cover
    from ._client import AsyncAcnologia, Acnologia

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
    guide_url: str = ""
    """Where the service's usage guide is (see `guide`)."""

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
            guide_url=str(data.get("guide_url") or ""),
        )


def _parse_time(value: Any) -> Optional[datetime]:
    if not value or not isinstance(value, str):
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def _opt_int(value: Any) -> Optional[int]:
    if value is None or isinstance(value, bool):
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _opt_float(value: Any) -> Optional[float]:
    if value is None or isinstance(value, bool):
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def _dict(value: Any) -> Mapping[str, Any]:
    return value if isinstance(value, Mapping) else {}


@dataclass(frozen=True)
class LimitHit:
    """The limit an app reached (`daily_requests`, `monthly_requests`,
    `daily_budget` or `monthly_budget`) and when it resets."""

    limit: str
    reset_at: Optional[datetime]

    @classmethod
    def from_json(cls, data: Any) -> Optional["LimitHit"]:
        if not isinstance(data, Mapping):
            return None
        return cls(limit=str(data.get("limit") or ""), reset_at=_parse_time(data.get("reset_at")))


@dataclass(frozen=True)
class MeApp:
    id: int
    name: str


@dataclass(frozen=True)
class MeLimits:
    """The app's request and budget limits; None means no limit."""

    daily_requests: Optional[int] = None
    monthly_requests: Optional[int] = None
    daily_budget: Optional[float] = None
    monthly_budget: Optional[float] = None


@dataclass(frozen=True)
class MeUsage:
    today_requests: int = 0
    today_cost: float = 0.0
    month_requests: int = 0
    month_cost: float = 0.0
    last_used_at: Optional[datetime] = None
    limit_hit: Optional[LimitHit] = None


@dataclass(frozen=True)
class MeService:
    slug: str
    name: str
    provider: str
    category: str
    available: int
    """Keys free right now for a share's token; 1 or 0 for an ordinary app."""
    logo_url: Optional[str] = None


@dataclass(frozen=True)
class MeShare:
    """The share behind a share's token. `status` is `active`, `paused`, `expired` or `revoked`."""

    id: str
    name: str
    owner_name: str
    recipient: str
    label: str
    status: str
    mode: str
    ends_at: Optional[datetime]


@dataclass(frozen=True)
class Me:
    """The token's own access, from `GET /api/v1/me`. `share` is None for an ordinary app."""

    app: MeApp
    limits: MeLimits
    usage: MeUsage
    limit_hit: Optional[LimitHit]
    services: List[MeService]
    share: Optional[MeShare]

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "Me":
        app = _dict(data.get("app"))
        lim = _dict(data.get("limits"))
        use = _dict(data.get("usage"))
        sh = data.get("share")
        share = None
        if isinstance(sh, Mapping):
            share = MeShare(
                id=str(sh.get("id") or ""),
                name=str(sh.get("name") or ""),
                owner_name=str(sh.get("owner_name") or ""),
                recipient=str(sh.get("recipient") or ""),
                label=str(sh.get("label") or ""),
                status=str(sh.get("status") or ""),
                mode=str(sh.get("mode") or ""),
                ends_at=_parse_time(sh.get("ends_at")),
            )
        return cls(
            app=MeApp(id=_opt_int(app.get("id")) or 0, name=str(app.get("name") or "")),
            limits=MeLimits(
                daily_requests=_opt_int(lim.get("daily_requests")),
                monthly_requests=_opt_int(lim.get("monthly_requests")),
                daily_budget=_opt_float(lim.get("daily_budget")),
                monthly_budget=_opt_float(lim.get("monthly_budget")),
            ),
            usage=MeUsage(
                today_requests=_opt_int(use.get("today_requests")) or 0,
                today_cost=_opt_float(use.get("today_cost")) or 0.0,
                month_requests=_opt_int(use.get("month_requests")) or 0,
                month_cost=_opt_float(use.get("month_cost")) or 0.0,
                last_used_at=_parse_time(use.get("last_used_at")),
                limit_hit=LimitHit.from_json(use.get("limit_hit")),
            ),
            limit_hit=LimitHit.from_json(data.get("limit_hit")),
            services=[
                MeService(
                    slug=str(s.get("slug") or ""),
                    name=str(s.get("name") or ""),
                    provider=str(s.get("provider") or ""),
                    category=str(s.get("category") or ""),
                    available=_opt_int(s.get("available")) or 0,
                    logo_url=s.get("logo_url") if isinstance(s.get("logo_url"), str) else None,
                )
                for s in data.get("services") or []
                if isinstance(s, Mapping)
            ],
            share=share,
        )


@dataclass(frozen=True)
class Guide:
    """A service's usage guide. `guide` is the structured guide as plain JSON
    data; `generated` is True when it was built from the provider spec."""

    markdown: str
    generated: bool
    guide: Any = None

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "Guide":
        return cls(
            markdown=str(data.get("markdown") or ""),
            generated=bool(data.get("generated", False)),
            guide=data.get("guide"),
        )


@dataclass(frozen=True)
class CapabilityRoute:
    service: str
    mapped: bool


@dataclass(frozen=True)
class Capability:
    """One call served by whichever provider can. `params` is plain JSON data."""

    slug: str
    name: str
    description: str
    strategy: str
    params: Any
    routes: List[CapabilityRoute]
    url: str

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "Capability":
        return cls(
            slug=str(data.get("slug") or ""),
            name=str(data.get("name") or ""),
            description=str(data.get("description") or ""),
            strategy=str(data.get("strategy") or ""),
            params=data.get("params"),
            routes=[
                CapabilityRoute(service=str(r.get("service") or ""), mapped=bool(r.get("mapped", False)))
                for r in data.get("routes") or []
                if isinstance(r, Mapping)
            ],
            url=str(data.get("url") or ""),
        )


@dataclass(frozen=True)
class ForwardHost:
    host: str
    service: str
    name: str
    path: str = ""


@dataclass(frozen=True)
class ForwardProxy:
    """How to use the forward proxy, from `GET /api/v1/proxy`.

    It never holds the token: `proxy_url_with_token` and `env` carry an
    `<app token>` placeholder. Use `forward_proxy_url` or `forward_proxy_env`
    for the real values."""

    proxy_url: str
    proxy_url_with_token: str
    ca_url: str
    ca_fingerprint: str
    passthrough: bool
    hosts: List[ForwardHost]
    env: Dict[str, str]
    service_header: str

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "ForwardProxy":
        return cls(
            proxy_url=str(data.get("proxy_url") or ""),
            proxy_url_with_token=str(data.get("proxy_url_with_token") or ""),
            ca_url=str(data.get("ca_url") or ""),
            ca_fingerprint=str(data.get("ca_fingerprint") or ""),
            passthrough=bool(data.get("passthrough", False)),
            hosts=[
                ForwardHost(
                    host=str(h.get("host") or ""),
                    service=str(h.get("service") or ""),
                    name=str(h.get("name") or ""),
                    path=str(h.get("path") or ""),
                )
                for h in data.get("hosts") or []
                if isinstance(h, Mapping)
            ],
            env={str(k): str(v) for k, v in _dict(data.get("env")).items()},
            service_header=str(data.get("service_header") or ""),
        )


@dataclass(frozen=True)
class Tried:
    """One route a capability call tried, and the status it answered."""

    service: str
    status: int


def _parse_tried(value: Optional[str]) -> List[Tried]:
    out: List[Tried] = []
    for part in (value or "").split(","):
        part = part.strip()
        if not part:
            continue
        svc, _, st = part.partition("=")
        out.append(Tried(service=svc.strip(), status=_opt_int(st.strip()) or 0))
    return out


@dataclass(frozen=True)
class ProxyInfo:
    """What Acnologia says about a proxied answer in its headers.

    `key_id` is None when absent (a share's token); `cache` is `hit`, `miss`
    and so on when caching applies; `replayed` is True when the answer was
    replayed for an Idempotency-Key; `tried` lists every route a capability
    call tried."""

    key_id: Optional[int]
    attempts: Optional[int]
    service: Optional[str]
    cache: Optional[str]
    replayed: bool
    capability: Optional[str]
    tried: List[Tried]


def proxy_info(resp: Any) -> ProxyInfo:
    """Read the Acnologia headers of a response from a proxy or capability call."""
    h = resp.headers
    return ProxyInfo(
        key_id=_opt_int(h.get("x-acnologia-key-id")),
        attempts=_opt_int(h.get("x-acnologia-attempts")),
        service=h.get("x-acnologia-service"),
        cache=h.get("x-acnologia-cache"),
        replayed=(h.get("idempotent-replayed") or "").strip().lower() == "true",
        capability=h.get("x-acnologia-capability"),
        tried=_parse_tried(h.get("x-acnologia-tried")),
    )


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
    exclusive: bool = False

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
            "exclusive": bool(data.get("exclusive", False)),
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
    """A key handed out by Acnologia. Use it as a context manager to report automatically:
    a clean exit reports `ok`, an exception reports the matching outcome."""

    _client: Optional["Acnologia"] = field(default=None, repr=False, compare=False)

    @classmethod
    def from_json(cls, data: Mapping[str, Any], client: Optional["Acnologia"] = None) -> "Lease":
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

    def renew(self, ttl_sec: Optional[float] = None) -> datetime:
        """Extend this open lease so a long job keeps its key. Updates and returns
        `expires_at`. None uses the server default (120 seconds); the server caps
        it at 24 hours. Raises AcnologiaError with code `lease_not_found` when the
        lease is no longer open."""
        if self._client is None:
            raise RuntimeError("lease is not bound to a client")
        return self._client.renew(self, ttl_sec)

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

    _client: Optional["AsyncAcnologia"] = field(default=None, repr=False, compare=False)

    @classmethod
    def from_json(cls, data: Mapping[str, Any], client: Optional["AsyncAcnologia"] = None) -> "AsyncLease":
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

    async def renew(self, ttl_sec: Optional[float] = None) -> datetime:
        """Async renew. See Lease.renew."""
        if self._client is None:
            raise RuntimeError("lease is not bound to a client")
        return await self._client.renew(self, ttl_sec)

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
