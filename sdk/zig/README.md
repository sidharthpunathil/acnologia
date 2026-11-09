# Acnologia for Zig

A Zig client for [Acnologia](../../README.md), a self-hosted API key server. It calls the consumer API with an app token (`acn_...`). Your code gets a proxied answer or a borrowed key, and never holds the key pool.

It uses only the standard library: `std.http.Client` and `std.json`. It needs Zig 0.17.0.

```zig
const std = @import("std");
const acnologia = @import("acnologia");

pub fn main(init: std.process.Init) !void {
    var client = try acnologia.Client.init(init.gpa, init.io, .{ .environ = init.environ_map });
    defer client.deinit();

    var res = try client.proxy(init.gpa, .POST, "agi-labs", "/v1/chat", .{
        .headers = &.{.{ .name = "Content-Type", .value = "application/json" }},
        .body = "{\"prompt\":\"hello\"}",
    });
    defer res.deinit();
    std.debug.print("{d} {s}\n", .{ res.status, res.body });
}
```

## Install

The package lives in a folder of the repository, so fetch the repository and point a module at the folder.

```sh
zig fetch --save=acnologia git+https://github.com/sidharthpunathil/acnologia#main
```

Then in your `build.zig`:

```zig
const dep = b.dependency("acnologia", .{});
const acnologia = b.createModule(.{
    .root_source_file = dep.path("sdk/zig/src/acnologia.zig"),
    .target = target,
    .optimize = optimize,
});
exe.root_module.addImport("acnologia", acnologia);
```

Pin a commit instead of `main` for repeatable builds. A tarball URL such as `https://github.com/sidharthpunathil/acnologia/archive/<commit>.tar.gz` works too.

## The client

```zig
var client = try acnologia.Client.init(gpa, io, .{
    .base_url = "http://127.0.0.1:8080",
    .token = "acn_...",
});
defer client.deinit();
```

Every option is optional:

| Option | Default | What it does |
|---|---|---|
| `base_url` | `ACNOLOGIA_URL` | Where Acnologia runs. |
| `token` | `ACNOLOGIA_TOKEN` | The app token. |
| `access_password` | see below | Sent as `X-Acnologia-Access`. |
| `environ` | null | Where the fallbacks are read. Pass `init.environ_map`. Null reads nothing. |
| `timeout_ms` | 75000 | Timeout for the API calls (lease, report, renew, me, services, guides, capabilities, forward proxy). 0 turns it off. |
| `proxy_timeout_ms` | 0 | Timeout for proxy calls. 0 waits as long as the provider takes. |
| `max_body_bytes` | 32 MiB | Largest answer read into memory. |

`X-Acnologia-Access` is sent only when you set `access_password`, or when `ACNOLOGIA_ACCESS_PASSWORD` is set and its server (`ACNOLOGIA_ACCESS_URL`, or else `ACNOLOGIA_URL`) has the same origin as `base_url`. The client never follows a redirect, so the token and the password never reach another host.

`init` fails with `error.MissingBaseUrl` or `error.MissingToken` when a value is in neither place.

The timeouts work by running each call as a concurrent task next to a timer. The default `Io` (from `std.process.Init`, or `std.Io.Threaded`) supports that. An `Io` without concurrency runs the call with no timeout.

## Proxy

`proxy` sends any method to `/proxy/<service>/<path>`. Acnologia attaches a key, moves to the next key on a rate limit and returns the provider's answer.

```zig
var res = try client.proxy(gpa, .POST, "agi-labs", "/v1/chat", .{
    .query = &.{.{ .name = "mode", .value = "fast" }},
    .headers = &.{.{ .name = "Content-Type", .value = "application/json" }},
    .body = "{\"prompt\":\"hello\"}",
    .idempotency_key = "order-1234", // Idempotency-Key: a repeat replays the first answer
    .no_cache = true,                 // Cache-Control: no-cache
});
defer res.deinit();

res.status;              // the provider's status, such as 200 or 429
res.body;                // the provider's body
res.header("content-type");
res.acnologia.key_id;    // X-Acnologia-Key-Id (null for a share's token)
res.acnologia.attempts;  // X-Acnologia-Attempts
res.acnologia.service;   // X-Acnologia-Service
res.acnologia.cache;     // X-Acnologia-Cache
res.acnologia.replayed;  // Idempotent-Replayed: true
res.acnologia.capability; // X-Acnologia-Capability, for a capability call
res.acnologia.tried;     // X-Acnologia-Tried as []const Tried{ .service, .status }
```

The provider's own errors come back as a response with their status. Errors Acnologia itself answers with, such as `no_key_available` or `upstream_unreachable`, come back as Zig errors. Set `.raw = true` to get those as responses too.

`res.json(T, allocator)` parses the body. Free the result with its `deinit`.

## Leases

