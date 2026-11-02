# Acnologia (C#)

.NET client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. .NET 8,
`System.Text.Json` only, no third party runtime dependencies. Every call takes a
`CancellationToken`, and the client works as an `IHttpClientFactory` typed client.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `WithKeyAsync` runs that loop and rotates keys for you.

## Install

The package is not on NuGet yet. Use a project reference:

```sh
dotnet add path/to/YourApp.csproj reference path/to/acnologia/sdk/csharp/src/Acnologia/Acnologia.csproj
```

or pack it into a local feed:

```sh
cd sdk/csharp
dotnet pack src/Acnologia -c Release -o ./artifacts
dotnet add path/to/YourApp.csproj package Acnologia --version 0.1.0 --source "$PWD/artifacts"
```

## Quick start

```csharp
using Acnologia;

using var acn = new AcnologiaClient("http://localhost:8080", "acn_your_app_token");
// or AcnologiaClient.FromEnvironment(), which reads ACNOLOGIA_URL and ACNOLOGIA_TOKEN

using var resp = await acn.Proxy("serpapi").GetAsync("/search.json",
    new Dictionary<string, string> { ["engine"] = "google", ["q"] = "coffee" });
Console.WriteLine(await resp.Content.ReadAsStringAsync());

foreach (var s in await acn.ServicesAsync())
    Console.WriteLine($"{s.Slug} available={s.Available} auth={s.Auth.Type}");
```

The token is an app token from the dashboard (Apps page), sent as
`Authorization: Bearer acn_...`. Tokens with the older `cog_` prefix still work.
Every request carries `User-Agent: acnologia-csharp/0.1.0`.

## Configure

| Setting | Default | Notes |
|---|---|---|
| `AcnologiaClientOptions.BaseUrl` | `ACNOLOGIA_URL` with `FromEnvironment` | Required. |
| `AcnologiaClientOptions.Token` | `ACNOLOGIA_TOKEN` with `FromEnvironment` | Required. |
| `AcnologiaClientOptions.UserAgent` | `acnologia-csharp/0.1.0` | |
| `AcnologiaClientOptions.Timeout` | 75 seconds | Bounds services, lease, report and renew calls (longer than the 60 second maximum lease wait). Proxy calls are not bounded by it. |

`new AcnologiaClient(baseUrl, token)` creates its own `HttpClient` that does not
follow redirects. To use `IHttpClientFactory`, register the options and a typed client:

```csharp
builder.Services.AddSingleton(new AcnologiaClientOptions
{
    BaseUrl = builder.Configuration["Acnologia:Url"],
    Token = builder.Configuration["Acnologia:Token"],
});
builder.Services.AddHttpClient<AcnologiaClient>();
```

With a supplied `HttpClient`, its own `Timeout` (100 seconds by default) still applies.

## Proxy mode

`acn.Proxy(service)` sends requests to `/proxy/{service}/{path}`. Upstream
responses come back unchanged: a non-2xx status is not an exception. Responses
are returned once the headers arrive, so bodies can stream; dispose them. Each
response carries `X-Acnologia-Key-Id`, `X-Acnologia-Attempts` and `X-Acnologia-Service`.

```csharp
var tts = acn.Proxy("elevenlabs");
using var audio = await tts.PostJsonAsync("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
    new { text = "Hello from Acnologia", model_id = "eleven_multilingual_v2" });
await using var file = File.Create("hello.mp3");
await audio.Content.CopyToAsync(file);
```

Other helpers: `GetAsync(path, query)`, `PostAsync(path, content)`,
`SendAsync(method, path, content, query)`, `Url(path, query)`, and
`SendAsync(HttpRequestMessage)`, which accepts a relative URI such as
`/search.json?q=x` or an absolute one (only the path and query are used).

### As an HttpClient

`acn.CreateProxyHttpClient(service)` returns an `HttpClient` whose base address
is `{baseUrl}/proxy/{service}/`. `AcnologiaProxyHandler` is the `DelegatingHandler`
behind it: it reroutes every request's path and query under the proxy and adds
the token, so code written against the provider's own base URL keeps working.

```csharp
builder.Services.AddHttpClient("serpapi", c => c.BaseAddress = new Uri("https://serpapi.com/"))
    .AddHttpMessageHandler(() => new AcnologiaProxyHandler(AcnologiaClientOptions.FromEnvironment(), "serpapi"))
    .ConfigurePrimaryHttpMessageHandler(() => new SocketsHttpHandler { AllowAutoRedirect = false });
```

Keep redirects off in the primary handler so the app token is not sent to another host.

## Lease mode

### WithKeyAsync (recommended)

Throw one of the rotation exceptions from your callback to rotate to the next key:

| Callback | Reported outcome | Rotates |
|---|---|---|
| returns | `ok` | no |
| throws `RateLimitedException(retryAfter, statusCode)` | `rate_limited` | yes |
| throws `QuotaExhaustedException(statusCode)` | `quota_exhausted` | yes |
| throws `InvalidKeyException(statusCode)` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown as is |

A rotation exception is also recognized as the inner exception of another one.
After `MaxAttempts` keys (default 5) the last rotation exception is rethrown;
the final attempt is reported without asking for a next key. When the pool has
no key left, `NoKeyAvailableException` is thrown.

