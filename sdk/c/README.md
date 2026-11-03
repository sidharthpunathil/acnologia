# Acnologia for C

Call APIs with the keys your Acnologia server holds, using your app's own `acn_` token. In proxy mode the server attaches the key, moves to the next key on rate limits and quota errors, and the secret never reaches your process. In lease mode you borrow a key for code that needs it raw, then report how it went.

C11, libcurl for HTTP. JSON goes through a vendored copy of cJSON 1.7.18 (MIT, see [`third_party/cjson`](third_party/cjson/README.md)), compiled into the library and kept out of the public header.

## Quick start

```c
#include <stdio.h>
#include <acnologia/acnologia.h>

int main(void) {
    acn_global_init();

    acn_client *client = NULL;
    acn_error err;
    if (acn_client_new(NULL, &client, &err) != ACN_OK) {   /* ACNOLOGIA_URL, ACNOLOGIA_TOKEN */
        fprintf(stderr, "%s\n", err.message);
        return 1;
    }

    acn_proxy_request req = {0};
    req.service = "agi-labs";
    req.path = "/v1/chat";
    acn_response *resp = NULL;
    if (acn_proxy(client, &req, &resp, &err) == ACN_OK) {
        printf("%d %.*s\n", resp->status, (int)resp->body_len, resp->body);
        acn_response_free(resp);
    } else {
        fprintf(stderr, "%s: %s\n", acn_strerror(err.code), err.message);
    }

    acn_client_free(client);
    acn_global_cleanup();
    return 0;
}
```

`agi-labs` is the slug of a service on your server. The request goes to `<ACNOLOGIA_URL>/proxy/agi-labs/v1/chat`.

## Build

You need a C11 compiler and libcurl.

- macOS: libcurl ships with the system.
- Debian and Ubuntu: `sudo apt-get install libcurl4-openssl-dev`.
- Windows: install curl with vcpkg (`vcpkg install curl:x64-windows`) and pass its toolchain file to CMake.

With CMake 3.16 or later:

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

Without CMake, the Makefile builds the same things into `build/`:

```sh
make
make test
```

## Link

From a checkout, add the folder and link the target:

```cmake
add_subdirectory(path/to/acnologia/sdk/c)
target_link_libraries(my_app PRIVATE acnologia::acnologia)
```

Or with FetchContent:

```cmake
include(FetchContent)
FetchContent_Declare(acnologia_c
  GIT_REPOSITORY https://github.com/sidharthpunathil/acnologia.git
  GIT_TAG        main          # pin a tag or commit in real projects
  SOURCE_SUBDIR  sdk/c)
FetchContent_MakeAvailable(acnologia_c)
target_link_libraries(my_app PRIVATE acnologia::acnologia)
```

Tests and the example build only when `sdk/c` is the top level project. Without CMake, link `build/libacnologia.a` and libcurl: `cc app.c -Ipath/to/sdk/c/include build/libacnologia.a $(curl-config --libs)`.

## Conventions

- Every call returns an `acn_code`. `ACN_OK` is zero.
- The last argument is an `acn_error *`. It may be `NULL`. On failure it holds the code, the HTTP status, the server's error code, a message and the retry hint.
- Out pointers are set only on `ACN_OK`. On failure they stay `NULL`, so there is nothing to free.
- Strings you pass in are copied or used only during the call.

## Connect

Create an app in the dashboard and copy its token (`acn_...`, shown once).

```c
acn_client_options opts;
acn_client_options_init(&opts);
opts.base_url = "http://127.0.0.1:8080";
opts.token = "acn_...";

acn_client *client = NULL;
acn_error err;
acn_code rc = acn_client_new(&opts, &client, &err);
```

Pass `NULL` for the options, or leave a field `NULL`, to read it from the environment.

| Field | Default | Meaning |
|---|---|---|
| `base_url` | `ACNOLOGIA_URL` | The server. A trailing slash is dropped |
| `token` | `ACNOLOGIA_TOKEN` | The app token |
| `access_password` | see below | Sent as `X-Acnologia-Access` when the server has an access password |
| `timeout_ms` | 75000 | For API calls. A lease with `wait_sec` stretches it to `wait_sec` plus 10 seconds. Negative turns it off |
| `connect_timeout_ms` | 10000 | Negative turns it off |
| `proxy_timeout_ms` | none | Proxy calls have no timeout unless you set one, since a provider may take long |
| `user_agent` | `acnologia-c/0.1.0` | The `User-Agent` header. `X-Acnologia-Client: acnologia-c/0.1.0` is always sent |

