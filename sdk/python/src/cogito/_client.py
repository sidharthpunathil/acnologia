from __future__ import annotations

import inspect
import os
from typing import Any, Awaitable, Callable, Dict, List, Optional, TypeVar, Union
from urllib.parse import quote

import httpx

from ._errors import AllKeysBusy, CogitoError, KeyProblem, NoKeyAvailable
from ._models import AsyncLease, Lease, Service, outcome_for

__version__ = "0.1.0"

T = TypeVar("T")

DEFAULT_TIMEOUT = 30.0
USER_AGENT = f"cogito-python/{__version__}"


def _resolve(base_url: Optional[str], token: Optional[str]) -> "tuple[str, str]":
    base = base_url or os.environ.get("COGITO_URL") or ""
    tok = token or os.environ.get("COGITO_TOKEN") or ""
    if not base:
        raise ValueError("base_url is required (or set COGITO_URL)")
    if not tok:
        raise ValueError("token is required (or set COGITO_TOKEN)")
    return base.rstrip("/"), tok


def _headers(token: str) -> Dict[str, str]:
    return {"Authorization": f"Bearer {token}", "User-Agent": USER_AGENT, "Accept": "application/json"}


def _proxy_headers(token: str) -> Dict[str, str]:
    return {"Authorization": f"Bearer {token}", "User-Agent": USER_AGENT}


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
    if code == "all_keys_busy":
        raise AllKeysBusy(message, retry_after=retry_after, status_code=resp.status_code, code=code)
    if code == "no_key_available" or (resp.status_code == 503 and not code):
        raise NoKeyAvailable(message, retry_after=retry_after, status_code=resp.status_code, code=code or "no_key_available")
    raise CogitoError(message, status_code=resp.status_code, code=code)


MAX_WAIT_SEC = 60


