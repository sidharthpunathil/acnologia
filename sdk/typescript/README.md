# @acnologia/keys

TypeScript and JavaScript client for [Acnologia](../../README.md), the self-hosted server that holds your API keys. Your code calls with its own `acn_` app token, and Acnologia attaches a real key, rotates to the next one on rate limits and quota errors, and enforces the limits you set.

```ts
import { Acnologia } from "@acnologia/keys";

const acnologia = new Acnologia({ baseUrl: "http://localhost:8080", token: process.env.ACNOLOGIA_TOKEN! });

const res = await acnologia.proxy("weather").get("/v1/forecast", { city: "Lisbon" });
console.log(res.status, res.headers.get("X-Acnologia-Key-Id"));
console.log(await res.json());
```

That request goes to `/proxy/weather/v1/forecast?city=Lisbon`. Acnologia adds the key and sends it on, and the key never reaches your process.

- No runtime dependencies; it uses `fetch`.
- Runs on Node 18 or later, Bun, Deno, browsers and edge runtimes.
- Ships ESM, CommonJS and type declarations.

## Install

The package is not on npm yet. Build it from a checkout and install the folder:

```bash
git clone https://github.com/sidharthpunathil/acnologia.git
cd acnologia/sdk/typescript && pnpm install && pnpm build
cd /path/to/your/app && npm install /path/to/acnologia/sdk/typescript
```

The build writes `dist/index.js` (ESM), `dist/index.cjs` (CommonJS) and the type declarations. `pnpm add` and `bun add` accept the same folder path.

## Authenticate

Create an app in the dashboard and copy its token (`acn_...`, shown once). Pass it with the server URL:

```ts
const acnologia = new Acnologia({
  baseUrl: "http://localhost:8080",
  token: process.env.ACNOLOGIA_TOKEN!,
});
```

The constructor throws a `TypeError` if either is missing. It does not read `ACNOLOGIA_URL` or `ACNOLOGIA_TOKEN` itself, so pass them in as above.

The app must be allowed the mode you use (`proxy`, `lease` or `both`). A lease from a proxy only app, or on a service another Acnologia server shares with yours, throws `AcnologiaError` with `status: 403` and `code: "lease_not_allowed"`. Call those services in proxy mode.

## Choose a mode

| Mode | You call | The secret reaches your process | Rotation |
|---|---|---|---|
| Proxy | Acnologia at `/proxy/{service}/...` | never | automatic, on the server |
| Lease | the provider directly | yes | you report the outcome; `withKey` does it for you |

Use proxy mode unless the provider's own SDK needs the raw key.

## Proxy mode

`acnologia.proxy(service)` returns a `Proxy`. Call it as if it were the provider:

```ts
const weather = acnologia.proxy("weather");

weather.get(path, query?, init?)
weather.delete(path, query?, init?)
weather.post(path, body?, init?)   // also put and patch
weather.request(method, path, { query?, headers?, body?, ...RequestInit })
```

```ts
const res = await weather.post("/v1/alerts", { city: "Lisbon" });
if (!res.ok) throw new Error(`weather ${res.status}: ${await res.text()}`);
```

- Each call returns the provider's standard `Response`. A non-2xx status does not throw, so check `res.ok`.
- Every response carries `X-Acnologia-Key-Id` (which key served it) and `X-Acnologia-Attempts`.
- Plain objects and arrays passed as `body` are sent as JSON. Strings, `Blob`, `FormData`, `URLSearchParams`, `ArrayBuffer`, typed arrays and `ReadableStream` are sent as they are.
- `query` accepts an object, `URLSearchParams` or an array of pairs. `null` and `undefined` values are dropped.
- Redirects are never followed, so your app token is not replayed to another host.
- The client's API timeout does not apply, because bodies may stream. Pass a `signal` in `init` to bound a proxy call.

## Lease mode with withKey

`withKey(service, fn, options?)` leases a key, runs your function, reports the outcome and returns what `fn` returned. Throw a rotation error from `fn` and the key is reported and `fn` runs again with the next key.

