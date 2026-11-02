package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;

import java.net.URI;
import java.net.http.HttpRequest;
import java.time.Duration;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;

/**
 * A key handed out by the pool. Report how it went with {@link #report}:
 * on services with a concurrency limit an unreported lease holds one of the
 * key's slots until its TTL runs out.
 *
 * <p>{@link #toString()} never includes the secret.
 */
public final class Lease {
    private final Acnologia client;
    private final String leaseId;
    private final long keyId;
    private final String service;
    private final String secret;
    private final Auth auth;
    private final String baseUrl;
    private final Instant expiresAt;
    private final boolean exclusive;

    Lease(Acnologia client, String leaseId, long keyId, String service, String secret, Auth auth, String baseUrl,
          Instant expiresAt, boolean exclusive) {
        this.client = client;
        this.leaseId = leaseId;
        this.keyId = keyId;
        this.service = service;
        this.secret = secret;
        this.auth = auth;
        this.baseUrl = baseUrl;
        this.expiresAt = expiresAt;
        this.exclusive = exclusive;
    }

    static Lease fromJson(Acnologia client, JsonNode n) {
        if (n == null || !n.isObject()) {
            return null;
        }
        return new Lease(client, n.path("lease_id").asText(""), n.path("key_id").asLong(0),
                n.path("service").asText(""), n.path("secret").asText(""), Auth.fromJson(n.get("auth")),
                n.path("base_url").asText(""), parseTime(n.path("expires_at").asText("")),
                n.path("exclusive").asBoolean(false));
    }

    static Instant parseTime(String s) {
        if (s == null || s.isEmpty()) {
            return null;
        }
        try {
            return OffsetDateTime.parse(s).toInstant();
        } catch (RuntimeException e) {
            return null;
        }
    }

    /** @return the lease id, used to report and renew */
    public String leaseId() {
        return leaseId;
    }

    /** @return the id of the key behind this lease */
    public long keyId() {
        return keyId;
    }

    /** @return the service slug */
    public String service() {
        return service;
    }

    /** @return the provider secret. Do not log it. */
    public String secret() {
        return secret;
    }

    /** @return where the provider expects the key */
    public Auth auth() {
        return auth;
    }

    /** @return the provider base URL */
    public String baseUrl() {
        return baseUrl;
    }

    /** @return when the lease expires, or null if the server did not say */
    public Instant expiresAt() {
        return expiresAt;
    }

    /** @return true when the key is reserved for this lease alone */
    public boolean exclusive() {
        return exclusive;
    }

    /**
     * Sends the outcome of this lease. When {@code report.rotate()} is true
     * and another key is available, the next lease comes back.
     *
     * @param report the outcome
     * @return the next lease, or empty
     * @throws ApiException when the server refuses the report (for example 409 when it was already reported)
     */
    public Optional<Lease> report(Report report) {
        JsonNode out = client.call("POST", reportPath(), reportBody(report));
        return Optional.ofNullable(fromJson(client, out.get("next")));
    }

    /**
     * Async form of {@link #report(Report)}.
     *
     * @param report the outcome
     * @return the next lease, or empty
     */
    public CompletableFuture<Optional<Lease>> reportAsync(Report report) {
        return client.callAsync("POST", reportPath(), reportBody(report))
                .thenApply(out -> Optional.ofNullable(fromJson(client, out.get("next"))));
    }

    /**
     * Extends this open lease so a long job keeps its key.
     *
     * @param ttl the new lifetime from now; zero uses the server default (two minutes)
     * @return the new expiry
     * @throws ApiException 404 {@code lease_not_found} when the lease is no longer open
     */
    public Instant renew(Duration ttl) {
        JsonNode out = client.call("POST", renewPath(), renewBody(ttl));
        return parseTime(out.path("expires_at").asText(""));
    }

    /**
     * Async form of {@link #renew(Duration)}.
     *
     * @param ttl the new lifetime from now
     * @return the new expiry
     */
    public CompletableFuture<Instant> renewAsync(Duration ttl) {
        return client.callAsync("POST", renewPath(), renewBody(ttl))
                .thenApply(out -> parseTime(out.path("expires_at").asText("")));
    }

    private String reportPath() {
        return "/api/v1/leases/" + Acnologia.pathEscape(leaseId) + "/report";
    }

    private String renewPath() {
        return "/api/v1/leases/" + Acnologia.pathEscape(leaseId) + "/renew";
    }

    private static ObjectNode renewBody(Duration ttl) {
        ObjectNode body = Acnologia.JSON.createObjectNode();
        if (ttl != null && !ttl.isZero() && !ttl.isNegative()) {
            body.put("ttl_sec", Acnologia.ceilSeconds(ttl));
        }
        return body;
    }