```csharp
var http = new HttpClient();
var json = await acn.WithKeyAsync("serpapi", async (lease, ct) =>
{
    using var req = new HttpRequestMessage(HttpMethod.Get,
        lease.Url("/search.json", new Dictionary<string, string> { ["q"] = "coffee" }));
    lease.Apply(req); // adds ?api_key=<secret>
    using var resp = await http.SendAsync(req, ct);
    var body = await resp.Content.ReadAsStringAsync(ct);
    if ((int)resp.StatusCode == 429 && body.Contains("run out of searches"))
        throw new QuotaExhaustedException(429);
    if ((int)resp.StatusCode == 429)
        throw new RateLimitedException(TimeSpan.FromHours(1), 429);
    if ((int)resp.StatusCode == 401)
        throw new InvalidKeyException(401);
    resp.EnsureSuccessStatusCode();
    return body;
}, new WithKeyOptions { MaxAttempts = 3 }, cancellationToken);
```

`WithKeyOptions.Lease` sets the first lease's `ExcludeKeyIds`, `Ttl`, `Wait` and
`Exclusive`; `Ttl`, `Wait` and `Exclusive` also apply to every later lease.

Every lease is reported, including when the cancellation token fires while your
callback runs (the report then goes out with its own 10 second timeout). Report
messages never contain the secret: it is replaced with `[key]`, and messages
are cut at 500 characters.

### Lease and report by hand

```csharp
var lease = await acn.LeaseAsync("serpapi", new LeaseOptions
{
    Ttl = TimeSpan.FromMinutes(5),
    Wait = TimeSpan.FromSeconds(10),
});
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
var next = await lease.ReportAsync(new LeaseReport
{
    Outcome = Outcome.RateLimited,
    StatusCode = 429,
    RetryAfter = TimeSpan.FromHours(1),
    Rotate = true, // ask for the next key in the same call
});
if (next is not null) { /* retry with next */ }
```

`Lease` has `LeaseId`, `KeyId`, `Service`, `Secret`, `Auth { Type, Name, Prefix }`,
`BaseUrl`, `ExpiresAt` and `Exclusive`. `ToString()`, the debugger view and JSON
serialization of a `Lease` never include the secret, and the client never logs it.

`lease.Apply(request)` places the secret according to `Auth.Type`: `query` sets the
query parameter `Auth.Name`, `header` sets header `Auth.Name`, `bearer` sets
`Auth.Name` (default `Authorization`) to `Bearer <secret>`. A non empty `Auth.Prefix`
goes in front of the secret for every type, and for `bearer` it replaces `Bearer `.

`lease.RenewAsync(ttl)` extends an open lease for a long job and returns the new expiry.

Outcomes: `Ok`, `RateLimited`, `QuotaExhausted`, `Invalid`, `Error`, `Busy` (the
account is at its concurrency cap) and `Banned` (the provider suspended the account).

### Concurrency limits

On a service with a per key concurrency limit, each lease holds one of the key's
slots until it is reported or its TTL runs out (default 120 seconds on limited
services, 300 otherwise). Set `LeaseOptions.Wait` (up to `AcnologiaClient.MaxWait`,
60 seconds) and the server waits that long for a free slot. Without it a busy pool
throws `AllKeysBusyException` right away; retry after its `RetryAfter`. Proxy mode
queues requests on the server and needs none of this.

## Exceptions

All derive from `AcnologiaException`.

| Type | When |
|---|---|
| `AcnologiaApiException` (`StatusCode`, `Code`, `ServerMessage`) | Any non-2xx from the consumer API without a more specific type |
| `UnauthenticatedException` | 401: token missing, wrong, revoked or expired |
| `ForbiddenException` | 403: app disabled, or not allowed this service or mode |
| `NoKeyAvailableException` (`RetryAfter`, `Busy`) | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusyException` (`RetryAfter`) | 503 `all_keys_busy`: every usable key is at its concurrency limit. Derives from `NoKeyAvailableException` |
| `ServiceUnavailableException` (`RetryAfter`) | 503 `service_unavailable`: the provider keeps failing and Acnologia paused it |
| `RateLimitedException` (`RetryAfter`, `StatusCode`) | You throw it from `WithKeyAsync` |
| `QuotaExhaustedException` (`StatusCode`) | You throw it from `WithKeyAsync` |
| `InvalidKeyException` (`StatusCode`) | You throw it from `WithKeyAsync` |

The API exceptions derive from `AcnologiaApiException`; the three rotation
exceptions derive from `KeyRotationException`. An API call that runs past
`Timeout` throws `TimeoutException`.

## Environment variables

| Variable | Used by |
|---|---|
| `ACNOLOGIA_URL` | `AcnologiaClient.FromEnvironment()`, `AcnologiaClientOptions.FromEnvironment()` |
| `ACNOLOGIA_TOKEN` | the same |

## Tests

```sh
cd sdk/csharp
dotnet test
# without a local .NET SDK:
docker run --rm -v "$PWD":/w -w /w mcr.microsoft.com/dotnet/sdk:8.0 dotnet test
```
