from __future__ import annotations

import json
from urllib.parse import unquote
from typing import Any, Dict, List, Optional

import httpx
import pytest

from cogito import (
    AllKeysBusy,
    AsyncCogito,
    Auth,
    Cogito,
    CogitoError,
    InvalidKey,
    Lease,
    NoKeyAvailable,
    QuotaExhausted,
    RateLimited,
)

BASE = "http://cogito.test"
TOKEN = "cog_test"


class FakeServer:
    """A tiny in-memory Cogito consumer API."""

    def __init__(self, keys: Optional[List[int]] = None, auth: Optional[Dict[str, str]] = None) -> None:
        self.keys = list(keys if keys is not None else [1, 2, 3])
        self.auth = auth or {"type": "query", "name": "api_key"}
        self.requests: List[httpx.Request] = []
        self.reports: List[Dict[str, Any]] = []
        self.lease_bodies: List[Dict[str, Any]] = []
        self.leases: Dict[str, int] = {}
        self.bad: set = set()
        self.counter = 0
        self.busy = False

    def _lease(self, exclude: List[int]) -> Optional[Dict[str, Any]]:
        for k in self.keys:
            if k not in exclude and k not in self.bad:
                self.counter += 1
                lid = f"l/{self.counter}"
                self.leases[lid] = k
                return {
                    "lease_id": lid,
                    "key_id": k,
                    "service": "serpapi",
                    "secret": f"secret-{k}",
                    "auth": self.auth,
                    "base_url": "https://serpapi.com",
                    "expires_at": "2026-11-01T12:05:00Z",
                }
        return None

    def handler(self, req: httpx.Request) -> httpx.Response:
        self.requests.append(req)
        if req.headers.get("authorization") != f"Bearer {TOKEN}":
            return httpx.Response(401, json={"error": {"code": "unauthorized", "message": "bad token"}})
        path = req.url.raw_path.decode().split("?")[0]
        if path == "/api/v1/services" and req.method == "GET":
            return httpx.Response(
                200,
                json={
                    "services": [
                        {
                            "slug": "serpapi",
                            "name": "SerpAPI",
                            "provider": "serpapi",
                            "category": "search",
                            "available": True,
                            "base_url": "https://serpapi.com",
                            "auth": {"type": "query", "name": "api_key"},
                        }
                    ]
                },
            )
        if path == "/api/v1/lease" and req.method == "POST":
            body = json.loads(req.content)
            self.lease_bodies.append(body)
            if body.get("service") == "missing":
                return httpx.Response(404, json={"error": {"code": "not_found", "message": "unknown service"}})
            if self.busy:
                return httpx.Response(
                    503,
                    json={"error": {"code": "all_keys_busy", "message": "every key is at its limit"}, "retry_after_sec": 1},
                )
            lease = self._lease(body.get("exclude_key_ids") or [])
            if lease is None:
                return httpx.Response(
                    503,
                    json={"error": {"code": "no_key_available", "message": "pool drained"}, "retry_after_sec": 42},
                )
            return httpx.Response(200, json=lease)
        if path.startswith("/api/v1/leases/") and path.endswith("/report"):
            raw_id = path[len("/api/v1/leases/") : -len("/report")]
            lid = unquote(raw_id)
            body = json.loads(req.content)
            body["_lease_id"] = lid
            self.reports.append(body)
            key = self.leases[lid]
            nxt = None
            if body["outcome"] in ("rate_limited", "quota_exhausted", "invalid"):
                self.bad.add(key)
            if body.get("rotate"):
                nxt = self._lease([key])
            return httpx.Response(200, json={"ok": True, "next": nxt})
        if path.startswith("/proxy/"):
            return httpx.Response(
                200,
                json={"path": path, "query": dict(req.url.params), "body": req.content.decode() or None},
                headers={"X-Cogito-Key-Id": "1", "X-Cogito-Attempts": "1"},
            )
        return httpx.Response(404, json={"error": {"code": "not_found", "message": path}})


@pytest.fixture
def server() -> FakeServer:
    return FakeServer()


@pytest.fixture
def cog(server: FakeServer) -> Cogito:
    return Cogito(BASE + "/", TOKEN, transport=httpx.MockTransport(server.handler))


def test_services(cog: Cogito) -> None:
    svcs = cog.services()
    assert len(svcs) == 1
    assert svcs[0].slug == "serpapi"
    assert svcs[0].available is True
    assert svcs[0].auth == Auth("query", "api_key")


