# Acnologia for Rust

Call the APIs your Acnologia server holds keys for, from Rust, with your app's own `acn_` token. Send requests through the proxy and the key never reaches your process. Or lease a key, call the provider yourself, and let `with_key` report each attempt and move to the next key on a rate limit, an empty quota or a rejected key.

The client is async, built on `reqwest` (rustls, no OpenSSL) and Tokio. The `blocking` feature adds a synchronous client in `acnologia::blocking`.

## Quick start

```rust
use acnologia::{Client, Error};

#[tokio::main]
async fn main() -> Result<(), Error> {
    let acn = Client::new("http://localhost:8080", "acn_your_app_token")?;
    let resp = acn
        .proxy("weather")
        .get("/v1/forecast", &[("city", "Lisbon")])
        .await?;
    println!("{}", resp.text().await?);
    Ok(())
}
```

`weather` is the slug of a service on your server. Acnologia attaches the key, sends the call on, and returns the provider's response.

## Install

The crate is not on crates.io yet. Add it as a git dependency:

```toml
[dependencies]
acnologia = { git = "https://github.com/sidharthpunathil/acnologia", package = "acnologia" }
# with the synchronous client:
# acnologia = { git = "https://github.com/sidharthpunathil/acnologia", package = "acnologia", features = ["blocking"] }
```

Cargo finds the crate in `sdk/rust` of that repository by its package name. Requires Rust 1.75 or later.

## Connect to your server

Create an app on the Apps page of the dashboard and copy its token. The SDK sends it as `Authorization: Bearer acn_...`. Tokens issued before the rename (`cog_...`) still work.

`Client::from_env()` reads the URL and token from the environment:

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | Server URL, such as `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | App token, `acn_...` |

For more options, start from `Client::builder(url, token)` or `Client::builder_from_env()`:

| Builder method | Default | Notes |
|---|---|---|
| `timeout` | 75s | Bounds API calls. It is longer than the 60s maximum lease wait. Proxy calls have no default timeout. |
| `user_agent` | `acnologia-rust/<version>` | |
| `http_client` | a client the SDK builds | Your own `reqwest::Client`, used for every call. The SDK then applies no timeout or redirect policy of its own, so set `redirect::Policy::none()` on it to keep the app token from being replayed to another host. |

## Call an API through the proxy

`acn.proxy(service)` returns a helper whose requests go to `/proxy/{service}/{path}`. The key stays on the server.

```rust
let weather = acn.proxy("weather");
let resp = weather
    .post_json("/v1/alerts", &serde_json::json!({"city": "Lisbon"}))
    .await?;
println!("{} via key {:?}", resp.status(), resp.headers().get("x-acnologia-key-id"));
```

- The upstream response comes back unchanged. A non-2xx status is an `Ok(Response)`, not an error.
- Each response carries `X-Acnologia-Key-Id`, `X-Acnologia-Attempts` and `X-Acnologia-Service`.
- Redirects are not followed.
- Proxy calls have no default timeout because bodies may stream. Use `RequestBuilder::timeout` to bound one.

Other helpers: `get(path, query)`, `post(path, content_type, body)`, `url(path, query)`, and `request(method, path)`, which returns a `reqwest::RequestBuilder` with the token already set.

## Lease a key with `with_key`

`with_key` leases a key, runs your callback, reports the outcome, and rotates when the callback returns one of the rotation errors:

| Callback returns | Reported outcome | Rotates |
|---|---|---|
| `Ok(value)` | `ok` | no |
| `Error::RateLimited { retry_after, .. }` | `rate_limited` | yes |
| `Error::QuotaExhausted { .. }` | `quota_exhausted` | yes |
| `Error::InvalidKey { .. }` | `invalid` | yes |
| any other error | `error` | no, the error is returned as is |

`acnologia::check_status(resp)` maps common provider statuses for you: 429 becomes `RateLimited` (with its `Retry-After`), 402 `QuotaExhausted`, 401 and 403 `InvalidKey`. Anything else passes through. Providers differ (some send 429 for an empty quota), so check the body yourself when it matters. `?` on a `reqwest::Error` also works and is reported as `error`.

```rust
use acnologia::{check_status, WithKeyOptions};
use std::time::Duration;

let http = reqwest::Client::new();
let body = acn
    .with_key_opts(
        "weather",
        WithKeyOptions::new().max_attempts(3).wait(Duration::from_secs(10)),
        |lease| {
            let http = http.clone();
            async move {
                let req = http.get(lease.url("/v1/forecast", &[("city", "Lisbon")]));
                let resp = check_status(lease.apply(req).send().await?)?; // apply adds the key
                Ok(resp.text().await?)
            }
        },
    )
    .await?;
```

`acn.with_key(service, f)` does the same with default options.

How it ends:

- After `max_attempts` keys (default 5) the last rotation error is returned. The final attempt is reported without asking for a next key.
- When the pool runs dry you get `Error::NoKeyAvailable` or `Error::AllKeysBusy`.
- Every lease is reported, also when the callback panics or the `with_key` future is dropped. In those two cases the report is spawned on the current Tokio runtime, best effort.
- Messages sent with a report have the secret scrubbed out.

`WithKeyOptions` takes `max_attempts`, `wait`, `ttl`, and `lease(LeaseOptions)` for the full lease options.

## Lease and report by hand

```rust
use acnologia::{LeaseOptions, Report};
use std::time::Duration;

