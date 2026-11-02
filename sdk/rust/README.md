# acnologia (Rust)

Rust client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `with_key` runs that loop and rotates keys for you.

The client is async, built on `reqwest` (rustls, no OpenSSL) and Tokio. The
`blocking` feature adds a synchronous client in `acnologia::blocking`.

## Install

Until the crate is on crates.io, add it as a git dependency:

```toml
[dependencies]
acnologia = { git = "https://github.com/sidharthpunathil/acnologia", package = "acnologia" }
# synchronous client too:
# acnologia = { git = "https://github.com/sidharthpunathil/acnologia", package = "acnologia", features = ["blocking"] }
```

Cargo finds the crate in `sdk/rust` of that repository by its package name.
Requires Rust 1.75+.

## Quick start

```rust
use acnologia::{Client, Error};

#[tokio::main]
async fn main() -> Result<(), Error> {
    let acn = Client::new("http://localhost:8080", "acn_your_app_token")?;
    let resp = acn
        .proxy("serpapi")
        .get("/search.json", &[("engine", "google"), ("q", "coffee")])
        .await?;
    println!("{}", resp.text().await?);
    Ok(())
}
```

The token is an app token from the Apps page of the dashboard. It is sent as
`Authorization: Bearer acn_...`; tokens issued before the rename (`cog_...`)
still work.

## Environment variables

`Client::from_env()` reads:

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | Server URL, such as `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | App token, `acn_...` |

`Client::builder(url, token)` (or `Client::builder_from_env()`) sets a
`timeout` for API calls (default 75s, longer than the 60s maximum lease
wait), a `user_agent` (default `acnologia-rust/<version>`), or your own
`reqwest::Client` with `http_client`.

## Proxy mode

`acn.proxy(service)` returns a helper whose requests go to
`/proxy/{service}/{path}`. Upstream responses come back unchanged: a non-2xx
status is an `Ok(Response)`, not an error. Each response carries
`X-Acnologia-Key-Id` and `X-Acnologia-Attempts`. Redirects are not followed, and
proxy calls have no default timeout because bodies may stream.

```rust
let tts = acn.proxy("elevenlabs");
let audio = tts
    .post_json(
        "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
        &serde_json::json!({"text": "Hello from Acnologia", "model_id": "eleven_multilingual_v2"}),
    )
    .await?
    .bytes()
    .await?;
```

Other helpers: `get(path, query)`, `post(path, content_type, body)`,
`url(path, query)`, and `request(method, path)`, which returns a
`reqwest::RequestBuilder` with the token already set.

## Lease mode

### with_key (recommended)

Return one of the rotation errors from your callback to move to the next key:

| Callback returns | Reported outcome | Rotates |
|---|---|---|
| `Ok(value)` | `ok` | no |
| `Error::RateLimited { retry_after, .. }` | `rate_limited` | yes |
| `Error::QuotaExhausted { .. }` | `quota_exhausted` | yes |
| `Error::InvalidKey { .. }` | `invalid` | yes |
| any other error | `error` | no, the error is returned as is |

After `max_attempts` keys (default 5) the last rotation error is returned; the
final attempt is reported without asking for a next key. When the pool runs
dry you get `Error::NoKeyAvailable` or `Error::AllKeysBusy`. Messages sent with
a report have the secret scrubbed out.

`acnologia::check_status(resp)` maps common provider statuses for you: 429
becomes `RateLimited` (with its `Retry-After`), 402 `QuotaExhausted`, 401 and
403 `InvalidKey`; anything else passes through. `?` on a `reqwest::Error`
works too and is reported as `error`.

```rust
use acnologia::{check_status, WithKeyOptions};
use std::time::Duration;

let http = reqwest::Client::new();
let body = acn
    .with_key_opts("serpapi", WithKeyOptions::new().max_attempts(3).wait(Duration::from_secs(10)), |lease| {
        let http = http.clone();
        async move {
            let req = http.get(lease.url("/search.json", &[("engine", "google"), ("q", "coffee")]));
            let resp = check_status(lease.apply(req).send().await?)?; // adds ?api_key=<secret>
            Ok(resp.text().await?)
        }
    })
    .await?;