A lease hands your code the raw key for a while. Report every lease when you are done with it. On a service with a concurrency limit, an unreported lease holds a slot until it expires.

```zig
var lease = try client.lease(gpa, "agi-labs", .{
    .ttl_sec = 120,     // lifetime; null uses the server default
    .wait_sec = 10,     // wait up to 10 s (at most 60) for a busy key
    .exclusive = false, // true keeps the key from every other caller
    .exclude_key_ids = &.{},
});
defer lease.deinit();

// Put the key where the provider expects it.
const url = try lease.url(gpa, "/v1/chat", &.{}); // adds the key for query auth
defer gpa.free(url);
const headers = try lease.applyHeaders(gpa, &.{.{ .name = "Accept", .value = "application/json" }});
defer gpa.free(headers);                          // adds the key for header and bearer auth

// A long job keeps its key.
_ = try lease.renew(.{ .ttl_sec = 600 });

// Say how it went. With rotate, the next key comes back in the same call.
if (try lease.report(.{ .outcome = .rate_limited, .status_code = 429, .retry_after_sec = 30, .rotate = true })) |next_lease| {
    var next = next_lease;
    defer next.deinit();
    // use next, then report it too
    _ = try next.report(.{ .outcome = .ok });
}
```

`lease.authHeader()` returns the header to send, or null for query auth. `lease.applyUrl(gpa, url)` adds the key to a URL you built yourself. The outcomes are `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned` and `error`.

A report message is cleaned before it is sent. The secret (raw and URL encoded) and the token become `[key]`, and the text is cut to 500 bytes.

## Me

```zig
var me = try client.me(gpa, .{});
defer me.deinit();
me.value.app.name;
me.value.limits.daily_requests;  // null means no limit
me.value.usage.today_requests;
me.value.services;               // what this token may use
me.value.share;                  // set only for a share's token
```

## Services and guides

```zig
var services = try client.services(gpa, .{});
defer services.deinit();
for (services.value) |s| {
    // s.slug, s.available, s.base_url, s.auth.type, s.auth.name, s.guide_url
}

var guide = try client.guide(gpa, "serpapi", .{});
defer guide.deinit();
guide.value.markdown;  // the guide as Markdown
guide.value.generated; // true when written from the provider's spec
guide.value.guide;     // the structured guide, a std.json.Value

const md = try client.guideMarkdown(gpa, "serpapi", .{}); // Accept: text/markdown
defer gpa.free(md);
```

## Capabilities

A capability is one job (such as a web search) that several providers can do. Acnologia picks a provider, translates the params and moves on when one fails.

```zig
var caps = try client.capabilities(gpa, .{});
defer caps.deinit();
for (caps.value) |c| {
    // c.slug, c.name, c.strategy, c.params (JSON), c.routes, c.url
}

var res = try client.capability(gpa, "web-search", "{\"query\":\"coffee\"}", .{
    .service = null, // a provider slug pins one route: ?_service=<slug>
    .raw = false,    // true returns the provider's own body: ?_raw=1
});
defer res.deinit();
res.acnologia.service; // who answered
for (res.acnologia.tried) |t| std.debug.print("{s}={d}\n", .{ t.service, t.status });
```

`capability` takes the same `idempotency_key`, `no_cache`, `headers`, `timeout_ms` and `diag` as `proxy`. Its errors (`bad_params`, `no_route`, `no_route_worked`) come back as `error.Api` with the code in `diag.code()`; set `.raw_errors = true` to get them as responses.

## Forward proxy

An app can use Acnologia as its `HTTPS_PROXY` and call provider URLs as they are. Save the CA, then hand the environment to the app's own HTTP client or to a child process.

```zig
try client.saveForwardProxyCA(null, .{}); // writes acnologia-ca.pem; no token is sent

var proxy_env = try client.forwardProxyEnv(gpa, null, .{}); // CA path, default acnologia-ca.pem
defer proxy_env.deinit();
// HTTPS_PROXY, HTTP_PROXY, NODE_USE_ENV_PROXY, NODE_EXTRA_CA_CERTS,
// REQUESTS_CA_BUNDLE and SSL_CERT_FILE. Add them to your own environment
// and start the app with it:
var env = try init.environ_map.clone(gpa);
defer env.deinit();
var it = proxy_env.iterator();
while (it.next()) |e| try env.put(e.key_ptr.*, e.value_ptr.*);
var child = try std.process.spawn(init.io, .{ .argv = &.{ "node", "app.js" }, .environ_map = &env });
_ = try child.wait(init.io);
```

The other forward proxy calls:

