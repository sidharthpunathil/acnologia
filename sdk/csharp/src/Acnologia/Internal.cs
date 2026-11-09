using System.Diagnostics;
using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json.Serialization;

namespace Acnologia;

/// <summary>Removes a secret from report messages and caps their length.</summary>
internal static class Scrubber
{
    public const int MaxReportMessage = 500;

    /// <summary>
    /// Replaces the secret (raw, percent encoded and form encoded) with <c>[key]</c> and truncates.
    /// Errors from query auth services often carry the full request URL, key included, and a
    /// report must not copy it to the server.
    /// </summary>
    public static string Scrub(string message, string secret)
    {
        if (!string.IsNullOrEmpty(secret))
        {
            message = message.Replace(secret, "[key]", StringComparison.Ordinal);
            var escaped = Uri.EscapeDataString(secret);
            if (escaped != secret) message = message.Replace(escaped, "[key]", StringComparison.Ordinal);
            var form = WebUtility.UrlEncode(secret);
            if (form != secret && form != escaped) message = message.Replace(form, "[key]", StringComparison.Ordinal);
        }
        if (message.Length > MaxReportMessage)
        {
            var cut = MaxReportMessage;
            if (char.IsHighSurrogate(message[cut - 1])) cut--;
            message = message[..cut];
        }
        return message;
    }
}

/// <summary>Server address and credentials shared by the client and the proxy handler.</summary>
[DebuggerDisplay("{ToString(),nq}")]
internal sealed class Endpoint
{
    public Endpoint(string? baseUrl, string? token, string? userAgent, string? accessPassword = null)
    {
        if (string.IsNullOrWhiteSpace(baseUrl))
            throw new ArgumentException("acnologia: base URL is required (or set ACNOLOGIA_URL)", nameof(baseUrl));
        if (string.IsNullOrWhiteSpace(token))
            throw new ArgumentException("acnologia: app token is required (or set ACNOLOGIA_TOKEN)", nameof(token));
        var trimmed = baseUrl.Trim().TrimEnd('/');
        if (!Uri.TryCreate(trimmed, UriKind.Absolute, out var u) || (u.Scheme != Uri.UriSchemeHttp && u.Scheme != Uri.UriSchemeHttps))
            throw new ArgumentException("acnologia: base URL must be an absolute http or https URL", nameof(baseUrl));
        BaseUrl = trimmed;
        Token = token.Trim();
        UserAgent = string.IsNullOrWhiteSpace(userAgent) ? AcnologiaClient.DefaultUserAgent : userAgent;
        AccessPassword = string.IsNullOrEmpty(accessPassword) ? null : accessPassword;
    }

    public Endpoint(AcnologiaClientOptions o) : this(o.BaseUrl, o.Token, o.UserAgent, o.AccessPassword) { }

    public string BaseUrl { get; }

    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    public string Token { get; }

    public string UserAgent { get; }

    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    public string? AccessPassword { get; }

    public void ApplyHeaders(HttpRequestMessage request, bool withToken = true)
    {
        if (withToken) request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", Token);
        request.Headers.Remove("User-Agent");
        request.Headers.TryAddWithoutValidation("User-Agent", UserAgent);
        if (AccessPassword is not null)
        {
            request.Headers.Remove(AcnologiaClient.AccessHeader);
            request.Headers.TryAddWithoutValidation(AcnologiaClient.AccessHeader, AccessPassword);
        }
    }

    public string ProxyPrefix(string service) => BaseUrl + "/proxy/" + Uri.EscapeDataString(service);

