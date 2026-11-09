"""Offline tests for the 0.2.0 calls."""

from __future__ import annotations

import base64
import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any, Dict, List, Optional

import httpx
import pytest

from acnologia import (
    Acnologia,
    AcnologiaError,
    AppLimitReached,
    AsyncAcnologia,
    ForwardProxy,
    ShareUnavailable,
    Tried,
    __version__,
    proxy_info,
)

BASE = "http://acnologia.test"
TOKEN = "acn_test"

CA_PEM = """-----BEGIN CERTIFICATE-----
MIIBbzCCARSgAwIBAgIUcgVGxQ5N4jflNZl+7uJrV55eYEEwCgYIKoZIzj0EAwIw
HDEaMBgGA1UEAwwRQWNub2xvZ2lhIFRlc3QgQ0EwIBcNMjYxMTA5MTg0NzI3WhgP
MjEyNjEwMTYxODQ3MjdaMBwxGjAYBgNVBAMMEUFjbm9sb2dpYSBUZXN0IENBMFkw
EwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEb+rNnfskTkhy+/VDODiLTglN7k9dr2Wb
ithaFSTf2ahgmQHaQ28hOwXQa15FLbj28X638Eq1FslPEjMQtfPTVaMyMDAwHQYD
VR0OBBYEFPcoyYoP5LiiWWeE+lALtPMt8XLyMA8GA1UdEwEB/wQFMAMBAf8wCgYI
KoZIzj0EAwIDSQAwRgIhAPXrn2uqUbR/IUFhXZ17mkcjg7HOTZG2ofeeKPblzlhy
AiEAzaxgpr8aUG2iMoamHBT8+rDl3FJkSWPWeL38iUyo01E=
-----END CERTIFICATE-----
"""

ME = {
    "app": {"id": 3, "name": "bot"},
    "limits": {"daily_requests": 100, "monthly_requests": None, "daily_budget": None, "monthly_budget": 5.5},
    "usage": {
        "today_requests": 7,
        "today_cost": 0.1,
        "month_requests": 70,
        "month_cost": 1,
        "last_used_at": "2026-11-01T10:00:00Z",
        "limit_hit": None,
    },
    "limit_hit": {"limit": "daily_requests", "reset_at": "2026-11-02T00:00:00Z"},
    "services": [{"slug": "serpapi", "name": "SerpAPI", "provider": "serpapi", "category": "search", "available": 1, "logo_url": None}],
    "share": {"id": "sh1", "name": "Team", "owner_name": "Ann", "recipient": "bob", "label": "x", "status": "paused", "mode": "proxy", "ends_at": None},
}


class Stub:
    def __init__(self, proxy_url: str = "http://proxy.test:8888") -> None:
        self.requests: List[httpx.Request] = []
        self.proxy_url = proxy_url

    def handler(self, req: httpx.Request) -> httpx.Response:
        self.requests.append(req)
        path = req.url.path
        if path == "/api/v1/proxy/ca.pem":
            return httpx.Response(200, text=CA_PEM, headers={"Content-Type": "application/x-pem-file"})
        if req.headers.get("authorization") != f"Bearer {TOKEN}":
            return httpx.Response(401, json={"error": {"code": "unauthenticated", "message": "bad token"}})
        if path == "/api/v1/me":
            return httpx.Response(200, json=ME)
        if path == "/api/v1/services":
            return httpx.Response(200, json={"services": [{"slug": "serpapi", "name": "SerpAPI", "provider": "serpapi", "category": "search",
                                                          "available": True, "base_url": "https://serpapi.com",
                                                          "auth": {"type": "query", "name": "api_key"},
                                                          "guide_url": "http://x/api/v1/services/serpapi/guide"}]})
        if path == "/api/v1/services/serpapi/guide":
            if "text/markdown" in req.headers.get("accept", ""):
                return httpx.Response(200, text="# serpapi", headers={"Content-Type": "text/markdown"})
            return httpx.Response(200, json={"markdown": "# serpapi", "generated": True, "guide": {"endpoints": [1]}})
        if path == "/api/v1/capabilities":
            return httpx.Response(200, json={"capabilities": [{"slug": "web-search", "name": "Web search", "description": "d",
                                                              "strategy": "failover", "params": {"q": {"type": "string"}},
                                                              "routes": [{"service": "serpapi", "mapped": True}],
                                                              "url": "http://x/proxy/web-search"}]})
        if path == "/api/v1/proxy":
            return httpx.Response(200, json={
                "proxy_url": self.proxy_url,
                "proxy_url_with_token": "http://app:%3Capp%20token%3E@proxy.test:8888",
                "ca_url": BASE + "/api/v1/proxy/ca.pem", "ca_fingerprint": "ab:cd", "passthrough": True,
                "hosts": [{"host": "serpapi.com", "service": "serpapi", "name": "SerpAPI"}],
                "env": {"HTTPS_PROXY": "http://app:%3Capp%20token%3E@proxy.test:8888"},
                "service_header": "X-Acnologia-Service",
            })
        if path.startswith("/proxy/"):
            fail = req.url.params.get("fail")
            if fail == "limit":
                return httpx.Response(429, headers={"Retry-After": "60"}, json={
                    "error": {"code": "app_limit_reached", "message": "this app reached its daily requests limit"},
                    "limit": "daily_requests", "reset_at": "2026-11-02T00:00:00Z", "retry_after_sec": 60})
            if fail == "share":
                return httpx.Response(403, headers={"X-Acnologia-Share": "revoked"},
                                      json={"error": {"code": "share_revoked", "message": "revoked"}})
            return httpx.Response(200, json={"ok": True}, headers={
                "X-Acnologia-Key-Id": "7", "X-Acnologia-Attempts": "2", "X-Acnologia-Service": "serpapi",
                "X-Acnologia-Cache": "miss", "Idempotent-Replayed": "true", "X-Acnologia-Capability": "web-search",
                "X-Acnologia-Tried": "brave=429, serpapi=200"})
        return httpx.Response(404, json={"error": {"code": "not_found", "message": path}})


