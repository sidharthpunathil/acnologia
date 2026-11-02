# acnologia (Python)

Python client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. Acnologia
holds many keys per provider (SerpAPI, ElevenLabs, any API key based HTTP service)
and hands out a working one. When a key hits a rate limit or runs out of quota,
you get the next one automatically.

- Sync client `Acnologia` and async client `AsyncAcnologia`, both built on `httpx` (the only dependency).
- Python 3.9+, fully typed (dataclasses, `py.typed`).

## Install

```bash
pip install acnologia
```

```python
import acnologia
```

## Configure

```python
from acnologia import Acnologia

acn = Acnologia("http://localhost:8080", "acn_your_app_token")
# or set ACNOLOGIA_URL and ACNOLOGIA_TOKEN and call Acnologia()
```

The token is an app token created in the Acnologia dashboard (Apps page). It is
sent as `Authorization: Bearer acn_...`.

## Two ways to use a key

**Proxy mode**: send the provider request to Acnologia. Acnologia injects a key,
retries with the next key on rate limits, and your process never sees a secret.

**Lease mode**: borrow a key, call the provider yourself, then report how it went.
Use this when the provider SDK needs the raw key or when you cannot route traffic
through Acnologia.

## Proxy mode

`acn.proxy(service)` returns an object with `get`, `post`, `put`, `patch`,
`delete` and `request(method, path, **httpx_kwargs)`. Each returns a plain
`httpx.Response` (non 2xx responses are returned, not raised). The response
carries `X-Acnologia-Key-Id` and `X-Acnologia-Attempts` headers.

### SerpAPI search

```python
serp = acn.proxy("serpapi")
resp = serp.get("/search.json", params={"engine": "google", "q": "best espresso grinder"})
resp.raise_for_status()
for r in resp.json().get("organic_results", [])[:5]:
    print(r["title"], r["link"])
```

### ElevenLabs text to speech

```python
tts = acn.proxy("elevenlabs")
resp = tts.post(
    "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
    json={"text": "Hello from Acnologia.", "model_id": "eleven_multilingual_v2"},
    headers={"Accept": "audio/mpeg"},
)
resp.raise_for_status()
with open("hello.mp3", "wb") as f:
    f.write(resp.content)
```

## Lease mode

`acn.lease(service, exclude_key_ids=None, ttl_sec=None, wait_sec=None)` returns a `Lease` with
`lease_id`, `key_id`, `service`, `secret`, `auth` (`type`, `name` and `prefix`),
`base_url` and `expires_at`. When every key is cooling down it raises
`NoKeyAvailable` with `retry_after` in seconds.

`lease.apply(params_or_headers)` returns a copy of the dict with the secret
placed where the provider expects it:

| `auth.type` | effect |
|---|---|
| `query` | treats the dict as query params, adds `auth.name` (SerpAPI: `api_key`) |
| `header` | treats the dict as headers, adds `auth.name` (ElevenLabs: `xi-api-key`) |
| `bearer` | sets header `auth.name` (default `Authorization`) to `Bearer <secret>` |

A non empty `auth.prefix` goes in front of the secret for every type; for
`bearer` it replaces `Bearer ` (so `"Token "` gives `Authorization: Token <secret>`).

`lease.request_kwargs(params=..., headers=...)` returns `{"params", "headers"}`
with the secret already in the right one, and `lease.url(path)` joins the
service `base_url` with a path.

### Context manager

A lease used as a context manager reports `ok` on a clean exit and an error
outcome if the block raises.

```python
import httpx
from acnologia import Acnologia, RateLimited

with acn.lease("serpapi") as lease:
    resp = httpx.get(lease.url("/search.json"), params=lease.apply({"engine": "google", "q": "coffee"}))
    if resp.status_code == 429:
        raise RateLimited(retry_after=3600, status_code=429)  # reported as rate_limited
    resp.raise_for_status()
    data = resp.json()
```

### Manual reporting and rotation

```python
lease = acn.lease("elevenlabs")
resp = httpx.post(
    lease.url("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM"),
    headers=lease.apply({"Accept": "audio/mpeg"}),
    json={"text": "Hi", "model_id": "eleven_multilingual_v2"},
)
if resp.status_code == 429:
    next_lease = lease.report("rate_limited", status_code=429, retry_after_sec=5, rotate=True)
    # next_lease is a fresh Lease on another key, or None if the pool is drained
else:
    lease.report("ok")
```

Outcomes: `ok`, `rate_limited`, `quota_exhausted`, `invalid`, `error`.

### with_key: retries with rotation

`acn.with_key(service, fn, max_attempts=5, ttl_sec=None, wait_sec=None)` leases a key, calls `fn(lease)`,
reports the result and returns what `fn` returned. Raise one of the exported
exceptions to make Acnologia cool the key down and retry with the next one:

