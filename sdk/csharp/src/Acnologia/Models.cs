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
