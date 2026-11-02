using System.Globalization;
using System.Text.Json;

namespace Acnologia;

/// <summary>Base class for every exception thrown by this SDK.</summary>
public class AcnologiaException : Exception
{
    /// <summary>Creates the exception.</summary>
    public AcnologiaException(string message, Exception? innerException = null) : base(message, innerException) { }

    internal static string Seconds(TimeSpan t) => t.TotalSeconds.ToString("0.###", CultureInfo.InvariantCulture) + "s";
}

/// <summary>
/// A non-2xx response from the consumer API that has no more specific type. The body looks
/// like <c>{"error": {"code": "...", "message": "..."}}</c>.
/// </summary>
public class AcnologiaApiException : AcnologiaException
{
    /// <summary>Creates the exception.</summary>
    public AcnologiaApiException(int statusCode, string code, string message)
        : base(Format(statusCode, code, message))
    {
        StatusCode = statusCode;
        Code = code;
        ServerMessage = message;
    }

    /// <summary>The HTTP status code.</summary>
    public int StatusCode { get; }

    /// <summary>The error code from the body, for example <c>lease_not_found</c>. Empty when the body had none.</summary>
    public string Code { get; }

    /// <summary>The error message from the body, or the start of the body when it was not JSON.</summary>
    public string ServerMessage { get; }

    private static string Format(int status, string code, string message) =>
        code.Length > 0 ? $"acnologia: http {status}: {code}: {message}" : $"acnologia: http {status}: {message}";

    internal static AcnologiaApiException FromResponse(int status, string? reason, string body, TimeSpan? headerRetryAfter)
    {
        string code = "", message = "";
        TimeSpan? retryAfter = null;
        try
        {
            using var doc = JsonDocument.Parse(body);
            var root = doc.RootElement;
            if (root.ValueKind == JsonValueKind.Object)
            {
                if (root.TryGetProperty("error", out var err) && err.ValueKind == JsonValueKind.Object)
                {
                    if (err.TryGetProperty("code", out var c) && c.ValueKind == JsonValueKind.String) code = c.GetString() ?? "";
                    if (err.TryGetProperty("message", out var m) && m.ValueKind == JsonValueKind.String) message = m.GetString() ?? "";
                }
                if (root.TryGetProperty("retry_after_sec", out var r) && r.ValueKind == JsonValueKind.Number && r.TryGetDouble(out var secs) && secs > 0)
                {
                    retryAfter = TimeSpan.FromSeconds(secs);
                }
            }
        }
        catch (JsonException)
        {
            // not JSON: fall back to the raw body below
        }
        retryAfter ??= headerRetryAfter;

        if (code is "no_key_available" or "all_keys_busy" || (status == 503 && code.Length == 0))
        {
            return code == "all_keys_busy"
                ? new AllKeysBusyException(message, retryAfter)
                : new NoKeyAvailableException(status, "no_key_available", message, retryAfter);
        }
        if (message.Length == 0 && code.Length == 0)
        {
            message = body.Trim();
            if (message.Length > 200) message = message[..200];
            if (message.Length == 0) message = string.IsNullOrEmpty(reason) ? $"HTTP {status}" : reason;
        }
        if (code == "service_unavailable") return new ServiceUnavailableException(status, code, message, retryAfter);
        if (status == 401) return new UnauthenticatedException(status, code, message);
        if (status == 403) return new ForbiddenException(status, code, message);
        return new AcnologiaApiException(status, code, message);
    }
}

/// <summary>HTTP 401: the app token is missing, wrong, revoked or expired.</summary>
public class UnauthenticatedException : AcnologiaApiException
{
    /// <summary>Creates the exception.</summary>
    public UnauthenticatedException(int statusCode, string code, string message) : base(statusCode, code, message) { }
}

/// <summary>
/// HTTP 403: the app is disabled, or may not use this service or mode (codes
/// <c>service_not_allowed</c>, <c>proxy_not_allowed</c>, <c>lease_not_allowed</c>).
/// </summary>
public class ForbiddenException : AcnologiaApiException
{
    /// <summary>Creates the exception.</summary>
    public ForbiddenException(int statusCode, string code, string message) : base(statusCode, code, message) { }
}

/// <summary>
/// HTTP 503: no key can be leased right now. <see cref="AcnologiaApiException.Code"/> is
/// <c>no_key_available</c> when every key is cooling down, exhausted or excluded, and
/// <c>all_keys_busy</c> (see <see cref="AllKeysBusyException"/>) when usable keys exist but every
/// one is at its concurrency limit.
/// </summary>
public class NoKeyAvailableException : AcnologiaApiException
{
    /// <summary>Creates the exception.</summary>
    public NoKeyAvailableException(int statusCode, string code, string message, TimeSpan? retryAfter)
        : base(statusCode, code, message)
    {
        RetryAfter = retryAfter;
    }

    /// <summary>How long to wait before retrying, when the server knows it.</summary>
    public TimeSpan? RetryAfter { get; }