A missing URL or token fails with `ACN_ERR_INVALID_ARGUMENT`.

The access password is sent only when you set it, or when `ACNOLOGIA_ACCESS_PASSWORD` is set and `base_url` is the server it belongs to: `ACNOLOGIA_ACCESS_URL` when that is set, else `ACNOLOGIA_URL`. Set `access_password = ""` to never send one. An app token passes the lock on its own, so most apps never need it.

`acn_services` lists the services this token may use (`GET /api/v1/services`). Free the list with `acn_service_list_free`.

## Proxy mode

```c
acn_pair query[] = {{"stream", "false"}};
acn_pair headers[] = {{"Content-Type", "application/json"}};
const char *body = "{\"prompt\":\"hello\"}";

acn_proxy_request req = {0};
req.method = "POST";                 /* NULL means GET */
req.service = "agi-labs";
req.path = "/v1/chat";
req.query = query;
req.query_count = 1;
req.headers = headers;
req.header_count = 1;
req.body = body;
req.body_len = strlen(body);
req.idempotency_key = "order-42";    /* optional */

acn_response *resp = NULL;
if (acn_proxy(client, &req, &resp, &err) == ACN_OK) {
    printf("status %d, key %lld, attempts %d\n", resp->status, (long long)resp->key_id, resp->attempts);
    acn_response_free(resp);
}
```

Any method works. `path` may carry its own query string. Your headers are sent as given, except `Authorization` and `User-Agent`, which the SDK sets. Redirects are returned, never followed, so the token never goes to another host.

The provider's answer comes back as it is, whatever its status: a 429 from the provider is `ACN_OK` with `resp->status` 429. When Acnologia itself refuses the call (no key, an app limit, a bad path), `acn_proxy` returns that error and no response. The two are told apart by `X-Acnologia-Service`, which the server puts on every provider answer.

The response holds:

| Field | Meaning |
|---|---|
| `status`, `body`, `body_len` | The answer. `body` has a NUL after `body_len` bytes |
| `headers`, `header_count` | Every header in order. `acn_response_header(resp, "name")` finds one, any case |
| `key_id` | `X-Acnologia-Key-Id`, -1 when absent |
| `attempts` | `X-Acnologia-Attempts`, 0 when absent |
| `service`, `cache` | `X-Acnologia-Service` and `X-Acnologia-Cache`, `NULL` when absent |
| `idempotent_replayed` | 1 when the server replayed an earlier answer for the same `Idempotency-Key` |

## Lease mode

```c
acn_lease_options lo = {0};
lo.ttl_sec = 300;     /* 0 uses the server default */
lo.wait_sec = 10;     /* wait up to 10 seconds for a free slot, at most 60 */

acn_lease *lease = NULL;
if (acn_lease_acquire(client, "agi-labs", &lo, &lease, &err) != ACN_OK) {
    /* ACN_ERR_NO_KEY_AVAILABLE or ACN_ERR_ALL_KEYS_BUSY: wait err.retry_after_sec */
}

char *url = NULL, *header = NULL;
acn_lease_apply(lease, "https://api.agi-labs.example/v1/chat", &url, &header, &err);
/* query auth: url has the key parameter, header is NULL
   header and bearer auth: url is a copy, header is "Name: value" */
/* ... call the provider with your own HTTP code ... */
acn_free(url);
acn_free(header);

acn_lease_renew(client, lease, 600, &err);   /* a long job keeps its key */

acn_report rep = {0};
rep.outcome = ACN_OUTCOME_RATE_LIMITED;
rep.status_code = 429;
rep.retry_after_sec = 30;
rep.rotate = 1;
acn_lease *next = NULL;
acn_lease_report(client, lease, &rep, &next, &err);
acn_lease_free(lease);
/* next is the next key, or NULL. Report and free it too. */
```

Always report a lease. On services with a concurrency limit an unreported lease holds one of the key's slots until its TTL runs out.