@pytest.fixture
def stub() -> Stub:
    return Stub()


@pytest.fixture
def acn(stub: Stub, monkeypatch: pytest.MonkeyPatch) -> Acnologia:
    monkeypatch.delenv("ACNOLOGIA_ACCESS_PASSWORD", raising=False)
    return Acnologia(BASE, TOKEN, transport=httpx.MockTransport(stub.handler))


def test_version() -> None:
    assert __version__ == "0.2.0"


def test_me(acn: Acnologia, stub: Stub) -> None:
    me = acn.me()
    req = stub.requests[-1]
    assert req.method == "GET" and req.url.path == "/api/v1/me"
    assert me.app.id == 3 and me.app.name == "bot"
    assert me.limits.daily_requests == 100 and me.limits.monthly_requests is None and me.limits.monthly_budget == 5.5
    assert me.usage.today_requests == 7 and me.usage.last_used_at is not None and me.usage.limit_hit is None
    assert me.limit_hit is not None and me.limit_hit.limit == "daily_requests" and me.limit_hit.reset_at.day == 2
    assert me.services[0].slug == "serpapi" and me.services[0].available == 1 and me.services[0].logo_url is None
    assert me.share is not None and me.share.status == "paused" and me.share.owner_name == "Ann" and me.share.ends_at is None


def test_services_guide_url(acn: Acnologia) -> None:
    assert acn.services()[0].guide_url == "http://x/api/v1/services/serpapi/guide"


def test_guide(acn: Acnologia, stub: Stub) -> None:
    g = acn.guide("serpapi")
    assert stub.requests[-1].url.path == "/api/v1/services/serpapi/guide"
    assert stub.requests[-1].headers["accept"] == "application/json"
    assert g.markdown == "# serpapi" and g.generated is True and g.guide == {"endpoints": [1]}
    assert acn.guide_markdown("serpapi") == "# serpapi"
    assert stub.requests[-1].headers["accept"] == "text/markdown"
    assert stub.requests[-1].headers["authorization"] == f"Bearer {TOKEN}"


def test_capabilities(acn: Acnologia, stub: Stub) -> None:
    caps = acn.capabilities()
    assert stub.requests[-1].method == "GET" and stub.requests[-1].url.path == "/api/v1/capabilities"
    c = caps[0]
    assert c.slug == "web-search" and c.strategy == "failover" and c.params == {"q": {"type": "string"}}
    assert c.routes[0].service == "serpapi" and c.routes[0].mapped and c.url == "http://x/proxy/web-search"


