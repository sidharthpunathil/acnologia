using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Acnologia.Tests;

/// <summary>A stub Acnologia consumer API, served from an HttpMessageHandler.</summary>
internal sealed class FakeServer : HttpMessageHandler
{
    public const string Base = "http://acn.test";
    public const string Token = "acn_test";
    public const string CaPem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";

    private readonly object _mu = new();
    private readonly long[] _keys;
    private int _next;

    public FakeServer(params long[] keys) => _keys = keys;

    public bool Busy { get; set; }
    public string? NextError { get; set; }
    public int NextErrorStatus { get; set; }
    public List<JsonObject> Leases { get; } = new();
    public List<JsonObject> Reports { get; } = new();
    public List<JsonObject> Renews { get; } = new();
    public List<HttpRequestMessage> Requests { get; } = new();
    public List<string> ProxyBodies { get; } = new();
    public Func<CancellationToken, Task>? BeforeReply { get; set; }

    public static JsonObject LeaseJson(long id) => new()
    {
        ["lease_id"] = $"l{id}",
        ["key_id"] = id,
        ["service"] = "serpapi",
        ["secret"] = $"sk{id}",
        ["auth"] = new JsonObject { ["type"] = "query", ["name"] = "api_key" },
        ["base_url"] = "https://serpapi.com",
        ["expires_at"] = "2026-11-01T12:00:00Z",
    };

    public AcnologiaClient Client(TimeSpan? timeout = null) =>
        new(new HttpClient(this), new AcnologiaClientOptions
        {
            BaseUrl = Base + "/",
            Token = Token,
            Timeout = timeout ?? AcnologiaClient.DefaultTimeout,
        });

    private static HttpResponseMessage Reply(int status, string json) => new((HttpStatusCode)status)
    {
        Content = new StringContent(json, Encoding.UTF8, "application/json"),
    };

