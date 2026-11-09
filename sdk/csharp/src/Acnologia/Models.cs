using System.Text.Json;
using System.Text.Json.Serialization;

namespace Acnologia;

/// <summary>
/// Where a provider expects the key. <see cref="Type"/> is <c>query</c>, <c>header</c> or
/// <c>bearer</c>; <see cref="Name"/> is the query parameter or header name.
/// </summary>
public sealed record AuthSpec
{
    /// <summary>Auth type: <c>query</c>, <c>header</c> or <c>bearer</c>.</summary>
    [JsonPropertyName("type")]
    public string Type { get; init; } = "header";

    /// <summary>Query parameter or header name. Header types default to <c>Authorization</c> when empty.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";

    /// <summary>
    /// Put in front of the secret for every type when not empty. For <c>bearer</c> it replaces
    /// the default <c>Bearer </c> prefix (for example <c>Token </c>).
    /// </summary>
    [JsonPropertyName("prefix")]
    public string? Prefix { get; init; }
}

/// <summary>A service from <c>GET /api/v1/services</c>.</summary>
public sealed record Service
{
    /// <summary>Service slug, used in lease and proxy calls.</summary>
    [JsonPropertyName("slug")]
    public string Slug { get; init; } = "";

    /// <summary>Display name.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";

    /// <summary>Provider id, for example <c>serpapi</c>.</summary>
    [JsonPropertyName("provider")]
    public string Provider { get; init; } = "";

    /// <summary>Category, for example <c>search</c>.</summary>
    [JsonPropertyName("category")]
    public string Category { get; init; } = "";

    /// <summary>True when at least one key can be handed out right now.</summary>
    [JsonPropertyName("available")]
    public bool Available { get; init; }

    /// <summary>The provider's base URL.</summary>
    [JsonPropertyName("base_url")]
    public string BaseUrl { get; init; } = "";

    /// <summary>Where the provider expects the key.</summary>
    [JsonPropertyName("auth")]
    public AuthSpec Auth { get; init; } = new();

    /// <summary>Where the service's usage guide is, <c>{baseUrl}/api/v1/services/{slug}/guide</c>.</summary>
    [JsonPropertyName("guide_url")]
    public string? GuideUrl { get; init; }
}

/// <summary>Optional parameters for <see cref="AcnologiaClient.LeaseAsync"/>.</summary>
public sealed record LeaseOptions
{
    /// <summary>Key ids the server must not hand out, for example keys you already tried.</summary>
    public IReadOnlyCollection<long>? ExcludeKeyIds { get; init; }

    /// <summary>
    /// Lease lifetime, sent as <c>ttl_sec</c> rounded up to whole seconds. Null uses the server
    /// default (120 seconds on services with a concurrency limit, 300 otherwise).
    /// </summary>
    public TimeSpan? Ttl { get; init; }

    /// <summary>
    /// How long the server may wait for a free slot when every usable key is at its concurrency
    /// limit, at most <see cref="AcnologiaClient.MaxWait"/>. Null or zero fails fast with
    /// <see cref="AllKeysBusyException"/>. Sent as <c>wait_sec</c>, rounded up to whole seconds.
    /// </summary>
    public TimeSpan? Wait { get; init; }

    /// <summary>
    /// Keep the key from every other caller, through leases or the proxy, until the lease is
    /// reported or expires.
    /// </summary>
    public bool Exclusive { get; init; }
}

/// <summary>The result of using a leased key.</summary>
public enum Outcome
{
    /// <summary>The call worked (<c>ok</c>).</summary>
    Ok,
    /// <summary>The provider rate limited the key (<c>rate_limited</c>).</summary>
    RateLimited,
    /// <summary>The key's quota is used up (<c>quota_exhausted</c>).</summary>
    QuotaExhausted,
    /// <summary>The provider rejected the key (<c>invalid</c>).</summary>
    Invalid,
    /// <summary>Any other failure; the key is not penalized (<c>error</c>).</summary>
    Error,
    /// <summary>The account is at its concurrency cap; the key steps aside briefly (<c>busy</c>).</summary>
    Busy,
    /// <summary>The provider suspended the account; the key leaves rotation (<c>banned</c>).</summary>
    Banned,
}

