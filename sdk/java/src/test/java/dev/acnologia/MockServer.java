package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
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
            if (!"Bearer acn_test".equals(ex.getRequestHeaders().getFirst("Authorization"))) {
                send(ex, 401, "{\"error\":{\"code\":\"unauthenticated\",\"message\":\"bad token\"}}", Map.of());
                return;
            }
            if (path.startsWith("/proxy/")) {
                proxy(ex, method, in);
                return;
            }
            if (failStatus != 0) {
                send(ex, failStatus, failBody, Map.of());
                return;
            }
            if (method.equals("GET") && path.equals("/api/v1/services")) {
                send(ex, 200, "{\"services\":[{\"slug\":\"serpapi\",\"name\":\"SerpAPI\",\"provider\":\"serpapi\","
                        + "\"category\":\"search\",\"available\":true,\"base_url\":\"https://serpapi.com\","
                        + "\"auth\":{\"type\":\"query\",\"name\":\"api_key\"}}]}", Map.of());
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
        send(ex, path.endsWith("/teapot") ? 418 : 200, out.toString(),
                Map.of("X-Acnologia-Key-Id", "7", "X-Acnologia-Attempts", "1", "Content-Type", "application/json"));
    }

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