```zig
var setup = try client.forwardProxy(gpa, .{});  // GET /api/v1/proxy
defer setup.deinit();
setup.value.proxy_url;       // http://acn.example.com:8888
setup.value.hosts;           // the hosts routed to your services
setup.value.ca_fingerprint;

const pem = try client.forwardProxyCA(gpa, .{}); // the CA as PEM text
defer gpa.free(pem);

const url = try client.forwardProxyURL(gpa, .{}); // http://app:acn_...@host:8888
defer {
    std.crypto.secureZero(u8, url);
    gpa.free(url);
}
```

`forwardProxyURL` and `forwardProxyEnv` contain the app token, percent encoded. `forwardProxy` and its `{f}` form never do: the server's `proxy_url_with_token` holds a `<app token>` placeholder.

## Errors

Every call returns `acnologia.Error`. Pass a `Diagnostic` to read the details.

```zig
var diag: acnologia.Diagnostic = .{};
var lease = client.lease(gpa, "agi-labs", .{ .diag = &diag }) catch |err| switch (err) {
    error.NoKeyAvailable, error.AllKeysBusy, error.ServiceUnavailable => {
        // wait diag.retry_after_sec seconds, then try again
        return err;
    },
    else => {
        std.debug.print("{f}\n", .{diag}); // status, code and message
        return err;
    },
};
```

| Error | Status and code |
|---|---|
| `Unauthenticated` | 401, or 403 `unauthenticated` |
| `ServiceNotAllowed` | 403 `service_not_allowed` |
| `ProxyNotAllowed` | 403 `proxy_not_allowed` |
| `LeaseNotAllowed` | 403 `lease_not_allowed` |
| `LeaseRevoked` | 403 `lease_revoked` |
| `ShareRevoked`, `SharePaused`, `ShareExpired` | 403 `share_revoked`, `share_paused`, `share_expired`. Together the share unavailable errors: `acnologia.isShareUnavailable(err)`, with `diag.share()` saying `revoked`, `paused` or `expired` (from `X-Acnologia-Share`) |
| `NoKeyAvailable` | 503 `no_key_available`, or 503 with no code |
| `AllKeysBusy` | 503 `all_keys_busy` (a kind of no key available: `acnologia.isNoKey(err)`) |
| `ServiceUnavailable` | 503 `service_unavailable` |
| `AppLimitReached` | 429 `app_limit_reached`; `diag.limit()` and `diag.resetAt()` say which and until when |
| `LeaseNotFound` | 404 `lease_not_found` |
| `ReportFailed` | 409 or 410 `report_failed` |
| `UnknownService` | 404 `unknown_service` |
| `UpstreamUnreachable`, `UpstreamBlocked` | 502 |
| `LoopDetected` | 508 `loop_detected` |
| `BadPath` | 400 `bad_path` |
| `BodyTooLarge` | 413 `body_too_large` |
| `Api` | any other error answer; read `diag.status` and `diag.code()`, such as `empty_group`, `bad_params`, `no_route` or `no_route_worked` |
| `Timeout`, `RequestFailed`, `ResponseTooLarge`, `BadResponse`, `InvalidUrl`, `InvalidArgument` | raised on this side |

`diag.retry_after_sec` comes from the body's `retry_after_sec`, or else from the `Retry-After` header.

No error message holds the token, the access password or a lease secret. `{f}` on a Client, a Lease or a Diagnostic prints `***` in their place.

## Memory rules

- `Client.init` copies its options. `client.deinit()` frees them and wipes the token.
- Each call takes the allocator for what it returns. You own the result.
- `ProxyResponse`, `Lease` and the `std.json.Parsed` from `me`, `services`, `guide`, `capabilities` and `forwardProxy` each have a `deinit`. Call it once. Slices inside stay valid until then.
- `lease.deinit()` wipes the secret before freeing it.
- The next lease from `report` uses the same allocator as the first. It needs its own `deinit`.
- `lease.url`, `lease.applyUrl` and `lease.applyHeaders` return a slice you free with the allocator you passed. The strings inside `applyHeaders` are borrowed from your headers and the lease.
- `guideMarkdown`, `forwardProxyCA` and `forwardProxyURL` return a slice you free with the allocator you passed. `forwardProxyEnv` returns a `std.process.Environ.Map`; call its `deinit`.
- `Diagnostic` uses fixed buffers. It needs no allocator and no `deinit`.
- A Lease points at its Client. Keep the Client in one place and alive until every lease is freed.
- The allocator passed to `Client.init` must be thread safe, since a call and its timer run as tasks. `init.gpa` and `std.heap.smp_allocator` are.

## Tests

```sh
cd sdk/zig
zig build test
```

The tests start a small HTTP server on 127.0.0.1 inside the test process. Nothing leaves the machine.

`zig build example -- agi-labs` runs [examples/main.zig](examples/main.zig) against the server in `ACNOLOGIA_URL` with `ACNOLOGIA_TOKEN`. It calls me, the proxy, then a lease that it renews and reports.