/// <summary>How a leased key behaved. Set <see cref="Rotate"/> to receive the next key in the same round trip.</summary>
public sealed record LeaseReport
{
    /// <summary>Creates a report with outcome <see cref="Outcome.Ok"/>.</summary>
    public LeaseReport() { }

    /// <summary>Creates a report with the given outcome.</summary>
    public LeaseReport(Outcome outcome) => Outcome = outcome;

    /// <summary>The outcome.</summary>
    public Outcome Outcome { get; init; } = Outcome.Ok;

    /// <summary>The provider's HTTP status code, if any.</summary>
    public int? StatusCode { get; init; }

    /// <summary>How long the key should cool down, sent as <c>retry_after_sec</c> rounded up.</summary>
    public TimeSpan? RetryAfter { get; init; }

    /// <summary>A short message shown on the dashboard. Do not include the secret.</summary>
    public string? Message { get; init; }

    /// <summary>Ask the server for the next lease in the same call.</summary>
    public bool Rotate { get; init; }
}

/// <summary>Options for <see cref="AcnologiaClient.WithKeyAsync{T}"/>.</summary>
public sealed record WithKeyOptions
{
    /// <summary>How many keys to try before giving up. Default 5.</summary>
    public int MaxAttempts { get; init; } = 5;

    /// <summary>
    /// Options for the first lease (exclusions, TTL, wait, exclusive). TTL, wait and exclusive
    /// also apply to every later lease.
    /// </summary>
    public LeaseOptions? Lease { get; init; }
}

internal static class OutcomeWire
{
    public static string ToWire(this Outcome o) => o switch
    {
        Outcome.Ok => "ok",
        Outcome.RateLimited => "rate_limited",
        Outcome.QuotaExhausted => "quota_exhausted",
        Outcome.Invalid => "invalid",
        Outcome.Error => "error",
        Outcome.Busy => "busy",
        Outcome.Banned => "banned",
        _ => throw new ArgumentOutOfRangeException(nameof(o), o, "unknown outcome"),
    };
}

/// <summary>The token's own access, from <c>GET /api/v1/me</c>.</summary>
public sealed record Me
{
    /// <summary>The app (for a share's token, named after the share).</summary>
    [JsonPropertyName("app")]
    public MeApp App { get; init; } = new();

    /// <summary>The app's limits; each is null when not set.</summary>
    [JsonPropertyName("limits")]
    public AppLimits Limits { get; init; } = new();

    /// <summary>Use today and this month.</summary>
    [JsonPropertyName("usage")]
    public AppUsage Usage { get; init; } = new();

    /// <summary>The limit the app has reached, or null.</summary>
    [JsonPropertyName("limit_hit")]
    public LimitHit? LimitHit { get; init; }

    /// <summary>The services the token may use.</summary>
    [JsonPropertyName("services")]
    public IReadOnlyList<MeService> Services { get; init; } = Array.Empty<MeService>();

    /// <summary>Null for an ordinary app; the share's details for a share's token.</summary>
    [JsonPropertyName("share")]
    public ShareInfo? Share { get; init; }
}

/// <summary>The app in <see cref="Me"/>.</summary>
public sealed record MeApp
{
    /// <summary>App id.</summary>
    [JsonPropertyName("id")]
    public long Id { get; init; }

    /// <summary>App name.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";
}

/// <summary>An app's request and budget limits. Null means no limit.</summary>
public sealed record AppLimits
{
    /// <summary>Requests per day.</summary>
    [JsonPropertyName("daily_requests")]
    public long? DailyRequests { get; init; }

    /// <summary>Requests per month.</summary>
    [JsonPropertyName("monthly_requests")]
    public long? MonthlyRequests { get; init; }

    /// <summary>Spend per day.</summary>
    [JsonPropertyName("daily_budget")]
    public double? DailyBudget { get; init; }

    /// <summary>Spend per month.</summary>
    [JsonPropertyName("monthly_budget")]
    public double? MonthlyBudget { get; init; }
}

/// <summary>An app's use, in <see cref="Me"/>.</summary>
public sealed record AppUsage
{
    /// <summary>Requests today.</summary>
    [JsonPropertyName("today_requests")]
    public long TodayRequests { get; init; }