let lease = acn
    .lease("weather", LeaseOptions::new().ttl(Duration::from_secs(300)).wait(Duration::from_secs(10)))
    .await?;
let resp = lease
    .apply(http.get(lease.url("/v1/forecast", &[("city", "Lisbon")])))
    .send()
    .await?;
if resp.status() == 429 {
    let next = lease
        .report(Report::rate_limited(Some(Duration::from_secs(3600))).status(429).rotate())
        .await?;
    // retry with `next` when it is Some
} else {
    lease.report(Report::ok()).await?;
}
```

Report on every path. On services with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out. `lease.renew(Some(ttl))` extends an open lease for a long job (`None` uses the server default of 120s) and returns the new expiry. Renewing a lease that is no longer open fails with `Error::Api`, code `lease_not_found`.

A lease fails with `Error::Forbidden`, code `lease_not_allowed`, when the app is proxy only or the service is shared to you by another Acnologia server. Call those services through the proxy.

**`LeaseOptions`**: `ttl`, `wait` (up to `acnologia::MAX_WAIT`, 60s, for a free slot when every key is busy), `exclude` (key ids to skip) and `exclusive` (keep the key from every other caller until you report).

**`Lease` fields**: `lease_id`, `key_id`, `service`, `auth { kind, name, prefix }`, `base_url`, `expires_at`, `exclusive`, and `secret()`. `Lease` derefs to `LeaseInfo`, which holds them.

**Placing the key**: `apply(request_builder)` puts the secret where `auth.kind` says:

| `auth.kind` | Where the secret goes |
|---|---|
| `Query` | query parameter `auth.name` |
| `Header` | header `auth.name` |
| `Bearer` | header `auth.name` (default `Authorization`) as `Bearer <secret>` |

A non-empty `auth.prefix` goes in front of the secret for every kind, and for bearer it replaces `Bearer `. `credential()` returns the header or query pair if you build requests another way, and `apply_to(&mut reqwest::Request)` works on a built request.

**Outcomes** for `Report::new` or the shortcuts: `Ok`, `RateLimited`, `QuotaExhausted`, `Invalid`, `Error`, `Busy` (the account is at its concurrency cap) and `Banned` (the provider suspended the account). `Report` also takes `status`, `retry_after`, `message` and `rotate`.

## List services

```rust
for s in acn.services().await? {
    println!("{} available={} auth={:?}", s.slug, s.available, s.auth.kind);
}
```

## Handle errors

Every call returns `acnologia::Error`:

| Variant | When |
|---|---|
| `NoKeyAvailable { retry_after, message }` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusy { retry_after, message }` | 503 `all_keys_busy`: every usable key is at its concurrency limit. Retry after `retry_after` (usually 1s) or lease with a wait |
| `ServiceUnavailable { retry_after, message }` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `Unauthenticated { code, message }` | 401: token missing, wrong, revoked or expired |
| `Forbidden { code, message }` | 403: app disabled, or `service_not_allowed`, `lease_not_allowed`, `proxy_not_allowed` |
| `Api { status, code, message }` | any other non-2xx from the consumer API, such as 404 `lease_not_found` or 409 `report_failed` |
| `RateLimited`, `QuotaExhausted`, `InvalidKey` | you return them from `with_key` |
| `Http`, `Decode`, `InvalidInput`, `Other` | transport failure, bad response body, bad argument, your own error |

Helpers: `is_no_key_available()` (true for both no key and busy), `is_busy()`, `is_rotation()`, `retry_after()`, `status()` and `code()`.

```rust
match acn.lease("weather", LeaseOptions::new()).await {
    Err(e) if e.is_no_key_available() => {
        tokio::time::sleep(e.retry_after().unwrap_or(Duration::from_secs(1))).await
    }
    other => { /* ... */ }
}
```

## Keep secrets out of logs

`Lease` and `LeaseInfo` print `[redacted]` in place of the secret in both `Debug` and `Display`. `Client` does the same for the token, and `Credential` redacts its value. Header values the SDK sets are marked sensitive. The SDK does no logging.

## Use the blocking client

With `features = ["blocking"]`, `acnologia::blocking::Client` has the same methods without `async`. Its `with_key` callback takes `&Lease` and returns `Result<T, Error>`. When the callback panics, the lease is reported `error` and the panic resumes. Do not use the blocking client inside an async runtime.

```rust
let acn = acnologia::blocking::Client::from_env()?;
let id = acn.with_key("weather", |lease| Ok(lease.key_id))?;
```

## Run the tests

From `sdk/rust`:

```sh
cargo test --all-features
cargo clippy --all-features --all-targets -- -D warnings
cargo fmt --check
```

## More

- [SDK overview](../README.md): every language, install commands, test commands
- [Consumer API reference](../../docs/api.md)
- [Acnologia README](../../README.md)
