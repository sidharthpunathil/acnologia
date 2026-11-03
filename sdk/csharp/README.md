# Acnologia for C#

Call the APIs your Acnologia server holds keys for, from .NET, with your app's own `acn_` token. Send requests through the proxy and the key never reaches your process. Or lease a key, call the provider yourself, and let `WithKeyAsync` report each attempt and move to the next key on a rate limit, an empty quota or a rejected key.

.NET 8, `System.Text.Json` only, with no third party runtime dependencies. Every call takes a `CancellationToken`, and the client works as an `IHttpClientFactory` typed client.

## Quick start

```csharp
using Acnologia;

using var acn = new AcnologiaClient("http://localhost:8080", "acn_your_app_token");

using var resp = await acn.Proxy("weather").GetAsync("/v1/forecast",
    new Dictionary<string, string> { ["city"] = "Lisbon" });
Console.WriteLine(await resp.Content.ReadAsStringAsync());
```

`weather` is the slug of a service on your server. Acnologia attaches the key, sends the call on, and returns the provider's response.

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

## Connect to your server

Create an app on the Apps page of the dashboard and copy its token. The SDK sends it as `Authorization: Bearer acn_...`. Tokens with the older `cog_` prefix still work.

`AcnologiaClient.FromEnvironment()` and `AcnologiaClientOptions.FromEnvironment()` read the URL and token from the environment, and throw `ArgumentException` when one is missing:

| Variable | Meaning |
|---|---|
| `ACNOLOGIA_URL` | Server URL, such as `http://localhost:8080` |
| `ACNOLOGIA_TOKEN` | App token, `acn_...` |

`AcnologiaClientOptions` holds the settings:

| Setting | Default | Notes |
|---|---|---|
| `BaseUrl` | `ACNOLOGIA_URL` with `FromEnvironment` | Required. |
| `Token` | `ACNOLOGIA_TOKEN` with `FromEnvironment` | Required. |
| `UserAgent` | `acnologia-csharp/0.1.0` | |
| `Timeout` | 75 seconds | Bounds services, lease, report and renew calls. It is longer than the 60 second maximum lease wait. Proxy calls are not bounded by it. |

`new AcnologiaClient(baseUrl, token)` and `new AcnologiaClient(options)` create their own `HttpClient`, which does not follow redirects. To use `IHttpClientFactory`, register the options and a typed client:

```csharp
builder.Services.AddSingleton(new AcnologiaClientOptions
{
    BaseUrl = builder.Configuration["Acnologia:Url"],
    Token = builder.Configuration["Acnologia:Token"],
});
builder.Services.AddHttpClient<AcnologiaClient>();
```

A supplied `HttpClient` keeps its own `Timeout` (100 seconds by default), so keep it longer than any lease wait. Its `BaseAddress` and default headers are not used, and you own and dispose it.

## Call an API through the proxy

`acn.Proxy(service)` returns an `AcnologiaProxy` whose requests go to `/proxy/{service}/{path}`. The key stays on the server.

```csharp
var weather = acn.Proxy("weather");
using var alert = await weather.PostJsonAsync("/v1/alerts", new { city = "Lisbon" });
if (alert.Headers.TryGetValues("X-Acnologia-Key-Id", out var keyId))
    Console.WriteLine($"{(int)alert.StatusCode} via key {keyId.First()}");
```

- The upstream response comes back unchanged. A non-2xx status is not an exception.
- Responses are returned once the headers arrive, so bodies can stream. Dispose them.
- Each response carries `X-Acnologia-Key-Id`, `X-Acnologia-Attempts` and `X-Acnologia-Service`.

Other helpers:

- `GetAsync(path, query)`, `PostAsync(path, content)`, `Url(path, query)`
- `SendAsync(method, path, content, query)`
- `SendAsync(HttpRequestMessage)`: accepts a relative URI such as `/v1/forecast?city=Lisbon` or an absolute one (only the path and query are used)

### Use the proxy as an HttpClient