    /// <summary>Cost today.</summary>
    [JsonPropertyName("today_cost")]
    public double TodayCost { get; init; }

    /// <summary>Requests this month.</summary>
    [JsonPropertyName("month_requests")]
    public long MonthRequests { get; init; }

    /// <summary>Cost this month.</summary>
    [JsonPropertyName("month_cost")]
    public double MonthCost { get; init; }

    /// <summary>When the app last made a request.</summary>
    [JsonPropertyName("last_used_at")]
    public DateTimeOffset? LastUsedAt { get; init; }

    /// <summary>The limit the app has reached, or null.</summary>
    [JsonPropertyName("limit_hit")]
    public LimitHit? LimitHit { get; init; }
}

/// <summary>A limit the app reached and when it resets.</summary>
public sealed record LimitHit
{
    /// <summary><c>daily_requests</c>, <c>monthly_requests</c>, <c>daily_budget</c> or <c>monthly_budget</c>.</summary>
    [JsonPropertyName("limit")]
    public string Limit { get; init; } = "";

    /// <summary>When the limit resets.</summary>
    [JsonPropertyName("reset_at")]
    public DateTimeOffset? ResetAt { get; init; }
}

/// <summary>A service in <see cref="Me"/>.</summary>
public sealed record MeService
{
    /// <summary>Service slug.</summary>
    [JsonPropertyName("slug")]
    public string Slug { get; init; } = "";

    /// <summary>Display name.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";

    /// <summary>Provider id.</summary>
    [JsonPropertyName("provider")]
    public string Provider { get; init; } = "";

    /// <summary>Category.</summary>
    [JsonPropertyName("category")]
    public string Category { get; init; } = "";

    /// <summary>
    /// Keys free right now for a share's token; 1 or 0 for an ordinary app, which never learns pool sizes.
    /// </summary>
    [JsonPropertyName("available")]
    public int Available { get; init; }

    /// <summary>The logo as a <c>data:</c> URI, or null.</summary>
    [JsonPropertyName("logo_url")]
    public string? LogoUrl { get; init; }
}

/// <summary>A share's details, in <see cref="Me"/> for a share's token.</summary>
public sealed record ShareInfo
{
    /// <summary>Share id.</summary>
    [JsonPropertyName("id")]
    public string Id { get; init; } = "";

    /// <summary>Share name.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";

    /// <summary>Who shared it.</summary>
    [JsonPropertyName("owner_name")]
    public string OwnerName { get; init; } = "";

    /// <summary>Who it was shared with.</summary>
    [JsonPropertyName("recipient")]
    public string Recipient { get; init; } = "";

    /// <summary>The owner's label for it.</summary>
    [JsonPropertyName("label")]
    public string Label { get; init; } = "";

    /// <summary><c>active</c>, <c>paused</c>, <c>expired</c> or <c>revoked</c>.</summary>
    [JsonPropertyName("status")]
    public string Status { get; init; } = "";

    /// <summary>How the share may be used, for example <c>proxy</c>.</summary>
    [JsonPropertyName("mode")]
    public string Mode { get; init; } = "";

    /// <summary>When the share ends, or null.</summary>
    [JsonPropertyName("ends_at")]
    public DateTimeOffset? EndsAt { get; init; }
}

/// <summary>A service's usage guide, from <c>GET /api/v1/services/{slug}/guide</c>.</summary>
public sealed record Guide
{
    /// <summary>The guide as Markdown.</summary>
    [JsonPropertyName("markdown")]
    public string Markdown { get; init; } = "";

    /// <summary>True when the server generated it rather than an admin writing it.</summary>
    [JsonPropertyName("generated")]
    public bool Generated { get; init; }

    /// <summary>The structured guide, as raw JSON.</summary>
    [JsonPropertyName("guide")]
    public JsonElement Content { get; init; }
}

/// <summary>A capability, from <c>GET /api/v1/capabilities</c>: one call served by several services.</summary>
public sealed record Capability
{
    /// <summary>Capability slug, callable at <c>/proxy/{slug}</c>.</summary>
    [JsonPropertyName("slug")]
    public string Slug { get; init; } = "";

    /// <summary>Display name.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";

    /// <summary>What it does.</summary>
    [JsonPropertyName("description")]
    public string Description { get; init; } = "";

