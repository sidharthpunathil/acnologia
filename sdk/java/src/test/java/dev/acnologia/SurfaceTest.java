package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.net.http.HttpResponse;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The calls added in 0.2.0. */
class SurfaceTest {
    MockServer srv;
    Acnologia c;

    @BeforeEach
    void setUp() throws IOException {
        srv = new MockServer();
        c = srv.client();
    }

    @AfterEach
    void tearDown() {
        srv.close();
    }

    private static JsonNode json(byte[] b) throws IOException {
        return MockServer.JSON.readTree(b);
    }

    @Test
    void version() {
        assertEquals("0.2.0", Acnologia.VERSION);
    }

    @Test
    void me() {
        Me me = c.me();
        assertEquals("GET", srv.lastMethod);
        assertEquals("Bearer acn_test", srv.lastHeaders.getFirst("Authorization"));
        assertEquals(12, me.app().id());
        assertEquals("crawler", me.app().name());
        assertEquals(1000L, me.limits().dailyRequests());
        assertNull(me.limits().monthlyRequests());
        assertNull(me.limits().dailyBudget());
        assertEquals(25.5, me.limits().monthlyBudget());
        assertEquals(4000, me.usage().monthRequests());
        assertEquals(0.25, me.usage().todayCost());
        assertEquals(Instant.parse("2026-11-01T10:00:00Z"), me.usage().lastUsedAt());
        assertEquals("daily_requests", me.usage().limitHit().limit());
        assertEquals(Instant.parse("2026-11-02T00:00:00Z"), me.limitHit().resetAt());
        assertEquals(1, me.services().size());
        assertEquals("serpapi", me.services().get(0).slug());
        assertEquals(1, me.services().get(0).available());
        assertNull(me.services().get(0).logoUrl());
        assertEquals("paused", me.share().status());
        assertEquals("Ana", me.share().ownerName());
        assertEquals("bob@example.com", me.share().recipient());
        assertNull(me.share().endsAt());
        assertEquals("crawler", c.meAsync().join().app().name());
    }

    @Test
    void servicesHaveGuideUrl() {
        assertEquals("http://acn.test/api/v1/services/serpapi/guide", c.services().get(0).guideUrl());
    }

    @Test
    void guide() {
        Guide g = c.guide("serpapi");
        assertEquals("application/json", srv.lastHeaders.getFirst("Accept"));
        assertEquals("# SerpAPI", g.markdown());
        assertTrue(g.generated());
        assertEquals("/search.json", g.guide().path("endpoints").get(0).path("path").asText());
        assertEquals("# SerpAPI", c.guideAsync("serpapi").join().markdown());
    }

    @Test
    void guideMarkdown() {
        assertEquals("# SerpAPI\n\nSearch Google.", c.guideMarkdown("serpapi"));
        assertEquals("text/markdown", srv.lastHeaders.getFirst("Accept"));
        assertEquals("Bearer acn_test", srv.lastHeaders.getFirst("Authorization"));
        assertEquals("# SerpAPI\n\nSearch Google.", c.guideMarkdownAsync("serpapi").join());
        ApiException e = assertThrows(ApiException.class, () -> c.guideMarkdown("nope"));
        assertEquals(404, e.statusCode());
    }

    @Test
    void capabilities() {
        List<Capability> caps = c.capabilities();
        assertEquals("GET", srv.lastMethod);
        assertEquals(1, caps.size());
        Capability cap = caps.get(0);
        assertEquals("web_search", cap.slug());
        assertEquals("priority", cap.strategy());
        assertEquals("query", cap.params().get(0).path("name").asText());
        assertEquals(List.of(new Capability.Route("serpapi", true), new Capability.Route("tavily", false)), cap.routes());
        assertEquals("http://acn.test/proxy/web_search", cap.url());
        assertEquals(1, c.capabilitiesAsync().join().size());
    }

