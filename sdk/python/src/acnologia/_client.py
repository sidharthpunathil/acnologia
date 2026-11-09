from __future__ import annotations

import inspect
import os
import ssl
from datetime import datetime
from typing import Any, Awaitable, Callable, Dict, List, Mapping, Optional, TypeVar, Union
from urllib.parse import quote, urlsplit, urlunsplit

import httpx

from ._errors import (
    AllKeysBusy,
    AcnologiaError,
    AppLimitReached,
    KeyProblem,
    NoKeyAvailable,
    ServiceUnavailable,
    ShareUnavailable,
)
from ._models import (
    AsyncLease,
    Capability,
    ForwardProxy,
    Guide,
    Lease,
    Me,
    Service,
    _parse_time,
    outcome_for,
)

__version__ = "0.2.0"

T = TypeVar("T")

DEFAULT_TIMEOUT = 30.0
USER_AGENT = f"acnologia-python/{__version__}"


def _resolve(base_url: Optional[str], token: Optional[str]) -> "tuple[str, str]":
    base = base_url or os.environ.get("ACNOLOGIA_URL") or ""
    tok = token or os.environ.get("ACNOLOGIA_TOKEN") or ""
    if not base:
        raise ValueError("base_url is required (or set ACNOLOGIA_URL)")
    if not tok:
        raise ValueError("token is required (or set ACNOLOGIA_TOKEN)")
    return base.rstrip("/"), tok


DEFAULT_CA_PATH = "acnologia-ca.pem"


def _resolve_access(access_password: Optional[str]) -> Optional[str]:
    return access_password or os.environ.get("ACNOLOGIA_ACCESS_PASSWORD") or None


def _public_headers(access: Optional[str]) -> Dict[str, str]:
    h = {"User-Agent": USER_AGENT}
    if access:
        h["X-Acnologia-Access"] = access
    return h


def _headers(token: str, access: Optional[str] = None, accept: str = "application/json") -> Dict[str, str]:
    return {"Authorization": f"Bearer {token}", **_public_headers(access), "Accept": accept}


def _proxy_headers(
    token: str,
    access: Optional[str] = None,
    idempotency_key: Optional[str] = None,
    no_cache: bool = False,
) -> Dict[str, str]:
    h = {"Authorization": f"Bearer {token}", **_public_headers(access)}
    if idempotency_key:
        h["Idempotency-Key"] = idempotency_key
    if no_cache:
        h["Cache-Control"] = "no-cache"
    return h


def _guide_path(slug: str) -> str:
    return f"/api/v1/services/{quote(slug, safe='')}/guide"


CA_PATH = "/api/v1/proxy/ca.pem"


def _capability_request(
    base_url: str,
    slug: str,
    params: Optional[Mapping[str, Any]],
    service: Optional[str],
    raw: bool,
) -> "tuple[str, Dict[str, str], Any]":
    q: Dict[str, str] = {}
    if service:
        q["_service"] = service
    if raw:
        q["_raw"] = "1"
    return f"{base_url}/proxy/{quote(slug, safe='')}", q, dict(params or {})


def _url_with_token(proxy_url: str, token: str) -> str:
    """`proxy_url` with user `app` and `token` as the password, percent escaped."""
    parts = urlsplit(proxy_url)
    if not parts.hostname:
        raise AcnologiaError(f"bad forward proxy URL {proxy_url!r}")
    host = parts.netloc.rpartition("@")[2]
    return urlunsplit((parts.scheme, f"app:{quote(token, safe='')}@{host}", parts.path, parts.query, parts.fragment))


def _env(url: str, ca_path: str) -> Dict[str, str]:
    return {
        "HTTPS_PROXY": url,
        "HTTP_PROXY": url,
        "NODE_USE_ENV_PROXY": "1",
        "NODE_EXTRA_CA_CERTS": ca_path,
        "REQUESTS_CA_BUNDLE": ca_path,
        "SSL_CERT_FILE": ca_path,
    }


def _ssl_context(ca_pem: str) -> ssl.SSLContext:
    ctx = ssl.create_default_context()
    ctx.load_verify_locations(cadata=ca_pem)
    return ctx