def test_capability_call(acn: Acnologia, stub: Stub) -> None:
    resp = acn.capability("web-search", {"q": "coffee"}, service="serpapi", raw=True, idempotency_key="k1", no_cache=True)
    req = stub.requests[-1]
    assert req.method == "POST" and req.url.path == "/proxy/web-search"
    assert dict(req.url.params) == {"_service": "serpapi", "_raw": "1"}
    assert json.loads(req.content) == {"q": "coffee"}
    assert req.headers["content-type"] == "application/json"
    assert req.headers["idempotency-key"] == "k1" and req.headers["cache-control"] == "no-cache"
    info = proxy_info(resp)
    assert info.capability == "web-search" and info.service == "serpapi"
    assert info.tried == [Tried("brave", 429), Tried("serpapi", 200)]

    acn.capability("web-search")
    req = stub.requests[-1]
    assert req.url.query == b"" and json.loads(req.content) == {} and "idempotency-key" not in req.headers


def test_proxy_options_and_info(acn: Acnologia, stub: Stub) -> None:
    resp = acn.proxy("serpapi").post("/search", json={"q": "x"}, idempotency_key="abc", no_cache=True)
    req = stub.requests[-1]
    assert req.headers["idempotency-key"] == "abc" and req.headers["cache-control"] == "no-cache"
    info = proxy_info(resp)
    assert info.key_id == 7 and info.attempts == 2 and info.service == "serpapi"
    assert info.cache == "miss" and info.replayed is True
    acn.proxy("serpapi").get("/search")
    assert "idempotency-key" not in stub.requests[-1].headers and "cache-control" not in stub.requests[-1].headers
    empty = proxy_info(httpx.Response(200))
    assert empty.key_id is None and empty.attempts is None and empty.tried == [] and empty.replayed is False


def test_access_password(stub: Stub, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("ACNOLOGIA_ACCESS_PASSWORD", raising=False)
    acn = Acnologia(BASE, TOKEN, transport=httpx.MockTransport(stub.handler), access_password="gate")
    acn.me()
    assert stub.requests[-1].headers["x-acnologia-access"] == "gate"
    acn.proxy("serpapi").get("/x")
    assert stub.requests[-1].headers["x-acnologia-access"] == "gate"
    acn.forward_proxy_ca()
    assert stub.requests[-1].headers["x-acnologia-access"] == "gate"

    monkeypatch.setenv("ACNOLOGIA_ACCESS_PASSWORD", "from-env")
    Acnologia(BASE, TOKEN, transport=httpx.MockTransport(stub.handler)).me()
    assert stub.requests[-1].headers["x-acnologia-access"] == "from-env"

    monkeypatch.delenv("ACNOLOGIA_ACCESS_PASSWORD")
    Acnologia(BASE, TOKEN, transport=httpx.MockTransport(stub.handler)).me()
    assert "x-acnologia-access" not in stub.requests[-1].headers


def test_app_limit_reached(acn: Acnologia) -> None:
    with pytest.raises(AppLimitReached) as ei:
        acn._call("GET", "/proxy/x?fail=limit")
    e = ei.value
    assert isinstance(e, AcnologiaError)
    assert e.status_code == 429 and e.code == "app_limit_reached" and e.limit == "daily_requests"
    assert e.retry_after == 60 and e.reset_at is not None and e.reset_at.day == 2


def test_share_unavailable(acn: Acnologia) -> None:
    with pytest.raises(ShareUnavailable) as ei:
        acn._call("GET", "/proxy/x?fail=share")
    assert ei.value.status == "revoked" and ei.value.status_code == 403 and ei.value.code == "share_revoked"


def test_share_status_from_code_and_generic_retry_after() -> None:
    def handler(req: httpx.Request) -> httpx.Response:
        if req.url.path == "/paused":
            return httpx.Response(403, json={"error": {"code": "share_paused", "message": "m"}})
        return httpx.Response(400, json={"error": {"code": "bad_params", "message": "q"}}, headers={"Retry-After": "2"})

    acn = Acnologia(BASE, TOKEN, transport=httpx.MockTransport(handler))
    with pytest.raises(ShareUnavailable) as ei:
        acn._call("GET", "/paused")
    assert ei.value.status == "paused"
    with pytest.raises(AcnologiaError) as ge:
        acn._call("GET", "/other")
    assert ge.value.code == "bad_params" and ge.value.retry_after == 2


def test_forward_proxy(acn: Acnologia, stub: Stub) -> None:
    fp = acn.forward_proxy()
    assert isinstance(fp, ForwardProxy)
    assert stub.requests[-1].url.path == "/api/v1/proxy"
    assert fp.proxy_url == "http://proxy.test:8888" and fp.ca_fingerprint == "ab:cd" and fp.passthrough
    assert fp.hosts[0].host == "serpapi.com" and fp.hosts[0].path == "" and fp.service_header == "X-Acnologia-Service"
    assert TOKEN not in repr(fp) and TOKEN not in str(fp)

    url = acn.forward_proxy_url()
    assert url == "http://app:acn_test@proxy.test:8888"
    env = acn.forward_proxy_env()
    assert env == {
        "HTTPS_PROXY": url, "HTTP_PROXY": url, "NODE_USE_ENV_PROXY": "1",
        "NODE_EXTRA_CA_CERTS": "acnologia-ca.pem", "REQUESTS_CA_BUNDLE": "acnologia-ca.pem", "SSL_CERT_FILE": "acnologia-ca.pem",
    }
    assert acn.forward_proxy_env("/etc/ca.pem")["SSL_CERT_FILE"] == "/etc/ca.pem"


def test_forward_proxy_url_escapes_token(stub: Stub) -> None:
    acn = Acnologia(BASE, "acn_a/b@c", transport=httpx.MockTransport(lambda r: httpx.Response(200, json={"proxy_url": "http://proxy.test:8888"})))
    assert acn.forward_proxy_url() == "http://app:acn_a%2Fb%40c@proxy.test:8888"


def test_forward_proxy_ca(acn: Acnologia, stub: Stub, tmp_path: Any) -> None:
    assert acn.forward_proxy_ca() == CA_PEM
    req = stub.requests[-1]
    assert req.url.path == "/api/v1/proxy/ca.pem" and "authorization" not in req.headers
    path = str(tmp_path / "ca.pem")
    assert acn.save_forward_proxy_ca(path) == path
    with open(path, encoding="utf-8") as f:
        assert f.read() == CA_PEM


class _RecordingProxy(BaseHTTPRequestHandler):
    seen: List[Dict[str, Optional[str]]] = []

    def do_GET(self) -> None:  # noqa: N802
        _RecordingProxy.seen.append({"line": self.requestline, "auth": self.headers.get("Proxy-Authorization")})
        body = b"via proxy"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args: Any) -> None:
        pass