`acn.CreateProxyHttpClient(service)` returns an `HttpClient` whose base address is `{baseUrl}/proxy/{service}/`. `AcnologiaProxyHandler` is the `DelegatingHandler` behind it: it reroutes every request's path and query under the proxy and adds the token, so code written against the provider's own base URL keeps working.

```csharp
builder.Services.AddHttpClient("weather", c => c.BaseAddress = new Uri("https://api.weather.example/"))
    .AddHttpMessageHandler(() => new AcnologiaProxyHandler(AcnologiaClientOptions.FromEnvironment(), "weather"))
    .ConfigurePrimaryHttpMessageHandler(() => new SocketsHttpHandler { AllowAutoRedirect = false });
```

Keep redirects off in the primary handler so the app token is not sent to another host.

## Lease a key with `WithKeyAsync`

`WithKeyAsync` leases a key, runs your callback, reports the outcome, and rotates when the callback throws one of the rotation exceptions:

| Callback | Reported outcome | Rotates |
|---|---|---|
| returns | `ok` | no |
| throws `RateLimitedException(retryAfter, statusCode)` | `rate_limited` | yes |
| throws `QuotaExhaustedException(statusCode)` | `quota_exhausted` | yes |
| throws `InvalidKeyException(statusCode)` | `invalid` | yes |
| throws anything else | `error` | no, the exception is rethrown as is |

```csharp
var http = new HttpClient();
var json = await acn.WithKeyAsync("weather", async (lease, ct) =>
{
    using var req = new HttpRequestMessage(HttpMethod.Get,
        lease.Url("/v1/forecast", new Dictionary<string, string> { ["city"] = "Lisbon" }));
    lease.Apply(req); // adds the key where the service expects it
    using var resp = await http.SendAsync(req, ct);
    var status = (int)resp.StatusCode;
    if (status == 429) throw new RateLimitedException(TimeSpan.FromHours(1), status);
    if (status == 402) throw new QuotaExhaustedException(status);
    if (status == 401) throw new InvalidKeyException(status);
    resp.EnsureSuccessStatusCode();
    return await resp.Content.ReadAsStringAsync(ct);
}, new WithKeyOptions { MaxAttempts = 3 }, cancellationToken);
```

Providers differ (some send 429 for an empty quota), so check the body when it matters and throw the exception that fits. A callback without a result uses the `Task` overload.

How it ends:

- A rotation exception is also recognized as the inner exception of another one.
- After `MaxAttempts` keys (default 5) the last rotation exception is rethrown. The final attempt is reported without asking for a next key.
- When the pool has no key left, `NoKeyAvailableException` is thrown (`AllKeysBusyException` when every usable key is at its concurrency limit).
- Every lease is reported, including when the cancellation token fires while your callback runs. The report then goes out with its own 10 second timeout.
- Report messages never contain the secret: it is replaced with `[key]`, and messages are cut at 500 characters.
- `WithKeyOptions.Lease` sets the first lease's `ExcludeKeyIds`, `Ttl`, `Wait` and `Exclusive`. `Ttl`, `Wait` and `Exclusive` also apply to every later lease.

## Lease and report by hand

```csharp
var lease = await acn.LeaseAsync("weather", new LeaseOptions
{
    Ttl = TimeSpan.FromMinutes(5),
    Wait = TimeSpan.FromSeconds(10),
});
// ... call the provider with lease.Apply(request) ...
var next = await lease.ReportAsync(new LeaseReport
{
    Outcome = Outcome.RateLimited,
    StatusCode = 429,
    RetryAfter = TimeSpan.FromHours(1),
    Rotate = true, // ask for the next key in the same call
});
if (next is not null) { /* retry with next */ }
```

Report on every path. `lease.ReportAsync(Outcome.Ok)` is the short form. `lease.RenewAsync(ttl)` extends an open lease for a long job and returns the new expiry.

A lease fails with `ForbiddenException`, code `lease_not_allowed`, when the app is proxy only or the service is shared to you by another Acnologia server. Call those services through the proxy.

