# acnologia (Python)

Python client for [Acnologia](../../README.md), the self-hosted server that holds your API keys. Your code calls with its own `acn_` app token, and Acnologia attaches a real key, rotates to the next one on rate limits and quota errors, and enforces the limits you set.

```python
from acnologia import Acnologia

acn = Acnologia("http://localhost:8080", "acn_your_app_token")

resp = acn.proxy("weather").get("/v1/forecast", params={"city": "Lisbon"})
print(resp.status_code, resp.headers["X-Acnologia-Key-Id"])
print(resp.json())
```

That request goes to `/proxy/weather/v1/forecast?city=Lisbon`. Acnologia adds the key and sends it on, and the key never reaches your process.

- A sync client, `Acnologia`, and an async one, `AsyncAcnologia`. Both use `httpx`, the only dependency.
- Python 3.9 or later, fully typed (dataclasses, `py.typed`).

## Install

The package is not on PyPI yet. Install it from the repository:

```bash
pip install "acnologia @ git+https://github.com/sidharthpunathil/acnologia.git#subdirectory=sdk/python"
```

```python
import acnologia
```

## Authenticate

Create an app in the dashboard (Apps page) and copy its token (`acn_...`, shown once). The client sends it as `Authorization: Bearer acn_...`.

```python
acn = Acnologia("http://localhost:8080", "acn_your_app_token")

# or set ACNOLOGIA_URL and ACNOLOGIA_TOKEN and call
acn = Acnologia()
```

A missing URL or token raises `ValueError`.

The app must be allowed the mode you use (`proxy`, `lease` or `both`). A lease from a proxy only app, or on a service another Acnologia server shares with yours, raises `AcnologiaError` with `status_code == 403` and `code == "lease_not_allowed"`. Call those services in proxy mode.

## Choose a mode

| Mode | You call | The secret reaches your process | Rotation |
|---|---|---|---|
| Proxy | Acnologia at `/proxy/{service}/...` | never | automatic, on the server |
| Lease | the provider directly | yes | you report the outcome; `with_key` does it for you |

Use proxy mode unless the provider's own SDK needs the raw key.

## Proxy mode

`acn.proxy(service)` returns a `Proxy` with `get`, `post`, `put`, `patch`, `delete` and `request(method, path, **httpx_kwargs)`. Keyword arguments go straight to `httpx`, and `proxy.url(path)` returns the full proxy URL.

```python
weather = acn.proxy("weather")

resp = weather.get("/v1/forecast", params={"city": "Lisbon"})
resp = weather.post("/v1/alerts", json={"city": "Lisbon"})
resp = weather.request("DELETE", "/v1/alerts/42")
```

Each call returns a plain `httpx.Response`. A non-2xx status from the provider is returned, not raised, so call `resp.raise_for_status()` if you want an exception. Every response carries `X-Acnologia-Key-Id` (which key served it) and `X-Acnologia-Attempts`.

## Lease mode with with_key

`acn.with_key(service, fn, max_attempts=5, ttl_sec=None, wait_sec=None, exclusive=False)` leases a key, calls `fn(lease)`, reports the result and returns what `fn` returned. Raise a rotation exception from `fn` and the key is reported and `fn` runs again with the next key.

```python
import httpx
from acnologia import Acnologia, InvalidKey, QuotaExhausted, RateLimited

acn = Acnologia("http://localhost:8080", "acn_your_app_token")

def forecast(lease):
    resp = httpx.get(lease.url("/v1/forecast"), **lease.request_kwargs(params={"city": "Lisbon"}))
    if resp.status_code == 429:
        raise RateLimited(retry_after=60, status_code=429)
    if resp.status_code == 402:
        raise QuotaExhausted(status_code=402)
    if resp.status_code == 401:
        raise InvalidKey(status_code=401)
    resp.raise_for_status()
    return resp.json()

data = acn.with_key("weather", forecast)
```

Which status means what depends on the provider. Map its responses to these exceptions:

| Raise | Reported outcome |
|---|---|
| nothing | `ok` |
| `RateLimited(retry_after=None)` | `rate_limited`; the key cools down for `retry_after` seconds |
| `QuotaExhausted()` | `quota_exhausted`; the key is parked until its quota resets |
| `InvalidKey()` | `invalid`; the key is disabled |
| anything else | `error`, then re-raised without a retry |

All three accept `message=` and `status_code=` keyword arguments, and all subclass `KeyProblem`. After `max_attempts` keys, `with_key` re-raises the last one. If the pool runs out first it raises `NoKeyAvailable`, or its subclass `AllKeysBusy` when every key is at its concurrency limit.

`with_key` reports every lease, whatever happens inside `fn`. `ttl_sec`, `wait_sec` and `exclusive` apply to every lease it takes. Messages sent with a report have the secret scrubbed out and are capped at 500 characters.

## Lease and report by hand

`acn.lease(service, exclude_key_ids=None, ttl_sec=None, wait_sec=None, exclusive=False)` returns a `Lease` with `lease_id`, `key_id`, `service`, `secret`, `auth` (`type`, `name`, `prefix`), `base_url`, `expires_at`, `exclusive` and `reported`.

`ttl_sec` defaults to the server's value: 120 seconds on services with a concurrency limit, 300 otherwise.

### As a context manager

A lease used in a `with` block reports `ok` on a clean exit. If the block raises, it reports the matching outcome (`rate_limited` for `RateLimited`, `error` for anything unexpected) and lets the exception through.

```python
with acn.lease("weather") as lease:
    resp = httpx.get(lease.url("/v1/forecast"), **lease.request_kwargs(params={"city": "Lisbon"}))
    if resp.status_code == 429:
        raise RateLimited(retry_after=60, status_code=429)  # reported as rate_limited
    resp.raise_for_status()
    data = resp.json()
```

### Reporting yourself

```python
lease = acn.lease("weather")
try:
    resp = httpx.get(lease.url("/v1/forecast"), **lease.request_kwargs(params={"city": "Lisbon"}))
    if resp.status_code == 429:
        next_lease = lease.report("rate_limited", status_code=429, retry_after_sec=60, rotate=True)
        # a fresh Lease on another key (report it too), or None if the pool is drained
    else:
        lease.report("ok" if resp.is_success else "error", status_code=resp.status_code)
finally:
    if not lease.reported:
        lease.report("error")
```

`lease.report(outcome, status_code=None, retry_after_sec=None, message=None, rotate=False)` returns the next `Lease` when `rotate` is true and another key is free, otherwise `None`.

Outcomes (also exported as `OUTCOMES`): `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned`, `error`.

### Where the secret goes

- `lease.url(path)` joins the service `base_url` with a path.
- `lease.request_kwargs(params=..., headers=...)` returns `{"params", "headers"}` with the secret already in the right one, ready to splat into an `httpx` or `requests` call.
- `lease.apply(params_or_headers)` returns a copy of one dict with the secret added:

| `auth.type` | Effect |
|---|---|
| `query` | treats the dict as query params and adds `auth.name` |
| `header` | treats the dict as headers and adds `auth.name` (default `Authorization`) |
| `bearer` | sets header `auth.name` (default `Authorization`) to `Bearer <secret>` |

A non-empty `auth.prefix` goes in front of the secret for every type. For `bearer` it replaces `Bearer `, so `"Token "` gives `Authorization: Token <secret>`.

The secret is left out of a lease's `repr`.

## Concurrency limits

Some providers cap how many requests one key may run at once, and Acnologia enforces that cap per key. On a service with a limit, each lease holds one of the key's slots until you report it or its `ttl_sec` runs out.

- **Always report.** `with_key` and the context manager report every lease, even when your code raises. If you call `lease()` yourself, report in a `finally` block.
- **Wait instead of failing.** Pass `wait_sec` (0 to 60) and the server waits up to that long for a slot when every usable key is busy. The SDK extends its read timeout to cover the wait.

```python
import time
from acnologia import AllKeysBusy

with acn.lease("weather", wait_sec=20) as lease:
    ...

data = acn.with_key("weather", forecast, wait_sec=20)

try:
    lease = acn.lease("weather")
except AllKeysBusy as exc:   # subclass of NoKeyAvailable, exc.busy is True
    time.sleep(exc.retry_after or 1)
```