def _write_ca(path: str, pem: str) -> str:
    with open(path, "w", encoding="utf-8") as f:
        f.write(pem)
    return path


def _header_retry_after(resp: httpx.Response) -> Optional[float]:
    try:
        value = float(resp.headers.get("retry-after", ""))
    except ValueError:
        return None
    return value if value > 0 else None


def _raise_for(resp: httpx.Response) -> None:
    if resp.is_success:
        return
    code = ""
    message = resp.reason_phrase or f"HTTP {resp.status_code}"
    retry_after: Optional[float] = None
    try:
        body = resp.json()
    except ValueError:
        body = None
    if not isinstance(body, dict):
        body = None
    if isinstance(body, dict):
        err = body.get("error")
        if isinstance(err, dict):
            code = str(err.get("code") or "")
            message = str(err.get("message") or message)
        elif isinstance(err, str):
            message = err
        if body.get("retry_after_sec") is not None:
            try:
                retry_after = float(body["retry_after_sec"])
            except (TypeError, ValueError):
                retry_after = None
    if retry_after is None or retry_after <= 0:
        retry_after = _header_retry_after(resp)
    if code == "service_unavailable":
        raise ServiceUnavailable(message, retry_after=retry_after, status_code=resp.status_code, code=code)
    if code == "all_keys_busy":
        raise AllKeysBusy(message, retry_after=retry_after, status_code=resp.status_code, code=code)
    if code == "no_key_available" or (resp.status_code == 503 and not code):
        raise NoKeyAvailable(message, retry_after=retry_after, status_code=resp.status_code, code=code or "no_key_available")
    if code == "app_limit_reached":
        raise AppLimitReached(
            message,
            limit=str((body or {}).get("limit") or ""),
            reset_at=_parse_time((body or {}).get("reset_at")),
            retry_after=retry_after,
            status_code=resp.status_code,
            code=code,
        )
    if code in ("share_revoked", "share_paused", "share_expired"):
        status = (resp.headers.get("x-acnologia-share") or "").strip() or code[len("share_") :]
        raise ShareUnavailable(message, status=status, status_code=resp.status_code, code=code, retry_after=retry_after)
    raise AcnologiaError(message, status_code=resp.status_code, code=code, retry_after=retry_after)


MAX_WAIT_SEC = 60


