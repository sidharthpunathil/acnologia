# acnologia-cpp

C++17 client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. It uses libcurl for HTTP and nlohmann/json internally (fetched by CMake, not exposed in the headers).

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `with_key` runs that loop and rotates keys for you.

## Install

Requires CMake 3.16+, a C++17 compiler and libcurl (it ships with macOS; on Debian or Ubuntu install `libcurl4-openssl-dev`).

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

Or with a checkout: `add_subdirectory(path/to/acnologia/sdk/cpp)` and the same `target_link_libraries`. Tests are built only when `sdk/cpp` is the top level project. `cmake --install` also installs a package, so `find_package(acnologia)` works.

## Quick start

```cpp
#include <acnologia/acnologia.hpp>
#include <iostream>

int main() {
    // Or acnologia::Client("http://localhost:8080", "acn_...")
    auto client = acnologia::Client::from_env();  // ACNOLOGIA_URL, ACNOLOGIA_TOKEN

    for (const auto& s : client.services()) std::cout << s.slug << " " << s.available << "\n";

    auto resp = client.proxy("serpapi").get("/search.json", {{"engine", "google"}, {"q", "coffee"}});
    std::cout << resp.status << " " << resp.body << "\n";
}
```

## Client

| Call | Does |
|---|---|
| `Client(base_url, token, ClientOptions{})` | `token` is an app token (`acn_...`; the older `cog_` prefix is still accepted by the server). |
| `Client::from_env(ClientOptions{})` | Reads `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`. Throws `std::invalid_argument` if either is missing. |
| `services()` | `GET /api/v1/services`: `slug`, `name`, `provider`, `category`, `available`, `base_url`, `auth{type, name, prefix}`. |
| `lease(service, LeaseOptions{})` | `POST /api/v1/lease`. Options: `exclude_key_ids`, `ttl`, `wait` (0 to 60s), `exclusive`. |
| `lease.report(Report{})` | `POST /api/v1/leases/{id}/report`. Returns the next lease when `rotate` is true and one is available, else `std::nullopt`. |
| `lease.renew(ttl)` | `POST /api/v1/leases/{id}/renew`. Returns and stores the new `expires_at`. |
| `with_key(service, fn, WithKeyOptions{})` | Lease, call, report, rotate. See below. |
| `proxy(service)` | Proxy helper. Also `proxy_url`, `proxy_get` and `proxy_request` on the client. |

`ClientOptions` has `transport` (a `std::shared_ptr<acnologia::Transport>`, default libcurl), `user_agent` (default `acnologia-cpp/0.1.0`) and `timeout` (default 75s for API calls, longer than the 60s maximum lease wait; a lease with a longer `wait` stretches it). Proxy calls have no timeout.

Outcomes: `Outcome::ok`, `rate_limited`, `quota_exhausted`, `invalid`, `error`, `busy` (the account is at its concurrency cap) and `banned` (the provider suspended the account).

## Lease mode

### with_key (recommended)

Throw one of the rotation exceptions from your callback to rotate to the next key:

| Callback | Reported outcome | Rotates |
|---|---|---|
| returns | `ok` | no, the return value is passed back |
| throws `acnologia::RateLimited(retry_after, status, message)` | `rate_limited` | yes |
| throws `acnologia::QuotaExhausted(status, message)` | `quota_exhausted` | yes |
| throws `acnologia::InvalidKey(status, message)` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown unchanged |

After `max_attempts` (default 5) keys, the last rotation exception is rethrown; the final attempt is reported without asking for a next key. When the report has no next key, `with_key` leases again excluding every key it tried. Every lease is reported, whatever the callback does, and the secret is scrubbed from any reported message.

```cpp
std::string body = client.with_key("serpapi", [](acnologia::Lease& lease) {
    // signed_url adds ?api_key=<secret> for query auth; for header auth use lease.apply().
    std::string url = lease.signed_url("/search.json", {{"engine", "google"}, {"q", "coffee"}});
    auto [status, text] = my_http_get(url);  // any HTTP library
    if (status == 429 && text.find("run out of searches") != std::string::npos)
        throw acnologia::QuotaExhausted(429);
    if (status == 429) throw acnologia::RateLimited(std::chrono::hours(1), 429);
    if (status == 401) throw acnologia::InvalidKey(401);
    if (status >= 400) throw std::runtime_error("serpapi: http " + std::to_string(status));
    return text;
});
```

`WithKeyOptions` has `max_attempts` and `lease` (`LeaseOptions`; `ttl`, `wait` and `exclusive` apply to every lease it takes).