    /// <summary>True when every usable key is at its concurrency limit (<c>all_keys_busy</c>).</summary>
    public bool Busy => Code == "all_keys_busy";
}

/// <summary>
/// HTTP 503 <c>all_keys_busy</c>: every usable key is at its concurrency limit, or at a rate limit
/// its provider publishes. It clears as soon as another caller reports, so retry after
/// <see cref="NoKeyAvailableException.RetryAfter"/> (usually 1 second) or lease with
/// <see cref="LeaseOptions.Wait"/>. It is a <see cref="NoKeyAvailableException"/>, so existing
/// catches still match.
/// </summary>
public class AllKeysBusyException : NoKeyAvailableException
{
    /// <summary>Creates the exception.</summary>
    public AllKeysBusyException(string message, TimeSpan? retryAfter) : base(503, "all_keys_busy", message, retryAfter) { }
}

/// <summary>
/// HTTP 503 <c>service_unavailable</c>: the provider keeps failing and Acnologia paused traffic to
/// it. Wait <see cref="RetryAfter"/>.
/// </summary>
public class ServiceUnavailableException : AcnologiaApiException
{
    /// <summary>Creates the exception.</summary>
    public ServiceUnavailableException(int statusCode, string code, string message, TimeSpan? retryAfter)
        : base(statusCode, code, message)
    {
        RetryAfter = retryAfter;
    }

    /// <summary>How long to wait before retrying, when the server knows it.</summary>
    public TimeSpan? RetryAfter { get; }
}

/// <summary>
/// Base class for the exceptions that tell <see cref="AcnologiaClient.WithKeyAsync{T}"/> to report
/// the key and rotate to the next one. They are also recognized as the inner exception of
/// another exception.
/// </summary>
public abstract class KeyRotationException : AcnologiaException
{
    /// <summary>Creates the exception.</summary>
    protected KeyRotationException(string message, int? statusCode, string? detail, Exception? innerException)
        : base(message, innerException)
    {
        StatusCode = statusCode;
        Detail = detail;
    }

    /// <summary>The provider's HTTP status code, if known. Sent as <c>status_code</c>.</summary>
    public int? StatusCode { get; }

    /// <summary>A short description sent as the report message (with the secret removed).</summary>
    public string? Detail { get; }

    /// <summary>The outcome this exception reports.</summary>
    public abstract Outcome Outcome { get; }

    internal virtual LeaseReport ToReport(string secret) => new()
    {
        Outcome = Outcome,
        StatusCode = StatusCode,
        Message = string.IsNullOrEmpty(Detail) ? null : Scrubber.Scrub(Detail, secret),
        Rotate = true,
    };

    internal static string Compose(string head, string? detail) =>
        string.IsNullOrEmpty(detail) ? head : head + ": " + detail;
}

/// <summary>Throw from a <see cref="AcnologiaClient.WithKeyAsync{T}"/> callback when the provider rate limited the key.</summary>
public class RateLimitedException : KeyRotationException
{
    /// <summary>Creates the exception.</summary>
    /// <param name="retryAfter">How long the key should cool down, if the provider said so.</param>
    /// <param name="statusCode">The provider's status code, usually 429.</param>
    /// <param name="message">A short description for the report.</param>
    /// <param name="innerException">The underlying exception, if any.</param>
    public RateLimitedException(TimeSpan? retryAfter = null, int? statusCode = null, string? message = null, Exception? innerException = null)
        : base(Compose(retryAfter is { } r && r > TimeSpan.Zero ? $"acnologia: rate limited (retry after {Seconds(r)})" : "acnologia: rate limited", message),
            statusCode, message, innerException)
    {
        RetryAfter = retryAfter;
    }

    /// <summary>How long the key should cool down, if the provider said so.</summary>
    public TimeSpan? RetryAfter { get; }

    /// <inheritdoc />
    public override Outcome Outcome => Outcome.RateLimited;

    internal override LeaseReport ToReport(string secret) =>
        base.ToReport(secret) with { RetryAfter = RetryAfter is { } r && r > TimeSpan.Zero ? r : null };
}

/// <summary>Throw from a <see cref="AcnologiaClient.WithKeyAsync{T}"/> callback when the key's quota is used up.</summary>
public class QuotaExhaustedException : KeyRotationException
{
    /// <summary>Creates the exception.</summary>
    public QuotaExhaustedException(int? statusCode = null, string? message = null, Exception? innerException = null)
        : base(Compose("acnologia: quota exhausted", message), statusCode, message, innerException) { }

    /// <inheritdoc />
    public override Outcome Outcome => Outcome.QuotaExhausted;
}

/// <summary>Throw from a <see cref="AcnologiaClient.WithKeyAsync{T}"/> callback when the provider rejected the key.</summary>
public class InvalidKeyException : KeyRotationException
{
    /// <summary>Creates the exception.</summary>
    public InvalidKeyException(int? statusCode = null, string? message = null, Exception? innerException = null)
        : base(Compose("acnologia: invalid key", message), statusCode, message, innerException) { }

    /// <inheritdoc />
    public override Outcome Outcome => Outcome.Invalid;
}