`AllKeysBusy` (HTTP 503 `all_keys_busy`) means the keys are fine but in use, and it clears as soon as another caller reports. `NoKeyAvailable` with `busy == False` means every key is cooling down, exhausted or excluded.

Proxy mode handles this on the server: Acnologia queues proxied requests for up to the service's `queue_timeout_sec` and never sends more than the limit in parallel on one key.

## Long jobs: renew and exclusive leases

`lease.renew(ttl_sec=None)` extends an open lease so a long job keeps its key. It updates and returns `lease.expires_at`; `acn.renew(lease, ttl_sec)` does the same. `None` uses the server default (120 seconds), and the server caps it at 24 hours. If the lease is no longer open it raises `AcnologiaError` with `code == "lease_not_found"` (HTTP 404).

`exclusive=True` on `lease()` or `with_key()` reserves the key for you only: no other lease or proxy call gets it until you report or the lease expires. `lease.exclusive` tells you whether you got it.

```python
with acn.lease("weather", ttl_sec=600, exclusive=True) as lease:
    ...  # halfway through a long job:
    lease.renew(600)
```

## Async

`AsyncAcnologia` has the same methods as coroutines. `lease()` returns an `AsyncLease`, which you use with `async with` after awaiting it, and `with_key` accepts a sync or async `fn`.

```python
import asyncio
from acnologia import AsyncAcnologia

async def main():
    async with AsyncAcnologia("http://localhost:8080", "acn_your_app_token") as acn:
        resp = await acn.proxy("weather").get("/v1/forecast", params={"city": "Lisbon"})
        print(resp.json())

        async with await acn.lease("weather") as lease:
            ...  # reports ok on exit

        data = await acn.with_key("weather", async_forecast)

asyncio.run(main())
```

## List services

```python
for s in acn.services():
    print(s.slug, s.available, s.auth.type, s.auth.name)
```

Each `Service` has `slug`, `name`, `provider`, `category`, `available`, `base_url` and `auth`.

## Errors and retries

| Exception | When |
|---|---|
| `AcnologiaError` | any non-2xx from the consumer API, such as 401, 403 `lease_not_allowed` or 404 `lease_not_found`. Has `.status_code`, `.code`, `.message` |
| `NoKeyAvailable(AcnologiaError)` | HTTP 503 `no_key_available`. Has `.retry_after` (seconds or `None`) and `.busy` |
| `AllKeysBusy(NoKeyAvailable)` | HTTP 503 `all_keys_busy`: every usable key is at its concurrency limit. `.busy` is `True`, `.retry_after` is usually 1 |
| `ServiceUnavailable(AcnologiaError)` | HTTP 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open. Has `.retry_after`. Not a `NoKeyAvailable` |
| `RateLimited`, `QuotaExhausted`, `InvalidKey` | raised by you inside `with_key` or a lease block. All subclass `KeyProblem` |

Wait `retry_after` seconds before trying again when it is set.

## Configuration

```python
Acnologia(base_url=None, token=None, *, timeout=30.0, http_client=None, transport=None)
```

- `timeout`: seconds, or an `httpx.Timeout`. Default 30.
- `http_client`: your own `httpx.Client` (`httpx.AsyncClient` for `AsyncAcnologia`) to reuse connection pools, proxies or TLS settings. The SDK does not close a client you pass in.
- `transport`: an `httpx` transport, for example `httpx.MockTransport` in tests.

Both clients work as context managers (`with` and `async with`) and close their own HTTP client on exit, or call `close()` / `aclose()`.

## Tests

```bash
python3 -m venv .venv && .venv/bin/pip install -e '.[test]' && .venv/bin/pytest
```

The suite runs offline against a mock transport. On Windows the venv lives in `.venv\Scripts`; see [the SDK index](../README.md#windows).

## More

- [All SDKs](../README.md): install lines for every language and how to run each suite
- [Request flows](../../docs/flows.md), [HTTP API and error codes](../../docs/api.md), [SDK overview](../../docs/sdks.md)