### Applying the key

`lease.apply()` returns an `AppliedAuth` with `location` (header or query), `name` and `value`, so it works with any HTTP library:

```cpp
auto a = lease.apply();
if (a.is_header()) headers = curl_slist_append(headers, a.header_line().c_str());
else url += "&" + a.name + "=" + acnologia::url_encode(a.value);
```

`query` auth uses the parameter `auth.name`, `header` auth the header `auth.name`, and `bearer` auth the header `auth.name` (default `Authorization`) with `Bearer <secret>`. A non empty `auth.prefix` goes in front of the secret for every type and, for `bearer`, replaces `Bearer `. `lease.url(path, query)` joins the provider base URL without the key; `lease.signed_url(path, query)` also adds it for query auth.

### Lease and report by hand

```cpp
acnologia::LeaseOptions opts;
opts.ttl = std::chrono::minutes(5);
opts.wait = std::chrono::seconds(10);
acnologia::Lease lease = client.lease("serpapi", opts);
// ... call the provider, then report on every path:
acnologia::Report r;
r.outcome = acnologia::Outcome::rate_limited;
r.status_code = 429;
r.retry_after = std::chrono::hours(1);
r.rotate = true;  // ask for the next key in the same call
if (auto next = lease.report(r)) { /* retry with *next */ }
```

The shorthand `lease.report(acnologia::Outcome::ok)` covers the simple case. On services with a concurrency limit, an unreported lease holds one of the key's slots until its TTL runs out (default 120 seconds on limited services, 300 otherwise), so report every lease or use `with_key`.

## Proxy mode

```cpp
auto proxy = client.proxy("elevenlabs");
auto resp = proxy.post("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
                       R"({"text":"Hello from Acnologia","model_id":"eleven_multilingual_v2"})");
std::cout << resp.status << " key " << resp.header("X-Acnologia-Key-Id") << "\n";
```

`get(path, query, headers)`, `post(path, body, content_type, headers)` and `request(method, path, body, headers, query)` send to `/proxy/{service}/{path}` with your app token. Upstream responses come back as they are: a non 2xx status is not an exception. Redirects are never followed, so the token is not replayed to another host. `url(path, query)` returns the full proxy URL if you would rather use your own HTTP client (send `Authorization: Bearer <token>` with it). The body is read fully into memory.

## Exceptions

Everything derives from `acnologia::Error` (a `std::runtime_error`).

| Type | When |
|---|---|
| `ApiError` (`status()`, `code()`, `message()`) | any non 2xx from the consumer API without a more specific type below |
| `NoKeyAvailable` (`retry_after()`, `busy()`) | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusy` (derives from `NoKeyAvailable`) | 503 `all_keys_busy`: every usable key is at its concurrency limit; retry after `retry_after()` or lease with `wait` |
| `ServiceUnavailable` (`retry_after()`) | 503 `service_unavailable`: the provider's circuit breaker is open |
| `Unauthenticated` | 401: token missing or wrong |
| `Forbidden` | 403: app disabled, or not allowed this service or mode |
| `RateLimited` (`retry_after()`), `QuotaExhausted`, `InvalidKey` | you throw them from `with_key`; they derive from `KeyRotationError` |
| `TransportError` | the request did not complete (connection refused, timeout) |

All the API exceptions derive from `ApiError`. Invalid arguments (an empty URL or token, a `wait` over 60s) throw `std::invalid_argument`.

## Secrets

`operator<<` and `to_string` on `Client`, `Lease`, `Proxy` and `AppliedAuth` print `[redacted]` in place of the token and the secret. The SDK does no logging. `lease.secret()`, `AppliedAuth::value` and `signed_url()` do contain the secret, so keep them out of your logs.

## Thread safety

A `Client` is immutable after construction; copies share state, and one client can be used from many threads at once. The libcurl transport uses a new easy handle per request and runs `curl_global_init` once, through a function local guard, the first time it is created. A custom `Transport` must be thread safe if you share the client. A `Lease` is a plain value and is not synchronized: use each lease from one thread at a time. A lease keeps the client's settings alive, so it stays usable after the client is destroyed.

## Custom transport

Implement `acnologia::Transport::send(const HttpRequest&)` and pass it in `ClientOptions::transport`. The tests use this to run without a network.

## Tests

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

The unit tests use a fake transport; an integration test runs the libcurl transport against a small HTTP server on 127.0.0.1 (POSIX only). doctest and nlohmann/json are fetched at configure time, pinned by release tag and hash.