    /// <summary>How a route is picked, for example <c>priority</c>.</summary>
    [JsonPropertyName("strategy")]
    public string? Strategy { get; init; }

    /// <summary>The parameters it takes, as raw JSON.</summary>
    [JsonPropertyName("params")]
    public JsonElement Params { get; init; }

    /// <summary>The services that can serve it.</summary>
    [JsonPropertyName("routes")]
    public IReadOnlyList<CapabilityRoute> Routes { get; init; } = Array.Empty<CapabilityRoute>();

    /// <summary>The full URL to POST params to.</summary>
    [JsonPropertyName("url")]
    public string Url { get; init; } = "";
}

/// <summary>One service that can serve a <see cref="Capability"/>.</summary>
public sealed record CapabilityRoute
{
    /// <summary>Service slug.</summary>
    [JsonPropertyName("service")]
    public string Service { get; init; } = "";

    /// <summary>True when the answer is mapped to the capability's common shape.</summary>
    [JsonPropertyName("mapped")]
    public bool Mapped { get; init; }
}

/// <summary>
/// Per call options for a proxy request. Use with the <see cref="AcnologiaProxy"/> overloads, or
/// <see cref="ApplyTo"/> on a request sent through <see cref="AcnologiaProxyHandler"/>.
/// </summary>
public record ProxyRequestOptions
{
    /// <summary>Sent as <c>Idempotency-Key</c>: a retry with the same key replays the first answer.</summary>
    public string? IdempotencyKey { get; init; }

    /// <summary>Sends <c>Cache-Control: no-cache</c> so the answer is not served from Acnologia's cache.</summary>
    public bool NoCache { get; init; }

    /// <summary>Adds the headers these options stand for to <paramref name="request"/>.</summary>
    public void ApplyTo(HttpRequestMessage request)
    {
        ArgumentNullException.ThrowIfNull(request);
        if (!string.IsNullOrEmpty(IdempotencyKey))
        {
            request.Headers.Remove("Idempotency-Key");
            request.Headers.TryAddWithoutValidation("Idempotency-Key", IdempotencyKey);
        }
        if (NoCache) request.Headers.CacheControl = new System.Net.Http.Headers.CacheControlHeaderValue { NoCache = true };
    }
}

/// <summary>Options for <see cref="AcnologiaClient.CapabilityAsync"/>.</summary>
public sealed record CapabilityOptions : ProxyRequestOptions
{
    /// <summary>Use only this service's route, sent as <c>?_service=</c>.</summary>
    public string? Service { get; init; }

    /// <summary>Return the provider's body unmapped, sent as <c>?_raw=1</c>.</summary>
    public bool Raw { get; init; }
}

/// <summary>One route a capability call tried, from <c>X-Acnologia-Tried</c>.</summary>
/// <param name="Service">The service slug.</param>
/// <param name="Status">The HTTP status it answered.</param>
public sealed record TriedRoute(string Service, int Status);

/// <summary>
/// The Acnologia headers of a proxy response. Get it with
/// <see cref="ProxyResponseExtensions.GetProxyInfo"/> or <see cref="From"/>.
/// </summary>
public sealed record ProxyResponseInfo
{
    /// <summary>The pool key used (<c>X-Acnologia-Key-Id</c>); null for a share's token.</summary>
    public long? KeyId { get; init; }

    /// <summary>How many keys were tried (<c>X-Acnologia-Attempts</c>).</summary>
    public int? Attempts { get; init; }

    /// <summary>The service that answered (<c>X-Acnologia-Service</c>).</summary>
    public string? Service { get; init; }

    /// <summary><c>hit</c>, <c>miss</c> and so on (<c>X-Acnologia-Cache</c>), when caching applies.</summary>
    public string? Cache { get; init; }

    /// <summary>True when the answer replays an earlier one with the same idempotency key.</summary>
    public bool Replayed { get; init; }

    /// <summary>The capability that served the call (<c>X-Acnologia-Capability</c>).</summary>
    public string? Capability { get; init; }

    /// <summary>The routes a capability call tried, in order (<c>X-Acnologia-Tried</c>).</summary>
    public IReadOnlyList<TriedRoute> Tried { get; init; } = Array.Empty<TriedRoute>();