def test_forward_proxy_client() -> None:
    srv = HTTPServer(("127.0.0.1", 0), _RecordingProxy)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        stub = Stub(proxy_url=f"http://127.0.0.1:{srv.server_address[1]}")
        acn = Acnologia(BASE, TOKEN, transport=httpx.MockTransport(stub.handler))
        with acn.forward_proxy_client(timeout=5) as hc:
            resp = hc.get("http://api.provider.test/v1/models")
        assert resp.text == "via proxy"
        seen = _RecordingProxy.seen[-1]
        assert seen["line"] == "GET http://api.provider.test/v1/models HTTP/1.1"
        assert seen["auth"] == "Basic " + base64.b64encode(b"app:" + TOKEN.encode()).decode()
    finally:
        srv.shutdown()
        srv.server_close()


async def test_async_calls(stub: Stub, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("ACNOLOGIA_ACCESS_PASSWORD", raising=False)
    async with AsyncAcnologia(BASE, TOKEN, transport=httpx.MockTransport(stub.handler), access_password="gate") as acn:
        me = await acn.me()
        assert me.app.name == "bot" and stub.requests[-1].headers["x-acnologia-access"] == "gate"
        assert (await acn.guide("serpapi")).generated is True
        assert await acn.guide_markdown("serpapi") == "# serpapi"
        assert (await acn.capabilities())[0].slug == "web-search"
        resp = await acn.capability("web-search", {"q": "a"}, service="serpapi")
        assert stub.requests[-1].url.params["_service"] == "serpapi"
        assert proxy_info(resp).tried[1] == Tried("serpapi", 200)
        resp = await acn.proxy("serpapi").get("/x", idempotency_key="z")
        assert stub.requests[-1].headers["idempotency-key"] == "z"
        assert (await acn.forward_proxy()).proxy_url == "http://proxy.test:8888"
        assert await acn.forward_proxy_ca() == CA_PEM
        assert "authorization" not in stub.requests[-1].headers
        assert (await acn.forward_proxy_env())["HTTPS_PROXY"] == "http://app:acn_test@proxy.test:8888"
        client = await acn.forward_proxy_client()
        assert isinstance(client, httpx.AsyncClient)
        await client.aclose()
        with pytest.raises(AppLimitReached):
            await acn._call("GET", "/proxy/x?fail=limit")
