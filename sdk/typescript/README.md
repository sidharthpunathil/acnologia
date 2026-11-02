# @acnologia/keys

TypeScript and JavaScript client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool.
Register many provider accounts (SerpAPI, ElevenLabs, any API key based service) in Acnologia and
let this client hand you a working key, or proxy your calls so keys rotate automatically when one
hits a rate limit.

- Zero runtime dependencies, uses `fetch`.
- Works in Node 18+, Bun, Deno, browsers, and edge runtimes.
- Ships ESM, CommonJS and type declarations.

## Install

```bash
npm install @acnologia/keys
# or
pnpm add @acnologia/keys
# or
bun add @acnologia/keys
```

Deno:

```ts
import { Acnologia } from "npm:@acnologia/keys";
```

## Setup

Create an app in the Acnologia dashboard and copy its token (`acn_...`, shown once).

```ts
import { Acnologia } from "@acnologia/keys";

const acnologia = new Acnologia({
  baseUrl: "http://localhost:8080",
  token: process.env.ACNOLOGIA_TOKEN!,
  // fetch: customFetch, // optional, defaults to globalThis.fetch
});

const services = await acnologia.services();
// [{ slug: "serpapi", name: "SerpAPI", available: true, baseUrl: "https://serpapi.com", auth: { type: "query", name: "api_key" }, ... }]
```

There are two ways to use a pool.

| Mode | You call | Secret leaves Acnologia | Rotation |
|------|----------|----------------------|----------|
| Proxy | Acnologia at `/proxy/{service}/...` | never | automatic, server side |
| Lease | the provider directly | yes, to your process | you report, `withKey` automates it |

The app must be allowed the mode you use (`proxy`, `lease` or `both`).

## Proxy mode

Call Acnologia as if it were the provider. Acnologia injects a key, retries with the next key on a
rate limit, and returns the provider's response as a standard `Response`. Non-2xx responses do
not throw, check `res.ok` yourself. The response carries `X-Acnologia-Key-Id` and
`X-Acnologia-Attempts` headers.

### SerpAPI search

```ts
const serp = acnologia.proxy("serpapi");

const res = await serp.get("/search.json", { engine: "google", q: "best espresso grinder" });
if (!res.ok) throw new Error(`SerpAPI ${res.status}: ${await res.text()}`);
const data = await res.json();
console.log(data.organic_results?.[0]?.title, "via key", res.headers.get("X-Acnologia-Key-Id"));
```

### ElevenLabs text to speech

```ts
import { writeFile } from "node:fs/promises";

const eleven = acnologia.proxy("elevenlabs");
const voiceId = "21m00Tcm4TlvDq8Ikwam";

const res = await eleven.post(`/v1/text-to-speech/${voiceId}`, {
  text: "Hello from a pooled key.",
  model_id: "eleven_multilingual_v2",
});
if (!res.ok) throw new Error(`ElevenLabs ${res.status}: ${await res.text()}`);
await writeFile("hello.mp3", new Uint8Array(await res.arrayBuffer()));
```

### Proxy API

```ts
const p = acnologia.proxy("serpapi");
p.get(path, query?, init?)
p.delete(path, query?, init?)
p.post(path, body?, init?)   // also put, patch
p.request(method, path, { query?, headers?, body?, ...RequestInit })
```

Plain objects and arrays passed as `body` are sent as JSON. Strings, `Blob`, `FormData`,
`URLSearchParams`, `ArrayBuffer`, typed arrays and `ReadableStream` are sent as is. `query`
accepts an object, `URLSearchParams` or an array of pairs.

## Lease mode

Lease a key, call the provider yourself, then report how it went. Reporting is what lets
Acnologia cool down rate limited keys and retire exhausted or invalid ones.

### withKey (recommended)

`withKey` leases a key, runs your function, and reports for you:

- function returns: reports `ok` and returns the value.
- function throws `RateLimitedError`, `QuotaExhaustedError` or `InvalidKeyError`: reports that
  outcome, gets the next key and calls your function again, up to `maxAttempts` (default 5).
- function throws anything else: reports `error` and rethrows.
- no key left: throws `NoKeyAvailableError` with `retryAfter` seconds.
- every key busy (concurrency limit): throws `AllKeysBusyError`, a subclass of
  `NoKeyAvailableError`. Pass `{ waitSec }` to wait for a slot instead.

