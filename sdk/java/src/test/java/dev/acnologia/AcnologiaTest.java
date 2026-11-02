package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class AcnologiaTest {
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
    void services() {
        List<Service> svcs = c.services();
        assertEquals(1, svcs.size());
        Service s = svcs.get(0);
        assertEquals("serpapi", s.slug());
        assertTrue(s.available());
        assertEquals("https://serpapi.com", s.baseUrl());
        assertEquals("query", s.auth().type());
        assertEquals("api_key", s.auth().name());
        assertEquals("acnologia-java/" + Acnologia.VERSION, srv.userAgents.get(0));
    }

    @Test
    void servicesAsync() {
        assertEquals("serpapi", c.servicesAsync().join().get(0).slug());
    }

    @Test
    void badToken() {
        Acnologia bad = Acnologia.create(srv.url(), "acn_wrong");
        UnauthenticatedException e = assertThrows(UnauthenticatedException.class, bad::services);
        assertEquals(401, e.statusCode());
        assertEquals("unauthenticated", e.code());
        assertEquals("bad token", e.errorMessage());
    }

    @Test
    void forbidden() {
        srv.failStatus = 403;
        srv.failBody = "{\"error\":{\"code\":\"service_not_allowed\",\"message\":\"this app cannot use service x\"}}";
        ForbiddenException e = assertThrows(ForbiddenException.class, () -> c.lease("x"));
        assertEquals("service_not_allowed", e.code());
    }

    @Test
    void genericApiError() {
        srv.failStatus = 409;
        srv.failBody = "{\"error\":{\"code\":\"report_failed\",\"message\":\"lease already reported\"}}";
        ApiException e = assertThrows(ApiException.class, () -> c.lease("serpapi"));
        assertEquals(ApiException.class, e.getClass());
        assertEquals(409, e.statusCode());
        assertEquals("report_failed", e.code());
        assertEquals("acnologia: http 409: report_failed: lease already reported", e.getMessage());
    }

    @Test
    void nonJsonErrorUsesBody() {
        srv.failStatus = 502;
        srv.failBody = "bad gateway";
        ApiException e = assertThrows(ApiException.class, c::services);
        assertEquals("", e.code());
        assertEquals("bad gateway", e.errorMessage());
    }

    @Test
    void serviceUnavailable() {
        srv.failStatus = 503;
        srv.failBody = "{\"error\":{\"code\":\"service_unavailable\",\"message\":\"paused\"},\"retry_after_sec\":30}";
        ServiceUnavailableException e = assertThrows(ServiceUnavailableException.class, c::services);
        assertEquals(Duration.ofSeconds(30), e.retryAfter());
    }

    @Test
    void leaseAndReport() {
        srv.keys(1, 2);
        Lease l = c.lease("serpapi", LeaseOptions.builder().excludeKeyIds(9L).ttl(Duration.ofMillis(1500))
                .exclusive(true).build());
        assertEquals("l1", l.leaseId());
        assertEquals(1, l.keyId());
        assertEquals("sk1", l.secret());
        assertEquals(Instant.parse("2026-11-01T12:00:00Z"), l.expiresAt());
        JsonNode body = srv.leases.get(0);
        assertEquals("serpapi", body.path("service").asText());
        assertEquals(9, body.path("exclude_key_ids").get(0).asLong());
        assertEquals(2, body.path("ttl_sec").asInt());
        assertTrue(body.path("exclusive").asBoolean());
        assertFalse(body.has("wait_sec"));

        Optional<Lease> next = l.report(Report.builder(Outcome.RATE_LIMITED).statusCode(429)
                .retryAfter(Duration.ofMillis(2500)).message("slow down").rotate(true).build());
        assertTrue(next.isPresent());
        assertEquals(2, next.get().keyId());
        JsonNode r = srv.reports.get(0);
        assertEquals("l1", r.path("_lease_id").asText());
        assertEquals("rate_limited", r.path("outcome").asText());
        assertEquals(429, r.path("status_code").asInt());
        assertEquals(3, r.path("retry_after_sec").asInt());
        assertEquals("slow down", r.path("message").asText());
        assertTrue(r.path("rotate").asBoolean());

        assertTrue(next.get().report(Report.ok()).isEmpty());
        JsonNode ok = srv.reports.get(1);
        assertEquals("ok", ok.path("outcome").asText());
        assertFalse(ok.has("rotate"));
        assertFalse(ok.has("status_code"));
    }

    @Test
    void leaseAsyncAndReportAsync() {
        srv.keys(1);
        Lease l = c.leaseAsync("serpapi").join();
        assertEquals(1, l.keyId());
        assertTrue(l.reportAsync(Report.ok()).join().isEmpty());
    }

    @Test
    void renew() {
        srv.keys(1);
        Lease l = c.lease("serpapi");
        Instant exp = l.renew(Duration.ofSeconds(3));
        assertEquals(Instant.parse("2026-11-01T12:03:00.123456789Z"), exp);
        assertEquals(exp, l.renewAsync(Duration.ofSeconds(3)).join());
    }

    @Test
    void renewGone() {
        Lease gone = new Lease(c, "gone", 1, "serpapi", "s", new Auth("query", "api_key", ""), "", null, false);
        ApiException e = assertThrows(ApiException.class, () -> gone.renew(Duration.ZERO));
        assertEquals(404, e.statusCode());
        assertEquals("lease_not_found", e.code());
    }

    @Test
    void noKeyAvailable() {
        NoKeyAvailableException e = assertThrows(NoKeyAvailableException.class, () -> c.lease("serpapi"));
        assertEquals(NoKeyAvailableException.class, e.getClass());
        assertEquals(Duration.ofSeconds(42), e.retryAfter());
        assertEquals("no_key_available", e.code());
        assertFalse(e.isBusy());
    }

    @Test
    void noKeyAvailableAsync() {
        CompletionException e = assertThrows(CompletionException.class, () -> c.leaseAsync("serpapi").join());
        assertInstanceOf(NoKeyAvailableException.class, e.getCause());
    }

    @Test
    void allKeysBusy() {
        srv.busy = true;
        AllKeysBusyException e = assertThrows(AllKeysBusyException.class,
                () -> c.lease("serpapi", LeaseOptions.builder().waitFor(Duration.ofMillis(10_500)).build()));
        assertTrue(e.isBusy());
        assertEquals(Duration.ofSeconds(1), e.retryAfter());
        assertEquals(11, srv.leases.get(0).path("wait_sec").asInt());
    }

    @Test
    void waitIsValidated() {
        assertThrows(IllegalArgumentException.class,
                () -> c.lease("serpapi", LeaseOptions.builder().waitFor(Duration.ofSeconds(61)).build()));
        assertThrows(IllegalArgumentException.class,
                () -> c.lease("serpapi", LeaseOptions.builder().waitFor(Duration.ofSeconds(-1)).build()));
        assertTrue(srv.leases.isEmpty());
    }

    private Lease lease(String type, String name, String prefix) {
        return new Lease(c, "l", 1, "svc", "s3cr3t", new Auth(type, name, prefix), "https://api.example.com/", null, false);
    }

    @Test
    void applyPlacesTheKey() {
        HttpRequest base = HttpRequest.newBuilder(URI.create("https://api.example.com/v1/x?q=a+b&api_key=old")).build();

        HttpRequest q = lease("query", "api_key", "").apply(base);
        assertEquals("https://api.example.com/v1/x?q=a+b&api_key=s3cr3t", q.uri().toString());

        HttpRequest h = lease("header", "xi-api-key", "").apply(base);
        assertEquals("s3cr3t", h.headers().firstValue("xi-api-key").orElseThrow());

        HttpRequest b = lease("bearer", "", "").apply(base);
        assertEquals("Bearer s3cr3t", b.headers().firstValue("Authorization").orElseThrow());

        HttpRequest t = lease("bearer", "", "Token ").apply(base);
        assertEquals("Token s3cr3t", t.headers().firstValue("Authorization").orElseThrow());

        HttpRequest hp = lease("header", "X-Key", "Key ").apply(base);
        assertEquals("Key s3cr3t", hp.headers().firstValue("X-Key").orElseThrow());

        Map<String, String> headers = new HashMap<>();
        Map<String, String> query = new HashMap<>();
        lease("query", "key", "").apply(headers, query);
        assertEquals(Map.of("key", "s3cr3t"), query);
        assertTrue(headers.isEmpty());

        assertEquals("https://api.example.com/search.json?q=coffee",
                lease("query", "api_key", "").url("search.json", Map.of("q", "coffee")));
    }

    @Test
    void leaseParsesAuthPrefix() {
        Lease l = Lease.fromJson(c, MockServer.leaseJson(3).set("auth",
                MockServer.JSON.createObjectNode().put("type", "bearer").put("name", "").put("prefix", "Token ")));
        assertEquals("Token ", l.auth().prefix());
    }

    @Test
    void withKeySuccess() {
        srv.keys(1);
        String out = c.withKey("serpapi", l -> "used " + l.secret());
        assertEquals("used sk1", out);
        assertEquals(1, srv.reports.size());
        assertEquals("ok", srv.reports.get(0).path("outcome").asText());
    }

    @Test
    void withKeyRotates() {
        srv.keys(1, 2, 3);
        AtomicInteger calls = new AtomicInteger();
        String out = c.withKey("serpapi", l -> {
            int n = calls.incrementAndGet();
            if (n == 1) {
                throw new RateLimitedException(Duration.ofHours(1), 429, "too many");
            }
            if (n == 2) {
                throw new IllegalStateException("wrapped", new QuotaExhaustedException(402, null));
            }
            return l.secret();
        });
        assertEquals("sk3", out);
        assertEquals(3, srv.reports.size());
        JsonNode r0 = srv.reports.get(0);
        assertEquals("rate_limited", r0.path("outcome").asText());
        assertEquals(3600, r0.path("retry_after_sec").asInt());
        assertEquals(429, r0.path("status_code").asInt());
        assertEquals("too many", r0.path("message").asText());
        assertTrue(r0.path("rotate").asBoolean());
        assertEquals("quota_exhausted", srv.reports.get(1).path("outcome").asText());
        assertEquals("ok", srv.reports.get(2).path("outcome").asText());
        assertEquals(1, srv.leases.size());
    }

    @Test
    void withKeyFallsBackToLease() {
        srv.keys(1);
        AtomicInteger calls = new AtomicInteger();
        // The report finds no next key, so withKey leases again excluding key 1; the pool is empty.
        NoKeyAvailableException e = assertThrows(NoKeyAvailableException.class, () -> c.withKey("serpapi", l -> {
            calls.incrementAndGet();
            throw new InvalidKeyException(401, "bad");
        }));
        assertEquals(1, calls.get());
        assertEquals(2, srv.leases.size());
        assertEquals(1, srv.leases.get(1).path("exclude_key_ids").get(0).asLong());
        assertEquals(42, e.retryAfter().getSeconds());
    }

    @Test
    void withKeyFallbackLeaseKeepsOptions() {
        srv.keys(1);
        WithKeyOptions opts = WithKeyOptions.builder().leaseOptions(LeaseOptions.builder().excludeKeyIds(7L).build())
                .ttl(Duration.ofSeconds(30)).waitFor(Duration.ofSeconds(5)).build();
        assertThrows(NoKeyAvailableException.class, () -> c.withKey("serpapi", opts, l -> {
            throw new InvalidKeyException();
        }));
        JsonNode second = srv.leases.get(1);
        assertEquals("[7,1]", second.path("exclude_key_ids").toString());
        assertEquals(30, second.path("ttl_sec").asInt());
        assertEquals(5, second.path("wait_sec").asInt());
    }

    @Test
    void withKeyMaxAttempts() {
        srv.keys(1, 2, 3, 4);
        AtomicInteger calls = new AtomicInteger();
        RateLimitedException e = assertThrows(RateLimitedException.class,
                () -> c.withKey("serpapi", WithKeyOptions.builder().maxAttempts(2).build(), l -> {
                    calls.incrementAndGet();
                    throw new RateLimitedException(Duration.ofSeconds(5));
                }));
        assertEquals(Duration.ofSeconds(5), e.retryAfter());
        assertEquals(2, calls.get());
        assertEquals(2, srv.reports.size());
        assertTrue(srv.reports.get(0).path("rotate").asBoolean());
        assertFalse(srv.reports.get(1).has("rotate"));
    }

    @Test
    void withKeyOtherErrorIsRethrownUnchanged() {
        srv.keys(1, 2);
        IOException boom = new IOException("boom");
        IOException e = assertThrows(IOException.class, () -> c.<String, IOException>withKey("serpapi", l -> {
            throw boom;
        }));
        assertSame(boom, e);
        assertEquals(1, srv.reports.size());
        assertEquals("error", srv.reports.get(0).path("outcome").asText());
        assertFalse(srv.reports.get(0).has("rotate"));
        assertTrue(srv.reports.get(0).path("message").asText().contains("boom"));
    }

    @Test
    void withKeyReportsOnError() {
        srv.keys(1);
        assertThrows(AssertionError.class, () -> c.withKey("serpapi", l -> {
            throw new AssertionError("fatal");
        }));
        assertEquals("error", srv.reports.get(0).path("outcome").asText());
    }

    @Test
    void withKeyScrubsSecret() {
        srv.keys(1);
        assertThrows(RuntimeException.class, () -> c.withKey("serpapi", l -> {
            throw new RuntimeException("GET https://serpapi.com/search?api_key=" + l.secret() + " failed");
        }));
        String msg = srv.reports.get(0).path("message").asText();
        assertFalse(msg.contains("sk1"), msg);
        assertTrue(msg.contains("api_key=[key]"), msg);
    }

    @Test
    void scrubMessage() {
        assertEquals("a [key] b [key]", Acnologia.scrubMessage("a k+y/= b k%2By%2F%3D", "k+y/="));
        assertEquals(500, Acnologia.scrubMessage("x".repeat(600), "s").length());
        assertEquals("", Acnologia.scrubMessage(null, "s"));
    }

    @Test
    void withKeyAsyncRotates() {
        srv.keys(1, 2);
        AtomicInteger calls = new AtomicInteger();
        String out = c.withKeyAsync("serpapi", l -> {
            if (calls.incrementAndGet() == 1) {
                return CompletableFuture.failedFuture(new RateLimitedException(Duration.ofSeconds(1)));
            }
            return CompletableFuture.completedFuture(l.secret());
        }).join();
        assertEquals("sk2", out);
        assertEquals("rate_limited", srv.reports.get(0).path("outcome").asText());
        assertEquals("ok", srv.reports.get(1).path("outcome").asText());
    }

    @Test
    void withKeyAsyncOtherErrorAndMaxAttempts() {
        srv.keys(1, 2, 3);
        IllegalStateException boom = new IllegalStateException("boom");
        CompletionException e = assertThrows(CompletionException.class,
                () -> c.withKeyAsync("serpapi", l -> CompletableFuture.<String>failedFuture(boom)).join());
        assertSame(boom, e.getCause());
        assertEquals("error", srv.reports.get(0).path("outcome").asText());

        CompletionException e2 = assertThrows(CompletionException.class,
                () -> c.withKeyAsync("serpapi", WithKeyOptions.builder().maxAttempts(2).build(), l -> {
                    throw new InvalidKeyException();
                }).join());
        assertInstanceOf(InvalidKeyException.class, e2.getCause());
        assertEquals(3, srv.reports.size());
        assertFalse(srv.reports.get(2).has("rotate"));
    }

    @Test
    void proxyGet() throws IOException {
        HttpResponse<byte[]> res = c.proxy("serpapi").get("/search.json", Map.of("q", "coffee beans"));
        assertEquals(200, res.statusCode());
        assertEquals("7", res.headers().firstValue("X-Acnologia-Key-Id").orElseThrow());
        JsonNode body = json(res.body());
        assertEquals("GET", body.path("method").asText());
        assertEquals("/proxy/serpapi/search.json", body.path("path").asText());
        assertEquals("q=coffee+beans", body.path("query").asText());
    }

    @Test
    void proxyPostJsonAndNon2xx() throws IOException {
        HttpResponse<byte[]> res = c.proxy("elevenlabs").postJson("v1/tts", Map.of("text", "hi"));
        JsonNode body = json(res.body());
        assertEquals("POST", body.path("method").asText());
        assertEquals("application/json", body.path("ct").asText());
        assertEquals("{\"text\":\"hi\"}", body.path("body").asText());

        HttpResponse<byte[]> tea = c.proxy("x").request("DELETE", "/teapot", null, Map.of("X-Custom", "1"), null);
        assertEquals(418, tea.statusCode());
        assertEquals("1", json(tea.body()).path("custom").asText());

        HttpResponse<byte[]> async = c.proxy("elevenlabs").postJsonAsync("/v1/tts", Map.of("a", 1)).join();
        assertEquals("{\"a\":1}", json(async.body()).path("body").asText());
    }

    @Test
    void proxySendRewritesRequest() throws IOException {
        HttpRequest req = HttpRequest.newBuilder(URI.create("https://api.elevenlabs.io/v1/voices?page=2"))
                .header("X-Custom", "yes").header("Authorization", "Bearer provider").GET().build();
        HttpResponse<byte[]> res = c.proxy("elevenlabs").send(req, HttpResponse.BodyHandlers.ofByteArray());
        JsonNode body = json(res.body());
        assertEquals("/proxy/elevenlabs/v1/voices", body.path("path").asText());
        assertEquals("page=2", body.path("query").asText());
        assertEquals("yes", body.path("custom").asText());

        HttpRequest already = HttpRequest.newBuilder(URI.create(c.proxy("elevenlabs").url("/v1/models"))).build();
        JsonNode b2 = json(c.proxy("elevenlabs").sendAsync(already, HttpResponse.BodyHandlers.ofByteArray()).join().body());
        assertEquals("/proxy/elevenlabs/v1/models", b2.path("path").asText());
    }

    @Test
    void proxyDoesNotFollowRedirects() {
        HttpResponse<byte[]> res = c.proxy("serpapi").get("/redirect");
        assertEquals(302, res.statusCode());
    }

    @Test
    void toStringRedactsSecrets() {
        assertFalse(c.toString().contains("acn_test"), c.toString());
        Lease l = lease("query", "api_key", "");
        assertFalse(l.toString().contains("s3cr3t"), l.toString());
        assertTrue(l.toString().contains("[redacted]"));
    }

    @Test
    void fromEnv() {
        Acnologia e = Acnologia.builder().env(Map.of("ACNOLOGIA_URL", srv.url() + "//", "ACNOLOGIA_TOKEN", "acn_test")).build();
        assertEquals(srv.url(), e.baseUrl());
        assertEquals(1, e.services().size());
        IllegalArgumentException ex = assertThrows(IllegalArgumentException.class,
                () -> Acnologia.builder().env(Map.of()).build());
        assertTrue(ex.getMessage().contains("ACNOLOGIA_URL"));
        assertThrows(IllegalArgumentException.class,
                () -> Acnologia.builder().env(Map.of("ACNOLOGIA_URL", "http://x")).build());
    }

    @Test
    void userAgentOverride() {
        Acnologia ua = Acnologia.builder().baseUrl(srv.url()).token("acn_test").userAgent("my-app/1").build();
        ua.services();
        assertEquals("my-app/1", srv.userAgents.get(0));
        assertNull(ApiException.fromResponse(500, null).getCause());
    }
}
