using System.Diagnostics;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;

namespace Acnologia;

/// <summary>Settings for <see cref="AcnologiaClient"/>. Suitable for binding from configuration.</summary>
[DebuggerDisplay("{ToString(),nq}")]
public sealed class AcnologiaClientOptions
{
    /// <summary>Where Acnologia runs, for example <c>http://localhost:8080</c>.</summary>
    public string? BaseUrl { get; set; }

    /// <summary>The app token, <c>acn_...</c> (tokens with the older <c>cog_</c> prefix still work).</summary>
    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    public string? Token { get; set; }

    /// <summary>Overrides the User-Agent header. Defaults to <see cref="AcnologiaClient.DefaultUserAgent"/>.</summary>
    public string? UserAgent { get; set; }

    /// <summary>
    /// Bounds each API call (services, lease, report, renew). Default
    /// <see cref="AcnologiaClient.DefaultTimeout"/>, longer than <see cref="AcnologiaClient.MaxWait"/>
    /// so a lease that waits for a slot can still succeed. <see cref="System.Threading.Timeout.InfiniteTimeSpan"/>
    /// or zero disables it. Proxy calls are not bounded by it; pass a cancellation token instead.
    /// </summary>
    public TimeSpan Timeout { get; set; } = AcnologiaClient.DefaultTimeout;

    /// <summary>
    /// The deploy's access password, for a server behind one. Sent as <c>X-Acnologia-Access</c>
    /// on every request when set.
    /// </summary>
    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    public string? AccessPassword { get; set; }

    /// <summary>
    /// Reads <c>ACNOLOGIA_URL</c>, <c>ACNOLOGIA_TOKEN</c> and <c>ACNOLOGIA_ACCESS_PASSWORD</c>.
    /// Values that are not set stay null.
    /// </summary>
    public static AcnologiaClientOptions FromEnvironment() => new()
    {
        BaseUrl = Environment.GetEnvironmentVariable(AcnologiaClient.UrlVariable),
        Token = Environment.GetEnvironmentVariable(AcnologiaClient.TokenVariable),
        AccessPassword = Environment.GetEnvironmentVariable(AcnologiaClient.AccessPasswordVariable),
    };

    /// <summary>A description with the token redacted.</summary>
    public override string ToString() => $"AcnologiaClientOptions {{ BaseUrl = {BaseUrl}, Token = [redacted] }}";
}

/// <summary>
/// Client for an Acnologia key pool, authenticated with an app token. Two ways to use pooled keys:
/// lease mode (<see cref="LeaseAsync"/>, <see cref="WithKeyAsync{T}"/>) hands you a key and you
/// report how it went; proxy mode (<see cref="Proxy(string)"/>, <see cref="AcnologiaProxyHandler"/>) sends
/// requests through Acnologia, which injects a key and never exposes it. Thread safe.
/// </summary>
[DebuggerDisplay("{ToString(),nq}")]
public sealed partial class AcnologiaClient : IDisposable
{
    /// <summary>The SDK version.</summary>
    public const string Version = "0.2.0";

    /// <summary>The default User-Agent, <c>acnologia-csharp/{Version}</c>.</summary>
    public const string DefaultUserAgent = "acnologia-csharp/" + Version;

    /// <summary>Environment variable read for the base URL.</summary>
    public const string UrlVariable = "ACNOLOGIA_URL";

    /// <summary>Environment variable read for the app token.</summary>
    public const string TokenVariable = "ACNOLOGIA_TOKEN";

    /// <summary>Environment variable read for the access password.</summary>
    public const string AccessPasswordVariable = "ACNOLOGIA_ACCESS_PASSWORD";

    /// <summary>The header that carries the access password.</summary>
    public const string AccessHeader = "X-Acnologia-Access";

    /// <summary>Default bound on API calls, longer than <see cref="MaxWait"/>.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(75);