    private static async Task<JsonObject> Body(HttpRequestMessage r, CancellationToken ct)
    {
        if (r.Content is null) return new JsonObject();
        var s = await r.Content.ReadAsStringAsync(ct);
        return s.Length == 0 ? new JsonObject() : JsonNode.Parse(s)!.AsObject();
    }

    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage r, CancellationToken ct)
    {
        lock (_mu) Requests.Add(r);
        if (BeforeReply is not null) await BeforeReply(ct);
        if (r.Method == HttpMethod.Get && r.RequestUri!.AbsolutePath == "/api/v1/proxy/ca.pem")
        {
            // public: served without a token
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(CaPem, Encoding.UTF8, "application/x-pem-file") };
        }
        if (r.Headers.Authorization?.ToString() != "Bearer " + Token)
            return Reply(401, """{"error":{"code":"unauthenticated","message":"invalid app token"}}""");
        if (!r.Headers.UserAgent.ToString().StartsWith("acnologia-csharp/", StringComparison.Ordinal))
            return Reply(400, """{"error":{"code":"bad_ua","message":"user agent"}}""");
        if (NextError is { } err)
        {
            NextError = null;
            return Reply(NextErrorStatus, err);
        }

        var path = r.RequestUri!.AbsolutePath;
        var method = r.Method.Method;
        if (method == "GET" && path == "/api/v1/services")
        {
            return Reply(200, """{"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":true,"base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"},"guide_url":"http://acn.test/api/v1/services/serpapi/guide"}]}""");
        }
        if (method == "GET" && path == "/api/v1/me")
        {
            return Reply(200, """
                {"app":{"id":12,"name":"crawler"},
                 "limits":{"daily_requests":1000,"monthly_requests":null,"daily_budget":null,"monthly_budget":25.5},
                 "usage":{"today_requests":3,"today_cost":0.02,"month_requests":40,"month_cost":1.5,"last_used_at":"2026-11-01T09:00:00Z","limit_hit":null},
                 "limit_hit":{"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z"},
                 "services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":1,"logo_url":"data:image/png;base64,AA=="}],
                 "share":{"id":"sh1","name":"For Ann","owner_name":"Sid","recipient":"ann@example.com","label":"trial","status":"paused","mode":"proxy","ends_at":null}}
                """);
        }
        if (method == "GET" && path == "/api/v1/services/serpapi/guide")
        {
            if (r.Headers.Accept.Any(a => a.MediaType == "text/markdown"))
                return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("# SerpAPI\n", Encoding.UTF8, "text/markdown") };
            return Reply(200, """{"markdown":"# SerpAPI\n","generated":true,"guide":{"endpoints":[{"path":"/search.json"}]}}""");
        }
        if (method == "GET" && path == "/api/v1/capabilities")
        {
            return Reply(200, """{"capabilities":[{"slug":"web_search","name":"Web search","description":"Search the web","strategy":"priority","params":[{"name":"query","type":"string","required":true}],"routes":[{"service":"serpapi","mapped":true},{"service":"tavily","mapped":false}],"url":"http://acn.test/proxy/web_search"}]}""");
        }
        if (method == "GET" && path == "/api/v1/proxy")
        {
            return Reply(200, """
                {"proxy_url":"http://acn.test:8888","proxy_url_with_token":"http://app:%3Capp%20token%3E@acn.test:8888",
                 "ca_url":"http://acn.test/api/v1/proxy/ca.pem","ca_fingerprint":"AB:CD","passthrough":true,
                 "hosts":[{"host":"serpapi.com","service":"serpapi","name":"SerpAPI"},{"host":"api.omkar.cloud","service":"omkar","name":"Omkar","path":"/~tiktok"}],
                 "env":{"HTTPS_PROXY":"http://app:%3Capp%20token%3E@acn.test:8888","NODE_USE_ENV_PROXY":"1"},
                 "service_header":"X-Acnologia-Service"}
                """);
        }
        if (method == "POST" && path == "/api/v1/lease")
        {
            var body = await Body(r, ct);
            lock (_mu)
            {
                Leases.Add(body);
                if (Busy)
                    return Reply(503, """{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}""");
                if (_next >= _keys.Length)
                    return Reply(503, """{"error":{"code":"no_key_available","message":"all keys cooling"},"retry_after_sec":42}""");
                return Reply(200, LeaseJson(_keys[_next++]).ToJsonString());
            }
        }
        if (method == "POST" && path.StartsWith("/api/v1/leases/", StringComparison.Ordinal) && path.EndsWith("/report", StringComparison.Ordinal))
        {
            var body = await Body(r, ct);
            body["_lease_id"] = path.Split('/')[4];
            lock (_mu)
            {
                Reports.Add(body);
                JsonNode? next = null;
                if (body["rotate"]?.GetValue<bool>() == true && _next < _keys.Length) next = LeaseJson(_keys[_next++]);
                return Reply(200, new JsonObject { ["ok"] = true, ["next"] = next }.ToJsonString());
            }
        }
        if (method == "POST" && path.EndsWith("/renew", StringComparison.Ordinal))
        {
            var body = await Body(r, ct);
            body["_lease_id"] = path.Split('/')[4];
            lock (_mu) Renews.Add(body);
            if (body["_lease_id"]!.GetValue<string>() == "gone")
                return Reply(404, """{"error":{"code":"lease_not_found","message":"lease not found, already reported, or expired"}}""");
            return Reply(200, """{"ok":true,"expires_at":"2026-11-02T10:00:00.5Z"}""");
        }
        if (path.StartsWith("/proxy/", StringComparison.Ordinal))
        {
            var b = r.Content is null ? "" : await r.Content.ReadAsStringAsync(ct);
            var resp = Reply(200, JsonSerializer.Serialize(new Dictionary<string, string>
            {
                ["method"] = method,
                ["path"] = r.RequestUri.AbsolutePath,
                ["query"] = r.RequestUri.Query.TrimStart('?'),
                ["body"] = b,
                ["ct"] = r.Content?.Headers.ContentType?.MediaType ?? "",
                ["custom"] = r.Headers.TryGetValues("X-Custom", out var v) ? string.Join(",", v) : "",
                ["idem"] = r.Headers.TryGetValues("Idempotency-Key", out var iv) ? string.Join(",", iv) : "",
                ["cache"] = r.Headers.CacheControl?.ToString() ?? "",
            }));
            resp.Headers.Add("X-Acnologia-Key-Id", "7");
            resp.Headers.Add("X-Acnologia-Attempts", "1");
            if (path.StartsWith("/proxy/web_search", StringComparison.Ordinal))
            {
                resp.Headers.Add("X-Acnologia-Service", "tavily");
                resp.Headers.Add("X-Acnologia-Capability", "web_search");
                resp.Headers.TryAddWithoutValidation("X-Acnologia-Tried", "serpapi=429, tavily=200");
            }
            else
            {
                resp.Headers.Add("X-Acnologia-Service", path.Split('/')[2]);
                resp.Headers.Add("X-Acnologia-Cache", "miss");
            }
            if (r.Headers.Contains("Idempotency-Key")) resp.Headers.Add("Idempotent-Replayed", "true");
            return resp;
        }
        return Reply(404, """{"error":{"code":"not_found","message":"no route"}}""");
    }
}