```ts
import { Acnologia, RateLimitedError, QuotaExhaustedError, InvalidKeyError } from "@acnologia/keys";

const forecast = await acnologia.withKey("weather", async (lease) => {
  const { headers } = lease.apply();
  const res = await fetch(lease.url("/v1/forecast", { city: "Lisbon" }), { headers });
  if (res.status === 429) throw new RateLimitedError(60, { statusCode: 429 });
  if (res.status === 402) throw new QuotaExhaustedError({ statusCode: 402 });
  if (res.status === 401) throw new InvalidKeyError({ statusCode: 401 });
  if (!res.ok) throw new Error(`weather ${res.status}`);
  return res.json();
});
```

`lease.url` puts the secret in the query string for `query` auth services, and `lease.apply()` returns the headers for `header` and `bearer` ones, so this works whichever the service uses.

Which status means what depends on the provider. Map its responses to these:

| `fn` does | Reported outcome | Then |
|---|---|---|
| returns | `ok` | returns the value |
| throws `RateLimitedError(retryAfter?, { statusCode?, message? })` | `rate_limited` | next key |
| throws `QuotaExhaustedError({ statusCode?, message? })` | `quota_exhausted` | next key |
| throws `InvalidKeyError({ statusCode?, message? })` | `invalid` | next key |
| throws anything else | `error` | rethrows |

All three rotation errors extend `KeyRotationError`. After `maxAttempts` keys, `withKey` throws the last one. If the pool runs out first it throws `NoKeyAvailableError`, or `AllKeysBusyError` when every key is at its concurrency limit.

`withKey` reports every lease, whatever `fn` does. Messages sent with a report have the secret scrubbed out and are capped at 500 characters.

Options: `{ maxAttempts?: number; ttlSec?: number; waitSec?: number; exclusive?: boolean }`. `maxAttempts` defaults to 5, and `ttlSec`, `waitSec` and `exclusive` apply to every lease it takes.

## Lease and report by hand

`acnologia.lease(service, options?)` returns a `Lease`. Options: `{ excludeKeyIds?: number[]; ttlSec?: number; waitSec?: number; exclusive?: boolean }`. `ttlSec` defaults to the server's value: 120 seconds on services with a concurrency limit, 300 otherwise.

```ts
import { NoKeyAvailableError } from "@acnologia/keys";

try {
  const lease = await acnologia.lease("weather", { ttlSec: 120, waitSec: 10 });
  try {
    const res = await fetch(lease.url("/v1/forecast", { city: "Lisbon" }), { headers: lease.apply().headers });
    if (res.status === 429) {
      const next = await lease.report({ outcome: "rate_limited", statusCode: 429, retryAfterSec: 60, rotate: true });
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

`lease.report({ outcome, statusCode?, retryAfterSec?, message?, rotate? })` returns the next `Lease` when `rotate` is true and another key is free, otherwise `null`.

Outcomes: `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned`, `error`.

### Where the secret goes

`Lease` has `leaseId`, `keyId`, `service`, `secret`, `auth` (`type`, `name`, `prefix`), `baseUrl`, `expiresAt`, `exclusive`, `reported` and `raw` (the server's JSON).

- `lease.url(path, query?)` builds `baseUrl + path` with the query string, and adds the secret for `query` auth.
- `lease.apply({ headers?, query? })` returns copies with the secret placed according to `lease.auth.type`. `headers` comes back as a plain object and `query` as a `URLSearchParams`.

| `auth.type` | Where the secret goes |
|---|---|
| `query` | `query[auth.name]` (default `api_key`) |
| `header` | `headers[auth.name]` (default `Authorization`) |
| `bearer` | `headers[auth.name or "Authorization"] = "Bearer <secret>"` |

A non-empty `auth.prefix` goes in front of the secret for every type. For `bearer` it replaces `Bearer `, so `prefix: "Token "` gives `Authorization: Token <secret>`.

`JSON.stringify(lease)` and `console.log` in Node show the secret as `***`, and a logged client hides its token the same way.

## Concurrency limits

Some providers cap how many requests one key may run at once, and Acnologia enforces that cap per key. On a service with a limit, each lease holds one of the key's slots until you report it or its `ttlSec` runs out.

- **Always report.** `withKey` does, even when your function throws. With a manual lease, report in a `finally` block.
- **Wait instead of failing.** Pass `waitSec` (0 to 60) to `lease()` or `withKey()` and the server waits up to that long for a slot when every usable key is busy. A value outside that range throws a `RangeError`.

```ts
import { AllKeysBusyError } from "@acnologia/keys";