| raise | reported outcome |
|---|---|
| `RateLimited(retry_after=None)` | `rate_limited` (key cools down for `retry_after` seconds) |
| `QuotaExhausted()` | `quota_exhausted` (key parked until its quota resets) |
| `InvalidKey()` | `invalid` (key disabled) |
| anything else | `error`, then re-raised without retry |

All three accept `message=` and `status_code=` keyword arguments.

```python
import httpx
from acnologia import Acnologia, InvalidKey, QuotaExhausted, RateLimited

acn = Acnologia("http://localhost:8080", "acn_your_app_token")

def search(lease):
    resp = httpx.get(lease.url("/search.json"), params=lease.apply({"engine": "google", "q": "coffee"}))
    if resp.status_code == 429:
        if "run out of searches" in resp.text:
            raise QuotaExhausted(status_code=429)
        raise RateLimited(retry_after=3600, status_code=429)
    if resp.status_code == 401:
        raise InvalidKey(status_code=401)
    resp.raise_for_status()
    return resp.json()

results = acn.with_key("serpapi", search)
```

ElevenLabs with `with_key`:

```python
def speak(lease):
    resp = httpx.post(
        lease.url("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM"),
        headers=lease.apply({"Accept": "audio/mpeg"}),
        json={"text": "Rotation is automatic.", "model_id": "eleven_multilingual_v2"},
    )
    if resp.status_code == 429:
        raise RateLimited(retry_after=5, status_code=429)
    if resp.status_code == 401:
        status = (resp.json().get("detail") or {}).get("status", "")
        if status == "quota_exceeded":
            raise QuotaExhausted(status_code=401)
        raise InvalidKey(status_code=401)
    resp.raise_for_status()
    return resp.content

audio = acn.with_key("elevenlabs", speak)
```

If no key is left, `with_key` raises `NoKeyAvailable` (check `.retry_after`),
or its subclass `AllKeysBusy` when every key is at its concurrency limit.
After `max_attempts` rotations it re-raises the last `RateLimited`,
`QuotaExhausted` or `InvalidKey`.

## Concurrency limits

Some providers cap how many requests one key may run at once (ElevenLabs
allows 2 per key by default). Acnologia enforces this per key, so a lease is
exclusive: on a service with a limit, each lease holds one of the key's slots
until you report it or its `ttl_sec` runs out (default 120 seconds on limited
services, 300 otherwise).

- **Always report.** `with_key` and the context manager report every lease,
  even when your code raises. If you call `lease()` yourself, call
  `lease.report(...)` in a `finally` block, or the slot stays taken until the
  TTL expires.
- **Wait instead of failing.** Pass `wait_sec` (0 to 60) and the server waits
  that long for a slot when every usable key is busy. The SDK extends its read
  timeout to cover the wait.

```python
from acnologia import AllKeysBusy

with acn.lease("elevenlabs", wait_sec=20) as lease:
    ...

audio = acn.with_key("elevenlabs", speak, wait_sec=20)

try:
    lease = acn.lease("elevenlabs")
except AllKeysBusy as exc:   # subclass of NoKeyAvailable, exc.busy is True
    time.sleep(exc.retry_after or 1)
```

`AllKeysBusy` (HTTP 503 `all_keys_busy`) means the keys are fine but in use;
it clears as soon as another caller reports. `NoKeyAvailable` with
`busy == False` means every key is cooling down, exhausted or excluded.

Proxy mode needs none of this: Acnologia queues proxied requests for up to the
service's `queue_timeout_sec` (30 seconds for ElevenLabs) and never sends more
than the limit in parallel on one key.

## Async

```python
import asyncio
from acnologia import AsyncAcnologia

async def main():
    async with AsyncAcnologia("http://localhost:8080", "acn_your_app_token") as acn:
        resp = await acn.proxy("serpapi").get("/search.json", params={"q": "coffee"})
        print(resp.json())

        async with await acn.lease("serpapi") as lease:
            ...  # reports ok on exit

        # fn may be sync or async
        data = await acn.with_key("serpapi", async_search)

asyncio.run(main())
```

## Other calls

```python
for s in acn.services():
    print(s.slug, s.available, s.auth.type, s.auth.name)
```

## Errors

- `AcnologiaError`: any non 2xx from the consumer API (`.status_code`, `.code`, `.message`).
- `NoKeyAvailable(AcnologiaError)`: HTTP 503 `no_key_available`, with `.retry_after` and `.busy`.
- `AllKeysBusy(NoKeyAvailable)`: HTTP 503 `all_keys_busy`, every usable key is at its concurrency limit. `.busy` is `True`, `.retry_after` is usually 1.
- `RateLimited`, `QuotaExhausted`, `InvalidKey`: raised by you inside `with_key` or a lease block. All subclass `KeyProblem`.

## Options

`Acnologia(base_url, token, timeout=30.0, http_client=None, transport=None)`. Pass
your own `httpx.Client` to reuse connection pools, proxies or TLS settings, or a
`transport` (for example `httpx.MockTransport`) in tests.

## Development

```bash
pip install -e '.[test]'
pytest
```