| Call | Does |
|---|---|
| `acn_lease_acquire(client, service, opts, &lease, &err)` | `POST /api/v1/lease`. Options: `exclude_key_ids` and `exclude_count`, `ttl_sec`, `wait_sec` (0 to 60), `exclusive` |
| `acn_lease_renew(client, lease, ttl_sec, &err)` | Extends an open lease and updates `lease->expires_at`. 0 uses the server default |
| `acn_lease_report(client, lease, &report, &next, &err)` | Sends `outcome`, `status_code`, `retry_after_sec`, `message` and `rotate`. With `rotate`, `*next` is the next key or `NULL` |
| `acn_lease_apply(lease, url, &url_out, &header_out, &err)` | Puts the secret where the provider expects it |
| `acn_lease_url(lease, path, &out, &err)` | `base_url` joined with `path`, without the secret |
| `acn_with_key(client, service, opts, max_attempts, fn, user, &err)` | Leases, calls `fn`, reports, and rotates on a rejected key |

The outcomes are `ACN_OUTCOME_OK`, `RATE_LIMITED`, `BUSY`, `QUOTA_EXHAUSTED`, `INVALID`, `BANNED` and `ERROR`. `acn_outcome_name` gives the wire name.

`acn_lease_apply` follows `lease->auth`. For `query` it adds `name=<prefix><secret>` to the URL. For `header` it returns `Name: <prefix><secret>`. For `bearer` it returns `Authorization: Bearer <secret>`, or the prefix in place of `Bearer ` when one is set. An empty name means `Authorization` (or `api_key` for a query). An unknown type fails with `ACN_ERR_INVALID_ARGUMENT`.

### The lease helper

```c
static void call_agi(const acn_lease *lease, acn_report *report, void *user) {
    int status = my_call(lease, user);         /* your HTTP code */
    if (status == 429) {
        report->outcome = ACN_OUTCOME_RATE_LIMITED;
        report->status_code = 429;
    }
}

acn_code rc = acn_with_key(client, "agi-labs", NULL, 5, call_agi, &my_state, &err);
```

`fn` gets a report already set to `ok`. Set `RATE_LIMITED`, `QUOTA_EXHAUSTED` or `INVALID` and the helper reports the key with `rotate` and calls `fn` again with the next one, up to `max_attempts` (0 means 5). Any other outcome is reported and ends the loop with `ACN_OK`. Every lease is reported. After the last rejected attempt the result is `ACN_ERR_ATTEMPTS_EXHAUSTED`. When the pool runs dry it is the no key error, with `retry_after_sec`.

## Me

```c
acn_me *me = NULL;
if (acn_me_get(client, &me, &err) == ACN_OK) {
    printf("%s: %lld requests today\n", me->app_name, (long long)me->today_requests);
    if (me->limit_hit) printf("limit %s until %s\n", me->limit_hit, me->limit_reset_at);
    acn_me_free(me);
}
```

`GET /api/v1/me` returns the token's app, its limits (-1 when not set), use today and this month, the services it may call, and for a share's token the share (`me->share`, `NULL` for an ordinary app). `me->raw_json` holds the whole answer.

## Errors