const data = await acnologia.withKey("weather", getForecast, { waitSec: 20 });

try {
  const lease = await acnologia.lease("weather");
} catch (err) {
  if (err instanceof AllKeysBusyError) {
    // err.busy === true, err.retryAfter is usually 1
  }
}
```

`AllKeysBusyError` (503 `all_keys_busy`) means the keys are fine but in use, and it clears as soon as another caller reports. It extends `NoKeyAvailableError`, so `instanceof NoKeyAvailableError` still catches it; use `err.busy` to tell them apart.

Proxy mode handles this on the server: Acnologia queues proxied requests for up to the service's `queue_timeout_sec` and never sends more than the limit in parallel on one key.

## Long jobs: renew and exclusive leases

`lease.renew(ttlSec?)` extends an open lease so a long job keeps its key. It updates `lease.expiresAt` and returns the new expiry as an RFC 3339 string; `acnologia.renew(lease, ttlSec?)` does the same. Omit `ttlSec` for the server default (120 seconds); the server caps it at 24 hours. If the lease is no longer open it throws `AcnologiaError` with `code: "lease_not_found"` (status 404).

`{ exclusive: true }` on `lease()` or `withKey()` reserves the key for you only: no other lease or proxy call gets it until you report or the lease expires. `lease.exclusive` tells you whether you got it.

```ts
const lease = await acnologia.lease("weather", { ttlSec: 600, exclusive: true });
// halfway through a long job:
await lease.renew(600);
```

## Proxy options and response headers

Every proxy call takes `idempotencyKey` (sends `Idempotency-Key`) and `noCache: true` (sends `Cache-Control: no-cache`) in its init. `proxyInfo(res)` reads the Acnologia headers of the answer:

```ts
import { proxyInfo } from "@acnologia/keys";

const res = await acnologia.proxy("weather").post("/v1/alerts", alert, { idempotencyKey: "order-42" });
const info = proxyInfo(res);
console.log(info.keyId, info.attempts, info.service, info.cache, info.replayed);
```

`ProxyInfo` has `keyId` (`null` for a share's token), `attempts`, `service`, `cache` (`hit`, `miss`, ...), `replayed`, `capability` and `tried` (`[{ service, status }]`). The service passed to `proxy()` may also be a service group or a capability slug.

## Capabilities

A capability is one call served by whichever provider can.

```ts
const caps = await acnologia.capabilities(); // slug, name, description, strategy, params, routes, url
const res = await acnologia.capability("web-search", { q: "coffee" }, { service: "serpapi" }); // raw: true for the provider's own body
console.log(await res.json(), proxyInfo(res).tried);
```

`capability` also takes `idempotencyKey`, `noCache` and `signal`, and returns the `Response` as is.

## Forward proxy

Point an app's own HTTP client at Acnologia and call provider URLs as they are. Save the CA, then start the app with the environment from `forwardProxyEnv`. Node releases that support `NODE_USE_ENV_PROXY=1` route `fetch` and `http` through `HTTPS_PROXY`; the CA variables cover Node, Python and most other runtimes.

```ts
import { writeFile } from "node:fs/promises";
import { spawn } from "node:child_process";

await writeFile("acnologia-ca.pem", await acnologia.forwardProxyCA());
const env = await acnologia.forwardProxyEnv("acnologia-ca.pem");
spawn("node", ["agent.js"], { env: { ...process.env, ...env }, stdio: "inherit" });

// in agent.js, the app's own client, unchanged:
const res = await fetch("https://api.openai.com/v1/models"); // Acnologia adds the key
```

| Call | Returns |
|---|---|
| `forwardProxy()` | `ForwardProxy`: `proxyUrl`, `proxyUrlWithToken` (with an `<app token>` placeholder), `caUrl`, `caFingerprint`, `passthrough`, `hosts`, `env`, `serviceHeader` |
| `forwardProxyCA()` | the CA certificate as PEM text. Public: the token is not sent |
| `forwardProxyURL()` | `http://app:acn_...@host:8888`. Contains the token |
| `forwardProxyEnv(caPath = "acnologia-ca.pem")` | `HTTPS_PROXY`, `HTTP_PROXY`, `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` set to `caPath`. Contains the token |

## About this token

`me()` returns the token's own access: `app`, `limits` (each `null` when unset), `usage`, `limitHit`, `services` (with `available` as a count and `logoUrl`) and `share` (`null` for an ordinary app; for a share's token its `status`, still answered after the share stopped).