Options: `{ maxAttempts?: number; ttlSec?: number; waitSec?: number; exclusive?: boolean }`.

#### SerpAPI search

```ts
import { Acnologia, RateLimitedError, QuotaExhaustedError, InvalidKeyError } from "@acnologia/keys";

const data = await acnologia.withKey("serpapi", async (lease) => {
  const res = await fetch(lease.url("/search.json", { engine: "google", q: "best espresso grinder" }));
  if (res.status === 429) {
    const body = await res.text();
    if (body.includes("run out of searches")) throw new QuotaExhaustedError({ statusCode: 429 });
    throw new RateLimitedError(3600, { statusCode: 429 });
  }
  if (res.status === 401) throw new InvalidKeyError({ statusCode: 401 });
  if (!res.ok) throw new Error(`SerpAPI ${res.status}`);
  return res.json();
});
```

`lease.url(path, query)` builds `baseUrl + path` and puts the secret in the query string for
services whose auth type is `query` (SerpAPI uses `api_key`).

#### ElevenLabs text to speech

```ts
const voiceId = "21m00Tcm4TlvDq8Ikwam";

const audio = await acnologia.withKey("elevenlabs", async (lease) => {
  const { headers } = lease.apply({ headers: { "Content-Type": "application/json" } });
  const res = await fetch(`${lease.baseUrl}/v1/text-to-speech/${voiceId}`, {
    method: "POST",
    headers, // includes xi-api-key
    body: JSON.stringify({ text: "Hello from a pooled key.", model_id: "eleven_multilingual_v2" }),
  });
  if (res.status === 429) {
    const err = await res.json().catch(() => null);
    if (err?.detail?.status === "system_busy") throw new Error("ElevenLabs busy");
    throw new RateLimitedError(5, { statusCode: 429 });
  }
  if (res.status === 401) {
    const err = await res.json().catch(() => null);
    if (err?.detail?.status === "quota_exceeded") throw new QuotaExhaustedError({ statusCode: 401 });
    throw new InvalidKeyError({ statusCode: 401 });
  }
  if (!res.ok) throw new Error(`ElevenLabs ${res.status}`);
  return new Uint8Array(await res.arrayBuffer());
});
```

### Manual lease and report

```ts
import { NoKeyAvailableError } from "@acnologia/keys";

try {
  const lease = await acnologia.lease("serpapi", { ttlSec: 120, waitSec: 10 });
  try {
    const res = await fetch(lease.url("/search.json", { q: "coffee" }));
    if (res.status === 429) {
      const next = await lease.report({ outcome: "rate_limited", statusCode: 429, retryAfterSec: 3600, rotate: true });
      // next is a fresh Lease (report it too), or null when no other key is free
    } else {
      await lease.report({ outcome: res.ok ? "ok" : "error", statusCode: res.status });
    }
  } finally {
    // always report, so a concurrency slot is never held until the TTL expires
    if (!lease.reported) await lease.report({ outcome: "error" });
  }
} catch (err) {
  if (err instanceof NoKeyAvailableError) {
    console.log(`pool empty, retry in ${err.retryAfter ?? "?"}s`);
  } else throw err;
}
```

Outcomes: `ok`, `rate_limited`, `quota_exhausted`, `invalid`, `error`.

### lease.apply

```ts
const { headers, query } = lease.apply({ headers: { Accept: "application/json" }, query: { q: "coffee" } });
```

Returns copies with the secret placed according to `lease.auth.type`:

| auth.type | Where the secret goes |
|-----------|-----------------------|
| `query` | `query[auth.name]` |
| `header` | `headers[auth.name]` |
| `bearer` | `headers[auth.name or "Authorization"] = "Bearer <secret>"` |

When `auth.prefix` is non empty it goes in front of the secret for every type, and for `bearer`
it replaces `Bearer ` (so `prefix: "Token "` gives `Authorization: Token <secret>`).

`headers` is a plain object, `query` is a `URLSearchParams`.

## Concurrency limits

Some providers cap how many requests one key may run at once (ElevenLabs allows 2 per key
by default). Acnologia enforces this per key, so a lease is exclusive: on a service with a
limit, each lease holds one of the key's slots until you report it or its `ttlSec` runs out
(default 120 seconds on limited services, 300 otherwise).