    /// <summary>Reads the Acnologia headers of <paramref name="response"/>.</summary>
    public static ProxyResponseInfo From(HttpResponseMessage response)
    {
        ArgumentNullException.ThrowIfNull(response);
        string? H(string name) =>
            response.Headers.TryGetValues(name, out var v) ? string.Join(",", v).Trim() is { Length: > 0 } s ? s : null : null;
        var tried = new List<TriedRoute>();
        foreach (var part in (H("X-Acnologia-Tried") ?? "").Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            var eq = part.LastIndexOf('=');
            if (eq > 0 && int.TryParse(part[(eq + 1)..].Trim(), System.Globalization.NumberStyles.Integer, System.Globalization.CultureInfo.InvariantCulture, out var st))
                tried.Add(new TriedRoute(part[..eq].Trim(), st));
        }
        return new ProxyResponseInfo
        {
            KeyId = long.TryParse(H("X-Acnologia-Key-Id"), System.Globalization.NumberStyles.Integer, System.Globalization.CultureInfo.InvariantCulture, out var k) ? k : null,
            Attempts = int.TryParse(H("X-Acnologia-Attempts"), System.Globalization.NumberStyles.Integer, System.Globalization.CultureInfo.InvariantCulture, out var a) ? a : null,
            Service = H("X-Acnologia-Service"),
            Cache = H("X-Acnologia-Cache"),
            Replayed = string.Equals(H("Idempotent-Replayed"), "true", StringComparison.OrdinalIgnoreCase),
            Capability = H("X-Acnologia-Capability"),
            Tried = tried,
        };
    }
}

/// <summary>Reads Acnologia's headers from proxy responses.</summary>
public static class ProxyResponseExtensions
{
    /// <summary>The Acnologia headers of a proxy response: key id, attempts, service, cache and so on.</summary>
    public static ProxyResponseInfo GetProxyInfo(this HttpResponseMessage response) => ProxyResponseInfo.From(response);
}

/// <summary>How to use the forward proxy, from <c>GET /api/v1/proxy</c>. Never holds the token.</summary>
public sealed record ForwardProxy
{
    /// <summary>The proxy address without credentials, for example <c>http://host:8888</c>.</summary>
    [JsonPropertyName("proxy_url")]
    public string ProxyUrl { get; init; } = "";

    /// <summary>The proxy address with an <c>&lt;app token&gt;</c> placeholder where the token goes.</summary>
    [JsonPropertyName("proxy_url_with_token")]
    public string ProxyUrlWithToken { get; init; } = "";

    /// <summary>Where to download the CA certificate.</summary>
    [JsonPropertyName("ca_url")]
    public string CaUrl { get; init; } = "";

    /// <summary>The CA certificate's fingerprint.</summary>
    [JsonPropertyName("ca_fingerprint")]
    public string CaFingerprint { get; init; } = "";

    /// <summary>True when HTTPS to hosts that are no service is tunneled through untouched.</summary>
    [JsonPropertyName("passthrough")]
    public bool Passthrough { get; init; }

    /// <summary>The hosts of the services the token may use.</summary>
    [JsonPropertyName("hosts")]
    public IReadOnlyList<ForwardProxyHost> Hosts { get; init; } = Array.Empty<ForwardProxyHost>();

    /// <summary>The environment an app sets, with the token as a placeholder.</summary>
    [JsonPropertyName("env")]
    public IReadOnlyDictionary<string, string> Env { get; init; } = new Dictionary<string, string>();

    /// <summary>The request header that picks a service when several match a host.</summary>
    [JsonPropertyName("service_header")]
    public string ServiceHeader { get; init; } = "";
}

/// <summary>A host the forward proxy routes to a service.</summary>
public sealed record ForwardProxyHost
{
    /// <summary>The host, for example <c>serpapi.com</c>.</summary>
    [JsonPropertyName("host")]
    public string Host { get; init; } = "";

    /// <summary>The service slug.</summary>
    [JsonPropertyName("service")]
    public string Service { get; init; } = "";

    /// <summary>The service's display name.</summary>
    [JsonPropertyName("name")]
    public string Name { get; init; } = "";

    /// <summary>The base path under the host, when the service has one.</summary>
    [JsonPropertyName("path")]
    public string? Path { get; init; }
}