    /// <summary>
    /// Resolves a request URI (relative such as <c>/search.json?q=x</c>, or absolute) under
    /// <c>/proxy/{service}/</c>. Only the path and query are kept. A path that already starts
    /// with the service's proxy prefix is kept as is.
    /// </summary>
    public Uri ProxyUri(string service, Uri requestUri)
    {
        var raw = requestUri.IsAbsoluteUri ? requestUri.PathAndQuery : requestUri.OriginalString;
        var hash = raw.IndexOf('#');
        if (hash >= 0) raw = raw[..hash];
        string path = raw, query = "";
        var q = raw.IndexOf('?');
        if (q >= 0)
        {
            path = raw[..q];
            query = raw[q..];
        }
        if (!path.StartsWith('/')) path = "/" + path;
        var prefix = ProxyPrefix(service);
        var prefixPath = new Uri(prefix).AbsolutePath.TrimEnd('/');
        if (path.Equals(prefixPath, StringComparison.Ordinal) || path.StartsWith(prefixPath + "/", StringComparison.Ordinal))
            path = path[prefixPath.Length..];
        return new Uri(prefix + "/" + path.TrimStart('/') + query, UriKind.Absolute);
    }

    public override string ToString() => $"Endpoint {{ BaseUrl = {BaseUrl}, Token = [redacted] }}";
}

internal static class Query
{
    public static string Encode(IEnumerable<KeyValuePair<string, string>>? query)
    {
        if (query is null) return "";
        var sb = new StringBuilder();
        foreach (var kv in query)
        {
            if (sb.Length > 0) sb.Append('&');
            sb.Append(Uri.EscapeDataString(kv.Key)).Append('=').Append(Uri.EscapeDataString(kv.Value ?? ""));
        }
        return sb.ToString();
    }

    /// <summary>Sets one query parameter on a relative or absolute URI, replacing any existing value.</summary>
    public static Uri Set(Uri uri, string name, string value)
    {
        var s = uri.IsAbsoluteUri ? uri.AbsoluteUri : uri.OriginalString;
        var fragment = "";
        var hash = s.IndexOf('#');
        if (hash >= 0)
        {
            fragment = s[hash..];
            s = s[..hash];
        }
        var existing = "";
        var q = s.IndexOf('?');
        if (q >= 0)
        {
            existing = s[(q + 1)..];
            s = s[..q];
        }
        var parts = new List<string>();
        foreach (var part in existing.Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var key = part.Split('=', 2)[0];
            if (Uri.UnescapeDataString(key.Replace('+', ' ')) != name) parts.Add(part);
        }
        parts.Add(Uri.EscapeDataString(name) + "=" + Uri.EscapeDataString(value));
        return new Uri(s + "?" + string.Join("&", parts) + fragment, uri.IsAbsoluteUri ? UriKind.Absolute : UriKind.Relative);
    }
}

internal sealed class LeaseWire
{
    [JsonPropertyName("lease_id")] public string LeaseId { get; set; } = "";
    [JsonPropertyName("key_id")] public long KeyId { get; set; }
    [JsonPropertyName("service")] public string Service { get; set; } = "";
    [JsonPropertyName("secret")] public string Secret { get; set; } = "";
    [JsonPropertyName("auth")] public AuthSpec? Auth { get; set; }
    [JsonPropertyName("base_url")] public string BaseUrl { get; set; } = "";
    [JsonPropertyName("expires_at")] public string? ExpiresAt { get; set; }
    [JsonPropertyName("exclusive")] public bool Exclusive { get; set; }

    public override string ToString() => $"LeaseWire {{ LeaseId = {LeaseId}, Secret = [redacted] }}";
}

internal sealed class ReportResponse
{
    [JsonPropertyName("ok")] public bool Ok { get; set; }
    [JsonPropertyName("next")] public LeaseWire? Next { get; set; }
}

internal sealed class RenewResponse
{
    [JsonPropertyName("ok")] public bool Ok { get; set; }
    [JsonPropertyName("expires_at")] public string? ExpiresAt { get; set; }
}

internal sealed class ServicesResponse
{
    [JsonPropertyName("services")] public List<Service>? Services { get; set; }
}

internal sealed class CapabilitiesResponse
{
    [JsonPropertyName("capabilities")] public List<Capability>? Capabilities { get; set; }
}