def test_requires_config(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("COGITO_URL", raising=False)
    monkeypatch.delenv("COGITO_TOKEN", raising=False)
    with pytest.raises(ValueError):
        Cogito()
    monkeypatch.setenv("COGITO_URL", BASE)
    monkeypatch.setenv("COGITO_TOKEN", TOKEN)
    assert Cogito().base_url == BASE


def test_lease_fields_and_body(cog: Cogito, server: FakeServer) -> None:
    lease = cog.lease("serpapi", exclude_key_ids=[1], ttl_sec=60)
    assert server.lease_bodies[-1] == {"service": "serpapi", "exclude_key_ids": [1], "ttl_sec": 60}
    assert lease.key_id == 2
    assert lease.secret == "secret-2"
    assert lease.expires_at is not None and lease.expires_at.year == 2026
    assert "secret-2" not in repr(lease)


def test_lease_minimal_body(cog: Cogito, server: FakeServer) -> None:
    cog.lease("serpapi")
    assert server.lease_bodies[-1] == {"service": "serpapi"}


def test_no_key_available(server: FakeServer) -> None:
    server.keys = []
    cog = Cogito(BASE, TOKEN, transport=httpx.MockTransport(server.handler))
    with pytest.raises(NoKeyAvailable) as ei:
        cog.lease("serpapi")
    assert ei.value.retry_after == 42
    assert ei.value.status_code == 503
    assert ei.value.code == "no_key_available"


def test_lease_sends_wait_sec(cog: Cogito, server: FakeServer) -> None:
    cog.lease("serpapi", wait_sec=10)
    assert server.lease_bodies[-1] == {"service": "serpapi", "wait_sec": 10}
    cog.lease("serpapi", wait_sec=0)
    assert server.lease_bodies[-1] == {"service": "serpapi"}
    with pytest.raises(ValueError):
        cog.lease("serpapi", wait_sec=61)


def test_lease_wait_extends_timeout() -> None:
    seen: List[Any] = []
    server = FakeServer()

    def handler(req: httpx.Request) -> httpx.Response:
        seen.append(req.extensions.get("timeout"))
        return server.handler(req)

    cog = Cogito(BASE, TOKEN, timeout=5.0, transport=httpx.MockTransport(handler))
    cog.lease("serpapi")
    assert seen[-1]["read"] == 5.0
    cog.lease("serpapi", wait_sec=30)
    assert seen[-1]["read"] == 40.0


def test_with_key_sends_wait_sec(cog: Cogito, server: FakeServer) -> None:
    def fn(lease: Lease) -> int:
        if lease.key_id == 1:
            raise RateLimited(retry_after=5)
        return lease.key_id

    assert cog.with_key("serpapi", fn, wait_sec=15, ttl_sec=60) == 2
    assert server.lease_bodies[0] == {"service": "serpapi", "ttl_sec": 60, "wait_sec": 15}
    assert all(r.get("_lease_id") for r in server.reports)
    assert [r["outcome"] for r in server.reports] == ["rate_limited", "ok"]


def test_all_keys_busy(server: FakeServer) -> None:
    server.busy = True
    cog = Cogito(BASE, TOKEN, transport=httpx.MockTransport(server.handler))
    with pytest.raises(AllKeysBusy) as ei:
        cog.lease("serpapi", wait_sec=5)
    err = ei.value
    assert isinstance(err, NoKeyAvailable)
    assert err.busy is True
    assert err.code == "all_keys_busy"
    assert err.status_code == 503
    assert err.retry_after == 1


def test_no_key_available_is_not_busy(server: FakeServer) -> None:
    server.keys = []
    cog = Cogito(BASE, TOKEN, transport=httpx.MockTransport(server.handler))
    with pytest.raises(NoKeyAvailable) as ei:
        cog.lease("serpapi")
    assert ei.value.busy is False
    assert not isinstance(ei.value, AllKeysBusy)


def test_with_key_busy_raises_without_calling_fn(server: FakeServer) -> None:
    server.busy = True
    cog = Cogito(BASE, TOKEN, transport=httpx.MockTransport(server.handler))
    calls: List[int] = []
    with pytest.raises(AllKeysBusy):
        cog.with_key("serpapi", lambda lease: calls.append(1), wait_sec=2)
    assert calls == []
    assert server.reports == []


def test_with_key_reports_on_unexpected_exception(cog: Cogito, server: FakeServer) -> None:
    class Boom(Exception):
        pass

    def fn(lease: Lease) -> None:
        raise Boom()

    with pytest.raises(Boom):
        cog.with_key("serpapi", fn)
    assert len(server.reports) == 1
    assert server.reports[0]["outcome"] == "error"
    assert server.reports[0]["rotate"] is False


def test_api_error(cog: Cogito) -> None:
    with pytest.raises(CogitoError) as ei:
        cog.lease("missing")
    assert not isinstance(ei.value, NoKeyAvailable)
    assert ei.value.code == "not_found"
    assert ei.value.status_code == 404


def test_bad_token(server: FakeServer) -> None:
    cog = Cogito(BASE, "cog_wrong", transport=httpx.MockTransport(server.handler))
    with pytest.raises(CogitoError) as ei:
        cog.services()
    assert ei.value.status_code == 401


def test_report_returns_next(cog: Cogito, server: FakeServer) -> None:
    lease = cog.lease("serpapi")
    nxt = lease.report("rate_limited", status_code=429, retry_after_sec=1.2, message="slow down", rotate=True)
    assert isinstance(nxt, Lease)
    assert nxt.key_id == 2
    assert lease.reported
    rep = server.reports[-1]
    assert rep == {
        "outcome": "rate_limited",
        "rotate": True,
        "status_code": 429,
        "retry_after_sec": 2,
        "message": "slow down",
        "_lease_id": "l/1",
    }
    # lease id with a slash must be path escaped
    assert server.requests[-1].url.raw_path == b"/api/v1/leases/l%2F1/report"


def test_report_without_rotate_returns_none(cog: Cogito) -> None:
    assert cog.lease("serpapi").report("ok") is None


def test_context_manager_ok(cog: Cogito, server: FakeServer) -> None:
    with cog.lease("serpapi") as lease:
        assert lease.key_id == 1
    assert server.reports[-1]["outcome"] == "ok"
    assert server.reports[-1]["rotate"] is False


def test_context_manager_error(cog: Cogito, server: FakeServer) -> None:
    with pytest.raises(ZeroDivisionError):
        with cog.lease("serpapi"):
            1 / 0
    assert server.reports[-1]["outcome"] == "error"
    assert "ZeroDivisionError" in server.reports[-1]["message"]


def test_context_manager_rate_limited(cog: Cogito, server: FakeServer) -> None:
    with pytest.raises(RateLimited):
        with cog.lease("serpapi"):
            raise RateLimited(retry_after=30, status_code=429)
    rep = server.reports[-1]
    assert rep["outcome"] == "rate_limited"
    assert rep["retry_after_sec"] == 30
    assert rep["status_code"] == 429
    assert rep["rotate"] is False


def test_context_manager_no_double_report(cog: Cogito, server: FakeServer) -> None:
    with cog.lease("serpapi") as lease:
        lease.report("quota_exhausted")
    assert len(server.reports) == 1


def test_with_key_success(cog: Cogito, server: FakeServer) -> None:
    assert cog.with_key("serpapi", lambda lease: lease.key_id * 10) == 10
    assert [r["outcome"] for r in server.reports] == ["ok"]


def test_with_key_rotates(cog: Cogito, server: FakeServer) -> None:
    seen: List[int] = []

    def call(lease: Lease) -> str:
        seen.append(lease.key_id)
        if lease.key_id == 1:
            raise RateLimited(retry_after=5)
        if lease.key_id == 2:
            raise QuotaExhausted("out of searches", status_code=429)
        return lease.secret

    assert cog.with_key("serpapi", call) == "secret-3"
    assert seen == [1, 2, 3]
    assert [(r["outcome"], r["rotate"]) for r in server.reports] == [
        ("rate_limited", True),
        ("quota_exhausted", True),
        ("ok", False),
    ]
    assert server.reports[0]["retry_after_sec"] == 5


def test_with_key_max_attempts(cog: Cogito, server: FakeServer) -> None:
    def call(lease: Lease) -> None:
        raise InvalidKey()

    with pytest.raises(InvalidKey):
        cog.with_key("serpapi", call, max_attempts=2)
    assert [(r["outcome"], r["rotate"]) for r in server.reports] == [("invalid", True), ("invalid", False)]


def test_with_key_pool_drained(cog: Cogito, server: FakeServer) -> None:
    def call(lease: Lease) -> None:
        raise RateLimited()

    with pytest.raises(NoKeyAvailable) as ei:
        cog.with_key("serpapi", call, max_attempts=10)
    assert ei.value.retry_after == 42
    # fallback lease call excluded every key already tried
    assert server.lease_bodies[-1]["exclude_key_ids"] == [1, 2, 3]


def test_with_key_other_error(cog: Cogito, server: FakeServer) -> None:
    def call(lease: Lease) -> None:
        raise KeyError("boom")

    with pytest.raises(KeyError):
        cog.with_key("serpapi", call)
    assert server.reports[-1]["outcome"] == "error"
    assert server.reports[-1]["rotate"] is False


def _lease(auth_type: str, name: str) -> Lease:
    return Lease(
        lease_id="x",
        key_id=1,
        service="s",
        secret="sk",
        auth=Auth(auth_type, name),
        base_url="https://api.example.com/",
        expires_at=None,
    )


def test_apply_query() -> None:
    lease = _lease("query", "api_key")
    params = {"q": "coffee"}
    assert lease.apply(params) == {"q": "coffee", "api_key": "sk"}
    assert params == {"q": "coffee"}  # input not mutated
    assert lease.request_kwargs({"q": "x"}) == {"params": {"q": "x", "api_key": "sk"}, "headers": {}}


def test_apply_header_and_bearer() -> None:
    assert _lease("header", "xi-api-key").apply() == {"xi-api-key": "sk"}
    assert _lease("bearer", "").apply({"Accept": "a"}) == {"Accept": "a", "Authorization": "Bearer sk"}
    assert _lease("bearer", "X-Auth").request_kwargs(headers={}) == {"params": {}, "headers": {"X-Auth": "Bearer sk"}}
    with pytest.raises(ValueError):
        _lease("weird", "x").apply()


def test_lease_url() -> None:
    lease = _lease("query", "api_key")
    assert lease.url("/search.json") == "https://api.example.com/search.json"
    assert lease.url() == "https://api.example.com/"


def test_proxy(cog: Cogito, server: FakeServer) -> None:
    serp = cog.proxy("serpapi")
    resp = serp.get("/search.json", params={"engine": "google", "q": "coffee"})
    assert resp.status_code == 200
    assert resp.json()["path"] == "/proxy/serpapi/search.json"
    assert resp.json()["query"] == {"engine": "google", "q": "coffee"}
    assert resp.headers["X-Cogito-Key-Id"] == "1"
    req = server.requests[-1]
    assert req.headers["authorization"] == f"Bearer {TOKEN}"

    eleven = cog.proxy("elevenlabs")
    resp = eleven.post("v1/text-to-speech/voice1", json={"text": "hi"}, headers={"Accept": "audio/mpeg"})
    assert resp.json()["path"] == "/proxy/elevenlabs/v1/text-to-speech/voice1"
    assert json.loads(resp.json()["body"]) == {"text": "hi"}
    assert server.requests[-1].headers["accept"] == "audio/mpeg"
    for method in ("put", "patch", "delete"):
        assert getattr(eleven, method)("/v1/x").status_code == 200
        assert server.requests[-1].method == method.upper()
    assert eleven.request("options", "/v1/y").status_code == 200


async def test_async_client(server: FakeServer) -> None:
    async with AsyncCogito(BASE, TOKEN, transport=httpx.MockTransport(server.handler)) as cog:
        svcs = await cog.services()
        assert svcs[0].slug == "serpapi"

        async with await cog.lease("serpapi") as lease:
            assert lease.key_id == 1
        assert server.reports[-1]["outcome"] == "ok"

        async def call(lease: Any) -> int:
            if lease.key_id == 1:
                raise RateLimited(retry_after=1)
            return lease.key_id

        assert await cog.with_key("serpapi", call) == 2
        assert [r["outcome"] for r in server.reports[-2:]] == ["rate_limited", "ok"]

        assert await cog.with_key("serpapi", lambda lease: "sync fn ok") == "sync fn ok"

        resp = await cog.proxy("serpapi").get("search.json", params={"q": "a"})
        assert resp.json()["query"] == {"q": "a"}

        await (await cog.lease("serpapi", wait_sec=7)).report("ok")
        assert server.lease_bodies[-1] == {"service": "serpapi", "wait_sec": 7}

        server.busy = True
        with pytest.raises(AllKeysBusy):
            await cog.lease("serpapi", wait_sec=1)
        server.busy = False

        server.keys = []
        with pytest.raises(NoKeyAvailable):
            await cog.lease("serpapi")