def _lease_body(
    service: str,
    exclude_key_ids: Optional[List[int]],
    ttl_sec: Optional[int],
    wait_sec: Optional[float] = None,
    exclusive: bool = False,
) -> Dict[str, Any]:
    body: Dict[str, Any] = {"service": service}
    if exclude_key_ids:
        body["exclude_key_ids"] = list(exclude_key_ids)
    if ttl_sec is not None:
        body["ttl_sec"] = int(ttl_sec)
    if wait_sec:
        if wait_sec < 0 or wait_sec > MAX_WAIT_SEC:
            raise ValueError(f"wait_sec must be between 0 and {MAX_WAIT_SEC}")
        body["wait_sec"] = int(-(-float(wait_sec) // 1))  # ceil
    if exclusive:
        body["exclusive"] = True
    return body


def _renew_path(lease_id: str) -> str:
    return f"/api/v1/leases/{quote(lease_id, safe='')}/renew"


def _renew_body(ttl_sec: Optional[float]) -> Dict[str, Any]:
    if ttl_sec is not None and ttl_sec > 0:
        return {"ttl_sec": int(-(-float(ttl_sec) // 1))}  # ceil
    return {}


def _renew_expiry(data: Any) -> "datetime":
    expires = _parse_time(data.get("expires_at") if isinstance(data, dict) else None)
    if expires is None:
        raise AcnologiaError("renew response has no expires_at")
    return expires


def _lease_timeout(current: httpx.Timeout, wait_sec: Optional[float]) -> Optional[httpx.Timeout]:
    """A timeout that outlasts the server side wait, or None to keep the client's."""
    if not wait_sec:
        return None
    need = float(wait_sec) + 10.0
    read = current.read
    if read is None or read >= need:
        return None
    return httpx.Timeout(
        connect=current.connect,
        read=need,
        write=current.write,
        pool=current.pool,
    )


def _report_body(
    outcome: str,
    status_code: Optional[int],
    retry_after_sec: Optional[float],
    message: Optional[str],
    rotate: bool,
) -> Dict[str, Any]:
    body: Dict[str, Any] = {"outcome": outcome, "rotate": bool(rotate)}
    if status_code is not None:
        body["status_code"] = int(status_code)
    if retry_after_sec is not None:
        body["retry_after_sec"] = int(-(-float(retry_after_sec) // 1))  # ceil
    if message:
        body["message"] = message
    return body


def _proxy_prefix(base_url: str, service: str) -> str:
    return f"{base_url}/proxy/{quote(service, safe='')}/"


class Proxy:
    """Calls a provider through Acnologia's proxy. Acnologia injects a key and rotates on
    rate limits, so the secret never reaches your process. Responses are returned
    as is, including non 2xx statuses."""

    def __init__(self, client: "Acnologia", service: str) -> None:
        self._client = client
        self.service = service
        self.prefix = _proxy_prefix(client.base_url, service)

    def url(self, path: str = "") -> str:
        return self.prefix + path.lstrip("/")

    def request(
        self,
        method: str,
        path: str = "",
        *,
        idempotency_key: Optional[str] = None,
        no_cache: bool = False,
        **kwargs: Any,
    ) -> httpx.Response:
        """Send a request. `idempotency_key` sends `Idempotency-Key`, `no_cache`
        sends `Cache-Control: no-cache`; other kwargs go to httpx. Read the
        Acnologia headers of the answer with `proxy_info`."""
        c = self._client
        base = _proxy_headers(c.token, c.access_password, idempotency_key, no_cache)
        headers = {**base, **dict(kwargs.pop("headers", None) or {})}
        return c._http.request(method.upper(), self.url(path), headers=headers, **kwargs)

    def get(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return self.request("GET", path, **kwargs)

    def post(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return self.request("POST", path, **kwargs)

    def put(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return self.request("PUT", path, **kwargs)

    def patch(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return self.request("PATCH", path, **kwargs)

    def delete(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return self.request("DELETE", path, **kwargs)


class AsyncProxy:
    """Async variant of Proxy."""

    def __init__(self, client: "AsyncAcnologia", service: str) -> None:
        self._client = client
        self.service = service
        self.prefix = _proxy_prefix(client.base_url, service)

    def url(self, path: str = "") -> str:
        return self.prefix + path.lstrip("/")

    async def request(
        self,
        method: str,
        path: str = "",
        *,
        idempotency_key: Optional[str] = None,
        no_cache: bool = False,
        **kwargs: Any,
    ) -> httpx.Response:
        """Async request. See Proxy.request."""
        c = self._client
        base = _proxy_headers(c.token, c.access_password, idempotency_key, no_cache)
        headers = {**base, **dict(kwargs.pop("headers", None) or {})}
        return await c._http.request(method.upper(), self.url(path), headers=headers, **kwargs)

    async def get(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return await self.request("GET", path, **kwargs)

    async def post(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return await self.request("POST", path, **kwargs)

    async def put(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return await self.request("PUT", path, **kwargs)

    async def patch(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return await self.request("PATCH", path, **kwargs)

    async def delete(self, path: str = "", **kwargs: Any) -> httpx.Response:
        return await self.request("DELETE", path, **kwargs)


class Acnologia:
    """Synchronous Acnologia client.

    >>> acn = Acnologia("http://localhost:8080", "acn_...")
    >>> with acn.lease("serpapi") as lease:
    ...     httpx.get(lease.url("/search.json"), params=lease.apply({"q": "coffee"}))
    """

    def __init__(
        self,
        base_url: Optional[str] = None,
        token: Optional[str] = None,
        *,
        timeout: Union[float, httpx.Timeout] = DEFAULT_TIMEOUT,
        http_client: Optional[httpx.Client] = None,
        transport: Optional[httpx.BaseTransport] = None,
        access_password: Optional[str] = None,
    ) -> None:
        """`access_password` (or `ACNOLOGIA_ACCESS_PASSWORD`) is sent as
        `X-Acnologia-Access` on every request, for a deploy behind the access
        password."""
        self.base_url, self.token = _resolve(base_url, token)
        self.access_password = _resolve_access(access_password)
        self._owns_http = http_client is None
        self._http = http_client or httpx.Client(timeout=timeout, transport=transport)

    # lifecycle
    def close(self) -> None:
        if self._owns_http:
            self._http.close()

    def __enter__(self) -> "Acnologia":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    # low level
    def _call(self, method: str, path: str, json: Any = None, timeout: Optional[httpx.Timeout] = None) -> Any:
        extra: Dict[str, Any] = {"timeout": timeout} if timeout is not None else {}
        resp = self._http.request(
            method, self.base_url + path, json=json, headers=_headers(self.token, self.access_password), **extra
        )
        _raise_for(resp)
        return resp.json() if resp.content else {}

    def _text(self, path: str, accept: str, auth: bool = True) -> str:
        headers = _headers(self.token, self.access_password, accept) if auth else {**_public_headers(self.access_password), "Accept": accept}
        resp = self._http.request("GET", self.base_url + path, headers=headers)
        _raise_for(resp)
        return resp.text

    def _report(self, lease_id: str, **kw: Any) -> Optional[Lease]:
        data = self._call("POST", f"/api/v1/leases/{quote(lease_id, safe='')}/report", _report_body(**kw))
        nxt = data.get("next") if isinstance(data, dict) else None
        return Lease.from_json(nxt, self) if nxt else None

    # consumer API
    def services(self) -> List[Service]:
        """List services this app token may use."""
        data = self._call("GET", "/api/v1/services")
        return [Service.from_json(s) for s in data.get("services", [])]

    def lease(
        self,
        service: str,
        exclude_key_ids: Optional[List[int]] = None,
        ttl_sec: Optional[int] = None,
        wait_sec: Optional[float] = None,
        exclusive: bool = False,
    ) -> Lease:
        """Borrow a key.

        `wait_sec` (0 to 60) lets the server wait that long for a slot when every
        usable key is at its concurrency limit. Raises AllKeysBusy (a
        NoKeyAvailable) if none frees up in time, and NoKeyAvailable when the
        pool is drained.

        Always report the lease (or use it as a context manager): on services
        with a concurrency limit the lease holds one of the key's slots until it
        is reported or its `ttl_sec` runs out.

        `exclusive=True` reserves the key for this lease only: no other lease
        or proxy call gets it until the lease is reported or expires.
        """
        body = _lease_body(service, exclude_key_ids, ttl_sec, wait_sec, exclusive)
        data = self._call("POST", "/api/v1/lease", body, timeout=_lease_timeout(self._http.timeout, wait_sec))
        return Lease.from_json(data, self)

    def with_key(
        self,
        service: str,
        fn: Callable[[Lease], T],
        max_attempts: int = 5,
        ttl_sec: Optional[int] = None,
        wait_sec: Optional[float] = None,
        exclusive: bool = False,
    ) -> T:
        """Call `fn(lease)` and report the outcome.

        If `fn` raises RateLimited, QuotaExhausted or InvalidKey, the key is
        reported with `rotate=True` and `fn` is called again with the next key,
        up to `max_attempts` times. Any other exception is reported as `error`
        and re-raised.

        Every lease is reported, whatever happens inside `fn`. That matters on
        services with a concurrency limit, where an unreported lease keeps one
        of the key's slots busy until its TTL expires. `wait_sec` is passed to
        every lease call, so a busy pool is waited on instead of failing fast,
        and so are `ttl_sec` and `exclusive`.
        """
        if max_attempts < 1:
            raise ValueError("max_attempts must be at least 1")
        tried: List[int] = []
        lease = self.lease(service, ttl_sec=ttl_sec, wait_sec=wait_sec, exclusive=exclusive)
        attempt = 0
        while True:
            attempt += 1
            tried.append(lease.key_id)
            try:
                result = fn(lease)
            except KeyProblem as exc:
                last = attempt >= max_attempts
                kwargs = outcome_for(exc, lease.secret)
                kwargs["rotate"] = not last
                nxt = lease.report(**kwargs)
                if last:
                    raise
                lease = nxt or self.lease(service, exclude_key_ids=tried, ttl_sec=ttl_sec, wait_sec=wait_sec, exclusive=exclusive)
                continue
            except BaseException as exc:
                try:
                    lease.report(**outcome_for(exc, lease.secret))
                except Exception:
                    pass
                raise
            lease.report("ok")
            return result

    def renew(self, lease: Lease, ttl_sec: Optional[float] = None) -> datetime:
        """Extend an open lease so a long job keeps its key, and update `lease.expires_at`.

        `ttl_sec` None uses the server default (120 seconds); the server caps it
        at 24 hours. Returns the new expiry. Raises AcnologiaError with code
        `lease_not_found` (HTTP 404) when the lease is no longer open.
        """
        data = self._call("POST", _renew_path(lease.lease_id), _renew_body(ttl_sec))
        lease.expires_at = _renew_expiry(data)
        return lease.expires_at

    def proxy(self, service: str) -> Proxy:
        """Return a helper that sends requests through `/proxy/{service}/`.

        `service` may also be a service group or a capability slug."""
        return Proxy(self, service)

    def me(self) -> Me:
        """The token's own access: app, limits, usage, services and, for a
        share's token, the share. A share that stopped still answers here,
        with its `status`."""
        return Me.from_json(self._call("GET", "/api/v1/me"))

    def guide(self, slug: str) -> Guide:
        """The usage guide of a service the token may use."""
        return Guide.from_json(self._call("GET", _guide_path(slug)))

    def guide_markdown(self, slug: str) -> str:
        """The usage guide of a service, as Markdown."""
        return self._text(_guide_path(slug), "text/markdown")

    def capabilities(self) -> List[Capability]:
        """The capabilities the app may call. Empty for a share's token."""
        data = self._call("GET", "/api/v1/capabilities")
        return [Capability.from_json(c) for c in data.get("capabilities") or []]

    def capability(
        self,
        slug: str,
        params: Optional[Mapping[str, Any]] = None,
        *,
        service: Optional[str] = None,
        raw: bool = False,
        idempotency_key: Optional[str] = None,
        no_cache: bool = False,
        **kwargs: Any,
    ) -> httpx.Response:
        """Call a capability: `POST /proxy/{slug}` with `params` as the JSON body.

        `service` uses only that provider's route (`?_service=`), `raw` returns
        the provider's own body (`?_raw=1`). The answer is returned as is, like
        a proxy call; read `capability`, `service` and `tried` with `proxy_info`."""
        url, q, body = _capability_request(self.base_url, slug, params, service, raw)
        headers = {**_proxy_headers(self.token, self.access_password, idempotency_key, no_cache), **dict(kwargs.pop("headers", None) or {})}
        return self._http.request("POST", url, params=q, json=body, headers=headers, **kwargs)

    def forward_proxy(self) -> ForwardProxy:
        """How to use the forward proxy: its URL, the CA, and the hosts it puts keys on."""
        return ForwardProxy.from_json(self._call("GET", "/api/v1/proxy"))

    def forward_proxy_ca(self) -> str:
        """The forward proxy's CA certificate as PEM. The route is public; no token is sent."""
        return self._text(CA_PATH, "application/x-pem-file", auth=False)

    def save_forward_proxy_ca(self, path: str = DEFAULT_CA_PATH) -> str:
        """Write the CA certificate to `path` and return the path."""
        return _write_ca(path, self.forward_proxy_ca())

    def forward_proxy_url(self) -> str:
        """The forward proxy URL with user `app` and this client's token as the
        password, such as `http://app:acn_...@host:8888`. It contains the token:
        do not log it."""
        return _url_with_token(self.forward_proxy().proxy_url, self.token)

    def forward_proxy_env(self, ca_path: str = DEFAULT_CA_PATH) -> Dict[str, str]:
        """Environment that sends an app's HTTP through the forward proxy:
        `HTTPS_PROXY` and `HTTP_PROXY` (the `forward_proxy_url`),
        `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`
        and `SSL_CERT_FILE` set to `ca_path`. Save the CA there with
        `save_forward_proxy_ca`. It contains the token: do not log it."""
        return _env(self.forward_proxy_url(), ca_path)

    def forward_proxy_client(self, **kwargs: Any) -> httpx.Client:
        """An httpx.Client that sends every request through the forward proxy,
        with this client's token as proxy credentials, trusting the system
        roots plus the Acnologia CA. Call provider URLs directly with it.
        Extra kwargs (such as `timeout`) go to httpx.Client; close it when done."""
        url = self.forward_proxy_url()
        ctx = _ssl_context(self.forward_proxy_ca())
        return httpx.Client(transport=httpx.HTTPTransport(proxy=httpx.Proxy(url), verify=ctx), **kwargs)


class AsyncAcnologia:
    """Asynchronous Acnologia client built on httpx.AsyncClient."""

    def __init__(
        self,
        base_url: Optional[str] = None,
        token: Optional[str] = None,
        *,
        timeout: Union[float, httpx.Timeout] = DEFAULT_TIMEOUT,
        http_client: Optional[httpx.AsyncClient] = None,
        transport: Optional[httpx.AsyncBaseTransport] = None,
        access_password: Optional[str] = None,
    ) -> None:
        """See Acnologia for `access_password`."""
        self.base_url, self.token = _resolve(base_url, token)
        self.access_password = _resolve_access(access_password)
        self._owns_http = http_client is None
        self._http = http_client or httpx.AsyncClient(timeout=timeout, transport=transport)

    async def aclose(self) -> None:
        if self._owns_http:
            await self._http.aclose()

    async def __aenter__(self) -> "AsyncAcnologia":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.aclose()

    async def _call(self, method: str, path: str, json: Any = None, timeout: Optional[httpx.Timeout] = None) -> Any:
        extra: Dict[str, Any] = {"timeout": timeout} if timeout is not None else {}
        resp = await self._http.request(
            method, self.base_url + path, json=json, headers=_headers(self.token, self.access_password), **extra
        )
        _raise_for(resp)
        return resp.json() if resp.content else {}

    async def _text(self, path: str, accept: str, auth: bool = True) -> str:
        headers = _headers(self.token, self.access_password, accept) if auth else {**_public_headers(self.access_password), "Accept": accept}
        resp = await self._http.request("GET", self.base_url + path, headers=headers)
        _raise_for(resp)
        return resp.text

    async def _report(self, lease_id: str, **kw: Any) -> Optional[AsyncLease]:
        data = await self._call("POST", f"/api/v1/leases/{quote(lease_id, safe='')}/report", _report_body(**kw))
        nxt = data.get("next") if isinstance(data, dict) else None
        return AsyncLease.from_json(nxt, self) if nxt else None

    async def services(self) -> List[Service]:
        data = await self._call("GET", "/api/v1/services")
        return [Service.from_json(s) for s in data.get("services", [])]

    async def lease(
        self,
        service: str,
        exclude_key_ids: Optional[List[int]] = None,
        ttl_sec: Optional[int] = None,
        wait_sec: Optional[float] = None,
        exclusive: bool = False,
    ) -> AsyncLease:
        """Async lease. See Acnologia.lease for `wait_sec`, `exclusive` and why you must report."""
        body = _lease_body(service, exclude_key_ids, ttl_sec, wait_sec, exclusive)
        data = await self._call("POST", "/api/v1/lease", body, timeout=_lease_timeout(self._http.timeout, wait_sec))
        return AsyncLease.from_json(data, self)

    async def with_key(
        self,
        service: str,
        fn: Callable[[AsyncLease], Union[T, Awaitable[T]]],
        max_attempts: int = 5,
        ttl_sec: Optional[int] = None,
        wait_sec: Optional[float] = None,
        exclusive: bool = False,
    ) -> T:
        """Async with_key. `fn` may be a plain function or a coroutine function.

        Like the sync version, every lease is reported, which frees its
        concurrency slot right away.
        """
        if max_attempts < 1:
            raise ValueError("max_attempts must be at least 1")
        tried: List[int] = []
        lease = await self.lease(service, ttl_sec=ttl_sec, wait_sec=wait_sec, exclusive=exclusive)
        attempt = 0
        while True:
            attempt += 1
            tried.append(lease.key_id)
            try:
                result = fn(lease)
                if inspect.isawaitable(result):
                    result = await result
            except KeyProblem as exc:
                last = attempt >= max_attempts
                kwargs = outcome_for(exc, lease.secret)
                kwargs["rotate"] = not last
                nxt = await lease.report(**kwargs)
                if last:
                    raise
                lease = nxt or await self.lease(service, exclude_key_ids=tried, ttl_sec=ttl_sec, wait_sec=wait_sec, exclusive=exclusive)
                continue
            except BaseException as exc:
                try:
                    await lease.report(**outcome_for(exc, lease.secret))
                except Exception:
                    pass
                raise
            await lease.report("ok")
            return result  # type: ignore[return-value]

    async def renew(self, lease: AsyncLease, ttl_sec: Optional[float] = None) -> datetime:
        """Async renew. See Acnologia.renew."""
        data = await self._call("POST", _renew_path(lease.lease_id), _renew_body(ttl_sec))
        lease.expires_at = _renew_expiry(data)
        return lease.expires_at

    def proxy(self, service: str) -> AsyncProxy:
        return AsyncProxy(self, service)

    async def me(self) -> Me:
        """Async me. See Acnologia.me."""
        return Me.from_json(await self._call("GET", "/api/v1/me"))

    async def guide(self, slug: str) -> Guide:
        return Guide.from_json(await self._call("GET", _guide_path(slug)))

    async def guide_markdown(self, slug: str) -> str:
        return await self._text(_guide_path(slug), "text/markdown")

    async def capabilities(self) -> List[Capability]:
        data = await self._call("GET", "/api/v1/capabilities")
        return [Capability.from_json(c) for c in data.get("capabilities") or []]

    async def capability(
        self,
        slug: str,
        params: Optional[Mapping[str, Any]] = None,
        *,
        service: Optional[str] = None,
        raw: bool = False,
        idempotency_key: Optional[str] = None,
        no_cache: bool = False,
        **kwargs: Any,
    ) -> httpx.Response:
        """Async capability. See Acnologia.capability."""
        url, q, body = _capability_request(self.base_url, slug, params, service, raw)
        headers = {**_proxy_headers(self.token, self.access_password, idempotency_key, no_cache), **dict(kwargs.pop("headers", None) or {})}
        return await self._http.request("POST", url, params=q, json=body, headers=headers, **kwargs)

    async def forward_proxy(self) -> ForwardProxy:
        return ForwardProxy.from_json(await self._call("GET", "/api/v1/proxy"))

    async def forward_proxy_ca(self) -> str:
        return await self._text(CA_PATH, "application/x-pem-file", auth=False)

    async def save_forward_proxy_ca(self, path: str = DEFAULT_CA_PATH) -> str:
        return _write_ca(path, await self.forward_proxy_ca())

    async def forward_proxy_url(self) -> str:
        """Contains the token: do not log it. See Acnologia.forward_proxy_url."""
        return _url_with_token((await self.forward_proxy()).proxy_url, self.token)

    async def forward_proxy_env(self, ca_path: str = DEFAULT_CA_PATH) -> Dict[str, str]:
        """Contains the token: do not log it. See Acnologia.forward_proxy_env."""
        return _env(await self.forward_proxy_url(), ca_path)

    async def forward_proxy_client(self, **kwargs: Any) -> httpx.AsyncClient:
        """An httpx.AsyncClient for the forward proxy. See Acnologia.forward_proxy_client."""
        url = await self.forward_proxy_url()
        ctx = _ssl_context(await self.forward_proxy_ca())
        return httpx.AsyncClient(transport=httpx.AsyncHTTPTransport(proxy=httpx.Proxy(url), verify=ctx), **kwargs)
