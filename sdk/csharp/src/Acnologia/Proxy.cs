using System.Diagnostics;
using System.Text;
using System.Text.Json;

namespace Acnologia;

/// <summary>
/// Sends requests through Acnologia's proxy for one service. Acnologia injects a key, retries on
/// rate limits with the next key, and never returns the secret. Upstream responses are returned
/// as they are: a non-2xx status is not an exception. Read Acnologia's answer headers (key id,
/// attempts, service, cache, replay, capability, tried routes) with
/// <see cref="ProxyResponseExtensions.GetProxyInfo"/>. Responses are returned as soon as the
/// headers arrive, so bodies can stream; dispose them when done.
/// </summary>
[DebuggerDisplay("AcnologiaProxy {{ Service = {Service} }}")]
public sealed class AcnologiaProxy
{
    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    private readonly AcnologiaClient _client;

    internal AcnologiaProxy(AcnologiaClient client, string service, ProxyRequestOptions? options = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(service);
        _client = client;
        Service = service;
        Options = options;
    }

    /// <summary>The service slug (or service group, or capability) this proxy calls.</summary>
    public string Service { get; }

    /// <summary>Options applied to every request this proxy sends (idempotency key, no cache), or null.</summary>
    public ProxyRequestOptions? Options { get; }

    /// <summary>
    /// A proxy for the same service that applies <paramref name="options"/> to every request, for
    /// example <c>proxy.WithOptions(new() { IdempotencyKey = "order-42" }).PostJsonAsync(...)</c>.
    /// </summary>
    public AcnologiaProxy WithOptions(ProxyRequestOptions? options) => new(_client, Service, options);

    /// <summary>The full proxy URL for a provider path and query.</summary>
    public string Url(string path, IEnumerable<KeyValuePair<string, string>>? query = null)
    {
        var u = _client.Endpoint.ProxyPrefix(Service) + "/" + (path ?? "").TrimStart('/');
        var qs = Query.Encode(query);
        return qs.Length > 0 ? u + "?" + qs : u;
    }

    /// <summary>
    /// Sends <paramref name="request"/> through the proxy. Its URI may be relative
    /// (<c>/search.json?q=x</c>) or absolute; only the path and query are used, resolved under
    /// <c>/proxy/{service}/</c>. A path that already starts with <c>/proxy/{service}/</c> is kept.
    /// The request is modified in place.
    /// </summary>
    public Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(request);
        request.RequestUri = _client.Endpoint.ProxyUri(Service, request.RequestUri
            ?? throw new ArgumentException("acnologia: request has no URI", nameof(request)));
        _client.Endpoint.ApplyHeaders(request);
        Options?.ApplyTo(request);
        return _client.Http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken);
    }

    /// <summary>Builds and sends a request through the proxy.</summary>
    public Task<HttpResponseMessage> SendAsync(HttpMethod method, string path, HttpContent? content = null,
        IEnumerable<KeyValuePair<string, string>>? query = null, CancellationToken cancellationToken = default)
    {
        var req = new HttpRequestMessage(method, Url(path, query)) { Content = content };
        return SendAsync(req, cancellationToken);
    }

    /// <summary>Sends a GET with optional query parameters.</summary>
    public Task<HttpResponseMessage> GetAsync(string path, IEnumerable<KeyValuePair<string, string>>? query = null,
        CancellationToken cancellationToken = default) =>
        SendAsync(HttpMethod.Get, path, null, query, cancellationToken);

    /// <summary>Sends a POST with the given content.</summary>
    public Task<HttpResponseMessage> PostAsync(string path, HttpContent? content, CancellationToken cancellationToken = default) =>
        SendAsync(HttpMethod.Post, path, content, null, cancellationToken);

    /// <summary>Encodes <paramref name="value"/> as JSON and POSTs it.</summary>
    public Task<HttpResponseMessage> PostJsonAsync<TValue>(string path, TValue value, JsonSerializerOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        var content = new StringContent(JsonSerializer.Serialize(value, options), Encoding.UTF8, "application/json");
        return PostAsync(path, content, cancellationToken);
    }
}

/// <summary>
/// A <see cref="DelegatingHandler"/> that routes every request through the Acnologia proxy for
/// one service and adds the app token. Use it with <c>IHttpClientFactory</c>
/// (<c>AddHttpClient(...).AddHttpMessageHandler(...)</c>) or wrap it in your own
/// <see cref="HttpClient"/>. The request's path and query are resolved under
/// <c>/proxy/{service}/</c>, so code written against the provider's own base URL keeps working.
/// Configure the primary handler not to follow redirects, so the token is not sent elsewhere.
/// </summary>
[DebuggerDisplay("AcnologiaProxyHandler {{ Service = {Service} }}")]
public sealed class AcnologiaProxyHandler : DelegatingHandler
{
    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    private readonly Endpoint _endpoint;

    /// <summary>Creates a handler that uses <paramref name="client"/>'s server and token.</summary>
    public AcnologiaProxyHandler(AcnologiaClient client, string service)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentException.ThrowIfNullOrEmpty(service);
        _endpoint = client.Endpoint;
        Service = service;
    }

    /// <summary>Creates a handler from options (base URL, token, user agent, access password).</summary>
    public AcnologiaProxyHandler(AcnologiaClientOptions options, string service)
    {
        ArgumentNullException.ThrowIfNull(options);
        ArgumentException.ThrowIfNullOrEmpty(service);
        _endpoint = new Endpoint(options);
        Service = service;
    }

    /// <summary>The service slug (or service group) requests go to.</summary>
    public string Service { get; }

    /// <inheritdoc />
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        request.RequestUri = _endpoint.ProxyUri(Service, request.RequestUri
            ?? throw new ArgumentException("acnologia: request has no URI", nameof(request)));
        _endpoint.ApplyHeaders(request);
        return base.SendAsync(request, cancellationToken);
    }

    /// <summary>A description with the token redacted.</summary>
    public override string ToString() => $"AcnologiaProxyHandler {{ Service = {Service}, BaseUrl = {_endpoint.BaseUrl}, Token = [redacted] }}";
}
