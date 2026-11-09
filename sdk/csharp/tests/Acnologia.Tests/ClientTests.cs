using System.Net;
using System.Net.Sockets;
using System.Text.Json;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Acnologia.Tests;

public class ClientTests
{
    [Fact]
    public async Task ServicesParsesAndSendsTokenAndUserAgent()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var svcs = await c.ServicesAsync();
        var s = Assert.Single(svcs);
        Assert.Equal("serpapi", s.Slug);
        Assert.True(s.Available);
        Assert.Equal("https://serpapi.com", s.BaseUrl);
        Assert.Equal(new AuthSpec { Type = "query", Name = "api_key" }, s.Auth);
        var req = Assert.Single(f.Requests);
        Assert.Equal("http://acn.test/api/v1/services", req.RequestUri!.ToString());
        Assert.Equal($"acnologia-csharp/{AcnologiaClient.Version}", req.Headers.UserAgent.ToString());
    }

    [Fact]
    public async Task BadTokenThrowsUnauthenticated()
    {
        var f = new FakeServer();
        using var c = new AcnologiaClient(new HttpClient(f), new AcnologiaClientOptions { BaseUrl = FakeServer.Base, Token = "acn_wrong" });
        var e = await Assert.ThrowsAsync<UnauthenticatedException>(() => c.ServicesAsync());
        Assert.Equal(401, e.StatusCode);
        Assert.Equal("unauthenticated", e.Code);
        Assert.Equal("invalid app token", e.ServerMessage);
        Assert.IsAssignableFrom<AcnologiaApiException>(e);
    }

    [Fact]
    public async Task ForbiddenAndOtherErrors()
    {
        var f = new FakeServer(1) { NextErrorStatus = 403, NextError = """{"error":{"code":"lease_not_allowed","message":"proxy only"}}""" };
        using var c = f.Client();
        var fe = await Assert.ThrowsAsync<ForbiddenException>(() => c.LeaseAsync("serpapi"));
        Assert.Equal("lease_not_allowed", fe.Code);

        f.NextErrorStatus = 409;
        f.NextError = """{"error":{"code":"report_failed","message":"lease already reported"}}""";
        var ae = await Assert.ThrowsAsync<AcnologiaApiException>(() => c.ServicesAsync());
        Assert.Equal(409, ae.StatusCode);
        Assert.Equal("report_failed", ae.Code);
        Assert.Contains("lease already reported", ae.Message);

        f.NextErrorStatus = 502;
        f.NextError = "upstream down";
        ae = await Assert.ThrowsAsync<AcnologiaApiException>(() => c.ServicesAsync());
        Assert.Equal("", ae.Code);
        Assert.Equal("upstream down", ae.ServerMessage);
    }

    [Fact]
    public async Task ServiceUnavailable()
    {
        var f = new FakeServer(1)
        {
            NextErrorStatus = 503,
            NextError = """{"error":{"code":"service_unavailable","message":"paused"},"retry_after_sec":30}""",
        };
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<ServiceUnavailableException>(() => c.LeaseAsync("serpapi"));
        Assert.Equal(TimeSpan.FromSeconds(30), e.RetryAfter);
    }

    [Fact]
    public async Task LeaseAndReport()
    {
        var f = new FakeServer(1, 2);
        using var c = f.Client();
        var l = await c.LeaseAsync("serpapi", new LeaseOptions { ExcludeKeyIds = new long[] { 9 }, Ttl = TimeSpan.FromSeconds(30) });
        Assert.Equal("l1", l.LeaseId);
        Assert.Equal(1, l.KeyId);
        Assert.Equal("sk1", l.Secret);
        Assert.Equal("https://serpapi.com", l.BaseUrl);
        Assert.Equal(new DateTimeOffset(2026, 11, 1, 12, 0, 0, TimeSpan.Zero), l.ExpiresAt);
        Assert.Equal("serpapi", f.Leases[0]["service"]!.GetValue<string>());
        Assert.Equal("[9]", f.Leases[0]["exclude_key_ids"]!.ToJsonString());
        Assert.Equal(30, f.Leases[0]["ttl_sec"]!.GetValue<long>());
        Assert.Null(f.Leases[0]["wait_sec"]);

        var next = await l.ReportAsync(new LeaseReport
        {
            Outcome = Outcome.RateLimited,
            StatusCode = 429,
            RetryAfter = TimeSpan.FromSeconds(1.5),
            Message = "slow down",
            Rotate = true,
        });
        Assert.NotNull(next);
        Assert.Equal(2, next!.KeyId);
        var r = f.Reports[0];
        Assert.Equal("l1", r["_lease_id"]!.GetValue<string>());
        Assert.Equal("rate_limited", r["outcome"]!.GetValue<string>());
        Assert.Equal(429, r["status_code"]!.GetValue<int>());
        Assert.Equal(2, r["retry_after_sec"]!.GetValue<int>());
        Assert.Equal("slow down", r["message"]!.GetValue<string>());
        Assert.True(r["rotate"]!.GetValue<bool>());

        Assert.Null(await next.ReportAsync(Outcome.Ok));
        Assert.Equal("""{"outcome":"ok","_lease_id":"l2"}""", f.Reports[1].ToJsonString());
    }

    [Theory]
    [InlineData(Outcome.Ok, "ok")]
    [InlineData(Outcome.RateLimited, "rate_limited")]
    [InlineData(Outcome.QuotaExhausted, "quota_exhausted")]
    [InlineData(Outcome.Invalid, "invalid")]
    [InlineData(Outcome.Error, "error")]
    [InlineData(Outcome.Busy, "busy")]
    [InlineData(Outcome.Banned, "banned")]
    public async Task OutcomeWireNames(Outcome o, string wire)
    {
        var f = new FakeServer(1);
        using var c = f.Client();
        var l = await c.LeaseAsync("serpapi");
        await c.ReportAsync(l, new LeaseReport(o));
        Assert.Equal(wire, f.Reports[0]["outcome"]!.GetValue<string>());
    }

    [Fact]
    public async Task NoKeyAvailable()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<NoKeyAvailableException>(() => c.LeaseAsync("serpapi"));
        Assert.Equal(TimeSpan.FromSeconds(42), e.RetryAfter);
        Assert.False(e.Busy);
        Assert.Equal("no_key_available", e.Code);
        Assert.Equal(503, e.StatusCode);
    }

    [Fact]
    public async Task EmptyCode503IsNoKeyAvailable()
    {
        var f = new FakeServer(1) { NextErrorStatus = 503, NextError = "" };
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<NoKeyAvailableException>(() => c.LeaseAsync("serpapi"));
        Assert.Equal("no_key_available", e.Code);
    }

    [Fact]
    public async Task AllKeysBusy()
    {
        var f = new FakeServer(1) { Busy = true };
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<AllKeysBusyException>(() => c.LeaseAsync("serpapi"));
        Assert.True(e.Busy);
        Assert.Equal(TimeSpan.FromSeconds(1), e.RetryAfter);
        Assert.IsAssignableFrom<NoKeyAvailableException>(e);
    }

    [Fact]
    public async Task LeaseSendsWaitAndExclusive()
    {
        var f = new FakeServer(1, 2);
        using var c = f.Client();
        await c.LeaseAsync("serpapi", new LeaseOptions { Wait = TimeSpan.FromSeconds(1.2), Exclusive = true });
        Assert.Equal(2, f.Leases[0]["wait_sec"]!.GetValue<long>());
        Assert.True(f.Leases[0]["exclusive"]!.GetValue<bool>());
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => c.LeaseAsync("serpapi", new LeaseOptions { Wait = TimeSpan.FromSeconds(61) }));
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => c.LeaseAsync("serpapi", new LeaseOptions { Wait = TimeSpan.FromSeconds(-1) }));
        Assert.Single(f.Leases);
    }

    [Fact]
    public async Task Renew()
    {
        var f = new FakeServer(1);
        using var c = f.Client();
        var l = await c.LeaseAsync("serpapi");
        var exp = await l.RenewAsync(TimeSpan.FromMinutes(10));
        Assert.Equal(600, f.Renews[0]["ttl_sec"]!.GetValue<long>());
        Assert.Equal("l1", f.Renews[0]["_lease_id"]!.GetValue<string>());
        Assert.Equal(new DateTimeOffset(2026, 11, 2, 10, 0, 0, 500, TimeSpan.Zero), exp);
        Assert.Equal(exp, l.ExpiresAt);

        await l.RenewAsync();
        Assert.Null(f.Renews[1]["ttl_sec"]);
    }

    [Fact]
    public async Task RenewUnknownLease()
    {
        var f = new FakeServer();
        f.NextErrorStatus = 404;
        f.NextError = """{"error":{"code":"lease_not_found","message":"gone"}}""";
        using var c = f.Client();
        var l = new Lease(c, new LeaseWire { LeaseId = "gone" });
        var e = await Assert.ThrowsAsync<AcnologiaApiException>(() => l.RenewAsync());
        Assert.Equal("lease_not_found", e.Code);
        Assert.Equal(404, e.StatusCode);
    }

    [Fact]
    public async Task TimeoutBoundsApiCalls()
    {
        var f = new FakeServer(1) { BeforeReply = ct => Task.Delay(TimeSpan.FromSeconds(10), ct) };
        using var c = f.Client(TimeSpan.FromMilliseconds(100));
        await Assert.ThrowsAsync<TimeoutException>(() => c.ServicesAsync());

        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(50));
        using var c2 = f.Client();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => c2.ServicesAsync(cts.Token));
    }

    [Fact]
    public void ApplyPutsTheKeyWhereTheServiceExpectsIt()
    {
        using var c = new FakeServer().Client();
        Lease Make(string type, string name, string? prefix = null) =>
            new(c, new LeaseWire { LeaseId = "l", Secret = "s e/cret", Auth = new AuthSpec { Type = type, Name = name, Prefix = prefix }, BaseUrl = "https://p.test/" });

        var req = new HttpRequestMessage(HttpMethod.Get, "https://p.test/search.json?q=a&api_key=old#frag");
        Make("query", "api_key").Apply(req);
        Assert.Equal("https://p.test/search.json?q=a&api_key=s%20e%2Fcret#frag", req.RequestUri!.AbsoluteUri);

        var rel = new HttpRequestMessage(HttpMethod.Get, new Uri("/search.json", UriKind.Relative));
        Make("query", "key").Apply(rel);
        Assert.Equal("/search.json?key=s%20e%2Fcret", rel.RequestUri!.OriginalString);

        req = new HttpRequestMessage(HttpMethod.Get, "https://p.test/");
        Make("header", "xi-api-key").Apply(req);
        Assert.Equal("s e/cret", req.Headers.GetValues("xi-api-key").Single());

        req = new HttpRequestMessage(HttpMethod.Get, "https://p.test/");
        req.Headers.TryAddWithoutValidation("Authorization", "old");
        Make("bearer", "").Apply(req);
        Assert.Equal("Bearer s e/cret", req.Headers.GetValues("Authorization").Single());

        req = new HttpRequestMessage(HttpMethod.Get, "https://p.test/");
        Make("bearer", "", "Token ").Apply(req);
        Assert.Equal("Token s e/cret", req.Headers.GetValues("Authorization").Single());

        req = new HttpRequestMessage(HttpMethod.Get, "https://p.test/");
        Make("header", "", "Key ").Apply(req);
        Assert.Equal("Key s e/cret", req.Headers.GetValues("Authorization").Single());

        req = new HttpRequestMessage(HttpMethod.Get, "https://p.test/x");
        Make("query", "k", "pre-").Apply(req);
        Assert.Equal("https://p.test/x?k=pre-s%20e%2Fcret", req.RequestUri!.AbsoluteUri);

        var u = Make("query", "k").Url("/search.json", new Dictionary<string, string> { ["q"] = "a b" });
        Assert.Equal("https://p.test/search.json?q=a%20b", u);
    }

    [Fact]
    public async Task LeaseParsesAuthPrefix()
    {
        var f = new FakeServer(1)
        {
            NextErrorStatus = 200,
            NextError = """{"lease_id":"l1","key_id":1,"service":"x","secret":"s","auth":{"type":"bearer","name":"Authorization","prefix":"Token "},"base_url":"https://x.test","expires_at":"2026-11-01T12:00:00Z","exclusive":true}""",
        };
        using var c = f.Client();
        var l = await c.LeaseAsync("x");
        Assert.Equal("Token ", l.Auth.Prefix);
        Assert.True(l.Exclusive);
    }

    [Fact]
    public async Task SecretsNeverAppearInStringsOrJson()
    {
        var f = new FakeServer(1);
        using var c = f.Client();
        var l = await c.LeaseAsync("serpapi");
        Assert.DoesNotContain("sk1", l.ToString());
        Assert.Contains("[redacted]", l.ToString());
        Assert.DoesNotContain("sk1", JsonSerializer.Serialize(l));
        Assert.DoesNotContain(FakeServer.Token, c.ToString());
        Assert.DoesNotContain(FakeServer.Token, new AcnologiaClientOptions { BaseUrl = "http://x", Token = FakeServer.Token }.ToString());
        Assert.DoesNotContain(FakeServer.Token, new AcnologiaProxyHandler(c, "serpapi").ToString());
        Assert.DoesNotContain(FakeServer.Token, c.Endpoint.ToString());
    }

    [Fact]
    public void ConstructorValidates()
    {
        Assert.Throws<ArgumentException>(() => new AcnologiaClient("", "acn_x"));
        Assert.Throws<ArgumentException>(() => new AcnologiaClient("http://x", ""));
        Assert.Throws<ArgumentException>(() => new AcnologiaClient("not a url", "acn_x"));
        using var c = new AcnologiaClient("http://localhost:9/base/", "acn_x");
        Assert.Equal("http://localhost:9/base", c.BaseUrl);
        Assert.Equal("acnologia-csharp/0.2.0", c.UserAgent);
    }

    [Fact]
    public void FromEnvironment()
    {
        var oldUrl = Environment.GetEnvironmentVariable(AcnologiaClient.UrlVariable);
        var oldTok = Environment.GetEnvironmentVariable(AcnologiaClient.TokenVariable);
        try
        {
            Environment.SetEnvironmentVariable("ACNOLOGIA_URL", null);
            Environment.SetEnvironmentVariable("ACNOLOGIA_TOKEN", null);
            Assert.Throws<ArgumentException>(() => AcnologiaClient.FromEnvironment());

            Environment.SetEnvironmentVariable("ACNOLOGIA_URL", "http://env.test:1234/");
            Environment.SetEnvironmentVariable("ACNOLOGIA_TOKEN", "acn_env");
            using var c = AcnologiaClient.FromEnvironment();
            Assert.Equal("http://env.test:1234", c.BaseUrl);
            Assert.Equal("acn_env", c.Endpoint.Token);
        }
        finally
        {
            Environment.SetEnvironmentVariable("ACNOLOGIA_URL", oldUrl);
            Environment.SetEnvironmentVariable("ACNOLOGIA_TOKEN", oldTok);
        }
    }

    [Fact]
    public async Task WorksAsHttpClientFactoryTypedClient()
    {
        var f = new FakeServer(1);
        var services = new ServiceCollection();
        services.AddSingleton(new AcnologiaClientOptions { BaseUrl = FakeServer.Base, Token = FakeServer.Token });
        services.AddHttpClient<AcnologiaClient>().ConfigurePrimaryHttpMessageHandler(() => f);
        services.AddHttpClient("serp")
            .ConfigureHttpClient(h => h.BaseAddress = new Uri("https://serpapi.com/"))
            .AddHttpMessageHandler(() => new AcnologiaProxyHandler(
                new AcnologiaClientOptions { BaseUrl = FakeServer.Base, Token = FakeServer.Token }, "serpapi"))
            .ConfigurePrimaryHttpMessageHandler(() => f);
        using var sp = services.BuildServiceProvider();

        var c = sp.GetRequiredService<AcnologiaClient>();
        var l = await c.LeaseAsync("serpapi");
        Assert.Equal(1, l.KeyId);

        var http = sp.GetRequiredService<IHttpClientFactory>().CreateClient("serp");
        using var resp = await http.GetAsync("/search.json?q=x");
        var m = await resp.Content.ReadFromJsonAsync();
        Assert.Equal("/proxy/serpapi/search.json", m["path"]);
        Assert.Equal("q=x", m["query"]);
    }

    [Fact]
    public async Task ProxyDoesNotFollowRedirects()
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        var port = ((IPEndPoint)listener.LocalEndpoint).Port;
        listener.Stop();
        using var http = new HttpListener();
        http.Prefixes.Add($"http://127.0.0.1:{port}/");
        http.Start();
        var hits = new List<string>();
        var serve = Task.Run(async () =>
        {
            var ctx = await http.GetContextAsync();
            lock (hits) hits.Add(ctx.Request.Url!.AbsolutePath + " " + ctx.Request.Headers["Authorization"]);
            ctx.Response.StatusCode = 302;
            ctx.Response.RedirectLocation = $"http://127.0.0.1:{port}/elsewhere";
            ctx.Response.Close();
        });

        using var c = new AcnologiaClient($"http://127.0.0.1:{port}", "acn_test");
        using var resp = await c.Proxy("serpapi").GetAsync("/search.json");
        await serve;
        Assert.Equal(HttpStatusCode.Found, resp.StatusCode);
        Assert.Equal(new[] { "/proxy/serpapi/search.json Bearer acn_test" }, hits);
    }
}

internal static class JsonExt
{
    public static async Task<Dictionary<string, string>> ReadFromJsonAsync(this HttpContent content) =>
        JsonSerializer.Deserialize<Dictionary<string, string>>(await content.ReadAsStringAsync())!;
}