    @Test
    void capabilityCall() throws IOException {
        ProxyResponse<byte[]> res = c.capability("web_search", Map.of("query", "coffee"),
                CapabilityOptions.builder().service("serpapi").raw(true).idempotencyKey("k1").noCache(true).build());
        JsonNode body = json(res.body());
        assertEquals("POST", body.path("method").asText());
        assertEquals("/proxy/web_search", body.path("path").asText());
        assertEquals("_service=serpapi&_raw=1", body.path("query").asText());
        assertEquals("{\"query\":\"coffee\"}", body.path("body").asText());
        assertEquals("application/json", body.path("ct").asText());
        assertEquals("k1", body.path("idem").asText());
        assertEquals("no-cache", body.path("cache_control").asText());
        assertEquals("web_search", res.capability());
        assertEquals(List.of(new ProxyResponse.Tried("tavily", 502), new ProxyResponse.Tried("serpapi", 200)), res.tried());
        assertTrue(res.replayed());

        ProxyResponse<byte[]> plain = c.capabilityAsync("web_search", null, null).join();
        JsonNode b2 = json(plain.body());
        assertEquals("{}", b2.path("body").asText());
        assertTrue(b2.path("query").isNull());
        assertTrue(b2.path("idem").isNull());
        assertFalse(plain.replayed());
    }

    @Test
    void proxyResponseAccessors() {
        ProxyResponse<byte[]> res = ProxyResponse.of(c.proxy("serpapi").get("/search.json"));
        assertEquals(7L, res.keyId().getAsLong());
        assertEquals(2, res.attempts());
        assertEquals("serpapi", res.service());
        assertEquals("miss", res.cache());
        assertFalse(res.replayed());
        assertEquals("", res.capability());
        assertTrue(res.tried().isEmpty());
        assertEquals(200, res.statusCode());
    }

    @Test
    void proxyIdempotencyKeyAndNoCache() throws IOException {
        Proxy p = c.proxy("serpapi");
        Proxy q = p.idempotencyKey("abc").noCache();
        HttpResponse<byte[]> res = q.get("/search.json");
        JsonNode body = json(res.body());
        assertEquals("abc", body.path("idem").asText());
        assertEquals("no-cache", body.path("cache_control").asText());
        assertTrue(ProxyResponse.of(res).replayed());
        // The original helper is unchanged.
        JsonNode plain = json(p.get("/search.json").body());
        assertTrue(plain.path("idem").isNull());
        assertTrue(plain.path("cache_control").isNull());
        // A request built by hand gets the headers too.
        JsonNode sent = json(q.requestAsync("POST", "/x", null, null, null).join().body());
        assertEquals("abc", sent.path("idem").asText());
    }

    @Test
    void forwardProxy() {
        ForwardProxy fp = c.forwardProxy();
        assertEquals("GET", srv.lastMethod);
        assertEquals("http://acn.test:8888", fp.proxyUrl());
        assertEquals("sha256:ab", fp.caFingerprint());
        assertTrue(fp.passthrough());
        assertEquals(new ForwardProxy.Host("serpapi.com", "serpapi", "SerpAPI", "/"), fp.hosts().get(0));
        assertEquals("1", fp.env().get("NODE_USE_ENV_PROXY"));
        assertEquals("X-Acnologia-Service", fp.serviceHeader());
        assertFalse(fp.toString().contains("acn_test"), fp.toString());
        assertEquals("http://acn.test:8888", c.forwardProxyAsync().join().proxyUrl());
    }

    @Test
    void forwardProxyCaSendsNoToken(@TempDir Path dir) throws IOException {
        assertEquals(MockServer.CA_PEM, c.forwardProxyCa());
        assertNull(srv.lastHeaders.getFirst("Authorization"));
        assertEquals(MockServer.CA_PEM, c.forwardProxyCaAsync().join());
        Path out = c.saveForwardProxyCa(dir.resolve("ca.pem"));
        assertEquals(MockServer.CA_PEM, Files.readString(out));
    }

