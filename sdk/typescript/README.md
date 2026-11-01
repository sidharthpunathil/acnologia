# @cogito/keys

TypeScript and JavaScript client for [Cogito](../../docs/llms.txt), a self-hosted API key pool.
Register many provider accounts (SerpAPI, ElevenLabs, any API key based service) in Cogito and
let this client hand you a working key, or proxy your calls so keys rotate automatically when one
hits a rate limit.

- Zero runtime dependencies, uses `fetch`.
- Works in Node 18+, Bun, Deno, browsers, and edge runtimes.
- Ships ESM, CommonJS and type declarations.

## Install

```bash
npm install @cogito/keys
# or
pnpm add @cogito/keys
# or
bun add @cogito/keys
```

Deno:

```ts
import { Cogito } from "npm:@cogito/keys";
```

## Setup

Create an app in the Cogito dashboard and copy its token (`cog_...`, shown once).

```ts
import { Cogito } from "@cogito/keys";

const cogito = new Cogito({
  baseUrl: "http://localhost:8080",
  token: process.env.COGITO_TOKEN!,
  // fetch: customFetch, // optional, defaults to globalThis.fetch
});

const services = await cogito.services();
// [{ slug: "serpapi", name: "SerpAPI", available: true, baseUrl: "https://serpapi.com", auth: { type: "query", name: "api_key" }, ... }]
```

There are two ways to use a pool.

| Mode | You call | Secret leaves Cogito | Rotation |
|------|----------|----------------------|----------|
| Proxy | Cogito at `/proxy/{service}/...` | never | automatic, server side |
| Lease | the provider directly | yes, to your process | you report, `withKey` automates it |

The app must be allowed the mode you use (`proxy`, `lease` or `both`).

## Proxy mode

Call Cogito as if it were the provider. Cogito injects a key, retries with the next key on a
rate limit, and returns the provider's response as a standard `Response`. Non-2xx responses do
not throw, check `res.ok` yourself. The response carries `X-Cogito-Key-Id` and
`X-Cogito-Attempts` headers.

### SerpAPI search

```ts
const serp = cogito.proxy("serpapi");

const res = await serp.get("/search.json", { engine: "google", q: "best espresso grinder" });
if (!res.ok) throw new Error(`SerpAPI ${res.status}: ${await res.text()}`);
const data = await res.json();
console.log(data.organic_results?.[0]?.title, "via key", res.headers.get("X-Cogito-Key-Id"));
```

### ElevenLabs text to speech

```ts
import { writeFile } from "node:fs/promises";

const eleven = cogito.proxy("elevenlabs");
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
const p = cogito.proxy("serpapi");
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
Cogito cool down rate limited keys and retire exhausted or invalid ones.

### withKey (recommended)

`withKey` leases a key, runs your function, and reports for you:

- function returns: reports `ok` and returns the value.
- function throws `RateLimitedError`, `QuotaExhaustedError` or `InvalidKeyError`: reports that
  outcome, gets the next key and calls your function again, up to `maxAttempts` (default 5).
- function throws anything else: reports `error` and rethrows.
- no key left: throws `NoKeyAvailableError` with `retryAfter` seconds.

#### SerpAPI search

```ts
import { Cogito, RateLimitedError, QuotaExhaustedError, InvalidKeyError } from "@cogito/keys";

const data = await cogito.withKey("serpapi", async (lease) => {
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

const audio = await cogito.withKey("elevenlabs", async (lease) => {
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
import { NoKeyAvailableError } from "@cogito/keys";

try {
  let lease = await cogito.lease("serpapi", { ttlSec: 120 });
  const res = await fetch(lease.url("/search.json", { q: "coffee" }));
  if (res.status === 429) {
    const next = await lease.report({ outcome: "rate_limited", statusCode: 429, retryAfterSec: 3600, rotate: true });
    // next is a fresh Lease, or null when no other key is free
  } else {
    await lease.report({ outcome: res.ok ? "ok" : "error", statusCode: res.status });
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

`headers` is a plain object, `query` is a `URLSearchParams`.

## Plain JavaScript

The package works without TypeScript.

### ESM (Node, Bun, Deno, bundlers)

```js
import { Cogito } from "@cogito/keys";

const cogito = new Cogito({ baseUrl: "http://localhost:8080", token: process.env.COGITO_TOKEN });
const res = await cogito.proxy("serpapi").get("/search.json", { q: "coffee" });
console.log(await res.json());
```

### CommonJS (require)

```js
const { Cogito, RateLimitedError } = require("@cogito/keys");

const cogito = new Cogito({ baseUrl: "http://localhost:8080", token: process.env.COGITO_TOKEN });

async function main() {
  const res = await cogito.proxy("serpapi").get("/search.json", { q: "coffee" });
  console.log(res.status, await res.json());
}
main();
```

### Browser

```html
<script type="module">
  import { Cogito } from "https://esm.sh/@cogito/keys";
  const cogito = new Cogito({ baseUrl: "https://cogito.example.com", token: "cog_..." });
</script>
```

Only ship an app token to a browser if that app is proxy only and limited to the services the
page needs. Anyone who can read the page can use the token.

## Errors

| Class | When |
|-------|------|
| `CogitoError` | Cogito returned a non-2xx response. Has `status`, `code`, `body`. |
| `NoKeyAvailableError` | 503 `no_key_available`. Has `retryAfter` (seconds or null). Extends `CogitoError`. |
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