    /// <summary>The longest <see cref="LeaseOptions.Wait"/> the server accepts.</summary>
    public static readonly TimeSpan MaxWait = TimeSpan.FromSeconds(60);

    private static readonly JsonSerializerOptions Json = new();

    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    internal readonly Endpoint Endpoint;
    private readonly HttpClient _http;
    private readonly bool _ownsHttp;
    private readonly TimeSpan _timeout;

    /// <summary>Creates a client with its own <see cref="HttpClient"/> (redirects are not followed).</summary>
    /// <param name="baseUrl">The Acnologia server, for example <c>http://localhost:8080</c>.</param>
    /// <param name="token">The app token, <c>acn_...</c>.</param>
    public AcnologiaClient(string baseUrl, string token)
        : this(new AcnologiaClientOptions { BaseUrl = baseUrl, Token = token }) { }

    /// <summary>Creates a client with its own <see cref="HttpClient"/> (redirects are not followed).</summary>
    public AcnologiaClient(AcnologiaClientOptions options)
        : this(CreateDefaultHttpClient(), options, ownsHttp: true) { }

    /// <summary>
    /// Creates a client on a caller supplied <see cref="HttpClient"/>, for example a typed client
    /// from <c>IHttpClientFactory</c>. The client's <see cref="HttpClient.BaseAddress"/> and
    /// default headers are not used. Its own <see cref="HttpClient.Timeout"/> still applies, so keep it
    /// longer than any lease wait. The caller owns and disposes it.
    /// </summary>
    public AcnologiaClient(HttpClient httpClient, AcnologiaClientOptions options)
        : this(httpClient, options, ownsHttp: false) { }

    private AcnologiaClient(HttpClient httpClient, AcnologiaClientOptions options, bool ownsHttp)
    {
        ArgumentNullException.ThrowIfNull(httpClient);
        ArgumentNullException.ThrowIfNull(options);
        Endpoint = new Endpoint(options);
        _http = httpClient;
        _ownsHttp = ownsHttp;
        _timeout = options.Timeout;
    }

    /// <summary>
    /// Creates a client from the <c>ACNOLOGIA_URL</c> and <c>ACNOLOGIA_TOKEN</c> environment
    /// variables. Throws <see cref="ArgumentException"/> when either is missing.
    /// </summary>
    /// <param name="httpClient">Optional HTTP client; null creates one the client owns.</param>
    public static AcnologiaClient FromEnvironment(HttpClient? httpClient = null)
    {
        var o = AcnologiaClientOptions.FromEnvironment();
        return httpClient is null ? new AcnologiaClient(o) : new AcnologiaClient(httpClient, o);
    }

    /// <summary>The Acnologia server, without a trailing slash.</summary>
    public string BaseUrl => Endpoint.BaseUrl;

    /// <summary>The User-Agent sent with every request.</summary>
    public string UserAgent => Endpoint.UserAgent;

    internal HttpClient Http => _http;

    private static HttpClient CreateDefaultHttpClient() =>
        new(new SocketsHttpHandler
        {
            AllowAutoRedirect = false,
            PooledConnectionLifetime = TimeSpan.FromMinutes(5),
        })
        {
            // API calls are bounded per call by Options.Timeout; proxied bodies may stream longer.
            Timeout = System.Threading.Timeout.InfiniteTimeSpan,
        };

    /// <summary>Lists the services this app token may use, each with its <see cref="Service.GuideUrl"/>.</summary>
    public async Task<IReadOnlyList<Service>> ServicesAsync(CancellationToken cancellationToken = default)
    {
        var res = await SendJsonAsync<ServicesResponse>(HttpMethod.Get, "/api/v1/services", null, cancellationToken).ConfigureAwait(false);
        return (IReadOnlyList<Service>?)res?.Services ?? Array.Empty<Service>();
    }

