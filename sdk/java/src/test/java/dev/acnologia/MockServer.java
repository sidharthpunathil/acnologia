package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.sun.net.httpserver.Headers;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Deque;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** A fake Acnologia server on the JDK's built in HttpServer. */
final class MockServer implements AutoCloseable {
    static final ObjectMapper JSON = new ObjectMapper();

    final HttpServer server;
    final Deque<Long> keys = new ArrayDeque<>();
    final List<JsonNode> leases = Collections.synchronizedList(new ArrayList<>());
    final List<JsonNode> reports = Collections.synchronizedList(new ArrayList<>());
    final List<String> userAgents = Collections.synchronizedList(new ArrayList<>());
    volatile boolean busy;
    /** When set, every /api/v1 call answers with this status and body. */
    volatile int failStatus;
    volatile String failBody;
    volatile Map<String, String> failHeaders = Map.of();
    /** The headers and raw query of the last request. */
    volatile Headers lastHeaders;
    volatile String lastQuery;
    volatile String lastMethod;

    MockServer() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", this::handle);
        server.start();
    }

    String url() {
        return "http://127.0.0.1:" + server.getAddress().getPort();
    }

    Acnologia client() {
        return Acnologia.create(url() + "/", "acn_test");
    }

    MockServer keys(long... ids) {
        for (long id : ids) {
            keys.add(id);
        }
        return this;
    }

    @Override
    public void close() {
        server.stop(0);
    }

    static ObjectNode leaseJson(long id) {
        ObjectNode n = JSON.createObjectNode();
        n.put("lease_id", "l" + id);
        n.put("key_id", id);
        n.put("service", "serpapi");
        n.put("secret", "sk" + id);
        n.putObject("auth").put("type", "query").put("name", "api_key");
        n.put("base_url", "https://serpapi.com");
        n.put("expires_at", "2026-11-01T12:00:00Z");
        return n;
    }

    private void handle(HttpExchange ex) throws IOException {
        try {
            String method = ex.getRequestMethod();
            String path = ex.getRequestURI().getRawPath();
            byte[] in = ex.getRequestBody().readAllBytes();
            userAgents.add(ex.getRequestHeaders().getFirst("User-Agent"));
            lastHeaders = ex.getRequestHeaders();
            lastQuery = ex.getRequestURI().getRawQuery();
            lastMethod = method;
            if (method.equals("GET") && path.equals("/api/v1/proxy/ca.pem")) {
                send(ex, 200, CA_PEM, Map.of("Content-Type", "application/x-pem-file"));
                return;
            }
            if (!"Bearer acn_test".equals(ex.getRequestHeaders().getFirst("Authorization"))) {
                send(ex, 401, "{\"error\":{\"code\":\"unauthenticated\",\"message\":\"bad token\"}}", Map.of());
                return;
            }
            if (path.startsWith("/proxy/")) {
                proxy(ex, method, in);
                return;
            }
            if (failStatus != 0) {
                send(ex, failStatus, failBody, failHeaders);
                return;
            }
            if (method.equals("GET") && path.equals("/api/v1/me")) {
                send(ex, 200, ME_JSON, Map.of());
                return;
            }
            if (method.equals("GET") && path.equals("/api/v1/services/serpapi/guide")) {
                String accept = ex.getRequestHeaders().getFirst("Accept");
                if (accept != null && accept.contains("text/markdown")) {
                    send(ex, 200, "# SerpAPI\n\nSearch Google.", Map.of("Content-Type", "text/markdown"));
                    return;
                }
                send(ex, 200, "{\"markdown\":\"# SerpAPI\",\"generated\":true,\"guide\":{\"endpoints\":[{\"path\":\"/search.json\"}]}}",
                        Map.of());
                return;
            }
            if (method.equals("GET") && path.equals("/api/v1/capabilities")) {
                send(ex, 200, "{\"capabilities\":[{\"slug\":\"web_search\",\"name\":\"Web search\",\"description\":\"Search the web\","
                        + "\"strategy\":\"priority\",\"params\":[{\"name\":\"query\",\"type\":\"string\",\"required\":true}],"
                        + "\"routes\":[{\"service\":\"serpapi\",\"mapped\":true},{\"service\":\"tavily\",\"mapped\":false}],"
                        + "\"url\":\"http://acn.test/proxy/web_search\"}]}", Map.of());
                return;
            }
            if (method.equals("GET") && path.equals("/api/v1/proxy")) {
                send(ex, 200, FORWARD_JSON, Map.of());
                return;
            }
            if (method.equals("GET") && path.equals("/api/v1/services")) {
                send(ex, 200, "{\"services\":[{\"slug\":\"serpapi\",\"name\":\"SerpAPI\",\"provider\":\"serpapi\","
                        + "\"category\":\"search\",\"available\":true,\"base_url\":\"https://serpapi.com\","
                        + "\"auth\":{\"type\":\"query\",\"name\":\"api_key\"},"
                        + "\"guide_url\":\"http://acn.test/api/v1/services/serpapi/guide\"}]}", Map.of());
                return;
            }
            if (method.equals("POST") && path.equals("/api/v1/lease")) {
                leases.add(JSON.readTree(in));
                if (busy) {
                    send(ex, 503, "{\"error\":{\"code\":\"all_keys_busy\",\"message\":\"every key is at its limit\"},"
                            + "\"retry_after_sec\":1}", Map.of());
                    return;
                }
                Long id;
                synchronized (keys) {
                    id = keys.poll();
                }
                if (id == null) {
                    send(ex, 503, "{\"error\":{\"code\":\"no_key_available\",\"message\":\"all keys cooling\"},"
                            + "\"retry_after_sec\":42}", Map.of());
                    return;
                }
                send(ex, 200, leaseJson(id).toString(), Map.of());
                return;
            }
            if (method.equals("POST") && path.matches("/api/v1/leases/[^/]+/report")) {
                ObjectNode body = (ObjectNode) JSON.readTree(in);
                body.put("_lease_id", path.split("/")[4]);
                reports.add(body);
                ObjectNode out = JSON.createObjectNode().put("ok", true);
                Long id = null;
                if (body.path("rotate").asBoolean(false)) {
                    synchronized (keys) {
                        id = keys.poll();
                    }
                }
                if (id != null) {
                    out.set("next", leaseJson(id));
                } else {
                    out.putNull("next");
                }
                send(ex, 200, out.toString(), Map.of());
                return;
            }
            if (method.equals("POST") && path.matches("/api/v1/leases/[^/]+/renew")) {
                JsonNode body = JSON.readTree(in);
                if (path.contains("gone")) {
                    send(ex, 404, "{\"error\":{\"code\":\"lease_not_found\",\"message\":\"lease not found\"}}", Map.of());
                    return;
                }
                send(ex, 200, "{\"ok\":true,\"expires_at\":\"2026-11-01T12:0" + body.path("ttl_sec").asInt(0) % 10
                        + ":00.123456789Z\"}", Map.of());
                return;
            }
            send(ex, 404, "{\"error\":{\"code\":\"not_found\",\"message\":\"no route\"}}", Map.of());
        } catch (Exception e) {
            send(ex, 500, "{\"error\":{\"code\":\"test\",\"message\":\"" + e + "\"}}", Map.of());
        }
    }

    private void proxy(HttpExchange ex, String method, byte[] in) throws IOException {
        String path = ex.getRequestURI().getRawPath();
        if (path.endsWith("/redirect")) {
            send(ex, 302, "", Map.of("Location", "http://example.invalid/steal"));
            return;
        }
        ObjectNode out = JSON.createObjectNode();
        out.put("method", method);
        out.put("path", path);
        out.put("query", ex.getRequestURI().getRawQuery());
        out.put("body", new String(in, StandardCharsets.UTF_8));
        out.put("ct", ex.getRequestHeaders().getFirst("Content-Type"));
        out.put("custom", ex.getRequestHeaders().getFirst("X-Custom"));
        out.put("idem", ex.getRequestHeaders().getFirst("Idempotency-Key"));
        out.put("cache_control", ex.getRequestHeaders().getFirst("Cache-Control"));
        Map<String, String> h = new HashMap<>();
        h.put("X-Acnologia-Key-Id", "7");
        h.put("X-Acnologia-Attempts", "2");
        h.put("X-Acnologia-Service", "serpapi");
        h.put("X-Acnologia-Cache", "miss");
        h.put("Content-Type", "application/json");
        if (ex.getRequestHeaders().getFirst("Idempotency-Key") != null) {
            h.put("Idempotent-Replayed", "true");
        }
        if (path.equals("/proxy/web_search")) {
            h.put("X-Acnologia-Capability", "web_search");
            h.put("X-Acnologia-Tried", "tavily=502, serpapi=200");
        }
        send(ex, path.endsWith("/teapot") ? 418 : 200, out.toString(), h);
    }

    static final String CA_PEM = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";

    static final String ME_JSON = "{\"app\":{\"id\":12,\"name\":\"crawler\"},"
            + "\"limits\":{\"daily_requests\":1000,\"monthly_requests\":null,\"daily_budget\":null,\"monthly_budget\":25.5},"
            + "\"usage\":{\"today_requests\":1000,\"today_cost\":0.25,\"month_requests\":4000,\"month_cost\":3.5,"
            + "\"last_used_at\":\"2026-11-01T10:00:00Z\",\"limit_hit\":{\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-02T00:00:00Z\"}},"
            + "\"limit_hit\":{\"limit\":\"daily_requests\",\"reset_at\":\"2026-11-02T00:00:00Z\"},"
            + "\"services\":[{\"slug\":\"serpapi\",\"name\":\"SerpAPI\",\"provider\":\"serpapi\",\"category\":\"search\","
            + "\"available\":1,\"logo_url\":null}],"
            + "\"share\":{\"id\":\"sh1\",\"name\":\"Team\",\"owner\":{\"name\":\"Ana\",\"id\":\"u1\"},\"owner_name\":\"Ana\","
            + "\"owner_id\":\"u1\",\"recipient\":\"bob@example.com\",\"label\":\"bob\",\"status\":\"paused\",\"mode\":\"proxy\","
            + "\"ends_at\":null}}";

    static final String FORWARD_JSON = "{\"proxy_url\":\"http://acn.test:8888\","
            + "\"proxy_url_with_token\":\"http://app:%3Capp%20token%3E@acn.test:8888\","
            + "\"ca_url\":\"http://acn.test/api/v1/proxy/ca.pem\",\"ca_fingerprint\":\"sha256:ab\",\"passthrough\":true,"
            + "\"hosts\":[{\"host\":\"serpapi.com\",\"service\":\"serpapi\",\"name\":\"SerpAPI\",\"path\":\"/\"}],"
            + "\"env\":{\"HTTPS_PROXY\":\"http://app:%3Capp%20token%3E@acn.test:8888\",\"NODE_USE_ENV_PROXY\":\"1\"},"
            + "\"service_header\":\"X-Acnologia-Service\"}";

    private static void send(HttpExchange ex, int status, String body, Map<String, String> headers) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        headers.forEach((k, v) -> ex.getResponseHeaders().set(k, v));
        if (!headers.containsKey("Content-Type")) {
            ex.getResponseHeaders().set("Content-Type", "application/json");
        }
        ex.sendResponseHeaders(status, b.length == 0 ? -1 : b.length);
        if (b.length > 0) {
            try (OutputStream os = ex.getResponseBody()) {
                os.write(b);
            }
        }
        ex.close();
    }
}