- **Always report.** `withKey` reports every lease, even when your function throws. With a
  manual lease, report in a `finally` block, or the slot stays taken until the TTL expires.
- **Wait instead of failing.** Pass `waitSec` (0 to 60) to `lease()` or `withKey()` and the
  server waits that long for a slot when every usable key is busy.

```ts
import { AllKeysBusyError } from "@acnologia/keys";

const audio = await acnologia.withKey("elevenlabs", speak, { waitSec: 20 });

try {
  const lease = await acnologia.lease("elevenlabs");
} catch (err) {
  if (err instanceof AllKeysBusyError) {
    // err.busy === true, err.retryAfter is usually 1
  }
}
```

`AllKeysBusyError` (503 `all_keys_busy`) means the keys are fine but in use; it clears as
soon as another caller reports. It extends `NoKeyAvailableError`, so existing
`instanceof NoKeyAvailableError` checks still catch it; use `err.busy` to tell them apart.

Proxy mode needs none of this: Acnologia queues proxied requests for up to the service's
`queue_timeout_sec` (30 seconds for ElevenLabs) and never sends more than the limit in
parallel on one key.

## Long jobs: renew and exclusive leases

`lease.renew(ttlSec?)` extends an open lease so a long job keeps its key, and updates and
returns `lease.expiresAt` (`acnologia.renew(lease, ttlSec?)` does the same). Omit `ttlSec` for
the server default (120 seconds); the server caps it at 24 hours. When the lease is no longer
open it throws `AcnologiaError` with `code: "lease_not_found"` (status 404).

`{ exclusive: true }` on `lease()` or `withKey()` reserves the key for you only: no other lease
or proxy call gets it until you report or the lease expires. `lease.exclusive` says whether you
got it.

```ts
const lease = await acnologia.lease("serpapi", { ttlSec: 600, exclusive: true });
// halfway through a long job:
await lease.renew(600);
```

## Plain JavaScript

The package works without TypeScript.

### ESM (Node, Bun, Deno, bundlers)

```js
import { Acnologia } from "@acnologia/keys";

const acnologia = new Acnologia({ baseUrl: "http://localhost:8080", token: process.env.ACNOLOGIA_TOKEN });
const res = await acnologia.proxy("serpapi").get("/search.json", { q: "coffee" });
console.log(await res.json());
```

### CommonJS (require)

```js
const { Acnologia, RateLimitedError } = require("@acnologia/keys");

const acnologia = new Acnologia({ baseUrl: "http://localhost:8080", token: process.env.ACNOLOGIA_TOKEN });

async function main() {
  const res = await acnologia.proxy("serpapi").get("/search.json", { q: "coffee" });
  console.log(res.status, await res.json());
}
main();
```

### Browser

```html
<script type="module">
  import { Acnologia } from "https://esm.sh/@acnologia/keys";
  const acnologia = new Acnologia({ baseUrl: "https://acnologia.example.com", token: "acn_..." });
</script>
```

Only ship an app token to a browser if that app is proxy only and limited to the services the
page needs. Anyone who can read the page can use the token.

## Errors

| Class | When |
|-------|------|
| `AcnologiaError` | Acnologia returned a non-2xx response. Has `status`, `code`, `body`. |
| `NoKeyAvailableError` | 503 `no_key_available`. Has `retryAfter` (seconds or null) and `busy`. Extends `AcnologiaError`. |
| `AllKeysBusyError` | 503 `all_keys_busy`: every usable key is at its concurrency limit. `busy` is true, `retryAfter` usually 1. Extends `NoKeyAvailableError`. |
| `ServiceUnavailableError` | 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open. Has `retryAfter` (seconds or null). Extends `AcnologiaError`, not `NoKeyAvailableError`. |
| `RateLimitedError(retryAfter?, { statusCode?, message? })` | You throw it inside `withKey`. |
| `QuotaExhaustedError({ statusCode?, message? })` | You throw it inside `withKey`. |
| `InvalidKeyError({ statusCode?, message? })` | You throw it inside `withKey`. |

## Development

```bash
pnpm install
pnpm test        # vitest
pnpm typecheck
pnpm build       # dist/index.js, dist/index.cjs, dist/index.d.ts
```

## License

MIT
