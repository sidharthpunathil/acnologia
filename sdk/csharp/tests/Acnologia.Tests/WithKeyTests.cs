using Xunit;

namespace Acnologia.Tests;

public class WithKeyTests
{
    [Fact]
    public async Task SuccessReportsOk()
    {
        var f = new FakeServer(1);
        using var c = f.Client();
        var calls = 0;
        var result = await c.WithKeyAsync("serpapi", (l, ct) => { calls++; return Task.FromResult(l.KeyId * 10); });
        Assert.Equal(10, result);
        Assert.Equal(1, calls);
        Assert.Equal("ok", Assert.Single(f.Reports)["outcome"]!.GetValue<string>());
    }

    [Fact]
    public async Task RotatesOnRateLimitAndQuota()
    {
        var f = new FakeServer(1, 2, 3);
        using var c = f.Client();
        var seen = new List<long>();
        await c.WithKeyAsync("serpapi", (l, ct) =>
        {
            seen.Add(l.KeyId);
            return l.KeyId switch
            {
                1 => throw new InvalidOperationException("wrapped", new RateLimitedException(TimeSpan.FromSeconds(30), 429)),
                2 => throw new QuotaExhaustedException(429),
                _ => Task.CompletedTask,
            };
        });
        Assert.Equal(new long[] { 1, 2, 3 }, seen);
        Assert.Equal(new[] { "rate_limited", "quota_exhausted", "ok" }, f.Reports.Select(r => r["outcome"]!.GetValue<string>()));
        Assert.Equal(30, f.Reports[0]["retry_after_sec"]!.GetValue<int>());
        Assert.Equal(429, f.Reports[0]["status_code"]!.GetValue<int>());
        Assert.True(f.Reports[0]["rotate"]!.GetValue<bool>());
        Assert.Single(f.Leases);
    }

    [Fact]
    public async Task FallsBackToLeaseWhenReportHasNoNext()
    {
        var f = new FakeServer(1);
        using var c = f.Client();
        await Assert.ThrowsAsync<NoKeyAvailableException>(() =>
            c.WithKeyAsync("serpapi", (l, ct) => Task.FromException(new InvalidKeyException(401))));
        Assert.Equal(2, f.Leases.Count);
        Assert.Equal("[1]", f.Leases[1]["exclude_key_ids"]!.ToJsonString());
        Assert.Equal("invalid", f.Reports[0]["outcome"]!.GetValue<string>());
    }

    [Fact]
    public async Task StopsAfterMaxAttempts()
    {
        var f = new FakeServer(1, 2, 3, 4);
        using var c = f.Client();
        var calls = 0;
        var e = await Assert.ThrowsAsync<RateLimitedException>(() =>
            c.WithKeyAsync("serpapi", (l, ct) => { calls++; throw new RateLimitedException(); }, new WithKeyOptions { MaxAttempts = 2 }));
        Assert.Equal(2, calls);
        Assert.Equal(2, f.Reports.Count);
        Assert.True(f.Reports[0]["rotate"]!.GetValue<bool>());
        Assert.Null(f.Reports[1]["rotate"]);
    }

    [Fact]
    public async Task OtherErrorReportsAndRethrows()
    {
        var f = new FakeServer(1, 2);
        using var c = f.Client();
        var boom = new ApplicationException("boom");
        var calls = 0;
        var e = await Assert.ThrowsAsync<ApplicationException>(() =>
            c.WithKeyAsync("serpapi", (l, ct) => { calls++; throw boom; }));
        Assert.Same(boom, e);
        Assert.Equal(1, calls);
        var r = Assert.Single(f.Reports);
        Assert.Equal("error", r["outcome"]!.GetValue<string>());
        Assert.Equal("boom", r["message"]!.GetValue<string>());
        Assert.Null(r["rotate"]);
    }

    [Fact]
    public async Task ReportMessagesAreScrubbed()
    {
        var f = new FakeServer(1, 2);
        using var c = f.Client();
        await Assert.ThrowsAsync<HttpRequestException>(() => c.WithKeyAsync("serpapi", (l, ct) =>
            l.KeyId == 1
                ? throw new InvalidKeyException(401, "GET https://serpapi.com/search?api_key=" + l.Secret + " failed")
                : throw new HttpRequestException("GET https://serpapi.com/search?api_key=" + l.Secret + new string('x', 600))));
        Assert.Equal("GET https://serpapi.com/search?api_key=[key] failed", f.Reports[0]["message"]!.GetValue<string>());
        var m = f.Reports[1]["message"]!.GetValue<string>();
        Assert.DoesNotContain("sk2", m);
        Assert.StartsWith("GET https://serpapi.com/search?api_key=[key]x", m);
        Assert.Equal(500, m.Length);
    }

    [Fact]
    public async Task ReportsAfterCancellation()
    {
        var f = new FakeServer(1);
        using var c = f.Client();
        using var cts = new CancellationTokenSource();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => c.WithKeyAsync("serpapi", async (l, ct) =>
        {
            cts.Cancel();
            await Task.Delay(Timeout.Infinite, ct);
        }, cancellationToken: cts.Token));
        Assert.Equal("error", Assert.Single(f.Reports)["outcome"]!.GetValue<string>());
    }

    [Fact]
    public async Task RotationAfterCancellationReportsWithoutRotate()
    {
        var f = new FakeServer(1, 2);
        using var c = f.Client();
        using var cts = new CancellationTokenSource();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => c.WithKeyAsync("serpapi", (l, ct) =>
        {
            cts.Cancel();
            throw new RateLimitedException();
        }, cancellationToken: cts.Token));
        var r = Assert.Single(f.Reports);
        Assert.Equal("rate_limited", r["outcome"]!.GetValue<string>());
        Assert.Null(r["rotate"]);
    }

    [Fact]
    public async Task PassesLeaseOptionsToEveryLease()
    {
        var f = new FakeServer(1) { Busy = true };
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<AllKeysBusyException>(() => c.WithKeyAsync("serpapi", (l, ct) => Task.CompletedTask,
            new WithKeyOptions { Lease = new LeaseOptions { Wait = TimeSpan.FromSeconds(20) } }));
        Assert.Equal(20, f.Leases[0]["wait_sec"]!.GetValue<long>());

        f.Busy = false;
        await Assert.ThrowsAsync<NoKeyAvailableException>(() => c.WithKeyAsync("serpapi", (l, ct) => throw new RateLimitedException(),
            new WithKeyOptions { Lease = new LeaseOptions { Wait = TimeSpan.FromSeconds(5), Ttl = TimeSpan.FromSeconds(60), ExcludeKeyIds = new long[] { 7 } } }));
        Assert.Equal("[7]", f.Leases[1]["exclude_key_ids"]!.ToJsonString());
        Assert.Equal("[7,1]", f.Leases[2]["exclude_key_ids"]!.ToJsonString());
        Assert.Equal(5, f.Leases[2]["wait_sec"]!.GetValue<long>());
        Assert.Equal(60, f.Leases[2]["ttl_sec"]!.GetValue<long>());
    }
}
