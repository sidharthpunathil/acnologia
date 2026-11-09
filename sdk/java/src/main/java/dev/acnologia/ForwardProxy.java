package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * How to use the forward proxy, from {@code GET /api/v1/proxy}. None of these
 * fields holds the app token: {@code proxyUrlWithToken} and {@code env} have
 * an {@code <app token>} placeholder, so {@link #toString()} is safe to log.
 * Use {@link Acnologia#forwardProxyUrl()} or {@link Acnologia#forwardProxyEnv()}
 * for values with the token filled in.
 *
 * @param proxyUrl          the proxy address, such as {@code http://host:8888}
 * @param proxyUrlWithToken the proxy URL with an {@code <app token>} placeholder
 * @param caUrl             where to download the CA certificate
 * @param caFingerprint     the CA certificate's fingerprint
 * @param passthrough       true when HTTPS to other hosts is tunneled as is
 * @param hosts             the service hosts the token may reach
 * @param env               suggested environment, with the placeholder
 * @param serviceHeader     the request header that picks a service or group
 */
public record ForwardProxy(String proxyUrl, String proxyUrlWithToken, String caUrl, String caFingerprint,
                           boolean passthrough, List<Host> hosts, Map<String, String> env, String serviceHeader) {

    /**
     * @param host    the provider host
     * @param service the service slug
     * @param name    the service name
     * @param path    the base path on that host
     */
    public record Host(String host, String service, String name, String path) {
    }

    static ForwardProxy fromJson(JsonNode n) {
        List<Host> hosts = new ArrayList<>();
        for (JsonNode h : n.path("hosts")) {
            hosts.add(new Host(Json.text(h, "host"), Json.text(h, "service"), Json.text(h, "name"),
                    Json.text(h, "path")));
        }
        Map<String, String> env = new LinkedHashMap<>();
        n.path("env").fields().forEachRemaining(e -> env.put(e.getKey(), e.getValue().asText("")));
        return new ForwardProxy(Json.text(n, "proxy_url"), Json.text(n, "proxy_url_with_token"),
                Json.text(n, "ca_url"), Json.text(n, "ca_fingerprint"), n.path("passthrough").asBoolean(false),
                List.copyOf(hosts), java.util.Collections.unmodifiableMap(env), Json.text(n, "service_header"));
    }
}