```ts
const me = await acnologia.me();
if (me.limitHit) console.log(me.limitHit.limit, "resets at", me.limitHit.resetAt);
```

## Usage guides

```ts
const g = await acnologia.guide("weather");          // markdown, generated, guide (plain JSON)
const md = await acnologia.guideMarkdown("weather"); // the Markdown alone
```

## List services

```ts
const services = await acnologia.services();
// [{ slug: "weather", name: "...", provider: "...", category: "...", available: true, baseUrl: "...", auth: { type: "query", name: "..." }, guideUrl: "..." }]
```

## Errors and retries

| Class | When |
|---|---|
| `AcnologiaError` | any non-2xx from the consumer API, such as 401, 403 `lease_not_allowed` or 404 `lease_not_found`. Has `status`, `code` and `body` |
| `NoKeyAvailableError` | 503 `no_key_available`. Has `retryAfter` (seconds or `null`) and `busy`. Extends `AcnologiaError` |
| `AllKeysBusyError` | 503 `all_keys_busy`: every usable key is at its concurrency limit. `busy` is true, `retryAfter` usually 1. Extends `NoKeyAvailableError` |
| `ServiceUnavailableError` | 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open. Has `retryAfter` (seconds or `null`). Extends `AcnologiaError`, not `NoKeyAvailableError` |
| `AppLimitReachedError` | 429 `app_limit_reached`: the app used up a daily or monthly request or budget limit. Has `limit`, `resetAt` and `retryAfter`. Extends `AcnologiaError` |
| `ShareUnavailableError` | 403 `share_revoked`, `share_paused` or `share_expired`. `shareStatus` is `revoked`, `paused` or `expired` (`status` stays the HTTP status). Extends `AcnologiaError` |
| `RateLimitedError`, `QuotaExhaustedError`, `InvalidKeyError` | you throw them inside `withKey` |

When the server sends no error code, `code` is `http_<status>`. Every `AcnologiaError` has `retryAfter` when the server sent `retry_after_sec` or `Retry-After`; wait that long before trying again. Other codes worth checking: `empty_group`, `bad_params`, `no_route`, `no_route_worked`, `proxy_not_allowed`, `lease_not_allowed`, `loop_detected`.

## Configuration

```ts
new Acnologia({
  baseUrl: "http://localhost:8080",
  token: "acn_...",
  fetch: customFetch,             // defaults to globalThis.fetch
  headers: { "X-Team": "data" },  // sent with every request to Acnologia
  timeoutMs: 75_000,              // lease, report, renew and services calls; 0 disables
  accessPassword: "...",          // sent as X-Acnologia-Access, for a deploy behind the access password
});
```

`timeoutMs` defaults to 75 seconds, longer than the 60 second maximum lease wait. Proxy calls are not bounded by it.

## Plain JavaScript

The package works without TypeScript.

ESM (Node, Bun, Deno, bundlers):

```js
import { Acnologia } from "@acnologia/keys";

const acnologia = new Acnologia({ baseUrl: "http://localhost:8080", token: process.env.ACNOLOGIA_TOKEN });
const res = await acnologia.proxy("weather").get("/v1/forecast", { city: "Lisbon" });
console.log(res.status, await res.json());
```

CommonJS:

```js
const { Acnologia } = require("@acnologia/keys");

const acnologia = new Acnologia({ baseUrl: "http://localhost:8080", token: process.env.ACNOLOGIA_TOKEN });

async function main() {
  const res = await acnologia.proxy("weather").get("/v1/forecast", { city: "Lisbon" });
  console.log(res.status, await res.json());
}
main();
```

### In a browser

Bundle the package with the rest of your front end. Ship an app token to a browser only if that app is proxy only and limited to the services the page needs: anyone who can read the page can use the token.

## Tests

```bash
pnpm install
pnpm test        # vitest
pnpm typecheck
pnpm build       # dist/index.js, dist/index.cjs, dist/index.d.ts
```

The suite runs offline against a stub `fetch`.

## More

- [All SDKs](../README.md): install lines for every language and how to run each suite
- [Request flows](../../docs/flows.md), [HTTP API and error codes](../../docs/api.md), [SDK overview](../../docs/sdks.md)

## License

MIT