**`LeaseOptions`**: `ExcludeKeyIds`, `Ttl` (null uses the server default: 120 seconds on services with a concurrency limit, 300 otherwise), `Wait` (up to `AcnologiaClient.MaxWait`, 60 seconds) and `Exclusive` (keep the key from every other caller, through leases or the proxy, until you report or the lease expires).

**`Lease` fields**: `LeaseId`, `KeyId`, `Service`, `Secret`, `Auth { Type, Name, Prefix }`, `BaseUrl`, `ExpiresAt` and `Exclusive`.

**Placing the key**: `lease.Apply(request)` puts the secret where `Auth.Type` says:

| `Auth.Type` | Where the secret goes |
|---|---|
| `query` | query parameter `Auth.Name` |
| `header` | header `Auth.Name` |
| `bearer` | header `Auth.Name` (default `Authorization`) as `Bearer <secret>` |

A non-empty `Auth.Prefix` goes in front of the secret for every type, and for `bearer` it replaces `Bearer `. `lease.Url(path, query)` joins the provider base URL without the secret.

**Outcomes**: `Ok`, `RateLimited`, `QuotaExhausted`, `Invalid`, `Error`, `Busy` (the account is at its concurrency cap) and `Banned` (the provider suspended the account).

### Concurrency limits

On a service with a per key concurrency limit, each lease holds one of the key's slots until it is reported or its TTL runs out. Set `LeaseOptions.Wait` and the server waits that long for a free slot. Without it a busy pool throws `AllKeysBusyException` right away; retry after its `RetryAfter`. Proxy mode queues requests on the server and needs none of this.

## List services

```csharp
foreach (var s in await acn.ServicesAsync())
    Console.WriteLine($"{s.Slug} available={s.Available} auth={s.Auth.Type}");
```

## Handle errors

All exceptions derive from `AcnologiaException`.

| Type | When |
|---|---|
| `AcnologiaApiException` (`StatusCode`, `Code`, `ServerMessage`) | any non-2xx from the consumer API without a more specific type, such as 404 `lease_not_found` |
| `UnauthenticatedException` | 401: token missing, wrong, revoked or expired |
| `ForbiddenException` | 403: app disabled, or not allowed this service or mode |
| `NoKeyAvailableException` (`RetryAfter`, `Busy`) | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `AllKeysBusyException` (`RetryAfter`) | 503 `all_keys_busy`: every usable key is at its concurrency limit. Derives from `NoKeyAvailableException` |
| `ServiceUnavailableException` (`RetryAfter`) | 503 `service_unavailable`: the provider's circuit breaker is open |
| `RateLimitedException` (`RetryAfter`, `StatusCode`) | you throw it from `WithKeyAsync` |
| `QuotaExhaustedException` (`StatusCode`) | you throw it from `WithKeyAsync` |
| `InvalidKeyException` (`StatusCode`) | you throw it from `WithKeyAsync` |

The API exceptions derive from `AcnologiaApiException`. The three rotation exceptions derive from `KeyRotationException`. An API call that runs past `Timeout` throws `TimeoutException`.

```csharp
try
{
    await acn.LeaseAsync("weather");
}
catch (NoKeyAvailableException e)
{
    await Task.Delay(e.RetryAfter ?? TimeSpan.FromSeconds(1));
}
```

## Keep secrets out of logs

`ToString()`, the debugger view and JSON serialization of a `Lease` never include the secret. `AcnologiaClient`, `AcnologiaClientOptions` and `AcnologiaProxyHandler` print `[redacted]` for the token. The client never logs either.

## Run the tests

From `sdk/csharp`:

```sh
dotnet test
# without a local .NET SDK:
docker run --rm -v "$PWD":/w -w /w mcr.microsoft.com/dotnet/sdk:8.0 dotnet test
```

## More

- [SDK overview](../README.md): every language, install commands, test commands
- [Consumer API reference](../../docs/api.md)
- [Acnologia README](../../README.md)
