using System.Text;
using System.Text.Json;

namespace Acnologia;

public sealed partial class AcnologiaClient
{
    /// <summary>
    /// The token's own access from <c>GET /api/v1/me</c>: app, limits, use, services and, for a
    /// share's token, the share. A share that stopped still answers here, with its
    /// <see cref="ShareInfo.Status"/>.
    /// </summary>
    public async Task<Me> MeAsync(CancellationToken cancellationToken = default) =>
        await SendJsonAsync<Me>(HttpMethod.Get, "/api/v1/me", null, cancellationToken).ConfigureAwait(false)
        ?? throw new AcnologiaException("acnologia: empty me response");

    /// <summary>A service's usage guide: Markdown, the structured guide and whether it was generated.</summary>
    public async Task<Guide> GuideAsync(string service, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(service);
        return await SendJsonAsync<Guide>(HttpMethod.Get, GuidePath(service), null, cancellationToken).ConfigureAwait(false)
            ?? throw new AcnologiaException("acnologia: empty guide response");
    }

    /// <summary>A service's usage guide as Markdown (<c>Accept: text/markdown</c>).</summary>
    public Task<string> GuideMarkdownAsync(string service, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(service);
        return SendTextAsync(HttpMethod.Get, GuidePath(service), null, "text/markdown", true, cancellationToken);
    }

    private static string GuidePath(string service) => "/api/v1/services/" + Uri.EscapeDataString(service) + "/guide";

    /// <summary>The capabilities this app may call (empty for a share's token).</summary>
    public async Task<IReadOnlyList<Capability>> CapabilitiesAsync(CancellationToken cancellationToken = default)
    {
        var res = await SendJsonAsync<CapabilitiesResponse>(HttpMethod.Get, "/api/v1/capabilities", null, cancellationToken).ConfigureAwait(false);
        return (IReadOnlyList<Capability>?)res?.Capabilities ?? Array.Empty<Capability>();
    }

    /// <summary>
    /// Calls a capability: POSTs <paramref name="parameters"/> as JSON to <c>/proxy/{slug}</c>.
    /// Like any proxy call the answer is returned as it is, a non-2xx status included; read
    /// <see cref="ProxyResponseInfo.Capability"/> and <see cref="ProxyResponseInfo.Tried"/> with
    /// <see cref="ProxyResponseExtensions.GetProxyInfo"/>. Dispose the response when done.
    /// </summary>
    /// <param name="slug">The capability slug.</param>
    /// <param name="parameters">The params, serialized as JSON; null sends <c>{}</c>.</param>
    /// <param name="options"><see cref="CapabilityOptions.Service"/> adds <c>?_service=</c>, <see cref="CapabilityOptions.Raw"/> adds <c>?_raw=1</c>.</param>
    /// <param name="cancellationToken">Cancels the call.</param>
    public Task<HttpResponseMessage> CapabilityAsync(string slug, object? parameters = null, CapabilityOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(slug);
        var query = new List<KeyValuePair<string, string>>();
        if (!string.IsNullOrEmpty(options?.Service)) query.Add(new("_service", options.Service));
        if (options?.Raw == true) query.Add(new("_raw", "1"));
        var json = parameters is null ? "{}" : JsonSerializer.Serialize(parameters, parameters.GetType(), Json);
        var qs = Query.Encode(query);
        var url = Endpoint.ProxyPrefix(slug) + (qs.Length > 0 ? "?" + qs : "");
        var req = new HttpRequestMessage(HttpMethod.Post, url)
        {
            Content = new StringContent(json, Encoding.UTF8, "application/json"),
        };
        Endpoint.ApplyHeaders(req);
        options?.ApplyTo(req);
        return _http.SendAsync(req, HttpCompletionOption.ResponseHeadersRead, cancellationToken);
    }
}