    /// <summary>
    /// Asks the pool for a key. Throws <see cref="NoKeyAvailableException"/> when the pool is empty
    /// or every key is cooling down, and <see cref="AllKeysBusyException"/> when every usable key
    /// is at its concurrency limit and <see cref="LeaseOptions.Wait"/> ran out.
    /// Always report the lease: on services with a concurrency limit it holds one of the key's
    /// slots until it is reported or its TTL expires.
    /// </summary>
    public async Task<Lease> LeaseAsync(string service, LeaseOptions? options = null, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(service);
        var body = new Dictionary<string, object> { ["service"] = service };
        if (options is not null)
        {
            if (options.ExcludeKeyIds is { Count: > 0 } ex) body["exclude_key_ids"] = ex.ToArray();
            if (options.Ttl is { } ttl && ttl > TimeSpan.Zero) body["ttl_sec"] = CeilSeconds(ttl);
            if (options.Wait is { } wait)
            {
                if (wait < TimeSpan.Zero || wait > MaxWait)
                    throw new ArgumentOutOfRangeException(nameof(options), wait, "acnologia: lease wait must be between 0 and 60 seconds");
                if (wait > TimeSpan.Zero) body["wait_sec"] = CeilSeconds(wait);
            }
            if (options.Exclusive) body["exclusive"] = true;
        }
        var w = await SendJsonAsync<LeaseWire>(HttpMethod.Post, "/api/v1/lease", body, cancellationToken).ConfigureAwait(false)
            ?? throw new AcnologiaException("acnologia: empty lease response");
        return new Lease(this, w);
    }

    /// <summary>
    /// Sends the outcome of a lease. When <see cref="LeaseReport.Rotate"/> is true and another key
    /// is available, the next lease is returned; otherwise null.
    /// </summary>
    public async Task<Lease?> ReportAsync(Lease lease, LeaseReport report, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(lease);
        ArgumentNullException.ThrowIfNull(report);
        var body = new Dictionary<string, object> { ["outcome"] = report.Outcome.ToWire() };
        if (report.StatusCode is { } sc && sc != 0) body["status_code"] = sc;
        if (report.RetryAfter is { } ra && ra > TimeSpan.Zero) body["retry_after_sec"] = CeilSeconds(ra);
        if (!string.IsNullOrEmpty(report.Message)) body["message"] = report.Message;
        if (report.Rotate) body["rotate"] = true;
        var path = "/api/v1/leases/" + Uri.EscapeDataString(lease.LeaseId) + "/report";
        var res = await SendJsonAsync<ReportResponse>(HttpMethod.Post, path, body, cancellationToken).ConfigureAwait(false);
        return res?.Next is { } next ? new Lease(this, next) : null;
    }

    /// <summary>
    /// Extends an open lease. Null uses the server default (120 seconds); the server caps it at
    /// 24 hours. Returns the new expiry. Prefer <see cref="Lease.RenewAsync"/>, which also updates
    /// <see cref="Lease.ExpiresAt"/>.
    /// </summary>
    public async Task<DateTimeOffset> RenewAsync(Lease lease, TimeSpan? ttl = null, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(lease);
        var body = new Dictionary<string, object>();
        if (ttl is { } t && t > TimeSpan.Zero) body["ttl_sec"] = CeilSeconds(t);
        var path = "/api/v1/leases/" + Uri.EscapeDataString(lease.LeaseId) + "/renew";
        var res = await SendJsonAsync<RenewResponse>(HttpMethod.Post, path, body, cancellationToken).ConfigureAwait(false);
        return Lease.ParseTime(res?.ExpiresAt) ?? throw new AcnologiaException("acnologia: renew response has no expires_at");
    }

    /// <summary>A proxy helper for <paramref name="service"/>: requests go to <c>/proxy/{service}/...</c>.</summary>
    public AcnologiaProxy Proxy(string service) => new(this, service);

    /// <summary>
    /// A proxy helper for <paramref name="service"/> that applies <paramref name="options"/>
    /// (<see cref="ProxyRequestOptions.IdempotencyKey"/>, <see cref="ProxyRequestOptions.NoCache"/>) to every request.
    /// </summary>
    public AcnologiaProxy Proxy(string service, ProxyRequestOptions? options) => new(this, service, options);