```

Every lease is reported, also when the callback panics or the `with_key`
future is dropped (then the report is spawned on the current Tokio runtime).

### Lease and report by hand

```rust
use acnologia::{LeaseOptions, Report};

let lease = acn
    .lease("serpapi", LeaseOptions::new().ttl(Duration::from_secs(300)).wait(Duration::from_secs(10)))
    .await?;
let resp = lease.apply(http.get(lease.url("/search.json", &[("q", "coffee")]))).send().await?;
if resp.status() == 429 {
    let next = lease
        .report(Report::rate_limited(Some(Duration::from_secs(3600))).status(429).rotate())
        .await?;
    // retry with `next` when it is Some
} else {
    lease.report(Report::ok()).await?;
}
```

Report on every path: on services with a concurrency limit an unreported lease
holds one of the key's slots until its TTL runs out. `lease.renew(Some(ttl))`
extends an open lease for long jobs.

`Lease` fields: `lease_id`, `key_id`, `service`, `auth { kind, name, prefix }`,
`base_url`, `expires_at`, `exclusive`, and `secret()`. `apply(request_builder)`
places the secret according to `auth.kind`: `Query` adds the query parameter
`auth.name`, `Header` sets header `auth.name`, `Bearer` sets `auth.name`
(default `Authorization`) to `Bearer <secret>`. A non empty `auth.prefix` goes
in front of the secret for every type and, for bearer, replaces `Bearer `.
`credential()` returns the header or query pair if you build requests another
way.

Outcomes: `Ok`, `RateLimited`, `QuotaExhausted`, `Invalid`, `Error`, `Busy`
(the account is at its concurrency cap) and `Banned` (the provider suspended
the account).

`LeaseOptions`: `ttl`, `wait` (up to `acnologia::MAX_WAIT`, 60s, for a free slot
when every key is busy), `exclude` and `exclusive`.

## Services

```rust
for s in acn.services().await? {
    println!("{} available={} auth={:?}", s.slug, s.available, s.auth.kind);
}
```

## Errors

Every call returns `acnologia::Error`:

| Variant | When |
|---|---|
| `NoKeyAvailable { retry_after, message }` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusy { retry_after, message }` | 503 `all_keys_busy`: every usable key is at its concurrency limit; retry after `retry_after` (usually 1s) or lease with a wait |
| `ServiceUnavailable { retry_after, message }` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `Unauthenticated { code, message }` | 401: token missing, wrong, revoked or expired |
| `Forbidden { code, message }` | 403: app disabled, or `service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed` |
| `Api { status, code, message }` | any other non-2xx from the consumer API, such as 404 `lease_not_found` or 409 `report_failed` |
| `RateLimited`, `QuotaExhausted`, `InvalidKey` | you return them from `with_key` |
| `Http`, `Decode`, `InvalidInput`, `Other` | transport failure, bad response body, bad argument, your own error |

Helpers: `is_no_key_available()` (true for both no key and busy),
`is_busy()`, `retry_after()`, `status()` and `code()`.

```rust
match acn.lease("serpapi", LeaseOptions::new()).await {
    Err(e) if e.is_no_key_available() => tokio::time::sleep(e.retry_after().unwrap_or(Duration::from_secs(1))).await,
    other => { /* ... */ }
}
```

## Secrets

`Lease` and `LeaseInfo` print `[redacted]` in place of the secret in both
`Debug` and `Display`, `Client` does the same for the token, and `Credential`
redacts its value. Header values the SDK sets are marked sensitive. The SDK
does no logging.

## Blocking

With `features = ["blocking"]`, `acnologia::blocking::Client` has the same
methods without `async`. Its `with_key` callback takes `&Lease` and returns
`Result<T, Error>`. Do not use it inside an async runtime.

```rust
let acn = acnologia::blocking::Client::from_env()?;
let n = acn.with_key("serpapi", |lease| Ok(lease.key_id))?;
```

## Tests

```sh
cargo test --all-features
cargo clippy --all-features --all-targets -- -D warnings
cargo fmt --check
```
