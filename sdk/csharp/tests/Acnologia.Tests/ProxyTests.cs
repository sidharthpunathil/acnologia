using System.Text;
using Xunit;

namespace Acnologia.Tests;

public class ProxyTests
{
    [Fact]
    public async Task GetWithQuery()
    {
        var f = new FakeServer();
        using var c = f.Client();
        using var resp = await c.Proxy("serpapi").GetAsync("/search.json", new Dictionary<string, string> { ["engine"] = "google", ["q"] = "coffee shop" });
        var m = await resp.Content.ReadFromJsonAsync();
        Assert.Equal("GET", m["method"]);
        Assert.Equal("/proxy/serpapi/search.json", m["path"]);
        Assert.Equal("engine=google&q=coffee%20shop", m["query"]);
        Assert.Equal("7", resp.Headers.GetValues("X-Acnologia-Key-Id").Single());
        Assert.Equal("http://acn.test/proxy/serpapi/v1/x?a=b", c.Proxy("serpapi").Url("v1/x", new Dictionary<string, string> { ["a"] = "b" }));
    }

    [Fact]
    public async Task PostJsonAndSend()
    {
        var f = new FakeServer();
        using var c = f.Client();
        var p = c.Proxy("elevenlabs");
        using (var resp = await p.PostJsonAsync("/v1/text-to-speech/abc", new { text = "hi" }))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("POST", m["method"]);
            Assert.Equal("/proxy/elevenlabs/v1/text-to-speech/abc", m["path"]);
            Assert.Equal("""{"text":"hi"}""", m["body"]);
            Assert.Equal("application/json", m["ct"]);
        }

        var req = new HttpRequestMessage(HttpMethod.Put, new Uri("/v1/voices?x=1", UriKind.Relative))
        {
            Content = new StringContent("raw", Encoding.UTF8, "text/plain"),
        };
        req.Headers.Add("X-Custom", "yes");
        using (var resp = await p.SendAsync(req))
        {
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("PUT", m["method"]);
            Assert.Equal("/proxy/elevenlabs/v1/voices", m["path"]);
            Assert.Equal("x=1", m["query"]);
            Assert.Equal("raw", m["body"]);
            Assert.Equal("yes", m["custom"]);
        }

        // An absolute URI is rerouted by path, and an existing proxy prefix is kept once.
        foreach (var uri in new[] { "https://api.elevenlabs.io/v1/models", "http://acn.test/proxy/elevenlabs/v1/models" })
        {
            using var resp = await p.SendAsync(new HttpRequestMessage(HttpMethod.Get, uri));
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("/proxy/elevenlabs/v1/models", m["path"]);
        }
    }

    [Fact]
    public async Task ProxyHttpClient()
    {
        var f = new FakeServer();
        using var c = f.Client();
        using var http = c.CreateProxyHttpClient("serpapi", f);
        Assert.Equal("http://acn.test/proxy/serpapi/", http.BaseAddress!.ToString());
        foreach (var path in new[] { "search.json?q=x", "/search.json?q=x", "https://serpapi.com/search.json?q=x" })
        {
            using var resp = await http.GetAsync(path);
            var m = await resp.Content.ReadFromJsonAsync();
            Assert.Equal("/proxy/serpapi/search.json", m["path"]);
            Assert.Equal("q=x", m["query"]);
        }
        Assert.All(f.Requests, r => Assert.Equal("Bearer acn_test", r.Headers.Authorization!.ToString()));
    }

    [Fact]
    public async Task NonSuccessIsReturnedNotThrown()
    {
        var f = new FakeServer { NextErrorStatus = 429, NextError = """{"error":"slow"}""" };
        using var c = f.Client();
        using var resp = await c.Proxy("serpapi").GetAsync("/x");
        Assert.Equal(429, (int)resp.StatusCode);
    }
}