    /// <summary>
    /// Creates an <see cref="HttpClient"/> whose requests go through the Acnologia proxy for
    /// <paramref name="service"/>. Its <see cref="HttpClient.BaseAddress"/> is
    /// <c>{baseUrl}/proxy/{service}/</c>, so relative paths such as <c>search.json?q=x</c> work;
    /// absolute URIs are rerouted by path. Redirects are not followed unless
    /// <paramref name="innerHandler"/> does so. The caller disposes the returned client.
    /// </summary>
    public HttpClient CreateProxyHttpClient(string service, HttpMessageHandler? innerHandler = null)
    {
        var handler = new AcnologiaProxyHandler(this, service)
        {
            InnerHandler = innerHandler ?? new SocketsHttpHandler { AllowAutoRedirect = false },
        };
        return new HttpClient(handler, disposeHandler: true)
        {
            BaseAddress = new Uri(Endpoint.ProxyPrefix(service) + "/"),
            Timeout = System.Threading.Timeout.InfiniteTimeSpan,
        };
    }

    internal static long CeilSeconds(TimeSpan t) => (long)Math.Ceiling(t.TotalSeconds);

    internal async Task<T?> SendJsonAsync<T>(HttpMethod method, string path, object? body, CancellationToken cancellationToken)
        where T : class
    {
        var text = await SendTextAsync(method, path, body, "application/json", true, cancellationToken).ConfigureAwait(false);
        if (string.IsNullOrWhiteSpace(text)) return null;
        try
        {
            return JsonSerializer.Deserialize<T>(text, Json);
        }
        catch (JsonException e)
        {
            throw new AcnologiaException("acnologia: decode response: " + e.Message, e);
        }
    }

    /// <summary>Sends an API call bounded by the timeout and returns the body text, throwing on a non-2xx.</summary>
    internal async Task<string> SendTextAsync(HttpMethod method, string path, object? body, string accept, bool withToken,
        CancellationToken cancellationToken)
    {
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        var bounded = _timeout > TimeSpan.Zero && _timeout != System.Threading.Timeout.InfiniteTimeSpan;
        if (bounded) cts.CancelAfter(_timeout);

        using var req = new HttpRequestMessage(method, Endpoint.BaseUrl + path);
        Endpoint.ApplyHeaders(req, withToken);
        req.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue(accept));
        if (body is not null)
        {
            req.Content = new StringContent(JsonSerializer.Serialize(body, Json), Encoding.UTF8, "application/json");
        }

        try
        {
            using var resp = await _http.SendAsync(req, HttpCompletionOption.ResponseContentRead, cts.Token).ConfigureAwait(false);
            var text = await resp.Content.ReadAsStringAsync(cts.Token).ConfigureAwait(false);
            if (!resp.IsSuccessStatusCode)
            {
                var share = resp.Headers.TryGetValues("X-Acnologia-Share", out var sv) ? sv.FirstOrDefault() : null;
                throw AcnologiaApiException.FromResponse((int)resp.StatusCode, resp.ReasonPhrase, text, resp.Headers.RetryAfter?.Delta, share);
            }
            return text;
        }
        catch (OperationCanceledException e) when (bounded && cts.IsCancellationRequested && !cancellationToken.IsCancellationRequested)
        {
            throw new TimeoutException($"acnologia: {method} {path} timed out after {AcnologiaException.Seconds(_timeout)}", e);
        }
    }

    /// <summary>Disposes the HTTP client when this client created it.</summary>
    public void Dispose()
    {
        if (_ownsHttp) _http.Dispose();
    }

    /// <summary>A description with the token redacted.</summary>
    public override string ToString() => $"AcnologiaClient {{ BaseUrl = {Endpoint.BaseUrl}, Token = [redacted] }}";
}
