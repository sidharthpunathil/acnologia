package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;

import java.io.IOException;
import java.net.URI;
import java.net.URLDecoder;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.ExecutionException;
import java.util.function.Function;

/**
 * Client for an Acnologia key pool, authenticated with an app token
 * ({@code acn_...}; tokens with the older {@code cog_} prefix still work).
 *
 * <pre>{@code
 * Acnologia acn = Acnologia.fromEnv(); // ACNOLOGIA_URL, ACNOLOGIA_TOKEN
 * String body = acn.withKey("serpapi", lease -> {
 *     HttpRequest req = lease.apply(HttpRequest.newBuilder(
 *         URI.create(lease.url("/search.json", Map.of("q", "coffee")))).build());
 *     HttpResponse<String> res = http.send(req, BodyHandlers.ofString());
 *     if (res.statusCode() == 429) throw new RateLimitedException(Duration.ofHours(1));
 *     return res.body();
 * });
 * }</pre>
 *
 * <p>Instances are thread safe. Every blocking method has an {@code Async}
 * twin that returns a {@link CompletableFuture}. {@link #toString()} never
 * includes the token.
 */
public final class Acnologia {
    /** The SDK version. */
    public static final String VERSION = "0.1.0";

    /**
     * Default timeout for API calls (services, lease, report, renew). It is
     * longer than {@link #MAX_WAIT} so a lease that waits for a slot can still
     * succeed. Proxy calls are not bounded by it.
     */
    public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(75);

    /** The longest lease wait the server accepts. */
    public static final Duration MAX_WAIT = Duration.ofSeconds(60);

    static final ObjectMapper JSON = new ObjectMapper();
    static final int MAX_REPORT_MESSAGE = 500;

    private final String baseUrl;
    private final String token;
    private final HttpClient http;
    private final HttpClient proxyHttp;
    private final String userAgent;
    private final Duration timeout;

    private Acnologia(Builder b) {
        String url = b.baseUrl;
        String tok = b.token;
        if (url == null || url.isEmpty()) {
            url = b.env.get("ACNOLOGIA_URL");
        }
        if (tok == null || tok.isEmpty()) {
            tok = b.env.get("ACNOLOGIA_TOKEN");
        }
        if (url == null || url.isEmpty()) {
            throw new IllegalArgumentException("acnologia: base URL is required (or set ACNOLOGIA_URL)");
        }
        if (tok == null || tok.isEmpty()) {
            throw new IllegalArgumentException("acnologia: app token is required (or set ACNOLOGIA_TOKEN)");
        }
        this.baseUrl = trimRight(url);
        this.token = tok;
        this.userAgent = b.userAgent == null ? "acnologia-java/" + VERSION : b.userAgent;
        this.timeout = b.timeout == null ? DEFAULT_TIMEOUT : b.timeout;
        HttpClient given = b.httpClient;
        this.http = given != null ? given : defaultClient();
        // The proxy must never follow redirects, so the app token is not replayed to another host.
        this.proxyHttp = this.http.followRedirects() == HttpClient.Redirect.NEVER ? this.http : defaultClient();
    }