    @Test
    void forwardProxyUrlAndEnv() {
        assertEquals("http://app:acn_test@acn.test:8888", c.forwardProxyUrl());
        assertEquals("http://app:acn_test@acn.test:8888", c.forwardProxyUrlAsync().join());
        Map<String, String> env = c.forwardProxyEnv();
        assertEquals(List.of("HTTPS_PROXY", "HTTP_PROXY", "NODE_USE_ENV_PROXY", "NODE_EXTRA_CA_CERTS",
                "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE"), List.copyOf(env.keySet()));
        assertEquals("http://app:acn_test@acn.test:8888", env.get("HTTPS_PROXY"));
        assertEquals("http://app:acn_test@acn.test:8888", env.get("HTTP_PROXY"));
        assertEquals("1", env.get("NODE_USE_ENV_PROXY"));
        assertEquals("acnologia-ca.pem", env.get("SSL_CERT_FILE"));
        Map<String, String> custom = c.forwardProxyEnvAsync("/etc/acn.pem").join();
        assertEquals("/etc/acn.pem", custom.get("NODE_EXTRA_CA_CERTS"));
        assertEquals("/etc/acn.pem", custom.get("REQUESTS_CA_BUNDLE"));
    }

    @Test
    void accessPassword() {
        Acnologia a = Acnologia.builder().baseUrl(srv.url()).token("acn_test").accessPassword("open sesame").build();
        a.services();
        assertEquals("open sesame", srv.lastHeaders.getFirst("X-Acnologia-Access"));
        a.forwardProxyCa();
        assertEquals("open sesame", srv.lastHeaders.getFirst("X-Acnologia-Access"));
        a.proxy("serpapi").get("/x");
        assertEquals("open sesame", srv.lastHeaders.getFirst("X-Acnologia-Access"));

        Acnologia fromEnv = Acnologia.builder().env(Map.of("ACNOLOGIA_URL", srv.url(), "ACNOLOGIA_TOKEN", "acn_test",
                "ACNOLOGIA_ACCESS_PASSWORD", "env pw")).build();
        fromEnv.me();
        assertEquals("env pw", srv.lastHeaders.getFirst("X-Acnologia-Access"));

        c.services();
        assertNull(srv.lastHeaders.getFirst("X-Acnologia-Access"));
    }

    @Test
    void appLimitReached() {
        srv.failStatus = 429;
        srv.failBody = "{\"error\":{\"code\":\"app_limit_reached\",\"message\":\"this app reached its daily requests limit\"},"
                + "\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-02T00:00:00Z\",\"retry_after_sec\":3600}";
        AppLimitReachedException e = assertThrows(AppLimitReachedException.class, () -> c.lease("serpapi"));
        assertEquals(429, e.statusCode());
        assertEquals("app_limit_reached", e.code());
        assertEquals("daily_requests", e.limit());
        assertEquals(Instant.parse("2026-11-02T00:00:00Z"), e.resetAt());
        assertEquals(Duration.ofHours(1), e.retryAfter());
    }

    @Test
    void retryAfterHeaderFallback() {
        srv.failStatus = 429;
        srv.failBody = "{\"error\":{\"code\":\"app_limit_reached\",\"message\":\"limit\"},\"limit\":\"monthly_budget\"}";
        srv.failHeaders = Map.of("Retry-After", "120");
        AppLimitReachedException e = assertThrows(AppLimitReachedException.class, c::services);
        assertEquals(Duration.ofSeconds(120), e.retryAfter());
        assertNull(e.resetAt());
    }

    @Test
    void shareUnavailable() {
        srv.failStatus = 403;
        srv.failBody = "{\"error\":{\"code\":\"share_paused\",\"message\":\"the owner paused this share\"}}";
        srv.failHeaders = Map.of("X-Acnologia-Share", "paused");
        ShareUnavailableException e = assertThrows(ShareUnavailableException.class, c::services);
        assertInstanceOf(ForbiddenException.class, e);
        assertEquals("share_paused", e.code());
        assertEquals("paused", e.shareStatus());

        ApiException noHeader = ApiException.fromResponse(403,
                "{\"error\":{\"code\":\"share_revoked\",\"message\":\"gone\"}}".getBytes());
        assertEquals("revoked", ((ShareUnavailableException) noHeader).shareStatus());
    }

    @Test
    void genericCodesStayOnApiException() {
        srv.failStatus = 503;
        srv.failBody = "{\"error\":{\"code\":\"no_route\",\"message\":\"no route\"},\"retry_after_sec\":5}";
        ApiException e = assertThrows(ApiException.class, c::capabilities);
        assertEquals("no_route", e.code());
        assertEquals(Duration.ofSeconds(5), e.retryAfter());
    }
}
