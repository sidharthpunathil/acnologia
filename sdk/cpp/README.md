# Acnologia for C++

Call APIs with the keys your Acnologia server holds, using your app's own `acn_` token. In proxy mode the server attaches the key, moves to the next key on rate limits and quota errors, and the secret never reaches your process. In lease mode you borrow a key for code that needs it raw, and `with_key` reports and rotates for you.

C++17, libcurl for HTTP. nlohmann/json is used inside the library only (fetched by CMake, not exposed in the headers).

## Quick start

```cpp
#include <acnologia/acnologia.hpp>
#include <iostream>

int main() {
    acnologia::Client client("http://localhost:8080", "acn_your_app_token");

    auto resp = client.proxy("agi-labs").post("/v1/chat", R"({"prompt":"hello"})");
    std::cout << resp.status << " " << resp.body << "\n";
}
```

`agi-labs` is the slug of a service on your server. The request goes to `http://localhost:8080/proxy/agi-labs/v1/chat`.

## Install

You need CMake 3.16 or later, a C++17 compiler and libcurl. libcurl ships with macOS. On Debian or Ubuntu install `libcurl4-openssl-dev`. On Windows use vcpkg (`vcpkg install curl`) and pass its toolchain file to CMake.

With FetchContent:

```cmake
include(FetchContent)
FetchContent_Declare(acnologia
  GIT_REPOSITORY https://github.com/sidharthpunathil/acnologia.git
  GIT_TAG        main          # pin a tag or commit in real projects
  SOURCE_SUBDIR  sdk/cpp)
FetchContent_MakeAvailable(acnologia)

target_link_libraries(my_app PRIVATE acnologia::acnologia)
```

From a checkout, use `add_subdirectory(path/to/acnologia/sdk/cpp)` and the same `target_link_libraries`.

Tests and install rules are on only when `sdk/cpp` is the top level project (`ACNOLOGIA_BUILD_TESTS` and `ACNOLOGIA_INSTALL`). Built that way, `cmake --install` installs a package, so `find_package(acnologia)` works.

## Connect

Create an app in the dashboard and copy its token (`acn_...`, shown once. The older `cog_` prefix is still accepted).

```cpp
acnologia::Client client("http://localhost:8080", "acn_...");

// or from the environment
auto from_env = acnologia::Client::from_env();
```

The environment variables:

| Variable | Used for |
|---|---|
| `ACNOLOGIA_URL` | The server URL, when you pass an empty one |
| `ACNOLOGIA_TOKEN` | The app token, when you pass an empty one |
| `ACNOLOGIA_ACCESS_PASSWORD` | Optional. Sent as `X-Acnologia-Access`, but only to the server in `ACNOLOGIA_ACCESS_URL`, or in `ACNOLOGIA_URL` when that is unset |

The constructor and `from_env` throw `std::invalid_argument` when the URL or the token is still empty. Both take an optional `ClientOptions`:

| Field | Default | Meaning |
|---|---|---|
| `transport` | libcurl | A `std::shared_ptr<acnologia::Transport>`, see [Custom transport](#custom-transport) |
| `user_agent` | `acnologia-cpp/0.1.0` | The `User-Agent` header |
| `timeout` | 75 seconds | For API calls (services, me, guide, lease, report, renew). Longer than the 60 second maximum lease wait, and a longer `wait` stretches it. Zero disables it. Proxy calls have no timeout |
| `access_password` | from the environment | The server's access password, sent as `X-Acnologia-Access` on every call. The consumer routes work without it, so you rarely need it |
| `throw_proxy_errors` | `false` | Throw typed errors for Acnologia's own answers on the proxy, see [Proxy errors](#proxy-errors) |

## Your access

`client.me()` calls `GET /api/v1/me` and returns a `Me`:

```cpp
acnologia::Me me = client.me();
std::cout << me.app.name << " used " << me.usage.today_requests << " requests today\n";
if (me.limit_hit) std::cout << "limit " << me.limit_hit->limit << " resets " << me.limit_hit->reset_at << "\n";
if (me.share && !me.share->active()) std::cout << "share is " << me.share->status << "\n";
```

| Field | Holds |
|---|---|
| `app` | `id` and `name` |
| `limits` | `daily_requests`, `monthly_requests`, `daily_budget`, `monthly_budget`. An empty optional means no limit |
| `usage` | `today_requests`, `today_cost`, `month_requests`, `month_cost`, `last_used_at` and `limit_hit` |
| `limit_hit` | The first limit reached, with `limit` and `reset_at`, or empty |
| `services` | `slug`, `name`, `provider`, `category`, `available` and `logo_url` |
| `share` | Only for a share's token: `id`, `name`, `owner{name, id}`, `recipient`, `label`, `status`, `mode` and `ends_at` |

A share's token whose share is paused, expired or revoked still gets an answer here, with that `status` and no services.

`client.services()` returns the services this token may use (`GET /api/v1/services`), each with `slug`, `name`, `provider`, `category`, `available`, `base_url`, `auth{type, name, prefix}` and `guide_url`.

`client.guide("agi-labs")` returns the service's usage guide as Markdown. It throws `Forbidden` for a service the token may not use.

## Proxy mode

```cpp
auto agi = client.proxy("agi-labs");

auto resp = agi.post("/v1/chat", R"({"prompt":"hello"})");
std::cout << resp.status << " key " << resp.header("X-Acnologia-Key-Id") << "\n";

agi.get("/v1/models", {{"limit", "10"}});
agi.patch("/v1/chats/42", R"({"title":"notes"})");
agi.del("/v1/chats/42");
```

| Call | Does |
|---|---|
| `get(path, query, headers)` | GET |
| `post(path, body, content_type, headers)` | POST, `content_type` defaults to `application/json` |
| `put(path, body, content_type, headers)` | PUT |
| `patch(path, body, content_type, headers)` | PATCH |
| `del(path, query, headers)` | DELETE |
| `post_idempotent(path, body, key, content_type, headers)` | POST with an `Idempotency-Key`, see below |
| `request(method, path, body, headers, query)` | Any method |
| `url(path, query)` | The full proxy URL, for your own HTTP client (send `Authorization: Bearer <token>` with it) |
| `check(resp)` | Throws the typed error when `resp` is one of Acnologia's own errors |

The client also has the shorthands `proxy_url`, `proxy_get` and `proxy_request`.

Every call returns an `HttpResponse` (`status`, `headers`, `body`, `header(name)`, `retry_after()`, `idempotent_replayed()`) as the provider sent it. A non 2xx status is not an exception. Redirects are never followed, so the token is not replayed to another host. The body is read fully into memory. The SDK sets `Authorization` and `User-Agent` itself and sends your other headers as given.

### Idempotent POSTs

A POST that creates something should not run twice when you retry it. Send it with an `Idempotency-Key`:

```cpp
auto resp = agi.post_idempotent("/v1/chat", R"({"prompt":"hello"})", "order-1234");
if (resp.idempotent_replayed()) std::cout << "the server replayed the first answer\n";
```

The proxy stores the answer for that key and replays it for a repeated key, with `Idempotent-Replayed: true`. Leave the key empty to get a random one from `acnologia::new_idempotency_key()`. When the connection fails, `post_idempotent` sends the request once more with the same key, which is safe. A key over 200 bytes throws `std::invalid_argument`. You can also set the `Idempotency-Key` header yourself on any call.

### Proxy errors

Acnologia answers some proxy calls itself: no key free, an app limit reached, a share that ended, a loop between servers. By default these come back as responses like any other. Set `ClientOptions::throw_proxy_errors` to get them as the typed errors in [Errors](#errors) instead:

```cpp
acnologia::ClientOptions opts;
opts.throw_proxy_errors = true;
acnologia::Client client("http://localhost:8080", "acn_...", opts);

try {
    client.proxy("agi-labs").post("/v1/chat", R"({"prompt":"hello"})");
} catch (const acnologia::AppLimitReached& e) {
    std::cout << e.limit() << " resets " << e.reset_at() << "\n";
}
```

The provider's own errors are never thrown. `acnologia::is_acnologia_error(resp)` tells the two apart: Acnologia's errors carry one of its error codes at the documented status and no `X-Acnologia-Key-Id` header. `proxy.check(resp)` and `acnologia::throw_if_acnologia_error(resp)` throw for one response.

## Lease mode

Use a lease when a library insists on holding the raw key.

```cpp
acnologia::LeaseOptions opts;
opts.ttl = std::chrono::minutes(5);
opts.wait = std::chrono::seconds(10);
acnologia::Lease lease = client.lease("agi-labs", opts);

std::string url = lease.url("/v1/chat");
auto a = lease.apply();  // the header or query parameter that carries the key
// call the provider, then report on every path:
lease.report(acnologia::Outcome::ok);
```

`LeaseOptions` fields:

- `exclude_key_ids`: keys that must not be returned.
- `ttl`: the lease lifetime. Zero uses the server default: 120 seconds on services with a concurrency limit, 300 otherwise.
- `wait`: up to 60 seconds to wait for a free slot when every usable key is busy. Zero fails fast with `AllKeysBusy`. More than 60 throws `std::invalid_argument`.
- `exclusive`: no other caller gets the key until you report or it expires.

`Lease` accessors: `lease_id()`, `key_id()`, `service()`, `secret()`, `auth()`, `base_url()`, `expires_at()` (RFC 3339), `exclusive()` and `reported()`.

A lease on a proxy only service, or from an app in proxy mode, throws `LeaseNotAllowed`. Call those services in proxy mode.

### Put the key on the request

`lease.apply()` returns an `AppliedAuth` with `location` (header or query), `name` and `value`, so it works with any HTTP library:

```cpp
auto a = lease.apply();
if (a.is_header()) headers = curl_slist_append(headers, a.header_line().c_str());
else url += "&" + a.name + "=" + acnologia::url_encode(a.value);
```

`query` auth uses the parameter `auth.name`, `header` auth the header `auth.name`, and `bearer` auth the header `auth.name` (default `Authorization`) with `Bearer <secret>`. A non empty `auth.prefix` goes in front of the secret for every type and replaces `Bearer ` for bearer.

`lease.url(path, query)` joins the provider base URL without the key. `lease.signed_url(path, query)` also adds the key for query auth.

### Report the outcome

```cpp
acnologia::Report r;
r.outcome = acnologia::Outcome::rate_limited;
r.status_code = 429;
r.retry_after = provider_response.retry_after();  // the provider's Retry-After, if any
r.rotate = true;  // ask for the next key in the same call
if (auto next = lease.report(r)) {
    // retry with *next
}
```

Outcomes: `ok`, `rate_limited`, `quota_exhausted`, `invalid`, `error`, `busy` (the account is at its concurrency cap) and `banned` (the provider suspended the account). `lease.report(outcome, status_code, rotate)` is the shorthand. With `rotate` set, `report` returns the next lease, or `std::nullopt` when no other key is free. The secret is scrubbed from `message`, and the message is cut to 500 bytes.

`acnologia::parse_retry_after(value)` reads a `Retry-After` value in seconds or as an HTTP date.

On services with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out, so report every lease or use `with_key`.

### Renew for long jobs

`lease.renew(ttl)` extends an open lease and returns and stores the new `expires_at`. Zero uses the server default of 120 seconds, and the server caps it at 24 hours. A lease that is no longer open throws `ApiError` with code `lease_not_found`. When the app may no longer hold the key (its lease mode or the service was taken away, or the key was disabled) it throws `LeaseRevoked`. `client.report(lease, r)` and `client.renew(lease, ttl)` do the same as the lease methods.

## with_key

`with_key` leases a key, calls your function with it, reports the outcome and returns your function's value:

```cpp
std::string body = client.with_key("agi-labs", [](acnologia::Lease& lease) {
    auto a = lease.apply();
    auto [status, text] = my_http_post(lease.url("/v1/chat"), a, R"({"prompt":"hello"})");  // any HTTP library
    if (status == 429) throw acnologia::RateLimited(std::chrono::minutes(1), 429);
    if (status == 402) throw acnologia::QuotaExhausted(402);
    if (status == 401) throw acnologia::InvalidKey(401);
    if (status >= 400) throw std::runtime_error("agi-labs: http " + std::to_string(status));
    return text;
});
```

Map the provider's responses to these exceptions to rotate:

| Your function | Reported as | Then |
|---|---|---|
| returns | `ok` | the return value is passed back |
| throws `acnologia::RateLimited(retry_after, status, message)` | `rate_limited` | retries with the next key |
| throws `acnologia::QuotaExhausted(status, message)` | `quota_exhausted` | retries with the next key |
| throws `acnologia::InvalidKey(status, message)` | `invalid` | retries with the next key |
| throws anything else | `error` | rethrows it unchanged |

`WithKeyOptions` has `max_attempts` (default 5) and `lease` (`LeaseOptions`. `ttl`, `wait` and `exclusive` apply to every lease it takes). After `max_attempts` keys the last rotation exception is rethrown. The final attempt is reported without asking for a next key. When a report has no next key, `with_key` leases again, excluding every key it tried. Every lease is reported, whatever your function does, and the secret is scrubbed from any reported message.

## Errors

Everything derives from `acnologia::Error`, a `std::runtime_error`.

| Type | When |
|---|---|
| `ApiError` (`status()`, `code()`, `message()`, `retry_after()`) | Any non 2xx from the consumer API without a more specific type below |
| `NoKeyAvailable` (`busy()`) | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusy` (derives from `NoKeyAvailable`) | 503 `all_keys_busy`: every usable key is at its concurrency limit. Retry after `retry_after()` or lease with `wait` |
| `ServiceUnavailable` | 503 `service_unavailable`: the provider's circuit breaker is open |
| `AppLimitReached` (`limit()`, `reset_at()`) | 429 `app_limit_reached`: the app used up a daily or monthly request or budget limit |
| `Unauthenticated` | 401: token missing or wrong |
| `Forbidden` | 403: app disabled, or not allowed this service or mode |
| `LeaseNotAllowed` (derives from `Forbidden`) | 403 `lease_not_allowed`: the service is proxy only for this token |
| `LeaseRevoked` (derives from `Forbidden`) | 403 `lease_revoked` from renew: the lease has ended |
| `ShareRevoked`, `SharePaused`, `ShareExpired` | 403 `share_revoked`, `share_paused`, `share_expired`: the owner ended or paused the share. They derive from `ShareUnavailable` (`share_status()`), a `Forbidden` |
| `UpstreamUnreachable` | 502 `upstream_unreachable`: the provider could not be reached |
| `UpstreamBlocked` | 502 `upstream_blocked`: the service points at a link-local or metadata address |
| `LoopDetected` | 508 `loop_detected`: the request passed through more than 3 Acnologia servers |
| `RateLimited` (`retry_after()`), `QuotaExhausted`, `InvalidKey` | Thrown by you from `with_key`. They derive from `KeyRotationError` |
| `TransportError` | The request did not complete (connection refused, timeout) |

`retry_after()` comes from `retry_after_sec` in the body, or else the `Retry-After` header. It is zero when the server did not say. Every type above `RateLimited` derives from `ApiError`. Invalid arguments (an empty URL or token, a `wait` over 60 seconds) throw `std::invalid_argument`.

## Secrets

`operator<<` on `Client`, `Lease`, `Proxy` and `AppliedAuth`, and `to_string` on `Client`, `Lease` and `AppliedAuth`, print `[redacted]` in place of the token and the secret. Error messages have the token and the access password replaced by `[redacted]`, and report messages have the lease secret replaced by `[key]`. The SDK does no logging. `lease.secret()`, `AppliedAuth::value` and `signed_url()` do contain the secret, so keep them out of your logs.

## Thread safety

A `Client` is immutable after construction. Copies share state, and one client can be used from many threads at once. The libcurl transport uses a new easy handle per request and runs `curl_global_init` once, the first time it is created. A custom `Transport` must be thread safe if you share the client.

A `Lease` is a plain value and is not synchronized: use each lease from one thread at a time. A lease keeps the client's settings alive, so it stays usable after the client is destroyed.

## Custom transport

Implement `acnologia::Transport::send(const HttpRequest&)` and pass it in `ClientOptions::transport`. The tests use this to run without a network. `acnologia::make_curl_transport()` returns the default libcurl one.

## Tests

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

The unit tests use a fake transport. An integration test runs the libcurl transport against a small HTTP server on 127.0.0.1 (POSIX only). doctest and nlohmann/json are fetched at configure time, pinned by release tag and hash.

## More

- [All SDKs](../README.md)
- [HTTP API and error codes](../../docs/api.md)
- [Request flows](../../docs/flows.md): what the proxy and leases do on the server