    private static HttpClient defaultClient() {
        return HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(10))
                .followRedirects(HttpClient.Redirect.NEVER)
                .build();
    }

    /**
     * Creates a client.
     *
     * @param baseUrl the Acnologia server, for example {@code http://localhost:8080}
     * @param token   the app token, {@code acn_...}
     * @return the client
     */
    public static Acnologia create(String baseUrl, String token) {
        return builder().baseUrl(baseUrl).token(token).build();
    }

    /**
     * Creates a client from the {@code ACNOLOGIA_URL} and
     * {@code ACNOLOGIA_TOKEN} environment variables.
     *
     * @return the client
     * @throws IllegalArgumentException when either variable is missing
     */
    public static Acnologia fromEnv() {
        return builder().build();
    }

    /** @return a builder; unset URL and token fall back to the environment */
    public static Builder builder() {
        return new Builder();
    }

    /** @return the server base URL, without a trailing slash */
    public String baseUrl() {
        return baseUrl;
    }

    /** @return the User-Agent sent with every request */
    public String userAgent() {
        return userAgent;
    }

    @Override
    public String toString() {
        return "Acnologia{baseUrl=" + baseUrl + ", token=[redacted]}";
    }

    // Services

    /**
     * Lists the services this app token may use.
     *
     * @return the services
     */
    public List<Service> services() {
        return parseServices(call("GET", "/api/v1/services", null));
    }

    /**
     * Async form of {@link #services()}.
     *
     * @return the services
     */
    public CompletableFuture<List<Service>> servicesAsync() {
        return callAsync("GET", "/api/v1/services", null).thenApply(Acnologia::parseServices);
    }

    private static List<Service> parseServices(JsonNode root) {
        List<Service> out = new ArrayList<>();
        for (JsonNode n : root.path("services")) {
            out.add(Service.fromJson(n));
        }
        return Collections.unmodifiableList(out);
    }

    // Leases

    /**
     * Leases a key with the server defaults.
     *
     * @param service the service slug
     * @return the lease; always report it
     * @see #lease(String, LeaseOptions)
     */
    public Lease lease(String service) {
        return lease(service, LeaseOptions.DEFAULT);
    }

    /**
     * Asks the pool for a key. Always {@link Lease#report} the lease: on
     * services with a concurrency limit it holds one of the key's slots until
     * it is reported or its TTL expires.
     *
     * @param service the service slug
     * @param options exclusions, TTL, wait and exclusive
     * @return the lease
     * @throws NoKeyAvailableException when every key is cooling down, exhausted or excluded
     * @throws AllKeysBusyException    when every usable key is at its concurrency limit and the wait ran out
     * @throws IllegalArgumentException when the wait is negative or over {@link #MAX_WAIT}
     */
    public Lease lease(String service, LeaseOptions options) {
        return Lease.fromJson(this, call("POST", "/api/v1/lease", leaseBody(service, options)));
    }

    /**
     * Async form of {@link #lease(String)}.
     *
     * @param service the service slug
     * @return the lease
     */
    public CompletableFuture<Lease> leaseAsync(String service) {
        return leaseAsync(service, LeaseOptions.DEFAULT);
    }

    /**
     * Async form of {@link #lease(String, LeaseOptions)}.
     *
     * @param service the service slug
     * @param options exclusions, TTL, wait and exclusive
     * @return the lease
     */
    public CompletableFuture<Lease> leaseAsync(String service, LeaseOptions options) {
        ObjectNode body;
        try {
            body = leaseBody(service, options);
        } catch (RuntimeException e) {
            return CompletableFuture.failedFuture(e);
        }
        return callAsync("POST", "/api/v1/lease", body).thenApply(n -> Lease.fromJson(this, n));
    }

    private static ObjectNode leaseBody(String service, LeaseOptions o) {
        ObjectNode body = JSON.createObjectNode();
        body.put("service", service);
        if (o == null) {
            return body;
        }
        if (!o.excludeKeyIds().isEmpty()) {
            ArrayNode ids = body.putArray("exclude_key_ids");
            o.excludeKeyIds().forEach(ids::add);
        }
        if (!o.ttl().isZero() && !o.ttl().isNegative()) {
            body.put("ttl_sec", ceilSeconds(o.ttl()));
        }
        if (o.waitFor().isNegative() || o.waitFor().compareTo(MAX_WAIT) > 0) {
            throw new IllegalArgumentException("acnologia: lease wait must be between 0 and " + MAX_WAIT.getSeconds() + "s");
        }
        if (!o.waitFor().isZero()) {
            body.put("wait_sec", ceilSeconds(o.waitFor()));
        }
        if (o.exclusive()) {
            body.put("exclusive", true);
        }
        return body;
    }

    // withKey

    /**
     * Leases a key for {@code service} and calls {@code fn} with it, with
     * the default options (five attempts).
     *
     * @param service the service slug
     * @param fn      your call to the provider
     * @param <T>     the result type
     * @param <E>     the checked exception {@code fn} may throw
     * @return what {@code fn} returned
     * @throws E whatever {@code fn} threw, when it is not a rotation exception
     * @see #withKey(String, WithKeyOptions, LeaseFunction)
     */
    public <T, E extends Exception> T withKey(String service, LeaseFunction<T, E> fn) throws E {
        return withKey(service, WithKeyOptions.DEFAULT, fn);
    }

    /**
     * Leases a key for {@code service} and calls {@code fn} with it.
     *
     * <ul>
     *   <li>{@code fn} returns: the lease is reported {@code ok} and its result
     *       is returned. A failed report is thrown.</li>
     *   <li>{@code fn} throws {@link RateLimitedException},
     *       {@link QuotaExhaustedException} or {@link InvalidKeyException}
     *       (directly or as a cause): the outcome is reported with rotate on
     *       and {@code fn} runs again with the next key, up to
     *       {@code maxAttempts}. When attempts run out, the last one is
     *       reported without rotate and its exception is thrown.</li>
     *   <li>{@code fn} throws anything else: the lease is reported
     *       {@code error} and the exception is thrown unchanged.</li>
     * </ul>
     *
     * <p>When the pool has no further key, {@link NoKeyAvailableException} is
     * thrown ({@link AllKeysBusyException} when every usable key is at its
     * concurrency limit). Every lease is reported, even when {@code fn} throws
     * an {@link Error}. Report messages are built from the exception with the
     * secret scrubbed out.
     *
     * @param service the service slug
     * @param options attempts and lease options
     * @param fn      your call to the provider
     * @param <T>     the result type
     * @param <E>     the checked exception {@code fn} may throw
     * @return what {@code fn} returned
     * @throws E whatever {@code fn} threw, when it is not a rotation exception
     */
    public <T, E extends Exception> T withKey(String service, WithKeyOptions options, LeaseFunction<T, E> fn) throws E {
        int max = options.maxAttempts();
        LeaseOptions first = options.leaseOptions();
        List<Long> tried = new ArrayList<>(first.excludeKeyIds());
        Lease lease = lease(service, first);
        Throwable last = null;
        for (int attempt = 1; attempt <= max; attempt++) {
            T result;
            try {
                result = fn.apply(lease);
            } catch (Throwable t) {
                KeyRotationException rot = rotationCause(t);
                if (rot == null) {
                    reportQuietly(lease, Report.error(scrubMessage(t.toString(), lease.secret())));
                    throw Acnologia.<RuntimeException>sneaky(t);
                }
                last = t;
                tried.add(lease.keyId());
                Report.Builder report = rot.toReport(lease.secret());
                if (attempt == max) {
                    reportQuietly(lease, report.build());
                    break;
                }
                Optional<Lease> next = lease.report(report.rotate(true).build());
                lease = next.isPresent() ? next.get() : lease(service, later(first, tried));
                continue;
            }
            lease.report(Report.ok());
            return result;
        }
        throw Acnologia.<RuntimeException>sneaky(last);
    }

    /**
     * Async form of {@link #withKey(String, LeaseFunction)}.
     *
     * @param service the service slug
     * @param fn      returns a stage with your result
     * @param <T>     the result type
     * @return the result of {@code fn}
     */
    public <T> CompletableFuture<T> withKeyAsync(String service, Function<Lease, ? extends CompletionStage<T>> fn) {
        return withKeyAsync(service, WithKeyOptions.DEFAULT, fn);
    }

    /**
     * Async form of {@link #withKey(String, WithKeyOptions, LeaseFunction)},
     * with the same rules: a stage that fails with a rotation exception (or
     * one caused by it) rotates, anything else is reported as {@code error}
     * and passed on.
     *
     * @param service the service slug
     * @param options attempts and lease options
     * @param fn      returns a stage with your result
     * @param <T>     the result type
     * @return the result of {@code fn}
     */
    public <T> CompletableFuture<T> withKeyAsync(String service, WithKeyOptions options,
                                                 Function<Lease, ? extends CompletionStage<T>> fn) {
        LeaseOptions first = options.leaseOptions();
        List<Long> tried = Collections.synchronizedList(new ArrayList<>(first.excludeKeyIds()));
        return leaseAsync(service, first).thenCompose(l -> attemptAsync(service, options, fn, l, 1, tried));
    }

    private <T> CompletableFuture<T> attemptAsync(String service, WithKeyOptions options,
                                                  Function<Lease, ? extends CompletionStage<T>> fn, Lease lease,
                                                  int attempt, List<Long> tried) {
        CompletableFuture<T> call;
        try {
            call = fn.apply(lease).toCompletableFuture();
        } catch (Throwable t) {
            call = CompletableFuture.failedFuture(t);
        }
        return call.handle((value, err) -> {
            if (err == null) {
                return lease.reportAsync(Report.ok()).thenApply(x -> value);
            }
            Throwable t = unwrap(err);
            KeyRotationException rot = rotationCause(t);
            if (rot == null) {
                return reportQuietlyAsync(lease, Report.error(scrubMessage(t.toString(), lease.secret())))
                        .thenCompose(x -> CompletableFuture.<T>failedFuture(t));
            }
            tried.add(lease.keyId());
            Report.Builder report = rot.toReport(lease.secret());
            if (attempt >= options.maxAttempts()) {
                return reportQuietlyAsync(lease, report.build()).thenCompose(x -> CompletableFuture.<T>failedFuture(t));
            }
            return lease.reportAsync(report.rotate(true).build())
                    .thenCompose(next -> next.isPresent()
                            ? CompletableFuture.completedFuture(next.get())
                            : leaseAsync(service, later(options.leaseOptions(), tried)))
                    .thenCompose(nl -> attemptAsync(service, options, fn, nl, attempt + 1, tried));
        }).thenCompose(Function.identity());
    }

    private static LeaseOptions later(LeaseOptions first, List<Long> tried) {
        List<Long> ids;
        synchronized (tried) {
            ids = new ArrayList<>(tried);
        }
        return first.toBuilder().excludeKeyIds(ids).build();
    }

    private static void reportQuietly(Lease lease, Report report) {
        try {
            lease.report(report);
        } catch (RuntimeException ignored) {
            // keep the caller's error
        }
    }

    private static CompletableFuture<Void> reportQuietlyAsync(Lease lease, Report report) {
        try {
            return lease.reportAsync(report).handle((x, e) -> null);
        } catch (RuntimeException e) {
            return CompletableFuture.completedFuture(null);
        }
    }

    static KeyRotationException rotationCause(Throwable t) {
        for (int depth = 0; t != null && depth < 32; depth++, t = t.getCause()) {
            if (t instanceof KeyRotationException) {
                return (KeyRotationException) t;
            }
        }
        return null;
    }

    private static Throwable unwrap(Throwable t) {
        while ((t instanceof CompletionException || t instanceof ExecutionException) && t.getCause() != null) {
            t = t.getCause();
        }
        return t;
    }

    @SuppressWarnings("unchecked")
    private static <X extends Throwable> RuntimeException sneaky(Throwable t) throws X {
        throw (X) t;
    }

    // Proxy

    /**
     * Returns a helper whose requests go to {@code /proxy/{service}/...}.
     * Acnologia injects a key, retries with the next key on rate limits, and
     * never returns the secret.
     *
     * @param service the service (or service group) slug
     * @return the proxy helper
     */
    public Proxy proxy(String service) {
        return new Proxy(this, service);
    }

    HttpClient proxyHttp() {
        return proxyHttp;
    }

    void setHeaders(HttpRequest.Builder b) {
        b.setHeader("Authorization", "Bearer " + token);
        if (!userAgent.isEmpty()) {
            b.setHeader("User-Agent", userAgent);
        }
    }

    // Transport

    private HttpRequest buildRequest(String method, String path, JsonNode body) {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(baseUrl + path));
        setHeaders(b);
        b.setHeader("Accept", "application/json");
        if (timeout != null && !timeout.isZero() && !timeout.isNegative()) {
            b.timeout(timeout);
        }
        if (body != null) {
            b.setHeader("Content-Type", "application/json");
            try {
                b.method(method, HttpRequest.BodyPublishers.ofByteArray(JSON.writeValueAsBytes(body)));
            } catch (IOException e) {
                throw new AcnologiaException("acnologia: encode request: " + e.getMessage(), e);
            }
        } else {
            b.method(method, HttpRequest.BodyPublishers.noBody());
        }
        return b.build();
    }

    JsonNode call(String method, String path, JsonNode body) {
        HttpRequest req = buildRequest(method, path, body);
        HttpResponse<byte[]> res;
        try {
            res = http.send(req, HttpResponse.BodyHandlers.ofByteArray());
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: " + method + " " + path + ": " + e, e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AcnologiaException("acnologia: interrupted", e);
        }
        return handle(res);
    }

    CompletableFuture<JsonNode> callAsync(String method, String path, JsonNode body) {
        HttpRequest req;
        try {
            req = buildRequest(method, path, body);
        } catch (RuntimeException e) {
            return CompletableFuture.failedFuture(e);
        }
        return http.sendAsync(req, HttpResponse.BodyHandlers.ofByteArray())
                .handle((res, err) -> {
                    if (err != null) {
                        Throwable t = unwrap(err);
                        if (t instanceof AcnologiaException) {
                            throw (AcnologiaException) t;
                        }
                        throw new AcnologiaException("acnologia: " + method + " " + path + ": " + t, t);
                    }
                    return handle(res);
                });
    }

    private static JsonNode handle(HttpResponse<byte[]> res) {
        int status = res.statusCode();
        byte[] data = res.body();
        if (status < 200 || status > 299) {
            throw ApiException.fromResponse(status, data);
        }
        if (data == null || data.length == 0) {
            return JSON.createObjectNode();
        }
        try {
            JsonNode n = JSON.readTree(data);
            return n == null ? JSON.createObjectNode() : n;
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: decode response: " + e.getMessage(), e);
        }
    }

    // Helpers

    static long ceilSeconds(Duration d) {
        long secs = d.getSeconds();
        return d.getNano() > 0 ? secs + 1 : secs;
    }

    static String trimRight(String s) {
        int end = s.length();
        while (end > 0 && s.charAt(end - 1) == '/') {
            end--;
        }
        return s.substring(0, end);
    }

    static String trimLeft(String s) {
        if (s == null) {
            return "";
        }
        int i = 0;
        while (i < s.length() && s.charAt(i) == '/') {
            i++;
        }
        return s.substring(i);
    }

    static String queryEscape(String s) {
        return URLEncoder.encode(s, StandardCharsets.UTF_8);
    }

    static String queryDecode(String s) {
        try {
            return URLDecoder.decode(s, StandardCharsets.UTF_8);
        } catch (IllegalArgumentException e) {
            return s;
        }
    }

    /** Percent encodes one path segment (RFC 3986 unreserved characters stay). */
    static String pathEscape(String s) {
        StringBuilder out = new StringBuilder();
        for (byte b : s.getBytes(StandardCharsets.UTF_8)) {
            char c = (char) (b & 0xff);
            if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
                    || c == '-' || c == '.' || c == '_' || c == '~') {
                out.append(c);
            } else {
                out.append('%').append(String.format("%02X", b & 0xff));
            }
        }
        return out.toString();
    }

    static String encodeQuery(Map<String, ?> query) {
        if (query == null || query.isEmpty()) {
            return "";
        }
        StringBuilder q = new StringBuilder();
        for (Map.Entry<String, ?> e : query.entrySet()) {
            Object v = e.getValue();
            if (v == null) {
                continue;
            }
            Iterable<?> values = v instanceof Iterable ? (Iterable<?>) v : List.of(v);
            for (Object one : values) {
                if (q.length() > 0) {
                    q.append('&');
                }
                q.append(queryEscape(e.getKey())).append('=').append(queryEscape(String.valueOf(one)));
            }
        }
        return q.toString();
    }

    /**
     * Removes the secret (raw and query escaped) from {@code message} and
     * truncates it to 500 characters. Errors from query auth services often
     * carry the full request URL, key included, and a report must not copy it
     * to the server.
     *
     * @param message the text to clean, may be null
     * @param secret  the lease secret
     * @return the cleaned text
     */
    public static String scrubMessage(String message, String secret) {
        String out = message == null ? "" : message;
        if (secret != null && !secret.isEmpty()) {
            out = out.replace(secret, "[key]");
            out = out.replace(queryEscape(secret), "[key]");
            out = out.replace(pathEscape(secret), "[key]");
        }
        if (out.length() > MAX_REPORT_MESSAGE) {
            int end = MAX_REPORT_MESSAGE;
            if (Character.isHighSurrogate(out.charAt(end - 1))) {
                end--;
            }
            out = out.substring(0, end);
        }
        return out;
    }

    /** Builds an {@link Acnologia} client. */
    public static final class Builder {
        private String baseUrl;
        private String token;
        private HttpClient httpClient;
        private String userAgent;
        private Duration timeout;
        private Map<String, String> env = System.getenv();

        private Builder() {
        }

        /**
         * @param baseUrl the Acnologia server, for example {@code http://localhost:8080}.
         *                Defaults to {@code ACNOLOGIA_URL}.
         * @return this builder
         */
        public Builder baseUrl(String baseUrl) {
            this.baseUrl = baseUrl;
            return this;
        }

        /**
         * @param token the app token, {@code acn_...}. Defaults to {@code ACNOLOGIA_TOKEN}.
         * @return this builder
         */
        public Builder token(String token) {
            this.token = token;
            return this;
        }

        /**
         * Sets the HTTP client. The proxy helper uses it only when it does not
         * follow redirects; otherwise the proxy uses its own client that never
         * follows them.
         *
         * @param httpClient the client
         * @return this builder
         */
        public Builder httpClient(HttpClient httpClient) {
            this.httpClient = httpClient;
            return this;
        }

        /**
         * @param userAgent overrides {@code acnologia-java/<version>}
         * @return this builder
         */
        public Builder userAgent(String userAgent) {
            this.userAgent = userAgent;
            return this;
        }

        /**
         * Timeout for API calls; zero disables it. Default
         * {@link Acnologia#DEFAULT_TIMEOUT}. Keep it longer than any lease wait.
         *
         * @param timeout the timeout
         * @return this builder
         */
        public Builder timeout(Duration timeout) {
            this.timeout = timeout;
            return this;
        }

        Builder env(Map<String, String> env) {
            this.env = env;
            return this;
        }

        /**
         * @return the client
         * @throws IllegalArgumentException when no URL or token is set or found in the environment
         */
        public Acnologia build() {
            return new Acnologia(this);
        }
    }
}
