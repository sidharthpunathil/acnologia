using System.Net;
using System.Net.Security;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using Xunit;

namespace Acnologia.Tests;

/// <summary>The 0.2.0 surface: me, guides, capabilities, forward proxy, proxy options and typed errors.</summary>
public class SurfaceTests
{
    [Fact]
    public async Task MeParsesAccessLimitsUsageAndShare()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var me = await c.MeAsync();
        Assert.Equal(12, me.App.Id);
        Assert.Equal("crawler", me.App.Name);
        Assert.Equal(1000, me.Limits.DailyRequests);
        Assert.Null(me.Limits.MonthlyRequests);
        Assert.Equal(25.5, me.Limits.MonthlyBudget);
        Assert.Equal(40, me.Usage.MonthRequests);
        Assert.Equal(1.5, me.Usage.MonthCost);
        Assert.Equal(new DateTimeOffset(2026, 11, 1, 9, 0, 0, TimeSpan.Zero), me.Usage.LastUsedAt);
        Assert.Equal("daily_requests", me.LimitHit!.Limit);
        Assert.Equal(new DateTimeOffset(2026, 11, 2, 0, 0, 0, TimeSpan.Zero), me.LimitHit.ResetAt);
        var s = Assert.Single(me.Services);
        Assert.Equal(1, s.Available);
        Assert.StartsWith("data:image/png", s.LogoUrl);
        Assert.Equal("paused", me.Share!.Status);
        Assert.Equal("Sid", me.Share.OwnerName);
        Assert.Null(me.Share.EndsAt);
        var req = Assert.Single(f.Requests);
        Assert.Equal(HttpMethod.Get, req.Method);
        Assert.Equal("/api/v1/me", req.RequestUri!.AbsolutePath);
        Assert.Equal("Bearer acn_test", req.Headers.Authorization!.ToString());
    }

    [Fact]
    public async Task ServicesCarryGuideUrl()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var s = Assert.Single(await c.ServicesAsync());
        Assert.Equal("http://acn.test/api/v1/services/serpapi/guide", s.GuideUrl);
    }

    [Fact]
    public async Task GuideAndGuideMarkdown()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var g = await c.GuideAsync("serpapi");
        Assert.Equal("# SerpAPI\n", g.Markdown);
        Assert.True(g.Generated);
        Assert.Equal("/search.json", g.Content.GetProperty("endpoints")[0].GetProperty("path").GetString());
        Assert.Contains(f.Requests[0].Headers.Accept, a => a.MediaType == "application/json");

        var md = await c.GuideMarkdownAsync("serpapi");
        Assert.Equal("# SerpAPI\n", md);
        Assert.Equal("/api/v1/services/serpapi/guide", f.Requests[1].RequestUri!.AbsolutePath);
        Assert.Contains(f.Requests[1].Headers.Accept, a => a.MediaType == "text/markdown");
    }

    [Fact]
    public async Task Capabilities()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var cap = Assert.Single(await c.CapabilitiesAsync());
        Assert.Equal("web_search", cap.Slug);
        Assert.Equal("priority", cap.Strategy);
        Assert.Equal(JsonValueKind.Array, cap.Params.ValueKind);
        Assert.Equal("query", cap.Params[0].GetProperty("name").GetString());
        Assert.Equal(2, cap.Routes.Count);
        Assert.True(cap.Routes[0].Mapped);
        Assert.Equal("tavily", cap.Routes[1].Service);
        Assert.Equal("http://acn.test/proxy/web_search", cap.Url);
        Assert.Equal("/api/v1/capabilities", f.Requests[0].RequestUri!.AbsolutePath);
    }

    [Fact]
    public async Task CapabilityPostsParamsAndReadsTried()
    {
        var f = new FakeServer();
        using var c = f.Client();
        using (var resp = await c.CapabilityAsync("web_search", new { query = "coffee" }))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("POST", m["method"]);
            Assert.Equal("/proxy/web_search", m["path"]);
            Assert.Equal("", m["query"]);
            Assert.Equal("""{"query":"coffee"}""", m["body"]);
            Assert.Equal("application/json", m["ct"]);
            var info = resp.GetProxyInfo();
            Assert.Equal("web_search", info.Capability);
            Assert.Equal("tavily", info.Service);
            Assert.Equal(new[] { new TriedRoute("serpapi", 429), new TriedRoute("tavily", 200) }, info.Tried);
        }

        using (var resp = await c.CapabilityAsync("web_search", null,
            new CapabilityOptions { Service = "serpapi", Raw = true, IdempotencyKey = "k1", NoCache = true }))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("_service=serpapi&_raw=1", m["query"]);
            Assert.Equal("{}", m["body"]);
            Assert.Equal("k1", m["idem"]);
            Assert.Equal("no-cache", m["cache"]);
        }
    }

    [Fact]
    public async Task ProxyOptionsAndResponseInfo()
    {
        var f = new FakeServer();
        using var c = f.Client();
        using (var resp = await c.Proxy("serpapi", new ProxyRequestOptions { IdempotencyKey = "order-42", NoCache = true }).GetAsync("/search.json"))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("order-42", m["idem"]);
            Assert.Equal("no-cache", m["cache"]);
            var info = resp.GetProxyInfo();
            Assert.Equal(7, info.KeyId);
            Assert.Equal(1, info.Attempts);
            Assert.Equal("serpapi", info.Service);
            Assert.Equal("miss", info.Cache);
            Assert.True(info.Replayed);
            Assert.Null(info.Capability);
            Assert.Empty(info.Tried);
        }

        // Without options nothing extra is sent, and WithOptions applies them later.
        var p = c.Proxy("serpapi");
        using (var resp = await p.GetAsync("/x"))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("", m["idem"]);
            Assert.Equal("", m["cache"]);
            Assert.False(resp.GetProxyInfo().Replayed);
        }
        using (var resp = await p.WithOptions(new() { NoCache = true }).PostJsonAsync("/x", new { a = 1 }))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("no-cache", m["cache"]);
        }

        // ApplyTo, for requests sent through the handler.
        using var http = c.CreateProxyHttpClient("serpapi", f);
        var req = new HttpRequestMessage(HttpMethod.Get, "search.json");
        new ProxyRequestOptions { IdempotencyKey = "h1" }.ApplyTo(req);
        using (var resp = await http.SendAsync(req))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("h1", m["idem"]);
        }
    }

    [Fact]
    public void ProxyInfoWithoutHeaders()
    {
        var info = new HttpResponseMessage(HttpStatusCode.OK).GetProxyInfo();
        Assert.Null(info.KeyId);
        Assert.Null(info.Attempts);
        Assert.Null(info.Service);
        Assert.False(info.Replayed);
        Assert.Empty(info.Tried);
    }

    [Fact]
    public async Task ForwardProxyParsesAndNeverHoldsTheToken()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var fp = await c.ForwardProxyAsync();
        Assert.Equal("http://acn.test:8888", fp.ProxyUrl);
        Assert.Equal("AB:CD", fp.CaFingerprint);
        Assert.True(fp.Passthrough);
        Assert.Equal(2, fp.Hosts.Count);
        Assert.Equal("/~tiktok", fp.Hosts[1].Path);
        Assert.Null(fp.Hosts[0].Path);
        Assert.Equal("1", fp.Env["NODE_USE_ENV_PROXY"]);
        Assert.Equal("X-Acnologia-Service", fp.ServiceHeader);
        Assert.Equal("/api/v1/proxy", f.Requests[0].RequestUri!.AbsolutePath);
        Assert.DoesNotContain(FakeServer.Token, fp.ToString());
        Assert.DoesNotContain(FakeServer.Token, JsonSerializer.Serialize(fp));
    }

    [Fact]
    public async Task ForwardProxyCAIsFetchedWithoutToken()
    {
        var f = new FakeServer();
        using var c = f.Client();
        Assert.Equal(FakeServer.CaPem, await c.ForwardProxyCAAsync());
        var req = Assert.Single(f.Requests);
        Assert.Equal("/api/v1/proxy/ca.pem", req.RequestUri!.AbsolutePath);
        Assert.Null(req.Headers.Authorization);

        var dir = Directory.CreateTempSubdirectory("acn");
        try
        {
            var path = await c.SaveForwardProxyCAAsync(Path.Combine(dir.FullName, "ca.pem"));
            Assert.Equal(FakeServer.CaPem, File.ReadAllText(path));
        }
        finally
        {
            dir.Delete(true);
        }
    }

    [Fact]
    public async Task ForwardProxyUrlAndEnv()
    {
        var f = new FakeServer();
        using var c = new AcnologiaClient(new HttpClient(f), new AcnologiaClientOptions { BaseUrl = FakeServer.Base, Token = FakeServer.Token });
        Assert.Equal("http://app:acn_test@acn.test:8888", await c.ForwardProxyUrlAsync());
        Assert.Equal("http://app:a%2Fb%40c@h:1", AcnologiaClient.ProxyUrlWithToken("http://old:pw@h:1", "a/b@c"));

        var env = await c.ForwardProxyEnvAsync();
        Assert.Equal("http://app:acn_test@acn.test:8888", env["HTTPS_PROXY"]);
        Assert.Equal(env["HTTPS_PROXY"], env["HTTP_PROXY"]);
        Assert.Equal("1", env["NODE_USE_ENV_PROXY"]);
        foreach (var k in new[] { "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE" })
            Assert.Equal("acnologia-ca.pem", env[k]);
        Assert.Equal("/etc/ca.pem", (await c.ForwardProxyEnvAsync("/etc/ca.pem"))["SSL_CERT_FILE"]);
    }

    [Fact]
    public async Task ForwardProxyHandlerUsesProxyCredentialsAndTrustsCA()
    {
        using var caKey = RSA.Create(2048);
        var caReq = new CertificateRequest("CN=Acnologia Test CA", caKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        caReq.CertificateExtensions.Add(new X509BasicConstraintsExtension(true, false, 0, true));
        using var ca = caReq.CreateSelfSigned(DateTimeOffset.UtcNow.AddDays(-1), DateTimeOffset.UtcNow.AddDays(1));

        var f = new FakeServer();
        var pem = ca.ExportCertificatePem();
        using var c = new AcnologiaClient(new HttpClient(new PemServer(f, pem)), new AcnologiaClientOptions { BaseUrl = FakeServer.Base, Token = FakeServer.Token });
        using var handler = await c.CreateForwardProxyHandlerAsync();
        Assert.True(handler.UseProxy);
        var proxy = Assert.IsType<WebProxy>(handler.Proxy);
        Assert.Equal(new Uri("http://acn.test:8888"), proxy.Address);
        var cred = proxy.Credentials!.GetCredential(proxy.Address!, "Basic")!;
        Assert.Equal("app", cred.UserName);
        Assert.Equal(FakeServer.Token, cred.Password);
        Assert.NotNull(handler.ServerCertificateCustomValidationCallback);

        using var leafKey = RSA.Create(2048);
        var leafReq = new CertificateRequest("CN=serpapi.com", leafKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        using var leaf = leafReq.Create(ca, DateTimeOffset.UtcNow.AddHours(-1), DateTimeOffset.UtcNow.AddHours(1), new byte[] { 1, 2, 3, 4 });
        using var other = new CertificateRequest("CN=other", RSA.Create(2048), HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1)
            .CreateSelfSigned(DateTimeOffset.UtcNow.AddDays(-1), DateTimeOffset.UtcNow.AddDays(1));

        Assert.True(AcnologiaClient.TrustsCA(ca, leaf, null, SslPolicyErrors.RemoteCertificateChainErrors));
        Assert.False(AcnologiaClient.TrustsCA(ca, other, null, SslPolicyErrors.RemoteCertificateChainErrors));
        Assert.False(AcnologiaClient.TrustsCA(ca, leaf, null, SslPolicyErrors.RemoteCertificateNameMismatch));
        Assert.True(AcnologiaClient.TrustsCA(ca, other, null, SslPolicyErrors.None));
    }

    [Fact]
    public async Task AccessPasswordIsSentEverywhere()
    {
        var f = new FakeServer();
        using var c = new AcnologiaClient(new HttpClient(f), new AcnologiaClientOptions
        {
            BaseUrl = FakeServer.Base, Token = FakeServer.Token, AccessPassword = "open sesame",
        });
        await c.MeAsync();
        await c.ForwardProxyCAAsync();
        using (await c.Proxy("serpapi").GetAsync("/x")) { }
        using (await c.CapabilityAsync("web_search")) { }
        Assert.Equal(4, f.Requests.Count);
        Assert.All(f.Requests, r => Assert.Equal("open sesame", r.Headers.GetValues("X-Acnologia-Access").Single()));
        Assert.DoesNotContain("open sesame", c.ToString());

        using var plain = f.Client();
        await plain.MeAsync();
        Assert.False(f.Requests[^1].Headers.Contains("X-Acnologia-Access"));
    }

    [Fact]
    public void AccessPasswordFromEnvironment()
    {
        var old = Environment.GetEnvironmentVariable(AcnologiaClient.AccessPasswordVariable);
        try
        {
            Environment.SetEnvironmentVariable(AcnologiaClient.AccessPasswordVariable, "pw");
            Assert.Equal("pw", AcnologiaClientOptions.FromEnvironment().AccessPassword);
            Assert.DoesNotContain("pw", AcnologiaClientOptions.FromEnvironment().ToString());
        }
        finally
        {
            Environment.SetEnvironmentVariable(AcnologiaClient.AccessPasswordVariable, old);
        }
    }

    [Fact]
    public async Task AppLimitReached()
    {
        var f = new FakeServer
        {
            NextErrorStatus = 429,
            NextError = """{"error":{"code":"app_limit_reached","message":"this app reached its daily budget limit"},"limit":"daily_budget","reset_at":"2026-11-02T00:00:00Z","retry_after_sec":3600}""",
        };
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<AppLimitReachedException>(() => c.MeAsync());
        Assert.Equal(429, e.StatusCode);
        Assert.Equal("app_limit_reached", e.Code);
        Assert.Equal("daily_budget", e.Limit);
        Assert.Equal(new DateTimeOffset(2026, 11, 2, 0, 0, 0, TimeSpan.Zero), e.ResetAt);
        Assert.Equal(TimeSpan.FromHours(1), e.RetryAfter);
        Assert.IsAssignableFrom<AcnologiaApiException>(e);
    }

    [Fact]
    public async Task ShareUnavailable()
    {
        foreach (var status in new[] { "revoked", "paused", "expired" })
        {
            var f = new FakeServer { NextErrorStatus = 403, NextError = "{\"error\":{\"code\":\"share_" + status + "\",\"message\":\"the owner " + status + " this share\"}}" };
            using var c = f.Client();
            var e = await Assert.ThrowsAsync<ShareUnavailableException>(() => c.ServicesAsync());
            Assert.Equal(status, e.ShareStatus);
            Assert.Equal("share_" + status, e.Code);
            Assert.IsAssignableFrom<ForbiddenException>(e);
        }
        Assert.Equal("paused", Assert.IsType<ShareUnavailableException>(
            AcnologiaApiException.FromResponse(403, null, """{"error":{"code":"unauthenticated","message":"x"}}""", null, "paused")).ShareStatus);
        Assert.IsType<ForbiddenException>(
            AcnologiaApiException.FromResponse(403, null, """{"error":{"code":"service_not_allowed","message":"x"}}""", null));
    }

    [Fact]
    public async Task GenericErrorsKeepCodeAndRetryAfter()
    {
        var f = new FakeServer { NextErrorStatus = 503, NextError = """{"error":{"code":"no_route_worked","message":"every route failed"},"retry_after_sec":5}""" };
        using var c = f.Client();
        var e = await Assert.ThrowsAsync<AcnologiaApiException>(() => c.CapabilitiesAsync());
        Assert.Equal("no_route_worked", e.Code);
        Assert.Equal(TimeSpan.FromSeconds(5), e.RetryAfter);
    }

    [Fact]
    public void VersionIsBumped() => Assert.Equal("0.2.0", AcnologiaClient.Version);

    /// <summary>Serves a real PEM for the CA route and passes the rest to the fake server.</summary>
    private sealed class PemServer(FakeServer inner, string pem) : DelegatingHandler(inner)
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/api/v1/proxy/ca.pem")
                return Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(pem) });
            return base.SendAsync(request, cancellationToken);
        }
    }
}
