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
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
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
    public static final String VERSION = "0.2.0";

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
    private final String accessPassword;

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
        String access = b.accessPassword;
        if (access == null || access.isEmpty()) {
            access = b.env.get("ACNOLOGIA_ACCESS_PASSWORD");
        }
        this.accessPassword = access == null ? "" : access;
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

    // Me

    /**
     * Tells this token about its own access: the app, its limits and use,
     * the services it may call and, for a share's token, the share. A share
     * token whose share stopped still gets an answer, with the share's status.
     *
     * @return the token's access
     */
    public Me me() {
        return Me.fromJson(call("GET", "/api/v1/me", null));
    }

    /**
     * Async form of {@link #me()}.
     *
     * @return the token's access
     */
    public CompletableFuture<Me> meAsync() {
        return callAsync("GET", "/api/v1/me", null).thenApply(Me::fromJson);
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

    /**
     * Fetches a service's usage guide: endpoints, parameters, limits and
     * errors, as Markdown and as structured JSON.
     *
     * @param service the service slug
     * @return the guide
     * @throws ForbiddenException {@code service_not_allowed} when the token may not use the service
     */
    public Guide guide(String service) {
        return Guide.fromJson(call("GET", guidePath(service), null));
    }

    /**
     * Async form of {@link #guide(String)}.
     *
     * @param service the service slug
     * @return the guide
     */
    public CompletableFuture<Guide> guideAsync(String service) {
        return callAsync("GET", guidePath(service), null).thenApply(Guide::fromJson);
    }

    /**
     * Fetches a service's usage guide as Markdown only
     * ({@code Accept: text/markdown}).
     *
     * @param service the service slug
     * @return the Markdown
     */
    public String guideMarkdown(String service) {
        return callText(guidePath(service), "text/markdown", true);
    }

    /**
     * Async form of {@link #guideMarkdown(String)}.
     *
     * @param service the service slug
     * @return the Markdown
     */
    public CompletableFuture<String> guideMarkdownAsync(String service) {
        return callTextAsync(guidePath(service), "text/markdown", true);
    }

    private static String guidePath(String service) {
        return "/api/v1/services/" + pathEscape(service) + "/guide";
    }

    private static List<Service> parseServices(JsonNode root) {
        List<Service> out = new ArrayList<>();
        for (JsonNode n : root.path("services")) {
            out.add(Service.fromJson(n));
        }
        return Collections.unmodifiableList(out);
    }

    // Capabilities

    /**
     * Lists the capabilities this app may call: one call each, served by
     * whichever provider can. Empty for a share's token.
     *
     * @return the capabilities
     */
    public List<Capability> capabilities() {
        return parseCapabilities(call("GET", "/api/v1/capabilities", null));
    }

    /**
     * Async form of {@link #capabilities()}.
     *
     * @return the capabilities
     */
    public CompletableFuture<List<Capability>> capabilitiesAsync() {
        return callAsync("GET", "/api/v1/capabilities", null).thenApply(Acnologia::parseCapabilities);
    }

    private static List<Capability> parseCapabilities(JsonNode root) {
        List<Capability> out = new ArrayList<>();
        for (JsonNode n : root.path("capabilities")) {
            out.add(Capability.fromJson(n));
        }
        return Collections.unmodifiableList(out);
    }

    /**
     * Calls a capability with the default options.
     *
     * @param slug   the capability slug
     * @param params the params, any value Jackson can write; null sends {@code {}}
     * @return the answer; a non-2xx status is not an exception
     * @see #capability(String, Object, CapabilityOptions)
     */
    public ProxyResponse<byte[]> capability(String slug, Object params) {
        return capability(slug, params, CapabilityOptions.DEFAULT);
    }

    /**
     * Calls a capability: {@code POST /proxy/{slug}} with {@code params} as
     * the JSON body. Like the proxy, the answer comes back as it is (use
     * {@link ApiException#fromResponse(HttpResponse)} on an Acnologia error),
     * and {@link ProxyResponse#tried()} lists the routes tried.
     *
     * @param slug    the capability slug
     * @param params  the params, any value Jackson can write; null sends {@code {}}
     * @param options pin a service, ask for the raw answer, idempotency and cache
     * @return the answer
     */
    public ProxyResponse<byte[]> capability(String slug, Object params, CapabilityOptions options) {
        HttpRequest req = capabilityRequest(slug, params, options);
        try {
            return ProxyResponse.of(proxyHttp.send(req, HttpResponse.BodyHandlers.ofByteArray()));
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: capability " + slug + ": " + e, e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AcnologiaException("acnologia: interrupted", e);
        }
    }

    /**
     * Async form of {@link #capability(String, Object, CapabilityOptions)}.
     *
     * @param slug    the capability slug
     * @param params  the params; null sends {@code {}}
     * @param options the options
     * @return the answer
     */
    public CompletableFuture<ProxyResponse<byte[]>> capabilityAsync(String slug, Object params,
                                                                    CapabilityOptions options) {
        HttpRequest req;
        try {
            req = capabilityRequest(slug, params, options);
        } catch (RuntimeException e) {
            return CompletableFuture.failedFuture(e);
        }
        return proxyHttp.sendAsync(req, HttpResponse.BodyHandlers.ofByteArray()).thenApply(ProxyResponse::of);
    }

    private HttpRequest capabilityRequest(String slug, Object params, CapabilityOptions o) {
        CapabilityOptions opts = o == null ? CapabilityOptions.DEFAULT : o;
        Map<String, Object> query = new LinkedHashMap<>();
        if (opts.service() != null && !opts.service().isEmpty()) {
            query.put("_service", opts.service());
        }
        if (opts.raw()) {
            query.put("_raw", "1");
        }
        String q = encodeQuery(query);
        String url = baseUrl + "/proxy/" + pathEscape(slug) + (q.isEmpty() ? "" : "?" + q);
        byte[] body;
        try {
            body = params == null ? "{}".getBytes(StandardCharsets.UTF_8) : JSON.writeValueAsBytes(params);
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: encode JSON: " + e.getMessage(), e);
        }
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url))
                .POST(HttpRequest.BodyPublishers.ofByteArray(body))
                .setHeader("Content-Type", "application/json");
        if (opts.idempotencyKey() != null && !opts.idempotencyKey().isEmpty()) {
            b.setHeader("Idempotency-Key", opts.idempotencyKey());
        }
        if (opts.noCache()) {
            b.setHeader("Cache-Control", "no-cache");
        }
        setHeaders(b);
        return b.build();
    }

    // Forward proxy

    /**
     * Describes the forward proxy: its address, the CA to trust, the hosts
     * this token may reach and the environment an app needs. Nothing in the
     * answer holds the token.
     *
     * @return the setup
     */
    public ForwardProxy forwardProxy() {
        return ForwardProxy.fromJson(call("GET", "/api/v1/proxy", null));
    }

    /**
     * Async form of {@link #forwardProxy()}.
     *
     * @return the setup
     */
    public CompletableFuture<ForwardProxy> forwardProxyAsync() {
        return callAsync("GET", "/api/v1/proxy", null).thenApply(ForwardProxy::fromJson);
    }

    /**
     * Downloads the forward proxy's CA certificate. The route is public, so
     * no token is sent.
     *
     * @return the PEM text
     */
    public String forwardProxyCa() {
        return callText(CA_PATH, "application/x-pem-file", false);
    }

    /**
     * Async form of {@link #forwardProxyCa()}.
     *
     * @return the PEM text
     */
    public CompletableFuture<String> forwardProxyCaAsync() {
        return callTextAsync(CA_PATH, "application/x-pem-file", false);
    }

    /**
     * Saves the forward proxy's CA certificate to {@code acnologia-ca.pem}.
     *
     * @return the path written
     * @see #saveForwardProxyCa(Path)
     */
    public Path saveForwardProxyCa() {
        return saveForwardProxyCa(Path.of(DEFAULT_CA_PATH));
    }

    /**
     * Downloads the forward proxy's CA certificate and writes it to
     * {@code path}.
     *
     * @param path where to write the PEM
     * @return {@code path}
     */
    public Path saveForwardProxyCa(Path path) {
        try {
            Files.writeString(path, forwardProxyCa(), StandardCharsets.UTF_8);
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: write " + path + ": " + e.getMessage(), e);
        }
        return path;
    }

    /**
     * Returns the forward proxy URL with user {@code app} and this client's
     * token as the password, such as {@code http://app:acn_...@host:8888}.
     * The result contains the token: keep it out of logs.
     *
     * @return the proxy URL with the token
     */
    public String forwardProxyUrl() {
        return proxyUrlWithToken(forwardProxy());
    }

    /**
     * Async form of {@link #forwardProxyUrl()}. The result contains the token.
     *
     * @return the proxy URL with the token
     */
    public CompletableFuture<String> forwardProxyUrlAsync() {
        return forwardProxyAsync().thenApply(this::proxyUrlWithToken);
    }

    /**
     * {@link #forwardProxyEnv(String)} with the CA at {@code acnologia-ca.pem}.
     * The result contains the token.
     *
     * @return the environment
     */
    public Map<String, String> forwardProxyEnv() {
        return forwardProxyEnv(DEFAULT_CA_PATH);
    }

    /**
     * Returns the environment that sends an app's HTTP calls through the
     * forward proxy: {@code HTTPS_PROXY} and {@code HTTP_PROXY} (the
     * {@link #forwardProxyUrl()}), {@code NODE_USE_ENV_PROXY=1}, and
     * {@code NODE_EXTRA_CA_CERTS}, {@code REQUESTS_CA_BUNDLE} and
     * {@code SSL_CERT_FILE} set to {@code caPath}. The proxy URLs contain the
     * token: keep the map out of logs.
     *
     * @param caPath where the CA certificate is (see {@link #saveForwardProxyCa(Path)})
     * @return the environment, in a fixed order
     */
    public Map<String, String> forwardProxyEnv(String caPath) {
        return proxyEnv(forwardProxy(), caPath);
    }

    /**
     * Async form of {@link #forwardProxyEnv(String)}. The result contains the token.
     *
     * @param caPath where the CA certificate is
     * @return the environment
     */
    public CompletableFuture<Map<String, String>> forwardProxyEnvAsync(String caPath) {
        return forwardProxyAsync().thenApply(fp -> proxyEnv(fp, caPath));
    }

    private static final String CA_PATH = "/api/v1/proxy/ca.pem";
    private static final String DEFAULT_CA_PATH = "acnologia-ca.pem";

    private Map<String, String> proxyEnv(ForwardProxy fp, String caPath) {
        String url = proxyUrlWithToken(fp);
        String ca = caPath == null || caPath.isEmpty() ? DEFAULT_CA_PATH : caPath;
        Map<String, String> env = new LinkedHashMap<>();
        env.put("HTTPS_PROXY", url);
        env.put("HTTP_PROXY", url);
        env.put("NODE_USE_ENV_PROXY", "1");
        env.put("NODE_EXTRA_CA_CERTS", ca);
        env.put("REQUESTS_CA_BUNDLE", ca);
        env.put("SSL_CERT_FILE", ca);
        return Collections.unmodifiableMap(env);
    }

    private String proxyUrlWithToken(ForwardProxy fp) {
        String u = fp.proxyUrl();
        int scheme = u.indexOf("://");
        String prefix = scheme < 0 ? "http://" : u.substring(0, scheme + 3);
        String rest = scheme < 0 ? u : u.substring(scheme + 3);
        int slash = rest.indexOf('/');
        int at = rest.lastIndexOf('@', slash < 0 ? rest.length() - 1 : slash);
        if (at >= 0) {
            rest = rest.substring(at + 1);
        }
        return prefix + "app:" + pathEscape(token) + "@" + rest;
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
     * @param service the service, service group or capability slug
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
        setCommonHeaders(b);
    }

    private void setCommonHeaders(HttpRequest.Builder b) {
        if (!userAgent.isEmpty()) {
            b.setHeader("User-Agent", userAgent);
        }
        if (!accessPassword.isEmpty()) {
            b.setHeader("X-Acnologia-Access", accessPassword);
        }
    }

    // Transport

    private HttpRequest buildRequest(String method, String path, JsonNode body) {
        return buildRequest(method, path, body, "application/json", true);
    }

    private HttpRequest buildRequest(String method, String path, JsonNode body, String accept, boolean auth) {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(baseUrl + path));
        if (auth) {
            setHeaders(b);
        } else {
            setCommonHeaders(b);
        }
        b.setHeader("Accept", accept);
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

    private String callText(String path, String accept, boolean auth) {
        HttpRequest req = buildRequest("GET", path, null, accept, auth);
        HttpResponse<byte[]> res;
        try {
            res = http.send(req, HttpResponse.BodyHandlers.ofByteArray());
        } catch (IOException e) {
            throw new AcnologiaException("acnologia: GET " + path + ": " + e, e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AcnologiaException("acnologia: interrupted", e);
        }
        return handleText(res);
    }

    private CompletableFuture<String> callTextAsync(String path, String accept, boolean auth) {
        HttpRequest req;
        try {
            req = buildRequest("GET", path, null, accept, auth);
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
                        throw new AcnologiaException("acnologia: GET " + path + ": " + t, t);
                    }
                    return handleText(res);
                });
    }

    private static String handleText(HttpResponse<byte[]> res) {
        int status = res.statusCode();
        if (status < 200 || status > 299) {
            throw ApiException.fromResponse(status, res.body(), res.headers());
        }
        return res.body() == null ? "" : new String(res.body(), StandardCharsets.UTF_8);
    }

    private static JsonNode handle(HttpResponse<byte[]> res) {
        int status = res.statusCode();
        byte[] data = res.body();
        if (status < 200 || status > 299) {
            throw ApiException.fromResponse(status, data, res.headers());
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
        private String accessPassword;
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

        /**
         * Sends {@code X-Acnologia-Access} on every request, for a server
         * behind the access password. Defaults to
         * {@code ACNOLOGIA_ACCESS_PASSWORD}.
         *
         * @param accessPassword the server's access password
         * @return this builder
         */
        public Builder accessPassword(String accessPassword) {
            this.accessPassword = accessPassword;
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
