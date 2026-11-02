using System.Runtime.ExceptionServices;

namespace Acnologia;

public sealed partial class AcnologiaClient
{
    /// <summary>Bounds a report sent after the caller's token was cancelled.</summary>
    internal static readonly TimeSpan ReportTimeout = TimeSpan.FromSeconds(10);

    /// <summary>
    /// Leases a key for <paramref name="service"/> and calls <paramref name="fn"/> with it.
    /// <list type="bullet">
    /// <item><paramref name="fn"/> returns: the lease is reported <c>ok</c> and the result is returned
    /// (a failed report is thrown).</item>
    /// <item><paramref name="fn"/> throws <see cref="RateLimitedException"/>, <see cref="QuotaExhaustedException"/>
    /// or <see cref="InvalidKeyException"/> (directly or as an inner exception): the outcome is reported
    /// with rotate and <paramref name="fn"/> is called again with the next key, up to
    /// <see cref="WithKeyOptions.MaxAttempts"/>. When attempts run out, the last such exception is
    /// rethrown; the final report does not ask for a next key.</item>
    /// <item><paramref name="fn"/> throws anything else: the lease is reported <c>error</c> and the
    /// exception is rethrown unchanged.</item>
    /// </list>
    /// When the pool has no further key, <see cref="NoKeyAvailableException"/> (or
    /// <see cref="AllKeysBusyException"/>) is thrown. Every lease is reported, even when
    /// <paramref name="cancellationToken"/> is cancelled while <paramref name="fn"/> runs (the
    /// report then goes out with its own short timeout). Report messages never contain the secret.
    /// </summary>
    public async Task<T> WithKeyAsync<T>(
        string service,
        Func<Lease, CancellationToken, Task<T>> fn,
        WithKeyOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(fn);
        var opts = options ?? new WithKeyOptions();
        var maxAttempts = opts.MaxAttempts > 0 ? opts.MaxAttempts : 5;
        var first = opts.Lease ?? new LeaseOptions();
        var tried = new List<long>(first.ExcludeKeyIds ?? Array.Empty<long>());

        var lease = await LeaseAsync(service, first, cancellationToken).ConfigureAwait(false);
        ExceptionDispatchInfo? last = null;

        for (var attempt = 1; attempt <= maxAttempts; attempt++)
        {
            T result = default!;
            Exception? failure = null;
            try
            {
                result = await fn(lease, cancellationToken).ConfigureAwait(false);
            }
            catch (Exception e)
            {
                failure = e;
            }

            if (failure is null)
            {
                await ReportDetachedAsync(lease, new LeaseReport(Outcome.Ok), cancellationToken).ConfigureAwait(false);
                return result;
            }

            var captured = ExceptionDispatchInfo.Capture(failure);
            var rotation = FindRotation(failure);
            if (rotation is null)
            {
                await TryReportDetachedAsync(lease, new LeaseReport
                {
                    Outcome = Outcome.Error,
                    Message = Scrubber.Scrub(failure.Message, lease.Secret),
                }, cancellationToken).ConfigureAwait(false);
                captured.Throw();
            }

            last = captured;
            tried.Add(lease.KeyId);
            var report = rotation!.ToReport(lease.Secret);
            if (attempt == maxAttempts || cancellationToken.IsCancellationRequested)
            {
                await TryReportDetachedAsync(lease, report with { Rotate = false }, cancellationToken).ConfigureAwait(false);
                if (attempt < maxAttempts) cancellationToken.ThrowIfCancellationRequested();
                break;
            }

            var next = await ReportAsync(lease, report, cancellationToken).ConfigureAwait(false);
            next ??= await LeaseAsync(service, new LeaseOptions
            {
                ExcludeKeyIds = tried.ToArray(),
                Ttl = first.Ttl,
                Wait = first.Wait,
                Exclusive = first.Exclusive,
            }, cancellationToken).ConfigureAwait(false);
            lease = next;
        }

        last!.Throw();
        throw new InvalidOperationException("unreachable");
    }

    /// <summary>
    /// The same as <see cref="WithKeyAsync{T}"/> for a callback without a result.
    /// </summary>
    public Task WithKeyAsync(
        string service,
        Func<Lease, CancellationToken, Task> fn,
        WithKeyOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(fn);
        return WithKeyAsync<bool>(service, async (l, ct) =>
        {
            await fn(l, ct).ConfigureAwait(false);
            return true;
        }, options, cancellationToken);
    }

    private static KeyRotationException? FindRotation(Exception? e)
    {
        for (var depth = 0; e is not null && depth < 16; depth++)
        {
            if (e is KeyRotationException k) return k;
            e = e is AggregateException { InnerExceptions.Count: 1 } agg ? agg.InnerExceptions[0] : e.InnerException;
        }
        return null;
    }

    private async Task ReportDetachedAsync(Lease lease, LeaseReport report, CancellationToken cancellationToken)
    {
        if (!cancellationToken.IsCancellationRequested)
        {
            await ReportAsync(lease, report, cancellationToken).ConfigureAwait(false);
            return;
        }
        using var cts = new CancellationTokenSource(ReportTimeout);
        await ReportAsync(lease, report, cts.Token).ConfigureAwait(false);
    }

    private async Task TryReportDetachedAsync(Lease lease, LeaseReport report, CancellationToken cancellationToken)
    {
        try
        {
            await ReportDetachedAsync(lease, report, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception)
        {
            // keep the caller's exception
        }
    }
}