| Code | Status and server code | When |
|---|---|---|
| `ACN_ERR_UNAUTHENTICATED` | 401, or 403 `unauthenticated` | Token missing or wrong, or the app is disabled |
| `ACN_ERR_SERVICE_NOT_ALLOWED` | 403 `service_not_allowed` | The app may not use this service |
| `ACN_ERR_PROXY_NOT_ALLOWED` | 403 `proxy_not_allowed` | The app is lease only |
| `ACN_ERR_LEASE_NOT_ALLOWED` | 403 `lease_not_allowed` | The app is proxy only, or the service is shared by another server |
| `ACN_ERR_NO_KEY_AVAILABLE` | 503 `no_key_available` | Every key is cooling down, exhausted or excluded |
| `ACN_ERR_ALL_KEYS_BUSY` | 503 `all_keys_busy` | Every usable key is at its concurrency limit. Retry soon or lease with `wait_sec` |
| `ACN_ERR_SERVICE_UNAVAILABLE` | 503 `service_unavailable` | The provider keeps failing and its circuit breaker is open |
| `ACN_ERR_APP_LIMIT_REACHED` | 429 `app_limit_reached` | A daily or monthly limit. `err.limit` and `err.reset_at` say which and until when |
| `ACN_ERR_SHARE_REVOKED`, `_PAUSED`, `_EXPIRED` | 403 `share_revoked`, `share_paused`, `share_expired` | A share's token whose share was revoked, paused or let expire |
| `ACN_ERR_LEASE_NOT_FOUND` | 404 `lease_not_found` | Renewing a lease that is not open |
| `ACN_ERR_REPORT_FAILED` | 409 or 410 `report_failed` | The lease was already reported, or expired long ago |
| `ACN_ERR_UPSTREAM_UNREACHABLE` | 502 `upstream_unreachable` | The provider could not be reached |
| `ACN_ERR_UPSTREAM_BLOCKED` | 502 `upstream_blocked` | The provider's address is blocked |
| `ACN_ERR_LOOP_DETECTED` | 508 `loop_detected` | The request passed through too many Acnologia servers |
| `ACN_ERR_BAD_PATH` | 400 `bad_path` | A proxy path with `?`, `#` or a dot segment |
| `ACN_ERR_BODY_TOO_LARGE` | 413 `body_too_large` | The proxy body is over the server's limit |
| `ACN_ERR_API` | anything else | Check `err.status` and `err.error_code`, such as `unknown_service` |
| `ACN_ERR_TRANSPORT` | none | No answer: refused, DNS, TLS or timeout |
| `ACN_ERR_BAD_RESPONSE` | 2xx | The answer was not the JSON the SDK expected |
| `ACN_ERR_INVALID_ARGUMENT`, `ACN_ERR_NO_MEMORY` | none | A bad argument, or out of memory |
| `ACN_ERR_ATTEMPTS_EXHAUSTED` | none | `acn_with_key` ran out of attempts |

`err.retry_after_sec` comes from the body's `retry_after_sec`, or else the `Retry-After` header (seconds or an HTTP date). It is 0 when the server gave neither. `acn_is_no_key(code)` is true for both no key codes. `acn_strerror(code)` names a code.

## Secrets

Error messages never hold the token, the access password or a lease secret: each is replaced by `[token]`, `[redacted]` or `[key]`, raw or URL encoded. A report `message` has the lease secret replaced by `[key]` before it is sent, and is cut to 500 bytes. The SDK does no logging. `lease->secret` and the strings from `acn_lease_apply` hold the secret, so keep them out of your logs. Freed clients and leases have their token and secret wiped first.

## Memory

| You get | Free it with |
|---|---|
| `acn_client *` | `acn_client_free` |
| `acn_response *` | `acn_response_free` |
| `acn_lease *` (including `next`) | `acn_lease_free` |
| `acn_me *` | `acn_me_free` |
| `acn_service_list *` | `acn_service_list_free` |
| `char *` from `acn_lease_apply` and `acn_lease_url` | `acn_free` |

Every free function accepts `NULL`. Strings inside a struct belong to it, and `acn_response_header` and `acn_client_base_url` return pointers owned by their object. `acn_error` is yours, on the stack, with nothing to free. Use `acn_free` rather than `free` for SDK strings, so a Windows program with a different C runtime stays safe.

## Thread safety

Call `acn_global_init` once at program start, before any other thread runs, and `acn_global_cleanup` once at the end. They wrap `curl_global_init` and `curl_global_cleanup`, and are the only global state.

A client does not change after `acn_client_new`, and every call uses its own libcurl handle, so one client can serve many threads at once. A lease is not synchronized: use each one from one thread at a time.

## Tests

```sh
cmake -S . -B build -DACN_SANITIZE=ON
cmake --build build
ctest --test-dir build --output-on-failure
```

or `make test SAN=1`. `ACN_SANITIZE` turns on AddressSanitizer and UndefinedBehaviorSanitizer with GCC and Clang. Everything is built with `-Wall -Wextra -Werror -pedantic` (`/W4 /WX` with MSVC).

The tests start a small HTTP server on 127.0.0.1 inside the test program, written with plain sockets, so they need no network and no other tools. They cover every error code, me, idempotency, `Retry-After`, the environment defaults, secret scrubbing, and applying a key to a header or a query.

[`examples/quickstart.c`](examples/quickstart.c) runs me, proxy, lease, apply, renew and report against a real server. Build it with the tests and run it with `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` set.

## More

- [All SDKs](../README.md)
- [HTTP API and error codes](../../docs/api.md)
- [Request flows](../../docs/flows.md): what the proxy and leases do on the server