    private static ObjectNode reportBody(Report r) {
        ObjectNode body = Acnologia.JSON.createObjectNode();
        body.put("outcome", r.outcome().wireValue());
        if (r.statusCode() != 0) {
            body.put("status_code", r.statusCode());
        }
        if (!r.retryAfter().isZero() && !r.retryAfter().isNegative()) {
            body.put("retry_after_sec", Acnologia.ceilSeconds(r.retryAfter()));
        }
        if (!r.message().isEmpty()) {
            body.put("message", r.message());
        }
        if (r.rotate()) {
            body.put("rotate", true);
        }
        return body;
    }

    /** @return the value to send: the prefix and secret, or {@code Bearer <secret>} for bearer */
    private String value() {
        if (auth.type().equals("bearer") && auth.prefix().isEmpty()) {
            return "Bearer " + secret;
        }
        return auth.prefix() + secret;
    }

    private String headerName() {
        return auth.name().isEmpty() ? "Authorization" : auth.name();
    }

    private String queryName() {
        return auth.name().isEmpty() ? "api_key" : auth.name();
    }

    /**
     * Returns a copy of {@code request} with the secret where the provider
     * expects it: a query parameter for {@code query}, a header for
     * {@code header}, or {@code Authorization: Bearer <secret>} for
     * {@code bearer}. A non empty {@code auth.prefix} goes in front of the
     * secret for every type and, for bearer, replaces {@code "Bearer "}.
     * Header names default to {@code Authorization}.
     *
     * @param request the request to the provider
     * @return a new request carrying the key
     */
    public HttpRequest apply(HttpRequest request) {
        HttpRequest.Builder b = HttpRequest.newBuilder(request, (n, v) -> true);
        if (auth.type().equals("query")) {
            b.uri(withQueryParam(request.uri(), queryName(), value()));
        } else {
            b.setHeader(headerName(), value());
        }
        return b.build();
    }

    /**
     * Puts the secret into the given mutable maps, for use with any HTTP
     * client: {@code query} auth adds to {@code query}, the other types add
     * to {@code headers}.
     *
     * @param headers request headers, modified in place
     * @param query   query parameters, modified in place
     */
    public void apply(Map<String, String> headers, Map<String, String> query) {
        if (auth.type().equals("query")) {
            query.put(queryName(), value());
        } else {
            headers.put(headerName(), value());
        }
    }

    /**
     * Joins the provider base URL with {@code path}. The secret is not added;
     * use {@link #apply(HttpRequest)} for that.
     *
     * @param path the provider path, such as {@code /search.json}
     * @return the URL
     */
    public String url(String path) {
        return url(path, null);
    }

    /**
     * Joins the provider base URL with {@code path} and {@code query}. The
     * secret is not added; use {@link #apply(HttpRequest)} for that.
     *
     * @param path  the provider path
     * @param query query parameters, may be null
     * @return the URL
     */
    public String url(String path, Map<String, ?> query) {
        String u = Acnologia.trimRight(baseUrl) + "/" + Acnologia.trimLeft(path);
        String q = Acnologia.encodeQuery(query);
        return q.isEmpty() ? u : u + "?" + q;
    }

    static URI withQueryParam(URI uri, String name, String value) {
        StringBuilder q = new StringBuilder();
        String raw = uri.getRawQuery();
        if (raw != null && !raw.isEmpty()) {
            for (String part : raw.split("&")) {
                if (part.isEmpty()) {
                    continue;
                }
                String key = part.contains("=") ? part.substring(0, part.indexOf('=')) : part;
                if (Acnologia.queryDecode(key).equals(name)) {
                    continue;
                }
                q.append(q.length() == 0 ? "" : "&").append(part);
            }
        }
        q.append(q.length() == 0 ? "" : "&").append(Acnologia.queryEscape(name)).append('=')
                .append(Acnologia.queryEscape(value));
        StringBuilder s = new StringBuilder();
        s.append(uri.getScheme()).append("://").append(uri.getRawAuthority());
        s.append(uri.getRawPath() == null ? "" : uri.getRawPath());
        s.append('?').append(q);
        if (uri.getRawFragment() != null) {
            s.append('#').append(uri.getRawFragment());
        }
        return URI.create(s.toString());
    }

    @Override
    public String toString() {
        return "Lease{leaseId=" + leaseId + ", keyId=" + keyId + ", service=" + service
                + ", secret=[redacted], auth=" + auth + ", baseUrl=" + baseUrl + ", expiresAt=" + expiresAt
                + ", exclusive=" + exclusive + "}";
    }
}