def _lease_body(
    service: str,
    exclude_key_ids: Optional[List[int]],
    ttl_sec: Optional[int],
    wait_sec: Optional[float] = None,
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
    return body


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
    """Calls a provider through Cogito's proxy. Cogito injects a key and rotates on
    rate limits, so the secret never reaches your process. Responses are returned
    as is, including non 2xx statuses."""

    def __init__(self, client: "Cogito", service: str) -> None:
        self._client = client
        self.service = service
        self.prefix = _proxy_prefix(client.base_url, service)

    def url(self, path: str = "") -> str:
        return self.prefix + path.lstrip("/")

    def request(self, method: str, path: str = "", **kwargs: Any) -> httpx.Response:
        headers = {**_proxy_headers(self._client.token), **dict(kwargs.pop("headers", None) or {})}
        return self._client._http.request(method.upper(), self.url(path), headers=headers, **kwargs)

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

    def __init__(self, client: "AsyncCogito", service: str) -> None:
        self._client = client
        self.service = service
        self.prefix = _proxy_prefix(client.base_url, service)

    def url(self, path: str = "") -> str:
        return self.prefix + path.lstrip("/")

    async def request(self, method: str, path: str = "", **kwargs: Any) -> httpx.Response:
        headers = {**_proxy_headers(self._client.token), **dict(kwargs.pop("headers", None) or {})}
        return await self._client._http.request(method.upper(), self.url(path), headers=headers, **kwargs)

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


class Cogito:
    """Synchronous Cogito client.

    >>> cog = Cogito("http://localhost:8080", "cog_...")
    >>> with cog.lease("serpapi") as lease:
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
    ) -> None:
        self.base_url, self.token = _resolve(base_url, token)
        self._owns_http = http_client is None
        self._http = http_client or httpx.Client(timeout=timeout, transport=transport)

    # lifecycle
    def close(self) -> None:
        if self._owns_http:
            self._http.close()

    def __enter__(self) -> "Cogito":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    # low level
    def _call(self, method: str, path: str, json: Any = None, timeout: Optional[httpx.Timeout] = None) -> Any:
        extra: Dict[str, Any] = {"timeout": timeout} if timeout is not None else {}
        resp = self._http.request(method, self.base_url + path, json=json, headers=_headers(self.token), **extra)
        _raise_for(resp)
        return resp.json() if resp.content else {}

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
    ) -> Lease:
        """Borrow a key.

        `wait_sec` (0 to 60) lets the server wait that long for a slot when every
        usable key is at its concurrency limit. Raises AllKeysBusy (a
        NoKeyAvailable) if none frees up in time, and NoKeyAvailable when the
        pool is drained.

        Always report the lease (or use it as a context manager): on services
        with a concurrency limit the lease holds one of the key's slots until it
        is reported or its `ttl_sec` runs out.
        """
        body = _lease_body(service, exclude_key_ids, ttl_sec, wait_sec)
        data = self._call("POST", "/api/v1/lease", body, timeout=_lease_timeout(self._http.timeout, wait_sec))
        return Lease.from_json(data, self)

    def with_key(
        self,
        service: str,
        fn: Callable[[Lease], T],
        max_attempts: int = 5,
        ttl_sec: Optional[int] = None,
        wait_sec: Optional[float] = None,
    ) -> T:
        """Call `fn(lease)` and report the outcome.

        If `fn` raises RateLimited, QuotaExhausted or InvalidKey, the key is
        reported with `rotate=True` and `fn` is called again with the next key,
        up to `max_attempts` times. Any other exception is reported as `error`
        and re-raised.

        Every lease is reported, whatever happens inside `fn`. That matters on
        services with a concurrency limit, where an unreported lease keeps one
        of the key's slots busy until its TTL expires. `wait_sec` is passed to
        every lease call, so a busy pool is waited on instead of failing fast.
        """
        if max_attempts < 1:
            raise ValueError("max_attempts must be at least 1")
        tried: List[int] = []
        lease = self.lease(service, ttl_sec=ttl_sec, wait_sec=wait_sec)
        attempt = 0
        while True:
            attempt += 1
            tried.append(lease.key_id)
            try:
                result = fn(lease)
            except KeyProblem as exc:
                last = attempt >= max_attempts
                kwargs = outcome_for(exc)
                kwargs["rotate"] = not last
                nxt = lease.report(**kwargs)
                if last:
                    raise
                lease = nxt or self.lease(service, exclude_key_ids=tried, ttl_sec=ttl_sec, wait_sec=wait_sec)
                continue
            except BaseException as exc:
                try:
                    lease.report(**outcome_for(exc))
                except Exception:
                    pass
                raise
            lease.report("ok")
            return result

    def proxy(self, service: str) -> Proxy:
        """Return a helper that sends requests through `/proxy/{service}/`."""
        return Proxy(self, service)


class AsyncCogito:
    """Asynchronous Cogito client built on httpx.AsyncClient."""

    def __init__(
        self,
        base_url: Optional[str] = None,
        token: Optional[str] = None,
        *,
        timeout: Union[float, httpx.Timeout] = DEFAULT_TIMEOUT,
        http_client: Optional[httpx.AsyncClient] = None,
        transport: Optional[httpx.AsyncBaseTransport] = None,
    ) -> None:
        self.base_url, self.token = _resolve(base_url, token)
        self._owns_http = http_client is None
        self._http = http_client or httpx.AsyncClient(timeout=timeout, transport=transport)

    async def aclose(self) -> None:
        if self._owns_http:
            await self._http.aclose()

    async def __aenter__(self) -> "AsyncCogito":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.aclose()

    async def _call(self, method: str, path: str, json: Any = None, timeout: Optional[httpx.Timeout] = None) -> Any:
        extra: Dict[str, Any] = {"timeout": timeout} if timeout is not None else {}
        resp = await self._http.request(method, self.base_url + path, json=json, headers=_headers(self.token), **extra)
        _raise_for(resp)
        return resp.json() if resp.content else {}

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
    ) -> AsyncLease:
        """Async lease. See Cogito.lease for `wait_sec` and why you must report."""
        body = _lease_body(service, exclude_key_ids, ttl_sec, wait_sec)
        data = await self._call("POST", "/api/v1/lease", body, timeout=_lease_timeout(self._http.timeout, wait_sec))
        return AsyncLease.from_json(data, self)

    async def with_key(
        self,
        service: str,
        fn: Callable[[AsyncLease], Union[T, Awaitable[T]]],
        max_attempts: int = 5,
        ttl_sec: Optional[int] = None,
        wait_sec: Optional[float] = None,
    ) -> T:
        """Async with_key. `fn` may be a plain function or a coroutine function.

        Like the sync version, every lease is reported, which frees its
        concurrency slot right away.
        """
        if max_attempts < 1:
            raise ValueError("max_attempts must be at least 1")
        tried: List[int] = []
        lease = await self.lease(service, ttl_sec=ttl_sec, wait_sec=wait_sec)
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
                kwargs = outcome_for(exc)
                kwargs["rotate"] = not last
                nxt = await lease.report(**kwargs)
                if last:
                    raise
                lease = nxt or await self.lease(service, exclude_key_ids=tried, ttl_sec=ttl_sec, wait_sec=wait_sec)
                continue
            except BaseException as exc:
                try:
                    await lease.report(**outcome_for(exc))
                except Exception:
                    pass
                raise
            await lease.report("ok")
            return result  # type: ignore[return-value]

    def proxy(self, service: str) -> AsyncProxy:
        return AsyncProxy(self, service)
