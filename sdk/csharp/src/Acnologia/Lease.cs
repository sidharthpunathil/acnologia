using System.Diagnostics;
using System.Globalization;
using System.Text.Json.Serialization;

namespace Acnologia;

/// <summary>
/// A key handed out by the pool. Report its outcome with <see cref="ReportAsync(LeaseReport, CancellationToken)"/> when done:
/// on services with a concurrency limit an unreported lease holds one of the key's slots until
/// its TTL expires. <see cref="ToString"/>, the debugger view and JSON serialization never
/// include <see cref="Secret"/>.
/// </summary>
[DebuggerDisplay("{ToString(),nq}")]
public sealed class Lease
{
    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    private readonly AcnologiaClient _client;

    internal Lease(AcnologiaClient client, LeaseWire w)
    {
        _client = client;
        LeaseId = w.LeaseId;
        KeyId = w.KeyId;
        Service = w.Service;
        Secret = w.Secret;
        Auth = w.Auth ?? new AuthSpec();
        BaseUrl = (w.BaseUrl ?? "").TrimEnd('/');
        ExpiresAt = ParseTime(w.ExpiresAt);
        Exclusive = w.Exclusive;
    }

    /// <summary>The lease id, <c>lse_...</c>.</summary>
    public string LeaseId { get; }

    /// <summary>The id of the leased key.</summary>
    public long KeyId { get; }

    /// <summary>The service slug.</summary>
    public string Service { get; }

    /// <summary>The provider secret. Never logged, printed or serialized by this SDK.</summary>
    [JsonIgnore]
    [DebuggerBrowsable(DebuggerBrowsableState.Never)]
    public string Secret { get; }

    /// <summary>Where the provider expects the key.</summary>
    public AuthSpec Auth { get; }

    /// <summary>The provider's base URL, without a trailing slash.</summary>
    public string BaseUrl { get; }

    /// <summary>When the lease expires. Updated by <see cref="RenewAsync"/>.</summary>
    public DateTimeOffset? ExpiresAt { get; private set; }

    /// <summary>True when the key is reserved for this lease only.</summary>
    public bool Exclusive { get; }

    /// <summary>
    /// Sends the outcome of this lease. When <see cref="LeaseReport.Rotate"/> is true and another
    /// key is available, the next lease is returned; otherwise null.
    /// </summary>
    public Task<Lease?> ReportAsync(LeaseReport report, CancellationToken cancellationToken = default) =>
        _client.ReportAsync(this, report, cancellationToken);

    /// <summary>Sends a report with just an outcome.</summary>
    public Task<Lease?> ReportAsync(Outcome outcome, CancellationToken cancellationToken = default) =>
        _client.ReportAsync(this, new LeaseReport(outcome), cancellationToken);

    /// <summary>
    /// Extends this open lease so a long job keeps its key. Null uses the server default (120
    /// seconds); the server caps it at 24 hours. Returns the new expiry and updates
    /// <see cref="ExpiresAt"/>. Throws <see cref="AcnologiaApiException"/> with code
    /// <c>lease_not_found</c> when the lease is no longer open.
    /// </summary>
    public async Task<DateTimeOffset> RenewAsync(TimeSpan? ttl = null, CancellationToken cancellationToken = default)
    {
        var exp = await _client.RenewAsync(this, ttl, cancellationToken).ConfigureAwait(false);
        ExpiresAt = exp;
        return exp;
    }

    /// <summary>
    /// Puts the secret on <paramref name="request"/> where the provider expects it: a query
    /// parameter, a header, or an <c>Authorization: Bearer</c> style header. A non empty
    /// <see cref="AuthSpec.Prefix"/> goes in front of the secret for every type; for
    /// <c>bearer</c> it replaces the default <c>Bearer </c> prefix. Header names default to
    /// <c>Authorization</c> when empty.
    /// </summary>
    public void Apply(HttpRequestMessage request)
    {
        ArgumentNullException.ThrowIfNull(request);
        var prefix = Auth.Prefix ?? "";
        var value = prefix + Secret;
        switch (Auth.Type)
        {
            case "query":
                if (request.RequestUri is null)
                    throw new InvalidOperationException("acnologia: request has no URI to add the key to");
                request.RequestUri = Query.Set(request.RequestUri, string.IsNullOrEmpty(Auth.Name) ? "api_key" : Auth.Name, value);
                break;
            case "bearer":
                SetHeader(request, prefix.Length == 0 ? "Bearer " + Secret : value);
                break;
            default:
                SetHeader(request, value);
                break;
        }
    }

    private void SetHeader(HttpRequestMessage request, string value)
    {
        var name = string.IsNullOrEmpty(Auth.Name) ? "Authorization" : Auth.Name;
        request.Headers.Remove(name);
        request.Headers.TryAddWithoutValidation(name, value);
    }

    /// <summary>
    /// Joins <see cref="BaseUrl"/> with <paramref name="path"/> and <paramref name="query"/>. The
    /// secret is not added; call <see cref="Apply"/> on the request for that.
    /// </summary>
    public string Url(string path, IEnumerable<KeyValuePair<string, string>>? query = null)
    {
        var u = BaseUrl + "/" + (path ?? "").TrimStart('/');
        var qs = Query.Encode(query);
        return qs.Length > 0 ? u + "?" + qs : u;
    }

    /// <summary>A description with the secret redacted.</summary>
    public override string ToString() =>
        $"Lease {{ LeaseId = {LeaseId}, KeyId = {KeyId}, Service = {Service}, Secret = [redacted], Auth = {Auth}, BaseUrl = {BaseUrl}, ExpiresAt = {ExpiresAt?.ToString("O", CultureInfo.InvariantCulture)} }}";

    internal static DateTimeOffset? ParseTime(string? s) =>
        !string.IsNullOrEmpty(s) && DateTimeOffset.TryParse(s, CultureInfo.InvariantCulture,
            DateTimeStyles.AssumeUniversal | DateTimeStyles.AdjustToUniversal, out var t) ? t : null;
}
